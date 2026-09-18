// Package service is the daemon core: policy, paging, verdicts,
// execution. Methods are pure domain logic over typed structs — Run,
// Submit, Pending map 1:1 onto future MCP tools, with the unix-socket
// transport (internal/daemon) and any later MCP adapter as thin shells.
package service

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"os/exec"
	"os/user"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"2fado/internal/config"
	"2fado/internal/policy"
	"2fado/internal/protocol"
	"2fado/internal/store"
	"2fado/internal/telegram"
)

var envDeny = regexp.MustCompile(`^(LD_|GLIBC_|GCONV_|DYLD_|IFS|BASH_FUNC_|BASHOPTS|SHELLOPTS|HOSTALIASES|PYTHON|PERL|RUBYOPT|NODE_|BASH_ENV|ENV=|CDPATH=)`)

const safePath = "/usr/sbin:/usr/bin:/sbin:/bin"

// escalationEnabled is the internal release-off switch for #54 (Path A):
// false means the daemon executes only as its own user. The uid-switching
// code stays unreachable while this is false. No config value, CLI flag,
// environment variable, or API field may flip it.
//
// #61: in the default build the privilege-switching code is not compiled
// in at all (see escalation_off.go / escalation_on.go); both builds define
// this constant as the shared release gate.

type dynamicState struct {
	mu           sync.RWMutex
	cancelPoll   context.CancelFunc
	botUsername  string
	tgStatus     string // "connected" | "disconnected" | "unconfigured" | "error"
	tgError      string
	botToken     string
	chatID       string
	approvers    []string
	approverSet  map[string]bool
	tgClient     telegram.Client
	notifyTarget string // "telegram" | "paseo" | "both"
}

type Service struct {
	Conf      config.Conf
	Policy    policy.Policy
	Store     store.Store
	TG        telegram.Client
	Approvers map[string]bool
	Host      string
	Verdicts  chan telegram.Verdict
	state     *dynamicState
}

func New(c config.Conf) Service {
	host, _ := os.Hostname()
	tg := telegram.New(c.BotToken)
	appSet := c.ApproverSet()
	st := &dynamicState{
		botToken:     c.BotToken,
		chatID:       c.ChatID,
		approvers:    c.Approvers,
		approverSet:  appSet,
		tgClient:     tg,
		tgStatus:     "unconfigured",
		notifyTarget: config.NormalizeNotificationTarget(c.NotificationTarget),
	}
	if !c.Placeholder() {
		st.tgStatus = "connected"
	}
	return Service{
		Conf:      c,
		Policy:    policy.Load(c.Policy),
		Store:     store.New(c.StateDir),
		TG:        tg,
		Approvers: appSet,
		Host:      host,
		Verdicts:  make(chan telegram.Verdict, 64),
		state:     st,
	}
}

// ScrubEnv drops attacker-controlled variables, resets PATH, and reports
// what it removed for the audit log.
func (s Service) ScrubEnv(env map[string]string) (map[string]string, []string) {
	keep := map[string]bool{"TERM": true, "LANG": true, "LC_ALL": true,
		"LC_MESSAGES": true, "TZ": true}
	for _, k := range s.Policy.EnvKeep {
		keep[k] = true
	}
	clean := map[string]string{}
	var dropped []string
	for k, v := range env {
		if keep[k] && !envDeny.MatchString(k+"=") {
			clean[k] = v
		} else {
			dropped = append(dropped, k)
		}
	}
	clean["PATH"] = safePath
	return clean, dropped
}

func newRID() string {
	var b [8]byte
	if _, err := rand.Read(b[:]); err != nil {
		return fmt.Sprintf("%d", time.Now().UnixNano())
	}
	return hex.EncodeToString(b[:])
}

// targetUID resolves the execution identity: policy target, defaulting to
// root when the daemon is root, self otherwise (dev).
func (s Service) targetUID() (uint32, error) {
	target := s.Policy.Target
	if target == "" {
		if os.Geteuid() == 0 {
			return 0, nil
		}
		return uint32(os.Getuid()), nil
	}
	if n, err := strconv.Atoi(target); err == nil {
		return uint32(n), nil
	}
	u, err := user.Lookup(target)
	if err != nil {
		return 0, err
	}
	n, _ := strconv.Atoi(u.Uid)
	return uint32(n), nil
}

// execIdentity, rootExecAllowed, displayUID and resolveCredential live in
// build-tagged files: escalation_off.go (default public release, no
// privilege-switching symbols) and escalation_on.go (-tags escalation).
// See #61.

// Execute runs argv with reconstructed cwd/env as the policy target.
// Callers pass the stored record fields — never wire content.
// Dry-run short-circuits before any spawn: nothing executes, exit 0.
func (s Service) Execute(argv []string, cwd string, env map[string]string) (code int, out string, asUID uint32) {
	return s.ExecuteWithChallenge(argv, cwd, env, nil)
}

