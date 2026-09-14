// Package protocol defines every message that crosses a boundary:
// client<->daemon socket, daemon<->Telegram, daemon<->disk. All structs,
// no dynamic typing. The Service methods in internal/service map 1:1 onto
// future MCP tools (run/approve/pending) — that fork stays open without
// this binary knowing what MCP is.
package protocol

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
type TelegramInfoResponse struct {
	Configured  bool     `json:"configured"`
	BotUsername string   `json:"bot_username,omitempty"`
	ChatID      string   `json:"chat_id,omitempty"`
	Approvers   []string `json:"approvers"`
	Status      string   `json:"status"`
	Error       string   `json:"error,omitempty"`
}

// TelegramSetConfigRequest sends updated Telegram bot credentials and recipients.
type TelegramSetConfigRequest struct {
	BotToken  *string  `json:"bot_token,omitempty"`
	ChatID    string   `json:"chat_id,omitempty"`
	Approvers []string `json:"approvers,omitempty"`
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

// StatusResponse is the answer to a StatusRequest.
// Status is one of: not_found | pending | confirming | running | completed | denied | timeout | client_aborted | acked.
type StatusResponse struct {
	ID        string         `json:"id"`
	Status    string         `json:"status"`
	Argv      []string       `json:"argv,omitempty"`
	Cwd       string         `json:"cwd,omitempty"`
	UID       uint32         `json:"uid,omitempty"`
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
}

// ListRequest asks for pending records (plugin server polls this).
type ListRequest struct{}

// PendingItem is one row of the list answer.
type PendingItem struct {
	ID        string         `json:"id"`
	Argv      []string       `json:"argv"`
	UID       uint32         `json:"uid"`
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
	ID        string   `json:"id"`
	Argv      []string `json:"argv"`
	Cwd       string   `json:"cwd"`
	Decision  string   `json:"decision"`
	By        string   `json:"by,omitempty"`
	Exit      int      `json:"exit"`
	Output    string   `json:"output,omitempty"`
	Step      string   `json:"step,omitempty"`
	ConfirmOf string   `json:"confirm_of,omitempty"`
	AuthURL   string   `json:"auth_url,omitempty"`
	Kind      string   `json:"kind,omitempty"`
	Link      string   `json:"link,omitempty"`
	Summary   string   `json:"summary,omitempty"`
	Acked     bool     `json:"acked,omitempty"`
	AckBy     string   `json:"ack_by,omitempty"`
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

// AuditEvent is one JSON line in the append-only log.
type AuditEvent struct {
	Ev         string   `json:"ev"` // request | verdict | allow | exec | confirm | confirmation_timeout | client_aborted | notify | ack
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
