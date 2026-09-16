// Package protocol defines every message that crosses a boundary:
// client<->daemon socket, daemon<->Telegram, daemon<->disk. All structs,
// no dynamic typing. The Service methods in internal/service map 1:1 onto
// future MCP tools (run/approve/pending) — that fork stays open without
// this binary knowing what MCP is.
package protocol

import (
	"reflect"
	"strings"
)

// RunRequest is what the client sends: the exact argv plus the caller's
// execution context for environment reconstruction.
type RunRequest struct {
	Argv   []string          `json:"argv"`
	Cwd    string            `json:"cwd"`
	Env    map[string]string `json:"env"`
	Detach bool              `json:"detach,omitempty"`
}

// VerdictSubmit records a human decision. In the PoC any local uid may
// submit; production restricts this to the SSO-bound UI.
// By names the decider (e.g. "paseo:<user>"); the daemon sanitizes it
// and falls back to "local". Attributional, never authoritative: the
// allowlist check lives with the submitter (plugin settings, SSO gate).
type VerdictSubmit struct {
	ID       string `json:"id"`
	Decision string `json:"decision"` // approve | deny
	By       string `json:"by,omitempty"`
}

// ErrorResponse is the transport-level error envelope. The daemon
// writes it when no typed response applies: malformed, oversized, or
// timed-out framing, unknown message kinds, or un-marshalable domain
// replies. Typed clients treat any {"error":...} reply as failure and
// surface the string; they never mistake it for success.
type ErrorResponse struct {
	Error string `json:"error"`
}

// ClientMessage is the socket envelope: exactly one field is set.
type ClientMessage struct {
	Run               *RunRequest               `json:"run,omitempty"`
	Verdict           *VerdictSubmit            `json:"verdict,omitempty"`
	Notify            *NotifyRequest            `json:"notify,omitempty"`
	Ack               *AckSubmit                `json:"ack,omitempty"`
	List              *ListRequest              `json:"list,omitempty"`
	Recent            *RecentRequest            `json:"recent,omitempty"`
	Status            *StatusRequest            `json:"status,omitempty"`
	TelegramInfo      *TelegramInfoRequest      `json:"telegram_info,omitempty"`
	TelegramSetConfig *TelegramSetConfigRequest `json:"telegram_set_config,omitempty"`
	PolicyAddRule     *PolicyAddRuleRequest     `json:"policy_add_rule,omitempty"`
	Version           *VersionRequest           `json:"version,omitempty"`
	Help              *HelpRequest              `json:"help,omitempty"`
}

// NotifyRequest asks the daemon to create a no-exec notify-only petition:
// a human-presence step (2FA link, staged approval, FYI+ack) with no
// command execution. Link is the clickable URL, Summary is short operator
// text, TTLSeconds overrides the default long TTL (0 = default).
type NotifyRequest struct {
	Link       string `json:"link"`
	Summary    string `json:"summary"`
	TTLSeconds int64  `json:"ttl_seconds,omitempty"`
}

// AckSubmit records a non-binding operator acknowledgement on a notify
// petition ("seen + acted"). By names the acker; daemon sanitizes it.
type AckSubmit struct {
	ID string `json:"id"`
	By string `json:"by,omitempty"`
}

// AckResponse answers an AckSubmit.
type AckResponse struct {
	Acked bool   `json:"acked"`
	Error string `json:"error,omitempty"`
}

type VersionRequest struct{}

// HelpRequest asks the daemon to describe its own socket API. Empty Op
// lists every op; a named Op returns that op's request/response shapes.
type HelpRequest struct {
	Op string `json:"op,omitempty"`
}

// FieldShape describes one struct field in wire terms.
type FieldShape struct {
	Name     string `json:"name"`
	JSON     string `json:"json"`
	Type     string `json:"type"`
	Optional bool   `json:"optional"`
}

// OpInfo describes one socket op: envelope key plus request/response types.
type OpInfo struct {
	Op             string       `json:"op"`
	Request        string       `json:"request"`
	Response       string       `json:"response"`
	RequestFields  []FieldShape `json:"request_fields"`
	ResponseFields []FieldShape `json:"response_fields"`
}

// HelpResponse answers a HelpRequest.
type HelpResponse struct {
	Ops []OpInfo `json:"ops"`
}

type VersionResponse struct {
	Version      string `json:"version"`
	GitCommit    string `json:"git_commit"`
	BuildTime    string `json:"build_time"`
	BinarySHA256 string `json:"binary_sha256"`
	PID          int    `json:"pid"`
}

