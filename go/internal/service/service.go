// Package service is the daemon core: policy, paging, verdicts,
// execution. Methods are pure domain logic over typed structs — Run,
// Submit, Pending map 1:1 onto future MCP tools, with the unix-socket
// transport (internal/daemon) and any later MCP adapter as thin shells.
package service

import (
	"bytes"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"os"
	"os/exec"
	"os/user"
	"regexp"
	"strconv"
	"strings"
	"syscall"
	"time"

	"2fado/internal/config"
	"2fado/internal/policy"
	"2fado/internal/protocol"
	"2fado/internal/store"
	"2fado/internal/telegram"
)

var envDeny = regexp.MustCompile(`^(LD_|PYTHON|PERL|RUBYOPT|NODE_|BASH_ENV|ENV=|CDPATH=)`)

const safePath = "/usr/sbin:/usr/bin:/sbin:/bin"

type Service struct {
	Conf      config.Conf
	Policy    policy.Policy
	Store     store.Store
	TG        telegram.Client
	Approvers map[string]bool
	Host      string
	Verdicts  chan telegram.Verdict
}

func New(c config.Conf) Service {
	host, _ := os.Hostname()
	return Service{
		Conf:      c,
		Policy:    policy.Load(c.Policy),
		Store:     store.New(c.StateDir),
		TG:        telegram.New(c.BotToken),
		Approvers: c.ApproverSet(),
		Host:      host,
		Verdicts:  make(chan telegram.Verdict, 64),
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

// Execute runs argv with reconstructed cwd/env as the policy target.
// Callers pass the stored record fields — never wire content.
// Dry-run short-circuits before any spawn: nothing executes, exit 0.
func (s Service) Execute(argv []string, cwd string, env map[string]string) (code int, out string, asUID uint32) {
	if s.Conf.DryRun {
		return 0, "would have run: " + strings.Join(argv, " "), uint32(os.Geteuid())
	}
	uid, err := s.targetUID()
	if err != nil {
		return 1, "2fadod: bad target_user: " + err.Error(), uint32(os.Geteuid())
	}
	cmd := exec.Command(argv[0], argv[1:]...)
	cmd.Dir = cwd
	cmd.Env = flatten(env)
	if uid != uint32(os.Geteuid()) || os.Geteuid() == 0 {
		cmd.SysProcAttr = &syscall.SysProcAttr{
			Credential: &syscall.Credential{Uid: uid, Gid: uid},
			Setsid:     true,
		}
	}
	var b bytes.Buffer
	cmd.Stdout = &b
	cmd.Stderr = &b
	err = cmd.Run()
	if err != nil {
		if ee, ok := err.(*exec.ExitError); ok {
			return ee.ExitCode(), b.String(), uid
		}
		return 1, b.String() + err.Error(), uid
	}
	return 0, b.String(), uid
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
	return s.RunWithCancel(req, uid, nil)
}

// RunWithCancel handles one RunRequest end to end with early cancellation support.
func (s Service) RunWithCancel(req protocol.RunRequest, uid uint32, cancel <-chan struct{}) protocol.RunResult {
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
	_ = s.Store.Save(protocol.PendingRecord{
		Argv:    req.Argv,
		UID:     uid,
		Cwd:     req.Cwd,
		Expires: expires.Unix(),
		Step:    "initial",
	}, rid)
	s.page(rid, req, uid)
	v := s.await(rid, expires, cancel)
	decision := "timeout"
	by := ""
	if v != nil {
		decision = v.Decision
		by = v.By
	}
	if decision == "client_aborted" {
		s.Store.Consume(rid, decision, by)
		s.closeCard(rid, req, uid, decision)
		s.Store.SaveResult(rid, -1, "client_aborted")
		s.Store.Append(protocol.AuditEvent{Ev: "client_aborted", Argv: req.Argv,
			RID: rid, Step: "initial", By: by})
		return protocol.RunResult{Status: "denied", Reason: "client_aborted"}
	}
	s.Store.Append(protocol.AuditEvent{Ev: "verdict", Argv: req.Argv,
		RID: rid, Step: "initial", Decision: decision, By: by})
	if decision != "approve" {
		s.closeCard(rid, req, uid, decision)
		s.Store.SaveResult(rid, -1, "")
		return protocol.RunResult{Status: "denied", Reason: decision}
	}
	if s.needsConfirm(req.Argv) {
		return s.confirm(rid, req, uid, clean, by, cancel)
	}
	s.closeCard(rid, req, uid, decision)
	return s.runApproved(rid, req, uid, clean)
}

func (s Service) needsConfirm(argv []string) bool {
	return s.Conf.ConfirmAll || s.Policy.NeedsConfirm(argv)
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
	_ = s.Store.Save(protocol.PendingRecord{
		Argv:      req.Argv,
		UID:       uid,
		Cwd:       req.Cwd,
		Expires:   expires.Unix(),
		Step:      "confirm",
		ConfirmOf: rid1,
		ChatID:    rec1.ChatID,
		MsgID:     rec1.MsgID,
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
	if !s.Conf.Placeholder() && rec1.ChatID != "" {
		s.TG.Edit(rec1.ChatID, rec1.MsgID, telegram.Card(s.Host, req.Argv,
			uid, req.Cwd, rid, left, "confirm", s.Conf.DryRun),
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
		s.closeCard(rid, req, uid, decision)
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
		s.closeCard(rid, req, uid, "timeout")
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
	s.closeCard(rid, req, uid, decision)
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
	code, out, asUID := s.Execute(req.Argv, req.Cwd, clean)
	s.Store.Append(protocol.AuditEvent{Ev: "exec", Argv: req.Argv,
		RID: rid, AsUID: asUID, Exit: code, Dry: s.Conf.DryRun})
	s.Store.SaveResult(rid, code, out)
	return protocol.RunResult{Status: "allowed", Exit: code, Output: out}
}

// List answers the plugin server's poll: pending, unexpired, undecided.
func (s Service) List() protocol.PendingList {
	items := s.Store.List(time.Now().Unix())
	if items == nil {
		items = []protocol.PendingItem{}
	}
	return protocol.PendingList{Items: items}
}

// Recent answers the plugin's history poll: decided records, newest first.
func (s Service) Recent(limit int) protocol.RecentList {
	items := s.Store.Recent(limit)
	if items == nil {
		items = []protocol.RecentItem{}
	}
	return protocol.RecentList{Items: items}
}

// Status answers a query for the state and outcome of a specific request ID.
func (s Service) Status(id string) protocol.StatusResponse {
	return s.Store.Status(id, time.Now().Unix())
}

// Submit records a local verdict (PoC path; production: SSO-bound UI).
func (s Service) Submit(sub protocol.VerdictSubmit) protocol.VerdictAck {
	if sub.Decision != "approve" && sub.Decision != "deny" {
		return protocol.VerdictAck{Recorded: false}
	}
	return protocol.VerdictAck{
		Recorded: s.Store.Consume(sub.ID, sub.Decision, cleanBy(sub.By)),
	}
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

func (s Service) page(rid string, req protocol.RunRequest, uid uint32) {
	text := telegram.Card(s.Host, req.Argv, uid, req.Cwd, rid,
		int64(s.Conf.Timeout), "", s.Conf.DryRun)
	if s.Conf.Placeholder() {
		fmt.Printf("[pager:stdout] approve: 2fado approve %s\n%s\n", rid, text)
		return
	}
	if mid, err := s.TG.Send(s.chatID(), text, rid); err == nil {
		s.Store.AttachPager(rid, s.chatID(), mid)
	}
}

func (s Service) closeCard(rid string, req protocol.RunRequest, uid uint32, decision string) {
	if s.Conf.Placeholder() {
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
	s.TG.Edit(rec.ChatID, rec.MsgID, telegram.Card(s.Host, req.Argv,
		rec.UID, rec.Cwd, rid, left, decision, s.Conf.DryRun),
		telegram.EmptyButtons())
}

// TelegramPump consumes poll results into the store (atomic: one wins).
func (s Service) TelegramPump() {
	for v := range s.Verdicts {
		s.Store.Consume(v.RID, v.Decision, v.By)
	}
}
