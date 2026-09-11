# 2fado Paseo plugin — spec v1 (second approval transport)

Status: spec. No code. Upstream facts (via Master, confirmed): pill
bodies + attached modal/popover are **human-only** (no agent read or
interaction path; buttons call plugin RPCs agents can't reach).
Timeline item **data is agent-visible** as transcript even though the
rendered card is paint-only. Design follows from that split.

## Goal

Approve/deny pending 2fado requests from inside Paseo. Same verdict
path as Telegram (one-time tokens into `2fadod`, burned on use,
fail-closed timeout); new human-only surface. Telegram stays as pager
fallback until the Paseo path proves itself.

## Surfaces (v1)

- **Toast** per new pending request: the ping. Human context, ephemeral.
- **Composer pill** carrying approval state (pending count/ids). Human-only
  content — safe home for the card entry point.
- **Modal/popover** attached to the pill: shows the exact argv + host /
  caller / cwd / expiry (rendered from `2fadod` state, never from chat)
  with **Approve / Deny** buttons. The decision surface.
- **No timeline rows in v1.** Timeline payloads are agent-readable; argv
  and verdicts must never ride in timeline data.
- Workspace panel deferred (status dashboard is a nice-to-have, not v1).

## RPC contracts (helper `defineContract`, both sides validated)

```ts
// shared/approval.ts — defineContract from paseo-plugin-helper/shared
pendingList = defineContract({
  name: "approval.list",
  input: z.object({ socketPath: z.string().min(1).optional() }),
  output: z.object({ items: z.array(z.object({
    id: z.string(), argv: z.array(z.string()), host: z.string(),
    caller: z.string(), cwd: z.string(), expiresIn: z.number(),
  })) }),
});
verdict = defineContract({
  name: "approval.verdict",
  input: z.object({ id: z.string(),
    decision: z.enum(["approve", "deny"]),
    socketPath: z.string().min(1).optional() }),
  output: z.object({ recorded: z.boolean() }),
});
// settings: defineSettingsContract (socketPath, telegramFallback),
// stored daemon-side via PluginStorage, auto-screen via
// registerHelperSettingsScreen under Settings → Plugins.
```

## Server behavior (daemon subprocess, trusted)

- Owns the socket to `2fadod` (per-call `socketPath` from the app,
  then `FADO_SOCKET` env, then `/run/2fado.sock`, then
  `/tmp/2fado-live.sock`). Polls `list` and fans out: toast per new id.
  Socket calls are wrapped in `guardRpcHandler` (5s timeout, max 4
  inflight, fail-fast) with structured `createPluginLogger` logging.
- Verdict handler submits `{id, decision, by: "paseo"}` over the `2fadod`
  socket and returns `{recorded}`. No per-call allowlist: v0.8 exposes no
  clicker identity on either runtime, so a names list would be theater —
  anyone holding the app can approve. Real gating belongs in `2fadod`
  (paseo-approvers list, daemon-side); the audit `by` field is attribution
  only.
- Credentials/sockets stay in the handler, never in client code.

## `2fadod` change required (small, typed)

One new socket op alongside `run`/`verdict`: `list` → pending records
(id, argv, uid, cwd, expires). Same JSON-lines protocol, same structs
family (`protocol.PendingList` / `protocol.PendingItem`). No auth change:
localhost socket + peercreds as today.

## Identity & attestation

- v0.8 gives the plugin no verified clicker identity, so there is no
  per-user allowlist on this transport. Verdicts carry `by: "paseo"` into
  the audit log next to Telegram's numeric ids — one log, two transports,
  same schema. Attribution only, never authority.
- One-time tokens unchanged: first recorded verdict wins (store PK);
  replays and double-taps return `recorded: false`.

## Settings screen

Socket path, Telegram-fallback toggle. No names to save — see above.

## MVP scope / non-goals

In: pill + modal + toast + settings, list + verdict RPCs, `2fadod` list
op, dry-run respected end to end (cards stamp it). Out: workspace panel,
timeline rows, confirm-tier UI (server enforces two-step; client just
renders the ⚠️ card state), push notifications outside the app.

## Verify

`npm run typecheck`, install/reload, `paseo plugin ls` → running;
pending `2fado run` → toast appears → pill → modal shows exact argv →
Approve → `recorded: true`, card closes, audit line with `by:
"paseo:<user>"`; Deny/timeout paths; empty-allowlist blocks everything;
mobile compact layout + dark theme text colors.
