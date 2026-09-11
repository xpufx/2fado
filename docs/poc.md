# PoC runbook (no sudo, no root, no Telegram needed)

Everything runs as your own user. Escalation is simulated: approved
commands execute as you.

## Build & try it

```sh
make build
export FADO_SOCKET=/tmp/2fado.sock FADO_STATE_DIR=/tmp/2fado-state
export FADO_CONF=$PWD/etc/2fado.conf.example   # placeholders intact
./bin/2fado daemon &                             # pager=stdout, verdicts local
./bin/2fado run -- htop                          # terminal 1: waits
./bin/2fado approve <request-id>                 # terminal 2: the "OOB thing"
```

With a real bot token + approver id in the config, terminal 2 becomes
your phone: tap Approve/Deny inline, daemon long-polls `getUpdates`
(outbound only, no open ports) and burns the one-time token.

## What's real vs fake

Real seams (the grown-up system inherits these untouched): exact-argv
request record, one-time token consumed atomically (`O_EXCL` — one
verdict wins), fail-closed timeout, env scrub + cwd reconstruction,
append-only audit log, daemon-owned children the requestor can't signal.

Fake for now: privilege (daemon runs as you; `target_user` resolves to
self unless euid is 0 — escalation is parked, not live), approver
identity (local CLI instead of SSO; Telegram `from.id` check is already
coded for the bot path), pager (stdout until you paste the token).

## Growing up (in order)

1. Paste token/id → phone approvals, still unprivileged. (Done — works.)
2. Root service + systemd unit → real escalation via `target_user`.
3. Policy tiers (allow/keyboard) → fewer buzzes.
4. Approval UI + IdP → replaces inline taps when taps aren't enough.
5. `2fado kill` + disconnect-withdraw → honest cancellation (owned pgs).
```

> Binary lives in `bin/` (gitignored); `make build`.