// PolicyAddRuleRequest asks the daemon to persist a whitelist/blacklist rule.
// match_type: "exact" (full argv), "base" ([baseBinary]), "custom" (prefix, "*" = single-arg wildcard).
type PolicyAddRuleRequest struct {
	Target    string   `json:"target"`     // whitelist | blacklist
	MatchType string   `json:"match_type"` // exact | base | custom
	Pattern   []string `json:"pattern"`
}

// PolicyAddRuleResponse answers PolicyAddRuleRequest.
type PolicyAddRuleResponse struct {
	Success    bool   `json:"success"`
	Error      string `json:"error,omitempty"`
	RulesCount int    `json:"rules_count,omitempty"`
}

// TelegramInfoRequest asks for Telegram bot configuration status and recipient metadata.
type TelegramInfoRequest struct{}

// TelegramInfoResponse answers TelegramInfoRequest.
// Status is one of: "connected" | "disconnected" | "unconfigured" | "error".
// NotificationTarget is one of: "telegram" | "paseo" | "both".
type TelegramInfoResponse struct {
	Configured         bool     `json:"configured"`
	BotUsername        string   `json:"bot_username,omitempty"`
	ChatID             string   `json:"chat_id,omitempty"`
	Approvers          []string `json:"approvers"`
	Status             string   `json:"status"`
	Error              string   `json:"error,omitempty"`
	NotificationTarget string   `json:"notification_target"`
}

// TelegramSetConfigRequest sends updated Telegram bot credentials and recipients.
// NotificationTarget selects the paging channels; empty leaves it unchanged.
type TelegramSetConfigRequest struct {
	BotToken           *string  `json:"bot_token,omitempty"`
	ChatID             string   `json:"chat_id,omitempty"`
	Approvers          []string `json:"approvers,omitempty"`
	NotificationTarget string   `json:"notification_target,omitempty"`
}

// TelegramSetConfigResponse answers TelegramSetConfigRequest.
type TelegramSetConfigResponse struct {
	Success     bool   `json:"success"`
	BotUsername string `json:"bot_username,omitempty"`
	Error       string `json:"error,omitempty"`
}

// PreviewRecord holds impact and expansion preview analysis for an approval request.
type PreviewRecord struct {
	ResolvedBinary string   `json:"resolved_binary,omitempty"`
	TargetCwd      string   `json:"target_cwd,omitempty"`
	AffectedCount  int      `json:"affected_count,omitempty"`
	SamplePaths    []string `json:"sample_paths,omitempty"`
	RiskLevel      string   `json:"risk_level,omitempty"` // low | medium | high | critical
	RiskReason     string   `json:"risk_reason,omitempty"`
}

// StatusRequest asks for the state and outcome of a specific request.
type StatusRequest struct {
	ID string `json:"id"`
}

// GitPin freezes the git tree state at petition time (T0) for TOCTOU
// drift detection at approval (T1). Present is false outside git repos;
// callers must degrade gracefully (skip the check, never fail on it).
type GitPin struct {
	Head      string `json:"head,omitempty"`
	Branch    string `json:"branch,omitempty"`
	Porcelain string `json:"porcelain,omitempty"`
	Clean     bool   `json:"clean"`
	Present   bool   `json:"present"`
}

// GitDrift reports a T0-vs-T1 tree comparison on the approval path.
// Block means execution was refused; otherwise the run proceeded with
// a DIRTY WORKSPACE warning attached.
type GitDrift struct {
	Drift  bool   `json:"drift"`
	Reason string `json:"reason,omitempty"`
	Block  bool   `json:"block,omitempty"`
}

// FDPin freezes the entrypoint executable at petition time (T0): the
// resolved path plus a content hash and device/inode identity captured
// from an O_RDONLY|O_CLOEXEC open. At approval (T1) the daemon
// re-opens, re-hashes, and executes via the verified fd (/dev/fd),
// so binary swaps and symlink redirections on the entrypoint refuse
// with FDDrift instead of running attacker bytes. Nil means the
// entrypoint was un-pinnable; callers must degrade gracefully.
type FDPin struct {
	Path        string `json:"path,omitempty"`
	Resolved    string `json:"resolved,omitempty"`
	SHA256      string `json:"sha256,omitempty"`
	Dev         uint64 `json:"dev,omitempty"`
	Ino         uint64 `json:"ino,omitempty"`
	Size        int64  `json:"size,omitempty"`
	Mode        uint32 `json:"mode,omitempty"`
	IsScript    bool   `json:"is_script,omitempty"`
	Interpreter string `json:"interpreter,omitempty"`
	InterpArg   string `json:"interp_arg,omitempty"`
}

