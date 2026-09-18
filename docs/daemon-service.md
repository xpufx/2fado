# Daemon service management & dev/worktree workflow

`2fadod` runs two ways:

1. **systemd --user** (the canonical path): `2fadod.service`, supervised,
   restarted on failure, logged via `journalctl --user`. Standard XDG paths.
2. **Fallback / isolated dev** (no systemd): `make dev` or the ad-hoc
   `restart-daemon` fallback, used by sandboxes and agents that have no
   active systemd user session.

The two paths share one socket/state resolution chain (see "Environment
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

## Environment overrides

The socket candidate chain is shared by daemon and CLI (`main.go` calls
`config.DefaultSocketPath()`), so both resolve the same path:

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
