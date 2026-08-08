package server

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/strausmann/fileee-mcp-server/internal/accounts"
	"github.com/strausmann/fileee-mcp-server/internal/clientpool"
	"github.com/strausmann/fileee-mcp-server/internal/config"
	"github.com/strausmann/fileee-mcp-server/internal/tools"
	"github.com/strausmann/gangway/identity"
	"github.com/strausmann/gangway/identity/testidp"
	"github.com/strausmann/go-fileee/fileee"
)

// envOf baut ein config.Env aus einer Map — dieselbe kleine Hilfsfunktion wie
// in internal/config/config_test.go, hier lokal dupliziert statt importiert:
// Test-Helfer eines fremden Pakets sind ausserhalb dieses Pakets nicht sichtbar,
// und ein eigenes Test-Hilfspaket waere fuer eine Zeile Logik unverhaeltnismaessig.
func envOf(m map[string]string) config.Env {
	return func(key string) string { return m[key] }
}

// mustPrefix parst eine feste CIDR-Liste fuer Testfaelle, in denen ein
// Parse-Fehler ein Programmierfehler im Test selbst waere, kein zu
// pruefendes Verhalten.
func mustPrefix(t *testing.T, cidrs ...string) []netip.Prefix {
	t.Helper()
	out := make([]netip.Prefix, 0, len(cidrs))
	for _, c := range cidrs {
		p, err := netip.ParsePrefix(c)
		if err != nil {
			t.Fatalf("mustPrefix(%q): %v", c, err)
		}
		out = append(out, p)
	}
	return out
}

// testConfig baut die kleinstmoegliche gueltige Einstellung mit einem lokalen
// Test-Aussteller (kein Netzwerkzugriff, siehe testidp.New) — ein echtes
// LoadConfig statt eines von Hand gebauten *config.Config, weil sonst die
// unexportierte subjectIndex-Aufloesung uebergangen wuerde und der Test etwas
// pruefte, das mit dem echten Startpfad nicht mehr uebereinstimmt.
//
// Baut auf testConfigWithIDP auf: die meisten Tests hier brauchen den
// *testidp.IDP selbst nie (sie pruefen nie einen authentifizierten Aufruf),
// aber ein paar — allen voran
// TestNewRegistersReadToolsUsableThroughTheRealWiring — muessen selbst ein
// Token gegen genau diesen Aussteller ausstellen.
func testConfig(t *testing.T) *config.Config {
	t.Helper()
	cfg, _ := testConfigWithIDP(t)
	return cfg
}

// testConfigWithIDP ist testConfig, gibt aber zusaetzlich den
// *testidp.IDP zurueck, gegen den MCP_OIDC_ISSUER zeigt — fuer Tests, die
// selbst ein Token ausstellen muessen (testConfig allein verwirft den
// Aussteller nach dem Bau der Config).
func testConfigWithIDP(t *testing.T) (*config.Config, *testidp.IDP) {
	t.Helper()
	idp := testidp.New(t)

	env := map[string]string{
		"MCP_AUTH_MODE":                  "oidc",
		"MCP_OIDC_ISSUER":                idp.URL(),
		"MCP_OIDC_AUDIENCE":              "fileee-mcp-server",
		"MCP_RESOURCE_URL":               "https://mcp.example.com/mcp",
		"MCP_ALLOWED_SUBJECTS":           "abc123",
		"FILEEE_ALLOWED_ORIGIN_PREFIXES": "0.0.0.0/0, ::/0",
		"FILEEE_USERNAME":                "nutzer@example.com",
		"FILEEE_PASSWORD":                "geheim",
	}

	cfg, err := config.LoadConfig(envOf(env))
	if err != nil {
		t.Fatalf("testConfigWithIDP: LoadConfig = %v", err)
	}
	return cfg, idp
}

// --- end-to-end: New()'s OWN wiring, not a parallel reimplementation ----
//
// Every test above (and every test in internal/tools/read_test.go) builds
// the tool-serving stack it exercises by hand: internal/tools/read_test.go
// has its own newGangwayServer, which registers RegisterRead and
// serve.WithToolKinds itself. That proves the TOOLS behave correctly given
// correct wiring — it says nothing about whether New() actually wires them
// that way. A review of this task's PR found exactly that gap by removing
// tools.RegisterRead(mcpServer, pool) from New(): the entire test suite,
// including every test in this file, stayed green, because nothing here
// ever called a tool through a *Server built by New() itself.
//
// The tests below close that gap: they build the server via New(), the
// only production entry point, and drive it exactly like a real client
// would — mint a token against the config's own issuer, call a tool over
// the streamable HTTP transport, and check the outcome. Remove
// tools.RegisterRead or serve.WithToolKinds(tools.ReadToolKinds()) from
// New() now, and TestNewRegistersReadToolsUsableThroughTheRealWiring fails.

