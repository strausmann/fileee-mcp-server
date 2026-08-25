package server

import (
	"context"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/strausmann/fileee-mcp-server/internal/clientpool"
	"github.com/strausmann/fileee-mcp-server/internal/config"
	"github.com/strausmann/fileee-mcp-server/internal/tools"
	"github.com/strausmann/gangway/identity/testidp"
	"github.com/strausmann/go-fileee/fileee"
)

// --- Aufgabe 7, Schritt 1: end-to-end gegen Gangways Test-Aussteller ------
//
// Wie unterscheidet sich das von den End-zu-Ende-Tests aus den Aufgaben 5/6
// (TestNewRegistersReadToolsUsableThroughTheRealWiring und Nachbarn)? Die
// laufen bereits ueber den echten New()-Weg und einen echten *testidp.IDP —
// zwei Eigenschaften, die man von Aufgabe 7 erwarten koennte. Der
// Unterschied liegt in dem, was diese Tests bewusst NICHT tun:
//
//  1. Sie bauen *config.Config aus einer Map im Testprozess
//     (testConfigWithIDP -> envOf(map[string]string{...})) — nie aus den
//     tatsaechlichen Prozess-Umgebungsvariablen, wie main.go es tut
//     (config.LoadConfig(os.Getenv) in cmd/fileee-mcp-server/main.go, run()).
//  2. Sie rufen Server.Run() nie auf. Sie nehmen s.Handler() und reichen
//     ihn an httptest.NewServer — einen EIGENEN, testinternen Listener.
//     Server.Run() (ueber gangway/serve.Server.Run) tut etwas anderes: es
//     baut einen echten *http.Server mit Addr: cfg.ListenAddr und ruft
//     ListenAndServe() darauf auf, in einer eigenen Goroutine, mit
//     Shutdown() bei Kontext-Abbruch. Das ist der Code, der beim
//     tatsaechlichen Deploy laeuft — und keiner der bisherigen Tests
//     bindet je an cfg.ListenAddr oder ruft Server.Run() ueberhaupt auf
//     (TestRunStopsCleanlyWhenContextIsCancelled prueft nur, dass Run()
//     sauber zurueckkehrt — nie mit einem echten Client, der waehrenddessen
//     etwas aufruft).
//
// Dieser Test schliesst genau diese Luecke: Konfiguration aus echten
// Umgebungsvariablen, Server.Run() tatsaechlich gestartet, ein echter
// MCP-Client, der ueber den tatsaechlichen Netzwerk-Weg (net.Dial gegen
// cfg.ListenAddr) anfragt, ein Werkzeugaufruf, und danach ein geordneter
// Shutdown ueber Kontext-Abbruch — bis Run() zurueckkehrt.
//
// Die Anmeldung selbst hat kein Testdoppel: identity/testidp signiert
// echte Tokens, Gangways echter OIDC-Verifier (identity.NewOIDC) prueft sie
// gegen das echte Discovery-Dokument und JWKS des Test-Ausstellers — nur
// der Fileee-Backend-Login (WithPoolOptions -> newFileeeMock) bleibt ein
// Test-Double, weil main.go dafuer keinen produktiven Umschalter kennt und
// dieser Test weder echte Zugangsdaten noch einen echten Netzwerkzugriff
// auf https://my.fileee.com voraussetzen soll — das waere Aufgabe 7
// Schritt 2s Gegenstueck auf der Fileee-Seite, nicht dieser Schritt hier.

// freeLocalAddr reserviert kurz einen freien lokalen Port und gibt ihn
// sofort wieder frei — der ueblichen Vorgehensweise, einen fuer
// Server.Run()s eigenen http.Server.ListenAndServe() konkret benennbaren
// Listen-Zeichenkette zu bekommen. Ein winziges Restrisiko bleibt (der Port
// koennte zwischen Freigabe und Gangways eigenem Bind von etwas anderem
// belegt werden), aber das ist das uebliche, akzeptierte Vorgehen fuer
// Tests, die einen konkreten, im Voraus bekannten Listen-String brauchen.
func freeLocalAddr(t *testing.T) string {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("freeLocalAddr: Listen: %v", err)
	}
	addr := l.Addr().String()
	if err := l.Close(); err != nil {
		t.Fatalf("freeLocalAddr: Close: %v", err)
	}
	return addr
}

