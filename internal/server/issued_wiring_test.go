package server

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/strausmann/fileee-mcp-server/internal/config"
	"github.com/strausmann/fileee-mcp-server/internal/issued"
	"github.com/strausmann/gangway/access"
	"github.com/strausmann/gangway/identity/testidp"
	"github.com/strausmann/gangway/serve"
)

// --- Aufgabe 3, Schritt 4: New() baut den Store WIRKLICH aus der Config ---
//
// internal/issued's eigene
// TestKonfigurierterDeckelGoverniertDenAusDerConfigGebautenStore (Schritt 5,
// dort untergebracht — siehe dessen Doc-Kommentar fuer die Begruendung, warum
// gerade DIESER Test PR #68s Fehlerbild fangen wuerde) belegt bereits, dass
// die FORMEL "issued.New(time.Duration(cfg.IssuedIDTTLSeconds)*time.Second,
// int(cfg.IssuedIDMaxPerIdentity))" korrekt ist. Was jener Test NICHT prueft:
// ob server.go's New() diese Formel tatsaechlich woertlich verwendet, statt
// z. B. einen hartcodierten Wert oder eine vertauschte Reihenfolge der
// beiden Felder.
//
// Dieser Test schliesst genau diese Luecke, indem er den ECHTEN, ueber New()
// gebauten *issued.Store liest (s.issuedStore, unexportiertes Feld,
// erreichbar weil diese Datei im selben Paket liegt) und ihn mit einer
// ECHT authentifizierten Identitaet befragt — dieselbe Sicherheitszusage,
// die internal/issued/issued_test.go's ctxMitIdentitaet fuer den Store
// isoliert bereits durchsetzt (siehe deren eigenen, ausfuehrlichen
// Doc-Kommentar zu WARUM ein echter Gangway-Rundlauf noetig ist statt einer
// selbstgebauten Attrappe).
//
// Der Gangway-Server, der die Identitaet fuer diesen Test ausstellt, ist
// bewusst ein EIGENER, separat von s.gw aufgesetzter (analog
// ctxMitIdentitaet) — nicht s.gw selbst: tools.RegisterAll (internal/tools)
// kennt s.issuedStore in dieser Aufgabe noch nicht (das aendert erst eine
// Folgeaufgabe, siehe Server.issuedStore's Doc-Kommentar), es gibt also
// keinen ueber s.Handler() erreichbaren Werkzeugaufruf, dessen ctx
// s.issuedStore je sehen wuerde. serve.IdentityFrom liest die Identitaet
// aber rein aus dem context.Context-Wert, den IRGENDEINE Gangway-
// Authentifizierung dort abgelegt hat — welcher *serve.Server-Instanz sie
// entstammt, ist ihm gleichgueltig. Der separate Gangway-Server hier dient
// deshalb ausschliesslich dazu, ein echtes, durch echte Middleware
// gelaufenes ctx zu gewinnen; der Store, den dieses ctx anschliessend
// befragt, ist trotzdem der ECHTE aus New().
func TestNewWiresConfiguredIssuedIDLimitsIntoTheStore(t *testing.T) {
	// New() discovers MCP_OIDC_ISSUER for real at construction time
	// (resolveVerifier) — a real, local testidp.IDP is needed here for the
	// same reason testConfigWithIDP builds one, not because this test cares
	// who the issuer trusts.
	idp := testidp.New(t)
	env := map[string]string{
		"MCP_AUTH_MODE":                     "oidc",
		"MCP_OIDC_PROVIDER":                 "generic",
		"MCP_OIDC_ISSUER":                   idp.URL(),
		"MCP_OIDC_CLIENT_ID":                "fileee-mcp-server",
		"MCP_RESOURCE_URL":                  "https://mcp.example.com/mcp",
		"MCP_ALLOWED_SUBJECTS":              "abc123",
		"FILEEE_ALLOWED_ORIGIN_PREFIXES":    "0.0.0.0/0, ::/0",
		"FILEEE_USERNAME":                   "nutzer@example.com",
		"FILEEE_PASSWORD":                   "geheim",
		"FILEEE_ISSUED_ID_MAX_PER_IDENTITY": "2",
	}
	cfg, err := config.LoadConfig(envOf(env))
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}
	if cfg.IssuedIDMaxPerIdentity != 2 {
		t.Fatalf("Vorbedingung: cfg.IssuedIDMaxPerIdentity = %d, erwartet 2 — Test misst sonst nichts",
			cfg.IssuedIDMaxPerIdentity)
	}

	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)

	s, err := New(ctx, cfg)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if s.issuedStore == nil {
		t.Fatal("s.issuedStore = nil nach New() — Schritt 4 der Aufgabe verlangt, dass New() ihn baut")
	}

	authCtx := authenticatedCtx(t, "alice")

	for _, id := range []string{"a", "b", "c"} {
		s.issuedStore.Record(authCtx, id)
	}

	if err := s.issuedStore.Check(authCtx, "a"); !errors.Is(err, issued.ErrNotIssued) {
		t.Fatalf("erste (aelteste) ID im ECHTEN, ueber New() gebauten Store bei konfiguriertem Deckel 2: "+
			"%v, want ErrNotIssued — New() haelt sich nicht an den konfigurierten Deckel", err)
	}
	for _, id := range []string{"b", "c"} {
		if err := s.issuedStore.Check(authCtx, id); err != nil {
			t.Fatalf("ID %q nach Ueberlauf: %v, want nil", id, err)
		}
	}
}

