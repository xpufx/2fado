package client

import (
	"io"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"2fado/internal/protocol"
)

// clearSocketEnv blanks every env var that steers discovery so a test never
// picks up the host's real daemon sockets.
func clearSocketEnv(t *testing.T) {
	t.Helper()
	// Isolate HOME so the plugin-managed candidate under ~/.paseo can never
	// resolve to the host's real daemon during discovery.
	t.Setenv("HOME", t.TempDir())
	for _, k := range []string{
		"TWOFADO_SOCKET", "FADO_SOCKET",
		"TWOFADO_RUN_DIR", "FADO_RUN_DIR",
		"XDG_RUNTIME_DIR",
	} {
		t.Setenv(k, "")
	}
}

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

func TestResolveSocketExplicitBypassesDiscovery(t *testing.T) {
	clearSocketEnv(t)
	// Nothing listens here; an explicit path must be returned as-is without
	// any liveness probe.
	explicit := filepath.Join(t.TempDir(), "explicit.sock")
	got, err := ResolveSocket(explicit)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != explicit {
		t.Fatalf("expected %q, got %q", explicit, got)
	}
}

func TestResolveSocketDiscoversLiveCandidate(t *testing.T) {
	clearSocketEnv(t)
	live := listenUnix(t, filepath.Join(t.TempDir(), "live.sock"))
	t.Setenv("TWOFADO_SOCKET", live)

	got, err := ResolveSocket("")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != live {
		t.Fatalf("expected discovered %q, got %q", live, got)
	}
}

func TestResolveSocketNoLiveCandidateErrors(t *testing.T) {
	clearSocketEnv(t)
	t.Setenv("TWOFADO_SOCKET", filepath.Join(t.TempDir(), "dead.sock"))

	if got, err := ResolveSocket(""); err == nil {
		t.Fatalf("expected error, got socket %q", got)
	}
}

// callResolved must surface the concrete path discovery selected so
// `2fado status` can report it.
func TestCallResolvedReportsSocket(t *testing.T) {
	clearSocketEnv(t)
	live := listenUnix(t, filepath.Join(t.TempDir(), "live.sock"))
	t.Setenv("TWOFADO_SOCKET", live)

	// The accept-only fake listener closes without replying; the read
	// returns EOF, but the resolved path is what matters here.
	_, resolved, _ := callResolved("", protocol.ClientMessage{Version: &protocol.VersionRequest{}})
	if resolved != live {
		t.Fatalf("expected resolved %q, got %q", live, resolved)
	}
}

// fakeDaemon replies to each request with reply, one JSON line per conn.
func fakeDaemon(t *testing.T, path, reply string) string {
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
			go func(c net.Conn) {
				defer c.Close()
				buf := make([]byte, 4096)
				_, _ = c.Read(buf)
				_, _ = c.Write([]byte(reply + "\n"))
			}(c)
		}
	}()
	return path
}

// Status must print the discovered socket path as a `socket:` field.
func TestStatusPrintsSocketField(t *testing.T) {
	clearSocketEnv(t)
	live := fakeDaemon(t, filepath.Join(t.TempDir(), "live.sock"),
		`{"id":"req-1","status":"pending","exit":0}`)
	t.Setenv("TWOFADO_SOCKET", live)

	old := os.Stdout
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	os.Stdout = w
	code := Status("", "req-1")
	w.Close()
	os.Stdout = old

	out, _ := io.ReadAll(r)
	r.Close()
	if code != 0 {
		t.Fatalf("expected exit 0, got %d", code)
	}
	if !strings.Contains(string(out), `"socket": "`+live+`"`) {
		t.Fatalf("expected socket field %q in output:\n%s", live, out)
	}
}
