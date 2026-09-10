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
		Run: &protocol.RunRequest{Argv: argv, Cwd: cwd, Env: env},
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
