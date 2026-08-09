package server

import (
	"context"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/strausmann/fileee-mcp-server/internal/clientpool"
	"github.com/strausmann/fileee-mcp-server/internal/config"
	"github.com/strausmann/fileee-mcp-server/internal/tools"
	"github.com/strausmann/gangway/identity"
	"github.com/strausmann/gangway/identity/testidp"
	"github.com/strausmann/go-fileee/fileee"
)

// --- tokenScopes: die ueblichen Claim-Formen fuer Scopes -------------------

// TestTokenScopes deckt tokenScopes direkt ab: "scope" ist die Form nach
// RFC 8693 (ein einzelner, leerzeichengetrennter String), "scp" ist Entras
// Aequivalent fuer delegierte Berechtigungen (siehe docs/idp/entra-id.md,
// Abschnitt "Zu den Scopes") -- in der Praxis ebenfalls ein leerzeichen-
// getrennter String, keine JSON-Liste. Eine Liste wird trotzdem akzeptiert,
// falls ein anderer Aussteller den Claim so befuellt (derselbe Grundsatz wie
// bei claimStrings fuer den Capability-Claim).
func TestTokenScopes(t *testing.T) {
	cases := []struct {
		name   string
		claims map[string]any
		want   []string
	}{
		{
			"scope als leerzeichengetrennter String (RFC 8693)",
			map[string]any{"scope": "read write"},
			[]string{"read", "write"},
		},
		{
			"scp als leerzeichengetrennter String (Entra)",
			map[string]any{"scp": "mcp.access"},
			[]string{"mcp.access"},
		},
		{
			"beide Claims gleichzeitig werden zusammengefuehrt",
			map[string]any{"scope": "read", "scp": "mcp.access"},
			[]string{"mcp.access", "read"},
		},
		{
			"scope als Liste (defensiv, falls ein Aussteller so befuellt)",
			map[string]any{"scope": []any{"read", "write"}},
			[]string{"read", "write"},
		},
		{
			"Listen-Eintrag, der kein String ist, wird uebersprungen",
			map[string]any{"scope": []any{"read", 42}},
			[]string{"read"},
		},
		{
			"von Hand gebautes []string (Identity, die ein Test selbst zusammenstellt)",
			map[string]any{"scope": []string{"read", "write"}},
			[]string{"read", "write"},
		},
		{
			"kein Scope-Claim liefert die leere Menge",
			map[string]any{"sub": "abc"},
			nil,
		},
		{
			"leerer String liefert die leere Menge",
			map[string]any{"scope": ""},
			nil,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := tokenScopes(tc.claims)

			var gotSorted []string
			for s := range got {
				gotSorted = append(gotSorted, s)
			}
			slices.Sort(gotSorted)
			want := slices.Clone(tc.want)
			slices.Sort(want)

			if !slices.Equal(gotSorted, want) {
				t.Errorf("tokenScopes(%#v) = %v, want %v", tc.claims, gotSorted, want)
			}
		})
	}
}

// --- scopesSatisfied: die Entscheidung selbst -------------------------------