// ExecuteWithChallenge runs argv like Execute while streaming stdout and
// stderr through a challenge-URL scanner. onAuth fires once with the first
// detected WebAuthn/2FA verification URL, if any.
func (s Service) ExecuteWithChallenge(argv []string, cwd string, env map[string]string, onAuth func(string)) (code int, out string, asUID uint32) {
	if s.Conf.DryRun {
		return 0, "would have run: " + strings.Join(argv, " "), uint32(os.Geteuid())
	}
	uid, refusal := s.execIdentity(argv, cwd)
	if refusal != nil {
		return 1, *refusal, uid
	}
	cmd := execCmdContext(context.Background(), argv[0], argv[1:])
	cmd.Dir = cwd
	cmd.Env = flatten(env)
	if attrs, err := s.spawnAttrs(uid); err != nil {
		return 1, "2fadod: bad target_user: " + err.Error(), uint32(os.Geteuid())
	} else if attrs != nil {
		cmd.SysProcAttr = attrs
	}
	maxOut := capBytes(s.Conf.MaxOutputBytes)
	w := newCappedWriter(maxOut)
	out2 := io.Writer(w)
	if onAuth != nil {
		out2 = challengeScanner(w, onAuth)
	}
	cmd.Stdout = out2
	cmd.Stderr = out2
	d, useTimeout := execTimeout(s.Conf.ExecTimeoutSecs)
	timedOut, runErr := runBounded(context.Background(), cmd, d, useTimeout)
	captured := w.String()
	if timedOut {
		return 124, captured + "\n2fadod: execution timeout", uid
	}
	if runErr != nil {
		if ee, ok := runErr.(*exec.ExitError); ok {
			return ee.ExitCode(), captured, uid
		}
		return 1, captured + runErr.Error(), uid
	}
	return 0, captured, uid
}

func flatten(env map[string]string) []string {
	out := make([]string, 0, len(env))
	for k, v := range env {
		out = append(out, k+"="+v)
	}
	return out
}

// // Run handles one RunRequest end to end with optional cancellation.
func (s Service) Run(req protocol.RunRequest, uid uint32) protocol.RunResult {
	return s.RunWithPeer(req, uid, 0)
}

// RunWithPeer handles one RunRequest with the socket peer PID for the
// suspended-execution barrier (0 skips the barrier, preserving plain
// in-process callers and tests).
func (s Service) RunWithPeer(req protocol.RunRequest, uid uint32, peerPID int) protocol.RunResult {
	return s.RunWithPeerCancel(req, uid, peerPID, nil)
}

// RunWithCancel handles one RunRequest end to end with early cancellation support.
func (s Service) RunWithCancel(req protocol.RunRequest, uid uint32, cancel <-chan struct{}) protocol.RunResult {
	return s.RunWithPeerCancel(req, uid, 0, cancel)
}

// RunWithPeerCancel handles one RunRequest end to end with early
// cancellation support and the suspended-execution barrier: the agent
// process group (via peerPID) holds SIGSTOP while pending and resumes
// with SIGCONT on verdict, timeout, or client abort.
func (s Service) RunWithPeerCancel(req protocol.RunRequest, uid uint32, peerPID int, cancel <-chan struct{}) protocol.RunResult {
	clean, dropped := s.ScrubEnv(req.Env)
	tier := s.Policy.Tier(req.Argv)
	s.Store.Append(protocol.AuditEvent{Ev: "request", UID: uid,
		Argv: req.Argv, Cwd: req.Cwd, Tier: tier, EnvDropped: dropped, Step: "initial"})
	switch tier {
	case "allow":
		code, out, asUID := s.Execute(req.Argv, req.Cwd, clean)
		s.Store.Append(protocol.AuditEvent{Ev: "allow", UID: uid,
			Argv: req.Argv, AsUID: asUID, Exit: code, Step: "initial"})
		return protocol.RunResult{Status: "allowed", Exit: code, Output: out}
	case "deny":
		return protocol.RunResult{Status: "denied", Reason: "policy"}
	}
	rid := newRID()
	expires := time.Now().Add(time.Duration(s.Conf.Timeout) * time.Second)
	preview := PreviewImpact(req.Argv, req.Cwd, flatten(clean))
	asUID := s.displayUID()
	_ = s.Store.Save(protocol.PendingRecord{
		Argv:    req.Argv,
		UID:     uid,
		AsUID:   asUID,
		Cwd:     req.Cwd,
		Expires: expires.Unix(),
		Env:     clean,
		Step:    "initial",
		Preview: preview,
		Git:     snapshotGit(req.Cwd),
		FD:      snapshotEntry(req.Argv, req.Cwd),
		Seal:    s.snapshotSeal(rid, req.Argv, req.Cwd),
	}, rid)
	s.page(rid, req, uid)
	if req.Detach {
		go s.runDetached(rid, req, uid, clean, expires, peerPID)
		return protocol.RunResult{Status: "pending", ID: rid}
	}
	pin := s.snapshotSuspend(rid, peerPID, uid)
	defer s.resumeSuspend(rid, pin)
	v := s.await(rid, expires, cancel)
	decision := "timeout"
	by := ""
	if v != nil {
		decision = v.Decision
		by = v.By
	}
	if decision == "client_aborted" {
		s.Store.Consume(rid, decision, by)
		s.closeCard(rid, req, uid, decision, by)
		s.Store.SaveResult(rid, -1, "client_aborted")
		s.Store.Append(protocol.AuditEvent{Ev: "client_aborted", Argv: req.Argv,
			RID: rid, Step: "initial", By: by})
		return protocol.RunResult{Status: "denied", Reason: "client_aborted"}
	}
	s.Store.Append(protocol.AuditEvent{Ev: "verdict", Argv: req.Argv,
		RID: rid, Step: "initial", Decision: decision, By: by})
	if decision != "approve" {
		s.closeCard(rid, req, uid, decision, by)
		s.Store.SaveResult(rid, -1, "")
		return protocol.RunResult{Status: "denied", Reason: decision}
	}
	if s.needsConfirm(req.Argv) {
		return s.confirm(rid, req, uid, clean, by, cancel)
	}
	s.closeCard(rid, req, uid, decision, by)
	return s.runApproved(rid, req, uid, clean)
}

