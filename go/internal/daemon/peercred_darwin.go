//go:build darwin

package daemon

import (
	"syscall"
)

func soPeercred(fd uintptr) (int, uint32, error) {
	// On Darwin, LOCAL_PEERPID (opt 2) on SOL_LOCAL (level 0) retrieves the peer PID.
	pid, err := syscall.GetsockoptInt(int(fd), 0, 2)
	if err != nil {
		return 0, 0, err
	}
	// Darwin sockets are restricted to user-only mode (0700); peer UID matches EUID.
	return pid, uint32(syscall.Geteuid()), nil
}
