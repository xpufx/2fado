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

func TestAuditQueryFiltersAndPagination(t *testing.T) {
	st := New(t.TempDir())
	if err := st.Init(); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 5; i++ {
		ev := "exec"
		if i%2 == 0 {
			ev = "request"
		}
		st.Append(protocol.AuditEvent{Ev: ev, UID: 1000, RID: "rid-a"})
	}
	st.Append(protocol.AuditEvent{Ev: "deny", UID: 2000, RID: "rid-b"})

	// Newest first, unfiltered.
	all := st.AuditQuery(protocol.AuditQueryRequest{})
	if len(all.Items) != 6 {
		t.Fatalf("unfiltered audit = %d items, want 6", len(all.Items))
	}
	if all.NextCursor != "" {
		t.Fatalf("single page should exhaust cursor, got %q", all.NextCursor)
	}
	if all.Items[len(all.Items)-1].RID != "rid-a" {
		t.Fatalf("oldest item = %+v, want rid-a", all.Items[len(all.Items)-1])
	}

	// Exact-match ev filter.
	reqs := st.AuditQuery(protocol.AuditQueryRequest{Ev: "request"})
	if len(reqs.Items) != 3 {
		t.Fatalf("ev=request = %d items, want 3", len(reqs.Items))
	}
	for _, it := range reqs.Items {
		if it.Ev != "request" {
			t.Fatalf("ev filter leaked %+v", it)
		}
	}

	// RID and UID filters AND together.
	filtered := st.AuditQuery(protocol.AuditQueryRequest{ID: "rid-a", UID: 1000})
	if len(filtered.Items) != 5 {
		t.Fatalf("id+uid filter = %d items, want 5", len(filtered.Items))
	}
	none := st.AuditQuery(protocol.AuditQueryRequest{ID: "rid-a", UID: 2000})
	if len(none.Items) != 0 {
		t.Fatalf("id+uid mismatch = %d items, want 0", len(none.Items))
	}

	// Bounded pagination: page of 2, cursor walks older pages.
	page1 := st.AuditQuery(protocol.AuditQueryRequest{Limit: 2})
	if len(page1.Items) != 2 || page1.NextCursor == "" {
		t.Fatalf("page1 = %d items, cursor %q", len(page1.Items), page1.NextCursor)
	}
	page2 := st.AuditQuery(protocol.AuditQueryRequest{Limit: 2, Cursor: page1.NextCursor})
	if len(page2.Items) != 2 || page2.NextCursor == "" {
		t.Fatalf("page2 = %d items, cursor %q", len(page2.Items), page2.NextCursor)
	}
	page3 := st.AuditQuery(protocol.AuditQueryRequest{Limit: 2, Cursor: page2.NextCursor})
	if len(page3.Items) != 2 || page3.NextCursor != "" {
		t.Fatalf("page3 = %d items, cursor %q (want exhausted)", len(page3.Items), page3.NextCursor)
	}
	seen := map[string]bool{}
	for _, it := range append(append(page1.Items, page2.Items...), page3.Items...) {
		seen[it.RID] = true
	}
	if !seen["rid-b"] || !seen["rid-a"] {
		t.Fatalf("pagination missed records: %v", seen)
	}
}

func TestAuditQueryEmptyAndBadCursor(t *testing.T) {
	st := New(t.TempDir())
	if err := st.Init(); err != nil {
		t.Fatal(err)
	}
	empty := st.AuditQuery(protocol.AuditQueryRequest{})
	if empty.Items == nil {
		t.Fatal("empty audit must return non-nil items slice")
	}
	if len(empty.Items) != 0 {
		t.Fatalf("empty audit = %d items, want 0", len(empty.Items))
	}
	bad := st.AuditQuery(protocol.AuditQueryRequest{Cursor: "not-a-number"})
	if len(bad.Items) != 0 || bad.NextCursor != "" {
		t.Fatalf("bad cursor = %+v, want empty", bad)
	}
}

func TestAsUIDExposedOnListStatusRecent(t *testing.T) {
	st := New(t.TempDir())
	if err := st.Init(); err != nil {
		t.Fatal(err)
	}
	now := time.Now().Unix()
	rec := protocol.PendingRecord{Argv: []string{"/bin/echo"}, UID: 1000, AsUID: 33, Cwd: "/tmp", Expires: now + 60}
	if err := st.Save(rec, "rid-asuid"); err != nil {
		t.Fatal(err)
	}
	items := st.List(now)
	if len(items) != 1 || items[0].AsUID != 33 {
		t.Fatalf("List AsUID = %+v, want 33", items)
	}
	if got := st.Status("rid-asuid", now); got.AsUID != 33 {
		t.Fatalf("Status AsUID = %d, want 33", got.AsUID)
	}
	st.Consume("rid-asuid", "deny", "t")
	st.SaveResult("rid-asuid", -1, "")
	found := false
	for _, r := range st.Recent(10) {
		if r.ID == "rid-asuid" {
			found = true
			if r.AsUID != 33 {
				t.Fatalf("Recent AsUID = %d, want 33", r.AsUID)
			}
		}
	}
	if !found {
		t.Fatal("Recent missing rid-asuid")
	}
}
