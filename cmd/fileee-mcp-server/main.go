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

	"github.com/strausmann/fileee-mcp-server/internal/config"
)

func main() {
	os.Exit(run(os.Args[1:], os.Getenv, os.Stdout, os.Stderr))
}

// run ist der testbare Kern von main: kein os.Exit und kein direkter Aufbau
// des Signal-Kontexts ausserhalb dieser Funktion, damit ein Test einen
// fehlerhaften Start am Rueckgabewert beobachten kann statt am tatsaechlichen
// Prozess-Exit.
func run(args []string, env config.Env, stdout, stderr io.Writer) int {
	if len(args) > 0 && args[0] == "version" {
		// Fprintf statt Fprintln: .golangci.yml nimmt errcheck nur fmt.Fprintf
		// von der Pflicht aus, den Rueckgabewert zu pruefen — ein Schreibfehler
		// auf stdout ist hier ohnehin nicht sinnvoll behandelbar.
		fmt.Fprintf(stdout, "%s\n", config.Version())
		return 0
	}

	cfg, err := config.LoadConfig(env)
	if err != nil {
		fmt.Fprintf(stderr, "fileee-mcp-server: Konfiguration ungueltig: %v\n", err)
		return 1
	}
	reportWarnings(stderr, cfg)

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

	fmt.Fprintf(stdout, "fileee-mcp-server %s hoert auf %s\n", config.Version(), cfg.ListenAddr)
	if err := srv.Run(ctx); err != nil {
		fmt.Fprintf(stderr, "fileee-mcp-server: beendet mit Fehler: %v\n", err)
		return 1
	}
	return 0
}

// reportWarnings schreibt die beim Laden gesammelten Warnungen nach stderr.
//
// cfg == nil wird abgefangen statt vorausgesetzt: LoadConfig liefert bei
// einem Fehler nie ein *Config zurueck (run() prueft err vorher und kehrt bei
// einem Fehler zurueck, bevor diese Funktion ueberhaupt aufgerufen wird), aber
// diese Funktion soll ihre eigene Nachbedingung nicht stillschweigend vom
// Aufrufer voraussetzen — ein kuenftiger Refactor, der die Reihenfolge in
// run() aendert oder eine zweite Aufrufstelle hinzufuegt, darf nicht mit
// einem Nil-Pointer-Absturz bezahlen (siehe TestReportWarningsIsNilSafe).
func reportWarnings(stderr io.Writer, cfg *config.Config) {
	if cfg == nil {
		return
	}
	for _, w := range cfg.Warnings {
		fmt.Fprintf(stderr, "fileee-mcp-server: Warnung: %s\n", w)
	}
}
