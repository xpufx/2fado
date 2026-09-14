package service

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"2fado/internal/policy"
	"2fado/internal/protocol"
)

func writeSealFile(t *testing.T, path, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o755); err != nil {
		t.Fatal(err)
	}
}

func saveSealed(t *testing.T, svc Service, rid string, pin *protocol.SealPin, argv []string, cwd string) {
	t.Helper()
	if err := svc.Store.Save(protocol.PendingRecord{
		Argv: argv, UID: 1000, Cwd: cwd,
		Expires: time.Now().Add(60 * time.Second).Unix(), Seal: pin,
	}, rid); err != nil {
		t.Fatal(err)
	}
}

func sealFixture(t *testing.T) (Service, string, string, string) {
	t.Helper()
	dir := t.TempDir()
	script := filepath.Join(dir, "deploy.sh")
	writeSealFile(t, script, "#!/bin/sh\necho sealed-v1\n")
	input := filepath.Join(dir, "args.txt")
	writeSealFile(t, input, "v1\n")
	svc := testService(t, policy.Policy{})
	return svc, dir, script, input
}

func TestSnapshotSealGraceful(t *testing.T) {
	svc := testService(t, policy.Policy{})
	dir := t.TempDir()
	if svc.snapshotSeal("rid-empty", nil, dir) != nil {
		t.Error("empty argv should yield nil pin")
	}
	if svc.snapshotSeal("rid-missing", []string{"tool.sh"}, filepath.Join(dir, "nope")) != nil {
		t.Error("missing cwd should yield nil pin")
	}
	if svc.snapshotSeal("", []string{"tool.sh"}, dir) != nil {
		t.Error("empty rid should yield nil pin")
	}
	if svc.snapshotSeal("rid-nofiles", []string{"/bin/echo", "hi"}, dir) != nil {
		t.Error("petition with no workspace files should yield nil pin")
	}
	if svc.snapshotSeal("rid-flags", []string{"/bin/echo", "-n", "--x=1"}, dir) != nil {
		t.Error("flags-only petition should yield nil pin")
	}
	outside := filepath.Join(t.TempDir(), "ext.sh")
	writeSealFile(t, outside, "#!/bin/sh\necho ext\n")
	if svc.snapshotSeal("rid-outside", []string{outside}, dir) != nil {
		t.Error("entrypoint outside cwd belongs to the fd pin, not the seal")
	}
}

func TestSnapshotSealManifest(t *testing.T) {
	svc, dir, script, input := sealFixture(t)
	pin := svc.snapshotSeal("rid-manifest", []string{script, input}, dir)
	if pin == nil {
		t.Fatal("expected a pin for workspace script + input")
	}
	if len(pin.Files) != 2 {
		t.Fatalf("files = %d, want 2", len(pin.Files))
	}
	if pin.Files[0].Rel >= pin.Files[1].Rel {
		t.Errorf("files not Rel-sorted: %q, %q", pin.Files[0].Rel, pin.Files[1].Rel)
	}
	for _, f := range pin.Files {
		raw, err := os.ReadFile(f.Abs)
		if err != nil {
			t.Fatal(err)
		}
		sum := sha256.Sum256(raw)
		if want := hex.EncodeToString(sum[:]); f.SHA256 != want {
			t.Errorf("%s hash = %s, want %s", f.Rel, f.SHA256, want)
		}
		if f.Size != int64(len(raw)) {
			t.Errorf("%s size = %d, want %d", f.Rel, f.Size, len(raw))
		}
	}
	if pin.Root != sealRoot(pin.Files) {
		t.Error("root must fold the sorted per-file hashes")
	}
	if len(pin.Root) != 64 {
		t.Errorf("root = %q, want 64-char hex", pin.Root)
	}
	raw, err := os.ReadFile(pin.Archive)
	if err != nil || len(raw) == 0 {
		t.Fatalf("archive at %q unreadable: %v", pin.Archive, err)
	}
}