// newFileeeMock starts a minimal stand-in for my.fileee.com: the login
// handshake always succeeds and POST /api/documents/rest/query always
// returns an empty result. It exists only so
// TestNewRegistersReadToolsUsableThroughTheRealWiring can point New()'s
// account pool at something other than the real, external
// https://my.fileee.com — see WithPoolOptions.
func newFileeeMock(t *testing.T) string {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/f/start", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	})
	mux.HandleFunc("POST /api/f/existent", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"existent":true,"twoFactorAuthEnabled":false}`))
	})
	mux.HandleFunc("POST /api/f/login", func(w http.ResponseWriter, _ *http.Request) {
		http.SetCookie(w, &http.Cookie{Name: "JSESSIONID", Value: "sess"})
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"loggedIn":true}`))
	})
	mux.HandleFunc("GET /api/f/user-session", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"authorized":true,"secondsBlocked":0}`))
	})
	mux.HandleFunc("POST /api/documents/rest/query", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"rows":[],"totalRows":0}`))
	})

	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv.URL
}

// bearerRoundTripper injects a fixed bearer token into every outgoing
// request — the same pattern gangway's own tests use
// (serve/serve_test.go, bearerRoundTripper) and internal/tools/read_test.go
// duplicates for the same reason: a test-only http.RoundTripper is small
// enough that sharing it across packages isn't worth an import.
type bearerRoundTripper struct{ token string }

func (t bearerRoundTripper) RoundTrip(r *http.Request) (*http.Response, error) {
	r = r.Clone(r.Context())
	r.Header.Set("Authorization", "Bearer "+t.token)
	return http.DefaultTransport.RoundTrip(r)
}

// TestNewRegistersReadToolsUsableThroughTheRealWiring is the review's
// central finding, turned into a guard — see the section doc comment
// above. It builds *Server via New() (WithPoolOptions only redirects the
// account pool to newFileeeMock; every production call to New() passes no
// options at all), serves it, authenticates as the one subject testConfig
// allows, and calls list_documents for real.
func TestNewRegistersReadToolsUsableThroughTheRealWiring(t *testing.T) {
	fileeeMock := newFileeeMock(t)
	cfg, idp := testConfigWithIDP(t)

	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)

	s, err := New(ctx, cfg, WithPoolOptions(
		clientpool.WithClientOptions(fileee.WithBaseURL(fileeeMock), fileee.WithRateLimit(1000, 1000)),
		// cfg.SessionDir defaults to /home/nonroot/sessions (the container's
		// path, see config.go) — unwritable here, so this test needs its own
		// directory the same way internal/tools/read_test.go's testPool does.
		clientpool.WithSessionStore(func(accountKey string) fileee.SessionStore {
			return fileee.NewFileSessionStore(filepath.Join(t.TempDir(), accountKey+".json"))
		}),
	))
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	httpSrv := httptest.NewServer(s.Handler())
	t.Cleanup(httpSrv.Close)

	// "abc123" is testConfig's MCP_ALLOWED_SUBJECTS entry.
	token := idp.Token(map[string]any{
		"iss": idp.URL(), "aud": "fileee-mcp-server", "sub": "abc123",
		"exp": time.Now().Add(time.Hour).Unix(),
	})
	client := mcp.NewClient(&mcp.Implementation{Name: "server-wiring-test", Version: "0.0.0"}, nil)
	transport := &mcp.StreamableClientTransport{
		Endpoint:             httpSrv.URL + "/mcp",
		HTTPClient:           &http.Client{Transport: bearerRoundTripper{token: token}},
		DisableStandaloneSSE: true,
	}
	session, err := client.Connect(ctx, transport, nil)
	if err != nil {
		t.Fatalf("Connect: %v", err)
	}
	t.Cleanup(func() { _ = session.Close() })

	res, err := session.CallTool(ctx, &mcp.CallToolParams{Name: tools.ToolListDocuments})
	if err != nil {
		t.Fatalf("CallTool(list_documents) through New()'s own wiring: %v — this is exactly what silently "+
			"broke when tools.RegisterRead or serve.WithToolKinds(tools.ReadToolKinds()) was removed from "+
			"New(), and every other test in this package stayed green", err)
	}
	if res.IsError {
		t.Fatalf("CallTool(list_documents): IsError = true, content = %+v", res.Content)
	}
}

