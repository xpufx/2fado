# Backend API — third-party consumer RPC reference (`approval.*`)

Status: draft. This describes the contract a third-party consumer implements against the daemon socket API; op inventory source of truth is `go/internal/protocol/protocol.go` (`ClientMessage`, 16 ops: run, verdict, notify, ask, select, ack, cancel, list, recent, audit, status, telegram_info, telegram_set_config, policy_add_rule, version, help).

All consumer RPCs are validated on both sides. Every input carries optional `socketPath` (future `backendRef` alias — see `docs/backend-adapter.md`).

## Security & Privilege Escalation Boundary

Privilege escalation is disabled in the default build and no escalation features are promised:
- Daemon and CLI operations execute strictly unprivileged with the daemon process UID/GID and environment.
- The default binary excludes privilege-switching implementation code at compile time.
- All requests and responses operate inside the daemon user boundary; no credential delegation or elevated execution is supported.

## `approval.list` — pending queue

- Input: `{socketPath?}`.
- Output: `{items: [{id, argv, host, caller, cwd, asUid, expiresIn, step: initial|confirm (default initial), confirmOf?, kind?, link?, summary?, question?, options?, selection?, selectionIdx?, acked?, ackBy?, authUrl?, preview?}]}`.
- Server: sends `{list:{}}`, camelCases daemon `PendingList`, sets `host=os.hostname()`, `caller=String(uid)`. Fail-soft: `{items:[]}` + `log.warn`.
- Client: polls every 3s (`POLL_MS`); toast per new id; `kind==="notify"` renders Ack-only cards (no argv); `kind==="ask"` renders interactive choice cards with options; `step==="confirm"` renders danger styling + `confirmOf` dedup against Recent.
  - *Ask multi-select & write-in status (#74)*: Protocol fields `multi_select` and `allow_write_in` are preserved on petition/selection structures for schema compatibility. In current reference daemon builds, interactive questions evaluate single option selections (`selection_idx` / `selection`); multi-select aggregation and write-in validation are deferred.

## `approval.verdict` — approve / deny

- Input: `{id, decision: approve|deny, socketPath?}`.
- Output: `{recorded}`.
- Server: sends `{verdict:{id,decision,by:"consumer"}}`; returns `{recorded: raw.recorded === true}`. Missing input → `{recorded:false}`. Fail-soft: `{recorded:false}` + `log.warn`.
- `by` is attribution only, never authority (see below).

## `approval.select` — option selection for interactive ask petitions

- Input: `{id (min 1), selection (string), selectionIdx?: number, socketPath?}`.
- Output: `{selected: boolean, error?: string}`.
- Server: sends `{select:{id,selection,selection_idx}}`. Records chosen option for an active `kind="ask"` petition via atomic store write, unblocking the waiting `ask` caller immediately. First selection wins. Missing input → `{selected:false, error:"missing id or selection"}`. Rejections/already selected → `{selected:false, error:"selection rejected"}`.

## `approval.cancel` — explicit cancellation

- Input: `{id (min 1), reason?: string, socketPath?}`.
- Output: `{cancelled: boolean, error?: string}`.
- Server: sends `{cancel:{id,reason,by:"consumer"}}`. Missing/empty id → `{cancelled:false, error:"missing id"}`. Unblocks pending, confirming, and waiting ask operations safely and idempotently. Resumes suspended process groups immediately, closes cards, and audits the cancellation reason and actor.

## `approval.ack` — notify acknowledgement

- Input: `{id (min 1), socketPath?}`.
- Output: `{acked}`.
- Server: sends `{ack:{id,by:"consumer"}}`. Missing/empty id → `{acked:false}`. Non-binding visibility signal ("seen + acted"); only meaningful for notify-kind items.

## `approval.recent` — history

- Input: `{socketPath?, limit? (int, 1–50, default 10)}`.
- Output: `{items: [{id, argv, cwd, asUid, decision, by, exit, output, step?, confirmOf?, kind?, link?, summary?, question?, options?, selection?, selectionIdx?, acked?, ackBy?, authUrl?}]}`.
- Server: sends `{recent:{limit}}`. Fail-soft: `{items:[]}`.
- Client: polls every 5s with limit 10 (`RECENT_POLL_MS`, `RECENT_LIMIT`); History tab hides if backend lacks this capability.

## `approval.audit` — audit-log history

- Input: `{cursor?, limit? (int, 1–200, default 50), ev?, id?, uid?, since? (unix sec, inclusive), until? (unix sec, inclusive), socketPath?}`.
- Output: `{items: [{ev, ts, uid?, by?, argv?, cwd?, tier?, decision?, rid?, step?, confirm_of?, as_uid?, dry?, exit?, env_dropped?, link?, summary?, question?, options?, selection?, selection_idx?, reason?}], next_cursor?}`.
- Server: sends `{audit:{cursor,limit,ev,id,uid,since,until}}`. Newest-first, exact-match ANDed filters, bounded page. `next_cursor` is an opaque continuation token for the next older page (absent on the last page). Unlike `recent` (a capped derived view of decided petitions, max 50), `audit` reads the append-only trail directly and never carries environment values, secrets, or credentials.
- Client: History tab can page deeper audit history instead of the capped recent list; unknown `ev` values render generically.

Retention: `audit.log` is append-only and never pruned by the request-store retention sweep (`DefaultPruneTTL`, 30 days, applies to `pending/` petition records only). Audit growth is bounded by the operator's disk maintenance, not by the daemon; the query is always bounded by `limit` (default 50, max 200) so a large trail never materializes in one response. Cursors are absolute oldest-first line indexes, so they stay valid as new events append at the tail.

## `approval.status` — execution outcome

- Input: `{id (min 1), socketPath?}`.
- Output: `{id, status, argv?, cwd?, asUid?, expiresIn?, decision?, by?, exit, output?, step?, confirmOf?, kind?, link?, summary?, question?, options?, selection?, selectionIdx?, acked?, ackBy?, ackAt?, authUrl?}`, where `status ∈ not_found|pending|confirming|running|completed|denied|timeout|client_aborted|confirmation_timeout|acked|selected|cancelled`.
- Server: sends `{status:{id}}`; unknown daemon status → `not_found`; missing id → `{id:"",status:"not_found",exit:-1}`. Fail-soft: `{id,status:"not_found",exit:-1}`.
- Client: after approve, polls ~350ms × 60 for `confirming→completed` transitions; without this op, tracking degrades to fire-and-forget + recent poll.

## `approval.telegram_info` / `approval.telegram_set_config` — Telegram fallback

- `telegram_info` input `{socketPath?}` → output `{configured, botUsername?, chatId?, approvers[] (default []), status: connected|disconnected|unconfigured|error (default unconfigured), error?, notificationTarget (channel selection; exact enum in protocol.go, default both)}`. Unknown daemon status falls back to `configured?connected:unconfigured`. Client polls every 15s.
- `telegram_set_config` input `{botToken?, chatId?, approvers?, notificationTarget?, socketPath?}` → output `{success, botUsername?, error?}`. Missing input → `{success:false, error:"missing input"}`.
- Both optional for third parties; capabilities flags gate the UI.

## `approval.health` — reachability probe

- Input: `{socketPath?}` → output `{reachable, version?, pid?}`.
- Server: sends `{version:{}}` with 1500ms socket timeout; unreachable → `{reachable:false}`. Client polls on the 3s cadence; drives the "daemon down" header state.

## `approval.policy_add_rule` — policy shortcuts

- Input: `{target: whitelist|blacklist, match_type: exact|base|custom, pattern: string[], socketPath?}` → output `{success, error?, rules_count?}`.
- Server: empty pattern → `{success:false, error:"empty pattern"}`; daemon reject → `{success:false, error: raw.error ?? "daemon rejected rule"}`.
- Client derives pattern from scope: `exact` = full argv, `base` = `[resolvedBinary]`, `custom` = edited prefix (`*` = single-arg wildcard). Optional for third parties.

## Polling cadences (third-party consumer)

| RPC | Cadence |
|---|---|
| `approval.list` (+ `approval.health`) | 3s |
| `approval.recent` | 5s (limit 10) |
| `approval.telegram_info` | 15s |
| `approval.status` (post-approve only) | ~350ms × 60 |

## `by` attribution note

Verdicts/acks carry `by:"consumer"` into the daemon audit log next to Telegram's numeric ids — one log, two transports, same schema. No per-user allowlist in v0.8: the runtime exposes no verified clicker identity, so a names list would be theater. Real gating belongs daemon-side; `by` is attribution only, never authority. First recorded verdict wins; replays return `recorded:false`.

## `approval.help` — op discovery (self-documenting API)

- Input: `{help:{op?}}` — `op` is optional; when set, the response may be narrowed to that op.
- Output: `{ops: [{op, request, response, request_fields[], response_fields[]}]}`. Each field shape is `{name, json, type, optional}`.
- The list is derived over `go/internal/protocol/protocol.go` (`ClientMessage` + `OpKeys()`), so it cannot drift from the envelope — this is the machine-readable op inventory a consumer should read instead of scraping `--help` text.
- Unrecognised envelope: the daemon replies with an error naming the valid ops (`unknown-request: valid ops: …`), so a probing consumer is guided rather than dead-ended.
