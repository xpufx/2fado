SHELL := /bin/bash
.SHELLFLAGS := -eu -o pipefail -c
GIT_COMMIT ?= $(shell git rev-parse --short HEAD 2>/dev/null || echo "unknown")
BUILD_TIME ?= $(shell date -u '+%Y-%m-%dT%H:%M:%SZ')
VERSION ?= 0.1.0-dev
LDFLAGS = -X main.GitCommit=$(GIT_COMMIT) -X main.BuildTime=$(BUILD_TIME) -X main.Version=$(VERSION)
DIST_DIR ?= $(CURDIR)/dist

# ---------------------------------------------------------------------------
# Runtime anchors. These mirror go/internal/config/anchors.go so `make` and
# the daemon agree on the canonical socket/state paths without duplicating
# the candidate chain. Override any of these on the command line, or set
# TWOFADO_SOCKET/TWOFADO_STATE_DIR/TWOFADO_RUN_DIR in the environment.
# ---------------------------------------------------------------------------
XDG_RUNTIME_DIR ?= $(shell printenv XDG_RUNTIME_DIR 2>/dev/null || true)
XDG_STATE_HOME ?= $(shell printenv XDG_STATE_HOME 2>/dev/null || true)

# Canonical socket: $XDG_RUNTIME_DIR/2fado/2fado.sock, graceful /tmp fallback.
ifneq ($(strip $(XDG_RUNTIME_DIR)),)
RUN_DIR := $(XDG_RUNTIME_DIR)/2fado
else
RUN_DIR := /tmp
endif
ifneq ($(strip $(XDG_STATE_HOME)),)
STATE_DIR ?= $(XDG_STATE_HOME)/2fado
else
STATE_DIR ?= $(HOME)/.local/state/2fado
endif

SOCKET ?= $(RUN_DIR)/2fado.sock
PID_FILE ?= $(RUN_DIR)/2fado.pid
LOG_FILE ?= $(RUN_DIR)/2fadod.log

# systemd --user is "active" only when we can actually reach the user
# manager over its bus. Sandboxes/CI frequently have no user session; those
# environments take the ad-hoc fallback path (or `make dev`) instead.
SYSTEMD_USER_ACTIVE := $(shell command -v systemctl >/dev/null 2>&1 && systemctl --user show-environment >/dev/null 2>&1 && echo yes || echo no)

# Dev/worktree instance: everything lives under the repo so agents can run
# an isolated daemon even when /tmp and ~/.local/state are unwritable.
DEV_ROOT ?= $(CURDIR)/.testrun
DEV_RUN_DIR ?= $(DEV_ROOT)/run
DEV_STATE_DIR ?= $(DEV_ROOT)/state
DEV_SOCKET ?= $(DEV_RUN_DIR)/2fado.sock
DEV_PID_FILE ?= $(DEV_RUN_DIR)/2fado.pid
DEV_LOG_FILE ?= $(DEV_ROOT)/2fadod.log

.PHONY: build test cross-build dist clean restart-daemon restart-daemon-fallback stop-daemon stop-daemon-fallback \
	service-install dev dev-stop status vet fmt reload ready reload-plugin \
	verify-escalation-excluded test-escalation test install-companion

build:
	cd go && go build -ldflags "$(LDFLAGS)" -o ../bin/2fado ./cmd/2fado

install-companion:
	node scripts/install-companion.mjs

# --- systemd --user management --------------------------------------------

service-install: build
	@if [ "$(SYSTEMD_USER_ACTIVE)" != "yes" ]; then \
		echo "no active systemd --user session: cannot install a user unit here"; \
		echo "use 'make dev' for an isolated no-systemd daemon instead"; \
		exit 1; \
	fi
	@install -d "$(HOME)/.local/bin" "$(HOME)/.config/systemd/user" "$(HOME)/.config/2fado"
	@install -m 0755 bin/2fado "$(HOME)/.local/bin/2fado"
	@install -m 0644 etc/2fadod.service "$(HOME)/.config/systemd/user/2fadod.service"
	@install -m 0644 "etc/2fadod@.service" "$(HOME)/.config/systemd/user/2fadod@.service"
	@systemctl --user daemon-reload
	@systemctl --user enable 2fadod.service
	@echo "installed ~/.config/systemd/user/2fadod.service + binary ~/.local/bin/2fado"
	@echo "start it with: make restart-daemon"