func TestScopesSatisfied(t *testing.T) {
	cases := []struct {
		name string
		cfg  *config.Config
		id   *identity.Identity
		want bool
	}{
		{
			"keine Pflicht-Scopes konfiguriert: jeder Aufrufer erlaubt",
			&config.Config{},
			&identity.Identity{Claims: map[string]any{}},
			true,
		},
		{
			"keine Pflicht-Scopes konfiguriert, nicht einmal eine Identitaet: erlaubt",
			&config.Config{},
			nil,
			true,
		},
		{
			"Pflicht-Scope ist im scp-Claim vorhanden",
			&config.Config{OIDCRequiredScopes: []string{"mcp.access"}},
			&identity.Identity{Claims: map[string]any{"scp": "mcp.access"}},
			true,
		},
		{
			"Pflicht-Scope ist im scope-Claim vorhanden",
			&config.Config{OIDCRequiredScopes: []string{"mcp.access"}},
			&identity.Identity{Claims: map[string]any{"scope": "mcp.access"}},
			true,
		},
		{
			"Token traegt einen anderen Scope, nicht den geforderten",
			&config.Config{OIDCRequiredScopes: []string{"mcp.access"}},
			&identity.Identity{Claims: map[string]any{"scp": "user.read"}},
			false,
		},
		{
			"mehrere Pflicht-Scopes, einer fehlt",
			&config.Config{OIDCRequiredScopes: []string{"mcp.access", "mcp.write"}},
			&identity.Identity{Claims: map[string]any{"scp": "mcp.access"}},
			false,
		},
		{
			"mehrere Pflicht-Scopes, beide vorhanden",
			&config.Config{OIDCRequiredScopes: []string{"mcp.access", "mcp.write"}},
			&identity.Identity{Claims: map[string]any{"scp": "mcp.access mcp.write"}},
			true,
		},
		{
			"Token traegt gar keinen Scope-Claim",
			&config.Config{OIDCRequiredScopes: []string{"mcp.access"}},
			&identity.Identity{Claims: map[string]any{"sub": "abc"}},
			false,
		},
		{
			"Pflicht-Scopes konfiguriert, aber keine Identitaet: fail-closed",
			&config.Config{OIDCRequiredScopes: []string{"mcp.access"}},
			nil,
			false,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := scopesSatisfied(tc.cfg, tc.id); got != tc.want {
				t.Errorf("scopesSatisfied(...) = %v, want %v", got, tc.want)
			}
		})
	}
}

// --- End-zu-Ende: MCP_OIDC_REQUIRED_SCOPES tatsaechlich durchgesetzt -------

// testConfigWithIDPAndRequiredScopes ist testConfigWithIDP, verlangt aber
// zusaetzlich scopes ueber MCP_OIDC_REQUIRED_SCOPES -- fuer die Tests, die
// pruefen, dass diese seit config.LoadConfig geladene, aber (Pruefbefund zu
// dieser Aufgabe) nirgends ausgewertete Einstellung tatsaechlich wirkt.
func testConfigWithIDPAndRequiredScopes(t *testing.T, scopes string) (*config.Config, *testidp.IDP) {
	t.Helper()
	idp := testidp.New(t)

	env := map[string]string{
		"MCP_AUTH_MODE":                  "oidc",
		"MCP_OIDC_ISSUER":                idp.URL(),
		"MCP_OIDC_AUDIENCE":              "fileee-mcp-server",
		"MCP_OIDC_REQUIRED_SCOPES":       scopes,
		"MCP_RESOURCE_URL":               "https://mcp.example.com/mcp",
		"MCP_ALLOWED_SUBJECTS":           "abc123",
		"FILEEE_ALLOWED_ORIGIN_PREFIXES": "0.0.0.0/0, ::/0",
		"FILEEE_USERNAME":                "nutzer@example.com",
		"FILEEE_PASSWORD":                "geheim",
	}

	cfg, err := config.LoadConfig(envOf(env))
	if err != nil {
		t.Fatalf("testConfigWithIDPAndRequiredScopes: LoadConfig = %v", err)
	}
	return cfg, idp
}

// TestNewRefusesACallerWithoutTheRequiredScope ist die End-zu-Ende-Probe:
// MCP_OIDC_REQUIRED_SCOPES verlangt "mcp.access", das ausgestellte Token
// traegt im scp-Claim einen ANDEREN Scope. Vor dieser Aenderung kam dieser
// Aufrufer durch -- MCP_OIDC_REQUIRED_SCOPES wurde zwar geladen (config.go,
// LoadConfig), aber von New()/AttachMCPSelector nie geprueft.
func TestNewRefusesACallerWithoutTheRequiredScope(t *testing.T) {
	fileeeMock := newFileeeMock(t)
	cfg, idp := testConfigWithIDPAndRequiredScopes(t, "mcp.access")

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

	// Gueltiges Token fuer den erlaubten Subject "abc123" -- der scp-Claim
	// traegt aber "user.read", nicht den geforderten "mcp.access".
	token := idp.Token(map[string]any{
		"iss": idp.URL(), "aud": "fileee-mcp-server", "sub": "abc123", "scp": "user.read",
		"exp": time.Now().Add(time.Hour).Unix(),
	})
	client := mcp.NewClient(&mcp.Implementation{Name: "scope-test", Version: "0.0.0"}, nil)
	transport := &mcp.StreamableClientTransport{
		Endpoint:             httpSrv.URL + "/mcp",
		HTTPClient:           &http.Client{Transport: bearerRoundTripper{token: token}},
		DisableStandaloneSSE: true,
	}
	session, err := client.Connect(ctx, transport, nil)
	if err == nil {
		_ = session.Close()
		t.Fatal("Connect gelang mit einem Token ohne den geforderten Scope -- MCP_OIDC_REQUIRED_SCOPES " +
			"wurde geladen, aber nicht durchgesetzt (der Pruefbefund zu dieser Aufgabe)")
	}
}