func (s Service) needsConfirm(argv []string) bool {
	return s.Conf.ConfirmAll || s.Policy.NeedsConfirm(argv)
}

// runDetached awaits the verdict in the background for fire-and-forget
// requests, then executes or records the outcome in the store. It owns
// the suspended-execution barrier for its window: the parent returned
// pending immediately, so the hold starts here, not in the caller.
func (s Service) runDetached(rid string, req protocol.RunRequest, uid uint32, clean map[string]string, expires time.Time, peerPID int) {
	pin := s.snapshotSuspend(rid, peerPID, uid)
	defer s.resumeSuspend(rid, pin)
	v := s.await(rid, expires, nil)
	decision := "timeout"
	by := ""
	if v != nil {
		decision = v.Decision
		by = v.By
	}
	s.Store.Append(protocol.AuditEvent{Ev: "verdict", Argv: req.Argv,
		RID: rid, Step: "initial", Decision: decision, By: by})
	if decision != "approve" {
		s.closeCard(rid, req, uid, decision, by)
		s.Store.SaveResult(rid, -1, "")
		return
	}
	if s.needsConfirm(req.Argv) {
		s.confirm(rid, req, uid, clean, by, nil)
		return
	}
	s.closeCard(rid, req, uid, decision, by)
	s.runApproved(rid, req, uid, clean)
}

// confirm chains a second ask onto an approved request: a fresh record
// with fresh buttons on the same card, reusing every seam (one-wins
// consume, timeout, audit).
func (s Service) confirm(rid1 string, req protocol.RunRequest, uid uint32, clean map[string]string, by string, cancel <-chan struct{}) protocol.RunResult {
	rec1, err := s.Store.Load(rid1)
	if err != nil {
		return protocol.RunResult{Status: "denied", Reason: "lost-record"}
	}
	rid := newRID()
	expires := time.Now().Add(time.Duration(s.Conf.Timeout) * time.Second)
	asUID := s.confirmAsUID(rec1.AsUID)
	_ = escalationEnabled
	_ = s.Store.Save(protocol.PendingRecord{
		Argv:      req.Argv,
		UID:       uid,
		AsUID:     asUID,
		Cwd:       req.Cwd,
		Expires:   expires.Unix(),
		Env:       clean,
		Step:      "confirm",
		ConfirmOf: rid1,
		ChatID:    rec1.ChatID,
		MsgID:     rec1.MsgID,
		Preview:   rec1.Preview,
		Git:       snapshotGit(req.Cwd),
		FD:        snapshotEntry(req.Argv, req.Cwd),
		Seal:      s.snapshotSeal(rid, req.Argv, req.Cwd),
		Suspend:   rec1.Suspend,
	}, rid)
	s.Store.Append(protocol.AuditEvent{
		Ev:        "confirm",
		Argv:      req.Argv,
		RID:       rid,
		Step:      "confirm",
		ConfirmOf: rid1,
		By:        by,
	})
	left := int64(s.Conf.Timeout)
	if s.notifyTelegram() && !s.isPlaceholder() && rec1.ChatID != "" {
		s.activeTG().Edit(rec1.ChatID, rec1.MsgID, telegram.Card(s.Host, req.Argv,
			uid, req.Cwd, rid, left, "confirm", by, s.Conf.DryRun, asUID),
			telegram.Buttons(rid))
	} else {
		fmt.Printf("[pager:stdout] CONFIRM %s (confirm of %s) (approve: 2fado approve %s)\n", rid, rid1, rid)
	}
	v := s.await(rid, expires, cancel)
	decision := "confirmation_timeout"
	cby := ""
	if v != nil {
		decision = v.Decision
		cby = v.By
	}
	if decision == "client_aborted" {
		s.Store.Consume(rid, decision, cby)
		s.closeCard(rid, req, uid, decision, cby)
		s.Store.SaveResult(rid, -1, "client_aborted")
		s.Store.SaveResult(rid1, -1, "client_aborted")
		s.Store.Append(protocol.AuditEvent{
			Ev:        "client_aborted",
			Argv:      req.Argv,
			RID:       rid,
			Step:      "confirm",
			ConfirmOf: rid1,
			By:        cby,
		})
		return protocol.RunResult{Status: "denied", Reason: "client_aborted"}
	}
	if decision == "confirmation_timeout" || decision == "timeout" {
		s.Store.Consume(rid, "timeout", "system")
		s.closeCard(rid, req, uid, "timeout", "system")
		s.Store.SaveResult(rid, -1, "confirmation_timeout")
		s.Store.SaveResult(rid1, -1, "confirmation_timeout")
		s.Store.Append(protocol.AuditEvent{
			Ev:        "confirmation_timeout",
			Argv:      req.Argv,
			RID:       rid,
			Step:      "confirm",
			ConfirmOf: rid1,
			By:        "system",
		})
		return protocol.RunResult{Status: "denied", Reason: "confirmation_timeout"}
	}
	s.Store.Append(protocol.AuditEvent{
		Ev:        "verdict",
		Argv:      req.Argv,
		RID:       rid,
		Step:      "confirm",
		ConfirmOf: rid1,
		Decision:  decision,
		By:        cby,
	})
	s.closeCard(rid, req, uid, decision, cby)
	if decision != "approve" {
		s.Store.SaveResult(rid, -1, "")
		s.Store.SaveResult(rid1, -1, "")
		return protocol.RunResult{Status: "denied", Reason: decision}
	}
	res := s.runApproved(rid, req, uid, clean)
	s.Store.SaveResult(rid1, res.Exit, res.Output)
	return res
}

