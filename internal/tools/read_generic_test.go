// White-box tests for read_generic.go's registerReadService helper and the
// two failure paths its handlers must surface correctly — a backend error
// from Query, and an empty ID rejected before Get ever touches the
// network. Both failure-path tests exercise the client-resolution-free
// halves of the handlers directly (listFromService, genericGetHandler)
// rather than driving a full gangway/HTTP round trip: clientFor's
// serve.IdentityFrom(ctx) can only ever return ok when the context came
// through gangway's own (unexported) identity wiring, which this package
// has no way to fabricate — see clientFor's own doc comment in read.go.
//
// This file also carries the registerReadService-panics-on-a-leaking-
// descriptor tests below tagDescriptor: mustNotLeakUntrustedLine
// (read_generic.go) runs automatically inside registerReadService, so
// every descriptor's own registration test (the pattern
// TestRegisterReadServiceMeldetListeUndDetailAn establishes) already
// exercises it — there is no separate helper Aufgabe 3 onward needs to
// remember to call.
package tools

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/strausmann/gangway/access"
	"github.com/strausmann/gangway/identity/testidp"
	"github.com/strausmann/gangway/serve"
	"github.com/strausmann/go-fileee/fileee"

	"github.com/strausmann/fileee-mcp-server/internal/clientpool"
	"github.com/strausmann/fileee-mcp-server/internal/issued"
)

// discardLogger builds a *slog.Logger that discards everything it is
// given — for the many tests in this file and in read_sync_test.go that
// pass one into registerReadService/registerSync/genericListHandler/
// genericGetHandler/genericSyncHandler but do not themselves assert on
// what it logs (that is read_generic_sync_diag_test.go's own job). Never
// nil: RegisterAll's own doc comment (read.go) requires a non-nil
// logger, and the same requirement carries down to every function this
// package threads it through.
func discardLogger() *slog.Logger {
	return slog.New(slog.DiscardHandler)
}

// tagSummary is this file's stand-in for a real service's summary struct
// (Aufgabe 3/4 define one per service) — just enough fields to prove
// Summarize's typed return value actually reaches genericListOutput /
// genericGetOutput and survives a tools/list round trip's schema
// derivation.
type tagSummary struct {
	ID string `json:"id"`
}

// descriptionFixture stands in for a real tool description in tests that
// never call descriptions_test.go's length check (that check runs against
// registeredReadTools(), i.e. only tools RegisterAll itself mounts —
// nothing in this file does) — kept realistically long anyway so a test
// failure here is never mistaken for that unrelated check.
const descriptionFixture = "Beschreibungstext lang genug fuer die Pruefung, mindestens hundertzwanzig Zeichen, damit der Beschreibungstest nicht anschlaegt."

func tagDescriptor() readServiceDescriptor[fileee.Tag, tagSummary] {
	return readServiceDescriptor[fileee.Tag, tagSummary]{
		ListName:        "list_tags",
		GetName:         "get_tag",
		ListDescription: descriptionFixture,
		GetDescription:  descriptionFixture,
		Service:         func(c *fileee.Client) fileee.ReadService[fileee.Tag] { return c.Tags },
		Summarize:       func(tag *fileee.Tag) tagSummary { return tagSummary{ID: tag.ID} },
		UntrustedLine:   func(tag *fileee.Tag) string { return tag.Name },
		PoisonProbe:     func(marker string) *fileee.Tag { return &fileee.Tag{ID: "t1", Name: marker} },
		// IDOf ist seit Aufgabe 4 Pflichtfeld (readServiceDescriptor.IDOf,
		// read_generic.go) — getFromService/listFromService rufen es
		// ungeprüft auf, ein nil-Wert würde jeden Erfolgsfall-Test dieser
		// Datei mit einer Nil-Pointer-Panik abbrechen.
		IDOf: func(tag *fileee.Tag) string { return tag.ID },
	}
}

func TestRegisterReadServiceMeldetListeUndDetailAn(t *testing.T) {
	s := mcp.NewServer(&mcp.Implementation{Name: "probe", Version: "0"}, nil)

	registerReadService(s, (*clientpool.Pool)(nil), discardLogger(), tagDescriptor(), nil)

	names := toolNamesOf(t, s)
	if !names["list_tags"] {
		t.Error("list_tags wurde nicht angemeldet")
	}
	if !names["get_tag"] {
		t.Error("get_tag wurde nicht angemeldet")
	}
}

// toolNamesOf verbindet einen echten MCP-Client ueber eine
// In-Memory-Transportstrecke mit s und liest tools/list — derselbe Weg wie
// names.go's registeredReadTools() und internal/server/server_test.go's
// toolNamesOf, hier lokal dupliziert: go-sdk v1.7.0's *mcp.Server haelt
// seine angemeldeten Werkzeuge in einer unexportierten featureSet, es gibt
// kein "s.Tools()".
func toolNamesOf(t *testing.T, s *mcp.Server) map[string]bool {
	t.Helper()
	ctx := context.Background()

	clientTransport, serverTransport := mcp.NewInMemoryTransports()
	serverSession, err := s.Connect(ctx, serverTransport, nil)
	if err != nil {
		t.Fatalf("Server.Connect: %v", err)
	}
	defer func() { _ = serverSession.Close() }()

	client := mcp.NewClient(&mcp.Implementation{Name: "read-generic-probe", Version: "0"}, nil)
	clientSession, err := client.Connect(ctx, clientTransport, nil)
	if err != nil {
		t.Fatalf("Client.Connect: %v", err)
	}
	defer func() { _ = clientSession.Close() }()

	res, err := clientSession.ListTools(ctx, nil)
	if err != nil {
		t.Fatalf("ListTools: %v", err)
	}
	names := make(map[string]bool, len(res.Tools))
	for _, tool := range res.Tools {
		names[tool.Name] = true
	}
	return names
}

