# Backend API — plugin RPC reference (`approval.*`)

Status: draft. Source: design report on [Issue #28](https://forge.mrs.aager.de/oktay/2fado/issues/28) (contract inventory §1). Code refs: `plugin/shared/approval.ts` (zod contracts), `plugin/server/fado.ts` (handlers), `plugin/client/approvals.tsx` (polling).

All contracts use helper `defineContract`; both sides validated. Every input carries optional `socketPath` (future `backendRef` alias — see `docs/backend-adapter.md`).

## `approval.list` — pending queue

- Input: `{socketPath?}`.
- Output: `{items: [{id, argv, host, caller, cwd, expiresIn, step: initial|confirm (default initial), confirmOf?, kind?, link?, summary?, acked?, ackBy?, authUrl?, preview?}]}`.
- Server: sends `{list:{}}`, camelCases daemon `PendingList`, sets `host=os.hostname()`, `caller=String(uid)`. Fail-soft: `{items:[]}` + `log.warn`.
- Client: polls every 3s (`POLL_MS`); toast per new id; `kind==="notify"` renders Ack-only cards (no argv); `step==="confirm"` renders danger styling + `confirmOf` dedup against Recent.

## `approval.verdict` — approve / deny

- Input: `{id, decision: approve|deny, socketPath?}`.
- Output: `{recorded}`.
- Server: sends `{verdict:{id,decision,by:"paseo"}}`; returns `{recorded: raw.recorded === true}`. Missing input → `{recorded:false}`. Fail-soft: `{recorded:false}` + `log.warn`.
- `by` is attribution only, never authority (see below).

## `approval.ack` — notify acknowledgement

- Input: `{id (min 1), socketPath?}`.
- Output: `{acked}`.
- Server: sends `{ack:{id,by:"paseo"}}`. Missing/empty id → `{acked:false}`. Non-binding visibility signal ("seen + acted"); only meaningful for notify-kind items.

## `approval.recent` — history

- Input: `{socketPath?, limit? (int, 1–50, default 10)}`.
- Output: `{items: [{id, argv, cwd, decision, by, exit, output, step?, confirmOf?, kind?, link?, summary?, acked?, ackBy?, authUrl?}]}`.
- Server: sends `{recent:{limit}}`. Fail-soft: `{items:[]}`.
- Client: polls every 5s with limit 10 (`RECENT_POLL_MS`, `RECENT_LIMIT`); History tab hides if backend lacks this capability.

## `approval.status` — execution outcome

- Input: `{id (min 1), socketPath?}`.
- Output: `{id, status, argv?, cwd?, expiresIn?, decision?, by?, exit, output?, step?, confirmOf?, kind?, link?, summary?, acked?, ackBy?, ackAt?, authUrl?}`, where `status ∈ not_found|pending|confirming|running|completed|denied|timeout|client_aborted|confirmation_timeout|acked`.
- Server: sends `{status:{id}}`; unknown daemon status → `not_found`; missing id → `{id:"",status:"not_found",exit:-1}`. Fail-soft: `{id,status:"not_found",exit:-1}`.
- Client: after approve, polls ~350ms × 60 for `confirming→completed` transitions; without this op, tracking degrades to fire-and-forget + recent poll.

## `approval.telegram_info` / `approval.telegram_set_config` — Telegram fallback

- `telegram_info` input `{socketPath?}` → output `{configured, botUsername?, chatId?, approvers[] (default []), status: connected|disconnected|unconfigured|error (default unconfigured), error?, notificationTarget: telegram|paseo|both (default both)}`. Unknown daemon status falls back to `configured?connected:unconfigured`. Client polls every 15s.
- `telegram_set_config` input `{botToken?, chatId?, approvers?, notificationTarget?, socketPath?}` → output `{success, botUsername?, error?}`. Missing input → `{success:false, error:"missing input"}`.
- Both optional for third parties; capabilities flags gate the UI.

## `approval.health` — reachability probe

- Input: `{socketPath?}` → output `{reachable, version?, pid?}`.
- Server: sends `{version:{}}` with 1500ms socket timeout; unreachable → `{reachable:false}`. Client polls on the 3s cadence; drives the "2fadod down" header state.

## `approval.policy_add_rule` — policy shortcuts

- Input: `{target: whitelist|blacklist, match_type: exact|base|custom, pattern: string[], socketPath?}` → output `{success, error?, rules_count?}`.
- Server: empty pattern → `{success:false, error:"empty pattern"}`; daemon reject → `{success:false, error: raw.error ?? "daemon rejected rule"}`.
- Client derives pattern from scope: `exact` = full argv, `base` = `[resolvedBinary]`, `custom` = edited prefix (`*` = single-arg wildcard). Optional for third parties.

## Polling cadences (client)

| RPC | Cadence |
|---|---|
| `approval.list` (+ `approval.health`) | 3s |
| `approval.recent` | 5s (limit 10) |
| `approval.telegram_info` | 15s |
| `approval.status` (post-approve only) | ~350ms × 60 |

## `by` attribution note

Verdicts/acks carry `by:"paseo"` into the daemon audit log next to Telegram's numeric ids — one log, two transports, same schema. No per-user allowlist in v0.8: the runtime exposes no verified clicker identity, so a names list would be theater. Real gating belongs daemon-side; `by` is attribution only, never authority. First recorded verdict wins; replays return `recorded:false`.
