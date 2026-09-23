# Backend CLI — parity surface for third parties

Status: draft. Daemon-side refs: `go/cmd/2fado/main.go` (subcommands, usage, env), `go/internal/client/client.go` (socket ops, exit codes), `go/internal/protocol/protocol.go` (envelope, request shapes — source of truth, `ClientMessage` 15 ops).

Third parties should mirror this surface for operator parity. Producer-side ops (`run`/`notify`/`ask`) vs consumer-side ops (list/verdict/status) — whether both are required is an open question (see `docs/backend-adapter.md`).

## Security & Privilege Escalation Boundary

Privilege escalation is disabled in the default build and no escalation features are promised:
- The CLI and daemon operate strictly under the unprivileged user identity.
- Execution-identity switching code is not compiled into the default binary (`internal/service/escalation_on.go` excluded).
- No root escalation, setuid execution, or sudo delegation is provided or promised.

## Subcommands

```
2fado daemon | 2fado run -- <argv...> | 2fado approve|deny <id>
  | 2fado cancel <id> [--reason <text>]
  | 2fado notify --link <url> --summary <text> [--ttl <dur>]
  | 2fado ask --question <q> --option <o> [--option <o>...] [--link <url>] [--ttl <dur>]
  | 2fado ack <id> | 2fado status <id> | 2fado list | 2fado recent [--limit N]
  | 2fado audit [--limit N] [--cursor C] [--ev E] [--id RID] [--uid U] [--since UNIX] [--until UNIX]
  | 2fado version | 2fado socket-version
```

