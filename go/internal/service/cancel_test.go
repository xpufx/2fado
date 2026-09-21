package service

import (
	"os"
	"strings"
	"testing"
	"time"

	"2fado/internal/policy"
	"2fado/internal/protocol"
)

// TestCancelPendingExec verifies cancelling a pending execution petition.
func TestCancelPendingExec(t *testing.T) {
	svc := testService(t, policy.Policy{Default: "ask"})
	ridChan := make(chan string, 1)
	resChan := make(chan protocol.RunResult, 1)

	go func() {
		resChan <- svc.RunWithPeerCancel(protocol.RunRequest{
			Argv: []string{"echo", "hi"},
			Cwd:  "/tmp",
		}, 1000, 0, nil)
	}()

	var rid string
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		items := svc.List().Items
		if len(items) > 0 {
			rid = items[0].ID
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if rid == "" {
		t.Fatal("pending petition did not appear in store")
	}
	ridChan <- rid

	// Cancel by petitioning UID
	cancelRes := svc.Cancel(protocol.CancelRequest{
		ID:     rid,
		By:     "agent",
		Reason: "user cancelled task",
	}, 1000)

	if !cancelRes.Cancelled || cancelRes.Error != "" {
		t.Fatalf("Cancel failed: %+v", cancelRes)
	}

	select {
	case res := <-resChan:
		if res.Status != "denied" || res.Reason != "cancelled" {
			t.Fatalf("RunWithPeerCancel returned %+v, want denied/cancelled", res)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("RunWithPeerCancel did not unblock after cancel")
	}

	// Verify status
	st := svc.Status(rid)
	if st.Status != "cancelled" || st.Decision != "cancelled" || st.Output != "user cancelled task" {
		t.Fatalf("Status = %+v, want cancelled with reason", st)
	}

	// Verify pending list is cleared
	if len(svc.List().Items) != 0 {
		t.Fatalf("List still contains items after cancellation: %+v", svc.List().Items)
	}

	// Verify recent includes cancelled record
	recent := svc.Recent(10).Items
	found := false
	for _, it := range recent {
		if it.ID == rid {
			found = true
			if it.Decision != "cancelled" || it.Output != "user cancelled task" {
				t.Fatalf("Recent item = %+v, want cancelled with reason", it)
			}
		}
	}
	if !found {
		t.Fatal("cancelled item not found in Recent")
	}

	// Idempotent second cancel
	cancel2 := svc.Cancel(protocol.CancelRequest{
		ID:     rid,
		By:     "agent",
		Reason: "repeated cancel",
	}, 1000)
	if !cancel2.Cancelled || cancel2.Error != "" {
		t.Fatalf("repeated Cancel must succeed idempotently: %+v", cancel2)
	}
}

// TestCancelConfirmingExec verifies cancelling during confirmation step.
func TestCancelConfirmingExec(t *testing.T) {
	svc := testService(t, policy.Policy{Default: "ask"})
	svc.Conf.ConfirmAll = true
	resChan := make(chan protocol.RunResult, 1)

	go func() {
		resChan <- svc.RunWithPeerCancel(protocol.RunRequest{
			Argv: []string{"rm", "-rf", "/tmp/test"},
			Cwd:  "/tmp",
		}, 1000, 0, nil)
	}()

	var rid1 string
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		items := svc.List().Items
		if len(items) > 0 {
			rid1 = items[0].ID
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if rid1 == "" {
		t.Fatal("initial petition not found")
	}

	// Approve initial step to trigger confirmation step
	ack := svc.Submit(protocol.VerdictSubmit{
		ID:       rid1,
		Decision: "approve",
		By:       "operator",
	}, uint32(os.Getuid()))
	if !ack.Recorded {
		t.Fatal("Submit approve failed")
	}

	// Wait for child confirmation petition
	var ridConfirm string
	deadline = time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		childRID, childRec := svc.Store.FindConfirmation(rid1)
		if childRec != nil {
			ridConfirm = childRID
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if ridConfirm == "" {
		t.Fatal("confirmation petition did not appear")
	}

	// Cancel the parent RID or confirming RID
	cancelRes := svc.Cancel(protocol.CancelRequest{
		ID:     rid1,
		By:     "operator",
		Reason: "aborted at confirm",
	}, uint32(os.Getuid()))
	if !cancelRes.Cancelled || cancelRes.Error != "" {
		t.Fatalf("Cancel confirm failed: %+v", cancelRes)
	}

	select {
	case res := <-resChan:
		if res.Status != "denied" || res.Reason != "cancelled" {
			t.Fatalf("RunWithPeerCancel returned %+v, want denied/cancelled", res)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("RunWithPeerCancel did not unblock on confirmation cancel")
	}

	// Both parent and confirm child must reflect cancelled
	st1 := svc.Status(rid1)
	if st1.Status != "cancelled" || st1.Decision != "cancelled" {
		t.Fatalf("Parent status = %+v, want cancelled", st1)
	}
	st2 := svc.Status(ridConfirm)
	if st2.Status != "cancelled" || st2.Decision != "cancelled" {
		t.Fatalf("Confirm status = %+v, want cancelled", st2)
	}
}

// TestCancelAskPetition verifies cancelling a waiting ask petition.
func TestCancelAskPetition(t *testing.T) {
	svc := testService(t, policy.Policy{Default: "ask"})
	resChan := make(chan protocol.AskResult, 1)

	go func() {
		resChan <- svc.Ask(protocol.AskRequest{
			Question:   "Select branch to merge:",
			Options:    []string{"feature-a", "feature-b"},
			TTLSeconds: 30,
		}, 1000)
	}()

	rid := waitForAsk(t, svc, 3*time.Second)

	// Cancel ask petition
	cancelRes := svc.Cancel(protocol.CancelRequest{
		ID:     rid,
		By:     "agent",
		Reason: "no longer needed",
	}, 1000)

	if !cancelRes.Cancelled || cancelRes.Error != "" {
		t.Fatalf("Cancel on ask failed: %+v", cancelRes)
	}

	select {
	case res := <-resChan:
		if res.Status != "denied" || res.Reason != "cancelled" {
			t.Fatalf("Ask returned %+v, want denied/cancelled", res)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("Ask did not return after cancellation")
	}

	st := svc.Status(rid)
	if st.Status != "cancelled" || st.Decision != "cancelled" || st.Output != "no longer needed" {
		t.Fatalf("Ask status = %+v, want cancelled", st)
	}
}

// TestCancelNotifyPetition verifies cancelling a notify petition.
func TestCancelNotifyPetition(t *testing.T) {
	svc := testService(t, policy.Policy{Default: "ask"})
	runRes := svc.Notify(protocol.NotifyRequest{
		Link:       "https://example.com/2fa",
		Summary:    "Verify 2FA",
		TTLSeconds: 30,
	}, 1000)
	if runRes.Status != "pending" || runRes.ID == "" {
		t.Fatalf("Notify = %+v, want pending", runRes)
	}

	cancelRes := svc.Cancel(protocol.CancelRequest{
		ID:     runRes.ID,
		By:     "operator",
		Reason: "session closed",
	}, uint32(os.Getuid()))

	if !cancelRes.Cancelled || cancelRes.Error != "" {
		t.Fatalf("Cancel notify failed: %+v", cancelRes)
	}

	st := svc.Status(runRes.ID)
	if st.Status != "cancelled" || st.Decision != "cancelled" {
		t.Fatalf("Notify status = %+v, want cancelled", st)
	}
}

// TestCancelAuthorization verifies unauthorized cancel attempts are rejected.
func TestCancelAuthorization(t *testing.T) {
	svc := testService(t, policy.Policy{Default: "ask"})
	ridChan := make(chan string, 1)

	go func() {
		svc.RunWithPeerCancel(protocol.RunRequest{
			Argv: []string{"ls"},
			Cwd:  "/tmp",
		}, 1000, 0, nil)
	}()

	var rid string
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		items := svc.List().Items
		if len(items) > 0 {
			rid = items[0].ID
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if rid == "" {
		t.Fatal("petition not found")
	}
	ridChan <- rid

	// Caller UID 2000 is neither admin nor 1000 (petition creator)
	res := svc.Cancel(protocol.CancelRequest{
		ID:     rid,
		By:     "stranger",
		Reason: "unauthorized",
	}, 2000)

	if res.Cancelled || res.Error != "unauthorized" {
		t.Fatalf("Cancel by non-owner should be unauthorized, got %+v", res)
	}

	// Verify audit log has cancel_unauthorized
	auditPath := svc.Store.Audit
	data, err := os.ReadFile(auditPath)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "cancel_unauthorized") {
		t.Fatalf("audit file missing cancel_unauthorized event: %s", string(data))
	}
}

// TestCancelResumesProcessGroup verifies suspended process groups receive SIGCONT on cancel.
func TestCancelResumesProcessGroup(t *testing.T) {
	cmd, pgid := startGroupedSleep(t)
	svc := testService(t, policy.Policy{Default: "ask"})

	go func() {
		svc.RunWithPeerCancel(protocol.RunRequest{
			Argv: []string{"test-cmd"},
			Cwd:  "/tmp",
		}, uint32(os.Getuid()), cmd.Process.Pid, nil)
	}()

	var rid string
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		items := svc.List().Items
		if len(items) > 0 {
			rid = items[0].ID
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if rid == "" {
		t.Fatal("petition not found")
	}

	// Verify the process is stopped
	state := procState(t, cmd.Process.Pid)
	if !strings.HasPrefix(state, "T") {
		t.Fatalf("process %d state = %q, want stopped (T)", cmd.Process.Pid, state)
	}

	// Cancel petition
	cancelRes := svc.Cancel(protocol.CancelRequest{
		ID:     rid,
		By:     "operator",
		Reason: "cancel and resume",
	}, uint32(os.Getuid()))

	if !cancelRes.Cancelled {
		t.Fatalf("Cancel failed: %+v", cancelRes)
	}

	// Verify process resumed (state should no longer be T/stopped)
	resumed := false
	for i := 0; i < 20; i++ {
		st := procState(t, cmd.Process.Pid)
		if strings.HasPrefix(st, "S") || strings.HasPrefix(st, "R") {
			resumed = true
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if !resumed {
		t.Fatalf("process %d (pgid %d) was not resumed by cancel, state=%s", cmd.Process.Pid, pgid, procState(t, cmd.Process.Pid))
	}
}
