// White-box tests for ops.go's operational tools — get_runtime_stats
// (Aufgabe C1) and, once C2 lands in this same file, get_tool_manifest.
// Both are hand-written (not routed through registerReadService/
// registerSync), so — same rule as every other bespoke handler in this
// package (fieldlist_test.go's own doc comment) — their output structs
// get their own field-allowlist test, and the one property this whole
// task exists to guarantee (no error text ever reaches an output) gets
// its own dedicated, adversarial test rather than relying on the generic
// descriptors' PoisonProbe mechanism, which never runs for a bespoke
// handler at all.
package tools

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/strausmann/gangway/identity"
	"github.com/strausmann/go-fileee/fileee"

	"github.com/strausmann/fileee-mcp-server/internal/clientpool"
)

// resetRuntimeStats clears the package-level runtimeStats singleton so
// each test starts from an empty counter set — runtimeStats is
// deliberately process-wide (see its own doc comment in ops.go), which
// means tests that do not reset it would otherwise see counts left over
// from whichever test ran first.
func resetRuntimeStats(t *testing.T) {
	t.Helper()
	runtimeStats.mu.Lock()
	runtimeStats.byTool = make(map[string]*toolCallStats)
	runtimeStats.mu.Unlock()
}

// TestLogToolEndVerzeichnetNieDenFehlertextNurDieEinordnung ist der
// Kern-Test dieser Aufgabe: ein Fehler, dessen Text einen
// geheimnisartigen Wert enthaelt, darf in der Statistik nirgends
// auftauchen -- nur classifyErr's feste Einordnung. Geprueft wird nicht
// nur das eine Feld, das den Fehler heute traegt (LastErrorKind), sondern
// die GESAMTE Ausgabe als Zeichenkette -- ein spaeter hinzugefuegtes Feld,
// das den Fehlertext doch durchreicht, faellt damit auf, ohne dass dieser
// Test angepasst werden muesste.
func TestLogToolEndVerzeichnetNieDenFehlertextNurDieEinordnung(t *testing.T) {
	resetRuntimeStats(t)
	geheimnisartig := errors.New("Zugriff verweigert, das Feld enthaelt den Wert Sonnenblumenkern-Zweitausendsechsundzwanzig")

	logToolEnd(context.Background(), discardLogger(), "geheimnistest_werkzeug", time.Now(), "", 0, geheimnisartig)

	out := runtimeStatsSnapshot()
	if len(out.Tools) != 1 {
		t.Fatalf("Tools = %+v, want genau einen Eintrag", out.Tools)
	}
	entry := out.Tools[0]
	if entry.LastErrorKind != "error" {
		t.Errorf("LastErrorKind = %q, want %q (classifyErr's Einordnung, nicht der Fehlertext)", entry.LastErrorKind, "error")
	}

	voll := fmt.Sprintf("%+v", out)
	if strings.Contains(voll, "Sonnenblumenkern") {
		t.Fatalf("Statistik-Schnappschuss enthaelt den Fehlertext irgendwo -- das darf nie passieren: %s", voll)
	}
}

// TestRecordToolCallZaehltAufrufeUndFehlerJeWerkzeug belegt den
// Erfolgsfall: mehrere Aufrufe, mit und ohne Fehler, ergeben die
// erwarteten Zaehlerstaende UND die Gesamtsummen ueber alle Werkzeuge.
func TestRecordToolCallZaehltAufrufeUndFehlerJeWerkzeug(t *testing.T) {
	resetRuntimeStats(t)

	logToolEnd(context.Background(), discardLogger(), "werkzeug_a", time.Now(), "", 1, nil)
	logToolEnd(context.Background(), discardLogger(), "werkzeug_a", time.Now(), "", 1, nil)
	logToolEnd(context.Background(), discardLogger(), "werkzeug_a", time.Now(), "", 0, errors.New("Gegenseite antwortet mit einem Fehler"))
	logToolEnd(context.Background(), discardLogger(), "werkzeug_b", time.Now(), "", 1, nil)

	out := runtimeStatsSnapshot()
	if out.TotalCalls != 4 {
		t.Errorf("TotalCalls = %d, want 4", out.TotalCalls)
	}
	if out.TotalErrors != 1 {
		t.Errorf("TotalErrors = %d, want 1", out.TotalErrors)
	}

	byName := make(map[string]runtimeStatsToolEntry, len(out.Tools))
	for _, e := range out.Tools {
		byName[e.Tool] = e
	}
	a, ok := byName["werkzeug_a"]
	if !ok {
		t.Fatal("werkzeug_a fehlt in der Statistik")
	}
	if a.Calls != 3 || a.Errors != 1 {
		t.Errorf("werkzeug_a: Calls=%d Errors=%d, want Calls=3 Errors=1", a.Calls, a.Errors)
	}
	if a.LastErrorAt == "" {
		t.Error("werkzeug_a: LastErrorAt ist leer, obwohl ein Fehler aufgetreten ist")
	}
	b, ok := byName["werkzeug_b"]
	if !ok {
		t.Fatal("werkzeug_b fehlt in der Statistik")
	}
	if b.Calls != 1 || b.Errors != 0 {
		t.Errorf("werkzeug_b: Calls=%d Errors=%d, want Calls=1 Errors=0", b.Calls, b.Errors)
	}
	if b.LastErrorKind != "" || b.LastErrorAt != "" {
		t.Errorf("werkzeug_b hatte nie einen Fehler, LastErrorKind/LastErrorAt sollten leer bleiben: %+v", b)
	}
}

