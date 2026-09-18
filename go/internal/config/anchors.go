package config

import (
	"fmt"
	"os"
	"path/filepath"
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

// DefaultSocketPath resolves the daemon socket:
//
//  1. TWOFADO_SOCKET / FADO_SOCKET (explicit full path)
//  2. TWOFADO_RUN_DIR/<2fado.sock> (runtime-dir override)
//  3. $XDG_RUNTIME_DIR/2fado/2fado.sock (XDG runtime convention)
//  4. /tmp/2fado.sock (graceful fallback when no XDG runtime dir exists)
//
// The CLI client resolves through the same function so client and daemon
// agree on the canonical socket without duplicating the candidate chain.
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