// TestNewRegistersSearchDocumentsToo is the same guard for the second
// tool, kept separate rather than folded into the test above: the review
// finding was that NO tool was reachable through New()'s real wiring, not
// specifically list_documents, and a shared registration bug that
// happened to spare one tool but not the other would otherwise go
// unnoticed.
func TestNewRegistersSearchDocumentsToo(t *testing.T) {
	fileeeMock := newFileeeMock(t)
	cfg, idp := testConfigWithIDP(t)

	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)

	s, err := New(ctx, cfg, WithPoolOptions(
		clientpool.WithClientOptions(fileee.WithBaseURL(fileeeMock), fileee.WithRateLimit(1000, 1000)),
		// cfg.SessionDir defaults to /home/nonroot/sessions (the container's
		// path, see config.go) — unwritable here, so this test needs its own
		// directory the same way internal/tools/read_test.go's testPool does.
		clientpool.WithSessionStore(func(accountKey string) fileee.SessionStore {
			return fileee.NewFileSessionStore(filepath.Join(t.TempDir(), accountKey+".json"))
		}),
	))
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	httpSrv := httptest.NewServer(s.Handler())
	t.Cleanup(httpSrv.Close)

	token := idp.Token(map[string]any{
		"iss": idp.URL(), "aud": "fileee-mcp-server", "sub": "abc123",
		"exp": time.Now().Add(time.Hour).Unix(),
	})
	client := mcp.NewClient(&mcp.Implementation{Name: "server-wiring-test", Version: "0.0.0"}, nil)
	transport := &mcp.StreamableClientTransport{
		Endpoint:             httpSrv.URL + "/mcp",
		HTTPClient:           &http.Client{Transport: bearerRoundTripper{token: token}},
		DisableStandaloneSSE: true,
	}
	session, err := client.Connect(ctx, transport, nil)
	if err != nil {
		t.Fatalf("Connect: %v", err)
	}
	t.Cleanup(func() { _ = session.Close() })

	res, err := session.CallTool(ctx, &mcp.CallToolParams{
		Name: tools.ToolSearchDocuments, Arguments: map[string]any{"term": "irrelevant"},
	})
	if err != nil {
		t.Fatalf("CallTool(search_documents) through New()'s own wiring: %v", err)
	}
	if res.IsError {
		t.Fatalf("CallTool(search_documents): IsError = true, content = %+v", res.Content)
	}
}

// TestNewRefusesASubjectNotOnTheAllowlistDespiteAValidToken is the
// end-to-end proof of the buildResolver fix above (Pruefbefund: a caller
// whose subject is not in MCP_ALLOWED_SUBJECTS previously got full access
// in single mode anyway, because accounts.NewSingle never looked at the
// subject at all). "not-on-the-allowlist" passes gangway's own checks
// completely — a validly signed token, from an allowed origin, for the
// configured issuer/audience — the only thing wrong with it is that this
// subject was never added to MCP_ALLOWED_SUBJECTS (testConfig only lists
// "abc123"). It must reach the tool (an ordinary, allowed call at the
// gangway/tool-authorization layer — this is not what ReadToolKinds
// guards) and then be refused there, by clientFor/accounts.ErrNoAccount —
// the same "access denied" tool-level result an unmapped multi-mode
// subject gets (TestUnknownCallerGetsAToolErrorNotAServerError, in
// internal/tools/read_test.go), not a protocol-level failure.
func TestNewRefusesASubjectNotOnTheAllowlistDespiteAValidToken(t *testing.T) {
	fileeeMock := newFileeeMock(t)
	cfg, idp := testConfigWithIDP(t)

	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)

	s, err := New(ctx, cfg, WithPoolOptions(
		clientpool.WithClientOptions(fileee.WithBaseURL(fileeeMock), fileee.WithRateLimit(1000, 1000)),
		clientpool.WithSessionStore(func(accountKey string) fileee.SessionStore {
			return fileee.NewFileSessionStore(filepath.Join(t.TempDir(), accountKey+".json"))
		}),
	))
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	httpSrv := httptest.NewServer(s.Handler())
	t.Cleanup(httpSrv.Close)

	// A validly signed token for the configured issuer/audience — but for
	// a subject that is NOT in testConfig's MCP_ALLOWED_SUBJECTS ("abc123").
	token := idp.Token(map[string]any{
		"iss": idp.URL(), "aud": "fileee-mcp-server", "sub": "not-on-the-allowlist",
		"exp": time.Now().Add(time.Hour).Unix(),
	})
	client := mcp.NewClient(&mcp.Implementation{Name: "server-wiring-test", Version: "0.0.0"}, nil)
	transport := &mcp.StreamableClientTransport{
		Endpoint:             httpSrv.URL + "/mcp",
		HTTPClient:           &http.Client{Transport: bearerRoundTripper{token: token}},
		DisableStandaloneSSE: true,
	}
	session, err := client.Connect(ctx, transport, nil)
	if err != nil {
		t.Fatalf("Connect: %v", err)
	}
	t.Cleanup(func() { _ = session.Close() })

	res, err := session.CallTool(ctx, &mcp.CallToolParams{Name: tools.ToolListDocuments})
	if err != nil {
		t.Fatalf("CallTool returned a transport/protocol-level error (%v) — a caller with a valid token but "+
			"an unlisted subject must get an ordinary tool result, not something that looks like this "+
			"server broke", err)
	}
	if !res.IsError {
		t.Fatal("res.IsError = false — a valid token for a subject NOT in MCP_ALLOWED_SUBJECTS got access to " +
			"the single configured Fileee account anyway (the exact bug this test guards against)")
	}
}