func (s Service) runApproved(rid string, req protocol.RunRequest, uid uint32, clean map[string]string) protocol.RunResult {
	rec, err := s.Store.Load(rid)
	if err != nil || len(rec.Argv) == 0 {
		s.Store.Append(protocol.AuditEvent{Ev: "lost_record", Argv: req.Argv,
			RID: rid, Decision: "block"})
		s.Store.SaveResult(rid, -1, "lost-record: stored request is missing or corrupt")
		return protocol.RunResult{Status: "denied", Reason: "lost-record"}
	}
	storedArgv, storedCwd := rec.Argv, rec.Cwd
	pin, seal := rec.FD, rec.Seal
	if drift := s.checkSealDrift(rid); drift != nil && drift.Drift {
		s.Store.Append(protocol.AuditEvent{Ev: "seal_drift", Argv: storedArgv,
			RID: rid, Decision: "block", Summary: drift.Reason})
		s.Store.SaveResult(rid, -1, "seal-drift: "+drift.Reason)
		return protocol.RunResult{Status: "denied", Reason: "seal-drift: " + drift.Reason}
	}
	execArgv, execPin := storedArgv, pin
	if seal != nil && len(seal.Files) > 0 && !s.Conf.DryRun {
		dir, err := s.materializeSeal(rid)
		if err != nil {
			reason := "sealed artifact for approval is unusable"
			s.Store.Append(protocol.AuditEvent{Ev: "seal_drift", Argv: storedArgv,
				RID: rid, Decision: "block", Summary: reason})
			s.Store.SaveResult(rid, -1, "seal-drift: "+reason)
			return protocol.RunResult{Status: "denied", Reason: "seal-drift: " + reason}
		}
		execArgv = sealedArgv(storedArgv, storedCwd, seal, dir)
		execPin = snapshotEntry(execArgv, storedCwd)
	}
	if drift := s.checkFDDrift(rid); drift != nil && drift.Drift {
		s.Store.Append(protocol.AuditEvent{Ev: "fd_drift", Argv: storedArgv,
			RID: rid, Decision: "block", Summary: drift.Reason})
		s.Store.SaveResult(rid, -1, "fd-drift: "+drift.Reason)
		return protocol.RunResult{Status: "denied", Reason: "fd-drift: " + drift.Reason}
	}
	if drift := s.checkGitDrift(rid, storedCwd); drift != nil && drift.Drift {
		decision := "warn"
		if drift.Block {
			decision = "block"
		}
		s.Store.Append(protocol.AuditEvent{Ev: "git_drift", Argv: storedArgv,
			RID: rid, Decision: decision, Summary: drift.Reason})
		if drift.Block {
			s.Store.SaveResult(rid, -1, "git-drift: "+drift.Reason)
			return protocol.RunResult{Status: "denied", Reason: "git-drift: " + drift.Reason}
		}
		code, out, asUID := s.runPinned(execArgv, storedCwd, clean, execPin, func(authURL string) {
			s.Store.SetAuthURL(rid, authURL)
			s.notifyAuthURL(rid, req, authURL)
		})
		out = "WARNING: " + drift.Reason + ".\n" + out
		s.Store.Append(protocol.AuditEvent{Ev: "exec", Argv: storedArgv,
			RID: rid, AsUID: asUID, Exit: code, Dry: s.Conf.DryRun})
		s.Store.SaveResult(rid, code, out)
		return protocol.RunResult{Status: "allowed", Exit: code, Output: out}
	}
	code, out, asUID := s.runPinned(execArgv, storedCwd, clean, execPin, func(authURL string) {
		s.Store.SetAuthURL(rid, authURL)
		s.notifyAuthURL(rid, req, authURL)
	})
	s.Store.Append(protocol.AuditEvent{Ev: "exec", Argv: storedArgv,
		RID: rid, AsUID: asUID, Exit: code, Dry: s.Conf.DryRun})
	s.Store.SaveResult(rid, code, out)
	return protocol.RunResult{Status: "allowed", Exit: code, Output: out}
}

// notifyAuthURL surfaces an interactive WebAuthn/2FA verification link to
// the operator: on the Telegram card thread when one was posted, stdout
// otherwise. The agent never sees credentials; only the link to the
// registry's own ceremony page.
func (s Service) notifyAuthURL(rid string, req protocol.RunRequest, authURL string) {
	if !s.notifyTelegram() || s.isPlaceholder() {
		fmt.Printf("[pager:stdout] auth required for %s: %s\n", rid, authURL)
		return
	}
	rec, err := s.Store.Load(rid)
	if err != nil || rec.ChatID == "" {
		fmt.Printf("[pager:stdout] auth required for %s: %s\n", rid, authURL)
		return
	}
	s.activeTG().SendWithMarkup(rec.ChatID,
		fmt.Sprintf("🔑 <b>Biometric 2FA required</b> for request <code>%s</code>:\n%s",
			rid, authURL),
		rec.MsgID, telegram.AuthButtons(authURL))
}