func TestCheckSealDriftSkipsWithoutPin(t *testing.T) {
	svc := testService(t, policy.Policy{})
	saveSealed(t, svc, "seal-nopin", nil, []string{"/bin/echo"}, t.TempDir())
	if d := svc.checkSealDrift("seal-nopin"); d != nil {
		t.Errorf("nil pin should skip, got %+v", d)
	}
	if d := svc.checkSealDrift("seal-missing"); d != nil {
		t.Errorf("missing record should skip, got %+v", d)
	}
}

func TestCheckSealDriftClean(t *testing.T) {
	svc, dir, script, input := sealFixture(t)
	pin := svc.snapshotSeal("rid-clean", []string{script, input}, dir)
	saveSealed(t, svc, "seal-clean", pin, []string{script, input}, dir)
	if d := svc.checkSealDrift("seal-clean"); d != nil {
		t.Errorf("unchanged inputs should not drift, got %+v", d)
	}
}

func TestCheckSealDriftMutationBlocks(t *testing.T) {
	svc, dir, script, input := sealFixture(t)
	pin := svc.snapshotSeal("rid-mut", []string{script, input}, dir)
	saveSealed(t, svc, "seal-mut", pin, []string{script, input}, dir)
	writeSealFile(t, script, "#!/bin/sh\necho HACKED\n")
	d := svc.checkSealDrift("seal-mut")
	if d == nil || !d.Drift || !d.Block {
		t.Fatalf("script mutation should block, got %+v", d)
	}
	if !strings.Contains(d.Reason, "deploy.sh") {
		t.Errorf("reason should name the file, got %q", d.Reason)
	}
}

func TestCheckSealDriftDeletionBlocks(t *testing.T) {
	svc, dir, script, input := sealFixture(t)
	pin := svc.snapshotSeal("rid-del", []string{script, input}, dir)
	saveSealed(t, svc, "seal-del", pin, []string{script, input}, dir)
	if err := os.Remove(input); err != nil {
		t.Fatal(err)
	}
	d := svc.checkSealDrift("seal-del")
	if d == nil || !d.Drift || !d.Block {
		t.Fatalf("deleted input should block, got %+v", d)
	}
	if !strings.Contains(d.Reason, "gone") {
		t.Errorf("reason should report the missing file, got %q", d.Reason)
	}
}

func TestMaterializeRunsFrozenBytes(t *testing.T) {
	svc, dir, script, _ := sealFixture(t)
	pin := svc.snapshotSeal("rid-frozen", []string{script}, dir)
	saveSealed(t, svc, "seal-frozen", pin, []string{script}, dir)
	frozen, err := svc.materializeSeal("seal-frozen")
	if err != nil {
		t.Fatalf("materializeSeal: %v", err)
	}
	writeSealFile(t, script, "#!/bin/sh\necho HACKED\n")
	rewritten := sealedArgv([]string{script}, dir, pin, frozen)
	if len(rewritten) != 1 || !strings.HasPrefix(rewritten[0], frozen) {
		t.Fatalf("entrypoint should rewrite into the frozen dir, got %v", rewritten)
	}
	code, out, _ := svc.Execute(rewritten, dir, map[string]string{})
	if code != 0 || !strings.Contains(out, "sealed-v1") {
		t.Fatalf("frozen exec = exit %d out %q, want sealed-v1", code, out)
	}
	if strings.Contains(out, "HACKED") {
		t.Errorf("attacker bytes must never execute, out = %q", out)
	}
}