func TestUnauthenticatedRequestIsRefusedWithAChallenge(t *testing.T) {
	cfg := testConfig(t)

	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)

	s, err := New(ctx, cfg)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	r := httptest.NewRequest(http.MethodPost, "/mcp", strings.NewReader(`{}`))
	r.RemoteAddr = "203.0.113.10:5000"
	w := httptest.NewRecorder()
	s.Handler().ServeHTTP(w, r)

	if w.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want %d", w.Code, http.StatusUnauthorized)
	}
	// Ohne diesen Pointer kann ein Connector nicht herausfinden, wo er sich
	// anmelden muss.
	if c := w.Header().Get("WWW-Authenticate"); !strings.Contains(c, "resource_metadata") {
		t.Errorf("WWW-Authenticate = %q, want a resource_metadata pointer", c)
	}
}

// Gegenversuch (Pflicht laut Auftrag): merkt ein Test, wenn AttachMCP
// vergessen wird? Ohne AttachMCP registriert Gangways Handler() die Route
// /mcp gar nicht erst (s.mcp bleibt nil, siehe serve.go) — der Request liefe
// dann in ein 404 vom inneren ServeMux, nicht in die 401-Challenge der
// Authentifizierung. Der obige Test wuerde also *auch* eine kaputte
// Implementierung bemerken, die AttachMCP ausslaesst: Status waere 404 statt
// 401, der Fatalf-Abbruch griffe. Siehe Bericht fuer die tatsaechliche Probe.
func TestUnauthenticatedRequestReachesTheChallengeNotA404(t *testing.T) {
	cfg := testConfig(t)

	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)

	s, err := New(ctx, cfg)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	r := httptest.NewRequest(http.MethodPost, "/mcp", strings.NewReader(`{}`))
	r.RemoteAddr = "203.0.113.10:5000"
	w := httptest.NewRecorder()
	s.Handler().ServeHTTP(w, r)

	if w.Code == http.StatusNotFound {
		t.Fatalf("status = 404 — /mcp ist nicht geroutet; AttachMCP wurde vermutlich nicht aufgerufen")
	}
}

// Der Origin-Gate laeuft VOR der Authentifizierung (siehe Gangways
// Handler()-Doku) — eine Adresse ausserhalb der Freigabeliste darf nicht bis
// zur 401-Challenge durchkommen.
func TestRequestFromDisallowedOriginIsRejectedBeforeAuth(t *testing.T) {
	cfg := testConfig(t)
	// Nur Loopback erlaubt — die Testadresse liegt bewusst ausserhalb.
	cfg.AllowedOriginPrefixes = mustPrefix(t, "127.0.0.1/32")

	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)

	s, err := New(ctx, cfg)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	r := httptest.NewRequest(http.MethodPost, "/mcp", strings.NewReader(`{}`))
	r.RemoteAddr = "203.0.113.10:5000"
	w := httptest.NewRecorder()
	s.Handler().ServeHTTP(w, r)

	// Genauer als "nicht 401": der Origin-Gate antwortet mit 403 (siehe
	// origin.Gate), nicht mit irgendeinem anderen Nicht-401-Code.
	if w.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want %d — der Origin-Gate haette vorher greifen und die Anfrage nie bis "+
			"zur Authentifizierung durchlassen sollen (Adresse %s ist nicht in der Freigabeliste)",
			w.Code, http.StatusForbidden, r.RemoteAddr)
	}
}

// /healthz ist bewusst von Origin-Gate und Authentifizierung ausgenommen
// (Liveness-/Readiness-Probe, siehe Gangways Handler()-Doku) — ein Container-
// Orchestrator ruft das nie von einer freigegebenen Adresse aus auf.
func TestHealthzIsReachableWithoutAuthOrAllowlist(t *testing.T) {
	cfg := testConfig(t)
	cfg.AllowedOriginPrefixes = mustPrefix(t, "127.0.0.1/32")

	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)

	s, err := New(ctx, cfg)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	r := httptest.NewRequest(http.MethodGet, "/healthz", nil)
	r.RemoteAddr = "203.0.113.10:5000"
	w := httptest.NewRecorder()
	s.Handler().ServeHTTP(w, r)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d fuer /healthz", w.Code, http.StatusOK)
	}
}

