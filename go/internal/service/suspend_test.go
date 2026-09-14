package service

import (
	"os"
	"os/exec"
	"runtime"
	"strings"
	"syscall"
	"testing"
	"time"

	"2fado/internal/policy"
	"2fado/internal/protocol"
)

func procState(t *testing.T, pid int) string {
	t.Helper()
	data, err := os.ReadFile("/proc/" + itoaPID(pid) + "/status")
	if err != nil {
		return ""
	}
	for _, line := range strings.Split(string(data), "\n") {
		if strings.HasPrefix(line, "State:") {
			return strings.TrimSpace(strings.TrimPrefix(line, "State:"))
		}
	}
	return ""
}

func itoaPID(n int) string {
	if n == 0 {
		return "0"
	}
	var b [20]byte
	i := len(b)
	for n > 0 {
		i--
		b[i] = byte('0' + n%10)
		n /= 10
	}
	return string(b[i:])
}

func startGroupedSleep(t *testing.T) (*exec.Cmd, int) {
	t.Helper()
	if runtime.GOOS != "linux" {
		t.Skip("SIGSTOP group test needs linux /proc")
	}
	cmd := exec.Command("sleep", "30")
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	if err := cmd.Start(); err != nil {
		t.Fatalf("start sleep: %v", err)
	}
	t.Cleanup(func() {
		_ = contPGID(cmd.Process.Pid)
		_ = cmd.Process.Kill()
		_, _ = cmd.Process.Wait()
	})
	pgid, err := syscall.Getpgid(cmd.Process.Pid)
	if err != nil {
		t.Fatalf("Getpgid: %v", err)
	}
	if pgid != cmd.Process.Pid {
		t.Fatalf("Setsid child pgid = %d, want own pid %d", pgid, cmd.Process.Pid)
	}
	return cmd, pgid
}

func waitState(t *testing.T, pid int, wantStopped bool) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for {
		st := procState(t, pid)
		if wantStopped && strings.HasPrefix(st, "T") {
			return
		}
		if !wantStopped && st != "" && !strings.HasPrefix(st, "T") && !strings.HasPrefix(st, "Z") {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("pid %d state = %q, want stopped=%v", pid, st, wantStopped)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func TestResolveSuspendSkips(t *testing.T) {
	if _, ok := resolveSuspendPGID(0, uint32(os.Geteuid())); ok {
		t.Error("pid 0 must skip")
	}
	if _, ok := resolveSuspendPGID(-3, uint32(os.Geteuid())); ok {
		t.Error("negative pid must skip")
	}
	if _, ok := resolveSuspendPGID(os.Getpid(), uint32(os.Geteuid())); ok {
		t.Error("daemon own pid must skip")
	}
	if _, ok := resolveSuspendPGID(1<<30, uint32(os.Geteuid())); ok {
		t.Error("nonexistent pid must skip")
	}
	t.Setenv("TWOFADO_SUSPEND", "0")
	if _, ok := resolveSuspendPGID(os.Getppid(), uint32(os.Geteuid())); ok {
		t.Error("TWOFADO_SUSPEND=0 must disable")
	}
}

func TestStopContGroupFreezesChild(t *testing.T) {
	cmd, pgid := startGroupedSleep(t)
	if pgid == syscall.Getpgrp() {
		t.Skip("child shares test pgid, refusing to stop it")
	}
	if err := stopPGID(pgid); err != nil {
		t.Fatalf("stopPGID: %v", err)
	}
	waitState(t, cmd.Process.Pid, true)
	if err := contPGID(pgid); err != nil {
		t.Fatalf("contPGID: %v", err)
	}
	waitState(t, cmd.Process.Pid, false)
}

func TestSnapshotResumeRoundTrip(t *testing.T) {
	svc := testService(t, policy.Policy{Default: "ask"})
	cmd, pgid := startGroupedSleep(t)
	if pgid == syscall.Getpgrp() {
		t.Skip("child shares test pgid, refusing to stop it")
	}
	rid := "suspend-roundtrip"
	if err := svc.Store.Save(protocol.PendingRecord{
		Argv: []string{"/bin/echo"}, UID: uint32(os.Geteuid()),
		Cwd: t.TempDir(), Expires: time.Now().Add(60 * time.Second).Unix(),
	}, rid); err != nil {
		t.Fatal(err)
	}
	pin := svc.snapshotSuspend(rid, cmd.Process.Pid, uint32(os.Geteuid()))
	if pin == nil || !pin.Suspended || pin.PGID != pgid {
		t.Fatalf("pin = %+v, want suspended pgid %d", pin, pgid)
	}
	waitState(t, cmd.Process.Pid, true)
	svc.resumeSuspend(rid, pin)
	waitState(t, cmd.Process.Pid, false)
}

func TestSnapshotSuspendSkipsWithoutPeer(t *testing.T) {
	svc := testService(t, policy.Policy{Default: "ask"})
	rid := "suspend-nopeer"
	if err := svc.Store.Save(protocol.PendingRecord{
		Argv: []string{"/bin/echo"}, UID: 1000,
		Cwd: t.TempDir(), Expires: time.Now().Add(60 * time.Second).Unix(),
	}, rid); err != nil {
		t.Fatal(err)
	}
	if pin := svc.snapshotSuspend(rid, 0, 1000); pin != nil {
		t.Errorf("pid 0 should yield nil pin, got %+v", pin)
	}
	svc.resumeSuspend(rid, nil)
}

func TestRunWithPeerNoBarrierStoresNoPin(t *testing.T) {
	svc := testService(t, policy.Policy{Default: "ask"})
	svc.Conf.Timeout = 10
	done := make(chan protocol.RunResult, 1)
	go func() {
		done <- svc.RunWithPeer(protocol.RunRequest{
			Argv: []string{"/bin/echo", "suspend-nopin"},
			Cwd:  t.TempDir(),
			Env:  map[string]string{},
		}, 1000, 0)
	}()
	var rid string
	deadline := time.Now().Add(5 * time.Second)
	for {
		items := svc.Store.List(time.Now().Unix())
		if len(items) > 0 {
			rid = items[0].ID
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("pending petition did not appear")
		}
		time.Sleep(20 * time.Millisecond)
	}
	rec, err := svc.Store.Load(rid)
	if err != nil {
		t.Fatal(err)
	}
	if rec.Suspend != nil {
		t.Fatalf("peerPID 0 should store no pin, got %+v", rec.Suspend)
	}
	if st := svc.Status(rid); st.Suspend != nil {
		t.Fatalf("status should carry no pin, got %+v", st.Suspend)
	}
	if !svc.Store.Consume(rid, "approve", "operator") {
		t.Fatal("consume failed")
	}
	select {
	case res := <-done:
		if res.Status != "allowed" {
			t.Fatalf("RunResult = %+v, want allowed", res)
		}
	case <-time.After(8 * time.Second):
		t.Fatal("run did not finish after approval")
	}
}
