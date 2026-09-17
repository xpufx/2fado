//go:build !escalation

package service

import (
	"os"
	"syscall"

	"2fado/internal/protocol"
)

// This file is the DEFAULT (public release) build: escalation and
// privilege-switching code is not compiled in at all. Build with
// `-tags escalation` to include the uid-switching implementation in
// escalation_on.go (#61).

// escalationEnabled is the release gate shared by both builds. In the
// default build privilege switching is absent from the binary entirely,
// so this is false and unreachable-in-practice.
const escalationEnabled = false

// rootExecAllowed is the pure uid-0 gate. Only reachable in the escalation
// build; kept here so both builds expose the same symbol set.
func rootExecAllowed(targetUID uint32, allowRoot bool, euid uint32) bool {
	_ = euid
	if targetUID != 0 {
		return true
	}
	return allowRoot
}

// spawnAttrs returns the SysProcAttr to attach before spawning. In the
// default build there is nothing privileged to attach: the child always
// runs as the daemon's own euid, so no credential is ever resolved and
// no uid-switching symbol is compiled in. Returns nil attributes.
func (s Service) spawnAttrs(_ uint32) (*syscall.SysProcAttr, error) {
	return nil, nil
}

// execIdentity resolves the uid to execute as. Default build (#54): always
// the daemon's own euid; any policy target resolving to a different uid is
// a fail-closed refusal (no spawn) with an escalation_disabled audit event.
func (s Service) execIdentity(argv []string, cwd string) (uint32, *string) {
	uid, err := s.targetUID()
	if err != nil {
		msg := "2fadod: bad target_user: " + err.Error()
		return uint32(os.Geteuid()), &msg
	}
	self := uint32(os.Geteuid())
	if uid == 0 || uid != self {
		s.Store.Append(protocol.AuditEvent{Ev: "escalation_disabled", UID: self,
			Argv: argv, Cwd: cwd, AsUID: uid})
		msg := "2fadod: refusing execution as another user (escalation is not in this release; runs as the daemon user only)"
		return self, &msg
	}
	return self, nil
}

// displayUID reports the execution identity for approval surfaces (#53).
// In the default build it is always the daemon user.
func (s Service) displayUID() uint32 {
	return uint32(os.Geteuid())
}

// confirmAsUID resolves the identity recorded on a confirm step. Default
// build: always the daemon's own euid.
func (s Service) confirmAsUID(_ uint32) uint32 {
	return uint32(os.Geteuid())
}
