// Package daemon is the unix-socket transport around Service: framing,
// peer credentials, connection handling. It knows bytes and sockets;
// all decisions live in internal/service.
package daemon

import (
	"bufio"
	"encoding/json"
	"fmt"
	"net"
	"os"

	"2fado/internal/protocol"
	"2fado/internal/service"
)

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
	if !svc.Conf.Placeholder() {
		go svc.TG.Poll(svc.Store.GetOffset(), svc.Approvers, svc.Verdicts, svc.Store.SetOffset)
	} else {
		fmt.Println("2fadod: no BOT_TOKEN configured, pager=stdout; verdicts via `2fado approve|deny <id>`")
	}
	go svc.TelegramPump()
	fmt.Printf("2fadod listening on %s (state %s)\n", svc.Conf.Socket, svc.Conf.StateDir)
	for {
		c, err := l.Accept()
		if err != nil {
			continue
		}
		go handle(svc, c)
	}
}

func handle(svc service.Service, c net.Conn) {
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
		res := svc.Run(*msg.Run, peerUID(c))
		out, _ = json.Marshal(res)
	default:
		return
	}
	out = append(out, '\n')
	_, _ = c.Write(out)
}

func tryRemove(path string) {
	_ = os.Remove(path)
}
