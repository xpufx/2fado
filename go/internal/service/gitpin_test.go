package service

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"2fado/internal/policy"
	"2fado/internal/protocol"
)

// initGitRepo builds a committed repo in a temp dir for pin/drift tests.
// It skips when git is unavailable.
func initGitRepo(t *testing.T) string {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
	dir := t.TempDir()
	run := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		cmd.Env = []string{"PATH=" + safePath, "HOME=" + t.TempDir(),
			"GIT_CONFIG_NOSYSTEM=1", "GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@t",
			"GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@t"}
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	run("init", "-q")
	if err := os.WriteFile(filepath.Join(dir, "f.txt"), []byte("v1"), 0o644); err != nil {
		t.Fatal(err)
	}
	run("add", ".")
	run("commit", "-qm", "init")
	return dir
}

func savePinned(t *testing.T, svc Service, rid string, pin *protocol.GitPin, cwd string) {
	t.Helper()
	if err := svc.Store.Save(protocol.PendingRecord{
		Argv: []string{"/bin/echo", "hi"}, UID: 1000, Cwd: cwd,
		Expires: time.Now().Add(60 * time.Second).Unix(), Git: pin,
	}, rid); err != nil {
		t.Fatal(err)
	}
}

func TestSnapshotGitDegradesGracefully(t *testing.T) {
	if snapshotGit("") != nil {
		t.Error("empty cwd should yield nil pin")
	}
	if snapshotGit(filepath.Join(t.TempDir(), "nope")) != nil {
		t.Error("missing cwd should yield nil pin")
	}
	plain := t.TempDir()
	pin := snapshotGit(plain)
	if pin == nil {
		t.Fatal("plain dir should yield a non-nil pin")
	}
	if pin.Present {
		t.Error("non-repo dir should have Present=false")
	}
}

func TestSnapshotGitCapturesRepo(t *testing.T) {
	dir := initGitRepo(t)
	pin := snapshotGit(dir)
	if pin == nil || !pin.Present {
		t.Fatal("repo dir should yield a present pin")
	}
	if len(pin.Head) != 40 {
		t.Errorf("Head = %q, want 40-char SHA", pin.Head)
	}
	if pin.Branch == "" {
		t.Error("Branch should be set")
	}
	if !pin.Clean || pin.Porcelain != "" {
		t.Error("fresh commit should pin a clean tree")
	}
	if err := os.WriteFile(filepath.Join(dir, "f.txt"), []byte("v2"), 0o644); err != nil {
		t.Fatal(err)
	}
	dirty := snapshotGit(dir)
	if dirty == nil || dirty.Clean || strings.TrimSpace(dirty.Porcelain) == "" {
		t.Error("modified worktree should pin a dirty tree")
	}
}

func TestCheckGitDriftNoPinSkips(t *testing.T) {
	svc := testService(t, policy.Policy{})
	savePinned(t, svc, "rid-nopin", nil, t.TempDir())
	if d := svc.checkGitDrift("rid-nopin", ""); d != nil {
		t.Errorf("missing pin should skip, got %+v", d)
	}
	savePinned(t, svc, "rid-nonrepo", &protocol.GitPin{Present: false}, t.TempDir())
	if d := svc.checkGitDrift("rid-nonrepo", ""); d != nil {
		t.Errorf("non-repo pin should skip, got %+v", d)
	}
	if d := svc.checkGitDrift("rid-missing", ""); d != nil {
		t.Errorf("missing record should skip, got %+v", d)
	}
}

func TestCheckGitDriftCleanTreeNoDrift(t *testing.T) {
	dir := initGitRepo(t)
	svc := testService(t, policy.Policy{})
	savePinned(t, svc, "rid-clean", snapshotGit(dir), dir)
	if d := svc.checkGitDrift("rid-clean", dir); d != nil {
		t.Errorf("unchanged tree should not drift, got %+v", d)
	}
}

func TestCheckGitDriftDirtyWarnsByDefault(t *testing.T) {
	dir := initGitRepo(t)
	svc := testService(t, policy.Policy{})
	savePinned(t, svc, "rid-dirty", snapshotGit(dir), dir)
	if err := os.WriteFile(filepath.Join(dir, "new.txt"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	d := svc.checkGitDrift("rid-dirty", dir)
	if d == nil || !d.Drift {
		t.Fatal("dirty tree should drift")
	}
	if d.Block {
		t.Error("dirty drift should warn, not block, under default policy")
	}
	if !strings.Contains(d.Reason, "DIRTY WORKSPACE") {
		t.Errorf("reason should flag dirty workspace, got %q", d.Reason)
	}
}

func TestCheckGitDriftStrictBlocksDirty(t *testing.T) {
	dir := initGitRepo(t)
	svc := testService(t, policy.Policy{})
	savePinned(t, svc, "rid-strict", snapshotGit(dir), dir)
	if err := os.WriteFile(filepath.Join(dir, "new.txt"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("TWOFADO_GIT_STRICT", "1")
	d := svc.checkGitDrift("rid-strict", dir)
	if d == nil || !d.Drift || !d.Block {
		t.Fatalf("strict dirty drift should block, got %+v", d)
	}
}

func TestCheckGitDriftHeadMoveBlocks(t *testing.T) {
	dir := initGitRepo(t)
	svc := testService(t, policy.Policy{})
	pin := snapshotGit(dir)
	pin.Head = strings.Repeat("0", 40)
	savePinned(t, svc, "rid-head", pin, dir)
	d := svc.checkGitDrift("rid-head", dir)
	if d == nil || !d.Drift || !d.Block {
		t.Fatalf("HEAD move should block, got %+v", d)
	}
	if !strings.Contains(d.Reason, "HEAD moved") {
		t.Errorf("reason should name the HEAD move, got %q", d.Reason)
	}
}

func TestRunApprovedBlocksOnDrift(t *testing.T) {
	dir := initGitRepo(t)
	svc := testService(t, policy.Policy{})
	pin := snapshotGit(dir)
	pin.Head = strings.Repeat("0", 40)
	savePinned(t, svc, "rid-runblock", pin, dir)
	svc.Store.Consume("rid-runblock", "approve", "tester")
	res := svc.runApproved("rid-runblock",
		protocol.RunRequest{Argv: []string{"/bin/echo", "hi"}, Cwd: dir}, 1000, map[string]string{})
	if res.Status != "denied" || !strings.Contains(res.Reason, "git-drift") {
		t.Fatalf("drifted approval should deny with git-drift, got %+v", res)
	}
	st := svc.Status("rid-runblock")
	if st.Decision != "git-drift" {
		t.Errorf("blocked run should read as git-drift, got %+v", st)
	}
}

func TestRunApprovedWarnsAndRunsOnDirty(t *testing.T) {
	dir := initGitRepo(t)
	svc := testService(t, policy.Policy{})
	svc.Conf.DryRun = true
	savePinned(t, svc, "rid-runwarn", snapshotGit(dir), dir)
	if err := os.WriteFile(filepath.Join(dir, "new.txt"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	res := svc.runApproved("rid-runwarn",
		protocol.RunRequest{Argv: []string{"/bin/echo", "hi"}, Cwd: dir}, 1000, map[string]string{})
	if res.Status != "allowed" {
		t.Fatalf("warn-level drift should still run, got %+v", res)
	}
	if !strings.Contains(res.Output, "DIRTY WORKSPACE") {
		t.Errorf("output should carry the dirty warning, got %q", res.Output)
	}
}
