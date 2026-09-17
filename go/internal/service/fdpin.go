// Entrypoint fd pinning: TOCTOU mitigation for binary swap and symlink
// redirection between petition (T0) and approval (T1). The daemon opens
// the entrypoint O_RDONLY|O_CLOEXEC at T0, hashes it, and records
// dev/ino identity. At T1 it re-opens, re-verifies, and executes via
// the verified fd (/dev/fd/N), so the verify-to-exec gap runs against
// the same open file description. Scripts run as interpreter + /dev/fd.
package service

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"

	"2fado/internal/protocol"
)

const maxPinHashBytes = 32 << 20

// snapshotEntry pins the entrypoint for argv in cwd. It returns nil when
// there is nothing to pin (empty argv) or the file is unusable, so
// callers degrade to a normal exec instead of denying.
func snapshotEntry(argv []string, cwd string) *protocol.FDPin {
	if len(argv) == 0 || strings.TrimSpace(argv[0]) == "" {
		return nil
	}
	path := resolveBinary(argv[0], cwd, []string{"PATH=" + safePath})
	f, err := os.OpenFile(path, os.O_RDONLY|syscall.O_CLOEXEC, 0)
	if err != nil {
		return nil
	}
	defer f.Close()
	return pinFromFile(path, f)
}

// pinFromFile hashes an already-open entrypoint and records identity.
// The caller owns f; this never closes it.
func pinFromFile(path string, f *os.File) *protocol.FDPin {
	fi, err := f.Stat()
	if err != nil || fi.IsDir() {
		return nil
	}
	var st syscall.Stat_t
	dev, ino := uint64(0), uint64(0)
	if err := syscall.Fstat(int(f.Fd()), &st); err == nil {
		dev, ino = uint64(st.Dev), uint64(st.Ino)
	}
	if _, err := f.Seek(0, io.SeekStart); err != nil {
		return nil
	}
	h := sha256.New()
	if _, err := io.CopyN(h, f, maxPinHashBytes); err != nil && err != io.EOF {
		return nil
	}
	sum := hex.EncodeToString(h.Sum(nil))
	resolved := path
	if rp, err := filepath.EvalSymlinks(path); err == nil {
		resolved = rp
	}
	pin := &protocol.FDPin{
		Path:     path,
		Resolved: resolved,
		SHA256:   sum,
		Dev:      dev,
		Ino:      ino,
		Size:     fi.Size(),
		Mode:     uint32(fi.Mode()),
	}
	if interp, arg, ok := parseShebang(f); ok {
		pin.IsScript = true
		pin.Interpreter = interp
		pin.InterpArg = arg
	}
	return pin
}

// parseShebang reads the interpreter from a #! first line. It seeks
// back to the start so later hashing sees the full file.
func parseShebang(f *os.File) (string, string, bool) {
	if _, err := f.Seek(0, io.SeekStart); err != nil {
		return "", "", false
	}
	head := make([]byte, 512)
	n, _ := f.Read(head)
	head = head[:n]
	if _, err := f.Seek(0, io.SeekStart); err != nil {
		return "", "", false
	}
	if len(head) < 2 || head[0] != '#' || head[1] != '!' {
		return "", "", false
	}
	line := string(head[2:])
	if i := strings.IndexByte(line, '\n'); i >= 0 {
		line = line[:i]
	}
	line = strings.TrimSpace(strings.TrimRight(line, "\r"))
	if line == "" {
		return "", "", false
	}
	fields := strings.Fields(line)
	interp, arg := fields[0], ""
	if interp == "/usr/bin/env" && len(fields) > 1 {
		interp = fields[1]
		if len(fields) > 2 {
			arg = strings.Join(fields[2:], " ")
		}
	} else if len(fields) > 1 {
		arg = strings.Join(fields[1:], " ")
	}
	if interp == "" {
		return "", "", false
	}
	return interp, arg, true
}

// openVerified re-opens the pinned path and checks content + identity.
// On success it returns the open fd, positioned at offset 0, for the
// caller to exec via /dev/fd and close. On mismatch it returns drift
// with Block=true and a closed (nil) fd.
func openVerified(pin *protocol.FDPin) (*os.File, *protocol.FDDrift) {
	if pin == nil || pin.Path == "" {
		return nil, nil
	}
	f, err := os.OpenFile(pin.Path, os.O_RDONLY|syscall.O_CLOEXEC, 0)
	if err != nil {
		return nil, &protocol.FDDrift{Drift: true, Block: true,
			Reason: "entrypoint at " + pin.Path + " is gone at approval"}
	}
	live := pinFromFile(pin.Path, f)
	if live == nil {
		f.Close()
		return nil, &protocol.FDDrift{Drift: true, Block: true,
			Reason: "entrypoint at " + pin.Path + " is unreadable at approval"}
	}
	if live.SHA256 != pin.SHA256 {
		f.Close()
		return nil, &protocol.FDDrift{Drift: true, Block: true,
			Reason: "entrypoint content changed since petition (" + shortHash(pin.SHA256) + " -> " + shortHash(live.SHA256) + ")"}
	}
	if pin.Ino != 0 && (live.Dev != pin.Dev || live.Ino != pin.Ino) {
		f.Close()
		return nil, &protocol.FDDrift{Drift: true, Block: true,
			Reason: "entrypoint replaced since petition (binary swap or symlink redirect)"}
	}
	if _, err := f.Seek(0, io.SeekStart); err != nil {
		f.Close()
		return nil, &protocol.FDDrift{Drift: true, Block: true,
			Reason: "entrypoint at " + pin.Path + " is unreadable at approval"}
	}
	return f, nil
}