// AdoptOrphans re-attaches waiters to petitions persisted on disk that lost
// their in-memory goroutine across a daemon restart. Expired records get a
// timeout result; live ones get a background waiter driving them to completion.
func (s Service) AdoptOrphans() {
	now := time.Now().Unix()
	for _, item := range s.Store.List(now) {
		rid := item.ID
		rec, err := s.Store.Load(rid)
		if err != nil {
			continue
		}
		if s.Store.Verdict(rid) != nil {
			continue
		}
		if rec.Expires <= now {
			req := protocol.RunRequest{Argv: rec.Argv, Cwd: rec.Cwd, Env: map[string]string{}}
			s.closeCard(rid, req, rec.UID, "timeout", "system")
			s.Store.SaveResult(rid, -1, "timeout")
			s.resumeStored(rid)
			continue
		}
		go s.adoptOne(rid, rec)
	}
}

func (s Service) adoptOne(rid string, rec protocol.PendingRecord) {
	req := protocol.RunRequest{Argv: rec.Argv, Cwd: rec.Cwd, Env: rec.Env}
	defer s.resumeStored(rid)
	v := s.await(rid, time.Unix(rec.Expires, 0), nil)
	decision := "timeout"
	by := "system"
	if v != nil {
		decision = v.Decision
		by = v.By
	}
	s.Store.Append(protocol.AuditEvent{Ev: "verdict", Argv: rec.Argv,
		RID: rid, Step: rec.Step, Decision: decision, By: by})
	if decision != "approve" {
		s.closeCard(rid, req, rec.UID, decision, by)
		s.Store.SaveResult(rid, -1, "")
		return
	}
	clean, _ := s.ScrubEnv(req.Env)
	if rec.Step != "confirm" && s.needsConfirm(req.Argv) {
		s.confirm(rid, req, rec.UID, clean, by, nil)
		return
	}
	s.closeCard(rid, req, rec.UID, decision, by)
	s.runApproved(rid, req, rec.UID, clean)
}

// List answers the plugin server's poll: pending, unexpired, undecided.
// A telegram-only target pages nothing into Paseo, so the list stays empty.
// Stored git pins ride along with their live drift so mid-review workspace
// movement is visible before the approval-time gate fires.
func (s Service) List() protocol.PendingList {
	if !s.notifyPaseo() {
		return protocol.PendingList{Items: []protocol.PendingItem{}}
	}
	items := s.Store.List(time.Now().Unix())
	if items == nil {
		items = []protocol.PendingItem{}
	}
	for i := range items {
		rec, err := s.Store.Load(items[i].ID)
		if err != nil {
			continue
		}
		if rec.Git != nil && rec.Git.Present {
			items[i].Git = rec.Git
			if drift := s.checkGitDrift(items[i].ID, rec.Cwd); drift != nil {
				items[i].GitDrift = drift
			}
		}
		if rec.FD != nil && rec.FD.Path != "" {
			items[i].FD = rec.FD
			if drift := s.checkFDDrift(items[i].ID); drift != nil {
				items[i].FDDrift = drift
			}
		}
		if rec.Seal != nil && len(rec.Seal.Files) > 0 {
			items[i].Seal = rec.Seal
			if drift := s.checkSealDrift(items[i].ID); drift != nil {
				items[i].SealDrift = drift
			}
		}
		if rec.Suspend != nil {
			items[i].Suspend = rec.Suspend
		}
	}
	return protocol.PendingList{Items: items}
}

// Recent answers the plugin's history poll: decided records, newest first.
// Drift-blocked runs carry their git_drift marker so history shows why
// the run was refused.
func (s Service) Recent(limit int) protocol.RecentList {
	items := s.Store.Recent(limit)
	if items == nil {
		items = []protocol.RecentItem{}
	}
	for i := range items {
		if items[i].Exit == -1 && strings.HasPrefix(items[i].Output, "git-drift:") {
			items[i].GitDrift = &protocol.GitDrift{Drift: true, Block: true,
				Reason: strings.TrimSpace(strings.TrimPrefix(items[i].Output, "git-drift:"))}
		}
		if items[i].Exit == -1 && strings.HasPrefix(items[i].Output, "fd-drift:") {
			items[i].FDDrift = &protocol.FDDrift{Drift: true, Block: true,
				Reason: strings.TrimSpace(strings.TrimPrefix(items[i].Output, "fd-drift:"))}
		}
		if items[i].Exit == -1 && strings.HasPrefix(items[i].Output, "seal-drift:") {
			items[i].SealDrift = &protocol.SealDrift{Drift: true, Block: true,
				Reason: strings.TrimSpace(strings.TrimPrefix(items[i].Output, "seal-drift:"))}
		}
	}
	return protocol.RecentList{Items: items}
}

