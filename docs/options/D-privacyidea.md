# Option D — privacyIDEA push (minimal custom code, sudo stays)

> Note (2026-09): pre-PoC research, superseded by the working Go PoC
> (`docs/poc.md`, `docs/backend-cli.md`). Kept for history; not the
> current plan.

## Tool research

**privacyIDEA** (`privacyidea/privacyidea`, AGPL-3.0, alive — server,
`privacyidea-pam`, and the `pi-authenticator` Flutter app all maintained;
only the legacy app repos are archived): MFA server with **push tokens**.
Flow with the PAM module, confirmed via docs: PAM calls
`/validate/triggerchallenge` (or `/validate/check` with PIN) → server
pushes to `pi-authenticator` → user fingerprint-confirms on the phone →
PAM polls `/validate/polltransaction` → success finalizes via
`/validate/check`. Challenge triggering is also API-callable (admin auth +
`policy_triggerchallenge`). So: `sudo` → PAM → phone buzzes → tap →
escalation proceeds. No custom UI, no IdP, no bot. `sudo` stays in the
loop (this is the one option that keeps it).

## What it does NOT give

- **No exact-command binding.** The human approves an *auth event*, not an
  argv. Nothing shows or signs the command; a rogue agent asking for
  `sudo` gets the same push prompt regardless of what follows. The
  `htop`/`rm` class of concern is simply unaddressed — policy here is
  "who may sudo," never "which command."
- **No audit of commands**, only of authentications.
- **No autonomy tiers** — every escalation pages the human (no allowlist
  short-circuit unless duplicated in sudoers, which reintroduces static
  privilege).
- AGPL-3.0 server; phone app is theirs (biometric binding included, but
  their display path, their push infra via Firebase — worth knowing for
  the threat model).

## Implementation plan

1. privacyIDEA in podman (+ DB), enroll push tokens in `pi-authenticator`.
2. `privacyidea-pam` in `/etc/pam.d/sudo`, `try_first_pass` as fits.
3. sudoers scoping as usual. Done — no `2fadod`, no UI, no Telegram.

## Feasibility: HIGH (to deploy) / FAIL (against requirements)

Deploys in an afternoon and the phone UX is the best of all options
(push + fingerprint, works from the couch). But it meets the letter
("human approves elevation") while missing the spec: no command binding,
no payload integrity, no tiers, no command audit. It is second-factor
auth, not approval of the exact command. Viable only if we downgrade the
requirements to "a human taps for every sudo" — which we already rejected
as unlivable (and as adding nothing over `sudo` + a pager).