// Netzwerkfehler-Pfad (test-coverage-pflicht.md: Happy-Path/Error-Path/
// Netzwerkfehler fuer jede Mutations-/Netzwerk-Funktion). New() kontaktiert
// ueber serve.New den Identity Provider — ein externer Aufruf, der
// fehlschlagen kann, ohne dass die Konfiguration selbst falsch ist.
func TestNewFailsWhenIssuerIsUnreachable(t *testing.T) {
	env := map[string]string{
		"MCP_AUTH_MODE":                  "oidc",
		"MCP_OIDC_ISSUER":                "http://127.0.0.1:1", // Port 1: sofort verweigert, kein Timeout-Warten
		"MCP_OIDC_AUDIENCE":              "fileee-mcp-server",
		"MCP_RESOURCE_URL":               "https://mcp.example.com/mcp",
		"MCP_ALLOWED_SUBJECTS":           "abc123",
		"FILEEE_ALLOWED_ORIGIN_PREFIXES": "0.0.0.0/0",
		"FILEEE_USERNAME":                "nutzer@example.com",
		"FILEEE_PASSWORD":                "geheim",
	}

	cfg, err := config.LoadConfig(envOf(env))
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	s, err := New(ctx, cfg)
	if err == nil {
		t.Fatal("New = nil error, erwartet einen Fehlschlag — der Issuer ist nicht erreichbar")
	}
	if s != nil {
		t.Error("New lieferte einen nicht-nil *Server zusammen mit einem Fehler")
	}
	if !strings.Contains(err.Error(), "fileee-mcp: gangway:") {
		t.Errorf("err = %q, erwartet das fileee-mcp: gangway:-Praefix", err)
	}
	// Zugangsdaten duerfen auch in diesem fruehen, mit ihnen inhaltlich
	// unverwandten Fehlerpfad nicht auftauchen.
	if strings.Contains(err.Error(), "geheim") {
		t.Error("Fehlermeldung enthaelt das Fileee-Passwort")
	}
}

// Fehler-vom-Gegenueber-Pfad (test-coverage-pflicht.md, dritte Pflichtklasse
// neben Erfolg und Netzwerkfehler; Pruefbefund an #17). "Niemand hoert zu"
// (TestNewFailsWhenIssuerIsUnreachable) nimmt einen anderen Weg durch
// go-oidc als "der Dienst antwortet, aber mit einem Fehler" — Letzteres ist
// der praktisch haeufigere Betriebsfall (Tippfehler in der
// Aussteller-Adresse, kaputte Discovery, Anmeldedienst im Wartungsmodus).
// Dieser Test stellt einen erreichbaren Aussteller nach, dessen
// Discovery-Endpunkt mit 500 antwortet, und belegt, dass New() dabei sauber
// fehlschlaegt statt zu haengen oder abzustuerzen.
func TestNewFailsWhenIssuerRespondsWithAnError(t *testing.T) {
	idp := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "internal error", http.StatusInternalServerError)
	}))
	defer idp.Close()

	env := map[string]string{
		"MCP_AUTH_MODE":                  "oidc",
		"MCP_OIDC_ISSUER":                idp.URL,
		"MCP_OIDC_AUDIENCE":              "fileee-mcp-server",
		"MCP_RESOURCE_URL":               "https://mcp.example.com/mcp",
		"MCP_ALLOWED_SUBJECTS":           "abc123",
		"FILEEE_ALLOWED_ORIGIN_PREFIXES": "0.0.0.0/0",
		"FILEEE_USERNAME":                "nutzer@example.com",
		"FILEEE_PASSWORD":                "geheim",
	}

	cfg, err := config.LoadConfig(envOf(env))
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	s, err := New(ctx, cfg)
	if err == nil {
		t.Fatal("New = nil error, erwartet einen Fehlschlag — der Aussteller antwortet mit einem Serverfehler")
	}
	if s != nil {
		t.Error("New lieferte einen nicht-nil *Server zusammen mit einem Fehler")
	}
	if !strings.Contains(err.Error(), "fileee-mcp: gangway:") {
		t.Errorf("err = %q, erwartet das fileee-mcp: gangway:-Praefix", err)
	}
	if strings.Contains(err.Error(), "geheim") {
		t.Error("Fehlermeldung enthaelt das Fileee-Passwort")
	}
}

