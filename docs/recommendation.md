# Recommendation: Option B — step-ca + Authelia (+ `2fadod` + tiny UI + pager)

> Note (2026-09): pre-PoC research, superseded by the working Go PoC
> (`docs/poc.md`, `docs/backend-cli.md`). Kept for history; not the
> current plan.

## Why B wins

Against the actual requirements (spec.md), A/B/C tie on every security
property — the differentiator is cost of ownership, and B is cheapest to
run: one small CA container plus one single-binary IdP/gate, both
Apache-2.0, both podman-trivial, no Postgres/Redis to back up. Authentik
(A) buys per-app step-up policies and event webhooks we can live without
(Authelia's per-user 2FA + our standalone pager cover both). Rauthy (C)
lost its edge when docs confirmed **no API-minted ephemeral passwords** —
as a pure IdP it offers nothing over Authelia except a nicer audit report,
with a smaller community and younger docs. privacyIDEA (D) is disqualified
on requirements, not effort: it never shows or binds the command, so the
core threat (approving *something* while the agent runs *something else*)
is untouched.

## What we build (and only this)

1. **`2fadod`** — policy engine + privileged executor + request store +
   one-time tokens + audit. Localhost only, fail-closed, no invented
   crypto. The one component with security opinions; keep it auditable in
   an afternoon.
2. **Approval UI** — dumb OIDC client of Authelia, private-net only,
   strict token validation, CSRF + one-time-token verdicts (spec §5).
3. **Pager script** — `2fadod` event → Telegram link + request id (~30 lines).
4. **`2fado` CLI** — agent-facing: mint cert (`step` CLI), call `2fadod`,
   wait on pend.

Everything else is somebody else's maintained code: step-ca mints,
Authelia attests, Telegram pages.

## Build order (each stage shippable)

1. `2fadod` + `2fado run`: default-deny + allowlist + audit. Useful day one.
2. Pend queue + UI + Authelia gate. The approval loop goes live.
3. Telegram pager. Couch compatibility.
4. Tiers/polish: keyboard-tier holds, execute-notice + cancel, env/cwd
   semantics.

## Pivot notes (held, not discarded)

- If Authelia chafes → Authentik is a drop-in seat swap (same OIDC shape,
  heavier closet). If Rauthy grows programmatic issuance → revisit C for
  the audited footprint.
- If per-command approval proves too fine-grained in practice → D's push
  model remains the escape hatch, with eyes open about what it doesn't bind.
- JumpServer/WarpGate/Teleport/SPIRE/Vault/opkssh stay on the shelf (see
  options archive): session-grain or fleet-grain tools for a command-grain
  problem.