// TestRecordToolCallIstNebenlaeufigkeitssicher faehrt viele gleichzeitige
// Aufrufe fuer dasselbe Werkzeug und prueft die exakte Endsumme -- unter
// -race deckt das sowohl eine Daten-Wettlaufsituation als auch eine
// verlorene Aktualisierung auf (eine ungeschuetzte read-modify-write-Folge
// wuerde bei diesem Umfang zuverlaessig eine zu niedrige Endsumme
// erzeugen, auch ohne dass der Race-Detector anschlaegt).
func TestRecordToolCallIstNebenlaeufigkeitssicher(t *testing.T) {
	resetRuntimeStats(t)
	const goroutines = 50
	const jeGoroutine = 20

	var wg sync.WaitGroup
	wg.Add(goroutines)
	for i := 0; i < goroutines; i++ {
		go func(i int) {
			defer wg.Done()
			for j := 0; j < jeGoroutine; j++ {
				if (i+j)%2 == 0 {
					logToolEnd(context.Background(), discardLogger(), "paralleles_werkzeug", time.Now(), "", 1, nil)
				} else {
					logToolEnd(context.Background(), discardLogger(), "paralleles_werkzeug", time.Now(), "", 0,
						errors.New("simulierter Fehler fuer den Nebenlaeufigkeitstest"))
				}
			}
		}(i)
	}
	wg.Wait()

	out := runtimeStatsSnapshot()
	var entry *runtimeStatsToolEntry
	for i := range out.Tools {
		if out.Tools[i].Tool == "paralleles_werkzeug" {
			entry = &out.Tools[i]
		}
	}
	if entry == nil {
		t.Fatal("paralleles_werkzeug taucht nach den nebenlaeufigen Aufrufen nicht in der Statistik auf")
	}
	wantCalls := int64(goroutines * jeGoroutine)
	if entry.Calls != wantCalls {
		t.Errorf("Calls = %d, want %d -- verlorene Aktualisierung unter Nebenlaeufigkeit", entry.Calls, wantCalls)
	}
}

func TestGetRuntimeStatsOutputFeldlisteIstAbgeschlossen(t *testing.T) {
	want := []string{"TotalCalls", "TotalErrors", "Tools"}
	got := fieldNames(getRuntimeStatsOutput{})
	if !reflect.DeepEqual(got, want) {
		t.Errorf("getRuntimeStatsOutput-Feldliste = %v, want %v", got, want)
	}
}

func TestRuntimeStatsToolEntryFeldlisteIstAbgeschlossen(t *testing.T) {
	want := []string{"Tool", "Calls", "Errors", "LastErrorKind", "LastErrorAt"}
	got := fieldNames(runtimeStatsToolEntry{})
	if !reflect.DeepEqual(got, want) {
		t.Errorf("runtimeStatsToolEntry-Feldliste = %v, want %v", got, want)
	}
}

func TestGetRuntimeStatsInputNimmtKeineParameterEntgegen(t *testing.T) {
	got := fieldNames(getRuntimeStatsInput{})
	if len(got) != 0 {
		t.Errorf("getRuntimeStatsInput hat Felder %v, want keine", got)
	}
}

func TestGetRuntimeStatsHandlerLiefertEinenSchnappschuss(t *testing.T) {
	resetRuntimeStats(t)
	logToolEnd(context.Background(), discardLogger(), "irgendein_werkzeug", time.Now(), "", 1, nil)

	handler := getRuntimeStatsHandler(discardLogger())
	_, out, err := handler(context.Background(), nil, getRuntimeStatsInput{})
	if err != nil {
		t.Fatalf("getRuntimeStatsHandler: %v", err)
	}
	if out.TotalCalls != 1 {
		t.Errorf("TotalCalls = %d, want 1", out.TotalCalls)
	}
}

