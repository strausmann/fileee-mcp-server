package issued

import (
	"context"
	"errors"
	"fmt"
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

// --- identitätstragende Kontexte: der echte Mechanismus, keine Attrappe -
//
// Der Auftrag, aus dem diese Datei entstand, nannte den Helfer unten
// serve.ContextWithIdentity. Eine solche exportierte Funktion existiert in
// github.com/strausmann/gangway v0.5.0 nicht (geprüft gegen den
// Modulquellcode: serve/serve.go's vollständige Liste exportierter Symbole
// trägt nur IdentityFrom und TokenFrom, nie einen Setter; der
// Kontext-Schlüsseltyp (identityKey{}) ist unexportiert). Gangways eigene
// Testsuite dokumentiert bewusst genau das, im Doc-Kommentar von
// TestAuthenticateAllowsValidTokenAndPublishesIdentity
// (serve/serve_test.go): "there is no longer a way to attach a bare
// http.HandlerFunc that reads the context directly" — die Identität wird
// ausschließlich von der Authentifizierungs-Middleware gesetzt, die
// AttachMCP vor einen echten *mcp.Server hängt, und ist nur aus einem von
// dieser Middleware tatsächlich aufgerufenen Tool-Handler heraus
// beobachtbar.
//
// Dieses Repository dokumentiert dieselbe Einschränkung von der
// konsumierenden Seite her bereits selbst, in
// internal/tools/read_generic_test.go, und weicht dort aus, indem es
// UNTERHALB der ctx-Grenze testet (interne Helfer bekommen einen bereits
// aufgelösten Wert statt eines Kontexts übergeben). Die öffentliche API
// dieses Pakets ist bewusst ctx-first — Record und Check nehmen beide
// context.Context entgegen, damit Check später direkt in das eigene ctx
// eines Tool-Handlers verdrahtet werden kann (ab Task 3) —, dieses
// Ausweichen ist für einen Test der öffentlichen Oberfläche also nicht
// verfügbar: irgendetwas muss mindestens einmal tatsächlich durch
// Gangways echte Anfrage-Pipeline laufen, um ein ctx zu erzeugen, das
// serve.IdentityFrom erkennt.
//
// ctxMitIdentitaet ist genau dieser echte, minimale Rundlauf: ein
// Wegwerf-Gangway-Server mit einem von testidp ausgestellten Token, ein
// einziges *mcp.Server-Werkzeug ("capture"), dessen einzige Aufgabe es
// ist, das ctx, mit dem es aufgerufen wurde, über einen Channel an diesen
// Helfer zurückzureichen, und ein echter MCP-Client, der es genau einmal
// aufruft. Was zurückkommt, ist ein echtes ctx, das tatsächlich durch
// Gangways eigene Authentifizierungs- und Tool-Autorisierungs-Middleware
// gelaufen ist — keine selbstgebaute Attrappe.
//
// Werte aus dem zurückgegebenen Kontext zu lesen, nachdem die Anfrage, die
// ihn erzeugt hat, abgeschlossen ist, ist sicher: ein Kontext-Abbruch
// betrifft nur Done()/Err(), nie bereits vor dem Abbruch angehängte
// Values — und der gepufferte Channel unten garantiert, dass das Senden
// abgeschlossen ist, bevor CallTool an diese Funktion zurückkehrt.
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
// müssen übereinstimmen, sonst würde ctxMitIdentitaet aus einem Grund an
// der Authentifizierung scheitern, der mit dem, was ein einzelner Test
// tatsächlich prüft, nichts zu tun hat (spiegelt
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

// mustPrefixes parst eine feste CIDR-Liste, für Fälle, in denen ein
// Parse-Fehler ein Bug in dieser Testdatei selbst wäre, nicht im
// geprüften Code (spiegelt internal/tools/read_test.go's eigenes
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

// Das ist die eigentliche Sicherheitszusage des Pakets: was eine Identität
// gelesen hat, ist für eine andere kein gültiges Ziel.
func TestIdentitaetenSindGetrennt(t *testing.T) {
	s := New(30*time.Minute, 1000)
	s.Record(ctxMitIdentitaet(t, "alice"), "doc-1")

	err := s.Check(ctxMitIdentitaet(t, "bob"), "doc-1")

	if !errors.Is(err, ErrNotIssued) {
		t.Fatalf("Check für fremde Identität: %v, want ErrNotIssued", err)
	}
}

func TestOhneGeprueteIdentitaetWirdWederAufgenommenNochFreigegeben(t *testing.T) {
	s := New(30*time.Minute, 1000)
	ohne := context.Background()

	s.Record(ohne, "doc-1") // darf nichts merken

	if err := s.Check(ohne, "doc-1"); err == nil {
		t.Fatal("Check ohne Identität: nil, want Fehler (fail-closed)")
	}
	// und die ID darf auch für eine echte Identität nicht gültig geworden sein
	if err := s.Check(ctxMitIdentitaet(t, "alice"), "doc-1"); !errors.Is(err, ErrNotIssued) {
		t.Fatalf("Check nach identitätslosem Record: %v, want ErrNotIssued", err)
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
// id == ""-Frühausstieg von Records Nebenwirkung. Das allein würde den
// Frühausstieg nie beweisen: TestLeereUndIdentischeIDsWerdenSauberBehandelt
// oben prüft nur, dass Check("") einen Fehler liefert — aber Record
// überspringt leere ids schon beim Aufnehmen (siehe Records eigenen
// Doc-Kommentar), "" landet über die öffentliche API also nie im Bucket,
// und der Nachschlag in Check hätte "" auch ganz ohne den Frühausstieg
// als "nicht aufgenommen" behandelt. Entfernt man den Frühausstieg, bleibt
// TestLeereUndIdentischeIDsWerdenSauberBehandelt deshalb grün — er testet
// eine Nebenwirkung von Record, nicht den Guard in Check (per Gegenprobe
// im Review bestätigt).
//
// Dieser Test belegt den Frühausstieg direkt, unabhängig von Records
// Verhalten: der Bucket wird — als Weißer-Kasten-Test im selben Paket,
// über die sonst unexportierten Felder s.byIdent/s.mu — künstlich mit
// einem ""-Schlüssel vorbelegt. Ohne den Frühausstieg würde Check das
// als Treffer werten und fälschlich nil zurückgeben; mit ihm bleibt es
// bei ErrNotIssued, unabhängig davon, was im Bucket steht.
func TestCheckLehntLeereIDAbAuchWennSieImBucketStuende(t *testing.T) {
	s := New(30*time.Minute, 1000)
	ctx := ctxMitIdentitaet(t, "alice")

	subject, ok := subjectOf(ctx)
	if !ok {
		t.Fatal("subjectOf(ctx): ok = false, ctxMitIdentitaet sollte eine echte Identität liefern")
	}
	s.mu.Lock()
	s.byIdent[subject] = map[string]time.Time{"": time.Now()}
	s.mu.Unlock()

	if err := s.Check(ctx, ""); !errors.Is(err, ErrNotIssued) {
		t.Fatalf("Check(\"\") trotz künstlich vorbelegtem Bucket: %v, want ErrNotIssued", err)
	}
}

// --- Verfall und Deckel -----------------------------------------------

// testUhr liefert eine steuerbare Zeit — ohne sie bräuchte jeder
// Verfallstest echtes Warten und wäre zeitabhängig-flaky (dasselbe
// Muster wie der bestehende clientpool-Flake).
type testUhr struct{ jetzt time.Time }

func (u *testUhr) Now() time.Time      { return u.jetzt }
func (u *testUhr) Vor(d time.Duration) { u.jetzt = u.jetzt.Add(d) }

func TestEineIDVerfaelltNachAblaufDerGueltigkeit(t *testing.T) {
	u := &testUhr{jetzt: time.Unix(1000000, 0)}
	s := New(30*time.Minute, 1000)
	s.SetClock(u.Now)
	ctx := ctxMitIdentitaet(t, "alice")

	s.Record(ctx, "doc-1")
	u.Vor(29 * time.Minute)
	if err := s.Check(ctx, "doc-1"); err != nil {
		t.Fatalf("Check nach 29 Minuten: %v, want nil", err)
	}

	u.Vor(2 * time.Minute) // insgesamt 31 Minuten
	if err := s.Check(ctx, "doc-1"); !errors.Is(err, ErrNotIssued) {
		t.Fatalf("Check nach 31 Minuten: %v, want ErrNotIssued", err)
	}
}

func TestDerDeckelVerdraengtDieAeltestenEintraege(t *testing.T) {
	u := &testUhr{jetzt: time.Unix(1000000, 0)}
	s := New(30*time.Minute, 3)
	s.SetClock(u.Now)
	ctx := ctxMitIdentitaet(t, "alice")

	for _, id := range []string{"a", "b", "c"} {
		s.Record(ctx, id)
		u.Vor(time.Second) // klare Reihenfolge der Aufnahmezeiten
	}
	s.Record(ctx, "d") // sprengt den Deckel von 3

	if err := s.Check(ctx, "a"); !errors.Is(err, ErrNotIssued) {
		t.Fatalf("älteste ID nach Überlauf: %v, want ErrNotIssued", err)
	}
	for _, id := range []string{"b", "c", "d"} {
		if err := s.Check(ctx, id); err != nil {
			t.Fatalf("ID %q nach Überlauf: %v, want nil", id, err)
		}
	}
}

func TestDerDeckelGiltJeIdentitaetNichtGlobal(t *testing.T) {
	s := New(30*time.Minute, 2)
	a, b := ctxMitIdentitaet(t, "alice"), ctxMitIdentitaet(t, "bob")

	s.Record(a, "a1", "a2")
	s.Record(b, "b1", "b2") // darf alices Einträge nicht verdrängen

	for _, id := range []string{"a1", "a2"} {
		if err := s.Check(a, id); err != nil {
			t.Fatalf("alices %q nach bobs Aufnahmen: %v, want nil", id, err)
		}
	}
}

// TestMaxPerIdentityKleinerGleichNullMerktSichNichts belegt effectiveMax's
// Klammerung (Store.maxPerIdentitys eigener Doc-Kommentar): ein
// maxPerIdentity <= 0 wird als reale Grenze durchgesetzt, nicht als
// "unbegrenzt" gelesen. Die beiden von TestDerDeckelVerdraengtDieAeltestenEintraege
// & Co. geprüften Werte (3, 2, 1000) erreichen effectiveMax's
// negativen Zweig nie — dieser Test deckt ihn gezielt ab, für 0 und
// einen negativen Wert.
func TestMaxPerIdentityKleinerGleichNullMerktSichNichts(t *testing.T) {
	for _, max := range []int{0, -1} {
		t.Run(fmt.Sprintf("max=%d", max), func(t *testing.T) {
			s := New(30*time.Minute, max)
			ctx := ctxMitIdentitaet(t, "alice")

			s.Record(ctx, "doc-1")

			if err := s.Check(ctx, "doc-1"); !errors.Is(err, ErrNotIssued) {
				t.Fatalf("Check nach Record mit maxPerIdentity=%d: %v, want ErrNotIssued", max, err)
			}
		})
	}
}

// TestTtlKleinerGleichNullIstSofortVerfallen belegt isExpired's
// Vorab-Guard (Store.ttls eigener Doc-Kommentar): ein ttl <= 0 gilt als
// sofort verfallen, auch wenn Record und Check im selben Moment laufen
// (s.now().Sub(recorded) wäre dann 0, "0 > 0" allein wäre false — genau
// die Lücke, die der Guard schließt). Die von
// TestEineIDVerfaelltNachAblaufDerGueltigkeit geprüfte Ttl (30 Minuten)
// erreicht isExpired's Vorab-Guard nie — dieser Test deckt ihn gezielt ab,
// für 0 und einen negativen Wert, ohne die Uhr überhaupt vorzurücken.
func TestTtlKleinerGleichNullIstSofortVerfallen(t *testing.T) {
	for _, ttl := range []time.Duration{0, -time.Minute} {
		t.Run(fmt.Sprintf("ttl=%s", ttl), func(t *testing.T) {
			u := &testUhr{jetzt: time.Unix(1000000, 0)}
			s := New(ttl, 1000)
			s.SetClock(u.Now)
			ctx := ctxMitIdentitaet(t, "alice")

			s.Record(ctx, "doc-1") // gleicher Zeitpunkt, Uhr wird nicht vorgerückt

			if err := s.Check(ctx, "doc-1"); !errors.Is(err, ErrNotIssued) {
				t.Fatalf("Check im selben Moment mit ttl=%s: %v, want ErrNotIssued", ttl, err)
			}
		})
	}
}

// TestGrenzfallGenauBeiTtlIstNochGueltig nagelt die von isExpired
// getroffene Wahl am exakten Übergang fest (siehe dessen eigenen
// Doc-Kommentar für die Begründung): eine ID bleibt gültig, solange ihr
// Alter höchstens ttl beträgt — Alter == ttl (auf die Nanosekunde genau)
// ist noch gültig, Alter == ttl + 1ns ist verfallen.
//
// Ohne diesen Test bleibt der eigentliche Entscheidungspunkt ungeprüft:
// TestEineIDVerfaelltNachAblaufDerGueltigkeit prüft nur 29 und 31 Minuten
// bei einer Ttl von 30 Minuten — beide liegen klar auf einer Seite des
// Übergangs. Vertauscht man in isExpired "s.now().Sub(recorded) > s.ttl"
// gegen ">=", bleiben alle bis hierhin existierenden Tests unverändert
// grün (Review-Befund) — erst dieser Test trifft den Übergang selbst.
// Per Gegenprobe bestätigt (">" gegen ">=" im Quellcode getauscht,
// dieser Test färbt dann rot, siehe Task-2-Report für den Lauf).
func TestGrenzfallGenauBeiTtlIstNochGueltig(t *testing.T) {
	u := &testUhr{jetzt: time.Unix(1000000, 0)}
	ttl := 30 * time.Minute
	s := New(ttl, 1000)
	s.SetClock(u.Now)
	ctx := ctxMitIdentitaet(t, "alice")

	s.Record(ctx, "doc-1")

	u.Vor(ttl) // Alter == ttl, exakt auf die Nanosekunde
	if err := s.Check(ctx, "doc-1"); err != nil {
		t.Fatalf("Check bei Alter == ttl: %v, want nil (noch gültig)", err)
	}

	u.Vor(time.Nanosecond) // Alter == ttl + 1ns
	if err := s.Check(ctx, "doc-1"); !errors.Is(err, ErrNotIssued) {
		t.Fatalf("Check bei Alter == ttl + 1ns: %v, want ErrNotIssued (verfallen)", err)
	}
}
