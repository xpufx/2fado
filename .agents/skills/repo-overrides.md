# 2fado Repository Skill Overrides

This file defines repository-specific commands, file locations, and runtime targets for agents operating in `2fado`. Universal workflows are defined in `SKILL.md`.

---

## 1. Runtime Environment & Synchronization (Step 2.5)

- **Sync Command**: `make ready` (rebuilds binary, restarts background daemon, reloads Paseo plugin, verifies socket, and outputs diagnostic card).
- **Status Command**: `make status` (inspects PID file, binary SHA-256, socket health, and Paseo plugin status).
- **Daemon Binary**: `bin/2fado` (built from `go/cmd/2fado`).
- **PID File**: `/tmp/2fado.pid` (or `$TWOFADO_PID_FILE`).
- **Socket Path**: `/tmp/2fado.sock` (or `$TWOFADO_SOCKET`).
- **Log File**: `/tmp/2fadod.log`.
- **Paseo Plugin ID**: `twofado` (located in `plugin/`, reloaded via `paseo plugin reload twofado`).

---

## 2. Test & Validation Commands

- **Daemon Unit Tests**: `cd go && go test -v ./...`
- **Plugin Typecheck**: `npm --prefix plugin run typecheck`
- **Full Build**: `make build`
