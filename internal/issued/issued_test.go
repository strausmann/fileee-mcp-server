package issued

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/strausmann/gangway/access"
	"github.com/strausmann/gangway/identity/testidp"
	"github.com/strausmann/gangway/serve"
)

// --- identitaetstragende Kontexte: der echte Mechanismus, keine Attrappe -
//
// Der Auftrag, aus dem diese Datei entstand, nannte den Helfer unten
// serve.ContextWithIdentity. Eine solche exportierte Funktion existiert in
// github.com/strausmann/gangway v0.5.0 nicht (geprueft gegen den
// Modulquellcode: serve/serve.go's vollstaendige Liste exportierter Symbole
// traegt nur IdentityFrom und TokenFrom, nie einen Setter; der
// Kontext-Schluesseltyp (identityKey{}) ist unexportiert). Gangways eigene
// Testsuite dokumentiert bewusst genau das, im Doc-Kommentar von
// TestAuthenticateAllowsValidTokenAndPublishesIdentity
// (serve/serve_test.go): "there is no longer a way to attach a bare
// http.HandlerFunc that reads the context directly" — die Identitaet wird
// ausschliesslich von der Authentifizierungs-Middleware gesetzt, die
// AttachMCP vor einen echten *mcp.Server haengt, und ist nur aus einem von
// dieser Middleware tatsaechlich aufgerufenen Tool-Handler heraus
// beobachtbar.
//
// Dieses Repository dokumentiert dieselbe Einschraenkung von der
// konsumierenden Seite her bereits selbst, in
// internal/tools/read_generic_test.go, und weicht dort aus, indem es
// UNTERHALB der ctx-Grenze testet (interne Helfer bekommen einen bereits
// aufgeloesten Wert statt eines Kontexts uebergeben). Die oeffentliche API
// dieses Pakets ist bewusst ctx-first — Record und Check nehmen beide
// context.Context entgegen, damit Check spaeter direkt in das eigene ctx
// eines Tool-Handlers verdrahtet werden kann (ab Task 3) —, dieses
// Ausweichen ist fuer einen Test der oeffentlichen Oberflaeche also nicht
// verfuegbar: irgendetwas muss mindestens einmal tatsaechlich durch
// Gangways echte Anfrage-Pipeline laufen, um ein ctx zu erzeugen, das
// serve.IdentityFrom erkennt.
//
// ctxMitIdentitaet ist genau dieser echte, minimale Rundlauf: ein
// Wegwerf-Gangway-Server mit einem von testidp ausgestellten Token, ein
// einziges *mcp.Server-Werkzeug ("capture"), dessen einzige Aufgabe es
// ist, das ctx, mit dem es aufgerufen wurde, ueber einen Channel an diesen
// Helfer zurueckzureichen, und ein echter MCP-Client, der es genau einmal
// aufruft. Was zurueckkommt, ist ein echtes ctx, das tatsaechlich durch
// Gangways eigene Authentifizierungs- und Tool-Autorisierungs-Middleware
// gelaufen ist — keine selbstgebaute Attrappe.
//
// Werte aus dem zurueckgegebenen Kontext zu lesen, nachdem die Anfrage, die
// ihn erzeugt hat, abgeschlossen ist, ist sicher: ein Kontext-Abbruch
// betrifft nur Done()/Err(), nie bereits vor dem Abbruch angehaengte
// Values — und der gepufferte Channel unten garantiert, dass das Senden
// abgeschlossen ist, bevor CallTool an diese Funktion zurueckkehrt.
func ctxMitIdentitaet(t *testing.T, subject string) context.Context {
	t.Helper()

	idp := testidp.New(t)
	captured := make(chan context.Context, 1)

	mcpServer := mcp.NewServer(&mcp.Implementation{Name: "issued-test", Version: "0.0.0"}, nil)
	mcp.AddTool(mcpServer, &mcp.Tool{Name: "capture"},
		func(ctx context.Context, _ *mcp.CallToolRequest, _ captureArgs) (*mcp.CallToolResult, any, error) {
			captured <- ctx
			return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: "ok"}}}, nil, nil
		})

	gwCfg := &serve.Config{
		Addr:            "127.0.0.1:0",
		PublicBaseURL:   "https://issued-test.example.invalid",
		IssuerURL:       idp.URL(),
		Audience:        issuedTestAudience,
		SubjectClaim:    "sub",
		AllowedPrefixes: mustPrefixes(t, "127.0.0.1/32", "::1/128"),
	}
	gw, err := serve.New(context.Background(), gwCfg, serve.WithDecider(access.AllowAll()))
	if err != nil {
		t.Fatalf("ctxMitIdentitaet(%q): serve.New: %v", subject, err)
	}
	gw.AttachMCP(mcpServer)

	srv := httptest.NewServer(gw.Handler())
	t.Cleanup(srv.Close)

	token := idp.Token(map[string]any{
		"iss": idp.URL(), "aud": issuedTestAudience, "sub": subject,
		"exp": time.Now().Add(time.Hour).Unix(),
	})
	client := mcp.NewClient(&mcp.Implementation{Name: "issued-test-client", Version: "0.0.0"}, nil)
	transport := &mcp.StreamableClientTransport{
		Endpoint:             srv.URL + "/mcp",
		HTTPClient:           &http.Client{Transport: bearerRoundTripper{token: token}},
		DisableStandaloneSSE: true,
	}
	session, err := client.Connect(context.Background(), transport, nil)
	if err != nil {
		t.Fatalf("ctxMitIdentitaet(%q): Connect: %v", subject, err)
	}
	t.Cleanup(func() { _ = session.Close() })

	res, err := session.CallTool(context.Background(), &mcp.CallToolParams{Name: "capture"})
	if err != nil {
		t.Fatalf("ctxMitIdentitaet(%q): CallTool(capture): %v", subject, err)
	}
	if res.IsError {
		t.Fatalf("ctxMitIdentitaet(%q): CallTool(capture): IsError = true, content = %+v", subject, res.Content)
	}

	return <-captured
}

