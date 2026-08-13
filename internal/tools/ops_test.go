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
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

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

	registerOpsTools(s, (*clientpool.Pool)(nil), discardLogger())

	names := toolNamesOf(t, s)
	if !names[ToolGetRuntimeStats] {
		t.Errorf("Werkzeug %q wurde nicht angemeldet", ToolGetRuntimeStats)
	}
}
