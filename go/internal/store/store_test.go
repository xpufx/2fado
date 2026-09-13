package store

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"2fado/internal/protocol"
)

func TestPruneKeepsLivePetitions(t *testing.T) {
	st := New(t.TempDir())
	if err := st.Init(); err != nil {
		t.Fatal(err)
	}
	live := protocol.PendingRecord{
		Argv: []string{"/bin/echo", "hi"}, UID: 1000,
		Cwd: "/tmp", Expires: time.Now().Add(time.Hour).Unix(),
		Step: "initial",
	}
	if err := st.Save(live, "live-1"); err != nil {
		t.Fatal(err)
	}
	old := protocol.PendingRecord{
		Argv: []string{"/bin/rm", "-rf", "/"}, UID: 1000,
		Cwd: "/tmp", Expires: time.Now().Add(-2 * time.Hour).Unix(),
		Step: "initial",
	}
	if err := st.Save(old, "stale-1"); err != nil {
		t.Fatal(err)
	}
	ancient := time.Now().Add(-48 * time.Hour)
	for _, suf := range []string{".json", ".verdict", ".result"} {
		p := filepath.Join(st.Pending, "stale-1"+suf)
		if suf == ".json" {
			_ = os.Chtimes(p, ancient, ancient)
			continue
		}
		_ = os.WriteFile(p, []byte("{}"), 0o600)
		_ = os.Chtimes(p, ancient, ancient)
	}
	if n, err := st.Prune(24 * time.Hour); err != nil || n != 1 {
		t.Fatalf("Prune = (%d, %v), want (1, nil)", n, err)
	}
	if _, err := st.Load("live-1"); err != nil {
		t.Errorf("live petition pruned: %v", err)
	}
	if _, err := st.Load("stale-1"); err == nil {
		t.Error("stale decided/expired record survived pruning")
	}
}

func TestPruneKeepsFreshDecided(t *testing.T) {
	st := New(t.TempDir())
	if err := st.Init(); err != nil {
		t.Fatal(err)
	}
	rec := protocol.PendingRecord{
		Argv: []string{"/bin/echo", "hi"}, UID: 1000,
		Cwd: "/tmp", Expires: time.Now().Add(time.Hour).Unix(),
		Step: "initial",
	}
	if err := st.Save(rec, "fresh-1"); err != nil {
		t.Fatal(err)
	}
	if !st.Consume("fresh-1", "approve", "tester") {
		t.Fatal("consume failed")
	}
	if n, err := st.Prune(24 * time.Hour); err != nil || n != 0 {
		t.Fatalf("Prune = (%d, %v), want (0, nil): fresh verdicts must survive", n, err)
	}
	if st.Verdict("fresh-1") == nil {
		t.Error("fresh verdict pruned too early")
	}
}
