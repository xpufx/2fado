package config

import (
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// listenUnix creates a live unix-socket listener and returns its path. The
// listener accepts and drops connections so probe dials never back up.
func listenUnix(t *testing.T, path string) string {
	t.Helper()
	l, err := net.Listen("unix", path)
	if err != nil {
		t.Fatalf("listen %s: %v", path, err)
	}
	t.Cleanup(func() { l.Close() })
	go func() {
		for {
			c, err := l.Accept()
			if err != nil {
				return
			}
			c.Close()
		}
	}()
	return path
}

// deadSocket returns a path under a temp dir that no process listens on.
func deadSocket(t *testing.T, name string) string {
	t.Helper()
	return filepath.Join(t.TempDir(), name)
}

func TestSocketCandidatesOrdering(t *testing.T) {
	clearRuntimeEnv(t)
	t.Setenv("TWOFADO_RUN_DIR", "/run/foo")
	t.Setenv("XDG_RUNTIME_DIR", "/run/xdg")
	// Force an explicit path so it must lead the chain.
	t.Setenv("TWOFADO_SOCKET", "/run/explicit.sock")

	got := SocketCandidates()
	if len(got) == 0 {
		t.Fatal("expected candidates")
	}
	if got[0] != "/run/explicit.sock" {
		t.Fatalf("expected explicit socket first, got %q", got[0])
	}
	if got[1] != filepath.Join("/run/foo", "2fado.sock") {
		t.Fatalf("expected RUN_DIR socket second, got %q", got[1])
	}

	home, _ := os.UserHomeDir()
	plugin := filepath.Join(home, ".paseo", "plugin-data", "xpufx", "twofado", "run", "2fado.sock")
	pluginAlt := filepath.Join(home, ".paseo", "plugin-data", "xpufx-org", "twofado", "run", "2fado.sock")
	if home != "" {
		idx := indexOf(got, plugin)
		if idx < 0 {
			t.Fatalf("expected plugin socket %q in chain %v", plugin, got)
		}
		if indexOf(got, pluginAlt) < 0 {
			t.Fatalf("expected sibling plugin socket %q in chain %v", pluginAlt, got)
		}
		if indexOf(got, filepath.Join("/run/xdg", "2fado", "2fado.sock")) < idx {
			t.Fatalf("expected XDG socket after plugin candidate, got %v", got)
		}
	}
	if last := got[len(got)-1]; last != filepath.Join(string(filepath.Separator)+"tmp", "2fado.sock") {
		t.Fatalf("expected /tmp socket last, got %q", last)
	}
}

func TestSocketCandidatesDedup(t *testing.T) {
	clearRuntimeEnv(t)
	t.Setenv("TWOFADO_SOCKET", filepath.Join(string(filepath.Separator)+"tmp", "2fado.sock"))
	got := SocketCandidates()
	seen := map[string]int{}
	for _, p := range got {
		seen[p]++
	}
	for p, n := range seen {
		if n > 1 {
			t.Fatalf("candidate %q appeared %d times in %v", p, n, got)
		}
	}
}

func TestDiscoverSocketPathPicksFirstLiveInOrder(t *testing.T) {
	first := listenUnix(t, filepath.Join(t.TempDir(), "first.sock"))
	second := listenUnix(t, filepath.Join(t.TempDir(), "second.sock"))
	dead := deadSocket(t, "dead.sock")

	got, err := discoverFrom([]string{dead, first, second}, "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != first {
		t.Fatalf("expected first live candidate %q, got %q", first, got)
	}
}

func TestDiscoverSocketPathExplicitDeadFallsThroughWithWarning(t *testing.T) {
	explicit := deadSocket(t, "explicit.sock")
	live := listenUnix(t, filepath.Join(t.TempDir(), "live.sock"))

	old := os.Stderr
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	os.Stderr = w

	got, derr := discoverFrom([]string{explicit, live}, explicit)

	w.Close()
	os.Stderr = old
	buf := make([]byte, 4096)
	n, _ := r.Read(buf)
	r.Close()
	warning := string(buf[:n])

	if derr != nil {
		t.Fatalf("unexpected error: %v", derr)
	}
	if got != live {
		t.Fatalf("expected fallback to %q, got %q", live, got)
	}
	if !strings.Contains(warning, explicit) || !strings.Contains(warning, "not live") {
		t.Fatalf("expected dead-explicit warning mentioning %q, got %q", explicit, warning)
	}
}

func TestDiscoverSocketPathNoneLiveError(t *testing.T) {
	a := deadSocket(t, "a.sock")
	b := deadSocket(t, "b.sock")

	got, err := discoverFrom([]string{a, b}, "")
	if err == nil {
		t.Fatalf("expected error, got socket %q", got)
	}
	if got != "" {
		t.Fatalf("expected empty socket, got %q", got)
	}
	for _, p := range []string{a, b} {
		if !strings.Contains(err.Error(), p) {
			t.Fatalf("expected error to list %q, got %q", p, err.Error())
		}
	}
	if !strings.Contains(err.Error(), "no live 2fado daemon socket found") {
		t.Fatalf("unexpected error text: %q", err.Error())
	}
}

// DiscoverSocketPath's full chain resolves a live explicit override without
// ever probing host-owned runtime paths.
func TestDiscoverSocketPathExplicitLive(t *testing.T) {
	clearRuntimeEnv(t)
	live := listenUnix(t, filepath.Join(t.TempDir(), "live.sock"))
	t.Setenv("TWOFADO_SOCKET", live)

	got, err := DiscoverSocketPath()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != live {
		t.Fatalf("expected %q, got %q", live, got)
	}
}

func indexOf(xs []string, want string) int {
	for i, x := range xs {
		if x == want {
			return i
		}
	}
	return -1
}