func TestRunApprovedBlocksOnSealDrift(t *testing.T) {
	svc, dir, script, _ := sealFixture(t)
	pin := svc.snapshotSeal("rid-runblock", []string{script}, dir)
	if pin == nil {
		t.Fatal("expected a pin")
	}
	writeSealFile(t, script, "#!/bin/sh\necho HACKED\n")
	saveSealed(t, svc, "seal-runblock", pin, []string{script}, dir)
	svc.Store.Consume("seal-runblock", "approve", "tester")
	res := svc.runApproved("seal-runblock",
		protocol.RunRequest{Argv: []string{script}, Cwd: dir}, 1000, map[string]string{})
	if res.Status != "denied" || !strings.Contains(res.Reason, "seal-drift") {
		t.Fatalf("mutated input should deny with seal-drift, got %+v", res)
	}
	st := svc.Status("seal-runblock")
	if st.Decision != "seal-drift" {
		t.Errorf("blocked run should read as seal-drift, got %+v", st)
	}
	if st.SealDrift == nil || !st.SealDrift.Block {
		t.Errorf("status should carry blocking SealDrift, got %+v", st.SealDrift)
	}
	found := false
	for _, it := range svc.Recent(10).Items {
		if it.ID == "seal-runblock" && it.SealDrift != nil && it.SealDrift.Block {
			found = true
		}
	}
	if !found {
		t.Error("recent should carry the blocking SealDrift marker")
	}
}

func TestRunApprovedRunsSealedWhenClean(t *testing.T) {
	svc, dir, script, _ := sealFixture(t)
	pin := svc.snapshotSeal("rid-runsealed", []string{script}, dir)
	if pin == nil {
		t.Fatal("expected a pin")
	}
	saveSealed(t, svc, "seal-runsealed", pin, []string{script}, dir)
	svc.Store.Consume("seal-runsealed", "approve", "tester")
	res := svc.runApproved("seal-runsealed",
		protocol.RunRequest{Argv: []string{script}, Cwd: dir}, 1000, map[string]string{})
	if res.Status != "allowed" || !strings.Contains(res.Output, "sealed-v1") {
		t.Fatalf("clean sealed approval should run frozen bytes, got %+v", res)
	}
	if strings.Contains(res.Output, "HACKED") {
		t.Errorf("attacker bytes must never execute, out = %q", res.Output)
	}
	items := svc.List().Items
	for _, it := range items {
		if it.ID == "seal-runsealed" {
			t.Error("executed petition should leave the pending list")
		}
	}
}

func TestRunApprovedFallsBackWithoutSeal(t *testing.T) {
	dir := t.TempDir()
	svc := testService(t, policy.Policy{})
	svc.Conf.DryRun = true
	saveSealed(t, svc, "seal-nopin-run", nil, []string{"/bin/echo", "hi"}, dir)
	res := svc.runApproved("seal-nopin-run",
		protocol.RunRequest{Argv: []string{"/bin/echo", "hi"}, Cwd: dir}, 1000, map[string]string{})
	if res.Status != "allowed" {
		t.Fatalf("unsealed approval should run, got %+v", res)
	}
}

func TestListCarriesSealWithLiveDrift(t *testing.T) {
	svc, dir, script, _ := sealFixture(t)
	pin := svc.snapshotSeal("rid-list", []string{script}, dir)
	saveSealed(t, svc, "seal-list", pin, []string{script}, dir)
	found := false
	for _, it := range svc.List().Items {
		if it.ID != "seal-list" {
			continue
		}
		found = true
		if it.Seal == nil || it.Seal.Root != pin.Root {
			t.Errorf("list should carry the bound root hash, got %+v", it.Seal)
		}
		if it.SealDrift != nil {
			t.Errorf("clean seal should show no drift, got %+v", it.SealDrift)
		}
	}
	if !found {
		t.Fatal("sealed petition missing from list")
	}
	writeSealFile(t, script, "#!/bin/sh\necho HACKED\n")
	for _, it := range svc.List().Items {
		if it.ID == "seal-list" && (it.SealDrift == nil || !it.SealDrift.Block) {
			t.Fatalf("mutated seal should show blocking drift in list, got %+v", it.SealDrift)
		}
	}
}