// fakeReadService steht fuer fileee.ReadService[T] in Tests, die die
// Fehlerpfade von listFromService/genericGetHandler pruefen — ohne
// Mock-HTTP-Server, ohne Login-Handshake: readServiceDescriptor.Service
// entscheidet, welche Implementierung der echte Handler bekommt, und ein
// Test kann dort schlicht diese Attrappe eintragen.
// diffErr/diffResult (added for read_sync_test.go's own error-path and
// happy-path tests — this fixture is shared across both files, same
// package) follow queryErr/getErr's own pattern: nil means "use the zero
// value", set means "return this instead". Existing callers that never
// set them are unaffected.
// queryResult/getResult (added for Aufgabe 3b's own Erfolgsfall-Tests of
// listFromService/getFromService — read_generic.go's Kernpfad, bisher nur
// ueber den leeren Standardwert erreicht) follow the exact same
// nil-means-zero-value, set-means-return-this pattern as diffResult — a
// fourth field on the same fixture, not a second fake type.
type fakeReadService[T any] struct {
	queryErr    error
	queryResult *fileee.QueryResult[T]
	getErr      error
	getResult   *T
	diffErr     error
	diffResult  *fileee.DiffResult[T]
}

func (f *fakeReadService[T]) Query(context.Context, fileee.QueryOptions) (*fileee.QueryResult[T], error) {
	if f.queryErr != nil {
		return nil, f.queryErr
	}
	if f.queryResult != nil {
		return f.queryResult, nil
	}
	return &fileee.QueryResult[T]{}, nil
}

func (f *fakeReadService[T]) Diff(context.Context, fileee.Cursor) (*fileee.DiffResult[T], error) {
	if f.diffErr != nil {
		return nil, f.diffErr
	}
	if f.diffResult != nil {
		return f.diffResult, nil
	}
	return &fileee.DiffResult[T]{}, nil
}

func (f *fakeReadService[T]) Get(context.Context, string) (*T, error) {
	if f.getErr != nil {
		return nil, f.getErr
	}
	if f.getResult != nil {
		return f.getResult, nil
	}
	var zero T
	return &zero, nil
}

func TestListFromServiceWickeltEinenGegenseitenFehlerMitDemWerkzeugnamenEin(t *testing.T) {
	backendErr := errors.New("Gegenseite antwortet nicht")
	d := tagDescriptor()
	service := &fakeReadService[fileee.Tag]{queryErr: backendErr}

	_, _, err := listFromService(context.Background(), d, service, genericListInput{})
	if err == nil {
		t.Fatal("erwarteter Fehler blieb aus")
	}
	if !errors.Is(err, backendErr) {
		t.Errorf("Fehler wickelt %v nicht ein, bekam: %v", backendErr, err)
	}
	if !strings.Contains(err.Error(), d.ListName) {
		t.Errorf("Fehlermeldung %q enthaelt nicht den Werkzeugnamen %q", err.Error(), d.ListName)
	}
}

func TestGenericGetHandlerLehntEineLeereKennungOhneNetzwerkzugriffAb(t *testing.T) {
	d := tagDescriptor()
	// p bleibt nil: erreicht der Handler clientFor doch noch, bricht der
	// Test mit einer Nil-Pointer-Dereferenzierung ab statt still zu
	// bestehen — das ist der Beleg, dass die leere Kennung VOR jedem
	// Netzwerkzugriff abgefangen wird.
	handler := genericGetHandler[fileee.Tag, tagSummary](nil, discardLogger(), d, nil)

	_, _, err := handler(context.Background(), nil, genericGetInput{ID: "   "})
	if err == nil {
		t.Fatal("erwarteter Fehler blieb aus")
	}
	if !strings.Contains(err.Error(), d.GetName) {
		t.Errorf("Fehlermeldung %q enthaelt nicht den Werkzeugnamen %q", err.Error(), d.GetName)
	}
}

// TestGetFromServiceLiefertZusammenfassungUndGerahmtenFremdtextBeiErfolg ist
// getFromService's eigener Erfolgsfall — bislang lief kein Test je durch
// diese Funktion mit einem echten Treffer, nur der Leerkennung-Test oben
// (der getFromService gar nicht erreicht, weil er schon vorher abbricht)
// und die Registrierungstests weiter unten (die nie einen Handler
// aufrufen). fakeReadService.getResult liefert dafuer einen echten
// fileee.Tag zurueck, dessen Name (das per tagDescriptor() gesetzte
// UntrustedLine-Feld) ein unvorhersagbarer Erkennungswert ist —
// newUntrustedBoundary (read.go), derselbe Mechanismus, den
// mustNotLeakUntrustedLine fuer ihre eigene Registrierungspruefung nutzt
// (siehe deren Kommentar in read_generic.go), hier zweckentfremdet als
// kollisionsfreier Testwert statt als Sicherheitsmarker: er kann per
// Konstruktion nicht zufaellig mit "t1" (der Struktur-ID) kollidieren.
func TestGetFromServiceLiefertZusammenfassungUndGerahmtenFremdtextBeiErfolg(t *testing.T) {
	marker, err := newUntrustedBoundary()
	if err != nil {
		t.Fatalf("newUntrustedBoundary: %v", err)
	}
	d := tagDescriptor()
	service := &fakeReadService[fileee.Tag]{getResult: &fileee.Tag{ID: "t1", Name: marker}}

	result, out, err := getFromService(context.Background(), d, service, "t1", nil)
	if err != nil {
		t.Fatalf("getFromService: %v", err)
	}

	// Struktur-Teil: die Zusammenfassung kommt typisiert zurueck ...
	if out.Entry.ID != "t1" {
		t.Errorf("Entry.ID = %q, want %q", out.Entry.ID, "t1")
	}
	// ... und der fremdbestimmte Text steht NICHT darin — dieselbe Pruefung,
	// die mustNotLeakUntrustedLine bei der Registrierung einmalig macht,
	// hier gegen den tatsaechlichen Handler-Output eines echten Aufrufs.
	for _, v := range summaryFieldValues(out.Entry) {
		if strings.Contains(v, marker) {
			t.Errorf("Struktur-Teil enthaelt den fremdbestimmten Text: Feldwert %q enthaelt den Erkennungswert", v)
		}
	}

	// Text-Teil: der fremdbestimmte Text erscheint gerahmt im Textinhalt.
	if len(result.Content) != 1 {
		t.Fatalf("Content hat %d Eintraege, want 1", len(result.Content))
	}
	text, ok := result.Content[0].(*mcp.TextContent)
	if !ok {
		t.Fatalf("Content[0] ist %T, want *mcp.TextContent", result.Content[0])
	}
	if !strings.Contains(text.Text, marker) {
		t.Errorf("Textinhalt enthaelt nicht den fremdbestimmten Text (Erkennungswert fehlt): %q", text.Text)
	}
	if !strings.Contains(text.Text, "<untrusted_external_content") {
		t.Errorf("Textinhalt ist nicht gerahmt (kein <untrusted_external_content>-Tag): %q", text.Text)
	}
}

