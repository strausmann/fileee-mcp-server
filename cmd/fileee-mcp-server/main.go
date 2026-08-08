// Command fileee-mcp-server stellt Fileee-Inhalte als MCP-Server (Streamable HTTP)
// fuer AI-Clients bereit und tritt dabei als OAuth-2.1-Resource-Server auf.
//
// Stand: Server startet und weist unauthentifizierte Anfragen ab (Gangway,
// ADR-0015). Konto-Aufloesung und Tools folgen in den naechsten
// Umsetzungsschritten (siehe README).
package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/signal"
	"syscall"
)

func main() {
	os.Exit(run(os.Args[1:], os.Getenv, os.Stdout, os.Stderr))
}

// run ist der testbare Kern von main: kein os.Exit und kein direkter Aufbau
// des Signal-Kontexts ausserhalb dieser Funktion, damit ein Test einen
// fehlerhaften Start am Rueckgabewert beobachten kann statt am tatsaechlichen
// Prozess-Exit.
func run(args []string, env Env, stdout, stderr io.Writer) int {
	if len(args) > 0 && args[0] == "version" {
		fmt.Fprintln(stdout, Version())
		return 0
	}

	cfg, err := LoadConfig(env)
	if err != nil {
		fmt.Fprintf(stderr, "fileee-mcp-server: Konfiguration ungueltig: %v\n", err)
		return 1
	}
	for _, w := range cfg.Warnings {
		fmt.Fprintf(stderr, "fileee-mcp-server: Warnung: %s\n", w)
	}

	// signal.NotifyContext liefert den Kontext, der sowohl New (Lebensdauer
	// des OIDC-Key-Refresh, siehe serve.New) als auch Run (geordneter
	// Shutdown) uebergeben wird — ein SIGTERM/SIGINT muss beides beenden,
	// nicht nur die laufende HTTP-Bedienung.
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	srv, err := New(ctx, cfg)
	if err != nil {
		fmt.Fprintf(stderr, "fileee-mcp-server: Start fehlgeschlagen: %v\n", err)
		return 1
	}

	fmt.Fprintf(stdout, "fileee-mcp-server %s hoert auf %s\n", Version(), cfg.ListenAddr)
	if err := srv.Run(ctx); err != nil {
		fmt.Fprintf(stderr, "fileee-mcp-server: beendet mit Fehler: %v\n", err)
		return 1
	}
	return 0
}