// waitForListener wartet, bis addr Verbindungen annimmt — Server.Run()
// startet ListenAndServe() in einer eigenen Goroutine (siehe
// gangway/serve.Server.Run), der erste Verbindungsversuch dieses Tests
// darf also nicht laufen, bevor dieser Bind tatsaechlich passiert ist.
//
// runErrc wird waehrend des Wartens mitgeprueft (Copilot-Befund): kehrt
// Run() vorzeitig zurueck — z. B. "address already in use", falls
// freeLocalAddrs kurze Freigabe-vor-Bind-Luecke ausnahmsweise doch von
// etwas anderem belegt wurde —, soll der Test sofort mit DIESEM Fehler
// abbrechen, statt zuerst die vollen 5s auf einen Verbindungsversuch zu
// warten und dann nur "lauscht nicht" zu melden, ohne die eigentliche
// Ursache zu nennen.
func waitForListener(t *testing.T, addr string, runErrc <-chan error) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		select {
		case err := <-runErrc:
			t.Fatalf("Server.Run() kehrte zurueck, bevor es auf %s zu hoeren begann: %v", addr, err)
		default:
		}
		conn, err := net.DialTimeout("tcp", addr, 100*time.Millisecond)
		if err == nil {
			_ = conn.Close()
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("Server.Run() hoerte innerhalb von 5s nicht auf %s", addr)
}

