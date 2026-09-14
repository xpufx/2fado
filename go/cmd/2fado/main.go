// Command 2fado is the single binary: client and daemon as subcommands.
//
//	2fado daemon            run the privileged service
//	2fado run -- <argv...>  petition to execute
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
	"time"

	"2fado/internal/client"
	"2fado/internal/config"
	"2fado/internal/daemon"
	"2fado/internal/service"
)

var (
	Version   = "0.1.0-dev"
	GitCommit = "unknown"
	BuildTime = "unknown"
)

func usage() int {
	fmt.Println("usage: 2fado daemon | 2fado run -- <argv...> | 2fado approve|deny <id> | 2fado notify --link <url> --summary <text> [--ttl <dur>] | 2fado ack <id> | 2fado status <id> | 2fado version")
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

func main() {
	if len(os.Args) < 2 {
		os.Exit(usage())
	}
	sock := config.GetEnvWithFallback("TWOFADO_SOCKET", "FADO_SOCKET")
	if sock == "" {
		sock = "/tmp/2fado.sock"
	}
	switch os.Args[1] {
	case "daemon":
		daemon.SetBuildInfo(Version, GitCommit, BuildTime)
		confPath := config.GetEnvWithFallback("TWOFADO_CONF", "FADO_CONF", "2FADO_CONF")
		if confPath == "" {
			confPath = "/etc/2fado/2fado.conf"
		}
		if err := daemon.Serve(service.New(config.Load(confPath))); err != nil {
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
	case "notify":
		os.Exit(notifyCmd(sock, os.Args[2:]))
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
	case "version", "-v", "--version":
		os.Exit(printVersion())
	case "socket-version":
		os.Exit(client.SocketVersion(sock))
	default:
		os.Exit(usage())
	}
}