// Ablehnung eines nicht unterstuetzten Auth-Modus (Pruefbefund zu PR #18).
// Vor dem Umzug lag dieser Zweig im selben Paket wie sein einziger direkter
// Beleg (main_test.go, TestRunFailsFastForUnsupportedAuthMode) — der Test
// deckte damals also sowohl run() als auch diesen Zweig in New() ab. Nach dem
// Umzug liegt New() in diesem Paket, der bisherige Test aber weiterhin im
// fremden cmd/fileee-mcp-server und prueft die Ablehnung nur als Nebeneffekt
// eines Ende-zu-Ende-Wegs durch run() — bei einem Gegenversuch (Ablehnung
// entfernt) schlaegt er sogar aus dem falschen Grund fehl, weil run() dann
// bei der naechsten Pruefung (Issuer unerreichbar) haengen bliebe statt am
// erwarteten MCP_AUTH_MODE-Hinweis. Dieser direkte Test verankert die
// Absicherung wieder dort, wo der Code liegt.
func TestNewRejectsUnsupportedAuthMode(t *testing.T) {
	for _, mode := range []config.AuthMode{config.AuthToken, config.AuthBoth} {
		t.Run(string(mode), func(t *testing.T) {
			cfg := testConfig(t)
			cfg.AuthMode = mode

			s, err := New(context.Background(), cfg)
			if err == nil {
				t.Fatalf("New() mit AuthMode=%q = nil error, erwartet einen Fehlschlag — Gangway v0.2.0 "+
					"baut intern immer einen OIDC-Verifier auf und bietet keinen Weg, einen anderen "+
					"identity.Verifier einzuhaengen (siehe ADR-0015-Nachtrag)", mode)
			}
			if s != nil {
				t.Error("New lieferte einen nicht-nil *Server zusammen mit einem Fehler")
			}
			if !strings.Contains(err.Error(), "MCP_AUTH_MODE") {
				t.Errorf("err = %q, erwartet einen Hinweis auf MCP_AUTH_MODE", err)
			}
		})
	}
}

// Absicherung (Prüfbefund): New() darf bei einer ResourceURL, die nicht auf
// /mcp endet oder kuerzer als das Suffix ist, nicht aus dem Bereich laufen —
// auch dann nicht, wenn ein Aufrufer eine *config.Config von Hand baut oder
// veraendert, statt sie ueber LoadConfig zu beziehen (LoadConfig erzwingt das
// Suffix nur auf dem eigenen Weg, New() ist trotzdem exportiert und nimmt
// jede *config.Config entgegen).
func TestNewRejectsResourceURLWithoutMCPSuffix(t *testing.T) {
	cases := []string{
		"https://mcp.example.com/", // falsches Suffix
		"mcp",                      // kuerzer als "/mcp" selbst
		"",                         // leer
	}

	for _, resourceURL := range cases {
		t.Run(resourceURL, func(t *testing.T) {
			cfg := testConfig(t)
			cfg.ResourceURL = resourceURL

			s, err := New(context.Background(), cfg)
			if err == nil {
				t.Fatalf("New(%q) = nil error, erwartet einen Fehlschlag statt eines Panics", resourceURL)
			}
			if s != nil {
				t.Error("New lieferte einen nicht-nil *Server zusammen mit einem Fehler")
			}
			if !strings.Contains(err.Error(), "/mcp") {
				t.Errorf("err = %q, erwartet einen Hinweis auf das erwartete /mcp-Suffix", err)
			}
		})
	}
}

// Pruefbefund: New() und Run() muessen denselben Kontext verwenden. Gangway
// haengt an New()s ctx auch die Hintergrundarbeit des Identity Providers
// (periodisches Nachladen der Signierschluessel, siehe der Kommentar in
// main.go zu signal.NotifyContext) — wird New() mit einem anderen Kontext
// gebaut als dem, der spaeter abgebrochen wird, erreicht der Abbruch diese
// Hintergrundarbeit nie, und der Test raeumt nur zur Haelfte auf.
func TestRunStopsCleanlyWhenContextIsCancelled(t *testing.T) {
	cfg := testConfig(t)
	cfg.ListenAddr = "127.0.0.1:0"

	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)

	s, err := New(ctx, cfg)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	errc := make(chan error, 1)
	go func() { errc <- s.Run(ctx) }()

	cancel()
	if err := <-errc; err != nil {
		t.Fatalf("Run = %v, erwartet nil nach geordnetem Shutdown", err)
	}
}

// --- buildResolver (Aufgabe 5: Registrierung der lesenden Werkzeuge) ------

