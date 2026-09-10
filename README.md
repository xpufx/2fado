# 2fado — out-of-band approval for agent command execution

Agents are fast, literal, and surrounded by untrusted text. `2fado` puts a
human in the loop of *execution*: the agent petitions, your phone buzzes,
you tap, it runs. Everything is recorded.

No sudo involved (yet): the agent runs `2fado run -- <argv>`, the `2fadod`
daemon decides (policy → allow / deny / ask-human over Telegram), executes
approved commands itself, and relays stdout + exit code. Fail closed on
every path: deny, timeout, error, or a phone left untouched.

## Status: working PoC

Live and ringing: daemon + client + Telegram pager, stdlib-only Python,
verified end to end (approve-by-phone, deny-by-phone, timeout, sender
allowlist, one-time tokens, audit log). Runs unprivileged — approved
commands execute as you. Root execution (`target_user`) is implemented but
parked: inert unless deliberately launched as root.

```
go/          Go module (stdlib only): single `2fado` binary —
             `daemon | run -- <argv...> | approve|deny <id>`
             typed protocol, service/transport split (MCP fork kept open)
bin/         build output (`make build`, gitignored)
etc/         2fado.conf.example (__TELEGRAM_BOT_TOKEN__ / __TELEGRAM_USER_ID__),
             policy.json.example, 2fadod.service.example
docs/spec.md            general spec + flow diagram (the web-scale version)
docs/options/           four tool compositions researched (step-ca, Authentik,
                        Authelia, Rauthy, privacyIDEA) — all still pivotable
docs/comparison.md      option matrix
docs/recommendation.md  recommended stack + build order
docs/poc.md             runbook: try the PoC in two terminals, no root needed
```

## Try it (no root, no token)

```sh
make build
./bin/2fado daemon &                             # pager=stdout in this mode
./bin/2fado run -- /bin/echo hi                 # terminal 1: waits
./bin/2fado approve <request-id>                # terminal 2: the human
```
(FADO_SOCKET/FADO_STATE_DIR/FADO_CONF env as in docs/poc.md.)

Paste a bot token + your Telegram id into the config and terminal 2
becomes your phone (outbound long-poll, no open ports).

## Design in one breath

The traveled message is evidence; the box record is authority. Execution
reads only the stored argv — never anything that crossed a wire. The agent
petitions but never grants; the daemon owns its children (requestor can't
signal them, only ask for cancellation); the audit says who asked, who
approved, what ran, and as whom.
