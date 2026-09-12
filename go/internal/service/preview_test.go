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

func TestPreviewImpactBinaryResolution(t *testing.T) {
	tmpDir := t.TempDir()

	// 1. Absolute binary path
	p1 := PreviewImpact([]string{"/bin/echo", "hello"}, tmpDir, nil)
	if p1.ResolvedBinary != "/bin/echo" {
		t.Errorf("ResolvedBinary = %q, want /bin/echo", p1.ResolvedBinary)
	}
	if p1.TargetCwd != tmpDir {
		t.Errorf("TargetCwd = %q, want %q", p1.TargetCwd, tmpDir)
	}
	if p1.RiskLevel != "low" {
		t.Errorf("RiskLevel = %q, want low", p1.RiskLevel)
	}

	// 2. Relative binary path with slash
	relBin := filepath.Join(tmpDir, "local-cmd")
	if err := os.WriteFile(relBin, []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	p2 := PreviewImpact([]string{"./local-cmd"}, tmpDir, nil)
	if p2.ResolvedBinary != relBin {
		t.Errorf("ResolvedBinary = %q, want %q", p2.ResolvedBinary, relBin)
	}

	// 3. Custom PATH in env
	customBinDir := filepath.Join(tmpDir, "custombin")
	if err := os.MkdirAll(customBinDir, 0o755); err != nil {
		t.Fatal(err)
	}
	customExe := filepath.Join(customBinDir, "mycustomtool")
	if err := os.WriteFile(customExe, []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	env := []string{"PATH=" + customBinDir + ":/usr/bin"}
	p3 := PreviewImpact([]string{"mycustomtool", "run"}, tmpDir, env)
	if p3.ResolvedBinary != customExe {
		t.Errorf("ResolvedBinary with custom PATH = %q, want %q", p3.ResolvedBinary, customExe)
	}

	// 4. System binary resolution
	p4 := PreviewImpact([]string{"rm", "-rf", "test"}, tmpDir, nil)
	if !strings.HasSuffix(p4.ResolvedBinary, "/rm") {
		t.Errorf("ResolvedBinary for rm = %q, want suffix /rm", p4.ResolvedBinary)
	}
}

func TestPreviewImpactReadOnlySafeCommands(t *testing.T) {
	tmpDir := t.TempDir()

	testCases := [][]string{
		{"echo", "hello", "world"},
		{"cat", "/etc/passwd"},
		{"ls", "-la", "/var/log"},
		{"grep", "-rn", "pattern", "."},
		{"git", "status"},
		{"git", "diff"},
		{"pwd"},
	}

	for _, tc := range testCases {
		prev := PreviewImpact(tc, tmpDir, nil)
		if prev == nil {
			t.Fatalf("PreviewImpact(%v) returned nil", tc)
		}
		if prev.RiskLevel != "low" {
			t.Errorf("PreviewImpact(%v).RiskLevel = %q, want low", tc, prev.RiskLevel)
		}
		if prev.AffectedCount != 0 {
			t.Errorf("PreviewImpact(%v).AffectedCount = %d, want 0", tc, prev.AffectedCount)
		}
		if len(prev.SamplePaths) != 0 {
			t.Errorf("PreviewImpact(%v).SamplePaths = %v, want empty", tc, prev.SamplePaths)
		}
	}
}

func TestPreviewImpactCriticalRisk(t *testing.T) {
	tmpDir := t.TempDir()

	testCases := []struct {
		argv   []string
		cwd    string
		reason string
	}{
		{argv: []string{"rm", "-rf", "/"}, cwd: tmpDir, reason: "root filesystem pattern"},
		{argv: []string{"rm", "-rf", "/etc"}, cwd: tmpDir, reason: "critical system directory"},
		{argv: []string{"rm", "-rf", "/etc/hosts"}, cwd: tmpDir, reason: "critical system directory"},
		{argv: []string{"rm", "-rf", "/usr/bin"}, cwd: tmpDir, reason: "critical system directory"},
		{argv: []string{"rm", "-rf", "/boot"}, cwd: tmpDir, reason: "critical system directory"},
		{argv: []string{"rm", "-rf", "/var/log"}, cwd: tmpDir, reason: "critical system directory"},
		{argv: []string{"rm", "-rf", "/home"}, cwd: tmpDir, reason: "system root directory: /home"},
		{argv: []string{"chmod", "-R", "777", "/etc"}, cwd: tmpDir, reason: "critical system directory"},
		{argv: []string{"chown", "-R", "root:root", "/var"}, cwd: tmpDir, reason: "critical system directory"},
		{argv: []string{"sudo", "rm", "-rf", "/etc/shadow"}, cwd: tmpDir, reason: "critical system directory"},
	}

	for _, tc := range testCases {
		prev := PreviewImpact(tc.argv, tc.cwd, nil)
		if prev == nil {
			t.Fatalf("PreviewImpact(%v) returned nil", tc.argv)
		}
		if prev.RiskLevel != "critical" {
			t.Errorf("PreviewImpact(%v).RiskLevel = %q, want critical", tc.argv, prev.RiskLevel)
		}
		if !strings.Contains(prev.RiskReason, tc.reason) {
			t.Errorf("PreviewImpact(%v).RiskReason = %q, want substring %q", tc.argv, prev.RiskReason, tc.reason)
		}
		if len(prev.SamplePaths) == 0 {
			t.Errorf("PreviewImpact(%v).SamplePaths is empty, expected samples", tc.argv)
		}
	}
}

func TestPreviewImpactHighRiskOutsideCwd(t *testing.T) {
	baseDir := t.TempDir()
	dirA := filepath.Join(baseDir, "dirA")
	dirB := filepath.Join(baseDir, "dirB")
	if err := os.MkdirAll(dirA, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(dirB, 0o755); err != nil {
		t.Fatal(err)
	}

	targetFile := filepath.Join(dirB, "secret.txt")
	if err := os.WriteFile(targetFile, []byte("data"), 0o644); err != nil {
		t.Fatal(err)
	}

	// rm ../dirB/secret.txt from dirA
	prev := PreviewImpact([]string{"rm", "-f", "../dirB/secret.txt"}, dirA, nil)
	if prev.RiskLevel != "high" {
		t.Errorf("RiskLevel = %q, want high", prev.RiskLevel)
	}
	if !strings.Contains(prev.RiskReason, "outside working directory") {
		t.Errorf("RiskReason = %q, want outside working directory warning", prev.RiskReason)
	}
	if prev.AffectedCount != 1 {
		t.Errorf("AffectedCount = %d, want 1", prev.AffectedCount)
	}
	if len(prev.SamplePaths) != 1 || prev.SamplePaths[0] != targetFile {
		t.Errorf("SamplePaths = %v, want [%s]", prev.SamplePaths, targetFile)
	}
}

func TestPreviewImpactHighRiskLargeFileTree(t *testing.T) {
	tmpDir := t.TempDir()
	subDir := filepath.Join(tmpDir, "tree")
	if err := os.MkdirAll(subDir, 0o755); err != nil {
		t.Fatal(err)
	}

	// Create 15 files in subDir
	for i := 0; i < 15; i++ {
		fn := filepath.Join(subDir, filepath.Base(tmpDir)+"-file-"+string(rune('a'+i))+".txt")
		if err := os.WriteFile(fn, []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	prev := PreviewImpact([]string{"rm", "-rf", "tree"}, tmpDir, nil)
	if prev.RiskLevel != "high" {
		t.Errorf("RiskLevel = %q, want high (file count > 10)", prev.RiskLevel)
	}
	if !strings.Contains(prev.RiskReason, "exceeds threshold of 10") {
		t.Errorf("RiskReason = %q, want exceeds threshold warning", prev.RiskReason)
	}
	// 1 tree directory + 15 files = 16 affected items
	if prev.AffectedCount != 16 {
		t.Errorf("AffectedCount = %d, want 16", prev.AffectedCount)
	}
	// Sample paths capped at 10
	if len(prev.SamplePaths) != 10 {
		t.Errorf("len(SamplePaths) = %d, want 10 (capped)", len(prev.SamplePaths))
	}
}

func TestPreviewImpactMediumRiskLocalDeletion(t *testing.T) {
	tmpDir := t.TempDir()
	f1 := filepath.Join(tmpDir, "temp1.txt")
	f2 := filepath.Join(tmpDir, "temp2.txt")
	_ = os.WriteFile(f1, []byte("1"), 0o644)
	_ = os.WriteFile(f2, []byte("2"), 0o644)

	// Single file deletion
	p1 := PreviewImpact([]string{"rm", "temp1.txt"}, tmpDir, nil)
	if p1.RiskLevel != "medium" {
		t.Errorf("p1.RiskLevel = %q, want medium", p1.RiskLevel)
	}
	if !strings.Contains(p1.RiskReason, "Deletes file within working directory") {
		t.Errorf("p1.RiskReason = %q, want deletes file warning", p1.RiskReason)
	}
	if p1.AffectedCount != 1 {
		t.Errorf("p1.AffectedCount = %d, want 1", p1.AffectedCount)
	}
	if len(p1.SamplePaths) != 1 || p1.SamplePaths[0] != f1 {
		t.Errorf("p1.SamplePaths = %v, want [%s]", p1.SamplePaths, f1)
	}

	// Multiple file deletion (<= 10 files)
	p2 := PreviewImpact([]string{"rm", "-f", "temp1.txt", "temp2.txt"}, tmpDir, nil)
	if p2.RiskLevel != "medium" {
		t.Errorf("p2.RiskLevel = %q, want medium", p2.RiskLevel)
	}
	if p2.AffectedCount != 2 {
		t.Errorf("p2.AffectedCount = %d, want 2", p2.AffectedCount)
	}
	if len(p2.SamplePaths) != 2 {
		t.Errorf("len(p2.SamplePaths) = %d, want 2", len(p2.SamplePaths))
	}
}

func TestPreviewImpactGlobInspection(t *testing.T) {
	tmpDir := t.TempDir()
	for i := 0; i < 4; i++ {
		fn := filepath.Join(tmpDir, filepath.Base(tmpDir)+"-item-"+string(rune('0'+i))+".tmp")
		_ = os.WriteFile(fn, []byte("temp"), 0o644)
	}

	prev := PreviewImpact([]string{"rm", "-f", "*.tmp"}, tmpDir, nil)
	if prev.RiskLevel != "medium" {
		t.Errorf("RiskLevel = %q, want medium", prev.RiskLevel)
	}
	if prev.AffectedCount != 4 {
		t.Errorf("AffectedCount = %d, want 4", prev.AffectedCount)
	}
	if len(prev.SamplePaths) != 4 {
		t.Errorf("len(SamplePaths) = %d, want 4", len(prev.SamplePaths))
	}
}

func TestPreviewImpactModifyingOperations(t *testing.T) {
	tmpDir := t.TempDir()
	f := filepath.Join(tmpDir, "script.sh")
	_ = os.WriteFile(f, []byte("#!/bin/sh\n"), 0o644)

	// chmod
	p1 := PreviewImpact([]string{"chmod", "+x", "script.sh"}, tmpDir, nil)
	if p1.RiskLevel != "medium" {
		t.Errorf("chmod RiskLevel = %q, want medium", p1.RiskLevel)
	}
	if p1.AffectedCount != 1 {
		t.Errorf("chmod AffectedCount = %d, want 1", p1.AffectedCount)
	}

	// mv within cwd
	p2 := PreviewImpact([]string{"mv", "script.sh", "script2.sh"}, tmpDir, nil)
	if p2.RiskLevel != "medium" {
		t.Errorf("mv RiskLevel = %q, want medium", p2.RiskLevel)
	}
	if p2.AffectedCount != 2 {
		t.Errorf("mv AffectedCount = %d, want 2", p2.AffectedCount)
	}

	// truncate
	p3 := PreviewImpact([]string{"truncate", "-s", "0", "script.sh"}, tmpDir, nil)
	if p3.RiskLevel != "medium" {
		t.Errorf("truncate RiskLevel = %q, want medium", p3.RiskLevel)
	}
	if p3.AffectedCount != 1 {
		t.Errorf("truncate AffectedCount = %d, want 1", p3.AffectedCount)
	}

	// git clean within cwd
	p4 := PreviewImpact([]string{"git", "clean", "-fd"}, tmpDir, nil)
	if p4.RiskLevel != "medium" {
		t.Errorf("git clean RiskLevel = %q, want medium", p4.RiskLevel)
	}
}

func TestPreviewPopulatedInPendingAndStatus(t *testing.T) {
	svc := testService(t, policy.Policy{Default: "ask"})
	svc.Conf.Timeout = 5
	tmpDir := t.TempDir()

	testFile := filepath.Join(tmpDir, "target.txt")
	_ = os.WriteFile(testFile, []byte("content"), 0o644)

	type runOut struct {
		res protocol.RunResult
	}
	done := make(chan runOut, 1)
	go func() {
		res := svc.Run(protocol.RunRequest{
			Argv: []string{"rm", "-f", "target.txt"},
			Cwd:  tmpDir,
			Env:  map[string]string{"PATH": safePath},
		}, 1000)
		done <- runOut{res: res}
	}()

	var petitionID string
	for i := 0; i < 50; i++ {
		items := svc.Store.List(time.Now().Unix())
		if len(items) > 0 {
			it := items[0]
			petitionID = it.ID
			if it.Preview == nil {
				t.Fatalf("PendingItem.Preview is nil")
			}
			if it.Preview.RiskLevel != "medium" {
				t.Errorf("PendingItem.Preview.RiskLevel = %q, want medium", it.Preview.RiskLevel)
			}
			if it.Preview.TargetCwd != tmpDir {
				t.Errorf("PendingItem.Preview.TargetCwd = %q, want %q", it.Preview.TargetCwd, tmpDir)
			}
			if it.Preview.AffectedCount != 1 {
				t.Errorf("PendingItem.Preview.AffectedCount = %d, want 1", it.Preview.AffectedCount)
			}
			if len(it.Preview.SamplePaths) != 1 || it.Preview.SamplePaths[0] != testFile {
				t.Errorf("PendingItem.Preview.SamplePaths = %v, want [%s]", it.Preview.SamplePaths, testFile)
			}
			break
		}
		time.Sleep(20 * time.Millisecond)
	}

	if petitionID == "" {
		t.Fatal("petition did not appear in store")
	}

	// Verify StatusResponse includes Preview
	st := svc.Status(petitionID)
	if st.Preview == nil {
		t.Fatalf("StatusResponse.Preview is nil")
	}
	if st.Preview.RiskLevel != "medium" {
		t.Errorf("StatusResponse.Preview.RiskLevel = %q, want medium", st.Preview.RiskLevel)
	}

	// Consume verdict to finish
	svc.Store.Consume(petitionID, "deny", "admin")
	out := <-done
	if out.res.Status != "denied" {
		t.Errorf("RunResult = %+v, want denied", out.res)
	}
}