// TestGetFromServiceWickeltEinenGegenseitenFehlerMitDemWerkzeugnamenEin ist
// getFromService's Gegenstueck zu TestListFromServiceWickelt... oben — bis
// hierhin gab es fuer getFromService nur den Leerkennungs-Test, der Get nie
// erreicht; ein Fehler von service.Get selbst lief in keinem Test.
func TestGetFromServiceWickeltEinenGegenseitenFehlerMitDemWerkzeugnamenEin(t *testing.T) {
	backendErr := errors.New("Gegenseite antwortet nicht")
	d := tagDescriptor()
	service := &fakeReadService[fileee.Tag]{getErr: backendErr}

	_, _, err := getFromService(context.Background(), d, service, "t1", nil)
	if err == nil {
		t.Fatal("erwarteter Fehler blieb aus")
	}
	if !errors.Is(err, backendErr) {
		t.Errorf("Fehler wickelt %v nicht ein, bekam: %v", backendErr, err)
	}
	if !strings.Contains(err.Error(), d.GetName) {
		t.Errorf("Fehlermeldung %q enthaelt nicht den Werkzeugnamen %q", err.Error(), d.GetName)
	}
}

// TestListFromServiceLiefertMehrereEintraegeUndSammeltGerahmtenFremdtext ist
// listFromService's eigener Erfolgsfall mit mehr als einer Zeile — der
// einzige bisherige Test dieser Funktion (TestListFromServiceWickelt... oben)
// deckt nur den Fehlerpfad ab; der Erfolgsfall lief bislang nie durch, immer
// nur gegen fakeReadService's leeren Standardwert (kein Row, kein Text).
func TestListFromServiceLiefertMehrereEintraegeUndSammeltGerahmtenFremdtext(t *testing.T) {
	marker1, err := newUntrustedBoundary()
	if err != nil {
		t.Fatalf("newUntrustedBoundary: %v", err)
	}
	marker2, err := newUntrustedBoundary()
	if err != nil {
		t.Fatalf("newUntrustedBoundary: %v", err)
	}
	d := tagDescriptor()
	service := &fakeReadService[fileee.Tag]{
		queryResult: &fileee.QueryResult[fileee.Tag]{
			Rows:      []fileee.Tag{{ID: "t1", Name: marker1}, {ID: "t2", Name: marker2}},
			TotalRows: 2,
		},
	}

	result, out, err := listFromService(context.Background(), d, service, genericListInput{})
	if err != nil {
		t.Fatalf("listFromService: %v", err)
	}

	// Alle Zeilen erscheinen, in der von der Gegenseite gelieferten Reihenfolge.
	if len(out.Entries) != 2 {
		t.Fatalf("Entries hat %d Eintraege, want 2", len(out.Entries))
	}
	if out.Entries[0].ID != "t1" || out.Entries[1].ID != "t2" {
		t.Errorf("Entries = %+v, want IDs t1, t2 in dieser Reihenfolge", out.Entries)
	}
	if out.TotalRows != 2 {
		t.Errorf("TotalRows = %d, want 2", out.TotalRows)
	}

	// Beide gerahmten Texte werden gesammelt und gemeinsam ausgeliefert.
	if len(result.Content) != 1 {
		t.Fatalf("Content hat %d Eintraege, want 1", len(result.Content))
	}
	text, ok := result.Content[0].(*mcp.TextContent)
	if !ok {
		t.Fatalf("Content[0] ist %T, want *mcp.TextContent", result.Content[0])
	}
	if !strings.Contains(text.Text, marker1) || !strings.Contains(text.Text, marker2) {
		t.Errorf("Textinhalt enthaelt nicht beide fremdbestimmten Zeilen: %q", text.Text)
	}
	if !strings.Contains(text.Text, "<untrusted_external_content") {
		t.Errorf("Textinhalt ist nicht gerahmt (kein <untrusted_external_content>-Tag): %q", text.Text)
	}
}

// TestUntrustedLineOfLiefertLeereZeichenketteOhneUntrustedLine ist
// untrustedLineOf's eigener Nil-Zweig. Die drei Erfolgsfall-Tests oben
// rufen listFromService/getFromService ausschliesslich mit tagDescriptor()
// auf, deren UntrustedLine gesetzt ist — sie decken also nur den
// GESETZTEN Zweig ab. Der Nil-Zweig (dieser Typ traegt nie Fremdtext,
// siehe UntrustedLine's eigener Kommentar auf readServiceDescriptor) lief
// bislang durch keinen tatsaechlichen Aufruf: die Registrierungstests
// weiter unten pruefen nur, DASS ein UntrustedLine-loser Deskriptor sauber
// anmeldet (TestRegisterReadServiceStartetSauberOhneFremdbestimmtenText),
// nie, was ein Aufruf gegen ihn zurueckgibt.
func TestUntrustedLineOfLiefertLeereZeichenketteOhneUntrustedLine(t *testing.T) {
	d := readServiceDescriptor[fileee.Tag, tagSummary]{
		Summarize: func(tag *fileee.Tag) tagSummary { return tagSummary{ID: tag.ID} },
		// UntrustedLine bewusst nicht gesetzt.
	}
	if line := untrustedLineOf(d, &fileee.Tag{ID: "t1"}); line != "" {
		t.Errorf("untrustedLineOf ohne UntrustedLine = %q, want leere Zeichenkette", line)
	}
}