func TestRegisterOpsToolsMeldetGetRuntimeStatsAn(t *testing.T) {
	s := mcp.NewServer(&mcp.Implementation{Name: "probe", Version: "0"}, nil)

	registerOpsTools(s, (*clientpool.Pool)(nil), ServerInfo{}, discardLogger())

	names := toolNamesOf(t, s)
	if !names[ToolGetRuntimeStats] {
		t.Errorf("Werkzeug %q wurde nicht angemeldet", ToolGetRuntimeStats)
	}
}

// --- get_tool_manifest (Aufgabe C2) ----------------------------------------

// TestGetToolManifestMeldetGenauSoVieleWerkzeugeWieTatsaechlichAngemeldetSind
// ist C2's Kern-Test: die gemeldete Anzahl muss der Anzahl tatsaechlich
// angemeldeter Werkzeuge entsprechen -- EINSCHLIESSLICH get_tool_manifest
// selbst und get_runtime_stats. Genau dieser Selbstbezug fehlte beim
// Dockhand-Server (292 statt 298 gemeldet, weil die dortige Liste von
// Hand gepflegt wurde und nie automatisch abgeglichen wurde). Die
// Referenzzahl kommt hier bewusst aus einem UNABHAENGIGEN zweiten
// Hin-und-Rueckl-Lauf (toolNamesOf, dieselbe Maschinerie wie
// registeredReadTools() in names.go) -- nicht aus der Anzahl der
// AddTool-Aufrufe in RegisterAll abgezaehlt, sonst wuerde der Test genau
// denselben blinden Fleck teilen, den er aufdecken soll.
func TestGetToolManifestMeldetGenauSoVieleWerkzeugeWieTatsaechlichAngemeldetSind(t *testing.T) {
	s := mcp.NewServer(&mcp.Implementation{Name: "probe", Version: "0"}, nil)
	RegisterAll(s, (*clientpool.Pool)(nil), ServerInfo{}, discardLogger())

	want := toolNamesOf(t, s)
	if !want[ToolGetToolManifest] {
		t.Fatal("Testaufbau fehlerhaft: get_tool_manifest selbst ist nicht angemeldet")
	}

	handler := getToolManifestHandler(s, discardLogger())
	_, out, err := handler(context.Background(), nil, getToolManifestInput{})
	if err != nil {
		t.Fatalf("getToolManifestHandler: %v", err)
	}

	if out.Total != len(want) {
		t.Errorf("Total = %d, want %d (Anzahl tatsaechlich angemeldeter Werkzeuge)", out.Total, len(want))
	}
	if len(out.Tools) != len(want) {
		t.Errorf("len(Tools) = %d, want %d", len(out.Tools), len(want))
	}

	got := make(map[string]bool, len(out.Tools))
	for _, tool := range out.Tools {
		got[tool.Name] = true
	}
	if !got[ToolGetToolManifest] {
		t.Error("get_tool_manifest zaehlt sich nicht selbst mit -- genau der Dockhand-Fehler (292 statt 298)")
	}
	if !got[ToolGetRuntimeStats] {
		t.Error("get_runtime_stats fehlt im eigenen Verzeichnis")
	}
	for name := range want {
		if !got[name] {
			t.Errorf("Werkzeug %q ist angemeldet, fehlt aber im Verzeichnis", name)
		}
	}
	for name := range got {
		if !want[name] {
			t.Errorf("Verzeichnis nennt %q, aber kein Werkzeug dieses Namens ist angemeldet", name)
		}
	}
}

func TestGetToolManifestWaechstMitNeuAngemeldetenWerkzeugenMit(t *testing.T) {
	s := mcp.NewServer(&mcp.Implementation{Name: "probe", Version: "0"}, nil)
	RegisterAll(s, (*clientpool.Pool)(nil), ServerInfo{}, discardLogger())
	handler := getToolManifestHandler(s, discardLogger())

	_, before, err := handler(context.Background(), nil, getToolManifestInput{})
	if err != nil {
		t.Fatalf("getToolManifestHandler (vorher): %v", err)
	}

	// Versuchsaufbau: ein zusaetzliches, unabhaengiges Werkzeug auf
	// demselben Wegwerf-Server anmelden -- s existiert nur fuer diesen
	// Test und wird mit ihm verworfen, ein Entfernen danach ist deshalb
	// nicht noetig (kein geteilter Zustand, keine Produktionsinstanz).
	mcp.AddTool(s, &mcp.Tool{
		Name:        "zusaetzliches_testwerkzeug_fuer_die_gegenprobe",
		Description: descriptionFixture,
		Annotations: &mcp.ToolAnnotations{ReadOnlyHint: true},
	}, func(_ context.Context, _ *mcp.CallToolRequest, _ struct{}) (*mcp.CallToolResult, struct{}, error) {
		return &mcp.CallToolResult{}, struct{}{}, nil
	})

	_, after, err := handler(context.Background(), nil, getToolManifestInput{})
	if err != nil {
		t.Fatalf("getToolManifestHandler (nachher): %v", err)
	}

	if after.Total != before.Total+1 {
		t.Errorf("Total nach zusaetzlicher Anmeldung = %d, want %d (vorher %d + 1)", after.Total, before.Total+1, before.Total)
	}
}

