package server

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/strausmann/fileee-mcp-server/internal/clientpool"
	"github.com/strausmann/fileee-mcp-server/internal/config"
	"github.com/strausmann/fileee-mcp-server/internal/tools"
	"github.com/strausmann/gangway/identity/testidp"
	"github.com/strausmann/go-fileee/fileee"
)

// --- MCP_OIDC_REQUIRED_SCOPES wird ANGEKUENDIGT, nicht nur durchgesetzt ----
//
// scopes_test.go deckt bereits ab, dass ein Aufrufer ohne den geforderten
// Scope abgewiesen wird (scopesSatisfied, AttachMCPSelector) -- das prueft
// aber nur den Fall, dass der Aufrufer BEREITS ein Token besitzt. Ein
// Connector, der noch gar kein Token hat (claude.ai beim allerersten
// Verbindungsaufbau), erfaehrt den geforderten Scope NICHT aus dieser
// Pruefung, sondern ausschliesslich aus zwei Ankuendigungs-Stellen VOR dem
// Token-Austausch: dem "scope"-Parameter der WWW-Authenticate-Challenge und
// dem scopes_supported-Feld des RFC-9728-Metadatendokuments (beide erst ab
// gangway v0.4.0 ueber serve.Config.RequiredScopes verfuegbar). Ohne diese
// Ankuendigung fordert der Connector einen falschen Standard-Scope an
// (z. B. "openid profile offline_access"), und Entra weist den
// Token-Austausch mit AADSTS9010010 zurueck, weil resource und scope nicht
// zusammenpassen -- der Server selbst wird dabei nie erreicht.
//
// Diese Tests pruefen deshalb New()'s EIGENE Verdrahtung von
// cfg.OIDCRequiredScopes nach serve.Config.RequiredScopes -- ueber den
// tatsaechlichen Handler()-Weg, nicht durch direktes Aufrufen einer
// gangway-internen Funktion. Ohne diese Verdrahtung waeren beide Felder
// leer, obwohl MCP_OIDC_REQUIRED_SCOPES gesetzt und von scopesSatisfied
// bereits durchgesetzt wird -- genau die Luecke, die den Live-Vorfall
// (AADSTS9010010, dreimal reproduziert) verursacht hat.

// wellKnownProtectedResourcePath ist der RFC-9728-Pfad, unter dem gangway
// das Metadatendokument mountet (gangway/serve, wellKnownProtectedResourcePath
// -- dort unexportiert, deshalb hier als eigene Konstante mit demselben
// Wert statt eines Imports).
const wellKnownProtectedResourcePath = "/.well-known/oauth-protected-resource"

// protectedResourceMetadata bildet nur das eine Feld ab, das diese Tests
// brauchen -- ein eigener, minimaler Typ statt eines Imports von
// oauthex.ProtectedResourceMetadata, damit dieses Testpaket keine zusaetzliche
// Abhaengigkeit fuer eine einzelne JSON-Pruefung braucht. Das
// `omitempty`-Verhalten des echten Feldes wird davon nicht beruehrt: die
// Abwesenheits-Tests unten pruefen die rohen JSON-Bytes, nicht dieses
// unmarshalte Struct.
type protectedResourceMetadata struct {
	ScopesSupported []string `json:"scopes_supported,omitempty"`
}

// unauthenticatedMCPRequest schickt eine unauthentifizierte Anfrage an /mcp
// und liefert die Antwort -- der Weg, ueber den ein Connector die
// WWW-Authenticate-Challenge zum ersten Mal sieht.
func unauthenticatedMCPRequest(t *testing.T, h http.Handler) *httptest.ResponseRecorder {
	t.Helper()
	r := httptest.NewRequest(http.MethodPost, "/mcp", strings.NewReader(`{}`))
	r.RemoteAddr = "203.0.113.10:5000"
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	return w
}