// --- mustNotLeakUntrustedLine: erzwungen ueber registerReadService selbst ---
//
// Zweite Korrekturrunde: die erste Fassung (leaksUntrustedLine, exakter
// String-Vergleich) hat einen zusammengesetzten Fremdtext uebersehen —
// UntrustedLine "Max " + Nachname, Summarize liefert nur den Nachnamen.
// Das ist fremdbestimmter Text, aber nicht Wort-fuer-Wort identisch mit
// der ganzen Zeile, und rutschte deshalb durch. Ersetzt durch
// mustNotLeakUntrustedLine (read_generic.go): ein von PoisonProbe erzeugter
// Erkennungswert wird in Summarize's Feldern per Enthaltensein gesucht —
// erfasst auch Teilfelder — und ist als zufaelliger, unvorhersagbarer Wert
// (newUntrustedBoundary, read.go) per Konstruktion kollisionsfrei mit
// echten Daten. Erzwungen ueber registerReadService selbst (Panic bei
// fehlendem/fehlerhaftem PoisonProbe oder gefundenem Leck), nicht ueber
// eine gesondert zu erinnernde Testfunktion — die Tests unten pruefen
// diesen Mechanismus, rufen aber nichts Eigenes mehr auf.

func TestRegisterReadServicePanictBeiEinemDeskriptorDerDenGerahmtenTextMitliefert(t *testing.T) {
	type leakySummary struct {
		ID   string
		Note string // Fehler unter Test: spiegelt UntrustedLine's Quelle 1:1
	}
	d := readServiceDescriptor[fileee.Tag, leakySummary]{
		ListName:        "list_leaky",
		GetName:         "get_leaky",
		ListDescription: descriptionFixture,
		GetDescription:  descriptionFixture,
		Service:         func(c *fileee.Client) fileee.ReadService[fileee.Tag] { return c.Tags },
		Summarize:       func(tag *fileee.Tag) leakySummary { return leakySummary{ID: tag.ID, Note: tag.Name} },
		UntrustedLine:   func(tag *fileee.Tag) string { return tag.Name },
		PoisonProbe:     func(marker string) *fileee.Tag { return &fileee.Tag{ID: "t1", Name: marker} },
	}

	defer func() {
		if r := recover(); r == nil {
			t.Error("erwartete Panic (Summarize liefert den gerahmten Text 1:1 mit) blieb aus")
		}
	}()
	s := mcp.NewServer(&mcp.Implementation{Name: "probe", Version: "0"}, nil)
	registerReadService(s, (*clientpool.Pool)(nil), discardLogger(), d, nil)
}

// TestRegisterReadServicePanictBeiZusammengesetztemFremdtextInEinemTeilfeld
// ist der vom Team-Lead/Pruefer konkret benannte Fall: UntrustedLine setzt
// sich aus mehreren Teilen zusammen ("Max " + Nachname), Summarize gibt
// nur EIN Teilfeld (den Nachnamen) zurueck — vollstaendig fremdbestimmter
// Text, aber nie Wort-fuer-Wort identisch mit der ganzen Zeile. Die erste
// Fassung dieses Mechanismus (exakter Vergleich) haette das NICHT erkannt.
func TestRegisterReadServicePanictBeiZusammengesetztemFremdtextInEinemTeilfeld(t *testing.T) {
	type contactLike struct{ LastName string }
	type contactSummary struct {
		LastName string // Fehler unter Test: taucht auch in UntrustedLine auf
	}

	d := readServiceDescriptor[contactLike, contactSummary]{
		ListName:        "list_contactlike",
		GetName:         "get_contactlike",
		ListDescription: descriptionFixture,
		GetDescription:  descriptionFixture,
		Service:         func(*fileee.Client) fileee.ReadService[contactLike] { return &fakeReadService[contactLike]{} },
		Summarize:       func(c *contactLike) contactSummary { return contactSummary{LastName: c.LastName} },
		UntrustedLine:   func(c *contactLike) string { return "Max " + c.LastName },
		PoisonProbe:     func(marker string) *contactLike { return &contactLike{LastName: marker} },
	}

	defer func() {
		if r := recover(); r == nil {
			t.Error("erwartete Panic (zusammengesetzter Fremdtext, nur ein Teilfeld leckt) blieb aus")
		}
	}()
	s := mcp.NewServer(&mcp.Implementation{Name: "probe", Version: "0"}, nil)
	registerReadService(s, (*clientpool.Pool)(nil), discardLogger(), d, nil)
}

func TestRegisterReadServicePanictWennPoisonProbeFehlt(t *testing.T) {
	d := tagDescriptor()
	d.PoisonProbe = nil

	defer func() {
		if r := recover(); r == nil {
			t.Error("erwartete Panic (PoisonProbe fehlt) blieb aus")
		}
	}()
	s := mcp.NewServer(&mcp.Implementation{Name: "probe", Version: "0"}, nil)
	registerReadService(s, (*clientpool.Pool)(nil), discardLogger(), d, nil)
}

