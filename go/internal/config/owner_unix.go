//go:build unix

package config

import (
	"fmt"
	"os"
	"syscall"
)

func ownerUID(fi os.FileInfo) (uint32, bool) {
	st, ok := fi.Sys().(*syscall.Stat_t)
	if !ok || st == nil {
		return 0, false
	}
	return st.Uid, true
}

func checkOwner(path string, fi os.FileInfo) error {
	if uid, ok := ownerUID(fi); ok {
		if int(uid) != os.Geteuid() {
			return fmt.Errorf("anchor %s: foreign owner %d, want %d", path, uid, os.Geteuid())
		}
	}
	return nil
}