func TestGetToolManifestOutputFeldlisteIstAbgeschlossen(t *testing.T) {
	want := []string{"Total", "Tools"}
	got := fieldNames(getToolManifestOutput{})
	if !reflect.DeepEqual(got, want) {
		t.Errorf("getToolManifestOutput-Feldliste = %v, want %v", got, want)
	}
}

func TestToolManifestEntryFeldlisteIstAbgeschlossen(t *testing.T) {
	want := []string{"Name", "Title", "Description", "Kind"}
	got := fieldNames(toolManifestEntry{})
	if !reflect.DeepEqual(got, want) {
		t.Errorf("toolManifestEntry-Feldliste = %v, want %v", got, want)
	}
}

// TestGetToolManifestEntryTraegtDenTitelDesWerkzeugs belegt, dass Title
// im Verzeichnis nicht nur als Feld existiert, sondern tatsaechlich mit
// dem Wert befuellt wird, den das jeweilige Werkzeug selbst ueber
// mcp.ToolAnnotations.Title traegt -- Aufgabe C2s Description verspricht
// "name, title, description and kind" je Eintrag; ohne diesen Test waere
// das Feld leer geblieben und das Versprechen falsch, obwohl
// TestToolManifestEntryFeldlisteIstAbgeschlossen (nur Feldnamen, keine
// Werte) bereits gruen war.
func TestGetToolManifestEntryTraegtDenTitelDesWerkzeugs(t *testing.T) {
	s := mcp.NewServer(&mcp.Implementation{Name: "probe", Version: "0"}, nil)
	RegisterAll(s, (*clientpool.Pool)(nil), ServerInfo{}, discardLogger())
	handler := getToolManifestHandler(s, discardLogger())

	_, out, err := handler(context.Background(), nil, getToolManifestInput{})
	if err != nil {
		t.Fatalf("getToolManifestHandler: %v", err)
	}

	byName := make(map[string]toolManifestEntry, len(out.Tools))
	for _, entry := range out.Tools {
		byName[entry.Name] = entry
	}

	entry, ok := byName[ToolGetToolManifest]
	if !ok {
		t.Fatal("get_tool_manifest fehlt im eigenen Verzeichnis")
	}
	if entry.Title != "Tool manifest" {
		t.Errorf("Title von %q = %q, want %q (aus mcp.ToolAnnotations.Title, registerOpsTools)", ToolGetToolManifest, entry.Title, "Tool manifest")
	}

	for _, tool := range out.Tools {
		if tool.Title == "" {
			t.Errorf("Werkzeug %q hat keinen Titel im Verzeichnis -- jedes ueber RegisterAll angemeldete Werkzeug setzt mcp.ToolAnnotations.Title (ADR-0018)", tool.Name)
		}
	}
}

func TestGetToolManifestInputNimmtKeineParameterEntgegen(t *testing.T) {
	got := fieldNames(getToolManifestInput{})
	if len(got) != 0 {
		t.Errorf("getToolManifestInput hat Felder %v, want keine", got)
	}
}

// TestGetToolManifestNenntDieBerechtigungsgruppeJeWerkzeug belegt, dass
// jeder Eintrag eine nicht-leere Kind-Angabe traegt -- deriveToolManifestKind
// (ops.go) leitet sie heute PRO WERKZEUG aus dessen eigener
// mcp.ToolAnnotations.ReadOnlyHint ab (die readToolNames/ReadToolKinds()-
// Einstufung, die diesen Wert frueher lieferte, ist mit Task 3 des
// tool-exposure-foundation-Umbaus entfallen), get_tool_manifest darf
// diese Information nicht verlieren.
func TestGetToolManifestNenntDieBerechtigungsgruppeJeWerkzeug(t *testing.T) {
	s := mcp.NewServer(&mcp.Implementation{Name: "probe", Version: "0"}, nil)
	RegisterAll(s, (*clientpool.Pool)(nil), ServerInfo{}, discardLogger())
	handler := getToolManifestHandler(s, discardLogger())

	_, out, err := handler(context.Background(), nil, getToolManifestInput{})
	if err != nil {
		t.Fatalf("getToolManifestHandler: %v", err)
	}
	for _, tool := range out.Tools {
		if tool.Kind == "" {
			t.Errorf("Werkzeug %q hat keine Berechtigungsgruppe im Verzeichnis", tool.Name)
		}
	}
}

