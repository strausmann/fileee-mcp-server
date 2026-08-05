// Command fileee-mcp-server stellt Fileee-Inhalte als MCP-Server (Streamable HTTP)
// fuer AI-Clients bereit und tritt dabei als OAuth-2.1-Resource-Server auf.
//
// Stand: Geruest. Konfiguration, Auth, Konto-Aufloesung und Tools folgen in den
// naechsten Umsetzungsschritten (siehe README).
package main

import (
	"fmt"
	"os"
)

func main() {
	if len(os.Args) > 1 && os.Args[1] == "version" {
		fmt.Println(Version())
		return
	}

	fmt.Fprintf(os.Stderr, "fileee-mcp-server %s — noch kein Server implementiert (Geruest-Stand)\n", Version())
	os.Exit(1)
}
