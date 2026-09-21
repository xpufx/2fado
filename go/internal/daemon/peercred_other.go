//go:build !linux && !darwin

package daemon

func soPeercred(fd uintptr) (int, uint32, error) {
	return 0, 0, nil
}