// TestGetToolManifestMeldetSchreibwerkzeugeNichtAlsLesend ist der
// Regressionstest fuer den behobenen Fehlbefund: vor diesem Fix trug
// toolManifestKind einen festen Literal "read", sodass JEDES Werkzeug --
// auch die acht mutierenden Schreibwerkzeuge -- im Verzeichnis als
// Kind:"read" erschien. Ein Host/Orchestrator, der Auto-Ausfuehrung an
// Kind festmacht, haette destruktive Werkzeuge (update_document,
// box_remove_document, ...) faelschlich als bestaetigungsfreie Reads
// behandelt. Der Test prueft explizit BEIDE Richtungen: jedes der acht
// Schreibwerkzeuge traegt Kind != "read" (aktuell "write"), UND jedes
// reine Lese-/Ops-Werkzeug traegt weiterhin Kind == "read" --
// deriveToolManifestKind darf die bestehende Klassifizierung nicht
// beschaedigen, waehrend sie die neue einfuehrt.
func TestGetToolManifestMeldetSchreibwerkzeugeNichtAlsLesend(t *testing.T) {
	s := mcp.NewServer(&mcp.Implementation{Name: "probe", Version: "0"}, nil)
	RegisterAll(s, (*clientpool.Pool)(nil), ServerInfo{}, discardLogger())
	handler := getToolManifestHandler(s, discardLogger())

	_, out, err := handler(context.Background(), nil, getToolManifestInput{})
	if err != nil {
		t.Fatalf("getToolManifestHandler: %v", err)
	}

	byName := make(map[string]toolManifestEntry, len(out.Tools))
	for _, entry := range out.Tools {
		byName[entry.Name] = entry
	}

	writeTools := []string{
		ToolCreateContact,
		ToolUpdateContact,
		ToolCreateReminder,
		ToolUpdateReminder,
		ToolBoxAddDocument,
		ToolBoxRemoveDocument,
		ToolUploadDocument,
		ToolUpdateDocument,
	}
	for _, name := range writeTools {
		entry, ok := byName[name]
		if !ok {
			t.Errorf("Schreibwerkzeug %q fehlt im Verzeichnis", name)
			continue
		}
		if entry.Kind == toolManifestKindRead {
			t.Errorf("Werkzeug %q: Kind = %q, darf nicht %q sein -- es schreibt/mutiert", name, entry.Kind, toolManifestKindRead)
		}
		if entry.Kind != toolManifestKindWrite {
			t.Errorf("Werkzeug %q: Kind = %q, want %q", name, entry.Kind, toolManifestKindWrite)
		}
	}

	readTools := []string{
		ToolGetToolManifest,
		ToolGetRuntimeStats,
		ToolListDocuments,
		ToolGetDocument,
	}
	for _, name := range readTools {
		entry, ok := byName[name]
		if !ok {
			t.Errorf("Lesewerkzeug %q fehlt im Verzeichnis", name)
			continue
		}
		if entry.Kind != toolManifestKindRead {
			t.Errorf("Werkzeug %q: Kind = %q, want %q -- es liest nur", name, entry.Kind, toolManifestKindRead)
		}
	}
}

func TestRegisterOpsToolsMeldetGetToolManifestAn(t *testing.T) {
	s := mcp.NewServer(&mcp.Implementation{Name: "probe", Version: "0"}, nil)

	registerOpsTools(s, (*clientpool.Pool)(nil), ServerInfo{}, discardLogger())

	names := toolNamesOf(t, s)
	if !names[ToolGetToolManifest] {
		t.Errorf("Werkzeug %q wurde nicht angemeldet", ToolGetToolManifest)
	}
}

// --- self_check (Aufgabe C3) ------------------------------------------

// resetSelfCheckCache clears selfCheckCache so each test starts from an
// empty cache -- same reasoning as resetRuntimeStats above: the cache is
// deliberately process-wide (see its own doc comment in ops.go).
func resetSelfCheckCache(t *testing.T) {
	t.Helper()
	selfCheckCache.mu.Lock()
	selfCheckCache.byAccount = make(map[string]selfCheckCacheEntry)
	selfCheckCache.mu.Unlock()
}

// TestClassifySelfCheckOutcomeUnterscheidetUngueltigeZugangsdatenVonNichtErreichbar
// ist der Kern-Test dieser Aufgabe (Schritt 1): bei ungueltigen
// Zugangsdaten muss das Werkzeug "erreichbar, Anmeldung ungueltig"
// melden -- NICHT "nicht erreichbar", der Fall, den Dockhand falsch
// gemeldet hatte.
func TestClassifySelfCheckOutcomeUnterscheidetUngueltigeZugangsdatenVonNichtErreichbar(t *testing.T) {
	out := classifySelfCheckOutcome(fileee.ErrInvalidCredentials)
	if out.Overall != "degraded" || !out.Reachable || out.AuthValid {
		t.Errorf("classifySelfCheckOutcome(ErrInvalidCredentials) = %+v, want Overall=degraded Reachable=true AuthValid=false", out)
	}
	if out.Detail != "reachable, login invalid" {
		t.Errorf("Detail = %q, want %q", out.Detail, "reachable, login invalid")
	}
}

