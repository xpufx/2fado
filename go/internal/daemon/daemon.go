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
	"fmt"
	"io"
	"net"
	"os"
	"strconv"
	"sync"

	"2fado/internal/protocol"
	"2fado/internal/service"
)

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

func pidFilePath() string {
	if p := os.Getenv("TWOFADO_PID_FILE"); p != "" {
		return p
	}
	return "/tmp/2fado.pid"
}

func peerUID(c net.Conn) uint32 {
	uc, ok := c.(*net.UnixConn)
	if !ok {
		return 0
	}
	f, err := uc.File()
	if err != nil {
		return 0
	}
	defer f.Close()
	// SO_PEERCRED via syscall is Linux-specific; keep it contained here.
	uid, err := soPeercred(f.Fd())
	if err != nil {
		return 0
	}
	return uid
}

func Serve(svc service.Service) error {
	daemonBinarySHA = computeBinarySHA()
	pidFile := pidFilePath()
	_ = os.WriteFile(pidFile, []byte(strconv.Itoa(os.Getpid())), 0o644)
	defer os.Remove(pidFile)
	tryRemove(svc.Conf.Socket)
	l, err := net.Listen("unix", svc.Conf.Socket)
	if err != nil {
		return err
	}
	defer l.Close()
	if err := os.Chmod(svc.Conf.Socket, 0o777); err != nil { // PoC; prod: dir perms + uid checks
		return err
	}
	if err := svc.Store.Init(); err != nil {
		return err
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	svc.StartTelegram(ctx)
	go svc.TelegramPump()
	fmt.Printf("2fadod listening on %s (state %s)\n", svc.Conf.Socket, svc.Conf.StateDir)
	for {
		c, err := l.Accept()
		if err != nil {
			continue
		}
		go handle(&svc, c)
	}
}

func handle(svc *service.Service, c net.Conn) {
	defer c.Close()
	line, err := bufio.NewReader(c).ReadBytes('\n')
	if err != nil {
		return
	}
	var msg protocol.ClientMessage
	if err := json.Unmarshal(line, &msg); err != nil {
		return
	}
	var out []byte
	switch {
	case msg.Verdict != nil:
		ack := svc.Submit(*msg.Verdict)
		out, _ = json.Marshal(ack)
	case msg.Run != nil:
		disconnect := make(chan struct{})
		var once sync.Once
		go func() {
			var b [1]byte
			_, _ = c.Read(b[:])
			once.Do(func() { close(disconnect) })
		}()
		res := svc.RunWithCancel(*msg.Run, peerUID(c), disconnect)
		out, _ = json.Marshal(res)
	case msg.List != nil:
		out, _ = json.Marshal(svc.List())
	case msg.Recent != nil:
		out, _ = json.Marshal(svc.Recent(msg.Recent.Limit))
	case msg.Status != nil:
		out, _ = json.Marshal(svc.Status(msg.Status.ID))
	case msg.TelegramInfo != nil:
		out, _ = json.Marshal(svc.TelegramInfo())
	case msg.TelegramSetConfig != nil:
		out, _ = json.Marshal(svc.TelegramSetConfig(*msg.TelegramSetConfig))
	case msg.PolicyAddRule != nil:
		out, _ = json.Marshal(svc.PolicyAddRule(*msg.PolicyAddRule))
	case msg.Version != nil:
		out, _ = json.Marshal(protocol.VersionResponse{
			Version:      daemonVersion,
			GitCommit:    daemonGitCommit,
			BuildTime:    daemonBuildTime,
			BinarySHA256: daemonBinarySHA,
			PID:          os.Getpid(),
		})
	default:
		return
	}
	out = append(out, '\n')
	_, _ = c.Write(out)
}

func tryRemove(path string) {
	_ = os.Remove(path)
}