restart-daemon:
	@if [ "$(SYSTEMD_USER_ACTIVE)" = "yes" ]; then \
		systemctl --user restart 2fadod.service || { \
			echo "systemctl restart failed; is the unit installed? run 'make service-install'"; exit 1; \
		}; \
		for i in $$(seq 1 50); do [ -S "$(SOCKET)" ] && break; sleep 0.1; done; \
		if [ -S "$(SOCKET)" ]; then \
			echo "daemon restarted via systemctl --user (socket=$(SOCKET))"; \
		else \
			echo "systemd accepted restart but socket not ready at $(SOCKET)"; \
			echo "check: journalctl --user -u 2fadod -n 50"; exit 1; \
		fi; \
	else \
		echo "no active systemd --user session; falling back to ad-hoc supervision"; \
		$(MAKE) --no-print-directory restart-daemon-fallback; \
	fi

stop-daemon:
	@if [ "$(SYSTEMD_USER_ACTIVE)" = "yes" ]; then \
		systemctl --user stop 2fadod.service || true; \
		echo "stopped 2fadod.service via systemctl --user"; \
	else \
		$(MAKE) --no-print-directory stop-daemon-fallback; \
	fi

# --- no-systemd fallback (first-class: agents/sandboxes use this) --------

restart-daemon-fallback: build
	@if [ -f "$(PID_FILE)" ]; then kill $$(cat $(PID_FILE)) 2>/dev/null || true; sleep 0.5; rm -f "$(PID_FILE)"; fi
	@rm -f "$(SOCKET)"
	@mkdir -p "$(RUN_DIR)" bin
	@TWOFADO_SOCKET="$(SOCKET)" TWOFADO_STATE_DIR="$(STATE_DIR)" \
		setsid ./bin/2fado daemon >>"$(LOG_FILE)" 2>&1 < /dev/null & echo $$! > "$(PID_FILE)"
	@for i in $$(seq 1 50); do [ -S "$(SOCKET)" ] && break; sleep 0.1; done
	@if [ -S "$(SOCKET)" ]; then \
		echo "daemon ready pid=$$(cat $(PID_FILE)) socket=$(SOCKET)"; \
	else \
		echo "daemon failed to start, see $(LOG_FILE)"; exit 1; \
	fi

stop-daemon-fallback:
	@if [ -f "$(PID_FILE)" ]; then \
		kill $$(cat $(PID_FILE)) 2>/dev/null || true; \
		sleep 0.5; rm -f "$(PID_FILE)" "$(SOCKET)"; \
		echo "stopped fallback daemon (pid file $(PID_FILE))"; \
	else \
		echo "no fallback pid file at $(PID_FILE); nothing to stop"; \
	fi

# --- isolated dev instance (branch/worktree, no systemd) ------------------

dev: build
	@rm -f "$(DEV_SOCKET)" "$(DEV_PID_FILE)"
	@mkdir -p "$(DEV_RUN_DIR)" "$(DEV_STATE_DIR)"
	@if [ -f "$(DEV_PID_FILE)" ]; then kill $$(cat $(DEV_PID_FILE)) 2>/dev/null || true; sleep 0.3; rm -f "$(DEV_PID_FILE)"; fi
	@TWOFADO_SOCKET="$(DEV_SOCKET)" \
		TWOFADO_STATE_DIR="$(DEV_STATE_DIR)" \
		TWOFADO_RUN_DIR="$(DEV_RUN_DIR)" \
		TWOFADO_CONF="$(CURDIR)/etc/2fado.conf.example" \
		setsid ./bin/2fado daemon >>"$(DEV_LOG_FILE)" 2>&1 < /dev/null & echo $$! > "$(DEV_PID_FILE)"
	@for i in $$(seq 1 50); do [ -S "$(DEV_SOCKET)" ] && break; sleep 0.1; done
	@if [ -S "$(DEV_SOCKET)" ]; then \
		echo "dev daemon ready (pid=$$(cat $(DEV_PID_FILE)))"; \
		echo "  export TWOFADO_SOCKET=$(DEV_SOCKET)"; \
		echo "  export TWOFADO_STATE_DIR=$(DEV_STATE_DIR)"; \
		echo "  ./bin/2fado socket-version"; \
		echo "stop it with: make dev-stop"; \
	else \
		echo "dev daemon failed to start, see $(DEV_LOG_FILE)"; exit 1; \
	fi