// issuedTestAudience ist die in dieser Datei durchgehend verwendete
// OIDC-Audience, sowohl beim Bau des Gangway-Servers (serve.Config.Audience)
// als auch beim Ausstellen eines Tokens (idp.Token's "aud"-Claim) — beide
// muessen uebereinstimmen, sonst wuerde ctxMitIdentitaet aus einem Grund an
// der Authentifizierung scheitern, der mit dem, was ein einzelner Test
// tatsaechlich prueft, nichts zu tun hat (spiegelt
// internal/tools/read_test.go's testAudience).
const issuedTestAudience = "fileee-mcp-issued-test"

// captureArgs ist der (leere) Argumenttyp des "capture"-Werkzeugs — es
// nimmt keine Eingabe entgegen, es existiert einzig, um das ctx zu
// beobachten, das Gangways Middleware dem Aufruf mitgegeben hat, der es
// erreicht hat.
type captureArgs struct{}

// bearerRoundTripper injiziert ein festes Bearer-Token in jede ausgehende
// Anfrage — dasselbe Muster, das internal/tools/read_test.go und
// gangway/serve/serve_test.go beide nutzen, um einen echten MCP-Client
// als authentifizierten Aufrufer durch den Streamable-HTTP-Transport zu
// treiben.
type bearerRoundTripper struct{ token string }

func (rt bearerRoundTripper) RoundTrip(r *http.Request) (*http.Response, error) {
	r = r.Clone(r.Context())
	r.Header.Set("Authorization", "Bearer "+rt.token)
	return http.DefaultTransport.RoundTrip(r)
}

// mustPrefixes parst eine feste CIDR-Liste, fuer Faelle, in denen ein
// Parse-Fehler ein Bug in dieser Testdatei selbst waere, nicht im
// geprueften Code (spiegelt internal/tools/read_test.go's eigenes
// mustPrefixes).
func mustPrefixes(t *testing.T, cidrs ...string) []netip.Prefix {
	t.Helper()
	out := make([]netip.Prefix, 0, len(cidrs))
	for _, c := range cidrs {
		p, err := netip.ParsePrefix(c)
		if err != nil {
			t.Fatalf("mustPrefixes(%q): %v", c, err)
		}
		out = append(out, p)
	}
	return out
}

// --- die eigentlichen Store-Tests ------------------------------------------

func TestRecordUndCheckLassenEineAusgelieferteIDDurch(t *testing.T) {
	s := New(30*time.Minute, 1000)
	ctx := ctxMitIdentitaet(t, "alice")

	s.Record(ctx, "doc-1")

	if err := s.Check(ctx, "doc-1"); err != nil {
		t.Fatalf("Check nach Record: %v, want nil", err)
	}
}

