# Comparison

> Note (2026-09): pre-PoC research, superseded by the working Go PoC
> (`docs/poc.md`, `docs/backend-cli.md`). Kept for history; not the
> current plan.

Ratings: ✅ meets · ⚠️ partial/needs glue · ❌ misses.

| Requirement | A: step-ca + Authentik | B: step-ca + Authelia | C: Rauthy (+step-ca) | D: privacyIDEA push |
|---|---|---|---|---|
| First factor isn't a password (short-lived, revokable) | ✅ SSH certs, minutes-long | ✅ same | ✅ same (via step-ca; Rauthy can't mint programmatically) | ⚠️ push approval, but per-auth not per-credential |
| Human approves the **exact** command | ✅ UI renders stored argv, verdict bound to request id+nonce | ✅ same | ✅ same | ❌ approves auth event, command invisible |
| `htop`/`rm` display integrity | ✅ mTLS-local UI channel + id-bound verdicts (custom, per spec §1/§5) | ✅ same | ✅ same | ❌ no command shown at all |
| Id attestation at decision time | ✅ Authentik OIDC + per-app policies, step-up auth | ✅ Authelia OIDC (Certified), WebAuthn/TOTP | ✅ Rauthy OIDC, ed25519 JWTs, forward-auth | ⚠️ token ownership + phone biometrics; no identity-vs-command binding |
| Fail-closed + one-time tokens + audit of commands | ✅ in `2fadod` | ✅ in `2fadod` | ✅ in `2fadod` | ⚠️ fail-closed auth; audit covers logins, not commands |
| Autonomy tiers (auto/tap/keyboard) | ✅ policy engine | ✅ policy engine | ✅ policy engine | ❌ human paged for everything |
| Telegram as pure pager | ⚠️ needs ~30-line script (no native transport) | ⚠️ same | ⚠️ same | n/a (push replaces it) |
| Operating footprint (podman) | ❌ heaviest: server+worker+outpost+Postgres+Redis | ✅ single binary + step-ca | ✅ single binary + step-ca | ⚠️ server + DB + Firebase-dependent push |
| License posture | ✅ Apache-2.0 throughout | ✅ Apache-2.0 throughout | ✅ Apache-2.0 (PAM/NSS parts GPLv3, unused) | ⚠️ AGPL-3.0 server |
| Custom code required | `2fadod` + tiny UI + pager | `2fadod` + tiny UI + pager | `2fadod` + tiny UI + pager | ~none |

B and A differ only in the IdP seat; C differs from B only in the IdP
brand (and loses on maturity/docs). D is a different animal: least code,
most requirements missed.