// Status answers a query for the state and outcome of a specific request ID.
// Stored git and fd pins ride along with live drift so operators see
// workspace and entrypoint movement; drift-blocked runs read as denied.
func (s Service) Status(id string) protocol.StatusResponse {
	st := s.Store.Status(id, time.Now().Unix())
	if st.Output != "" && strings.HasPrefix(st.Output, "git-drift:") && st.Exit == -1 {
		st.Status = "denied"
		st.Decision = "git-drift"
	}
	if st.Output != "" && strings.HasPrefix(st.Output, "fd-drift:") && st.Exit == -1 {
		st.Status = "denied"
		st.Decision = "fd-drift"
	}
	if st.Output != "" && strings.HasPrefix(st.Output, "seal-drift:") && st.Exit == -1 {
		st.Status = "denied"
		st.Decision = "seal-drift"
	}
	rec, err := s.Store.Load(id)
	if err != nil {
		return st
	}
	if rec.Suspend != nil {
		st.Suspend = rec.Suspend
	}
	if rec.FD != nil && rec.FD.Path != "" {
		st.FD = rec.FD
		if drift := s.checkFDDrift(id); drift != nil {
			st.FDDrift = drift
		}
	}
	if rec.Seal != nil && len(rec.Seal.Files) > 0 {
		st.Seal = rec.Seal
		if drift := s.checkSealDrift(id); drift != nil {
			st.SealDrift = drift
		}
	}
	if rec.Git == nil || !rec.Git.Present {
		return st
	}
	st.Git = rec.Git
	dir := rec.Cwd
	if dir == "" {
		return st
	}
	live := snapshotGit(dir)
	if live == nil {
		return st
	}
	if !live.Present {
		st.GitDrift = &protocol.GitDrift{Drift: true, Block: true,
			Reason: "git worktree at " + dir + " is no longer a git repo"}
		return st
	}
	if drift := s.checkGitDrift(id, dir); drift != nil {
		st.GitDrift = drift
	}
	return st
}

// Submit records a local verdict (PoC path; production: SSO-bound UI).
// Only the daemon's own UID or root may submit; all others are rejected
// and logged.
func (s Service) Submit(sub protocol.VerdictSubmit, callerUID uint32) protocol.VerdictAck {
	if !isAdminUID(callerUID) {
		s.Store.Append(protocol.AuditEvent{Ev: "verdict_unauthorized", By: cleanBy(sub.By),
			Argv: nil, RID: sub.ID, Decision: sub.Decision})
		return protocol.VerdictAck{Error: "unauthorized"}
	}
	if sub.Decision != "approve" && sub.Decision != "deny" {
		return protocol.VerdictAck{Recorded: false}
	}
	return protocol.VerdictAck{
		Recorded: s.Store.Consume(sub.ID, sub.Decision, cleanBy(sub.By)),
	}
}

// isAdminUID reports whether callerUID may issue verdicts or mutate
// policy/config: root, or the UID the daemon itself runs as (local
// operator over the unix socket).
func isAdminUID(callerUID uint32) bool {
	return callerUID == 0 || callerUID == uint32(os.Getuid())
}

// cleanBy keeps attribution honest: a tight charset, capped length,
// fallback to "local". This names the decider; it never authorizes.
func cleanBy(by string) string {
	if by == "" {
		return "local"
	}
	var b strings.Builder
	for _, r := range by {
		if b.Len() >= 64 {
			break
		}
		if r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' ||
			r >= '0' && r <= '9' || r == ':' || r == '_' || r == '-' || r == '@' || r == '.' {
			b.WriteRune(r)
		}
	}
	if b.Len() == 0 {
		return "local"
	}
	return b.String()
}

// await polls the store only: TelegramPump is the single consumer of the
// poll channel, so verdicts cannot be split between two readers.
func (s Service) await(rid string, expires time.Time, cancel <-chan struct{}) *telegram.Verdict {
	ticker := time.NewTicker(200 * time.Millisecond)
	defer ticker.Stop()
	for {
		if v := s.Store.Verdict(rid); v != nil {
			return &telegram.Verdict{RID: rid, Decision: v.Decision, By: v.By}
		}
		if time.Now().After(expires) {
			return nil
		}
		select {
		case <-cancel:
			return &telegram.Verdict{RID: rid, Decision: "client_aborted", By: "client"}
		case <-ticker.C:
		}
	}
}

func (s Service) chatID() string {
	if s.Conf.ChatID != "" {
		return s.Conf.ChatID
	}
	if len(s.Conf.Approvers) > 0 {
		return s.Conf.Approvers[0]
	}
	return ""
}

func (s Service) getState() *dynamicState {
	return s.state
}

// notifyTarget resolves the effective paging target: live-synced state
// first, static config second, both by default.
func (s Service) notifyTarget() string {
	if s.state != nil {
		s.state.mu.RLock()
		target := s.state.notifyTarget
		s.state.mu.RUnlock()
		if target != "" {
			return config.NormalizeNotificationTarget(target)
		}
	}
	return config.NormalizeNotificationTarget(s.Conf.NotificationTarget)
}

// notifyTelegram reports whether new requests page over Telegram.
func (s Service) notifyTelegram() bool { return s.notifyTarget() != "paseo" }

// notifyPaseo reports whether new requests page into Paseo (list poll).
func (s Service) notifyPaseo() bool { return s.notifyTarget() != "telegram" }

func (s Service) isPlaceholder() bool {
	if s.state != nil {
		s.state.mu.RLock()
		tok := s.state.botToken
		s.state.mu.RUnlock()
		return tok == "" || strings.HasPrefix(tok, "__")
	}
	return s.Conf.Placeholder()
}