// TestRegisterReadServicePanictWennPoisonProbeDasFalscheFeldSetzt ist die
// vom Team-Lead angemahnte Gegenprobe: setzt PoisonProbe ein anderes Feld
// als das, welches UntrustedLine tatsaechlich liest, prueft der Test
// nichts Sinnvolles mehr — mustNotLeakUntrustedLine muss das selbst
// erkennen (Erkennungswert erreicht UntrustedLine's Ausgabe nicht) und
// paniken, statt eine bedeutungslose "kein Leck gefunden"-Bewertung
// durchzulassen.
func TestRegisterReadServicePanictWennPoisonProbeDasFalscheFeldSetzt(t *testing.T) {
	d := readServiceDescriptor[fileee.Tag, tagSummary]{
		ListName:        "list_tags_wrongfield",
		GetName:         "get_tag_wrongfield",
		ListDescription: descriptionFixture,
		GetDescription:  descriptionFixture,
		Service:         func(c *fileee.Client) fileee.ReadService[fileee.Tag] { return c.Tags },
		Summarize:       func(tag *fileee.Tag) tagSummary { return tagSummary{ID: tag.ID} },
		UntrustedLine:   func(tag *fileee.Tag) string { return tag.Name },
		PoisonProbe:     func(marker string) *fileee.Tag { return &fileee.Tag{ID: marker} }, // FALSCH: setzt ID statt Name
	}

	defer func() {
		if r := recover(); r == nil {
			t.Error("erwartete Panic (PoisonProbe setzt das falsche Feld, Erkennungswert erreicht UntrustedLine nicht) blieb aus")
		}
	}()
	s := mcp.NewServer(&mcp.Implementation{Name: "probe", Version: "0"}, nil)
	registerReadService(s, (*clientpool.Pool)(nil), discardLogger(), d, nil)
}

// TestRegisterReadServicePanictNichtBeiUnabhaengigenLegitimenFeldern ist
// der Fehlalarm-Gegenversuch aus BEIDEN Korrekturrunden, gegen den neuen
// Mechanismus wiederholt: ein legitimes Feld (ID) enthaelt zufaellig das
// Wort "Rechnung", der fremdbestimmte Name ist ebenfalls "Rechnung" —
// keine Kollision, weil mustNotLeakUntrustedLine gegen einen zufaelligen,
// 128-Bit-Erkennungswert prueft, nie gegen den echten Namen. Ein zufaellig
// gemeinsames Wort zwischen zwei unabhaengigen Feldern kann diesen
// Erkennungswert nicht treffen.
func TestRegisterReadServicePanictNichtBeiUnabhaengigenLegitimenFeldern(t *testing.T) {
	d := readServiceDescriptor[fileee.Tag, tagSummary]{
		ListName:        "list_tags_independent",
		GetName:         "get_tag_independent",
		ListDescription: descriptionFixture,
		GetDescription:  descriptionFixture,
		Service:         func(c *fileee.Client) fileee.ReadService[fileee.Tag] { return c.Tags },
		Summarize:       func(*fileee.Tag) tagSummary { return tagSummary{ID: "Rechnung-2026-001"} },
		UntrustedLine:   func(tag *fileee.Tag) string { return tag.Name },
		PoisonProbe:     func(marker string) *fileee.Tag { return &fileee.Tag{ID: "Rechnung-2026-001", Name: marker} },
		// IDOf ist seit mustHaveIDOf (read_generic.go) Pflichtfeld -- dieser Test prueft
		// eine andere Sache (mustNotLeakUntrustedLine's Fehlalarm-Freiheit) und darf deshalb
		// nicht an der NEUEN Pruefung scheitern.
		IDOf: func(tag *fileee.Tag) string { return tag.ID },
	}

	s := mcp.NewServer(&mcp.Implementation{Name: "probe", Version: "0"}, nil)
	registerReadService(s, (*clientpool.Pool)(nil), discardLogger(), d, nil) // darf NICHT paniken
}

// --- 3. Korrekturrunde: UntrustedLine optional, fuer Typen ohne Fremdtext ---
//
// Aufgabe 3 (Schlagworte, Firmen, Dokumenttypen, Schemata) hat KEINEN
// fremdbestimmten Text — diese Namen waehlt der Kontoinhaber selbst, laut
// Entwurf. Kein PoisonProbe kann fuer so einen Deskriptor je die
// Selbstpruefung erfuellen, weil UntrustedLine die Entitaet gar nicht
// ansieht. UntrustedLine ist deshalb optional (nil): nil bedeutet "dieser
// Typ traegt nie Fremdtext", ueberspringt die Pruefung UND die
// PoisonProbe-Pflicht vollstaendig. Siehe readServiceDescriptor's eigener
// Kommentar (read_generic.go) fuer die Abgrenzung zu "UntrustedLine ist
// gesetzt, liefert aber fuer DIESE Entitaet leeren Text".

// TestRegisterReadServiceStartetSauberOhneFremdbestimmtenText ist der vom
// Team-Lead/Pruefer konkret geforderte erste Abnahme-Fall: ein Deskriptor
// nach Aufgabe-3-Muster (UntrustedLine nil, PoisonProbe nil) MUSS sauber
// anmelden koennen — sonst kaeme der Server beim Start nicht hoch, sobald
// Aufgabe 3 diese vier Dienste verdrahtet.
func TestRegisterReadServiceStartetSauberOhneFremdbestimmtenText(t *testing.T) {
	d := readServiceDescriptor[fileee.Tag, tagSummary]{
		ListName:        "list_tags_notrusted",
		GetName:         "get_tag_notrusted",
		ListDescription: descriptionFixture,
		GetDescription:  descriptionFixture,
		Service:         func(c *fileee.Client) fileee.ReadService[fileee.Tag] { return c.Tags },
		Summarize:       func(tag *fileee.Tag) tagSummary { return tagSummary{ID: tag.ID} },
		// UntrustedLine bewusst nicht gesetzt — kein Fremdtext bei diesem Typ.
		// PoisonProbe bewusst nicht gesetzt — es gibt nichts zu vergiften.
		// IDOf ist seit mustHaveIDOf (read_generic.go) Pflichtfeld, unabhaengig davon.
		IDOf: func(tag *fileee.Tag) string { return tag.ID },
	}

	s := mcp.NewServer(&mcp.Implementation{Name: "probe", Version: "0"}, nil)
	registerReadService(s, (*clientpool.Pool)(nil), discardLogger(), d, nil) // darf NICHT paniken

	names := toolNamesOf(t, s)
	if !names["list_tags_notrusted"] || !names["get_tag_notrusted"] {
		t.Error("Werkzeuge eines Deskriptors ohne Fremdtext wurden nicht angemeldet")
	}
}