// TestBuildResolverSingleModeMapsAllowedSubjectsToTheOneAccount belegt den
// korrigierten single-Zweig von buildResolver ueber den echten Startpfad
// (testConfig setzt MCP_ALLOWED_SUBJECTS=abc123, FILEEE_MODE bleibt beim
// Default single). "irgendjemand" stand hier fruehen einer aelteren
// Fassung dieses Tests Pate (Pruefbefund, seither korrigiert): jedes
// beliebige Subject bekam Zugriff, weil buildResolver im Modus single
// accounts.NewSingle nutzte — subject-blind per eigenem Doc-Kommentar.
// MCP_ALLOWED_SUBJECTS war damit erzwungen, aber wirkungslos, obwohl
// config.go selbst das Gegenteil verspricht ("Ohne sie duerfte jeder
// Account des IdP auf die Dokumente zugreifen" — mit ihr eben nicht mehr
// jeder). Dieser Test belegt jetzt genau das Gegenteil des alten Namens:
// nur das erlaubte Subject bekommt Zugriff, ein beliebiges anderes nicht
// — siehe TestBuildResolverSingleModeRefusesASubjectNotOnTheAllowlist
// direkt darunter fuer die Ablehnung.
func TestBuildResolverSingleModeMapsAllowedSubjectsToTheOneAccount(t *testing.T) {
	cfg := testConfig(t)

	r, err := buildResolver(cfg)
	if err != nil {
		t.Fatalf("buildResolver: %v", err)
	}
	// "abc123" ist testConfigs MCP_ALLOWED_SUBJECTS-Eintrag.
	got, err := r.Credentials(context.Background(), &identity.Identity{Subject: "abc123"})
	if err != nil {
		t.Fatalf("Credentials: %v", err)
	}
	if got.Username != "nutzer@example.com" {
		t.Errorf("Username = %q, want %q", got.Username, "nutzer@example.com")
	}
}

// TestBuildResolverSingleModeRefusesASubjectNotOnTheAllowlist ist die
// Kernaussage des Fixes: ein Subject, das NICHT in MCP_ALLOWED_SUBJECTS
// steht, bekommt im Modus single keinen Zugriff — trotz eines ansonsten
// gueltigen, vom konfigurierten IdP signierten Tokens. Vor der Korrektur
// waere hier accounts.ErrNoAccount NIE aufgetreten, weil accounts.NewSingle
// jedes Subject akzeptiert. Siehe
// TestNewRefusesASubjectNotOnTheAllowlistDespiteAValidToken fuer denselben
// Beleg ueber den vollen, echten New()-Weg mit einem real signierten Token.
func TestBuildResolverSingleModeRefusesASubjectNotOnTheAllowlist(t *testing.T) {
	cfg := testConfig(t)

	r, err := buildResolver(cfg)
	if err != nil {
		t.Fatalf("buildResolver: %v", err)
	}
	if _, err := r.Credentials(context.Background(), &identity.Identity{Subject: "nicht-auf-der-liste"}); !errors.Is(err, accounts.ErrNoAccount) {
		t.Errorf("Credentials(nicht-auf-der-liste): err = %v, want ErrNoAccount", err)
	}
}

// TestBuildResolverMultiModeMapsSubjectsAcrossAccounts belegt den multi-Zweig:
// zwei Konten, je eigenes Subject, plus die Ablehnung eines dritten,
// unbekannten Subjects ohne Fallback (ADR-0012, Punkt 4/5) — ueber denselben
// echten Startpfad wie testConfig, nur mit FILEEE_MODE=multi.
func TestBuildResolverMultiModeMapsSubjectsAcrossAccounts(t *testing.T) {
	idp := testidp.New(t)
	env := map[string]string{
		"MCP_AUTH_MODE":                  "oidc",
		"MCP_OIDC_ISSUER":                idp.URL(),
		"MCP_OIDC_AUDIENCE":              "fileee-mcp-server",
		"MCP_RESOURCE_URL":               "https://mcp.example.com/mcp",
		"FILEEE_ALLOWED_ORIGIN_PREFIXES": "0.0.0.0/0, ::/0",
		"FILEEE_MODE":                    "multi",
		"FILEEE_ACCOUNTS":                "alice,bob",
		"FILEEE_ACCOUNT_ALICE_USERNAME":  "alice@example.com",
		"FILEEE_ACCOUNT_ALICE_PASSWORD":  "kein-echtes-passwort-alice",
		"FILEEE_ACCOUNT_ALICE_SUBJECTS":  "sub-alice",
		"FILEEE_ACCOUNT_BOB_USERNAME":    "bob@example.com",
		"FILEEE_ACCOUNT_BOB_PASSWORD":    "kein-echtes-passwort-bob",
		"FILEEE_ACCOUNT_BOB_SUBJECTS":    "sub-bob",
	}
	cfg, err := config.LoadConfig(envOf(env))
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}

	r, err := buildResolver(cfg)
	if err != nil {
		t.Fatalf("buildResolver: %v", err)
	}

	got, err := r.Credentials(context.Background(), &identity.Identity{Subject: "sub-bob"})
	if err != nil {
		t.Fatalf("Credentials(sub-bob): %v", err)
	}
	if got.Username != "bob@example.com" {
		t.Errorf("Username = %q, want %q", got.Username, "bob@example.com")
	}

	if _, err := r.Credentials(context.Background(), &identity.Identity{Subject: "unbekannt"}); !errors.Is(err, accounts.ErrNoAccount) {
		t.Errorf("Credentials(unbekannt): err = %v, want ErrNoAccount", err)
	}
}

