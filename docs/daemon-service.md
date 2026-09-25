# Daemon service management & dev/worktree workflow

`2fadod` runs three ways:

1. **systemd --user** (the canonical path): `2fadod.service`, supervised,
   restarted on failure, logged via `journalctl --user`. Standard XDG paths.
2. **Paseo Plugin Supervisor** (desktop plugin context): embedded supervisor in
   the Paseo desktop plugin, managing lifecycle and companion binary acquisition
   without requiring systemd.
3. **Fallback / isolated dev** (no systemd): `make dev` or the ad-hoc
   `restart-daemon` fallback, used by sandboxes and agents that have no
   active systemd user session.

The paths share one socket/state resolution chain (see "Environment
overrides" below) so a client always reaches whichever daemon the
environment names.

## systemd --user service

```sh
make service-install    # build, install binary + unit, daemon-reload, enable
make restart-daemon     # systemctl --user restart 2fadod.service
make status             # systemctl --user status + socket-version
make stop-daemon        # systemctl --user stop 2fadod.service
journalctl --user -u 2fadod -f
```

- Unit: `etc/2fadod.service` → `~/.config/systemd/user/2fadod.service`
- Binary: `~/.local/bin/2fado`
- Socket: `$XDG_RUNTIME_DIR/2fado/2fado.sock` (graceful `/tmp/2fado.sock`
  fallback when `$XDG_RUNTIME_DIR` is unset)
- State: `$XDG_STATE_HOME/2fado` (default `~/.local/state/2fado`)
- Config: `~/.config/2fado/2fado.conf` (missing → placeholder/stdout pager)

`etc/2fadod.service.example` is a *system* (root) unit for a future
privileged layout; it is not used by the user workflow and is not installed.

### Multi-instance (worktrees / branches)

The template `etc/2fadod@.service` runs isolated instances:

```sh
systemctl --user start 2fadod@worktree.service
TWOFADO_SOCKET=$XDG_RUNTIME_DIR/2fado/worktree.sock ./bin/2fado socket-version
```

Each instance `%i` gets its own socket, state, and config:

| Resource | Path |
|---|---|
| socket | `$XDG_RUNTIME_DIR/2fado/<instance>.sock` |
| state | `$XDG_STATE_HOME/2fado/<instance>` |
| config | `~/.config/2fado/<instance>.conf` |

Point a build at a worktree binary by installing that binary to
`~/.local/bin/2fado` (or point `ExecStart=` at the worktree path in a drop-in)
and start `2fadod@<instance>`; the primary `2fadod.service` is untouched.

## Isolated dev instance (no systemd) — `make dev`

This is the first-class path for agents and sandboxes: everything lives under
the repo, so it works even when `/tmp` and `~/.local/state` are unwritable
and there is no systemd user bus.

```sh
make dev
# prints:
#   dev daemon ready (pid=...)
#   export TWOFADO_SOCKET=/home/you/2fado/.testrun/run/2fado.sock
#   export TWOFADO_STATE_DIR=/home/you/2fado/.testrun/state
#   ./bin/2fado socket-version
#   stop it with: make dev-stop

make dev-stop          # clean shutdown
```

`make dev` pins a branch-specific binary to a repo-local socket, state, and
pid file (`.testrun/`), with `TWOFADO_CONF` set to the committed placeholder
config — so it can never pick up a real `/etc/2fado/2fado.conf` or clash with
a running primary daemon. `.testrun/` is gitignored.

One-liner to exercise it end to end:

```sh
make dev \
  && TWOFADO_SOCKET="$PWD/.testrun/run/2fado.sock" ./bin/2fado socket-version \
  && make dev-stop
```

## Paseo Plugin Supervisor & Auto-Install (non-systemd desktop)

In the Paseo desktop plugin context (`plugins/twofado`), users can run without
`systemd --user` or manual terminal intervention. The plugin server supervises
the `2fado daemon` process directly and manages binary acquisition:

1. **Check socket first**: The supervisor checks `$TWOFADO_SOCKET`,
   `$XDG_RUNTIME_DIR/2fado/2fado.sock`, or `/tmp/2fado.sock`. If an existing
   daemon (systemd or standalone) is healthy, it adopts it as `managed: "external"`.
2. **Companion binary acquisition**:
   - At plugin install time, Paseo executes `build` preparation commands declared
     in `paseo-plugin.json`:
     `"build": [["node", "scripts/install-companion.mjs"]]`
   - `scripts/install-companion.mjs` detects the host OS and architecture
     (`linux/amd64`, `linux/arm64`, `darwin/amd64`, `darwin/arm64`), downloads
     the matching release archive, verifies SHA256 checksums from `SHA256SUMS`,
     and unpacks it into `bin/2fado`.
   - In dev/local mode, it automatically detects and uses local `dist/` archives.
   - At runtime, the plugin UI provides an on-demand fallback button to install
     the binary if initial acquisition was skipped.
3. **Embedded supervisor RPCs**:
   - `daemon.status`: reports state (`online`, `offline`, `starting`), PID,
     socket path, version, and uptime.
   - `daemon.start` / `daemon.stop` / `daemon.restart`: manages child process
     lifecycle without requiring root or systemd.
   - `daemon.logs`: streams the in-memory circular ring buffer of recent daemon
     stdout/stderr logs for troubleshooting.

## Socket discovery

The daemon binds exactly one path (`config.DefaultSocketPath`, or `SOCKET=`
in a config file). Clients do not assume that path is the live one: on every
call they probe an ordered candidate chain with a unix-dial liveness check
(150 ms per candidate) and use the first socket that accepts a connection
(`config.DiscoverSocketPath`). Candidate order, first match wins:

1. `TWOFADO_SOCKET` → `FADO_SOCKET` (explicit full path). If set but **dead**,
   discovery prints a warning to stderr and continues down the chain rather
   than failing.
2. `TWOFADO_RUN_DIR` → `<run_dir>/2fado.sock` (relocates socket **and** pid
   file as one knob; also `FADO_RUN_DIR`).
3. `~/.paseo/plugin-data/<publisher>/twofado/run/2fado.sock`: the
   plugin-supervised daemon (publisher is `xpufx`, with the `xpufx-org`
   sibling also probed). Lets the CLI reach a plugin-managed daemon with no
   env setup.
4. `$XDG_RUNTIME_DIR/2fado/2fado.sock` (XDG runtime convention).
5. `/tmp/2fado.sock` (graceful fallback).

Candidates are deduped and empty entries dropped. If none is live,
`DiscoverSocketPath` returns an error listing every probed candidate. Passing
an explicit path (tests, embedded callers) skips discovery entirely, so the
150 ms probe cost is paid only by the CLI's default path, and only until the
first live candidate.

`2fado status <id>` prints the connected socket as a `socket:` field
alongside the daemon's `StatusResponse`, so it is always clear which daemon
answered.

## Environment overrides

The socket candidate chain is shared by daemon and CLI: the daemon binds the
single deterministic `config.DefaultSocketPath()`, and the CLI probes the
superset chain above via `config.DiscoverSocketPath()` (see "Socket
discovery"). The bind path resolves in this order:

1. `TWOFADO_SOCKET` → `FADO_SOCKET` (explicit full path)
2. `TWOFADO_RUN_DIR` → `<run_dir>/2fado.sock` (relocates socket **and** pid
   file as one knob; also `FADO_RUN_DIR`)
3. `$XDG_RUNTIME_DIR` → `<runtime>/2fado/2fado.sock`
4. `/tmp/2fado.sock` (graceful fallback)

| Var(s) | Use | Default |
|---|---|---|
| `TWOFADO_SOCKET` → `FADO_SOCKET` | explicit socket path | see chain above |
| `TWOFADO_RUN_DIR` → `FADO_RUN_DIR` | runtime dir for socket + pid | unset |
| `TWOFADO_STATE_DIR` → `FADO_STATE_DIR` | state/store dir | `$XDG_STATE_HOME/2fado` |
| `TWOFADO_PID_FILE` → `FADO_PID_FILE` | explicit pid file | state dir (or run dir) |
| `TWOFADO_CONF` → `FADO_CONF` → `2FADO_CONF` | daemon config path | `/etc/2fado/2fado.conf` |

A daemon config file `SOCKET=`/`STATE_DIR=` entry sits between the env
overrides and the defaults above (`TWOFADO_*` env still wins).

## Make targets

| Target | Behavior |
|---|---|
| `build` | compile `bin/2fado` |
| `service-install` | install user unit + binary, enable (requires systemd --user) |
| `restart-daemon` | `systemctl --user restart`, else ad-hoc fallback |
| `stop-daemon` | `systemctl --user stop`, else kill fallback pid |
| `status` | systemd status (or pid/socket fallback) + socket-version |
| `dev` / `dev-stop` | isolated repo-local daemon (no systemd) |
| `ready` | `build` + `restart-daemon` + `status` |