// FDDrift reports a T0-vs-T1 entrypoint comparison on the approval path.
// Block is always true: a swapped entrypoint never executes.
type FDDrift struct {
	Drift  bool   `json:"drift"`
	Reason string `json:"reason,omitempty"`
	Block  bool   `json:"block,omitempty"`
}

// SealFile is one frozen input in the artifact manifest: the archive
// name (Rel, slash-separated, cwd-relative), the live source it was
// bundled from (Abs), and its SHA-256 at petition time.
type SealFile struct {
	Rel    string `json:"rel,omitempty"`
	Abs    string `json:"abs,omitempty"`
	SHA256 string `json:"sha256,omitempty"`
	Size   int64  `json:"size,omitempty"`
	Mode   uint32 `json:"mode,omitempty"`
}

// SealPin freezes the petition's mutable inputs at T0: the daemon
// bundles them into an immutable tar.gz (Archive) and binds approval
// to the Root hash over the sorted per-file hashes. Nil (or empty)
// means nothing sealable; callers must degrade gracefully.
type SealPin struct {
	Root    string     `json:"root,omitempty"`
	Files   []SealFile `json:"files,omitempty"`
	Archive string     `json:"archive,omitempty"`
}

// SealDrift reports a T0-vs-T1 artifact comparison on the approval
// path. Block is always true: a mutated input never executes live;
// verified runs execute the rewritten sealed copies instead.
type SealDrift struct {
	Drift  bool   `json:"drift"`
	Reason string `json:"reason,omitempty"`
	Block  bool   `json:"block,omitempty"`
}

// SuspendPin freezes the requesting agent at petition time (T0): the
// daemon identifies the caller's process group via the socket peer PID
// and holds it with SIGSTOP while the operator reviews. SIGCONT on
// verdict, timeout, or client abort resumes it. Best-effort only: a
// stopped group cannot fork new work, but background groups, other
// UIDs, and pre-stop mutations are outside this barrier.
type SuspendPin struct {
	PID       int  `json:"pid,omitempty"`
	PGID      int  `json:"pgid,omitempty"`
	Suspended bool `json:"suspended,omitempty"`
}

// StatusResponse is the answer to a StatusRequest.
// Status is one of: not_found | pending | confirming | running | completed | denied | timeout | client_aborted | acked.
type StatusResponse struct {
	ID        string         `json:"id"`
	Status    string         `json:"status"`
	Argv      []string       `json:"argv,omitempty"`
	Cwd       string         `json:"cwd,omitempty"`
	UID       uint32         `json:"uid,omitempty"`
	AsUID     uint32         `json:"as_uid,omitempty"`
	ExpiresIn int64          `json:"expires_in,omitempty"`
	Decision  string         `json:"decision,omitempty"`
	By        string         `json:"by,omitempty"`
	Exit      int            `json:"exit"`
	Output    string         `json:"output,omitempty"`
	Step      string         `json:"step,omitempty"`
	ConfirmOf string         `json:"confirm_of,omitempty"`
	Preview   *PreviewRecord `json:"preview,omitempty"`
	AuthURL   string         `json:"auth_url,omitempty"`
	Kind      string         `json:"kind,omitempty"`
	Link      string         `json:"link,omitempty"`
	Summary   string         `json:"summary,omitempty"`
	Acked     bool           `json:"acked,omitempty"`
	AckBy     string         `json:"ack_by,omitempty"`
	AckAt     int64          `json:"ack_at,omitempty"`
	Git       *GitPin        `json:"git,omitempty"`
	GitDrift  *GitDrift      `json:"git_drift,omitempty"`
	FD        *FDPin         `json:"fd,omitempty"`
	FDDrift   *FDDrift       `json:"fd_drift,omitempty"`
	Seal      *SealPin       `json:"seal,omitempty"`
	SealDrift *SealDrift     `json:"seal_drift,omitempty"`
	Suspend   *SuspendPin    `json:"suspend,omitempty"`
}

// ListRequest asks for pending records (plugin server polls this).
type ListRequest struct{}