// TestBuildResolverRefusesASubjectMappedToTwoAccounts ist der Gegenversuch
// zum Pruefbefund: LoadConfig selbst laesst ein doppeltes Subject nie durch
// (config.go, ladeKonten, cfg.subjectIndex-Kollisionspruefung), aber
// buildResolver() ist exportiert-in-diesem-Paket und nimmt jede *config.Config
// entgegen — eine von Hand gebaute Config mit demselben Subject in zwei
// Konten muss deshalb selbst hier noch abgelehnt werden, statt still das
// zuletzt gesehene Konto gewinnen zu lassen (das haette einen Aufrufer auf
// ein fremdes Konto abgebildet — genau der first-match-wins-Fehler, den
// ADR-0012 Punkt 4 ausschliesst).
func TestBuildResolverRefusesASubjectMappedToTwoAccounts(t *testing.T) {
	cfg := testConfig(t)
	cfg.AccountMode = config.ModeMulti
	cfg.Accounts = []config.Account{
		{Key: "alice", Username: "alice@example.com", Password: "pw-a", Subjects: []string{"geteiltes-subject"}},
		{Key: "bob", Username: "bob@example.com", Password: "pw-b", Subjects: []string{"geteiltes-subject"}},
	}

	r, err := buildResolver(cfg)
	if err == nil {
		t.Fatal("buildResolver: want an error for a subject mapped to two accounts, got nil")
	}
	if r != nil {
		t.Error("buildResolver lieferte einen nicht-nil Resolver zusammen mit einem Fehler")
	}
	if !strings.Contains(err.Error(), "geteiltes-subject") {
		t.Errorf("err = %q, erwartet einen Hinweis auf das betroffene Subject", err)
	}
}

// TestNewFailsWhenSingleModeHasNoConfiguredAccount deckt buildResolvers
// eigene Absicherung ab: LoadConfig selbst laesst den Modus single nie ohne
// genau ein Konto durch, aber New() ist exportiert und nimmt jede *Config
// entgegen (siehe die Anmerkung bei der ResourceURL-Pruefung weiter oben) —
// eine von Hand veraenderte *Config muss deshalb eine benannte Fehlermeldung
// statt eines Index-Out-of-Range-Panics ausloesen.
func TestNewFailsWhenSingleModeHasNoConfiguredAccount(t *testing.T) {
	cfg := testConfig(t)
	cfg.Accounts = nil

	s, err := New(context.Background(), cfg)
	if err == nil {
		t.Fatal("New: want an error, got nil")
	}
	if s != nil {
		t.Error("New lieferte einen nicht-nil *Server zusammen mit einem Fehler")
	}
	if !strings.Contains(err.Error(), "genau ein konfiguriertes Konto") {
		t.Errorf("err = %q, erwartet einen Hinweis auf die single-Konto-Pflicht", err)
	}
}

// --- sessionFilePath -------------------------------------------------------

// TestSessionFilePathIsDeterministicUniqueAndFilesystemSafe deckt
// sessionFilePath direkt ab: dieselbe Eingabe muss denselben Pfad liefern
// (Wiederverwendung der Session ueber Neustarts hinweg), unterschiedliche
// Konten muessen unterschiedliche Dateien treffen, und der Klartext-Konto-Key
// (der bei clientpool ein beliebiger Fileee-Benutzername sein kann, siehe
// sessionFilePaths Doc-Kommentar) darf nicht im Dateinamen auftauchen.
func TestSessionFilePathIsDeterministicUniqueAndFilesystemSafe(t *testing.T) {
	dir := t.TempDir()

	first := sessionFilePath(dir, "user@example.com")
	second := sessionFilePath(dir, "user@example.com")
	if first != second {
		t.Errorf("sessionFilePath ist nicht deterministisch: %q != %q", first, second)
	}
	if filepath.Dir(first) != dir {
		t.Errorf("sessionFilePath(%q, ...) = %q, erwartet einen Pfad innerhalb von %q", dir, first, dir)
	}
	if filepath.Ext(first) != ".json" {
		t.Errorf("sessionFilePath = %q, erwartet die Endung .json", first)
	}
	if strings.Contains(first, "user@example.com") {
		t.Errorf("sessionFilePath = %q enthaelt den Konto-Key im Klartext", first)
	}

	other := sessionFilePath(dir, "different@example.com")
	if other == first {
		t.Error("zwei unterschiedliche Konto-Keys ergaben dieselbe Session-Datei")
	}
}
