# Backend adapter — third-party contract

Status: draft. Source of truth for the op inventory: `go/internal/protocol/protocol.go` (`ClientMessage`, 13 ops: run, verdict, notify, ask, ack, list, recent, status, telegram_info, telegram_set_config, policy_add_rule, version, help).

This page defines what a third-party backend implements so a third-party consumer can use it without the reference daemon.

## Transport (today)

- Unix socket, JSON-lines: one JSON object + `\n` per request, one JSON line back, fresh connection per call.
- Socket timeout 2000ms per candidate (`telegram_info` uses 8000ms); every RPC wrapped in a 5s-timeout, max-4-inflight guard.
- Socket candidate order: per-call `socketPath` → `$TWOFADO_SOCKET` → `$FADO_SOCKET` → `$TWOFADO_RUN_DIR/2fado.sock` → `$XDG_RUNTIME_DIR/2fado/2fado.sock` → `/tmp/2fado.sock`. The daemon's canonical socket is `$XDG_RUNTIME_DIR/2fado/2fado.sock` (see docs/daemon-service.md).
- Envelope (`protocol.ClientMessage`): exactly one field set. Third-party consumers use 8 of 13 ops (list, verdict, ack, recent, status, telegram_info, telegram_set_config, policy_add_rule — see the op table below); `run`, `notify`, `ask`, `version`, `help` are producer/CLI-only or discovery today (see `docs/backend-cli.md`).

## Op table (consumer → backend)

| Socket op sent | Request shape (daemon side) | Response shape |
|---|---|---|
| `{list:{}}` | `ListRequest{}` | `PendingList{items: PendingItem{id,argv,uid,as_uid,cwd,expires_in,step,confirm_of,preview,auth_url,kind,link,summary,question,options,selection,selection_idx,acked,ack_by}}` |
| `{verdict:{id,decision,by:"consumer"}}` | `VerdictSubmit{id,decision:approve\|deny,by}` | `VerdictAck{recorded}` |
| `{ack:{id,by:"consumer"}}` | `AckSubmit{id,by}` | `AckResponse{acked}` |
| `{recent:{limit}}` | `RecentRequest{limit}` | `RecentList{items: RecentItem{id,argv,cwd,as_uid,decision,by,exit,output,step,confirm_of,auth_url,kind,link,summary,question,options,selection,selection_idx,acked,ack_by}}` |
| `{status:{id}}` | `StatusRequest{id}` | `StatusResponse{id,status,argv,cwd,uid,as_uid,expires_in,decision,by,exit,output,step,confirm_of,preview,auth_url,kind,link,summary,question,options,selection,selection_idx,acked,ack_by,ack_at}` |
| `{telegram_info:{}}` | `TelegramInfoRequest{}` | `TelegramInfoResponse{configured,bot_username,chat_id,approvers,status:connected\|disconnected\|unconfigured\|error,error}` |
| `{telegram_set_config:{bot_token,chat_id,approvers}}` | `TelegramSetConfigRequest{bot_token?,chat_id?,approvers?}` | `TelegramSetConfigResponse{success,bot_username,error}` |
| `{policy_add_rule:{target,match_type,pattern}}` | `PolicyAddRuleRequest{target:whitelist\|blacklist,match_type:exact\|base\|custom,pattern[]}` | `PolicyAddRuleResponse{success,error,rules_count}` |

Consumer-side mapping notes (third-party consumer implements this): `PendingItem` is camelCased plus `host=os.hostname()`, `caller=String(uid)`, `step` normalized to `"confirm"|"initial"`; `status` whitelisted to `not_found|pending|confirming|running|completed|denied|timeout|client_aborted|confirmation_timeout|acked|selected`, else `not_found`; telegram falls back to `configured?connected:unconfigured`.

## PendingItem: core vs optional

MUST (MVP): `id`, plus either exec core (`argv`, `cwd`, `expires_in`), notify core (`kind="notify"`, `link`, `summary`), or ask core (`kind="ask"`, `question`, `options`). `argv` may be empty for pure notify or ask backends — client renders Ack-only cards with no argv for notify, or interactive choice cards for ask.

SHOULD: `step`/`confirm_of` (2-step rendering + `confirmOf` dedup against Recent), `acked`/`ack_by`, `auth_url`, `selection`/`selection_idx`.

MAY: `preview{resolved_binary,target_cwd,affected_count,sample_paths,risk_level,risk_reason}` — optional-render block, safe to omit.

## Status lifecycle

`pending → running → completed | denied | timeout`, with `confirming` as the transient 2-step state between first approval and execution, plus terminal `not_found | client_aborted | confirmation_timeout | acked | selected`. Client behavior that constrains backends: after approve, polls `approval.status` ~350ms × 60 for `confirming→completed` transitions; without `getStatus`, post-approve tracking degrades to fire-and-forget + recent poll.

## Proposed `ApprovalBackend` boundary

```ts
interface ApprovalBackend {
  kind: string; // e.g. "socket" | "third-party-id"
  capabilities: {
    recent: boolean; statusPoll: boolean; ack: boolean;
    policyRules: boolean; telegram: boolean; preview: boolean; twoStep: boolean;
  };
  listPending(input: {backendRef?: string}): Promise<PendingListOutput>;
  submitVerdict(input: {id: string; decision: "approve"|"deny"}): Promise<{recorded: boolean}>;
  submitAck(input: {id: string}): Promise<{acked: boolean}>;
  listRecent(input: {limit?: number}): Promise<RecentListOutput>;
  getStatus(input: {id: string}): Promise<StatusOutput>;
  addPolicyRule(...): ...; getTelegramInfo(...); setTelegramConfig(...);
}
```

`TwofadoSocketBackend` becomes the default implementation, not the only one. `recent`/`statusPoll`/`ack`/`policyRules`/`telegram`/`preview`/`twoStep` gate tabs, badges, and dialogs; missing capabilities degrade (History tab hides without `listRecent`, ack flow only for notify-kind items).

## Settings key: `backendRef`

Today `socketPath` rides in every RPC input and settings hold one socket path. Generalize to `backendRef` (socket path | URL | handle) with the old field kept as alias. Attribution `by:"consumer"` and any auth header/token become backend config, never consumer code.

## Error / fail-soft rules

Keep today's fail-soft shape so third-party outages render the existing "unreachable" card instead of throwing: list/recent return `{items:[]}`, verdict/ack return `{recorded:false}`/`{acked:false}`, status returns `{status:"not_found"}`, all with `log.warn`.

## `version` handshake

Recommend adopting the existing `version`-style handshake op (`{version:{}}` → `{version,git_commit,build_time,binary_sha256,pid}`, probed via `approval.health`) so the consumer can probe capabilities at settings-save time.

## What stays 2fado-specific vs reusable

- Reusable/generic: pending-queue UX (toast/pill/modal, polling cadences, ack flow, `kind/link/summary` notify pattern, status lifecycle, 2-step `confirm` rendering).
- 2fado-specific (do NOT force on third parties): exec semantics (`argv/cwd/uid/env` reconstruction, `run`/`detach` op), preview risk model, Telegram fallback transport, policy `match_type` vocabulary (`exact|base|custom`), audit-log `by` conventions.

## Open questions (human sign-off)

1. Keep unix-socket JSON-lines as the blessed third-party transport, or bless HTTP too?
2. Rename `socketPath`→`backendRef` now or alias?
3. Is `run`/`notify`/`ask` producer-side required of third parties, or consumer-side (list/verdict/status) only?