// PendingItem is one row of the list answer.
type PendingItem struct {
	ID        string         `json:"id"`
	Argv      []string       `json:"argv"`
	UID       uint32         `json:"uid"`
	AsUID     uint32         `json:"as_uid,omitempty"`
	Cwd       string         `json:"cwd"`
	ExpiresIn int64          `json:"expires_in"`
	Step      string         `json:"step,omitempty"`
	ConfirmOf string         `json:"confirm_of,omitempty"`
	Preview   *PreviewRecord `json:"preview,omitempty"`
	AuthURL   string         `json:"auth_url,omitempty"`
	Kind      string         `json:"kind,omitempty"`
	Link      string         `json:"link,omitempty"`
	Summary   string         `json:"summary,omitempty"`
	Acked     bool           `json:"acked,omitempty"`
	AckBy     string         `json:"ack_by,omitempty"`
	Git       *GitPin        `json:"git,omitempty"`
	GitDrift  *GitDrift      `json:"git_drift,omitempty"`
	FD        *FDPin         `json:"fd,omitempty"`
	FDDrift   *FDDrift       `json:"fd_drift,omitempty"`
	Seal      *SealPin       `json:"seal,omitempty"`
	SealDrift *SealDrift     `json:"seal_drift,omitempty"`
	Suspend   *SuspendPin    `json:"suspend,omitempty"`
}

// PendingList answers ListRequest: unexpired, undecided records only.
type PendingList struct {
	Items []PendingItem `json:"items"`
}

// RecentRequest asks for recently decided records (newest first).
type RecentRequest struct {
	Limit int `json:"limit,omitempty"`
}

// RecentItem is one decided record with its verdict and execution result.
// Exit is -1 and Output empty when nothing executed (denied/timeout).
type RecentItem struct {
	ID        string     `json:"id"`
	Argv      []string   `json:"argv"`
	Cwd       string     `json:"cwd"`
	AsUID     uint32     `json:"as_uid,omitempty"`
	Decision  string     `json:"decision"`
	By        string     `json:"by,omitempty"`
	Exit      int        `json:"exit"`
	Output    string     `json:"output,omitempty"`
	Step      string     `json:"step,omitempty"`
	ConfirmOf string     `json:"confirm_of,omitempty"`
	AuthURL   string     `json:"auth_url,omitempty"`
	Kind      string     `json:"kind,omitempty"`
	Link      string     `json:"link,omitempty"`
	Summary   string     `json:"summary,omitempty"`
	Acked     bool       `json:"acked,omitempty"`
	AckBy     string     `json:"ack_by,omitempty"`
	GitDrift  *GitDrift  `json:"git_drift,omitempty"`
	FDDrift   *FDDrift   `json:"fd_drift,omitempty"`
	SealDrift *SealDrift `json:"seal_drift,omitempty"`
}

// RecentList answers RecentRequest: decided records, newest first.
type RecentList struct {
	Items []RecentItem `json:"items"`
}

// RunResult is the daemon's final answer to a run request.
type RunResult struct {
	Status string `json:"status"` // allowed | denied | pending
	ID     string `json:"id,omitempty"`
	Exit   int    `json:"exit,omitempty"`
	Output string `json:"output,omitempty"`
	Reason string `json:"reason,omitempty"`
}

// VerdictAck answers a verdict submission.
type VerdictAck struct {
	Recorded bool   `json:"recorded"`
	Error    string `json:"error,omitempty"`
}

// PendingRecord is the authoritative stored request. Execution reads only
// this — never anything that crossed a wire.
// Kind is "" for exec petitions, "notify" for no-exec notify petitions.
type PendingRecord struct {
	Argv      []string          `json:"argv"`
	UID       uint32            `json:"uid"`
	AsUID     uint32            `json:"as_uid,omitempty"`
	Cwd       string            `json:"cwd"`
	Expires   int64             `json:"expires"`
	Env       map[string]string `json:"env,omitempty"`
	ChatID    string            `json:"chat_id,omitempty"`
	MsgID     int64             `json:"message_id,omitempty"`
	Step      string            `json:"step,omitempty"`
	ConfirmOf string            `json:"confirm_of,omitempty"`
	Preview   *PreviewRecord    `json:"preview,omitempty"`
	AuthURL   string            `json:"auth_url,omitempty"`
	Kind      string            `json:"kind,omitempty"`
	Link      string            `json:"link,omitempty"`
	Summary   string            `json:"summary,omitempty"`
	Git       *GitPin           `json:"git,omitempty"`
	FD        *FDPin            `json:"fd,omitempty"`
	Seal      *SealPin          `json:"seal,omitempty"`
	Suspend   *SuspendPin       `json:"suspend,omitempty"`
}

// AckRecord is written once, atomically (O_EXCL): first ack wins.
// Non-binding visibility signal; the agent verifies live state itself.
type AckRecord struct {
	By       string `json:"by"`
	UnixNano int64  `json:"unix_nano"`
}