func TestClassifySelfCheckOutcomeUnterscheidetUngueltigenZweitenFaktorVonNichtErreichbar(t *testing.T) {
	out := classifySelfCheckOutcome(fileee.ErrTwoFactorInvalid)
	if out.Overall != "degraded" || !out.Reachable || out.AuthValid {
		t.Errorf("classifySelfCheckOutcome(ErrTwoFactorInvalid) = %+v, want Overall=degraded Reachable=true AuthValid=false", out)
	}
}

func TestClassifySelfCheckOutcomeMeldetErfolgAlsOk(t *testing.T) {
	out := classifySelfCheckOutcome(nil)
	if out.Overall != "ok" || !out.Reachable || !out.AuthValid {
		t.Errorf("classifySelfCheckOutcome(nil) = %+v, want Overall=ok Reachable=true AuthValid=true", out)
	}
	if out.Detail != "reachable, login valid" {
		t.Errorf("Detail = %q, want %q", out.Detail, "reachable, login valid")
	}
}

// TestClassifySelfCheckOutcomeMeldetEinenUnbekanntenFehlerAlsNichtErreichbar
// deckt den dritten Zustand ab: ein Fehler, der keinem der beiden
// bekannten Anmelde-Fehlerwerte entspricht (ein Netzwerkfehler, ein
// 5xx von Fileee, o.ae.), faellt auf "nicht erreichbar".
func TestClassifySelfCheckOutcomeMeldetEinenUnbekanntenFehlerAlsNichtErreichbar(t *testing.T) {
	out := classifySelfCheckOutcome(errors.New("dial tcp: connection refused"))
	if out.Overall != "down" || out.Reachable || out.AuthValid {
		t.Errorf("classifySelfCheckOutcome(Netzwerkfehler) = %+v, want Overall=down Reachable=false AuthValid=false", out)
	}
	if out.Detail != "not reachable" {
		t.Errorf("Detail = %q, want %q", out.Detail, "not reachable")
	}
}

func TestSelfCheckResultForMeldetOkBeiErfolgreicherAnmeldung(t *testing.T) {
	resetSelfCheckCache(t)
	probe := func(context.Context, *identity.Identity) error { return nil }
	id := &identity.Identity{Subject: "alice"}

	out := selfCheckResultFor(context.Background(), probe, id, "alice-account")
	if out.Overall != "ok" || out.Cached {
		t.Errorf("selfCheckResultFor = %+v, want Overall=ok Cached=false", out)
	}
	if out.CheckedAt == "" {
		t.Error("CheckedAt ist leer, obwohl ein echter Versuch stattfand")
	}
}

// TestSelfCheckResultForBegrenztSichSelbst ist Schritt 5's
// Selbstbegrenzungs-Test: zwei Aufrufe kurz hintereinander fuer dasselbe
// Konto duerfen nur EINEN echten Versuch ausloesen -- der zweite bekommt
// das zwischengespeicherte Ergebnis mit demselben Zeitstempel zurueck.
func TestSelfCheckResultForBegrenztSichSelbst(t *testing.T) {
	resetSelfCheckCache(t)
	var calls int32
	probe := func(context.Context, *identity.Identity) error {
		atomic.AddInt32(&calls, 1)
		return fileee.ErrInvalidCredentials
	}
	id := &identity.Identity{Subject: "alice"}

	first := selfCheckResultFor(context.Background(), probe, id, "alice-account")
	second := selfCheckResultFor(context.Background(), probe, id, "alice-account")

	if got := atomic.LoadInt32(&calls); got != 1 {
		t.Errorf("probe wurde %d mal aufgerufen, want genau 1 (zwei Aufrufe kurz hintereinander duerfen nur einen echten Versuch ausloesen)", got)
	}
	if first.Cached {
		t.Error("erster Aufruf: Cached = true, want false (das war der echte Versuch)")
	}
	if !second.Cached {
		t.Error("zweiter Aufruf: Cached = false, want true (das Zeitfenster war noch nicht abgelaufen)")
	}
	if second.CheckedAt != first.CheckedAt {
		t.Errorf("second.CheckedAt = %q, want gleich first.CheckedAt = %q (der Zeitstempel des echten Versuchs, nicht des Cache-Treffers)", second.CheckedAt, first.CheckedAt)
	}
	if second.Overall != first.Overall || second.AuthValid != first.AuthValid || second.Reachable != first.Reachable {
		t.Errorf("second = %+v weicht inhaltlich von first = %+v ab, obwohl es aus dem Cache kam", second, first)
	}
}

