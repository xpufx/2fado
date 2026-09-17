// Package daemon is the unix-socket transport around Service: framing,
// peer credentials, connection handling. It knows bytes and sockets;
// all decisions live in internal/service.
package daemon

import (
	"bufio"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"2fado/internal/config"

	"2fado/internal/protocol"
	"2fado/internal/service"
	"2fado/internal/store"
)

// SocketPerms is the explicit Unix socket mode policy: owner-only
// (per-user session daemon, Model A per issue #17). Reachability is
// not authorization: verdict and policy/config mutations are gated on
// the SO_PEERCRED peer UID in internal/service (issue #20), and the
// suspended-execution barrier keys off the peer PID. Tightening the
// mode shrinks who can even connect; it changes none of those gates.
//
// Compatibility: the default path stays /tmp/2fado.sock (shared dir,
// pre-existing socket symlink/DoS caveats from #17 apply). Only the
// mode changes, 0777 -> 0700; same-UID clients are unaffected.
const SocketPerms = 0o700

// Framing bounds: one newline-terminated JSON request per connection.
// MaxRequestBytes caps the buffered line (env maps can be large; 1 MiB
// admits them while bounding peer-held memory). RequestReadTimeout
// bounds how long a peer can hold a connection without completing the
// line; ResponseWriteTimeout bounds the reply write. The read deadline
// covers framing only and is cleared before dispatch so long verdict
// waits (RunWithPeerCancel) are unaffected.
var (
	MaxRequestBytes      = 1 << 20
	RequestReadTimeout   = 15 * time.Second
	ResponseWriteTimeout = 10 * time.Second
	// MaxResponseOutputBytes bounds RunResult.Output bytes marshaled into
	// the socket response (#60). Excess is cut with a truncation marker
	// so the daemon never marshals unbounded output.
	MaxResponseOutputBytes = 262144
)

// boundRunOutput truncates output with an explicit marker.
func boundRunOutput(s string) string {
	if len(s) <= MaxResponseOutputBytes {
		return s
	}
	return s[:MaxResponseOutputBytes] + fmt.Sprintf("\n…[truncated %d bytes, ceiling %d bytes]", len(s)-MaxResponseOutputBytes, MaxResponseOutputBytes)
}

var (
	daemonVersion   = "0.1.0-dev"
	daemonGitCommit = "unknown"
	daemonBuildTime = "unknown"
	daemonBinarySHA = ""
)

func SetBuildInfo(version, commit, buildTime string) {
	daemonVersion, daemonGitCommit, daemonBuildTime = version, commit, buildTime
}

func computeBinarySHA() string {
	exe, err := os.Executable()
	if err != nil {
		return "unknown"
	}
	f, err := os.Open(exe)
	if err != nil {
		return "unknown"
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "unknown"
	}
	return hex.EncodeToString(h.Sum(nil))
}

func pidFilePath(stateDir string) string {
	if p := os.Getenv("TWOFADO_PID_FILE"); p != "" {
		return p
	}
	return config.DefaultPidFile(stateDir)
}

func writePidFile(path string) error {
	if dir := filepath.Dir(path); dir != "" {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			return err
		}
	}
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_TRUNC|syscall.O_NOFOLLOW, 0o644)
	if err != nil {
		return err
	}
	defer f.Close()
	if _, err := f.Write([]byte(strconv.Itoa(os.Getpid()))); err != nil {
		return err
	}
	return f.Sync()
}

func peerUID(c net.Conn) uint32 {
	_, uid := peerCred(c)
	return uid
}

// peerCred returns the socket peer PID and UID via SO_PEERCRED. The
// PID identifies the requesting agent process group at petition time
// for the suspended-execution barrier; unknown peers yield pid 0,
// which skips the barrier.
func peerCred(c net.Conn) (int, uint32) {
	uc, ok := c.(*net.UnixConn)
	if !ok {
		return 0, 0
	}
	f, err := uc.File()
	if err != nil {
		return 0, 0
	}
	defer f.Close()
	// SO_PEERCRED via syscall is Linux-specific; keep it contained here.
	pid, uid, err := soPeercred(f.Fd())
	if err != nil {
		return 0, 0
	}
	return pid, uid
}