// TestNewAllowsACallerWithTheRequiredScope ist das Gegenstueck: derselbe
// Aufrufer, derselbe geforderte Scope -- diesmal traegt das Token ihn, und
// der Werkzeugaufruf muss durchgehen wie ohne MCP_OIDC_REQUIRED_SCOPES.
func TestNewAllowsACallerWithTheRequiredScope(t *testing.T) {
	fileeeMock := newFileeeMock(t)
	cfg, idp := testConfigWithIDPAndRequiredScopes(t, "mcp.access")

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

	token := idp.Token(map[string]any{
		"iss": idp.URL(), "aud": "fileee-mcp-server", "sub": "abc123", "scp": "mcp.access",
		"exp": time.Now().Add(time.Hour).Unix(),
	})
	client := mcp.NewClient(&mcp.Implementation{Name: "scope-test", Version: "0.0.0"}, nil)
	transport := &mcp.StreamableClientTransport{
		Endpoint:             httpSrv.URL + "/mcp",
		HTTPClient:           &http.Client{Transport: bearerRoundTripper{token: token}},
		DisableStandaloneSSE: true,
	}
	session, err := client.Connect(ctx, transport, nil)
	if err != nil {
		t.Fatalf("Connect mit dem geforderten Scope im Token: %v", err)
	}
	t.Cleanup(func() { _ = session.Close() })

	res, err := session.CallTool(ctx, &mcp.CallToolParams{Name: tools.ToolListDocuments})
	if err != nil {
		t.Fatalf("CallTool(list_documents): %v", err)
	}
	if res.IsError {
		t.Fatalf("CallTool(list_documents): IsError = true, content = %+v", res.Content)
	}
}

// TestNewRefusesACallerWithNoScopeClaimAtAll deckt den Fall ab, dass ein
// Token gar keinen scope/scp-Claim traegt (z. B. weil der Aussteller anders
// konfiguriert ist) -- muss wie ein fehlender Scope behandelt werden, nicht
// als "Pruefung nicht anwendbar, also durchlassen".
func TestNewRefusesACallerWithNoScopeClaimAtAll(t *testing.T) {
	fileeeMock := newFileeeMock(t)
	cfg, idp := testConfigWithIDPAndRequiredScopes(t, "mcp.access")

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

	token := idp.Token(map[string]any{
		"iss": idp.URL(), "aud": "fileee-mcp-server", "sub": "abc123",
		"exp": time.Now().Add(time.Hour).Unix(),
	})
	client := mcp.NewClient(&mcp.Implementation{Name: "scope-test", Version: "0.0.0"}, nil)
	transport := &mcp.StreamableClientTransport{
		Endpoint:             httpSrv.URL + "/mcp",
		HTTPClient:           &http.Client{Transport: bearerRoundTripper{token: token}},
		DisableStandaloneSSE: true,
	}
	session, err := client.Connect(ctx, transport, nil)
	if err == nil {
		_ = session.Close()
		t.Fatal("Connect gelang mit einem Token ganz ohne Scope-Claim, obwohl MCP_OIDC_REQUIRED_SCOPES " +
			"gesetzt ist")
	}
}