// TestRegisterReadServicePanictWennPoisonProbeOhneUntrustedLineGesetztIst
// ist das zusaetzliche, kostenlose Sicherheitsnetz: ein gesetztes
// PoisonProbe bei nil UntrustedLine liest sich wie "die Fremdtext-Pruefung
// wurde angefangen und nicht zu Ende verdrahtet" — die eine Haelfte dieses
// Fehlers, die eine Mechanik erkennen kann (die andere Haelfte — beide
// Felder faelschlich leer bei einem Typ, der tatsaechlich Fremdtext
// traegt — kann keine Mechanik erkennen, das ist Fachwissen, siehe
// UntrustedLine's eigener Kommentar).
func TestRegisterReadServicePanictWennPoisonProbeOhneUntrustedLineGesetztIst(t *testing.T) {
	d := tagDescriptor()
	d.UntrustedLine = nil // PoisonProbe bleibt gesetzt (aus tagDescriptor())

	defer func() {
		if r := recover(); r == nil {
			t.Error("erwartete Panic (PoisonProbe gesetzt, UntrustedLine nil) blieb aus")
		}
	}()
	s := mcp.NewServer(&mcp.Implementation{Name: "probe", Version: "0"}, nil)
	registerReadService(s, (*clientpool.Pool)(nil), discardLogger(), d, nil)
}

// TestMustNotLeakUntrustedTextMeldetDenDeskriptorTyp ist die Meldungstext-
// Gegenprobe zu den Panic-Tests oben: die pruefen nur, DASS eine Panic
// geschieht, nie WELCHER Text drinsteht. Bei der Extraktion von
// mustNotLeakUntrustedText (Aufgabe 2b, Antrag #46, erste Runde) ist genau
// dieser Text verlorengegangen — der Bezeichner "readServiceDescriptor"
// fehlte in vier von fuenf Meldungen, weil kein Test je den Wortlaut
// pruefte. Dieser Test schliesst die Luecke: schlaegt fehl, sollte
// "readServiceDescriptor" wieder aus einer Meldung verschwinden.
func TestMustNotLeakUntrustedTextMeldetDenDeskriptorTyp(t *testing.T) {
	d := tagDescriptor()
	d.PoisonProbe = nil // UntrustedLine bleibt gesetzt (aus tagDescriptor())

	defer func() {
		r := recover()
		if r == nil {
			t.Fatal("erwartete Panic blieb aus")
		}
		msg, ok := r.(string)
		if !ok {
			t.Fatalf("Panic-Wert ist kein string: %v", r)
		}
		if !strings.Contains(msg, "readServiceDescriptor") {
			t.Errorf("Panic-Meldung %q nennt nicht den Deskriptor-Typ readServiceDescriptor", msg)
		}
	}()
	s := mcp.NewServer(&mcp.Implementation{Name: "probe", Version: "0"}, nil)
	registerReadService(s, (*clientpool.Pool)(nil), discardLogger(), d, nil)
}

// TestWrapUntrustedLinesWithBoundaryEmptyLinesProducesEmptyResult ist
// wrapUntrustedLinesWithBoundary's eigener Leerfall — dieselbe Wahl wie
// bei wrapUntrustedLines (keine nicht-leere Zeile → kein Rahmen, reine
// Rauschvermeidung, siehe wrapUntrustedLines' eigenen Kommentar).
func TestWrapUntrustedLinesWithBoundaryEmptyLinesProducesEmptyResult(t *testing.T) {
	boundary, err := newUntrustedBoundary()
	if err != nil {
		t.Fatalf("newUntrustedBoundary: %v", err)
	}
	result := wrapUntrustedLinesWithBoundary(boundary, []string{"", "   ", "\t"})
	if result == nil {
		t.Fatal("result ist nil, want ein leeres *mcp.CallToolResult{}")
	}
	if len(result.Content) != 0 {
		t.Errorf("Content hat %d Eintraege, want 0 — es gibt hier keine nicht-leere Zeile zu rahmen", len(result.Content))
	}
}

// TestWrapUntrustedLinesWithBoundaryMatchesWrapUntrustedLinesFormat ist
// die vom Auftrag geforderte Formattreue-Pruefung: wrapUntrustedLines
// erzeugt bei jedem Aufruf einen frischen, unvorhersagbaren Boundary-
// String (newUntrustedBoundary, read.go) — dieser Test extrahiert genau
// diesen Boundary-String aus wrapUntrustedLines' eigenem Output und
// speist ihn in wrapUntrustedLinesWithBoundary — bei gleichem Boundary
// und gleichen Zeilen MUESSEN beide Pfade byte-identischen Text liefern,
// sonst hat der Umbau (Fix 3, homelab-management-Repo) die bestehende
// Rahmung veraendert statt nur ihren Erzeugungszeitpunkt zu verschieben.
func TestWrapUntrustedLinesWithBoundaryMatchesWrapUntrustedLinesFormat(t *testing.T) {
	lines := []string{"Erste Zeile", "", "  ", "Zweite Zeile mit Umlauten: ä ö ü ß"}

	original, err := wrapUntrustedLines(lines)
	if err != nil {
		t.Fatalf("wrapUntrustedLines: %v", err)
	}
	if len(original.Content) != 1 {
		t.Fatalf("wrapUntrustedLines Content hat %d Eintraege, want 1", len(original.Content))
	}
	originalText, ok := original.Content[0].(*mcp.TextContent)
	if !ok {
		t.Fatalf("Content[0] ist %T, want *mcp.TextContent", original.Content[0])
	}

	boundaryPattern := regexp.MustCompile(`boundary="([0-9a-f]+)"`)
	match := boundaryPattern.FindStringSubmatch(originalText.Text)
	if match == nil {
		t.Fatalf("kein boundary=\"...\" im Output von wrapUntrustedLines gefunden: %q", originalText.Text)
	}
	boundary := match[1]

	rebuilt := wrapUntrustedLinesWithBoundary(boundary, lines)
	if len(rebuilt.Content) != 1 {
		t.Fatalf("wrapUntrustedLinesWithBoundary Content hat %d Eintraege, want 1", len(rebuilt.Content))
	}
	rebuiltText, ok := rebuilt.Content[0].(*mcp.TextContent)
	if !ok {
		t.Fatalf("Content[0] ist %T, want *mcp.TextContent", rebuilt.Content[0])
	}

	if rebuiltText.Text != originalText.Text {
		t.Errorf("wrapUntrustedLinesWithBoundary liefert bei gleichem Boundary-String einen ANDEREN Text als wrapUntrustedLines:\n--- wrapUntrustedLines ---\n%s\n--- wrapUntrustedLinesWithBoundary ---\n%s", originalText.Text, rebuiltText.Text)
	}
}