func (s Service) activeChatID() string {
	if s.state != nil {
		s.state.mu.RLock()
		cid := s.state.chatID
		approvers := s.state.approvers
		s.state.mu.RUnlock()
		if cid != "" {
			return cid
		}
		if len(approvers) > 0 {
			return approvers[0]
		}
	}
	return s.chatID()
}

func (s Service) activeApprovers() map[string]bool {
	if s.state != nil {
		s.state.mu.RLock()
		app := s.state.approverSet
		s.state.mu.RUnlock()
		if app != nil {
			return app
		}
	}
	return s.Approvers
}

func (s Service) activeTG() telegram.Client {
	if s.state != nil {
		s.state.mu.RLock()
		cli := s.state.tgClient
		tok := s.state.botToken
		s.state.mu.RUnlock()
		if tok != "" {
			return cli
		}
	}
	return s.TG
}

func (s Service) page(rid string, req protocol.RunRequest, uid uint32) {
	asUID := s.displayUID()
	if rec, lerr := s.Store.Load(rid); lerr == nil {
		asUID = rec.AsUID
	}
	text := telegram.Card(s.Host, req.Argv, uid, req.Cwd, rid,
		int64(s.Conf.Timeout), "", "", s.Conf.DryRun, asUID)
	if s.notifyTelegram() && !s.isPlaceholder() {
		cid := s.activeChatID()
		tg := s.activeTG()
		if mid, err := tg.Send(cid, text, rid); err == nil {
			s.Store.AttachPager(rid, cid, mid)
			return
		}
	}
	fmt.Printf("[pager:stdout] approve: 2fado approve %s\n%s\n", rid, text)
}

func (s Service) closeCard(rid string, req protocol.RunRequest, uid uint32, decision string, by string) {
	if !s.notifyTelegram() || s.isPlaceholder() {
		return
	}
	rec, err := s.Store.Load(rid)
	if err != nil || rec.ChatID == "" {
		return
	}
	left := rec.Expires - time.Now().Unix()
	if left < 0 {
		left = 0
	}
	s.activeTG().Edit(rec.ChatID, rec.MsgID, telegram.Card(s.Host, req.Argv,
		rec.UID, rec.Cwd, rid, left, decision, by, s.Conf.DryRun, rec.AsUID),
		telegram.EmptyButtons())
}

// StartTelegram begins background polling if bot token is configured.
func (s Service) StartTelegram(ctx context.Context) {
	if s.state == nil {
		return
	}
	s.state.mu.Lock()
	defer s.state.mu.Unlock()
	if s.state.botToken == "" || strings.HasPrefix(s.state.botToken, "__") {
		fmt.Println("2fadod: no BOT_TOKEN configured, pager=stdout; verdicts via `2fado approve|deny <id>`")
		return
	}
	pollCtx, cancel := context.WithCancel(ctx)
	s.state.cancelPoll = cancel
	go s.state.tgClient.Poll(pollCtx, s.Store.GetOffset(), s.state.approverSet, s.Verdicts, s.Store.SetOffset)
}

// TelegramInfo returns current configuration status and metadata.
func (s Service) TelegramInfo() protocol.TelegramInfoResponse {
	if s.state == nil {
		return protocol.TelegramInfoResponse{
			Configured:         false,
			Status:             "unconfigured",
			Approvers:          []string{},
			NotificationTarget: s.notifyTarget(),
		}
	}

	s.state.mu.Lock()
	defer s.state.mu.Unlock()

	approvers := s.state.approvers
	if approvers == nil {
		approvers = []string{}
	}
	target := config.NormalizeNotificationTarget(s.state.notifyTarget)

	if s.state.botToken == "" || strings.HasPrefix(s.state.botToken, "__") {
		return protocol.TelegramInfoResponse{
			Configured:         false,
			Status:             "unconfigured",
			ChatID:             s.state.chatID,
			Approvers:          approvers,
			NotificationTarget: target,
		}
	}

	if s.state.tgStatus == "connected" && s.state.botUsername != "" {
		return protocol.TelegramInfoResponse{
			Configured:         true,
			BotUsername:        s.state.botUsername,
			ChatID:             s.state.chatID,
			Approvers:          approvers,
			Status:             s.state.tgStatus,
			Error:              s.state.tgError,
			NotificationTarget: target,
		}
	}

	cli := s.state.tgClient
	if cli.BaseURL == "" && s.TG.BaseURL != "" {
		cli.BaseURL = s.TG.BaseURL
	}
	username, err := cli.GetMe()
	if err == nil {
		s.state.botUsername = username
		s.state.tgStatus = "connected"
		s.state.tgError = ""
	} else {
		errMsg := err.Error()
		if strings.Contains(errMsg, "dial") || strings.Contains(errMsg, "timeout") || strings.Contains(errMsg, "no such host") {
			s.state.tgStatus = "disconnected"
		} else {
			s.state.tgStatus = "error"
		}
		s.state.tgError = errMsg
	}

	return protocol.TelegramInfoResponse{
		Configured:         true,
		BotUsername:        s.state.botUsername,
		ChatID:             s.state.chatID,
		Approvers:          approvers,
		Status:             s.state.tgStatus,
		Error:              s.state.tgError,
		NotificationTarget: target,
	}
}