// metadataDocumentRequest GETet das RFC-9728-Metadatendokument -- der
// zweite Weg, ueber den ein Connector den geforderten Scope erfahren kann,
// unabhaengig von einer vorherigen 401-Antwort (siehe gangway/serve,
// wellKnownProtectedResourcePathSpecific-Kommentar).
func metadataDocumentRequest(t *testing.T, h http.Handler) (*http.Response, []byte) {
	t.Helper()
	r := httptest.NewRequest(http.MethodGet, wellKnownProtectedResourcePath, nil)
	r.RemoteAddr = "203.0.113.10:5000"
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	resp := w.Result()
	body := make([]byte, 0)
	buf := make([]byte, 4096)
	for {
		n, err := resp.Body.Read(buf)
		body = append(body, buf[:n]...)
		if err != nil {
			break
		}
	}
	return resp, body
}

// TestNewAdvertisesRequiredScopeInChallengeWhenConfigured ist die
// Regression zum Live-Vorfall: ist MCP_OIDC_REQUIRED_SCOPES gesetzt, muss
// die 401-Antwort auf eine unauthentifizierte Anfrage einen
// scope-Parameter tragen -- der einzige Weg, auf dem ein Connector, der
// noch gar kein Token hat, den geforderten Scope ueberhaupt erfahren kann,
// bevor er sich beim IdP anmeldet.
func TestNewAdvertisesRequiredScopeInChallengeWhenConfigured(t *testing.T) {
	cfg, _ := testConfigWithIDPAndRequiredScopes(t, "mcp.access")

	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)

	s, err := New(ctx, cfg)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	w := unauthenticatedMCPRequest(t, s.Handler())
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want %d", w.Code, http.StatusUnauthorized)
	}

	challenge := w.Header().Get("WWW-Authenticate")
	if !strings.Contains(challenge, `scope="mcp.access"`) {
		t.Errorf(`WWW-Authenticate = %q, want a scope="mcp.access" parameter -- ohne ihn erfaehrt ein `+
			`Connector ohne vorhandenes Token nie, welchen Scope er beim IdP anfordern muss (Live-Vorfall: `+
			`AADSTS9010010)`, challenge)
	}
	// scope darf resource_metadata nicht verdraengen -- ein Connector
	// braucht weiterhin den Verweis auf das Metadatendokument, um Aussteller
	// und weitere Angaben zu finden.
	if !strings.Contains(challenge, "resource_metadata") {
		t.Errorf("WWW-Authenticate = %q, erwarte weiterhin einen resource_metadata-Verweis", challenge)
	}
}

// TestNewAdvertisesMultipleRequiredScopesSpaceDelimitedInChallenge prueft
// das Well-Formedness-Detail aus RFC 6750 §3: mehrere Scopes stehen als EIN
// leerzeichengetrennter Wert in einem einzigen scope-Parameter -- nicht als
// mehrere Parameter (kein gueltiges auth-param) und nicht kommasepariert
// (das ist nur die Schreibweise von MCP_OIDC_REQUIRED_SCOPES selbst, nicht
// das Wire-Format).
func TestNewAdvertisesMultipleRequiredScopesSpaceDelimitedInChallenge(t *testing.T) {
	cfg, _ := testConfigWithIDPAndRequiredScopes(t, "mcp.access,offline_access")

	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)

	s, err := New(ctx, cfg)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	w := unauthenticatedMCPRequest(t, s.Handler())

	challenge := w.Header().Get("WWW-Authenticate")
	if !strings.Contains(challenge, `scope="mcp.access offline_access"`) {
		t.Errorf(`WWW-Authenticate = %q, want scope="mcp.access offline_access"`, challenge)
	}
}

// TestNewOmitsScopeParameterInChallengeWhenNoRequiredScopesConfigured ist
// die Regression fuer Abwaertskompatibilitaet: ohne MCP_OIDC_REQUIRED_SCOPES
// darf sich an der Challenge nichts aendern. Geprueft wird der exakte
// Header-Wert, nicht nur die Abwesenheit eines Substrings -- damit auch
// jede andere ungewollte Aenderung am unkonfigurierten Pfad hier auffiele.
func TestNewOmitsScopeParameterInChallengeWhenNoRequiredScopesConfigured(t *testing.T) {
	cfg := testConfig(t) // MCP_OIDC_REQUIRED_SCOPES bewusst nicht gesetzt

	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)

	s, err := New(ctx, cfg)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	w := unauthenticatedMCPRequest(t, s.Handler())

	challenge := w.Header().Get("WWW-Authenticate")
	want := `Bearer resource_metadata="https://mcp.example.com/.well-known/oauth-protected-resource"`
	if challenge != want {
		t.Errorf("WWW-Authenticate = %q, want %q (unveraendert gegenueber vor dieser Aenderung)", challenge, want)
	}
}

