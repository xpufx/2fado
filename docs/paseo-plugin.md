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

## RPC contracts (Zod, both sides validated)

```ts
// shared/approval.ts
pendingList = defineRpc({
  name: "approval.list",
  input: z.object({}),
  output: z.object({ items: z.array(z.object({
    id: z.string(), argv: z.array(z.string()), host: z.string(),
    caller: z.string(), cwd: z.string(), expiresIn: z.number(),
  })) }),
});
verdict = defineRpc({
  name: "approval.verdict",
  input: z.object({ id: z.string(),
    decision: z.enum(["approve", "deny"]) }),
  output: z.object({ recorded: z.boolean() }),
});
```

## Server behavior (daemon subprocess, trusted)

- Owns the socket to `2fadod` (path from plugin settings). Polls pending
  (see `2fadod` change below) and fans out: toast per new id.
- Verdict handler: checks caller against the **approver allowlist in
  plugin settings** (fail closed when empty — no allowlist, no approvals),
  submits `{id, decision}` over the `2fadod` socket as `by: "paseo:<user>"`,
  returns `{recorded}`. Never trusts transport identity alone; the
  allowlist check happens per verdict, at decision time.
- Credentials/sockets stay in the handler, never in client code.

## `2fadod` change required (small, typed)

One new socket op alongside `run`/`verdict`: `list` → pending records
(id, argv, uid, cwd, expires). Same JSON-lines protocol, same structs
family (`protocol.PendingList` / `protocol.PendingItem`). No auth change:
localhost socket + peercreds as today.

## Identity & attestation

- Clicker identity comes from the Paseo session; the handler maps it to
  the settings allowlist per call. Empty/missing allowlist ⇒ deny.
- Verdicts carry `by: "paseo:<user>"` into the audit log next to
  Telegram's numeric ids — one log, two transports, same schema.
- One-time tokens unchanged: first recorded verdict wins (store PK);
  replays and double-taps return `recorded: false`.

## Settings screen

Socket path, approver allowlist (Paseo user ids), Telegram-fallback
toggle. Fail-closed defaults throughout.

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