// TestSelfCheckResultForBegrenztSichSelbstUnterNebenlaeufigkeit belegt,
// dass die Selbstbegrenzung auch unter echter Nebenlaeufigkeit haelt --
// nicht nur bei zwei sequenziellen Aufrufen. Ohne selfCheckGroup
// (singleflight) koennten mehrere gleichzeitige Aufrufe, die alle noch
// keinen Cache-Eintrag vorfinden, jeder fuer sich einen echten Versuch
// ausloesen.
func TestSelfCheckResultForBegrenztSichSelbstUnterNebenlaeufigkeit(t *testing.T) {
	resetSelfCheckCache(t)
	var calls int32
	probe := func(context.Context, *identity.Identity) error {
		atomic.AddInt32(&calls, 1)
		time.Sleep(10 * time.Millisecond)
		return nil
	}
	id := &identity.Identity{Subject: "alice"}

	const goroutines = 20
	var wg sync.WaitGroup
	wg.Add(goroutines)
	for i := 0; i < goroutines; i++ {
		go func() {
			defer wg.Done()
			selfCheckResultFor(context.Background(), probe, id, "alice-account")
		}()
	}
	wg.Wait()

	if got := atomic.LoadInt32(&calls); got != 1 {
		t.Errorf("probe wurde unter %d gleichzeitigen Aufrufen %d mal ausgefuehrt, want genau 1", goroutines, got)
	}
}

// TestSelfCheckResultForFuehrtNachAblaufDesFenstersErneutEinenEchtenVersuchAus
// belegt die Gegenprobe zur Selbstbegrenzung: nach Ablauf des
// Zeitfensters loest ein weiterer Aufruf sehr wohl einen neuen echten
// Versuch aus -- die Begrenzung ist zeitlich befristet, keine dauerhafte
// Sperre. Das Fenster wird direkt im Cache-Eintrag zurueckdatiert statt
// eine Minute lang zu warten.
func TestSelfCheckResultForFuehrtNachAblaufDesFenstersErneutEinenEchtenVersuchAus(t *testing.T) {
	resetSelfCheckCache(t)
	var calls int32
	probe := func(context.Context, *identity.Identity) error {
		atomic.AddInt32(&calls, 1)
		return nil
	}
	id := &identity.Identity{Subject: "alice"}

	selfCheckResultFor(context.Background(), probe, id, "alice-account")

	selfCheckCache.mu.Lock()
	entry := selfCheckCache.byAccount["alice-account"]
	entry.at = entry.at.Add(-2 * selfCheckMinInterval)
	selfCheckCache.byAccount["alice-account"] = entry
	selfCheckCache.mu.Unlock()

	out := selfCheckResultFor(context.Background(), probe, id, "alice-account")
	if out.Cached {
		t.Error("Cached = true, want false -- das Zeitfenster war abgelaufen, ein neuer echter Versuch war faellig")
	}
	if got := atomic.LoadInt32(&calls); got != 2 {
		t.Errorf("probe wurde %d mal aufgerufen, want 2 (einmal vor, einmal nach Ablauf des Fensters)", got)
	}
}

// TestSelfCheckResultForBegrenztSichSelbstJeKontoUnabhaengig belegt, dass
// die Selbstbegrenzung pro KONTO gilt, nicht global -- zwei verschiedene
// Konten duerfen sich nicht gegenseitig blockieren.
func TestSelfCheckResultForBegrenztSichSelbstJeKontoUnabhaengig(t *testing.T) {
	resetSelfCheckCache(t)
	var calls int32
	probe := func(context.Context, *identity.Identity) error {
		atomic.AddInt32(&calls, 1)
		return nil
	}

	selfCheckResultFor(context.Background(), probe, &identity.Identity{Subject: "alice"}, "alice-account")
	selfCheckResultFor(context.Background(), probe, &identity.Identity{Subject: "bob"}, "bob-account")

	if got := atomic.LoadInt32(&calls); got != 2 {
		t.Errorf("probe wurde %d mal aufgerufen, want 2 -- zwei verschiedene Konten duerfen sich nicht gegenseitig begrenzen", got)
	}
}

