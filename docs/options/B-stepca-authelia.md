# Option B — step-ca (agent identity) + Authelia (SSO gate) + custom UI + 2fadod

## Tool research

**step-ca**: as in Option A — JWK provisioner, `step ca token` →
`step ssh certificate`, minutes-long user certs, podman-friendly.

**Authelia** (`authelia/authelia`, Apache-2.0, OpenID Certified™, alive):
does two jobs in one small binary — **forward-auth gate** (reverse proxy
`auth_request` subrequest; answers 200 + `Remote-User/Groups/Email` headers
or redirects to its portal) **and OIDC provider** for the UI's native login.
Second factors: WebAuthn/passkeys, TOTP, Duo push (proprietary — ignore).
Compared to Authentik: no proxy-outpost container, no Postgres/Redis
requirement (SQLite/file backends fine at this scale), far less to operate.
No notification transports at all — the Telegram pager is our ~30-line
script, same as everywhere. Forward-auth header trust means the reverse
proxy config must be exact (strip/overwrite `Remote-*` from clients).

## Implementation plan

1. `step-ca` in podman, JWK provisioner, as in Option A. Same agent flow.
2. `2fadod` identical to Option A (cert verify → policy → run/deny/pend).
3. Approval UI identical to Option A, but an OIDC client of **Authelia**
   instead of Authentik. Same validation checklist (spec §5).
4. Authelia (single container/binary) + reverse-proxy forward-auth in front
   of the UI; public portal, private app; two-factor policy (WebAuthn) for
   the approval subject; per-URL access rules for LAN-only if desired.
5. Pager script (Telegram Bot API, link + request id).
6. Same audit log.

## Feasibility: HIGH

Smallest operating footprint of the full-spec options: two light services
(step-ca, Authelia) + two tiny custom pieces (`2fadod`, UI) + pager script.
Gives up Authentik's per-app step-up policies and event webhooks — both
compensable (Authelia 2FA regulation per user; pager script standalone).
