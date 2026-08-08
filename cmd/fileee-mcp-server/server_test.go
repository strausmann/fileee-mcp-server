package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"strings"
	"testing"

	"github.com/strausmann/gangway/identity/testidp"
)

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
// LoadConfig statt eines von Hand gebauten *Config, weil sonst die
// unexportierte subjectIndex-Aufloesung uebergangen wuerde und der Test etwas
// pruefte, das mit dem echten Startpfad nicht mehr uebereinstimmt.
func testConfig(t *testing.T) *Config {
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

	cfg, err := LoadConfig(envOf(env))
	if err != nil {
		t.Fatalf("testConfig: LoadConfig = %v", err)
	}
	return cfg
}

func TestUnauthenticatedRequestIsRefusedWithAChallenge(t *testing.T) {
	cfg := testConfig(t)

	s, err := New(context.Background(), cfg)
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

	s, err := New(context.Background(), cfg)
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

	s, err := New(context.Background(), cfg)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	r := httptest.NewRequest(http.MethodPost, "/mcp", strings.NewReader(`{}`))
	r.RemoteAddr = "203.0.113.10:5000"
	w := httptest.NewRecorder()
	s.Handler().ServeHTTP(w, r)

	if w.Code == http.StatusUnauthorized {
		t.Fatalf("status = 401 — der Origin-Gate haette vorher greifen und die Anfrage nie bis zur "+
			"Authentifizierung durchlassen sollen (Adresse %s ist nicht in der Freigabeliste)", r.RemoteAddr)
	}
}

// /healthz ist bewusst von Origin-Gate und Authentifizierung ausgenommen
// (Liveness-/Readiness-Probe, siehe Gangways Handler()-Doku) — ein Container-
// Orchestrator ruft das nie von einer freigegebenen Adresse aus auf.
func TestHealthzIsReachableWithoutAuthOrAllowlist(t *testing.T) {
	cfg := testConfig(t)
	cfg.AllowedOriginPrefixes = mustPrefix(t, "127.0.0.1/32")

	s, err := New(context.Background(), cfg)
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

func TestRunStopsCleanlyWhenContextIsCancelled(t *testing.T) {
	cfg := testConfig(t)
	cfg.ListenAddr = "127.0.0.1:0"

	s, err := New(context.Background(), cfg)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	errc := make(chan error, 1)
	go func() { errc <- s.Run(ctx) }()

	cancel()
	if err := <-errc; err != nil {
		t.Fatalf("Run = %v, erwartet nil nach geordnetem Shutdown", err)
	}
}
