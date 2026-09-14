# Go port notes

> Note (2026-09): the shipped CLI is wider than the one-liner below —
> see `docs/backend-cli.md` for the full surface (`daemon | run |
> approve|deny | notify | ack | status | version | socket-version`).

Python proved the loop (it rang the phone); Go is the keeper. Single
static binary, `2fado daemon | run | approve|deny | notify | ack |
status | version | socket-version`, stdlib only
(`go build`, no deps).

## Typing discipline

Every boundary is structs (`internal/protocol`): socket messages, pending
records, verdicts, audit events, Bot API payloads. No `any`, no
`map[string]interface{}` anywhere — `grep` enforces it. The one dynamic
shape in the system is the process environment, which is
`map[string]string` by nature. Linux-only code (peercreds) sits behind a
build tag in `internal/daemon/peercred_linux.go`.

## The MCP fork in the road

Not an MCP server — but shaped for the fork: `internal/service` holds
pure domain logic (`Run`, `Submit`) over typed structs with transport in
`internal/daemon` (unix socket today). A future MCP adapter calls the
same methods directly; the mapping is 1:1 (run → tool, approve → tool,
pending → resource). Nothing in service imports transport, Telegram, or
anything MCP-shaped.
