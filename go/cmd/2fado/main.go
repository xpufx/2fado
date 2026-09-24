// Command 2fado is the single binary: client and daemon as subcommands.
//
//	2fado daemon            run the privileged service
//	2fado run -- <argv...>  petition to execute
//	2fado ask --question <q> --option <o> --option <o>  interactive choice
//	2fado approve|deny <id> local verdict (PoC; production: SSO-bound UI)
//	2fado status <id>       inspect state and execution result
//	2fado version           print binary version and checksum
package main

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"
	"time"

	"2fado/internal/client"
	"2fado/internal/config"
	"2fado/internal/daemon"
	"2fado/internal/protocol"
	"2fado/internal/service"
)

var (
	Version   = "0.1.0-dev"
	GitCommit = "unknown"
	BuildTime = "unknown"
)

func usage() int {
	fmt.Println("usage: 2fado daemon | 2fado run -- <argv...> | 2fado approve|deny <id> | 2fado select <id> [--index N | --choice text] | 2fado cancel <id> [--reason <text>] | 2fado notify --link <url> --summary <text> [--ttl <dur>] | 2fado ask --question <q> --option <o> --option <o> [--link <url>] [--ttl <dur>] | 2fado ack <id> | 2fado status <id> | 2fado list | 2fado recent [--limit N] | 2fado audit [--limit N] [--cursor C] [--ev E] [--id RID] [--uid U] [--since UNIX] [--until UNIX] | 2fado version")
	return 2
}

func binarySHA256() string {
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

func printVersion() int {
	fmt.Printf("version: %s\ncommit: %s\nbuild_time: %s\nsha256: %s\n", Version, GitCommit, BuildTime, binarySHA256())
	return 0
}

// notifyCmd parses: 2fado notify --link <url> --summary <text> [--ttl <dur>].
func notifyCmd(sock string, args []string) int {
	var link, summary, ttlStr string
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--link":
			if i+1 >= len(args) {
				return usage()
			}
			i++
			link = args[i]
		case "--summary":
			if i+1 >= len(args) {
				return usage()
			}
			i++
			summary = args[i]
		case "--ttl":
			if i+1 >= len(args) {
				return usage()
			}
			i++
			ttlStr = args[i]
		default:
			return usage()
		}
	}
	if link == "" || summary == "" {
		return usage()
	}
	var ttlSeconds int64
	if ttlStr != "" {
		d, err := time.ParseDuration(ttlStr)
		if err != nil || d <= 0 {
			fmt.Fprintln(os.Stderr, "2fado: bad --ttl (try 30m, 2h, 24h)")
			return 2
		}
		ttlSeconds = int64(d / time.Second)
	}
	return client.Notify(sock, link, summary, ttlSeconds)
}

// askCmd parses: 2fado ask --question <q> --option <o> [--option <o>...]
// [--link <url>] [--ttl <dur>]. Blocks until the operator selects or TTL.
func askCmd(sock string, args []string) int {
	var question, link, ttlStr string
	var options []string
	var multiSelect, allowWriteIn bool
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--question":
			if i+1 >= len(args) {
				return usage()
			}
			i++
			question = args[i]
		case "--option":
			if i+1 >= len(args) {
				return usage()
			}
			i++
			options = append(options, args[i])
		case "--link":
			if i+1 >= len(args) {
				return usage()
			}
			i++
			link = args[i]
		case "--ttl":
			if i+1 >= len(args) {
				return usage()
			}
			i++
			ttlStr = args[i]
		case "--multi-select":
			multiSelect = true
		case "--allow-write-in":
			allowWriteIn = true
		default:
			return usage()
		}
	}
	if question == "" || len(options) < 2 {
		return usage()
	}
	var ttlSeconds int64
	if ttlStr != "" {
		d, err := time.ParseDuration(ttlStr)
		if err != nil || d <= 0 {
			fmt.Fprintln(os.Stderr, "2fado: bad --ttl (try 30s, 5m, 1h)")
			return 2
		}
		ttlSeconds = int64(d / time.Second)
	}
	return client.Ask(sock, question, options, link, ttlSeconds, multiSelect, allowWriteIn)
}

