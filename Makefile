SHELL := /bin/bash
.SHELLFLAGS := -eu -o pipefail -c
GIT_COMMIT ?= $(shell git rev-parse --short HEAD 2>/dev/null || echo "unknown")
BUILD_TIME ?= $(shell date -u '+%Y-%m-%dT%H:%M:%SZ')
LDFLAGS = -X main.GitCommit=$(GIT_COMMIT) -X main.BuildTime=$(BUILD_TIME) -X main.Version=0.1.0-dev
PID_FILE ?= /tmp/2fado.pid
LOG_FILE ?= /tmp/2fadod.log
SOCKET ?= /tmp/2fado.sock

build:
	cd go && go build -ldflags "$(LDFLAGS)" -o ../bin/2fado ./cmd/2fado

restart-daemon:
	@if [ -f "$(PID_FILE)" ]; then kill `cat $(PID_FILE)` 2>/dev/null || true; sleep 0.5; rm -f "$(PID_FILE)"; fi
	@rm -f "$(SOCKET)"
	@mkdir -p bin
	@setsid ./bin/2fado daemon >>"$(LOG_FILE)" 2>&1 < /dev/null & echo $$! > "$(PID_FILE)"
	@for i in $$(seq 1 50); do [ -S "$(SOCKET)" ] && break; sleep 0.1; done
	@[ -S "$(SOCKET)" ] && echo "daemon ready pid=$$(cat $(PID_FILE)) socket=$(SOCKET)" || (echo "daemon failed to start, see $(LOG_FILE)"; exit 1)

reload-plugin: # plugin lives in paseo repo now (plugins/twofado); nothing local to reload
	@if command -v paseo >/dev/null 2>&1; then paseo plugin reload twofado; else echo "paseo CLI not available, skipping plugin reload"; fi

status:
	@echo "=== 2fado status ==="; \
	echo "repo HEAD: $$(git rev-parse --short HEAD 2>/dev/null || echo unknown) ($$(git rev-parse HEAD 2>/dev/null || echo unknown))"; \
	if [ -f "$(PID_FILE)" ]; then DPID=$$(cat $(PID_FILE)); echo "daemon PID file: $$DPID (alive: $$(kill -0 $$DPID 2>/dev/null && echo yes || echo no))"; else echo "daemon PID file: missing"; fi; \
	if [ -x ./bin/2fado ]; then echo "binary sha256: $$(sha256sum ./bin/2fado | cut -d' ' -f1)"; ./bin/2fado version || true; else echo "binary: missing (run make build)"; fi; \
	if [ -S "$(SOCKET)" ]; then echo "socket: healthy ($(SOCKET))"; ./bin/2fado socket-version || true; else echo "socket: missing ($(SOCKET))"; fi; \
	echo "plugin: paseo-side now (plugins/twofado); status below"; \
	if command -v paseo >/dev/null 2>&1; then paseo plugin status twofado 2>&1 || paseo plugin list 2>&1 | grep -i twofado || echo "plugin status unknown"; else echo "paseo CLI not available"; fi; \
	echo "=== sync verdict ==="; \
	H=$$(git rev-parse --short HEAD 2>/dev/null || echo unknown); \
	if [ -S "$(SOCKET)" ] && [ -f "$(PID_FILE)" ]; then echo "Daemon in sync with HEAD ($$H): check hashes above"; else echo "NOT in sync: daemon not running"; fi

ready: build restart-daemon status

reload: ready

vet:
	cd go && go vet ./...

fmt:
	cd go && gofmt -w .
