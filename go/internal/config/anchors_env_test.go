package config

import (
	"path/filepath"
	"testing"
)

// clearRuntimeEnv blanks every env var that influences socket/pid/run-dir
// resolution so each case starts from a clean slate. t.Setenv restores the
// original values after the test, so host XDG_*/TWOFADO_* vars never leak
// between cases.
func clearRuntimeEnv(t *testing.T) {
	t.Helper()
	for _, k := range []string{
		"TWOFADO_SOCKET", "FADO_SOCKET",
		"TWOFADO_RUN_DIR", "FADO_RUN_DIR",
		"TWOFADO_PID_FILE", "FADO_PID_FILE",
		"XDG_RUNTIME_DIR",
	} {
		t.Setenv(k, "")
	}
}

func TestRunDirPrecedence(t *testing.T) {
	clearRuntimeEnv(t)
	t.Setenv("TWOFADO_RUN_DIR", "/run/twofado")
	t.Setenv("FADO_RUN_DIR", "/run/fado")
	if got := RunDir(); got != "/run/twofado" {
		t.Fatalf("expected TWOFADO_RUN_DIR to win, got %q", got)
	}
	t.Setenv("TWOFADO_RUN_DIR", "")
	if got := RunDir(); got != "/run/fado" {
		t.Fatalf("expected FADO_RUN_DIR fallback, got %q", got)
	}
	t.Setenv("FADO_RUN_DIR", "")
	if got := RunDir(); got != "" {
		t.Fatalf("expected empty RunDir, got %q", got)
	}
}

func TestDefaultSocketPathPrecedence(t *testing.T) {
	clearRuntimeEnv(t)

	// 1. Explicit full path wins over everything.
	t.Setenv("TWOFADO_SOCKET", "/x/twofado.sock")
	t.Setenv("FADO_SOCKET", "/x/fado.sock")
	t.Setenv("TWOFADO_RUN_DIR", "/x/run")
	t.Setenv("XDG_RUNTIME_DIR", "/x/xdg")
	if got := DefaultSocketPath(); got != "/x/twofado.sock" {
		t.Fatalf("expected TWOFADO_SOCKET, got %q", got)
	}

	// 2. FADO_SOCKET fallback.
	t.Setenv("TWOFADO_SOCKET", "")
	if got := DefaultSocketPath(); got != "/x/fado.sock" {
		t.Fatalf("expected FADO_SOCKET fallback, got %q", got)
	}

	// 3. TWOFADO_RUN_DIR relocates the socket dir.
	t.Setenv("FADO_SOCKET", "")
	if got := DefaultSocketPath(); got != filepath.Join("/x/run", "2fado.sock") {
		t.Fatalf("expected RUN_DIR socket, got %q", got)
	}

	// 4. XDG_RUNTIME_DIR -> <runtime>/2fado/2fado.sock.
	t.Setenv("TWOFADO_RUN_DIR", "")
	if got := DefaultSocketPath(); got != filepath.Join("/x/xdg", "2fado", "2fado.sock") {
		t.Fatalf("expected XDG socket, got %q", got)
	}

	// 5. Graceful /tmp fallback when nothing is set.
	t.Setenv("XDG_RUNTIME_DIR", "")
	if got := DefaultSocketPath(); got != filepath.Join(string(filepath.Separator)+"tmp", "2fado.sock") {
		t.Fatalf("expected /tmp fallback, got %q", got)
	}
}

func TestDefaultPidFilePrecedence(t *testing.T) {
	clearRuntimeEnv(t)

	// 1. Explicit pid file wins.
	t.Setenv("TWOFADO_PID_FILE", "/x/twofado.pid")
	if got := DefaultPidFile("/state"); got != "/x/twofado.pid" {
		t.Fatalf("expected TWOFADO_PID_FILE, got %q", got)
	}

	// 2. FADO_PID_FILE fallback.
	t.Setenv("TWOFADO_PID_FILE", "")
	t.Setenv("FADO_PID_FILE", "/x/fado.pid")
	if got := DefaultPidFile("/state"); got != "/x/fado.pid" {
		t.Fatalf("expected FADO_PID_FILE fallback, got %q", got)
	}

	// 3. RUN_DIR relocates the pid file.
	t.Setenv("FADO_PID_FILE", "")
	t.Setenv("TWOFADO_RUN_DIR", "/x/run")
	if got := DefaultPidFile("/state"); got != filepath.Join("/x/run", "2fado.pid") {
		t.Fatalf("expected RUN_DIR pid, got %q", got)
	}

	// 4. StateDir default.
	t.Setenv("TWOFADO_RUN_DIR", "")
	if got := DefaultPidFile("/state"); got != filepath.Join("/state", "2fado.pid") {
		t.Fatalf("expected state-dir pid, got %q", got)
	}
}

func TestDefaultSocketPathHonorsRunDirOverXdg(t *testing.T) {
	clearRuntimeEnv(t)
	t.Setenv("TWOFADO_RUN_DIR", "/repo/.testrun/run")
	t.Setenv("XDG_RUNTIME_DIR", "/run/user/1000")
	if got := DefaultSocketPath(); got != filepath.Join("/repo/.testrun/run", "2fado.sock") {
		t.Fatalf("expected RUN_DIR to beat XDG_RUNTIME_DIR, got %q", got)
	}
}