// --- rec.Record über die generischen Handler --------------------------------
//
// Jeder andere Test dieser Datei bleibt bewusst UNTERHALB der ctx-Grenze
// (siehe Datei-Kopfkommentar) — er prüft entweder einen Fehlerpfad oder
// eine reine Struktur-Zusicherung, keiner von beiden braucht je eine
// echte, verifizierte Identität. TestGetFromServiceMerktDenGezieltenEinzelabrufListFromServiceMerktNichts
// ist die erste Ausnahme in dieser Datei: um zu belegen, dass getFromService
// tatsächlich bei rec (issued.Store) meldet — UND dass listFromService das
// SEIT ADR-0019 NICHT mehr tut —, muss Check ein ctx sehen, aus dem
// serve.IdentityFrom(ctx) eine echte Identität liest — dieselbe
// Einschränkung, die internal/issued/issued_test.go's eigener Kommentar
// zu ctxMitIdentitaet ausführlich begründet (kein exportierter Setter in
// gangway v0.5.0, die Identität wird ausschließlich von Gangways
// Authentifizierungs-Middleware gesetzt, nie von einer selbstgebauten
// Attrappe). Der Test bleibt trotzdem UNTERHALB von clientFor: er ruft
// listFromService/getFromService direkt mit einem Fake-Service auf, genau
// wie jeder andere Erfolgsfall-Test dieser Datei (z. B.
// TestListFromServiceLiefertMehrereEintraegeUndSammeltGerahmtenFremdtext)
// — nur eben mit einem echten, identitätstragenden ctx statt
// context.Background().

// TestGetFromServiceMerktDenGezieltenEinzelabrufListFromServiceMerktNichts
// ist die "beide Richtungen"-Umkehrung von ADR-0019: sie treibt
// tagDescriptor() über listFromService mit zwei Zeilen UND über
// getFromService mit einer weiteren, gegen einen echten *issued.Store —
// und prüft, dass NACH dem List-Aufruf KEINE der beiden gelisteten IDs
// per Check gültig ist (sie wurden vom Aufrufer nie einzeln genannt, siehe
// listFromService's eigener Doc-Kommentar), während die per getFromService
// geholte ID gültig ist (der eine gezielte Einzelabruf, den ADR-0019
// weiterhin erfasst).
//
// Vor dieser Verschärfung hieß dieser Test
// TestGenericHandlerMerktAusgelieferteIDs und prüfte das genaue Gegenteil
// der List-Hälfte (beide gelisteten IDs müssen gültig sein) — siehe
// Git-Historie für die alte Fassung, falls der frühere, breitere
// Aufnahme-Mechanismus je nachvollzogen werden muss.
func TestGetFromServiceMerktDenGezieltenEinzelabrufListFromServiceMerktNichts(t *testing.T) {
	d := tagDescriptor()
	service := &fakeReadService[fileee.Tag]{
		queryResult: &fileee.QueryResult[fileee.Tag]{
			Rows:      []fileee.Tag{{ID: "t1", Name: "Rechnung"}, {ID: "t2", Name: "Mahnung"}},
			TotalRows: 2,
		},
		getResult: &fileee.Tag{ID: "t3", Name: "Vertrag"},
	}
	rec := issued.New(time.Hour, 100)
	ctx := ctxMitIdentitaet(t, "alice")

	if _, _, err := listFromService(ctx, d, service, genericListInput{}); err != nil {
		t.Fatalf("listFromService: %v", err)
	}
	for _, id := range []string{"t1", "t2"} {
		if err := rec.Check(ctx, id); err == nil {
			t.Errorf("Check(%q) nach dem List-Aufruf: nil, want einen Fehler — list_* darf seit ADR-0019 keine ID mehr aufnehmen, die der Aufrufer nicht einzeln genannt hat", id)
		}
	}

	if _, _, err := getFromService(ctx, d, service, "t3", rec); err != nil {
		t.Fatalf("getFromService: %v", err)
	}
	if err := rec.Check(ctx, "t3"); err != nil {
		t.Errorf("Check(t3) nach dem Get-Aufruf: %v, want nil (id wurde soeben gezielt per ID abgerufen)", err)
	}
}

