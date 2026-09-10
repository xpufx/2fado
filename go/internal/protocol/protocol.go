// Package protocol defines every message that crosses a boundary:
// client<->daemon socket, daemon<->Telegram, daemon<->disk. All structs,
// no dynamic typing. The Service methods in internal/service map 1:1 onto
// future MCP tools (run/approve/pending) — that fork stays open without
// this binary knowing what MCP is.
package protocol

// RunRequest is what the client sends: the exact argv plus the caller's
// execution context for environment reconstruction.
type RunRequest struct {
	Argv []string          `json:"argv"`
	Cwd  string            `json:"cwd"`
	Env  map[string]string `json:"env"`
}

// VerdictSubmit records a human decision. In the PoC any local uid may
// submit; production restricts this to the SSO-bound UI.
type VerdictSubmit struct {
	ID       string `json:"id"`
	Decision string `json:"decision"` // approve | deny
}

// ClientMessage is the socket envelope: exactly one field is set.
type ClientMessage struct {
	Run     *RunRequest    `json:"run,omitempty"`
	Verdict *VerdictSubmit `json:"verdict,omitempty"`
	List    *ListRequest   `json:"list,omitempty"`
}

// ListRequest asks for pending records (plugin server polls this).
type ListRequest struct{}

// PendingItem is one row of the list answer.
type PendingItem struct {
	ID        string   `json:"id"`
	Argv      []string `json:"argv"`
	UID       uint32   `json:"uid"`
	Cwd       string   `json:"cwd"`
	ExpiresIn int64    `json:"expires_in"`
}

// PendingList answers ListRequest: unexpired, undecided records only.
type PendingList struct {
	Items []PendingItem `json:"items"`
}

// RunResult is the daemon's final answer to a run request.
type RunResult struct {
	Status string `json:"status"` // allowed | denied
	Exit   int    `json:"exit,omitempty"`
	Output string `json:"output,omitempty"`
	Reason string `json:"reason,omitempty"`
}

// VerdictAck answers a verdict submission.
type VerdictAck struct {
	Recorded bool `json:"recorded"`
}

// PendingRecord is the authoritative stored request. Execution reads only
// this — never anything that crossed a wire.
type PendingRecord struct {
	Argv    []string `json:"argv"`
	UID     uint32   `json:"uid"`
	Cwd     string   `json:"cwd"`
	Expires int64    `json:"expires"`
	ChatID  string   `json:"chat_id,omitempty"`
	MsgID   int64    `json:"message_id,omitempty"`
}

// VerdictRecord is written once, atomically (O_EXCL): one verdict wins.
type VerdictRecord struct {
	Decision string `json:"decision"`
	By       string `json:"by"`
	UnixNano int64  `json:"unix_nano"`
}

// AuditEvent is one JSON line in the append-only log.
type AuditEvent struct {
	Ev         string   `json:"ev"` // request | verdict | allow | exec
	UID        uint32   `json:"uid,omitempty"`
	By         string   `json:"by,omitempty"`
	Argv       []string `json:"argv,omitempty"`
	Cwd        string   `json:"cwd,omitempty"`
	Tier       string   `json:"tier,omitempty"`
	Decision   string   `json:"decision,omitempty"`
	RID        string   `json:"rid,omitempty"`
	AsUID      uint32   `json:"as_uid,omitempty"`
	Dry        bool     `json:"dry,omitempty"`
	Exit       int      `json:"exit,omitempty"`
	EnvDropped []string `json:"env_dropped,omitempty"`
	TS         string   `json:"ts"`
}