// TestNewAdvertisesScopesSupportedInMetadataDocumentWhenConfigured ist die
// zweite Ankuendigungs-Stelle aus RFC 9728: ein Connector, der das
// Metadatendokument abfragt, bevor er je eine 401-Antwort gesehen hat,
// braucht scopes_supported dort ebenfalls.
func TestNewAdvertisesScopesSupportedInMetadataDocumentWhenConfigured(t *testing.T) {
	cfg, _ := testConfigWithIDPAndRequiredScopes(t, "mcp.access,offline_access")

	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)

	s, err := New(ctx, cfg)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	resp, body := metadataDocumentRequest(t, s.Handler())
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET %s = %d, want %d", wellKnownProtectedResourcePath, resp.StatusCode, http.StatusOK)
	}

	var doc protectedResourceMetadata
	if err := json.Unmarshal(body, &doc); err != nil {
		t.Fatalf("Metadatendokument ist kein gueltiges JSON: %v (%s)", err, body)
	}
	want := []string{"mcp.access", "offline_access"}
	if len(doc.ScopesSupported) != len(want) {
		t.Fatalf("ScopesSupported = %v, want %v", doc.ScopesSupported, want)
	}
	for i, s := range want {
		if doc.ScopesSupported[i] != s {
			t.Errorf("ScopesSupported[%d] = %q, want %q", i, doc.ScopesSupported[i], s)
		}
	}
}

// TestNewOmitsScopesSupportedFieldInMetadataDocumentWhenNotConfigured ist
// die Regression auf der Metadatendokument-Seite: geprueft werden die
// rohen JSON-Bytes auf das Feld, nicht ein unmarshaltes Struct -- ein
// leerer Go-Zero-Wert sieht identisch aus, egal ob das Feld fehlt oder
// leer mitgeschickt wurde. Nur die Bytes auf der Leitung beweisen, dass
// omitempty tatsaechlich gegriffen hat.
func TestNewOmitsScopesSupportedFieldInMetadataDocumentWhenNotConfigured(t *testing.T) {
	cfg := testConfig(t) // MCP_OIDC_REQUIRED_SCOPES bewusst nicht gesetzt

	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)

	s, err := New(ctx, cfg)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	resp, body := metadataDocumentRequest(t, s.Handler())
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET %s = %d, want %d", wellKnownProtectedResourcePath, resp.StatusCode, http.StatusOK)
	}
	if strings.Contains(string(body), "scopes_supported") {
		t.Errorf("Metadatendokument enthaelt scopes_supported trotz nicht gesetztem "+
			"MCP_OIDC_REQUIRED_SCOPES, erwarte das Feld vollstaendig weggelassen: %s", body)
	}
}

// --- MCP_OIDC_ADVERTISED_SCOPES: angekuendigt statt MCP_OIDC_REQUIRED_SCOPES, wenn gesetzt ---
//
// Der Live-Vorfall AADSTS9010010 (oben) wurde durch MCP_OIDC_REQUIRED_SCOPES
// behoben -- fuer Authentik, wo der beim IdP angeforderte Scope-Name
// identisch mit dem im Token ausgestellten scp-Claim ist. Bei Entra sind
// beide Werte NICHT identisch: angefordert werden muss die vollqualifizierte
// Form "https://<host>/mcp/<scope>", waehrend der scp-Claim im
// ausgestellten Token weiterhin nur den kurzen Namen traegt. Ein Connector,
// der MCP_OIDC_REQUIRED_SCOPES als Ankuendigung nimmt, fordert dort den
// falschen (kurzen) Namen an -- Entra weist den Token-Austausch dann mit
// AADSTS650053 zurueck ("scope ... that doesn't exist on the resource
// 'Microsoft Graph'"), bevor dieser Server je erreicht wird.
//
// MCP_OIDC_ADVERTISED_SCOPES trennt die beiden Rollen: wenn gesetzt,
// ersetzt sie AUSSCHLIESSLICH, was VOR dem Token-Austausch angekuendigt
// wird (WWW-Authenticate "scope" und RFC-9728 scopes_supported) --
// MCP_OIDC_REQUIRED_SCOPES bleibt unveraendert das, wogegen scopesSatisfied
// den scp/scope-Claim des bereits ausgestellten Tokens prueft (scopes.go).