// selectCmd parses: 2fado select <id> [--index N | --choice text].
func selectCmd(sock string, args []string) int {
	if len(args) == 0 {
		return usage()
	}
	var id, choice string
	selectionIdx := -1
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--index", "-i":
			if i+1 >= len(args) {
				return usage()
			}
			i++
			n, err := strconv.Atoi(args[i])
			if err != nil || n < 0 {
				fmt.Fprintln(os.Stderr, "2fado: bad --index (want non-negative int)")
				return 2
			}
			selectionIdx = n
		case "--choice", "-c":
			if i+1 >= len(args) {
				return usage()
			}
			i++
			choice = args[i]
		default:
			if strings.HasPrefix(args[i], "-") {
				return usage()
			}
			if id == "" {
				id = args[i]
			} else if choice == "" {
				choice = args[i]
			} else {
				return usage()
			}
		}
	}
	if id == "" {
		return usage()
	}
	// If only index or only choice is specified, resolve the counterpart from daemon status
	if choice == "" && selectionIdx >= 0 {
		st, err := client.QueryStatus(sock, id)
		if err == nil && len(st.Options) > 0 {
			if selectionIdx < len(st.Options) {
				choice = st.Options[selectionIdx]
			} else {
				fmt.Fprintf(os.Stderr, "2fado: index %d out of range (options: %d)\n", selectionIdx, len(st.Options))
				return 1
			}
		}
	} else if choice != "" && selectionIdx < 0 {
		st, err := client.QueryStatus(sock, id)
		if err == nil && len(st.Options) > 0 {
			for idx, opt := range st.Options {
				if opt == choice {
					selectionIdx = idx
					break
				}
			}
		}
		if selectionIdx < 0 {
			selectionIdx = 0
		}
	}
	if choice == "" {
		fmt.Fprintln(os.Stderr, "2fado: must specify --choice <text> or --index <N>")
		return usage()
	}
	if selectionIdx < 0 {
		selectionIdx = 0
	}
	return client.Select(sock, id, choice, selectionIdx)
}

// cancelCmd parses: 2fado cancel <id> [--reason <text>].
func cancelCmd(sock string, args []string) int {
	if len(args) == 0 {
		return usage()
	}
	id := ""
	reason := ""
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--reason":
			if i+1 >= len(args) {
				return usage()
			}
			i++
			reason = args[i]
		default:
			if strings.HasPrefix(args[i], "-") {
				return usage()
			}
			if id != "" {
				return usage()
			}
			id = args[i]
		}
	}
	if id == "" {
		return usage()
	}
	return client.Cancel(sock, id, reason)
}

// auditCmd parses: 2fado audit [--limit N] [--cursor C] [--ev E] [--id RID]
// [--uid U] [--since UNIX] [--until UNIX]. Prints one page of audit events.
func auditCmd(sock string, args []string) int {
	var req protocol.AuditQueryRequest
	for i := 0; i < len(args); i++ {
		needValue := func() (string, bool) {
			if i+1 >= len(args) {
				return "", false
			}
			i++
			return args[i], true
		}
		switch args[i] {
		case "--limit":
			v, ok := needValue()
			if !ok {
				return usage()
			}
			n, err := strconv.Atoi(v)
			if err != nil || n < 0 {
				fmt.Fprintln(os.Stderr, "2fado: bad --limit (want non-negative int)")
				return 2
			}
			req.Limit = n
		case "--cursor":
			v, ok := needValue()
			if !ok {
				return usage()
			}
			req.Cursor = v
		case "--ev":
			v, ok := needValue()
			if !ok {
				return usage()
			}
			req.Ev = v
		case "--id":
			v, ok := needValue()
			if !ok {
				return usage()
			}
			req.ID = v
		case "--uid":
			v, ok := needValue()
			if !ok {
				return usage()
			}
			n, err := strconv.ParseUint(v, 10, 32)
			if err != nil {
				fmt.Fprintln(os.Stderr, "2fado: bad --uid (want uint32)")
				return 2
			}
			req.UID = uint32(n)
		case "--since":
			v, ok := needValue()
			if !ok {
				return usage()
			}
			n, err := strconv.ParseInt(v, 10, 64)
			if err != nil || n < 0 {
				fmt.Fprintln(os.Stderr, "2fado: bad --since (want unix seconds)")
				return 2
			}
			req.Since = n
		case "--until":
			v, ok := needValue()
			if !ok {
				return usage()
			}
			n, err := strconv.ParseInt(v, 10, 64)
			if err != nil || n < 0 {
				fmt.Fprintln(os.Stderr, "2fado: bad --until (want unix seconds)")
				return 2
			}
			req.Until = n
		default:
			return usage()
		}
	}
	return client.Audit(sock, req)
}