// authenticatedCtx spiegelt internal/issued/issued_test.go's
// ctxMitIdentitaet (siehe deren Doc-Kommentar fuer die vollstaendige
// Begruendung) — hier lokal nachgebaut statt importiert: ein Test-Helfer
// eines fremden Pakets ist ausserhalb dieses Pakets nicht sichtbar, und ein
// eigenes Test-Hilfspaket waere fuer diese eine Funktion unverhaeltnismaessig.
// bearerRoundTripper und mustPrefix sind bereits Teil dieses Pakets
// (server_test.go) und werden hier wiederverwendet statt erneut definiert.
func authenticatedCtx(t *testing.T, subject string) context.Context {
	t.Helper()

	idp := testidp.New(t)
	captured := make(chan context.Context, 1)

	mcpServer := mcp.NewServer(&mcp.Implementation{Name: "issued-wiring-test", Version: "0.0.0"}, nil)
	mcp.AddTool(mcpServer, &mcp.Tool{Name: "capture"},
		func(ctx context.Context, _ *mcp.CallToolRequest, _ struct{}) (*mcp.CallToolResult, any, error) {
			captured <- ctx
			return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: "ok"}}}, nil, nil
		})

	const audience = "fileee-mcp-issued-wiring-test"
	gwCfg := &serve.Config{
		Addr:            "127.0.0.1:0",
		PublicBaseURL:   "https://issued-wiring-test.example.invalid",
		IssuerURL:       idp.URL(),
		Audience:        audience,
		SubjectClaim:    "sub",
		AllowedPrefixes: mustPrefix(t, "127.0.0.1/32", "::1/128"),
	}
	gw, err := serve.New(context.Background(), gwCfg, serve.WithDecider(access.AllowAll()))
	if err != nil {
		t.Fatalf("authenticatedCtx(%q): serve.New: %v", subject, err)
	}
	gw.AttachMCP(mcpServer)

	srv := httptest.NewServer(gw.Handler())
	t.Cleanup(srv.Close)

	token := idp.Token(map[string]any{
		"iss": idp.URL(), "aud": audience, "sub": subject,
		"exp": time.Now().Add(time.Hour).Unix(),
	})
	client := mcp.NewClient(&mcp.Implementation{Name: "issued-wiring-test-client", Version: "0.0.0"}, nil)
	transport := &mcp.StreamableClientTransport{
		Endpoint:             srv.URL + "/mcp",
		HTTPClient:           &http.Client{Transport: bearerRoundTripper{token: token}},
		DisableStandaloneSSE: true,
	}
	session, err := client.Connect(context.Background(), transport, nil)
	if err != nil {
		t.Fatalf("authenticatedCtx(%q): Connect: %v", subject, err)
	}
	t.Cleanup(func() { _ = session.Close() })

	res, err := session.CallTool(context.Background(), &mcp.CallToolParams{Name: "capture"})
	if err != nil {
		t.Fatalf("authenticatedCtx(%q): CallTool(capture): %v", subject, err)
	}
	if res.IsError {
		t.Fatalf("authenticatedCtx(%q): CallTool(capture): IsError = true, content = %+v", subject, res.Content)
	}

	return <-captured
}