// testConfigWithIDPAndScopes ist testConfigWithIDPAndRequiredScopes, setzt
// aber zusaetzlich MCP_OIDC_ADVERTISED_SCOPES -- fuer die Tests, die die
// Trennung zwischen Ankuendigung und Pruefung belegen. advertised == "" laesst
// die Variable bewusst ungesetzt (Fallback-Pfad).
func testConfigWithIDPAndScopes(t *testing.T, required, advertised string) (*config.Config, *testidp.IDP) {
	t.Helper()
	idp := testidp.New(t)

	env := map[string]string{
		"MCP_AUTH_MODE":                  "oidc",
		"MCP_OIDC_PROVIDER":              "generic",
		"MCP_OIDC_ISSUER":                idp.URL(),
		"MCP_OIDC_CLIENT_ID":             "fileee-mcp-server",
		"MCP_OIDC_REQUIRED_SCOPES":       required,
		"MCP_RESOURCE_URL":               "https://mcp.example.com/mcp",
		"MCP_ALLOWED_SUBJECTS":           "abc123",
		"FILEEE_ALLOWED_ORIGIN_PREFIXES": "0.0.0.0/0, ::/0",
		"FILEEE_USERNAME":                "nutzer@example.com",
		"FILEEE_PASSWORD":                "geheim",
	}
	if advertised != "" {
		env["MCP_OIDC_ADVERTISED_SCOPES"] = advertised
	}

	cfg, err := config.LoadConfig(envOf(env))
	if err != nil {
		t.Fatalf("testConfigWithIDPAndScopes: LoadConfig = %v", err)
	}
	return cfg, idp
}

// TestNewAdvertisesTheAdvertisedScopeInsteadOfRequiredScopeInChallenge ist
// die Kern-Regression fuer diese Trennung: MCP_OIDC_REQUIRED_SCOPES traegt
// die kurze, gegen den scp-Claim pruefbare Form, MCP_OIDC_ADVERTISED_SCOPES
// die vollqualifizierte Entra-Form -- beide Werte sind absichtlich
// verschieden, damit ein Fehler, der stattdessen RequiredScopes ankuendigt
// (oder beide vermischt), auffaellt.
func TestNewAdvertisesTheAdvertisedScopeInsteadOfRequiredScopeInChallenge(t *testing.T) {
	cfg, _ := testConfigWithIDPAndScopes(t, "mcp.access", "https://fileee-mcp-entra.strausmann.cloud/mcp/mcp.access")

	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)

	s, err := New(ctx, cfg)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	w := unauthenticatedMCPRequest(t, s.Handler())

	challenge := w.Header().Get("WWW-Authenticate")
	want := `scope="https://fileee-mcp-entra.strausmann.cloud/mcp/mcp.access"`
	if !strings.Contains(challenge, want) {
		t.Errorf("WWW-Authenticate = %q, want it to contain %s", challenge, want)
	}
	if strings.Contains(challenge, `scope="mcp.access"`) {
		t.Errorf("WWW-Authenticate = %q, darf den nackten MCP_OIDC_REQUIRED_SCOPES-Wert nicht mehr "+
			"ankuendigen, sobald MCP_OIDC_ADVERTISED_SCOPES gesetzt ist", challenge)
	}
}

