//go:build linux

package daemon

import (
	"syscall"
	"unsafe"
)

type ucred struct {
	pid int32
	uid uint32
	gid uint32
}

func soPeercred(fd uintptr) (uint32, error) {
	var cred ucred
	l := uint32(unsafe.Sizeof(cred))
	_, _, errno := syscall.Syscall6(syscall.SYS_GETSOCKOPT,
		fd,
		uintptr(syscall.SOL_SOCKET),
		uintptr(syscall.SO_PEERCRED),
		uintptr(unsafe.Pointer(&cred)),
		uintptr(unsafe.Pointer(&l)), 0)
	if errno != 0 {
		return 0, errno
	}
	return cred.uid, nil
}