dev-stop:
	@if [ -f "$(DEV_PID_FILE)" ]; then \
		kill $$(cat $(DEV_PID_FILE)) 2>/dev/null || true; \
		sleep 0.3; rm -f "$(DEV_PID_FILE)" "$(DEV_SOCKET)"; \
		echo "stopped dev daemon (pid file $(DEV_PID_FILE))"; \
	else \
		echo "no dev pid file at $(DEV_PID_FILE); nothing to stop"; \
	fi

reload-plugin: # plugin lives in paseo repo now (plugins/twofado); nothing local to reload
	@if command -v paseo >/dev/null 2>&1; then paseo plugin reload twofado; else echo "paseo CLI not available, skipping plugin reload"; fi

status:
	@echo "=== 2fado status ==="; \
	echo "repo HEAD: $$(git rev-parse --short HEAD 2>/dev/null || echo unknown) ($$(git rev-parse HEAD 2>/dev/null || echo unknown))"; \
	if [ "$(SYSTEMD_USER_ACTIVE)" = "yes" ]; then \
		echo "systemd --user: active"; \
		if systemctl --user is-enabled 2fadod.service >/dev/null 2>&1; then \
			echo "unit 2fadod.service: enabled"; \
		else \
			echo "unit 2fadod.service: not enabled (run make service-install)"; \
		fi; \
		systemctl --user --no-pager --lines=5 status 2fadod.service 2>&1 | sed 's/^/  /' || true; \
	else \
		echo "systemd --user: no active session (fallback supervision in use)"; \
		if [ -f "$(PID_FILE)" ]; then \
			DPID=$$(cat $(PID_FILE)); \
			echo "daemon PID file: $$DPID (alive: $$(kill -0 $$DPID 2>/dev/null && echo yes || echo no))"; \
		else \
			echo "daemon PID file: missing"; \
		fi; \
	fi; \
	if [ -x ./bin/2fado ]; then echo "binary sha256: $$(sha256sum ./bin/2fado | cut -d' ' -f1)"; ./bin/2fado version || true; else echo "binary: missing (run make build)"; fi; \
	if [ -S "$(SOCKET)" ]; then echo "socket: healthy ($(SOCKET))"; TWOFADO_SOCKET="$(SOCKET)" ./bin/2fado socket-version || true; else echo "socket: missing ($(SOCKET))"; fi; \
	echo "plugin: paseo-side now (plugins/twofado); status below"; \
	if command -v paseo >/dev/null 2>&1; then paseo plugin status twofado 2>&1 || paseo plugin list 2>&1 | grep -i twofado || echo "plugin status unknown"; else echo "paseo CLI not available"; fi; \
	echo "=== sync verdict ==="; \
	H=$$(git rev-parse --short HEAD 2>/dev/null || echo unknown); \
	if [ -S "$(SOCKET)" ]; then echo "Daemon socket up (HEAD $$H); compare binary sha256 above with socket-version binary_sha256"; else echo "NOT in sync: daemon not running"; fi

ready: build restart-daemon status

reload: ready

vet:
	cd go && go vet ./...

# #61: the default build must exclude the escalation code entirely. This
# target proves it: escalation_on.go must not appear in the default file
# set, and resolveCredential must be undefined without the tag.
verify-escalation-excluded:
	@cd go && \
	  if go list -f '{{range .GoFiles}}{{.}} {{end}}' ./internal/service | grep -q escalation_on.go; then \
	    echo "FAIL: escalation_on.go compiled into default build"; exit 1; \
	  fi; \
	  if go list -tags escalation -f '{{range .GoFiles}}{{.}} {{end}}' ./internal/service | grep -q escalation_on.go; then \
	    echo "ok: default excludes escalation_on.go; -tags escalation includes it"; \
	  else \
	    echo "FAIL: -tags escalation does not include escalation_on.go"; exit 1; \
	  fi
	@cd go && go test -tags escalation ./internal/service/ -run TestResolveCredential -count=1 >/dev/null && echo "ok: escalation tests pass under tag"

test-escalation:
	cd go && go test -tags escalation ./...

test:
	cd go && go test ./...

fmt:
	cd go && gofmt -w .

cross-build:
	@VERSION="$(VERSION)" DIST_DIR="$(DIST_DIR)" ./scripts/build-cross.sh

dist:
	@VERSION="$(VERSION)" DIST_DIR="$(DIST_DIR)" ./scripts/build-cross.sh --package

clean:
	rm -rf bin/2fado $(DEV_ROOT) $(DIST_DIR)