// VerdictRecord is written once, atomically (O_EXCL): one verdict wins.
type VerdictRecord struct {
	Decision string `json:"decision"`
	By       string `json:"by"`
	UnixNano int64  `json:"unix_nano"`
}

// responseTypes maps envelope keys to their response types. It lives
// here, next to the structs, so the daemon derives everything below
// via reflection instead of mirroring shapes by hand.
var responseTypes = map[string]reflect.Type{
	"run":                 reflect.TypeFor[RunResult](),
	"verdict":             reflect.TypeFor[VerdictAck](),
	"notify":              reflect.TypeFor[RunResult](),
	"ack":                 reflect.TypeFor[AckResponse](),
	"list":                reflect.TypeFor[PendingList](),
	"recent":              reflect.TypeFor[RecentList](),
	"status":              reflect.TypeFor[StatusResponse](),
	"telegram_info":       reflect.TypeFor[TelegramInfoResponse](),
	"telegram_set_config": reflect.TypeFor[TelegramSetConfigResponse](),
	"policy_add_rule":     reflect.TypeFor[PolicyAddRuleResponse](),
	"version":             reflect.TypeFor[VersionResponse](),
	"help":                reflect.TypeFor[HelpResponse](),
}

// ShapeOf reflects over a struct type and returns its wire field shapes.
func ShapeOf(t reflect.Type) []FieldShape {
	for t.Kind() == reflect.Pointer {
		t = t.Elem()
	}
	if t.Kind() != reflect.Struct {
		return nil
	}
	out := make([]FieldShape, 0, t.NumField())
	for i := 0; i < t.NumField(); i++ {
		f := t.Field(i)
		if !f.IsExported() {
			continue
		}
		tag := f.Tag.Get("json")
		name := strings.SplitN(tag, ",", 2)[0]
		if name == "" {
			name = strings.ToLower(f.Name)
		}
		if name == "-" {
			continue
		}
		out = append(out, FieldShape{
			Name:     f.Name,
			JSON:     name,
			Type:     f.Type.String(),
			Optional: strings.Contains(tag, "omitempty"),
		})
	}
	return out
}

// OpKeys returns the valid ClientMessage envelope keys in field order.
func OpKeys() []string {
	t := reflect.TypeFor[ClientMessage]()
	keys := make([]string, 0, t.NumField())
	for i := 0; i < t.NumField(); i++ {
		tag := t.Field(i).Tag.Get("json")
		name := strings.SplitN(tag, ",", 2)[0]
		if name != "" && name != "-" {
			keys = append(keys, name)
		}
	}
	return keys
}

// Describe returns OpInfo for every envelope key, or just op when named.
func Describe(op string) ([]OpInfo, bool) {
	t := reflect.TypeFor[ClientMessage]()
	var ops []OpInfo
	for i := 0; i < t.NumField(); i++ {
		f := t.Field(i)
		key := strings.SplitN(f.Tag.Get("json"), ",", 2)[0]
		if op != "" && key != op {
			continue
		}
		rt := f.Type
		for rt.Kind() == reflect.Pointer {
			rt = rt.Elem()
		}
		info := OpInfo{
			Op:             key,
			Request:        rt.Name(),
			RequestFields:  ShapeOf(rt),
			ResponseFields: nil,
		}
		if resp, ok := responseTypes[key]; ok {
			info.Response = resp.Name()
			info.ResponseFields = ShapeOf(resp)
		}
		ops = append(ops, info)
		if op != "" {
			return ops, true
		}
	}
	if op != "" {
		return nil, false
	}
	return ops, true
}

type AuditEvent struct {
	Ev         string   `json:"ev"` // request | verdict | allow | exec | confirm | confirmation_timeout | client_aborted | notify | ack | git_drift | fd_drift | seal_drift | suspend | resume
	UID        uint32   `json:"uid,omitempty"`
	By         string   `json:"by,omitempty"`
	Argv       []string `json:"argv,omitempty"`
	Cwd        string   `json:"cwd,omitempty"`
	Tier       string   `json:"tier,omitempty"`
	Decision   string   `json:"decision,omitempty"`
	RID        string   `json:"rid,omitempty"`
	Step       string   `json:"step,omitempty"`
	ConfirmOf  string   `json:"confirm_of,omitempty"`
	AsUID      uint32   `json:"as_uid,omitempty"`
	Dry        bool     `json:"dry,omitempty"`
	Exit       int      `json:"exit,omitempty"`
	EnvDropped []string `json:"env_dropped,omitempty"`
	TS         string   `json:"ts"`
	Link       string   `json:"link,omitempty"`
	Summary    string   `json:"summary,omitempty"`
}
