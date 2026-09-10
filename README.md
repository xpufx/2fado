# 2fado — 2FA for sudo, for autonomous agents

`2fado` (2FA daemon) is a second factor for `sudo` designed for autonomous
agent use, with an optional remote approval step.

## Problem

Agents routinely need `sudo`, but:

- NOPASSWD `sudo` is convenient and terrifying.
- Password-in-a-file `sudo` is just NOPASSWD with extra steps.
- Interactive 2FA (TOTP / push) blocks autonomy — nobody is awake at 3am to
  approve `apt install`.

`2fado` keeps a human-grade second factor without killing autonomy: routine,
policy-approved escalations succeed on their own, everything else pends for
remote approval.

## How it works

```
agent → sudo → PAM (pam_exec) → 2fado policy engine ─┬─ allow (signed, audited)
                                                      ├─ deny (audited)
                                                      └─ pend → remote approver
                                                              (e.g. phone / chat)
                                                                ├─ approve → allow
                                                                └─ deny/timeout → deny
```

1. Agent runs `sudo <cmd>`.
2. A PAM `pam_exec` hook calls `2fado check --user … --command …`.
3. `2fado` evaluates policy (`/etc/2fado/policy.yaml`):
   - `allow` rules: exact command allowlists per agent/user, with rate limits
     and expiry.
   - `require-approval` rules: push a request to the remote second factor
     (out-of-band approve/deny), fail-closed on timeout.
   - default: `deny`.
4. Every decision is appended to a signed, append-only audit log
   (`/var/log/2fado/audit.log`).
5. Optional `2fado agent` daemon holds the approval queue and serves the
   remote approver channel (TUI / webhook / Matrix / SSH — transport TBD).

## Design principles

- **Fail closed.** Any error, timeout, or missing policy = deny.
- **Least privilege.** Allowlists are exact argv matches, not prefixes. No
  shells, no wildcards that smuggle `sh -c`.
- **Autonomy where safe, human where not.** Boring, pinned commands auto-pass;
  novel or destructive ones wait for a human.
- **Auditable.** Every allow/deny/approval is logged with who, what, when, and
  the policy line that fired.
- **No secrets in agent reach.** The agent never holds the approval key; it can
  request, never grant.

## Planned layout

```
2fado/
├── README.md
├── policy.example.yaml      # example /etc/2fado/policy.yaml
├── pam.d/sudo               # pam_exec line to wire into sudo
├── src/2fado                # policy engine + CLI (check, approve, audit)
└── docs/threat-model.md     # what this does and does NOT protect against
```

## Status

Greenfield — this README is the spec. Implementation starts with the policy
engine (`2fado check`) and the PAM wiring; the remote approval transport comes
second.

## License

TBD.