// checkFDDrift compares the T0 pin against the live entrypoint. It
// returns nil when there is nothing to check so execution proceeds.
func (s Service) checkFDDrift(rid string) *protocol.FDDrift {
	rec, err := s.Store.Load(rid)
	if err != nil || rec.FD == nil || rec.FD.Path == "" {
		return nil
	}
	f, drift := openVerified(rec.FD)
	if drift != nil {
		return drift
	}
	f.Close()
	return nil
}

func shortHash(h string) string {
	if len(h) > 12 {
		return h[:12]
	}
	if h == "" {
		return "unknown"
	}
	return h
}

// resolveInterp finds a shebang interpreter on PATH when relative.
func resolveInterp(interp string) string {
	if interp == "" {
		return ""
	}
	if filepath.IsAbs(interp) || strings.Contains(interp, "/") {
		return interp
	}
	if lp, err := exec.LookPath(interp); err == nil {
		return lp
	}
	return interp
}

// pinnedCmd builds an exec.Cmd running argv via the verified fd. The fd
// arrives in the child as /dev/fd/3; scripts run as interpreter +
// /dev/fd/3 plus the original extra args.
func pinnedCmd(argv []string, cwd string, env []string, pin *protocol.FDPin, fd *os.File) *exec.Cmd {
	const fdPath = "/dev/fd/3"
	if pin != nil && pin.IsScript && pin.Interpreter != "" {
		interp := resolveInterp(pin.Interpreter)
		args := []string{}
		if pin.InterpArg != "" {
			args = append(args, strings.Fields(pin.InterpArg)...)
		}
		args = append(args, fdPath)
		args = append(args, argv[1:]...)
		cmd := exec.Command(interp, args...)
		cmd.Dir = cwd
		cmd.Env = env
		cmd.ExtraFiles = []*os.File{fd}
		return cmd
	}
	args := append([]string{}, argv...)
	if len(args) > 0 {
		args[0] = fdPath
	}
	var cmd *exec.Cmd
	if len(args) > 1 {
		cmd = exec.Command(args[0], args[1:]...)
	} else {
		cmd = exec.Command(args[0])
	}
	cmd.Dir = cwd
	cmd.Env = env
	cmd.ExtraFiles = []*os.File{fd}
	return cmd
}

// runPinned runs argv via the verified entrypoint fd with the standard
// credential drop, cwd/env reconstruction, and challenge scanning. The
// caller must have verified pin; a nil pin falls back to plain exec.
func (s Service) runPinned(argv []string, cwd string, env map[string]string, pin *protocol.FDPin, onAuth func(string)) (int, string, uint32) {
	if s.Conf.DryRun {
		return 0, "would have run: " + strings.Join(argv, " "), uint32(os.Geteuid())
	}
	uid, refusal := s.execIdentity(argv, cwd)
	if refusal != nil {
		return 1, *refusal, uid
	}
	flat := flatten(env)
	var cred *syscall.Credential
	if escalationEnabled && (uid != uint32(os.Geteuid()) || os.Geteuid() == 0) {
		var credErr error
		cred, credErr = resolveCredential(uid)
		if credErr != nil {
			return 1, "2fadod: bad target_user: " + credErr.Error(), uint32(os.Geteuid())
		}
	}
	if pin == nil {
		return s.execPlain(argv, cwd, flat, uid, cred, onAuth)
	}
	fd, drift := openVerified(pin)
	if drift != nil {
		return 1, "fd-drift: " + drift.Reason, uid
	}
	defer fd.Close()
	cmd := pinnedCmd(argv, cwd, flat, pin, fd)
	if cred != nil {
		cmd.SysProcAttr = &syscall.SysProcAttr{Credential: cred, Setsid: true}
	}
	return s.runCmdBounded(cmd, uid, onAuth)
}

// runCmdBounded wires capped capture + wall-clock group kill around cmd.
func (s Service) runCmdBounded(cmd *exec.Cmd, uid uint32, onAuth func(string)) (int, string, uint32) {
	maxOut := capBytes(s.Conf.MaxOutputBytes)
	w := newCappedWriter(maxOut)
	out := io.Writer(w)
	if onAuth != nil {
		out = challengeScanner(w, onAuth)
	}
	cmd.Stdout = out
	cmd.Stderr = out
	d, useTimeout := execTimeout(s.Conf.ExecTimeoutSecs)
	timedOut, runErr := runBounded(context.Background(), cmd, d, useTimeout)
	captured := w.String()
	if timedOut {
		return 124, captured + "\n2fadod: execution timeout", uid
	}
	if runErr != nil {
		if ee, ok := runErr.(*exec.ExitError); ok {
			return ee.ExitCode(), captured, uid
		}
		return 1, captured + runErr.Error(), uid
	}
	return 0, captured, uid
}

// execPlain is the unpinned path: same credential/cwd/env handling as
// runPinned without fd verification.
func (s Service) execPlain(argv []string, cwd string, flat []string, uid uint32, cred *syscall.Credential, onAuth func(string)) (int, string, uint32) {
	cmd := execCmdContext(context.Background(), argv[0], argv[1:])
	cmd.Dir = cwd
	cmd.Env = flat
	if cred != nil {
		cmd.SysProcAttr = &syscall.SysProcAttr{Credential: cred, Setsid: true}
	}
	return s.runCmdBounded(cmd, uid, onAuth)
}