// enforceSocketPerms applies the SocketPerms policy after listen
// (listen honors umask, so chmod after bind) and verifies the result:
// anything other than owner-only bits is a hard startup error, never
// a silent downgrade.
func enforceSocketPerms(path string) error {
	if err := os.Chmod(path, SocketPerms); err != nil {
		return err
	}
	fi, err := os.Stat(path)
	if err != nil {
		return err
	}
	if fi.Mode().Perm() != SocketPerms {
		return fmt.Errorf("2fadod: socket %s mode %o, want %o", path, fi.Mode().Perm(), SocketPerms)
	}
	return nil
}

func Serve(svc service.Service) error {
	daemonBinarySHA = computeBinarySHA()
	if err := config.EnsureAnchorDir(svc.Conf.StateDir); err != nil {
		return err
	}
	if dir := filepath.Dir(svc.Conf.Socket); dir != "" && dir != "." {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			return err
		}
		if err := svc.Conf.ValidateAnchors(); err != nil {
			return err
		}
	}
	if err := svc.Store.Init(); err != nil {
		return err
	}
	pidFile := pidFilePath(svc.Conf.StateDir)
	if err := writePidFile(pidFile); err != nil {
		return err
	}
	defer os.Remove(pidFile)
	if fi, err := os.Lstat(svc.Conf.Socket); err == nil {
		if fi.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("2fadod: socket %s is a symlink, refused", svc.Conf.Socket)
		}
	}
	tryRemove(svc.Conf.Socket)
	l, err := net.Listen("unix", svc.Conf.Socket)
	if err != nil {
		return err
	}
	defer l.Close()
	if err := enforceSocketPerms(svc.Conf.Socket); err != nil {
		return err
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	svc.StartTelegram(ctx)
	go svc.TelegramPump()
	go svc.AdoptOrphans()
	if n, err := svc.Store.Prune(store.DefaultPruneTTL); err == nil && n > 0 {
		fmt.Printf("2fadod: pruned %d stale petition records\n", n)
	}
	fmt.Printf("2fadod listening on %s (state %s)\n", svc.Conf.Socket, svc.Conf.StateDir)
	return serveLoop(l, &svc)
}

// serveLoop accepts connections until the listener is closed. The
// close path is Listener.Close: Accept then reports net.ErrClosed and
// the loop returns nil (clean shutdown), it never spins on terminal
// errors. Transient failures back off (10ms growing to 1s, reset on
// success) instead of hot-looping.
func serveLoop(l net.Listener, svc *service.Service) error {
	backoff := 10 * time.Millisecond
	const maxBackoff = time.Second
	for {
		c, err := l.Accept()
		if err != nil {
			if errors.Is(err, net.ErrClosed) {
				return nil
			}
			var ne net.Error
			if errors.As(err, &ne) && (ne.Timeout() || ne.Temporary()) {
				time.Sleep(backoff)
				backoff = min(backoff*2, maxBackoff)
				continue
			}
			time.Sleep(backoff)
			backoff = min(backoff*2, maxBackoff)
			continue
		}
		backoff = 10 * time.Millisecond
		go handle(svc, c)
	}
}

var (
	errRequestTooLarge = errors.New("request-too-large")
	errRequestTimeout  = errors.New("request-timeout")
)

// readRequestLine reads one newline-terminated request bounded by
// MaxRequestBytes and RequestReadTimeout. Oversized, timed-out,
// empty, or unterminated lines are explicit errors; the deadline is
// cleared on success so request handling runs unbounded.
func readRequestLine(c net.Conn) ([]byte, error) {
	if err := c.SetReadDeadline(time.Now().Add(RequestReadTimeout)); err != nil {
		return nil, err
	}
	defer c.SetReadDeadline(time.Time{})
	r := bufio.NewReader(io.LimitReader(c, int64(MaxRequestBytes)+2))
	line, err := r.ReadBytes('\n')
	if err != nil {
		var ne net.Error
		if errors.As(err, &ne) && ne.Timeout() {
			return nil, errRequestTimeout
		}
		if len(line) > MaxRequestBytes {
			return nil, errRequestTooLarge
		}
		return nil, err
	}
	if len(line) > MaxRequestBytes+1 {
		return nil, errRequestTooLarge
	}
	if len(line) == 0 || (len(line) == 1 && line[0] == '\n') {
		return nil, io.ErrUnexpectedEOF
	}
	return line, nil
}

