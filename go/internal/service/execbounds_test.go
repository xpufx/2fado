package service

import (
	"context"
	"os/exec"
	"strings"
	"testing"
	"time"
)

// TestCappedWriterTruncatesWithMarker covers the byte ceiling (#60):
// excess output is dropped and an explicit marker records how much.
func TestCappedWriterTruncatesWithMarker(t *testing.T) {
	w := newCappedWriter(10)
	if _, err := w.Write([]byte("0123456789ABCDEF")); err != nil {
		t.Fatalf("write: %v", err)
	}
	got := w.String()
	if !strings.HasPrefix(got, "0123456789") {
		t.Errorf("prefix = %q, want 0123456789", got)
	}
	if !strings.Contains(got, "truncated 6 bytes") {
		t.Errorf("marker missing dropped count: %q", got)
	}
	if len(got) > 10+len(TruncationMarkerFmt)+8 {
		t.Errorf("output not bounded: len=%d", len(got))
	}
}

// TestCappedWriterUnderCeilingIsLossless ensures the common case is untouched
// and carries no marker.
func TestCappedWriterUnderCeilingIsLossless(t *testing.T) {
	w := newCappedWriter(100)
	if _, err := w.Write([]byte("hello")); err != nil {
		t.Fatalf("write: %v", err)
	}
	if got := w.String(); got != "hello" {
		t.Errorf("got %q, want %q", got, "hello")
	}
}

// TestRunBoundedKillsOnTimeout is the "timeout kills" case: an overlong
// command is killed at the deadline and reported as timed out.
func TestRunBoundedKillsOnTimeout(t *testing.T) {
	cmd := exec.Command("sleep", "30")
	start := time.Now()
	timedOut, err := runBounded(context.Background(), cmd, 100*time.Millisecond, true)
	elapsed := time.Since(start)

	if !timedOut {
		t.Errorf("timedOut = false, want true")
	}
	if err == nil {
		t.Errorf("err = nil, want non-nil after kill")
	}
	if elapsed > 5*time.Second {
		t.Errorf("kill did not take effect promptly: %v", elapsed)
	}
}

// TestRunBoundedCompletesUnderTimeout ensures the happy path still returns
// output and exit status without a spurious timeout.
func TestRunBoundedCompletesUnderTimeout(t *testing.T) {
	cmd := exec.Command("echo", "ok")
	timedOut, err := runBounded(context.Background(), cmd, 10*time.Second, true)
	if timedOut {
		t.Errorf("timedOut = true, want false")
	}
	if err != nil {
		t.Errorf("err = %v, want nil", err)
	}
}

// TestRunBoundedTimeoutDisabled runs with the timeout disabled, exercising
// the plain Run() path.
func TestRunBoundedTimeoutDisabled(t *testing.T) {
	cmd := exec.Command("echo", "unbounded")
	timedOut, err := runBounded(context.Background(), cmd, 0, false)
	if timedOut {
		t.Errorf("timedOut = true, want false")
	}
	if err != nil {
		t.Errorf("err = %v, want nil", err)
	}
}

// TestExecTimeoutResolution pins the config semantics: negative disables,
// zero selects the default, positive is honoured.
func TestExecTimeoutResolution(t *testing.T) {
	if _, enabled := execTimeout(-1); enabled {
		t.Errorf("negative should disable the timeout")
	}
	if d, enabled := execTimeout(0); !enabled || d != DefaultExecTimeout {
		t.Errorf("zero should select the default, got %v enabled=%v", d, enabled)
	}
	if d, enabled := execTimeout(5); !enabled || d != 5*time.Second {
		t.Errorf("5 should be 5s, got %v enabled=%v", d, enabled)
	}
}

// TestBoundOutputTruncates covers the socket-response ceiling helper.
func TestBoundOutputTruncates(t *testing.T) {
	if got := boundOutput("abc", 10); got != "abc" {
		t.Errorf("under ceiling changed: %q", got)
	}
	got := boundOutput("0123456789ABCDEF", 10)
	if !strings.HasPrefix(got, "0123456789") || !strings.Contains(got, "truncated") {
		t.Errorf("over ceiling not truncated with marker: %q", got)
	}
	if got := boundOutput("abc", 0); got != "abc" {
		t.Errorf("max<=0 should pass through: %q", got)
	}
}
