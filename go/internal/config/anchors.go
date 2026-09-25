package config

import (
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strings"
	"time"
)

func DefaultStateDir() string {
	if d := os.Getenv("XDG_STATE_HOME"); d != "" {
		return filepath.Join(d, "2fado")
	}
	home, _ := os.UserHomeDir()
	if home != "" {
		return filepath.Join(home, ".local", "state", "2fado")
	}
	return filepath.Join(string(filepath.Separator)+"var", "lib", "2fado")
}

// RunDir returns the 2fado runtime-directory override, or "" when unset.
// When set it relocates the socket and pid file as a single knob, which is
// what isolated dev/worktree instances use to avoid touching the primary
// user daemon (see `make dev` and docs/daemon-service.md). The state
// directory is independent and governed by TWOFADO_STATE_DIR.
func RunDir() string {
	return GetEnvWithFallback("TWOFADO_RUN_DIR", "FADO_RUN_DIR")
}

// DefaultSocketPath resolves the daemon's bind path, first match wins:
//
//  1. TWOFADO_SOCKET / FADO_SOCKET (explicit full path)
//  2. TWOFADO_RUN_DIR/<2fado.sock> (runtime-dir override)
//  3. $XDG_RUNTIME_DIR/2fado/2fado.sock (XDG runtime convention)
//  4. /tmp/2fado.sock (graceful fallback when no XDG runtime dir exists)
//
// This is the *daemon's* single bind path; it performs no liveness probing.
// Clients use DiscoverSocketPath, whose candidate chain is a superset that
// also covers a plugin-managed daemon and falls over dead explicit paths.
func DefaultSocketPath() string {
	if p := GetEnvWithFallback("TWOFADO_SOCKET", "FADO_SOCKET"); p != "" {
		return p
	}
	if d := RunDir(); d != "" {
		return filepath.Join(d, "2fado.sock")
	}
	if d := os.Getenv("XDG_RUNTIME_DIR"); d != "" {
		return filepath.Join(d, "2fado", "2fado.sock")
	}
	return filepath.Join(string(filepath.Separator)+"tmp", "2fado.sock")
}

// pluginSocketCandidates are the Paseo plugin supervisor's managed-daemon
// sockets (see scripts/daemon-supervisor.mjs):
// ~/.paseo/plugin-data/<publisher>/twofado/run/2fado.sock. The CLI never
// binds here; it only probes them so a plugin-managed daemon is reachable
// without env setup.
//
// The publisher segment differs by checkout ("xpufx" vs "xpufx-org"), so
// discovery lists sibling plugin-data roots rather than hardcoding one.
func pluginSocketCandidates() []string {
	home, _ := os.UserHomeDir()
	if home == "" {
		return nil
	}
	var out []string
	for _, rel := range []string{
		filepath.Join(".paseo", "plugin-data", "xpufx", "twofado", "run", "2fado.sock"),
		filepath.Join(".paseo", "plugin-data", "xpufx-org", "twofado", "run", "2fado.sock"),
	} {
		out = append(out, filepath.Join(home, rel))
	}
	return out
}

// SocketCandidates returns the ordered client-side socket discovery chain
// (deduped, empties dropped). It is a superset of DefaultSocketPath: it
// inserts the plugin-managed socket before the XDG and /tmp fallbacks.
func SocketCandidates() []string {
	var out []string
	seen := map[string]bool{}
	add := func(p string) {
		if p == "" || seen[p] {
			return
		}
		seen[p] = true
		out = append(out, p)
	}
	add(GetEnvWithFallback("TWOFADO_SOCKET", "FADO_SOCKET"))
	if d := RunDir(); d != "" {
		add(filepath.Join(d, "2fado.sock"))
	}
	for _, p := range pluginSocketCandidates() {
		add(p)
	}
	if d := os.Getenv("XDG_RUNTIME_DIR"); d != "" {
		add(filepath.Join(d, "2fado", "2fado.sock"))
	}
	add(filepath.Join(string(filepath.Separator)+"tmp", "2fado.sock"))
	return out
}