// writeJSON marshals v, appends the framing newline, and writes under
// ResponseWriteTimeout. Marshal failures fall back to the error
// envelope; write failures are returned, never swallowed.
func writeJSON(c net.Conn, v any) error {
	out, err := json.Marshal(v)
	if err != nil {
		out, _ = json.Marshal(protocol.ErrorResponse{Error: "internal-error"})
	}
	out = append(out, '\n')
	if err := c.SetWriteDeadline(time.Now().Add(ResponseWriteTimeout)); err != nil {
		return err
	}
	defer c.SetWriteDeadline(time.Time{})
	_, err = c.Write(out)
	return err
}

func writeError(c net.Conn, msg string) {
	_ = writeJSON(c, protocol.ErrorResponse{Error: msg})
}

func handle(svc *service.Service, c net.Conn) {
	defer c.Close()
	line, err := readRequestLine(c)
	if err != nil {
		switch {
		case errors.Is(err, errRequestTooLarge):
			writeError(c, "request-too-large")
		case errors.Is(err, errRequestTimeout):
			writeError(c, "request-timeout")
		default:
			writeError(c, "malformed-request")
		}
		return
	}
	var msg protocol.ClientMessage
	if err := json.Unmarshal(line, &msg); err != nil {
		writeError(c, "malformed-request")
		return
	}
	var out []byte
	peerPID, uid := peerCred(c)
	switch {
	case msg.Verdict != nil:
		ack := svc.Submit(*msg.Verdict, uid)
		out, err = json.Marshal(ack)
	case msg.Notify != nil:
		res := svc.Notify(*msg.Notify, uid)
		out, err = json.Marshal(res)
	case msg.Ack != nil:
		ack := svc.Ack(*msg.Ack, uid)
		out, err = json.Marshal(ack)
	case msg.Run != nil:
		disconnect := make(chan struct{})
		var once sync.Once
		go func() {
			var b [1]byte
			_, _ = c.Read(b[:])
			once.Do(func() { close(disconnect) })
		}()
		res := svc.RunWithPeerCancel(*msg.Run, uid, peerPID, disconnect)
		res.Output = boundRunOutput(res.Output)
		out, err = json.Marshal(res)
	case msg.List != nil:
		out, err = json.Marshal(svc.List())
	case msg.Recent != nil:
		out, err = json.Marshal(svc.Recent(msg.Recent.Limit))
	case msg.Status != nil:
		out, err = json.Marshal(svc.Status(msg.Status.ID))
	case msg.TelegramInfo != nil:
		out, err = json.Marshal(svc.TelegramInfo())
	case msg.TelegramSetConfig != nil:
		out, err = json.Marshal(svc.TelegramSetConfig(*msg.TelegramSetConfig, uid))
	case msg.PolicyAddRule != nil:
		out, err = json.Marshal(svc.PolicyAddRule(*msg.PolicyAddRule, uid))
	case msg.Help != nil:
		ops, ok := protocol.Describe(msg.Help.Op)
		if !ok {
			writeError(c, "unknown-help-op:"+msg.Help.Op)
			return
		}
		out, err = json.Marshal(protocol.HelpResponse{Ops: ops})
	case msg.Version != nil:
		out, err = json.Marshal(protocol.VersionResponse{
			Version:      daemonVersion,
			GitCommit:    daemonGitCommit,
			BuildTime:    daemonBuildTime,
			BinarySHA256: daemonBinarySHA,
			PID:          os.Getpid(),
		})
	default:
		writeError(c, "unknown-request: valid ops: "+strings.Join(protocol.OpKeys(), ","))
		return
	}
	if err != nil {
		writeError(c, "internal-error")
		return
	}
	out = append(out, '\n')
	if err := c.SetWriteDeadline(time.Now().Add(ResponseWriteTimeout)); err != nil {
		return
	}
	defer c.SetWriteDeadline(time.Time{})
	_, _ = c.Write(out)
}

func tryRemove(path string) {
	_ = os.Remove(path)
}
