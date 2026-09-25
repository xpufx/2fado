# 2fado — out-of-band approval for agent command execution

Agents are fast, literal, and surrounded by untrusted text. `2fado` puts a
human in the loop of *execution*: the agent petitions, your phone buzzes,
you tap, it runs. Everything is recorded.

No sudo involved (yet): the agent runs `2fado run -- <argv>`, the `2fado
daemon` decides (policy → allow / deny / ask-human over Telegram), executes
approved commands itself, and relays stdout + exit code. Fail closed on
every path: deny, timeout, error, or a phone left untouched.

## Status: working PoC

Live and ringing: single Go binary, stdlib-only, Telegram pager, verified end
to end (approve-by-phone, deny-by-phone, timeout, sender allowlist, one-time
tokens, audit log). Full subcommand list in
[docs/backend-cli.md](docs/backend-cli.md); the daemon socket speaks 16 typed
ops ([docs/backend-api.md](docs/backend-api.md)).

| Role | Subcommands |
|---|---|
| producer / operator | `daemon`, `run -- <argv…> [--detach]`, `ask`, `select`, `cancel`, `notify`, `ack` |
| inspector | `approve\|deny <id>`, `status <id>`, `list`, `recent`, `audit` |
| health | `version`, `socket-version` |

Runs unprivileged — approved commands execute as the daemon user only;
execution-identity escalation is not in this release. The privilege-switching
code is **not compiled into the default binary at all** (#61): `go build ./...`
excludes `internal/service/escalation_on.go`, so the shipped binary provably
lacks the escalation symbols. Build with `-tags escalation` to include it
out-of-band for development.

### Security & Privilege Boundary

`2fado` operates strictly unprivileged as the current user. **Privilege escalation is disabled in the default build and no escalation features are promised.**
- Approved commands execute strictly under the daemon user UID/GID and environment.
- Default builds strictly omit privilege-switching symbols (`internal/service/escalation_on.go` is excluded).
- No root execution, credential delegation, or identity escalation is promised or provided.

```
go/            Go module (stdlib only): single `2fado` binary, cmd/2fado +
                internal/{client,config,daemon,policy,protocol,service,store,telegram}
bin/           build output (`make build`, gitignored)
Makefile       build, systemd/dev daemon lifecycle, cross-build, dist, test, vet
scripts/       build-cross.sh, install-companion.mjs, daemon-supervisor.mjs,
                publish-github-release.sh, mirror-github.sh
etc/           2fado.conf.example (__TELEGRAM_BOT_TOKEN__ / __TELEGRAM_USER_ID__),
                policy.json.example, 2fadod.service (systemd --user unit),
                2fadod@.service (templated instance unit), 2fadod.service.example
                (future system/root layout, not used today)
docs/poc.md                       runbook: try the PoC in two terminals, no root needed
docs/daemon-service.md            systemd --user service, socket discovery, dev/worktree workflow
docs/backend-cli.md               CLI usage reference (subcommands, flags, env, exit codes)
docs/backend-api.md               daemon socket API reference for third-party consumers
docs/backend-adapter.md           third-party backend contract (design)
docs/build-and-release.md         cross-platform build runbook & release verification
docs/toctou-suspended-execution.md design decision: suspended execution vs TOCTOU race
docs/telegram-rich-surfaces.md    RFC: expandable blocks, spoilers, paged lists
```

## Try it (no root, no token)

```sh
make dev                                        # isolated daemon, repo-local socket/state
export TWOFADO_SOCKET="$PWD/.testrun/run/2fado.sock"   # the CLI probes this first
./bin/2fado run -- /bin/echo hi                 # terminal 1: waits
./bin/2fado approve <request-id>                # terminal 2: the human
make dev-stop                                   # clean shutdown
```

`make dev` pins the daemon to a repo-local socket/state (`.testrun/`) and
prints the exact env to export, so it never touches a real daemon. The export
is required: a repo-local socket is not on the client discovery chain, so
without `TWOFADO_SOCKET` the CLI would fall through to the XDG/`/tmp`
candidates and miss it. On a machine with a systemd user session,
`make service-install` + `make restart-daemon` manages `2fadod.service` via
`systemctl --user` instead. See docs/daemon-service.md.

(TWOFADO_SOCKET/TWOFADO_STATE_DIR/TWOFADO_RUN_DIR/TWOFADO_CONF env as in
docs/daemon-service.md; legacy FADO_* also supported.)

Paste a bot token + your Telegram id into the config and terminal 2
becomes your phone (outbound long-poll, no open ports).

## Cross-platform builds & packaging

`2fado` is built with Go stdlib only and cross-compiles to `linux/amd64`,
`linux/arm64`, `darwin/amd64`, and `darwin/arm64` (`CGO_ENABLED=0`).

```sh
make cross-build        # cross-compile versioned bare binaries into dist/
make dist               # also bundle legacy tarballs + generate SHA256SUMS
make install-companion  # fetch the host binary from a published release
```

`make dist VERSION=0.1.3` emits `2fado-<version>-<os>-<arch>` binaries,
`2fado-<version>-<os>-<arch>.tar.gz` archives, and a `SHA256SUMS` manifest
covering both. See [docs/build-and-release.md](docs/build-and-release.md) for
the full runbook, single-platform compilation (e.g. Apple Silicon macOS), the
`PLATFORMS=` override, and the automated Forgejo → GitHub release mirror.

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
