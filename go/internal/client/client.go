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
	if ack.Error != "" {
		fmt.Fprintf(os.Stderr, "2fado: verdict rejected (%s)\n", ack.Error)
		return 1
	}
	fmt.Printf("{recorded: %v}\n", ack.Recorded)
	return 0
}

// Notify ships link+summary+ttl, prints the petition id, exits 0 on accept.
func Notify(sock, link, summary string, ttlSeconds int64) int {
	raw, err := call(sock, protocol.ClientMessage{
		Notify: &protocol.NotifyRequest{Link: link, Summary: summary, TTLSeconds: ttlSeconds},
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
		fmt.Printf("2fado: notify submitted (id: %s). Use '2fado status %s' to check ack.\n", res.ID, res.ID)
		return 0
	}
	fmt.Fprintf(os.Stderr, "2fado: notify rejected (%s)\n", res.Reason)
	return 1
}

// Ask ships a question + options and blocks until the operator selects one
// or the petition expires. It prints the chosen option to stdout and exits
// 0 on selection, 2 on timeout, 1 on rejection/transport error.
func Ask(sock, question string, options []string, link string, ttlSeconds int64, multiSelect bool, allowWriteIn bool) int {
	raw, err := call(sock, protocol.ClientMessage{
		Ask: &protocol.AskRequest{Question: question, Options: options, Link: link, TTLSeconds: ttlSeconds, MultiSelect: multiSelect, AllowWriteIn: allowWriteIn},
	})
	if err != nil {
		fmt.Fprintln(os.Stderr, "2fado: "+err.Error())
		return 1
	}
	var res protocol.AskResult
	if err := json.Unmarshal(raw, &res); err != nil {
		fmt.Fprintln(os.Stderr, "2fado: bad reply")
		return 1
	}
	if res.Status == "selected" {
		fmt.Println(res.Selection)
		return 0
	}
	if res.Status == "timeout" {
		fmt.Fprintf(os.Stderr, "2fado: ask timed out (id: %s)\n", res.ID)
		return 2
	}
	fmt.Fprintf(os.Stderr, "2fado: ask rejected (%s)\n", res.Reason)
	return 1
}

// Ack submits an acknowledgement for a notify petition id.
func Ack(sock, id string) int {
	raw, err := call(sock, protocol.ClientMessage{
		Ack: &protocol.AckSubmit{ID: id},
	})
	if err != nil {
		fmt.Fprintln(os.Stderr, "2fado: "+err.Error())
		return 1
	}
	var ack protocol.AckResponse
	if err := json.Unmarshal(raw, &ack); err != nil {
		fmt.Fprintln(os.Stderr, "2fado: bad reply")
		return 1
	}
	if ack.Error != "" {
		fmt.Fprintf(os.Stderr, "2fado: ack rejected (%s)\n", ack.Error)
		return 1
	}
	fmt.Printf("{acked: %v}\n", ack.Acked)
	return 0
}

// Cancel submits a cancellation request for a petition id.
func Cancel(sock, id, reason string) int {
	raw, err := call(sock, protocol.ClientMessage{
		Cancel: &protocol.CancelRequest{ID: id, Reason: reason, By: "cli"},
	})
	if err != nil {
		fmt.Fprintln(os.Stderr, "2fado: "+err.Error())
		return 1
	}
	var res protocol.CancelResponse
	if err := json.Unmarshal(raw, &res); err != nil {
		fmt.Fprintln(os.Stderr, "2fado: bad reply")
		return 1
	}
	if res.Error != "" {
		fmt.Fprintf(os.Stderr, "2fado: cancel rejected (%s)\n", res.Error)
		return 1
	}
	fmt.Printf("{cancelled: %v}\n", res.Cancelled)
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
	if res.Status == "not_found" || res.Status == "denied" || res.Status == "timeout" || res.Status == "cancelled" {
		return 1
	}
	return 0
}

// List prints pending records (id, argv, cwd, step/confirm_of, kind, expires_in).
func List(sock string) int {
	raw, err := call(sock, protocol.ClientMessage{
		List: &protocol.ListRequest{},
	})
	if err != nil {
		fmt.Fprintln(os.Stderr, "2fado: "+err.Error())
		return 1
	}
	var res protocol.PendingList
	if err := json.Unmarshal(raw, &res); err != nil {
		fmt.Fprintln(os.Stderr, "2fado: bad reply")
		return 1
	}
	if len(res.Items) == 0 {
		fmt.Println("2fado: no pending requests")
		return 0
	}
	for _, it := range res.Items {
		fmt.Printf("id: %s\n  argv: %v\n  cwd: %s\n  step: %s\n  confirm_of: %s\n  kind: %s\n  expires_in: %ds\n",
			it.ID, it.Argv, it.Cwd, it.Step, it.ConfirmOf, it.Kind, it.ExpiresIn)
	}
	return 0
}

// Recent prints decided records (id, argv, decision, by, exit, step/confirm_of).
func Recent(sock string, limit int) int {
	raw, err := call(sock, protocol.ClientMessage{
		Recent: &protocol.RecentRequest{Limit: limit},
	})
	if err != nil {
		fmt.Fprintln(os.Stderr, "2fado: "+err.Error())
		return 1
	}
	var res protocol.RecentList
	if err := json.Unmarshal(raw, &res); err != nil {
		fmt.Fprintln(os.Stderr, "2fado: bad reply")
		return 1
	}
	if len(res.Items) == 0 {
		fmt.Println("2fado: no recent records")
		return 0
	}
	for _, it := range res.Items {
		fmt.Printf("id: %s\n  argv: %v\n  decision: %s\n  by: %s\n  exit: %d\n  step: %s\n  confirm_of: %s\n",
			it.ID, it.Argv, it.Decision, it.By, it.Exit, it.Step, it.ConfirmOf)
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