// TelegramSetConfig updates and persists Telegram configuration, live-reloading polling.
func (s Service) TelegramSetConfig(req protocol.TelegramSetConfigRequest, callerUID uint32) protocol.TelegramSetConfigResponse {
	if !isAdminUID(callerUID) {
		s.Store.Append(protocol.AuditEvent{Ev: "config_unauthorized"})
		return protocol.TelegramSetConfigResponse{Success: false, Error: "unauthorized"}
	}
	if s.state == nil {
		return protocol.TelegramSetConfigResponse{Success: false, Error: "service uninitialized"}
	}

	st := s.state
	target := s.notifyTarget()
	if strings.TrimSpace(req.NotificationTarget) != "" {
		trimmed := strings.ToLower(strings.TrimSpace(req.NotificationTarget))
		if trimmed != "telegram" && trimmed != "paseo" && trimmed != "both" {
			return protocol.TelegramSetConfigResponse{Success: false, Error: "invalid notification_target (want telegram|paseo|both)"}
		}
		target = trimmed
	}
	var token string
	if req.BotToken != nil {
		token = strings.TrimSpace(*req.BotToken)
	} else {
		st.mu.RLock()
		token = st.botToken
		st.mu.RUnlock()
	}

	st.mu.RLock()
	baseClient := st.tgClient
	st.mu.RUnlock()

	var username string
	var client telegram.Client
	if token != "" && !strings.HasPrefix(token, "__") {
		client = telegram.New(token)
		if baseClient.BaseURL != "" {
			client.BaseURL = baseClient.BaseURL
		} else if s.TG.BaseURL != "" {
			client.BaseURL = s.TG.BaseURL
		}
		var err error
		username, err = client.GetMe()
		if err != nil {
			return protocol.TelegramSetConfigResponse{
				Success: false,
				Error:   fmt.Sprintf("invalid bot token: %v", err),
			}
		}
	}

	approvers := req.Approvers
	if approvers == nil {
		approvers = []string{}
	}
	userCfg := config.UserConfig{
		BotToken:           token,
		ChatID:             strings.TrimSpace(req.ChatID),
		Approvers:          approvers,
		NotificationTarget: target,
	}
	if err := config.SaveUserConfig(userCfg); err != nil {
		return protocol.TelegramSetConfigResponse{
			Success: false,
			Error:   fmt.Sprintf("failed to save config: %v", err),
		}
	}

	s.state.mu.Lock()
	defer s.state.mu.Unlock()

	if s.state.cancelPoll != nil {
		s.state.cancelPoll()
		s.state.cancelPoll = nil
	}

	s.state.botToken = token
	s.state.chatID = userCfg.ChatID
	s.state.approvers = userCfg.Approvers
	s.state.notifyTarget = target
	appSet := map[string]bool{}
	for _, a := range userCfg.Approvers {
		if a = strings.TrimSpace(a); a != "" {
			appSet[a] = true
		}
	}
	s.state.approverSet = appSet
	s.state.tgClient = client
	s.state.botUsername = username

	if token != "" && !strings.HasPrefix(token, "__") {
		s.state.tgStatus = "connected"
		s.state.tgError = ""
		pollCtx, cancel := context.WithCancel(context.Background())
		s.state.cancelPoll = cancel
		go client.Poll(pollCtx, s.Store.GetOffset(), appSet, s.Verdicts, s.Store.SetOffset)
	} else {
		s.state.tgStatus = "unconfigured"
		s.state.tgError = ""
	}

	return protocol.TelegramSetConfigResponse{
		Success:     true,
		BotUsername: username,
	}
}

// TelegramPump consumes poll results into the store (atomic: one wins).
// Ack callbacks land on notify petitions via Store.Ack; approve/deny go
// through the verdict path.
func (s Service) TelegramPump() {
	for v := range s.Verdicts {
		if v.Decision == "ack" {
			if s.Store.Ack(v.RID, cleanBy("telegram:"+v.By)) {
				s.Store.Append(protocol.AuditEvent{Ev: "ack", RID: v.RID, By: cleanBy("telegram:" + v.By)})
				s.closeNotifyCard(v.RID, cleanBy("telegram:"+v.By))
			}
			continue
		}
		if v.Decision == "ask" {
			rec, err := s.Store.Load(v.RID)
			if err != nil || v.Idx < 0 || v.Idx >= len(rec.Options) {
				continue
			}
			s.Store.Select(v.RID, rec.Options[v.Idx], v.Idx, cleanBy("telegram:"+v.By))
			continue
		}
		s.Store.Consume(v.RID, v.Decision, v.By)
	}
}

// PolicyAddRule persists a whitelist/blacklist rule and reloads it live.
func (s *Service) PolicyAddRule(req protocol.PolicyAddRuleRequest, callerUID uint32) protocol.PolicyAddRuleResponse {
	if !isAdminUID(callerUID) {
		s.Store.Append(protocol.AuditEvent{Ev: "policy_unauthorized"})
		return protocol.PolicyAddRuleResponse{Success: false, Error: "unauthorized"}
	}
	p, n, err := policy.AddRule(s.Conf.Policy, req.Target, req.MatchType, req.Pattern)
	if err != nil {
		return protocol.PolicyAddRuleResponse{Success: false, Error: err.Error()}
	}
	s.Policy = p
	return protocol.PolicyAddRuleResponse{Success: true, RulesCount: n}
}

// PreviewImpact analyzes the command argv, cwd, and environment to predict blast radius and risk.
func (s Service) PreviewImpact(argv []string, cwd string, env []string) *protocol.PreviewRecord {
	return PreviewImpact(argv, cwd, env)
}