func main() {
	if len(os.Args) < 2 {
		os.Exit(usage())
	}
	sock := config.DefaultSocketPath()
	switch os.Args[1] {
	case "daemon":
		daemon.SetBuildInfo(Version, GitCommit, BuildTime)
		confPath := config.GetEnvWithFallback("TWOFADO_CONF", "FADO_CONF", "2FADO_CONF")
		if confPath == "" {
			confPath = "/etc/2fado/2fado.conf"
		}
		conf := config.Load(confPath)
		// #54 Path A: --allow-root is release-hidden and not honored.
		// Accepted-but-ignored args are deliberately NOT parsed so no
		// runtime surface can enable escalation.
		if err := daemon.Serve(service.New(conf)); err != nil {
			fmt.Fprintln(os.Stderr, "2fadod: "+err.Error())
			os.Exit(1)
		}
	case "run":
		args := os.Args[2:]
		detach := false
		var filtered []string
		for _, a := range args {
			switch a {
			case "--detach", "-d", "--async":
				detach = true
			default:
				filtered = append(filtered, a)
			}
		}
		args = filtered
		if len(args) > 0 && args[0] == "--" {
			args = args[1:]
		}
		if len(args) == 0 {
			os.Exit(usage())
		}
		os.Exit(client.RunWithDetach(sock, args, detach))
	case "approve", "deny":
		if len(os.Args) != 3 {
			os.Exit(usage())
		}
		os.Exit(client.Verdict(sock, os.Args[1], os.Args[2]))
	case "select":
		os.Exit(selectCmd(sock, os.Args[2:]))
	case "cancel":
		os.Exit(cancelCmd(sock, os.Args[2:]))
	case "notify":
		os.Exit(notifyCmd(sock, os.Args[2:]))
	case "ask":
		os.Exit(askCmd(sock, os.Args[2:]))
	case "ack":
		if len(os.Args) != 3 {
			os.Exit(usage())
		}
		os.Exit(client.Ack(sock, os.Args[2]))
	case "status":
		if len(os.Args) != 3 {
			os.Exit(usage())
		}
		os.Exit(client.Status(sock, os.Args[2]))
	case "list":
		if len(os.Args) != 2 {
			os.Exit(usage())
		}
		os.Exit(client.List(sock))
	case "recent":
		limit := 0
		args := os.Args[2:]
		for i := 0; i < len(args); i++ {
			if args[i] != "--limit" || i+1 >= len(args) {
				os.Exit(usage())
			}
			n, err := strconv.Atoi(args[i+1])
			if err != nil || n < 0 {
				fmt.Fprintln(os.Stderr, "2fado: bad --limit (want non-negative int)")
				os.Exit(2)
			}
			limit = n
			i++
		}
		os.Exit(client.Recent(sock, limit))
	case "audit":
		os.Exit(auditCmd(sock, os.Args[2:]))
	case "version", "-v", "--version":
		os.Exit(printVersion())
	case "socket-version":
		os.Exit(client.SocketVersion(sock))
	default:
		os.Exit(usage())
	}
}
