# 2fado Repository Skill Overrides

This file defines repository-specific commands, file locations, and runtime targets for agents operating in `2fado`. Universal workflows are defined in `SKILL.md`.

---

## 1. Runtime Environment & Synchronization (Step 2.5)

- **Sync Command**: `make ready` (rebuilds binary, restarts background daemon, verifies socket, and outputs diagnostic card).
- **Status Command**: `make status` (inspects PID file, binary SHA-256, socket health, and paseo-side plugin status).
- **Daemon Binary**: `bin/2fado` (built from `go/cmd/2fado`).
- **PID File**: `/tmp/2fado.pid` (or `$TWOFADO_PID_FILE`).
- **Socket Path**: `/tmp/2fado.sock` (or `$TWOFADO_SOCKET`).
- **Log File**: `/tmp/2fadod.log`.
- **Paseo Plugin**: lives in the paseo repo (`plugins/twofado`, id `twofado`); this repo carries no plugin code. Socket API is the only coupling.

---

## 2. Test & Validation Commands

- **Daemon Unit Tests**: `cd go && go test -v ./...`
- **Full Build**: `make build`