// TestEndToEndAgainstGangwaysTestIssuer ist Aufgabe 7, Schritt 1: Server
// aus Umgebungsvariablen starten, echt anmelden (Gangways Test-Aussteller,
// kein Testdoppel fuer die Anmeldung selbst), ein Werkzeug aufrufen, geordnet
// beenden — von Anfang bis Ende ueber den tatsaechlichen Netzwerk-Weg.
func TestEndToEndAgainstGangwaysTestIssuer(t *testing.T) {
	idp := testidp.New(t)
	fileeeMock := newFileeeMock(t)
	listenAddr := freeLocalAddr(t)
	resourceURL := "http://" + listenAddr + "/mcp"

	// Echte Prozess-Umgebungsvariablen (t.Setenv setzt/restauriert die
	// tatsaechliche OS-Umgebung des Testprozesses) — nicht die
	// In-Memory-Map, die testConfigWithIDP an anderer Stelle in diesem
	// Paket verwendet. config.LoadConfig(os.Getenv) ist exakt der Weg, den
	// cmd/fileee-mcp-server/main.go's run() nimmt.
	t.Setenv("MCP_AUTH_MODE", "oidc")
	t.Setenv("MCP_OIDC_PROVIDER", "generic")
	t.Setenv("MCP_OIDC_ISSUER", idp.URL())
	t.Setenv("MCP_OIDC_CLIENT_ID", "fileee-mcp-server")
	t.Setenv("MCP_RESOURCE_URL", resourceURL)
	t.Setenv("MCP_ALLOWED_SUBJECTS", "e2e-subject")
	t.Setenv("MCP_LISTEN_ADDR", listenAddr)
	t.Setenv("FILEEE_ALLOWED_ORIGIN_PREFIXES", "127.0.0.1/32,::1/128")
	t.Setenv("FILEEE_USERNAME", "e2e@example.invalid")
	t.Setenv("FILEEE_PASSWORD", "kein-echtes-passwort-e2e")

	cfg, err := config.LoadConfig(os.Getenv)
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// WithPoolOptions bleibt der einzige Test-Only-Eingriff — siehe
	// Abschnitts-Doc-Kommentar oben, warum der Fileee-Backend-Login (anders
	// als die Anmeldung selbst) ein Double bekommt.
	s, err := New(ctx, cfg, WithPoolOptions(
		clientpool.WithClientOptions(fileee.WithBaseURL(fileeeMock), fileee.WithRateLimit(1000, 1000)),
		clientpool.WithSessionStore(func(accountKey string) fileee.SessionStore {
			return fileee.NewFileSessionStore(filepath.Join(t.TempDir(), accountKey+".json"))
		}),
	))
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	runErrc := make(chan error, 1)
	go func() { runErrc <- s.Run(ctx) }()
	waitForListener(t, listenAddr, runErrc)

	// "e2e-subject" ist die in MCP_ALLOWED_SUBJECTS gelistete Kennung (seit
	// der Korrektur aus Aufgabe 5 die einzige, die im Modus single Zugriff
	// bekommt — ein anderes Subject wuerde hier mit "access denied"
	// scheitern, siehe TestNewRefusesASubjectNotOnTheAllowlistDespiteAValidToken).
	token := idp.Token(map[string]any{
		"iss": idp.URL(), "aud": "fileee-mcp-server", "sub": "e2e-subject",
		"exp": time.Now().Add(time.Hour).Unix(),
	})
	client := mcp.NewClient(&mcp.Implementation{Name: "e2e-testidp", Version: "0.0.0"}, nil)
	transport := &mcp.StreamableClientTransport{
		Endpoint:             resourceURL,
		HTTPClient:           &http.Client{Transport: bearerRoundTripper{token: token}},
		DisableStandaloneSSE: true,
	}
	// ctx statt context.Background() (Copilot-Befund): so greift ctx.cancel()
	// (unten fuer den Shutdown-Teil, aber auch bei einem vorzeitigen
	// Testabbruch/Timeout ueber t.Cleanup/der aeusseren -timeout-Deadline)
	// auch fuer Connect/CallTool, statt dass ein haengender Aufruf hier den
	// Testlauf insgesamt blockieren koennte.
	session, err := client.Connect(ctx, transport, nil)
	if err != nil {
		t.Fatalf("Connect ueber den echten Netzwerk-Weg (%s): %v", resourceURL, err)
	}

	res, err := session.CallTool(ctx, &mcp.CallToolParams{Name: tools.ToolListDocuments})
	if err != nil {
		t.Fatalf("CallTool(list_documents): %v", err)
	}
	if res.IsError {
		t.Fatalf("CallTool(list_documents): IsError = true, content = %+v", res.Content)
	}

	// Sitzung VOR dem Abbruch schliessen, nicht per defer ans Testende
	// verschoben: srv.Shutdown() (gangway/serve.Server.Run, ueber
	// net/http.Server.Shutdown) wartet, bis alle aktiven Verbindungen
	// beendet sind. Eine noch offene Keep-Alive-Verbindung dieser Sitzung
	// haette Shutdown() ohne erkennbaren Grund verzoegert — beobachtet als
	// sporadisches Fehlschlagen dieses Tests unter -race/-count (Run()
	// kehrte nicht innerhalb der 5s-Frist zurueck, weil es auf genau diese
	// Verbindung wartete).
	if err := session.Close(); err != nil {
		t.Fatalf("session.Close(): %v", err)
	}

	// Geordneter Shutdown: Kontext abbrechen, Run() muss zurueckkehren
	// (gangway/serve.Server.Run wartet auf srv.Shutdown, siehe dessen
	// Doc-Kommentar) — TestRunStopsCleanlyWhenContextIsCancelled prueft
	// dieselbe Eigenschaft bereits isoliert; hier ist sie Teil des vollen
	// Ablaufs, nach einem echten, erfolgreichen Aufruf.
	//
	// Wartefrist 15s, nicht 5s: eigene Belastungslaeufe (-race -count=50)
	// zeigten Run() fast immer in unter 3ms zurueckkehren, aber vereinzelt
	// (etwa 1 von 20 Laeufen) mit realen ~5-6s — nie ein tatsaechliches
	// Haengenbleiben (err war dabei immer nil), sondern schlicht eine
	// langsamere http.Server.Shutdown()-Runde. gangway/serve.Server.Run
	// selbst raeumt intern bis zu 20s dafuer ein (shutdownCtx, siehe dessen
	// Quelltext) — eine engere Frist hier haette regelmaessig etwas als
	// fehlgeschlagen gemeldet, das die Bibliothek selbst noch als normal
	// einstuft.
	cancel()
	select {
	case err := <-runErrc:
		if err != nil {
			t.Fatalf("Run() = %v, erwartet nil nach geordnetem Shutdown", err)
		}
	case <-time.After(15 * time.Second):
		t.Fatal("Run() kehrte nach Kontext-Abbruch nicht innerhalb von 15s zurueck")
	}
}

