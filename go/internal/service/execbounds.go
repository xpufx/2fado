package service

import (
	"bytes"
	"context"
	"fmt"
	"os/exec"
	"syscall"
	"sync/atomic"
	"time"
)

// TruncationMarkerFmt marks cut output with the dropped byte count.
const TruncationMarkerFmt = "\n…[truncated %d bytes, ceiling %d bytes]"

// DefaultMaxOutputBytes applies when Conf.MaxOutputBytes is unset.
const DefaultMaxOutputBytes = 262144

// DefaultExecTimeout is the fallback wall-clock execution timeout.
const DefaultExecTimeout = 120 * time.Second

// capBytes resolves the capture ceiling.
func capBytes(conf int) int {
	if conf > 0 {
		return conf
	}
	return DefaultMaxOutputBytes
}

// execTimeout resolves the wall-clock timeout; <=0 disables.
func execTimeout(secs int) (time.Duration, bool) {
	if secs < 0 {
		return 0, false
	}
	if secs == 0 {
		return DefaultExecTimeout, true
	}
	return time.Duration(secs) * time.Second, true
}

// cappedWriter is an io.Writer with a hard byte ceiling.
type cappedWriter struct {
	buf     bytes.Buffer
	max     int
	dropped int64
}

func newCappedWriter(max int) *cappedWriter { return &cappedWriter{max: max} }

func (w *cappedWriter) Write(p []byte) (int, error) {
	room := w.max - w.buf.Len()
	if room <= 0 {
		w.dropped += int64(len(p))
		return len(p), nil
	}
	if len(p) > room {
		w.buf.Write(p[:room])
		w.dropped += int64(len(p) - room)
		return len(p), nil
	}
	w.buf.Write(p)
	return len(p), nil
}

func (w *cappedWriter) String() string {
	s := w.buf.String()
	if w.dropped > 0 {
		s += fmt.Sprintf(TruncationMarkerFmt, w.dropped, w.max)
	}
	return s
}

// boundOutput truncates s to max bytes with an explicit marker.
func boundOutput(s string, max int) string {
	if max <= 0 || len(s) <= max {
		return s
	}
	return s[:max] + fmt.Sprintf(TruncationMarkerFmt, len(s)-max, max)
}

// startBounded starts cmd with a process group + timeout; caller waits.
// It sets Setsid (new group) preserving any Credential, starts the
// command, and arms a timer killing the whole group (-pid) on expiry.
//
// The pid is published to the timer goroutine through an atomic only
// after Start() succeeds, so the timer never touches cmd.Process
// concurrently with os/exec (which would be a data race).
func startBounded(cmd *exec.Cmd, d time.Duration, enabled bool) (timedOut *atomic.Bool, pid *atomic.Int64, startErr error, stop func()) {
	flag := &atomic.Bool{}
	proc := &atomic.Int64{}
	if cmd.SysProcAttr == nil {
		cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	} else {
		cmd.SysProcAttr.Setsid = true
	}
	if !enabled {
		return flag, proc, nil, func() {}
	}
	if err := cmd.Start(); err != nil {
		return flag, proc, err, func() {}
	}
	proc.Store(int64(cmd.Process.Pid))
	t := time.AfterFunc(d, func() {
		flag.Store(true)
		if p := proc.Load(); p > 0 {
			_ = syscall.Kill(-int(p), syscall.SIGKILL)
		}
	})
	return flag, proc, nil, func() { t.Stop() }
}

// runBounded runs cmd to completion with output to out.
func runBounded(ctx context.Context, cmd *exec.Cmd, timeout time.Duration, useTimeout bool) (timedOut bool, runErr error) {
	flag, _, startErr, stop := startBounded(cmd, timeout, useTimeout)
	defer stop()
	if startErr != nil {
		return false, startErr
	}
	// When the timeout is armed, startBounded already called Start(), so we
	// must Wait() rather than Run() to avoid a double-start.
	if useTimeout {
		runErr = cmd.Wait()
	} else {
		runErr = cmd.Run()
	}
	_ = ctx
	return flag.Load(), runErr
}

// execCmdContext builds a cancellable command honoring ctx.
func execCmdContext(ctx context.Context, name string, argv []string) *exec.Cmd {
	if ctx != nil {
		return exec.CommandContext(ctx, name, argv...)
	}
	return exec.Command(name, argv...)
}
