# PoC runbook (no sudo, no root, no Telegram needed)

Everything runs as your own user. Escalation is simulated: approved
commands execute as you (or `echo`, if you'd rather not).

## Try it

```sh
export FADO_SOCKET=/tmp/2fado-$USER.sock FADO_STATE_DIR=/tmp/2fado-$USER
export FADO_CONF=$PWD/etc/2fado.conf.example   # placeholders intact
./bin/2fadod &                                   # pager=stdout, verdicts local
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
append-only audit log.

Fake for now: privilege (daemon runs as you; the `TODO(escalation)`
marker in `execute()` is where root + setuid enters), approver identity
(local CLI instead of SSO; Telegram `from.id` check is already coded for
the bot path), pager (stdout until you paste the token).

## Growing up (in order)

1. Paste token/id → phone approvals, still unprivileged.
2. Root service + systemd unit → real escalation at `execute()`.
3. Policy tiers (allow/keyboard) → fewer buzzes.
4. Approval UI + IdP → replaces inline taps when taps aren't enough.
```

> `chmod +x bin/2fado bin/2fadod` if modes didn't survive.
