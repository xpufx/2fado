// Command 2fado is the single binary: client and daemon as subcommands.
//
//	2fado daemon            run the privileged service
//	2fado run -- <argv...>  petition to execute
//	2fado approve|deny <id> local verdict (PoC; production: SSO-bound UI)
//	2fado status <id>       inspect state and execution result
package main

import (
	"fmt"
	"os"

	"2fado/internal/client"
	"2fado/internal/config"
	"2fado/internal/daemon"
	"2fado/internal/service"
)

func usage() int {
	fmt.Println("usage: 2fado daemon | 2fado run -- <argv...> | 2fado approve|deny <id> | 2fado status <id>")
	return 2
}

func main() {
	if len(os.Args) < 2 {
		os.Exit(usage())
	}
	sock := os.Getenv("FADO_SOCKET")
	if sock == "" {
		sock = "/tmp/2fado.sock"
	}
	switch os.Args[1] {
	case "daemon":
		confPath := os.Getenv("FADO_CONF")
		if confPath == "" {
			confPath = "/etc/2fado/2fado.conf"
		}
		if err := daemon.Serve(service.New(config.Load(confPath))); err != nil {
			fmt.Fprintln(os.Stderr, "2fadod: "+err.Error())
			os.Exit(1)
		}
	case "run":
		args := os.Args[2:]
		if len(args) > 0 && args[0] == "--" {
			args = args[1:]
		}
		if len(args) == 0 {
			os.Exit(usage())
		}
		os.Exit(client.Run(sock, args))
	case "approve", "deny":
		if len(os.Args) != 3 {
			os.Exit(usage())
		}
		os.Exit(client.Verdict(sock, os.Args[1], os.Args[2]))
	case "status":
		if len(os.Args) != 3 {
			os.Exit(usage())
		}
		os.Exit(client.Status(sock, os.Args[2]))
	default:
		os.Exit(usage())
	}
}
