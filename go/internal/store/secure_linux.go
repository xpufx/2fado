package store

import (
	"fmt"
	"os"
	"syscall"
)

const noFollow = syscall.O_NOFOLLOW

func validateAnchorDir(path string) error {
	fi, err := os.Lstat(path)
	if err != nil {
		return err
	}
	if fi.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("anchor %s: symlinked anchor refused", path)
	}
	if !fi.IsDir() {
		return fmt.Errorf("anchor %s: not a directory", path)
	}
	if perm := fi.Mode().Perm(); perm&0o022 != 0 {
		return fmt.Errorf("anchor %s: mode %o too permissive", path, perm)
	}
	if st, ok := fi.Sys().(*syscall.Stat_t); ok && st != nil {
		if int(st.Uid) != os.Geteuid() {
			return fmt.Errorf("anchor %s: foreign owner refused", path)
		}
	}
	return nil
}

func writeNoFollow(path string, data []byte, perm os.FileMode, fsync bool) error {
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_TRUNC|noFollow, perm)
	if err != nil {
		return err
	}
	defer f.Close()
	if _, err := f.Write(data); err != nil {
		return err
	}
	if fsync {
		if err := f.Sync(); err != nil {
			return err
		}
	}
	return nil
}

func writeExclNoFollowSync(path string, data []byte, perm os.FileMode) error {
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL|noFollow, perm)
	if err != nil {
		return err
	}
	defer f.Close()
	if _, err := f.Write(data); err != nil {
		return err
	}
	return f.Sync()
}

func appendNoFollow(path string, data []byte, perm os.FileMode) error {
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_APPEND|noFollow, perm)
	if err != nil {
		return err
	}
	defer f.Close()
	_, err = f.Write(data)
	return err
}
