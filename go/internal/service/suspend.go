// Suspended execution: TOCTOU mitigation that holds the requesting
// agent's process group with SIGSTOP during the review window and
// resumes it with SIGCONT on verdict, timeout, or client abort. It
// stops a single-threaded agent racing the operator, not a full
// sandbox: background groups, other UIDs, and pre-stop mutations are
// outside this barrier. Every signal is best-effort and audited; a
// failed stop never denies the petition.
package service

import (
	"os"
	"strings"
	"syscall"

	"2fado/internal/protocol"
)

// suspendEnabled reports whether the STOP/CONT barrier applies.
// TWOFADO_SUSPEND=0/false/off/no disables it; default enabled.
func suspendEnabled() bool {
	switch strings.ToLower(strings.TrimSpace(os.Getenv("TWOFADO_SUSPEND"))) {
	case "0", "false", "off", "no", "disable", "disabled":
		return false
	}
	return true
}

// resolveSuspendPGID identifies the agent process group at petition time
// from the socket peer PID. It returns ok=false when there is nothing
// safe to signal: unknown pid, disabled barrier, UID the daemon may not
// signal, or the daemon's own group.
func resolveSuspendPGID(peerPID int, uid uint32) (int, bool) {
	if peerPID <= 0 || peerPID == os.Getpid() {
		return 0, false
	}
	if !suspendEnabled() {
		return 0, false
	}
	self := uint32(os.Geteuid())
	if self != 0 && uid != self {
		return 0, false
	}
	pgid, err := syscall.Getpgid(peerPID)
	if err != nil || pgid <= 1 {
		return 0, false
	}
	if pgid == syscall.Getpgrp() {
		return 0, false
	}
	return pgid, true
}

// stopPGID freezes the group; contPGID resumes it. Negative-pid Kill
// targets the group. Callers treat errors as advisory, never fatal.
func stopPGID(pgid int) error {
	if pgid <= 1 || pgid == syscall.Getpgrp() {
		return syscall.EINVAL
	}
	return syscall.Kill(-pgid, syscall.SIGSTOP)
}

func contPGID(pgid int) error {
	if pgid <= 1 {
		return syscall.EINVAL
	}
	return syscall.Kill(-pgid, syscall.SIGCONT)
}

// snapshotSuspend pins the caller for rid: resolve, stop, record. It
// returns the pin for the stored record (nil when nothing to hold) so
// crash recovery can still resume the group later.
func (s Service) snapshotSuspend(rid string, peerPID int, uid uint32) *protocol.SuspendPin {
	pgid, ok := resolveSuspendPGID(peerPID, uid)
	if !ok {
		return nil
	}
	pin := &protocol.SuspendPin{PID: peerPID, PGID: pgid}
	if err := stopPGID(pgid); err != nil {
		s.Store.Append(protocol.AuditEvent{Ev: "suspend", UID: uid,
			RID: rid, Decision: "skip", Summary: err.Error()})
		return pin
	}
	pin.Suspended = true
	s.Store.Append(protocol.AuditEvent{Ev: "suspend", UID: uid, RID: rid})
	if rec, err := s.Store.Load(rid); err == nil {
		rec.Suspend = pin
		_ = s.Store.Save(rec, rid)
	}
	return pin
}

// resumeSuspend releases a held group and audits the outcome. Nil and
// un-suspended pins are no-ops; unknown groups (ESRCH) still audit.
func (s Service) resumeSuspend(rid string, pin *protocol.SuspendPin) {
	if pin == nil || !pin.Suspended || pin.PGID <= 1 {
		return
	}
	errStr := ""
	if err := contPGID(pin.PGID); err != nil {
		errStr = err.Error()
	}
	ev := protocol.AuditEvent{Ev: "resume", RID: rid}
	if errStr != "" {
		ev.Decision = "skip"
		ev.Summary = errStr
	}
	s.Store.Append(ev)
}

// resumeStored best-effort resumes the group recorded on rid, used when
// the in-memory pin is gone (daemon restart, adopted orphan).
func (s Service) resumeStored(rid string) {
	rec, err := s.Store.Load(rid)
	if err != nil || rec.Suspend == nil {
		return
	}
	s.resumeSuspend(rid, rec.Suspend)
}