// TestSelfCheckGibtNieDenFehlertextDerGegenseiteWeiter ist Schritt 6:
// selbst wenn die Anmeldeprobe einen Fehler mit geheimnisartigem Text
// liefert, darf dieser Text in der GESAMTEN Ausgabe nirgends auftauchen
// -- nur classifySelfCheckOutcome's feste Einordnung. Gleiche Bauart wie
// TestLogToolEndVerzeichnetNieDenFehlertextNurDieEinordnung oben: die
// gesamte Ausgabe wird als Zeichenkette geprueft, nicht nur ein
// einzelnes Feld.
func TestSelfCheckGibtNieDenFehlertextDerGegenseiteWeiter(t *testing.T) {
	resetSelfCheckCache(t)
	geheimnisartig := errors.New("login failed for password Sonnenblumenkern-Zweitausendsechsundzwanzig")
	probe := func(context.Context, *identity.Identity) error { return geheimnisartig }
	id := &identity.Identity{Subject: "alice"}

	out := selfCheckResultFor(context.Background(), probe, id, "leak-account")

	voll := fmt.Sprintf("%+v", out)
	if strings.Contains(voll, "Sonnenblumenkern") {
		t.Fatalf("self_check-Ausgabe enthaelt den Fehlertext der Gegenseite: %s", voll)
	}
	if out.Overall != "down" {
		t.Errorf("Overall = %q, want %q (unbekannter Fehler faellt auf 'nicht erreichbar')", out.Overall, "down")
	}
}

func TestGetSelfCheckOutputFeldlisteIstAbgeschlossen(t *testing.T) {
	want := []string{"Overall", "Reachable", "AuthValid", "Detail", "CheckedAt", "Cached"}
	got := fieldNames(getSelfCheckOutput{})
	if !reflect.DeepEqual(got, want) {
		t.Errorf("getSelfCheckOutput-Feldliste = %v, want %v", got, want)
	}
}

// TestClassifySelfCheckOutcomeBehandeltEineKontosperreAlsNichtErreichbar
// dokumentiert eine bewusste Entscheidung, keine Luecke: *fileee.BlockedError
// ist auf dem Weg, den ProbeLogin tatsaechlich nutzt (Client.Login ->
// login(), NICHT EnsureSession/ensureSession), strukturell unerreichbar --
// gegen go-fileee v0.2.0 selbst geprueft (auth.go:157-247 vs. auth.go:301).
// Ein eigener "blocked"-Zustand wurde deshalb kurzzeitig ergaenzt
// (Nachtrag nach einer Rueckmeldung, die den Fehlertyp nannte, ohne den
// erzeugenden Pfad zu pruefen) und wieder entfernt, statt eine
// Faehigkeit vorzutaeuschen, die self_check auf diesem Pfad nicht hat.
// Sollte BlockedError trotzdem einmal ankommen (z.B. weil sich
// go-fileee's login() aendert), faellt er bewusst auf "down" -- korrekt
// im Sinn von "nicht auswertbar", nicht falsch im Sinn von "degraded"
// (das wuerde einen Aufrufer dazu verleiten, ein korrektes Passwort zu
// ersetzen und die Sperre damit zu verlaengern).
func TestClassifySelfCheckOutcomeBehandeltEineKontosperreAlsNichtErreichbar(t *testing.T) {
	out := classifySelfCheckOutcome(&fileee.BlockedError{SecondsBlocked: 42})
	if out.Overall != "down" {
		t.Errorf("classifySelfCheckOutcome(BlockedError) = %+v, want Overall=down (kein eigener Zustand -- siehe Testkommentar)", out)
	}
}

func TestGetSelfCheckInputNimmtKeineParameterEntgegen(t *testing.T) {
	got := fieldNames(getSelfCheckInput{})
	if len(got) != 0 {
		t.Errorf("getSelfCheckInput hat Felder %v, want keine", got)
	}
}

// TestGetSelfCheckHandlerVerweigertOhneVerifizierteIdentitaet belegt den
// ersten Ausstiegspunkt des Handlers: ohne Identitaet im Context (die
// serve.IdentityFrom liefern wuerde) gibt es keinen Anmeldeversuch --
// derselbe erste Schritt, den clientFor (read.go) fuer jedes andere
// Werkzeug in diesem Paket auch macht.
func TestGetSelfCheckHandlerVerweigertOhneVerifizierteIdentitaet(t *testing.T) {
	handler := getSelfCheckHandler((*clientpool.Pool)(nil), discardLogger())
	_, _, err := handler(context.Background(), nil, getSelfCheckInput{})
	if err == nil {
		t.Fatal("getSelfCheckHandler ohne Identitaet im Context: err = nil, want Fehler")
	}
}

func TestRegisterOpsToolsMeldetSelfCheckAn(t *testing.T) {
	s := mcp.NewServer(&mcp.Implementation{Name: "probe", Version: "0"}, nil)

	registerOpsTools(s, (*clientpool.Pool)(nil), ServerInfo{}, discardLogger())

	names := toolNamesOf(t, s)
	if !names[ToolSelfCheck] {
		t.Errorf("Werkzeug %q wurde nicht angemeldet", ToolSelfCheck)
	}
}