- `daemon` — run the unprivileged approval daemon service. Config path via `TWOFADO_CONF`/`FADO_CONF`/`2FADO_CONF`, default `/etc/2fado/2fado.conf`.
- `run -- <argv...> [--detach|-d|--async]` — petition to execute. Ships `RunRequest{argv,cwd,env,detach?}` as `{run:{...}}`. Without `--detach`, a `pending` answer prints `request submitted, waiting for approval (id: …)` to stderr and exits 1; with `--detach`, prints `request submitted (id: …). Detached. Use '2fado status <id>'…` and exits 0.
- `approve|deny <id>` — local verdict (PoC; production: SSO-bound UI). Sends `{verdict:{id,decision}}`, prints `{recorded: …}`. Rejected verdicts print `verdict rejected (…)` to stderr, exit 1.
- `cancel <id> [--reason <text>]` — cancel a pending, confirming, or waiting ask petition safely and idempotently. Admins or the original petition creator UID may cancel. Process groups are resumed immediately, Telegram/Paseo cards are closed, and cancellation reason is audited. Sends `{cancel:{id,reason,by:"cli"}}`, prints `{cancelled: true}`. Rejections print `cancel rejected (…)`, exit 1.
- `notify --link <url> --summary <text> [--ttl <dur>]` — create a no-exec notify-only petition (human-presence step: 2FA link, staged approval, FYI+ack). Sends `{notify:{link,summary,ttl_seconds?}}` (`--ttl` parsed as Go duration, e.g. `30m`, `2h`, `24h`; `0` = default long TTL). Prints `notify submitted (id: …)`; rejects print `notify rejected (…)`.
- `ask --question <q> --option <o> [--option <o>...] [--link <url>] [--ttl <dur>] [--multi-select] [--allow-write-in]` — create an interactive choice question petition. Sends `{ask:{question,options,link?,ttl_seconds?,multi_select?,allow_write_in?}}` (requires at least two options). Blocks until selection lands or TTL expires. Prints chosen option to stdout and exits 0 on selection, 2 on timeout, 1 on rejection/transport error. Note (#74): `--multi-select` and `--allow-write-in` flags are persisted in protocol/store for compatibility; interactive single-choice resolution is active in current reference daemon builds.
- `ack <id>` — acknowledge a notify petition. Sends `{ack:{id}}`, prints `{acked: …}`; rejects print `ack rejected (…)`.
- `status <id>` — inspect state and execution result. Sends `{status:{id}}`, pretty-prints the `StatusResponse` JSON.
- `list` — inspect pending queue. Sends `{list:{}}`, prints one block per item (id, argv, cwd, step/confirm_of, kind, expires_in).
- `recent [--limit N]` — inspect decided history. Sends `{recent:{limit}}` (`--limit` omitted → `0`, daemon default), prints one block per item (id, argv, decision, by, exit, step/confirm_of).
- `audit [--limit N] [--cursor C] [--ev E] [--id RID] [--uid U] [--since UNIX] [--until UNIX]` — query the append-only audit trail. Sends `{audit:{cursor?,limit?,ev?,id?,uid?,since?,until?}}`, pretty-prints the `AuditQueryResponse` JSON (`items` newest-first, `next_cursor` for the next older page). Unlike `recent` (capped derived view), `audit` reads the full trail and never exposes env values, secrets, or credentials. Filters are exact-match and ANDed.
- `version` (`-v`, `--version`) — print local binary `version/commit/build_time/sha256` (no socket).
- `socket-version` — query the daemon over the socket (`{version:{}}`), pretty-print `{version,git_commit,build_time,binary_sha256,pid}`. This is the op `approval.health` probes.

## Env vars

| Var(s) | Use | Default |
|---|---|---|
| `TWOFADO_SOCKET` → `FADO_SOCKET` | explicit daemon socket path | see chain below |
| `TWOFADO_RUN_DIR` → `FADO_RUN_DIR` | runtime dir for socket + pid file | unset |
| `TWOFADO_STATE_DIR` → `FADO_STATE_DIR` | daemon state/store dir | `$XDG_STATE_HOME/2fado` |
| `TWOFADO_PID_FILE` → `FADO_PID_FILE` | explicit daemon pid file | state dir (or run dir) |
| `TWOFADO_CONF` → `FADO_CONF` → `2FADO_CONF` | daemon config path (`daemon` subcommand) | `/etc/2fado/2fado.conf` |

Socket candidate chain (shared by daemon and CLI via `config.DefaultSocketPath`):
`$TWOFADO_SOCKET` → `$FADO_SOCKET` → `$TWOFADO_RUN_DIR/2fado.sock` →
`$XDG_RUNTIME_DIR/2fado/2fado.sock` → `/tmp/2fado.sock`. A daemon config
`SOCKET=` entry sits between the env overrides and these defaults.

## Exit-code conventions (`client.go`)

- `run`: `allowed` → relay `res.Output` to stdout, exit remote code; `pending` without detach → exit 1; `pending` with detach → exit 0; `denied` → `denied (…)` on stderr, exit 1. Socket/parse failure → exit 1.
- `notify`: `pending` → exit 0; rejected → exit 1. `ack`/`approve|deny`: success (even `recorded/acked: false`) → exit 0; socket/parse/`error`-field failure → exit 1.
- `ask`: `selected` → exit 0 (chosen option printed to stdout); `timeout` → exit 2; rejected/transport error → exit 1.
- `cancel`: success (including idempotent duplicate cancellation) → exit 0; socket/parse/`error`-field failure (e.g. `unauthorized`, `not found`) → exit 1.
- `status`: `completed` → exit remote code; `not_found|denied|timeout|cancelled` → exit 1; all other states (`pending|confirming|running|…`) → exit 0 after printing JSON. Socket/parse failure → exit 1.
- `list`/`recent`: print human-readable blocks, exit 0 on success, 1 on socket/parse failure.
- `audit`: prints pretty-printed JSON, exit 0 on success (including an empty page), 1 on socket/parse failure; bad filters → exit 2 via `usage()`.
- `socket-version`: prints JSON, exit 0 on success, 1 on failure. Bad usage (e.g. bad `--ttl`, missing args) → exit 2 via `usage()`.

## Socket ops behind the CLI

Each subcommand is one JSON-lines envelope over the unix socket, one JSON line back, fresh connection per call: run, verdict, cancel, notify, ask, ack, status, version, list, recent, audit (11 socket ops). Daemon-only ops (telegram_info, telegram_set_config, policy_add_rule, help) have no CLI subcommand (help is available via direct socket query).