func TestCheckLehntEineNieAusgelieferteIDAb(t *testing.T) {
	s := New(30*time.Minute, 1000)
	ctx := ctxMitIdentitaet(t, "alice")

	err := s.Check(ctx, "doc-unbekannt")

	if !errors.Is(err, ErrNotIssued) {
		t.Fatalf("Check ohne Record: %v, want ErrNotIssued", err)
	}
}

// Das ist die eigentliche Sicherheitszusage des Pakets: was eine Identitaet
// gelesen hat, ist fuer eine andere kein gueltiges Ziel.
func TestIdentitaetenSindGetrennt(t *testing.T) {
	s := New(30*time.Minute, 1000)
	s.Record(ctxMitIdentitaet(t, "alice"), "doc-1")

	err := s.Check(ctxMitIdentitaet(t, "bob"), "doc-1")

	if !errors.Is(err, ErrNotIssued) {
		t.Fatalf("Check fuer fremde Identitaet: %v, want ErrNotIssued", err)
	}
}

func TestOhneGeprueteIdentitaetWirdWederAufgenommenNochFreigegeben(t *testing.T) {
	s := New(30*time.Minute, 1000)
	ohne := context.Background()

	s.Record(ohne, "doc-1") // darf nichts merken

	if err := s.Check(ohne, "doc-1"); err == nil {
		t.Fatal("Check ohne Identitaet: nil, want Fehler (fail-closed)")
	}
	// und die ID darf auch fuer eine echte Identitaet nicht gueltig geworden sein
	if err := s.Check(ctxMitIdentitaet(t, "alice"), "doc-1"); !errors.Is(err, ErrNotIssued) {
		t.Fatalf("Check nach identitaetslosem Record: %v, want ErrNotIssued", err)
	}
}

func TestLeereUndIdentischeIDsWerdenSauberBehandelt(t *testing.T) {
	s := New(30*time.Minute, 1000)
	ctx := ctxMitIdentitaet(t, "alice")

	s.Record(ctx, "", "doc-1", "doc-1") // leer wird ignoriert, doppelt ist kein Fehler

	if err := s.Check(ctx, ""); err == nil {
		t.Fatal("Check mit leerer ID: nil, want Fehler")
	}
	if err := s.Check(ctx, "doc-1"); err != nil {
		t.Fatalf("Check nach doppeltem Record: %v, want nil", err)
	}
}

// TestCheckLehntLeereIDAbAuchWennSieImBucketStuende isoliert Checks eigenen
// id == ""-Fruehausstieg von Records Nebenwirkung. Das allein wuerde den
// Fruehausstieg nie beweisen: TestLeereUndIdentischeIDsWerdenSauberBehandelt
// oben prueft nur, dass Check("") einen Fehler liefert — aber Record
// ueberspringt leere ids schon beim Aufnehmen (siehe Records eigenen
// Doc-Kommentar), "" landet ueber die oeffentliche API also nie im Bucket,
// und der Nachschlag in Check haette "" auch ganz ohne den Fruehausstieg
// als "nicht aufgenommen" behandelt. Entfernt man den Fruehausstieg, bleibt
// TestLeereUndIdentischeIDsWerdenSauberBehandelt deshalb gruen — er testet
// eine Nebenwirkung von Record, nicht den Guard in Check (per Gegenprobe
// im Review bestaetigt).
//
// Dieser Test belegt den Fruehausstieg direkt, unabhaengig von Records
// Verhalten: der Bucket wird — als Weisser-Kasten-Test im selben Paket,
// ueber die sonst unexportierten Felder s.byIdent/s.mu — kuenstlich mit
// einem ""-Schluessel vorbelegt. Ohne den Fruehausstieg wuerde Check das
// als Treffer werten und faelschlich nil zurueckgeben; mit ihm bleibt es
// bei ErrNotIssued, unabhaengig davon, was im Bucket steht.
func TestCheckLehntLeereIDAbAuchWennSieImBucketStuende(t *testing.T) {
	s := New(30*time.Minute, 1000)
	ctx := ctxMitIdentitaet(t, "alice")

	subject, ok := subjectOf(ctx)
	if !ok {
		t.Fatal("subjectOf(ctx): ok = false, ctxMitIdentitaet sollte eine echte Identitaet liefern")
	}
	s.mu.Lock()
	s.byIdent[subject] = map[string]time.Time{"": time.Now()}
	s.mu.Unlock()

	if err := s.Check(ctx, ""); !errors.Is(err, ErrNotIssued) {
		t.Fatalf("Check(\"\") trotz kuenstlich vorbelegtem Bucket: %v, want ErrNotIssued", err)
	}
}
