# 2fado — out-of-band approval for agent command execution

Agents are fast, literal, and surrounded by untrusted text. `2fado` puts a
human in the loop of *execution*: the agent petitions, your phone buzzes,
you tap, it runs. Everything is recorded.

No sudo involved (yet): the agent runs `2fado run -- <argv>`, the `2fado
daemon` decides (policy → allow / deny / ask-human over Telegram), executes
approved commands itself, and relays stdout + exit code. Fail closed on
every path: deny, timeout, error, or a phone left untouched.

## Status: working PoC

Live and ringing: single Go binary (`2fado daemon | run | approve|deny |
notify | ack | status | version | socket-version`), stdlib-only, Telegram
pager, verified end to end (approve-by-phone, deny-by-phone, timeout,
sender allowlist, one-time tokens, audit log).
Runs unprivileged — approved commands execute as you. Root execution
(`target_user`) is implemented but parked: inert unless deliberately
launched as root.

```
go/          Go module (stdlib only): single `2fado` binary —
              `daemon | run -- <argv...> | approve|deny <id> |
              notify | ack <id> | status <id> | version | socket-version`
              typed protocol, service/transport split (MCP fork kept open)
bin/         build output (`make build`, gitignored)
etc/         2fado.conf.example (__TELEGRAM_BOT_TOKEN__ / __TELEGRAM_USER_ID__),
              policy.json.example, 2fadod.service.example
docs/poc.md             runbook: try the PoC in two terminals, no root needed
docs/backend-cli.md     CLI usage reference
docs/backend-api.md     daemon socket API reference for third-party consumers
docs/backend-adapter.md third-party backend contract (design)
docs/toctou-suspended-execution.md
                        design decision: suspended execution vs TOCTOU race
```

## Try it (no root, no token)

```sh
make build
./bin/2fado daemon &                             # pager=stdout in this mode
./bin/2fado run -- /bin/echo hi                 # terminal 1: waits
./bin/2fado approve <request-id>                # terminal 2: the human
```
(TWOFADO_SOCKET/TWOFADO_STATE_DIR/TWOFADO_CONF env as in docs/poc.md; legacy FADO_* also supported.)

Paste a bot token + your Telegram id into the config and terminal 2
becomes your phone (outbound long-poll, no open ports).

## Design in one breath

The traveled message is evidence; the box record is authority. Execution
reads only the stored argv — never anything that crossed a wire. The agent
petitions but never grants; the daemon owns its children (requestor can't
signal them, only ask for cancellation); the audit says who asked, who
approved, what ran, and as whom.

## License

Copyright (C) 2026 xpufx.

This program is free software: you can redistribute it and/or modify it
under the terms of the GNU General Public License as published by the Free
Software Foundation, either version 3 of the License, or (at your option)
any later version.

This program is distributed in the hope that it will be useful, but WITHOUT
ANY WARRANTY; without even the implied warranty of MERCHANTABILITY or
FITNESS FOR A PARTICULAR PURPOSE. See the GNU General Public License for
more details.

You should have received a copy of the GNU General Public License along
with this program. If not, see <https://www.gnu.org/licenses/>.
See `LICENSE` for the full text (GPL-3.0-or-later).
