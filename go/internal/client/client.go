// Package client implements the agent-facing subcommands over the same
// typed protocol the daemon serves.
package client

import (
	"bufio"
	"encoding/json"
	"fmt"
	"net"
	"os"

	"2fado/internal/protocol"
)

func call(sock string, msg protocol.ClientMessage) ([]byte, error) {
	c, err := net.Dial("unix", sock)
	if err != nil {
		return nil, err
	}
	defer c.Close()
	out, err := json.Marshal(msg)
	if err != nil {
		return nil, err
	}
	if _, err := c.Write(append(out, '\n')); err != nil {
		return nil, err
	}
	return bufio.NewReader(c).ReadBytes('\n')
}

// Run ships argv+cwd+env, relays output, and exits with the remote code.
func Run(sock string, argv []string) int {
	return RunWithDetach(sock, argv, false)
}

// RunWithDetach ships argv+cwd+env; with detach it returns immediately
// after the daemon accepts the request as pending.
func RunWithDetach(sock string, argv []string, detach bool) int {
	env := map[string]string{}
	for _, kv := range os.Environ() {
		for i := 0; i < len(kv); i++ {
			if kv[i] == '=' {
				env[kv[:i]] = kv[i+1:]
				break
			}
		}
	}
	cwd, _ := os.Getwd()
	raw, err := call(sock, protocol.ClientMessage{
		Run: &protocol.RunRequest{Argv: argv, Cwd: cwd, Env: env, Detach: detach},
	})
	if err != nil {
		fmt.Fprintln(os.Stderr, "2fado: "+err.Error())
		return 1
	}
	var res protocol.RunResult
	if err := json.Unmarshal(raw, &res); err != nil {
		fmt.Fprintln(os.Stderr, "2fado: bad reply")
		return 1
	}
	if res.Status == "pending" {
		if detach {
			fmt.Printf("2fado: request submitted (id: %s). Detached. Use '2fado status %s' to check outcome.\n", res.ID, res.ID)
			return 0
		}
		fmt.Fprintf(os.Stderr, "2fado: request submitted, waiting for approval (id: %s)...\n", res.ID)
		return 1
	}
	if res.Status == "allowed" {
		fmt.Print(res.Output)
		return res.Exit
	}
	fmt.Fprintf(os.Stderr, "2fado: denied (%s)\n", res.Reason)
	return 1
}

// Verdict submits approve|deny for a request id (PoC: local human).
func Verdict(sock, decision, id string) int {
	raw, err := call(sock, protocol.ClientMessage{
		Verdict: &protocol.VerdictSubmit{ID: id, Decision: decision},
	})
	if err != nil {
		fmt.Fprintln(os.Stderr, "2fado: "+err.Error())
		return 1
	}
	var ack protocol.VerdictAck
	if err := json.Unmarshal(raw, &ack); err != nil {
		fmt.Fprintln(os.Stderr, "2fado: bad reply")
		return 1
	}
	fmt.Printf("{recorded: %v}\n", ack.Recorded)
	return 0
}

// Status queries the state and execution outcome of a request id.
func Status(sock, id string) int {
	raw, err := call(sock, protocol.ClientMessage{
		Status: &protocol.StatusRequest{ID: id},
	})
	if err != nil {
		fmt.Fprintln(os.Stderr, "2fado: "+err.Error())
		return 1
	}
	var res protocol.StatusResponse
	if err := json.Unmarshal(raw, &res); err != nil {
		fmt.Fprintln(os.Stderr, "2fado: bad reply")
		return 1
	}
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	_ = enc.Encode(res)
	if res.Status == "completed" {
		return res.Exit
	}
	if res.Status == "not_found" || res.Status == "denied" || res.Status == "timeout" {
		return 1
	}
	return 0
}

// SocketVersion queries the daemon for its build version over the socket.
func SocketVersion(sock string) int {
	raw, err := call(sock, protocol.ClientMessage{
		Version: &protocol.VersionRequest{},
	})
	if err != nil {
		fmt.Fprintln(os.Stderr, "2fado: "+err.Error())
		return 1
	}
	var res protocol.VersionResponse
	if err := json.Unmarshal(raw, &res); err != nil {
		fmt.Fprintln(os.Stderr, "2fado: bad reply")
		return 1
	}
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	_ = enc.Encode(res)
	return 0
}
