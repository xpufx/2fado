package service

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"2fado/internal/policy"
	"2fado/internal/protocol"
)

func writeExec(t *testing.T, path, body string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(body), 0o755); err != nil {
		t.Fatal(err)
	}
}

func saveFD(t *testing.T, svc Service, rid string, pin *protocol.FDPin, argv []string, cwd string) {
	t.Helper()
	if err := svc.Store.Save(protocol.PendingRecord{
		Argv: argv, UID: 1000, Cwd: cwd,
		Expires: time.Now().Add(60 * time.Second).Unix(), FD: pin,
	}, rid); err != nil {
		t.Fatal(err)
	}
}

func TestSnapshotEntryGraceful(t *testing.T) {
	if snapshotEntry(nil, t.TempDir()) != nil {
		t.Error("empty argv should yield nil pin")
	}
	if snapshotEntry([]string{""}, t.TempDir()) != nil {
		t.Error("blank argv0 should yield nil pin")
	}
	if snapshotEntry([]string{filepath.Join(t.TempDir(), "nope")}, t.TempDir()) != nil {
		t.Error("missing entrypoint should yield nil pin")
	}
	dir := t.TempDir()
	sub := filepath.Join(dir, "sub")
	if err := os.Mkdir(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	if snapshotEntry([]string{sub}, dir) != nil {
		t.Error("directory entrypoint should yield nil pin")
	}
}

func TestSnapshotEntryPinsBinary(t *testing.T) {
	pin := snapshotEntry([]string{"/bin/echo", "hi"}, t.TempDir())
	if pin == nil {
		t.Fatal("expected a pin for /bin/echo")
	}
	if pin.SHA256 == "" || pin.Path == "" || pin.Resolved == "" {
		t.Errorf("pin missing identity fields: %+v", pin)
	}
	if pin.IsScript {
		t.Error("/bin/echo should not parse as a script")
	}
}

func TestSnapshotEntryDetectsScript(t *testing.T) {
	dir := t.TempDir()
	script := filepath.Join(dir, "hello.sh")
	writeExec(t, script, "#!/bin/sh\necho hi\n")
	pin := snapshotEntry([]string{script}, dir)
	if pin == nil {
		t.Fatal("expected a pin for the script")
	}
	if !pin.IsScript || pin.Interpreter != "/bin/sh" {
		t.Errorf("script pin = isScript=%v interp=%q, want true /bin/sh", pin.IsScript, pin.Interpreter)
	}
}

func TestCheckFDDriftSkipsWithoutPin(t *testing.T) {
	svc := testService(t, policy.Policy{})
	saveFD(t, svc, "fd-nopin", nil, []string{"/bin/echo"}, t.TempDir())
	if d := svc.checkFDDrift("fd-nopin"); d != nil {
		t.Errorf("nil pin should skip, got %+v", d)
	}
	if d := svc.checkFDDrift("fd-missing"); d != nil {
		t.Errorf("missing record should skip, got %+v", d)
	}
}

func TestCheckFDDriftClean(t *testing.T) {
	dir := t.TempDir()
	script := filepath.Join(dir, "s.sh")
	writeExec(t, script, "#!/bin/sh\necho v1\n")
	svc := testService(t, policy.Policy{})
	saveFD(t, svc, "fd-clean", snapshotEntry([]string{script}, dir), []string{script}, dir)
	if d := svc.checkFDDrift("fd-clean"); d != nil {
		t.Errorf("unchanged entrypoint should not drift, got %+v", d)
	}
}

func TestCheckFDDriftBinarySwapBlocks(t *testing.T) {
	dir := t.TempDir()
	bin := filepath.Join(dir, "tool.sh")
	writeExec(t, bin, "#!/bin/sh\necho v1\n")
	svc := testService(t, policy.Policy{})
	saveFD(t, svc, "fd-swap", snapshotEntry([]string{bin}, dir), []string{bin}, dir)
	writeExec(t, bin, "#!/bin/sh\necho HACKED\n")
	d := svc.checkFDDrift("fd-swap")
	if d == nil || !d.Drift || !d.Block {
		t.Fatalf("binary swap should block, got %+v", d)
	}
	if !strings.Contains(d.Reason, "content changed") {
		t.Errorf("reason should name content change, got %q", d.Reason)
	}
}

func TestCheckFDDriftSymlinkRedirectBlocks(t *testing.T) {
	dir := t.TempDir()
	a := filepath.Join(dir, "a.sh")
	b := filepath.Join(dir, "b.sh")
	writeExec(t, a, "#!/bin/sh\necho A\n")
	writeExec(t, b, "#!/bin/sh\necho B\n")
	link := filepath.Join(dir, "tool")
	if err := os.Symlink(a, link); err != nil {
		t.Fatal(err)
	}
	svc := testService(t, policy.Policy{})
	saveFD(t, svc, "fd-link", snapshotEntry([]string{link}, dir), []string{link}, dir)
	if err := os.Remove(link); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(b, link); err != nil {
		t.Fatal(err)
	}
	d := svc.checkFDDrift("fd-link")
	if d == nil || !d.Drift || !d.Block {
		t.Fatalf("symlink redirect should block, got %+v", d)
	}
}

func TestPinnedExecRefusesSwappedBytes(t *testing.T) {
	dir := t.TempDir()
	script := filepath.Join(dir, "pinned.sh")
	writeExec(t, script, "#!/bin/sh\necho pinned-v1\n")
	pin := snapshotEntry([]string{script}, dir)
	if pin == nil {
		t.Fatal("expected a pin")
	}
	writeExec(t, script, "#!/bin/sh\necho hacked-v2\n")
	svc := testService(t, policy.Policy{})
	code, out, _ := svc.runPinned([]string{script}, dir, map[string]string{}, pin, nil)
	if code == 0 || !strings.Contains(out, "fd-drift:") {
		t.Fatalf("swapped entrypoint should refuse, got exit %d out %q", code, out)
	}
	if strings.Contains(out, "hacked-v2") {
		t.Errorf("attacker bytes must never execute, out = %q", out)
	}
}

func TestPinnedExecBinary(t *testing.T) {
	pin := snapshotEntry([]string{"/bin/echo", "hi"}, t.TempDir())
	if pin == nil {
		t.Skip("no pinnable /bin/echo")
	}
	svc := testService(t, policy.Policy{})
	code, out, _ := svc.runPinned([]string{"/bin/echo", "pinned-ok"}, t.TempDir(), map[string]string{}, pin, nil)
	if code != 0 || !strings.Contains(out, "pinned-ok") {
		t.Fatalf("pinned binary exec = exit %d out %q", code, out)
	}
}

func TestPinnedExecScript(t *testing.T) {
	dir := t.TempDir()
	script := filepath.Join(dir, "hello.sh")
	writeExec(t, script, "#!/bin/sh\necho script-pinned-ok\n")
	pin := snapshotEntry([]string{script}, dir)
	if pin == nil {
		t.Fatal("expected a pin")
	}
	svc := testService(t, policy.Policy{})
	code, out, _ := svc.runPinned([]string{script}, dir, map[string]string{}, pin, nil)
	if code != 0 || !strings.Contains(out, "script-pinned-ok") {
		t.Fatalf("pinned script exec = exit %d out %q", code, out)
	}
}

func TestRunApprovedBlocksOnFDDrift(t *testing.T) {
	dir := t.TempDir()
	bin := filepath.Join(dir, "tool.sh")
	writeExec(t, bin, "#!/bin/sh\necho v1\n")
	pin := snapshotEntry([]string{bin}, dir)
	if pin == nil {
		t.Fatal("expected a pin")
	}
	writeExec(t, bin, "#!/bin/sh\necho HACKED\n")
	svc := testService(t, policy.Policy{})
	saveFD(t, svc, "fd-runblock", pin, []string{bin}, dir)
	svc.Store.Consume("fd-runblock", "approve", "tester")
	res := svc.runApproved("fd-runblock",
		protocol.RunRequest{Argv: []string{bin}, Cwd: dir}, 1000, map[string]string{})
	if res.Status != "denied" || !strings.Contains(res.Reason, "fd-drift") {
		t.Fatalf("swapped entrypoint should deny with fd-drift, got %+v", res)
	}
	st := svc.Status("fd-runblock")
	if st.Decision != "fd-drift" {
		t.Errorf("blocked run should read as fd-drift, got %+v", st)
	}
	if st.FDDrift == nil || !st.FDDrift.Block {
		t.Errorf("status should carry blocking FDDrift, got %+v", st.FDDrift)
	}
	found := false
	for _, it := range svc.Recent(10).Items {
		if it.ID == "fd-runblock" && it.FDDrift != nil && it.FDDrift.Block {
			found = true
		}
	}
	if !found {
		t.Error("recent should carry the blocking FDDrift marker")
	}
}

func TestRunApprovedFallsBackWithoutPin(t *testing.T) {
	dir := t.TempDir()
	svc := testService(t, policy.Policy{})
	svc.Conf.DryRun = true
	saveFD(t, svc, "fd-nopin-run", nil, []string{"/bin/echo", "hi"}, dir)
	res := svc.runApproved("fd-nopin-run",
		protocol.RunRequest{Argv: []string{"/bin/echo", "hi"}, Cwd: dir}, 1000, map[string]string{})
	if res.Status != "allowed" {
		t.Fatalf("unpinned approval should run, got %+v", res)
	}
}