// --- Instanz-Beschreibung erreicht den Client ueber initialize ------------
//
// Aufbau identisch zu TestEndToEndAgainstGangwaysTestIssuer oben: dieselbe
// Reihenfolge von idp, fileeeMock, freeLocalAddr, t.Setenv, LoadConfig, New
// und waitForListener. Der einzige Unterschied ist die zusaetzliche
// Umgebungsvariable MCP_INSTANCE_DESCRIPTION und die Pruefung am Ende gegen
// session.InitializeResult().Instructions statt gegen einen Tool-Aufruf.
func TestInstanceDescriptionErreichtDenClientUeberInitialize(t *testing.T) {
	idp := testidp.New(t)
	fileeeMock := newFileeeMock(t)
	listenAddr := freeLocalAddr(t)
	resourceURL := "http://" + listenAddr + "/mcp"

	will := "Testumgebung. Wegwerfdaten, hier darf experimentiert werden. Nicht das produktive Archiv."

	t.Setenv("MCP_AUTH_MODE", "oidc")
	t.Setenv("MCP_OIDC_PROVIDER", "generic")
	t.Setenv("MCP_OIDC_ISSUER", idp.URL())
	t.Setenv("MCP_OIDC_CLIENT_ID", "fileee-mcp-server")
	t.Setenv("MCP_RESOURCE_URL", resourceURL)
	t.Setenv("MCP_ALLOWED_SUBJECTS", "e2e-subject")
	t.Setenv("MCP_LISTEN_ADDR", listenAddr)
	t.Setenv("FILEEE_ALLOWED_ORIGIN_PREFIXES", "127.0.0.1/32,::1/128")
	t.Setenv("FILEEE_USERNAME", "e2e@example.invalid")
	t.Setenv("FILEEE_PASSWORD", "kein-echtes-passwort-e2e")
	t.Setenv("MCP_INSTANCE_DESCRIPTION", will)

	cfg, err := config.LoadConfig(os.Getenv)
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	s, err := New(ctx, cfg, WithPoolOptions(
		clientpool.WithClientOptions(fileee.WithBaseURL(fileeeMock), fileee.WithRateLimit(1000, 1000)),
		clientpool.WithSessionStore(func(accountKey string) fileee.SessionStore {
			return fileee.NewFileSessionStore(filepath.Join(t.TempDir(), accountKey+".json"))
		}),
	))
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	runErrc := make(chan error, 1)
	go func() { runErrc <- s.Run(ctx) }()
	waitForListener(t, listenAddr, runErrc)

	token := idp.Token(map[string]any{
		"iss": idp.URL(), "aud": "fileee-mcp-server", "sub": "e2e-subject",
		"exp": time.Now().Add(time.Hour).Unix(),
	})
	client := mcp.NewClient(&mcp.Implementation{Name: "e2e-testidp", Version: "0.0.0"}, nil)
	transport := &mcp.StreamableClientTransport{
		Endpoint:             resourceURL,
		HTTPClient:           &http.Client{Transport: bearerRoundTripper{token: token}},
		DisableStandaloneSSE: true,
	}
	session, err := client.Connect(ctx, transport, nil)
	if err != nil {
		t.Fatalf("Connect ueber den echten Netzwerk-Weg (%s): %v", resourceURL, err)
	}

	// Der Vergleich prueft den Inhalt, nicht die Existenz. Ein "if got == """
	// bliebe grün, sobald irgendein Text ankommt — auch ein falscher.
	got := session.InitializeResult().Instructions
	if got != will {
		t.Errorf("Instructions = %q, erwartet %q", got, will)
	}

	if err := session.Close(); err != nil {
		t.Fatalf("session.Close(): %v", err)
	}

	cancel()
	select {
	case err := <-runErrc:
		if err != nil {
			t.Fatalf("Run() = %v, erwartet nil nach geordnetem Shutdown", err)
		}
	case <-time.After(15 * time.Second):
		t.Fatal("Run() kehrte nach Kontext-Abbruch nicht innerhalb von 15s zurueck")
	}
}
