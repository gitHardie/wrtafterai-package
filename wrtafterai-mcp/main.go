// wrtafterai-mcp — WrtAfterAI 路由器 MCP Server
// 子命令: serve / token / setup / tunnel / doctor / version
package main

import (
	"fmt"
	"os"

	"github.com/gitHardie/wrtafterai-mcp/internal/mctl"
	"github.com/gitHardie/wrtafterai-mcp/internal/server"
)

const version = "0.1.0"

func usage() {
	fmt.Fprint(os.Stderr, `wrtafterai-mcp — AI router management MCP server

Usage:
  wrtafterai-mcp serve [--stdio]              run MCP server (HTTP by default)
  wrtafterai-mcp token add <name> <level>     level: readonly|write
  wrtafterai-mcp token list
  wrtafterai-mcp token revoke <name>
  wrtafterai-mcp setup                        one-time bootstrap (token+dns+card)
  wrtafterai-mcp tunnel quick                 temporary trycloudflare.com tunnel
  wrtafterai-mcp tunnel set <CF_TOKEN>        persistent named tunnel via cloudflared
  wrtafterai-mcp doctor                       self-check
  wrtafterai-mcp version
`)
}
func main() {
	if len(os.Args) < 2 {
		usage()
		os.Exit(1)
	}
	var err error
	switch os.Args[1] {
	case "serve":
		err = server.Run(os.Args[2:], version)
	case "token":
		err = mctl.TokenCmd(os.Args[2:])
	case "setup":
		err = mctl.SetupCmd(os.Args[2:], version)
	case "tunnel":
		err = mctl.TunnelCmd(os.Args[2:])
	case "doctor":
		err = mctl.DoctorCmd(version)
	case "version":
		fmt.Println("wrtafterai-mcp", version)
	default:
		usage()
		os.Exit(1)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}
