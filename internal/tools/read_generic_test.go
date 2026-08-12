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
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/strausmann/go-fileee/fileee"

	"github.com/strausmann/fileee-mcp-server/internal/clientpool"
)

// discardLogger builds a *slog.Logger that discards everything it is
// given — for the many tests in this file and in read_sync_test.go that
// pass one into registerReadService/registerSync/genericListHandler/
// genericGetHandler/genericSyncHandler but do not themselves assert on
// what it logs (that is read_generic_sync_diag_test.go's own job). Never
// nil: RegisterRead's own doc comment (read.go) requires a non-nil
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
// registeredReadTools(), i.e. only tools RegisterRead itself mounts —
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
	}
}

func TestRegisterReadServiceMeldetListeUndDetailAn(t *testing.T) {
	s := mcp.NewServer(&mcp.Implementation{Name: "probe", Version: "0"}, nil)

	registerReadService(s, (*clientpool.Pool)(nil), discardLogger(), tagDescriptor())

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
type fakeReadService[T any] struct {
	queryErr   error
	getErr     error
	diffErr    error
	diffResult *fileee.DiffResult[T]
}

func (f *fakeReadService[T]) Query(context.Context, fileee.QueryOptions) (*fileee.QueryResult[T], error) {
	if f.queryErr != nil {
		return nil, f.queryErr
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
	handler := genericGetHandler[fileee.Tag, tagSummary](nil, discardLogger(), d)

	_, _, err := handler(context.Background(), nil, genericGetInput{ID: "   "})
	if err == nil {
		t.Fatal("erwarteter Fehler blieb aus")
	}
	if !strings.Contains(err.Error(), d.GetName) {
		t.Errorf("Fehlermeldung %q enthaelt nicht den Werkzeugnamen %q", err.Error(), d.GetName)
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
	registerReadService(s, (*clientpool.Pool)(nil), discardLogger(), d)
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
	registerReadService(s, (*clientpool.Pool)(nil), discardLogger(), d)
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
	registerReadService(s, (*clientpool.Pool)(nil), discardLogger(), d)
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
	registerReadService(s, (*clientpool.Pool)(nil), discardLogger(), d)
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
	}

	s := mcp.NewServer(&mcp.Implementation{Name: "probe", Version: "0"}, nil)
	registerReadService(s, (*clientpool.Pool)(nil), discardLogger(), d) // darf NICHT paniken
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
	}

	s := mcp.NewServer(&mcp.Implementation{Name: "probe", Version: "0"}, nil)
	registerReadService(s, (*clientpool.Pool)(nil), discardLogger(), d) // darf NICHT paniken

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
	registerReadService(s, (*clientpool.Pool)(nil), discardLogger(), d)
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
	registerReadService(s, (*clientpool.Pool)(nil), discardLogger(), d)
}
