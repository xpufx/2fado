//go:build escalation

package service

import (
	"os"
	"os/user"
	"strconv"
	"syscall"

	"2fado/internal/protocol"
)

// This file is compiled ONLY under `-tags escalation`. It carries the
// uid-switching and privilege-escalation implementation that the public
// release deliberately excludes (#61).
//
// WARNING: the escalation paths are gated by escalationEnabled (false in
// this release, #54 Path A) and the structural defects documented in #56
// are unresolved. Enabling this tag does NOT produce a safe privileged
// build; it exists so the code can be developed and tested out-of-band
// while the default binary provably lacks it.

// escalationEnabled is the release gate shared by both builds. Even in the
// escalation build the release switch defaults off (#54 Path A).
const escalationEnabled = false

// rootExecAllowed is the pure uid-0 gate: only uid-0 is gated, and only
// the daemon-start opt-in (allowRoot) permits it.
func rootExecAllowed(targetUID uint32, allowRoot bool, euid uint32) bool {
	_ = euid
	if targetUID != 0 {
		return true
	}
	return allowRoot
}

// resolveCredential builds the full credential set for the target UID:
// real primary GID plus supplementary groups, so the child drops every
// caller group instead of inheriting e.g. root/wheel/sudo/docker.
func resolveCredential(uid uint32) (*syscall.Credential, error) {
	u, err := user.LookupId(strconv.Itoa(int(uid)))
	if err != nil {
		return nil, err
	}
	gid, err := strconv.Atoi(u.Gid)
	if err != nil {
		return nil, err
	}
	var groups []uint32
	if gids, err := u.GroupIds(); err == nil {
		for _, g := range gids {
			if n, err := strconv.Atoi(g); err == nil {
				groups = append(groups, uint32(n))
			}
		}
	}
	return &syscall.Credential{Uid: uid, Gid: uint32(gid), Groups: groups}, nil
}

// spawnAttrs returns the SysProcAttr to attach before spawning, including
// a Credential when the target uid differs from the daemon's euid (or the
// daemon is root). Mirrors the historical inline logic, now build-tagged.
func (s Service) spawnAttrs(uid uint32) (*syscall.SysProcAttr, error) {
	if !escalationEnabled {
		return nil, nil
	}
	if uid == uint32(os.Geteuid()) && os.Geteuid() != 0 {
		return nil, nil
	}
	cred, err := resolveCredential(uid)
	if err != nil {
		return nil, err
	}
	return &syscall.SysProcAttr{Credential: cred, Setsid: true}, nil
}

// execIdentity resolves the uid to execute as under the release switch.
func (s Service) execIdentity(argv []string, cwd string) (uint32, *string) {
	uid, err := s.targetUID()
	if err != nil {
		msg := "2fadod: bad target_user: " + err.Error()
		return uint32(os.Geteuid()), &msg
	}
	self := uint32(os.Geteuid())
	if !escalationEnabled {
		if uid == 0 || uid != self {
			s.Store.Append(protocol.AuditEvent{Ev: "escalation_disabled", UID: self,
				Argv: argv, Cwd: cwd, AsUID: uid})
			msg := "2fadod: refusing execution as another user (escalation is not in this release; runs as the daemon user only)"
			return self, &msg
		}
		return self, nil
	}
	if !rootExecAllowed(uid, s.Conf.AllowRootExec, self) {
		s.Store.Append(protocol.AuditEvent{Ev: "root_exec_blocked", UID: self,
			Argv: argv, Cwd: cwd, AsUID: uid})
		msg := "2fadod: refusing uid-0 execution (daemon started without --allow-root / ALLOW_ROOT=1)"
		return self, &msg
	}
	return uid, nil
}

// displayUID reports the execution identity for approval surfaces (#53).
func (s Service) displayUID() uint32 {
	if !escalationEnabled {
		return uint32(os.Geteuid())
	}
	uid, err := s.targetUID()
	if err != nil {
		return uint32(os.Geteuid())
	}
	return uid
}

// confirmAsUID resolves the identity recorded on a confirm step.
func (s Service) confirmAsUID(stored uint32) uint32 {
	if !escalationEnabled {
		self := uint32(os.Geteuid())
		if stored != self {
			return self
		}
		return self
	}
	return s.displayUID()
}
