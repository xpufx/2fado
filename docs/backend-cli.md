# Backend CLI — parity surface for third parties

Status: draft. Daemon-side refs: `go/cmd/2fado/main.go` (subcommands, usage, env), `go/internal/client/client.go` (socket ops, exit codes), `go/internal/protocol/protocol.go` (envelope, request shapes — source of truth, `ClientMessage` 11 ops).

Third parties should mirror this surface for operator parity. Producer-side ops (`run`/`notify`) vs consumer-side ops (list/verdict/status) — whether both are required is an open question (see `docs/backend-adapter.md`).

## Subcommands

```
2fado daemon | 2fado run -- <argv...> | 2fado approve|deny <id>
  | 2fado notify --link <url> --summary <text> [--ttl <dur>]
  | 2fado ack <id> | 2fado status <id> | 2fado list | 2fado recent [--limit N]
  | 2fado version | 2fado socket-version
```

- `daemon` — run the privileged service. Config path via `TWOFADO_CONF`/`FADO_CONF`/`2FADO_CONF`, default `/etc/2fado/2fado.conf`.
- `run -- <argv...> [--detach|-d|--async]` — petition to execute. Ships `RunRequest{argv,cwd,env,detach?}` as `{run:{...}}`. Without `--detach`, a `pending` answer prints `request submitted, waiting for approval (id: …)` to stderr and exits 1; with `--detach`, prints `request submitted (id: …). Detached. Use '2fado status <id>'…` and exits 0.
- `approve|deny <id>` — local verdict (PoC; production: SSO-bound UI). Sends `{verdict:{id,decision}}`, prints `{recorded: …}`. Rejected verdicts print `verdict rejected (…)` to stderr, exit 1.
- `notify --link <url> --summary <text> [--ttl <dur>]` — create a no-exec notify-only petition (human-presence step: 2FA link, staged approval, FYI+ack). Sends `{notify:{link,summary,ttl_seconds?}}` (`--ttl` parsed as Go duration, e.g. `30m`, `2h`, `24h`; `0` = default long TTL). Prints `notify submitted (id: …)`; rejects print `notify rejected (…)` to stderr.
- `ack <id>` — acknowledge a notify petition. Sends `{ack:{id}}`, prints `{acked: …}`; rejects print `ack rejected (…)`.
- `status <id>` — inspect state and execution result. Sends `{status:{id}}`, pretty-prints the `StatusResponse` JSON.
- `list` — inspect pending queue. Sends `{list:{}}`, prints one block per item (id, argv, cwd, step/confirm_of, kind, expires_in).
- `recent [--limit N]` — inspect decided history. Sends `{recent:{limit}}` (`--limit` omitted → `0`, daemon default), prints one block per item (id, argv, decision, by, exit, step/confirm_of).
- `version` (`-v`, `--version`) — print local binary `version/commit/build_time/sha256` (no socket).
- `socket-version` — query the daemon over the socket (`{version:{}}`), pretty-print `{version,git_commit,build_time,binary_sha256,pid}`. This is the op `approval.health` probes.

## Env vars

| Var(s) | Use | Default |
|---|---|---|
| `TWOFADO_SOCKET` → `FADO_SOCKET` | Daemon socket path (CLI candidate chain) | `/tmp/2fado.sock` |
| `TWOFADO_CONF` → `FADO_CONF` → `2FADO_CONF` | Daemon config path (`daemon` subcommand) | `/etc/2fado/2fado.conf` |

Note: the frozen socket candidate chain is per-call `socketPath` → `$TWOFADO_SOCKET` → `$FADO_SOCKET` → `/tmp/2fado.sock`. There is no `/run/2fado.sock` candidate.

## Exit-code conventions (`client.go`)

- `run`: `allowed` → relay `res.Output` to stdout, exit remote code; `pending` without detach → exit 1; `pending` with detach → exit 0; `denied` → `denied (…)` on stderr, exit 1. Socket/parse failure → exit 1.
- `notify`: `pending` → exit 0; rejected → exit 1. `ack`/`approve|deny`: success (even `recorded/acked: false`) → exit 0; socket/parse/`error`-field failure → exit 1.
- `status`: `completed` → exit remote code; `not_found|denied|timeout` → exit 1; all other states (`pending|confirming|running|…`) → exit 0 after printing JSON. Socket/parse failure → exit 1.
- `list`/`recent`: print human-readable blocks, exit 0 on success, 1 on socket/parse failure.
- `socket-version`: prints JSON, exit 0 on success, 1 on failure. Bad usage (e.g. bad `--ttl`, missing args) → exit 2 via `usage()`.

## Socket ops behind the CLI

Each subcommand is one JSON-lines envelope over the unix socket, one JSON line back, fresh connection per call: run, verdict, notify, ack, status, version, list, recent. Daemon-only ops (telegram_info, telegram_set_config, policy_add_rule) have no CLI subcommand.