// SocketProbeTimeout bounds a single candidate's liveness probe. A live
// unix socket accepts a connection immediately; the deadline only caps a
// hung/stale path so discovery cannot stall on one candidate.
var SocketProbeTimeout = 150 * time.Millisecond

// probeLive reports whether path accepts a unix-socket connection within
// SocketProbeTimeout. The connection is closed immediately; liveness is
// existence + accept, not protocol validity.
func probeLive(path string) bool {
	d := net.Dialer{Timeout: SocketProbeTimeout}
	c, err := d.Dial("unix", path)
	if err != nil {
		return false
	}
	c.Close()
	return true
}

// DiscoverSocketPath probes SocketCandidates in order and returns the
// first live socket. An explicit TWOFADO_SOCKET/FADO_SOCKET that is dead
// does not abort discovery: a warning goes to stderr and the chain
// continues. When nothing is live the error lists every probed candidate.
func DiscoverSocketPath() (string, error) {
	return discoverFrom(SocketCandidates(), GetEnvWithFallback("TWOFADO_SOCKET", "FADO_SOCKET"))
}

// discoverFrom is the candidate-chain core, split out so tests can drive
// an explicit ordered chain without host /tmp or HOME leaks.
func discoverFrom(candidates []string, explicit string) (string, error) {
	for _, p := range candidates {
		if probeLive(p) {
			return p, nil
		}
		if explicit != "" && p == explicit {
			fmt.Fprintf(os.Stderr, "2fado: warning: %s is not live, probing further candidates\n", p)
		}
	}
	return "", fmt.Errorf("no live 2fado daemon socket found; probed: %s", strings.Join(candidates, ", "))
}

func DefaultPidFile(stateDir string) string {
	if p := GetEnvWithFallback("TWOFADO_PID_FILE", "FADO_PID_FILE"); p != "" {
		return p
	}
	if d := RunDir(); d != "" {
		return filepath.Join(d, "2fado.pid")
	}
	if stateDir == "" {
		stateDir = DefaultStateDir()
	}
	return filepath.Join(stateDir, "2fado.pid")
}

func ValidateAnchorDir(path string) error {
	fi, err := os.Lstat(path)
	if err != nil {
		return fmt.Errorf("anchor %s: %w", path, err)
	}
	if fi.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("anchor %s: symlinked anchor refused", path)
	}
	if !fi.IsDir() {
		return fmt.Errorf("anchor %s: not a directory", path)
	}
	if perm := fi.Mode().Perm(); perm&0o022 != 0 {
		return fmt.Errorf("anchor %s: mode %o too permissive", path, perm)
	}
	if st, ok := ownerUID(fi); ok {
		if int(st) != os.Geteuid() {
			return fmt.Errorf("anchor %s: foreign owner %d, want %d", path, st, os.Geteuid())
		}
	}
	return nil
}

func EnsureAnchorDir(path string) error {
	if err := os.MkdirAll(path, 0o700); err != nil {
		return err
	}
	if err := os.Chmod(path, 0o700); err != nil {
		return err
	}
	return ValidateAnchorDir(path)
}

func (c Conf) ValidateAnchors() error {
	if err := ValidateAnchorDir(c.StateDir); err != nil {
		return err
	}
	if dir := filepath.Dir(c.Socket); dir != "" {
		if fi, err := os.Lstat(dir); err == nil {
			if fi.Mode()&os.ModeSymlink != 0 {
				return fmt.Errorf("anchor %s: symlinked anchor refused", dir)
			}
			if uid, ok := ownerUID(fi); ok && int(uid) != os.Geteuid() {
				return fmt.Errorf("anchor %s: foreign owner refused", dir)
			}
		}
	}
	return nil
}
