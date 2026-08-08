package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"strings"
	"testing"
	"time"

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

	cfg, err := LoadConfig(envOf(env))
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

	cfg, err := LoadConfig(envOf(env))
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

// Absicherung (Prüfbefund): New() darf bei einer ResourceURL, die nicht auf
// /mcp endet oder kuerzer als das Suffix ist, nicht aus dem Bereich laufen —
// auch dann nicht, wenn ein Aufrufer eine *Config von Hand baut oder
// veraendert, statt sie ueber LoadConfig zu beziehen (LoadConfig erzwingt das
// Suffix nur auf dem eigenen Weg, New() ist trotzdem exportiert und nimmt
// jede *Config entgegen).
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