// TestNewAdvertisesTheAdvertisedScopeInsteadOfRequiredScopeInMetadataDocument
// ist die RFC-9728-Haelfte derselben Trennung -- scopes_supported muss
// denselben MCP_OIDC_ADVERTISED_SCOPES-Wert tragen wie die
// WWW-Authenticate-Challenge, nicht MCP_OIDC_REQUIRED_SCOPES.
func TestNewAdvertisesTheAdvertisedScopeInsteadOfRequiredScopeInMetadataDocument(t *testing.T) {
	cfg, _ := testConfigWithIDPAndScopes(t, "mcp.access", "https://fileee-mcp-entra.strausmann.cloud/mcp/mcp.access")

	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)

	s, err := New(ctx, cfg)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	resp, body := metadataDocumentRequest(t, s.Handler())
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET %s = %d, want %d", wellKnownProtectedResourcePath, resp.StatusCode, http.StatusOK)
	}

	var doc protectedResourceMetadata
	if err := json.Unmarshal(body, &doc); err != nil {
		t.Fatalf("Metadatendokument ist kein gueltiges JSON: %v (%s)", err, body)
	}
	want := []string{"https://fileee-mcp-entra.strausmann.cloud/mcp/mcp.access"}
	if !slices.Equal(doc.ScopesSupported, want) {
		t.Errorf("ScopesSupported = %v, want %v", doc.ScopesSupported, want)
	}
}

// TestNewFallsBackToRequiredScopesInChallengeWhenAdvertisedScopesNotConfigured
// ist die Abwaertskompatibilitaets-Regression: jede bestehende Authentik-
// Bereitstellung setzt nur MCP_OIDC_REQUIRED_SCOPES (kurze Namen auf beiden
// Seiten) und muss byte-genau dasselbe WWW-Authenticate sehen wie vor dieser
// Aenderung -- ohne MCP_OIDC_ADVERTISED_SCOPES darf sich am Ankuendigungspfad
// nichts aendern.
func TestNewFallsBackToRequiredScopesInChallengeWhenAdvertisedScopesNotConfigured(t *testing.T) {
	cfg, _ := testConfigWithIDPAndScopes(t, "mcp.access,offline_access", "")

	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)

	s, err := New(ctx, cfg)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	w := unauthenticatedMCPRequest(t, s.Handler())

	challenge := w.Header().Get("WWW-Authenticate")
	if !strings.Contains(challenge, `scope="mcp.access offline_access"`) {
		t.Errorf(`WWW-Authenticate = %q, want scope="mcp.access offline_access" (unveraenderter Fallback `+
			`auf MCP_OIDC_REQUIRED_SCOPES)`, challenge)
	}
}

// TestScopesSatisfiedStillChecksRequiredScopesWhenAdvertisedScopesConfigured
// belegt, dass MCP_OIDC_ADVERTISED_SCOPES die eigentliche Pruefung
// (scopesSatisfied gegen den scp/scope-Claim des Tokens) unberuehrt laesst:
// ein Aufrufer mit einem Token, das den kurzen MCP_OIDC_REQUIRED_SCOPES-Wert
// im scp-Claim traegt, muss weiterhin durchgehen -- selbst wenn
// MCP_OIDC_ADVERTISED_SCOPES einen voellig anderen Wert ankuendigt. Waere
// die Pruefung faelschlich auf ADVERTISED umgestellt worden, wuerde dieser
// Aufruf hier scheitern, weil kein Token je den vollqualifizierten Wert im
// scp-Claim traegt (siehe docs/idp/entra-id.md, "Zu den Scopes").
func TestScopesSatisfiedStillChecksRequiredScopesWhenAdvertisedScopesConfigured(t *testing.T) {
	fileeeMock := newFileeeMock(t)
	cfg, idp := testConfigWithIDPAndScopes(t, "mcp.access", "https://fileee-mcp-entra.strausmann.cloud/mcp/mcp.access")

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

	// Das Token traegt den KURZEN Wert im scp-Claim, wie Entra ihn
	// tatsaechlich ausstellt -- nicht den vollqualifizierten
	// MCP_OIDC_ADVERTISED_SCOPES-Wert.
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
		t.Fatalf("Connect mit dem kurzen scp-Claim-Wert trotz gesetztem MCP_OIDC_ADVERTISED_SCOPES: %v", err)
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