// TestAlleGenerischenDeskriptorenHabenEinIDOf ist Aufgabe 4's Schritt 4 —
// die günstige Hälfte des Guardrails: geht über jeden von RegisterAll
// (über registerSyncTools/registerReferenceTools/registerPeopleTools)
// tatsächlich angemeldeten readServiceDescriptor/syncDescriptor und
// schlägt fehl, sobald IDOf fehlt. Boxen (read_boxes.go) sind
// handgeschriebene Werkzeuge, keine registerReadService/registerSync-
// Deskriptoren — sie gehören, wie list_documents/get_document/
// sync_documents/list_document_conversations, zur teureren Hälfte des
// Guardrails in issued_coverage_test.go (Aufgabe 5).
func TestAlleGenerischenDeskriptorenHabenEinIDOf(t *testing.T) {
	checks := map[string]bool{
		"referenceTagDescriptor":                referenceTagDescriptor().IDOf != nil,
		"referenceCompanyDescriptor":            referenceCompanyDescriptor().IDOf != nil,
		"referenceDocumentTypeDescriptor":       referenceDocumentTypeDescriptor().IDOf != nil,
		"referenceDocumentTypeSchemeDescriptor": referenceDocumentTypeSchemeDescriptor().IDOf != nil,
		"contactDescriptor":                     contactDescriptor().IDOf != nil,
		"reminderDescriptor":                    reminderDescriptor().IDOf != nil,
		"conversationDescriptor":                conversationDescriptor().IDOf != nil,
		"tagSyncDescriptor":                     tagSyncDescriptor().IDOf != nil,
		"companySyncDescriptor":                 companySyncDescriptor().IDOf != nil,
		"documentTypeSyncDescriptor":            documentTypeSyncDescriptor().IDOf != nil,
		"documentTypeSchemeSyncDescriptor":      documentTypeSchemeSyncDescriptor().IDOf != nil,
		"contactSyncDescriptor":                 contactSyncDescriptor().IDOf != nil,
		"reminderSyncDescriptor":                reminderSyncDescriptor().IDOf != nil,
		"conversationSyncDescriptor":            conversationSyncDescriptor().IDOf != nil,
	}
	for name, ok := range checks {
		if !ok {
			t.Errorf("%s().IDOf ist nil — Pflichtfeld, siehe readServiceDescriptor.IDOf/syncDescriptor.IDOf (read_generic.go/read_sync.go)", name)
		}
	}
}

// ctxMitIdentitaet liefert ein echtes, verifiziertes ctx für subject —
// derselbe minimale Wegwerf-Gangway-Rundlauf wie
// internal/issued/issued_test.go's gleichnamiger Helfer (siehe dessen
// ausführlichen Kommentar für das vollständige WARUM: kein exportierter
// Setter in gangway v0.5.0, die Identität wird ausschließlich von
// Gangways Authentifizierungs-Middleware gesetzt) — hier lokal
// nachgebaut statt importiert, aus demselben Grund, den
// internal/server/issued_wiring_test.go's authenticatedCtx für sich
// selbst nennt: ein Test-Helfer eines fremden Pakets ist außerhalb
// dieses Pakets nicht sichtbar, ein eigenes Test-Hilfspaket für diese
// eine Funktion wäre unverhältnismäßig.
func ctxMitIdentitaet(t *testing.T, subject string) context.Context {
	t.Helper()

	idp := testidp.New(t)
	captured := make(chan context.Context, 1)

	mcpServer := mcp.NewServer(&mcp.Implementation{Name: "read-generic-identity-test", Version: "0.0.0"}, nil)
	mcp.AddTool(mcpServer, &mcp.Tool{Name: "capture"},
		func(ctx context.Context, _ *mcp.CallToolRequest, _ struct{}) (*mcp.CallToolResult, any, error) {
			captured <- ctx
			return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: "ok"}}}, nil, nil
		})

	const audience = "fileee-mcp-read-generic-identity-test"
	gwCfg := &serve.Config{
		Addr:            "127.0.0.1:0",
		PublicBaseURL:   "https://read-generic-identity-test.example.invalid",
		IssuerURL:       idp.URL(),
		Audience:        audience,
		SubjectClaim:    "sub",
		AllowedPrefixes: mustIdentityPrefixes(t, "127.0.0.1/32", "::1/128"),
	}
	gw, err := serve.New(context.Background(), gwCfg, serve.WithDecider(access.AllowAll()))
	if err != nil {
		t.Fatalf("ctxMitIdentitaet(%q): serve.New: %v", subject, err)
	}
	gw.AttachMCP(mcpServer)

	srv := httptest.NewServer(gw.Handler())
	t.Cleanup(srv.Close)

	token := idp.Token(map[string]any{
		"iss": idp.URL(), "aud": audience, "sub": subject,
		"exp": time.Now().Add(time.Hour).Unix(),
	})
	client := mcp.NewClient(&mcp.Implementation{Name: "read-generic-identity-test-client", Version: "0.0.0"}, nil)
	transport := &mcp.StreamableClientTransport{
		Endpoint:             srv.URL + "/mcp",
		HTTPClient:           &http.Client{Transport: identityBearerRoundTripper{token: token}},
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

// identityBearerRoundTripper injiziert ein festes Bearer-Token in jede
// ausgehende Anfrage — dasselbe Muster wie
// internal/issued/issued_test.go's bearerRoundTripper, hier eigens
// benannt, um mit keinem gleichnamigen Bezeichner dieses Pakets zu
// kollidieren.
type identityBearerRoundTripper struct{ token string }

func (rt identityBearerRoundTripper) RoundTrip(req *http.Request) (*http.Response, error) {
	req = req.Clone(req.Context())
	req.Header.Set("Authorization", "Bearer "+rt.token)
	return http.DefaultTransport.RoundTrip(req)
}

// mustIdentityPrefixes parst cidrs zu netip.Prefix — derselbe kleine
// Helfer wie internal/issued/issued_test.go's mustPrefixes und
// internal/server/server_test.go's mustPrefix, hier eigens benannt aus
// demselben Sichtbarkeits-Grund wie ctxMitIdentitaet oben.
func mustIdentityPrefixes(t *testing.T, cidrs ...string) []netip.Prefix {
	t.Helper()
	out := make([]netip.Prefix, 0, len(cidrs))
	for _, c := range cidrs {
		p, err := netip.ParsePrefix(c)
		if err != nil {
			t.Fatalf("mustIdentityPrefixes(%q): %v", c, err)
		}
		out = append(out, p)
	}
	return out
}
