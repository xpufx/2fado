# Option A — step-ca (agent identity) + Authentik (SSO gate) + custom UI + 2fadod

## Tool research

**step-ca** (`smallstep/certificates`, Apache-2.0, alive): online CA for
short-lived SSH certificates. The automation-shaped hole is the **JWK
provisioner**: mint a JWT offline with the provisioner key
(`step ca token --provisioner … --ssh`), exchange it for a user cert
(`step ssh certificate`), valid for minutes. Principals pin *which* agent;
validity window is the expiry. Agent flow is two CLI calls — scriptable
without human touch. CA runs fine in podman; root cert distributed once per
box.

**Authentik** (`goauthentik/authentik`): full IdP (OIDC, SAML, LDAP
outposts) plus a **proxy outpost** with two gating modes — full proxy, or
forward-auth against your existing reverse proxy (per-app or domain-level),
injecting `X-authentik-username/email/...` headers upstream. Notification
transports are **email + in-app only** (no native Telegram — a small
event-webhook → bot script covers the pager). Per-application access
policies exist (e.g. require LAN IP, require WebAuthn re-auth for the
approval app specifically). Price: Postgres + Redis + server + worker +
outpost containers — the heaviest IdP here.

## Implementation plan

1. `step-ca` in podman (JWK provisioner, SSH CA enabled). Agent wrapper
   mints a ≤5-min user cert before each `2fado run`.
2. `2fadod` (custom, root-owned): verifies agent cert against CA root
   (principal + validity), evaluates `policy.yaml` tiers, executes
   allowlisted argv / denies / stores pend requests with one-time tokens.
3. Approval UI (custom, tiny): OIDC client of Authentik (code flow + PKCE),
   private-network only, reads pending from `2fadod` over a
   mutually-authenticated local channel, Approve/Deny burns the token.
   Strict `id_token` validation per spec §5.
4. Authentik proxy outpost (forward-auth mode) in front of the UI; public
   IdP, private app; per-app policy (LAN-only + WebAuthn step-up for the
   keyboard tier).
5. Pager: `2fadod` → Authentik event webhook (or direct) → ~30-line Telegram
   bot script posting link + request id.
6. Audit: append-only log on the box (who[sub]/what[argv]/when/policy line).

## Feasibility: HIGH

Every component does exactly its designed job; custom code is confined to
`2fadod` + a dumb UI + a pager script, none of it inventing crypto. Main
cost is operating Authentik (several containers, Postgres backups) for what
is ultimately a one-app gate.
