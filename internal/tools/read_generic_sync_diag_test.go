// read_generic_sync_diag_test.go covers Aufgabe 2c's own acceptance
// criterion: registerReadService's and registerSync's handlers must log
// through the *slog.Logger their registration functions now take —
// exactly the pattern listDocumentsHandler/searchDocumentsHandler already
// establish (read.go, read_diag_test.go). Neither family threaded a
// logger through before this task (Antrag #45/#46 both shipped without
// it); this file is the failing-first proof that they do now, plus the
// masking gegenprobe the task order calls for as its own step.
//
// context.Background() carries no verified identity (serve.IdentityFrom
// only ever returns ok when the context came through gangway's own
// unexported wiring — see clientFor's own doc comment, read.go), so every
// handler call below reaches clientFor and stops there. That is
// deliberate, not a shortcut around a harder test: package tools (this
// white-box test file) has no way to fabricate a verified identity
// (unlike package tools_test's read_diag_test.go, which drives a real
// gangway+HTTP round trip for RegisterAll's own two tools), and a
// call that fails at clientFor still exercises exactly what this task
// is about — logToolStart fires before clientFor runs at all, and
// logToolEnd fires on the error path, both through the logger each
// handler factory below was actually given. p stays nil throughout: the
// same precondition TestGenericGetHandlerLehntEineLeereKennungOhneNetzwerkzugriffAb
// and this file's own tests below rely on — clientFor's identity check
// runs before it would ever dereference p.
package tools

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"github.com/strausmann/go-fileee/fileee"

	"github.com/strausmann/fileee-mcp-server/internal/diag"
)

// gegenprobeSecretValue ist frei erfunden und beruehrt keinen echten
// Zugangsdaten-Pfad — dieselbe Konvention wie read_diag_test.go's eigene
// secretTitle/secretBackendMessage-Fixtures (lesbarer Satz statt
// zufaellig aussehender Zeichenfolge, damit automatisierte
// Secret-Scanner den Test-Fixture-Wert nicht mit einem echten,
// hochentropischen Token verwechseln). Was diesen Wert zur Gegenprobe
// macht, ist ausschliesslich der Attribut-SCHLUESSEL an der Aufrufstelle
// unten ("api_token"/"session_secret") — internal/diag's
// forbiddenKeyFragments gleicht ueber den Schluessel ab, nie ueber den
// Wert (siehe dessen eigener Kommentar).
const gegenprobeSecretValue = "Do-Not-Log-Me API Token 2026"

// TestGenericListHandlerLogsToolCallWithName ist Schritt 1/2 fuer die
// readServiceDescriptor-Familie: ein Aufruf ueber genericListHandler —
// denselben Handler, den registerReadService anmeldet — muss den
// Werkzeugnamen ins Protokoll schreiben, sobald ein Logger uebergeben
// wird. Vor der Aenderung nimmt genericListHandler gar keinen Logger
// entgegen; dieser Test schlaegt bis dahin schon beim Kompilieren fehl.
func TestGenericListHandlerLogsToolCallWithName(t *testing.T) {
	var buf bytes.Buffer
	logger := diag.New(diag.LevelDebug, &buf)
	d := tagDescriptor()

	handler := genericListHandler[fileee.Tag, tagSummary](nil, logger, d)
	_, _, err := handler(context.Background(), nil, genericListInput{})
	if err == nil {
		t.Fatal("erwarteter Fehler (keine verifizierte Identitaet im Kontext) blieb aus")
	}

	if !strings.Contains(buf.String(), d.ListName) {
		t.Errorf("Protokoll enthaelt nicht den Werkzeugnamen %q: %s", d.ListName, buf.String())
	}
}

// TestGenericGetHandlerLogsToolCallWithName ist dieselbe Pruefung fuer
// genericGetHandler, dazu die Gegenprobe aus Schritt 5: derselbe Logger
// muss weiterhin maskieren, wenn er direkt mit einem geheimnisartigen
// Schluessel angesprochen wird — der Beleg, dass genericGetHandler den
// uebergebenen, bereits maskierenden Logger tatsaechlich weiterreicht statt
// einen eigenen, unmaskierten aufzubauen (internal/diag's
// Maskierungs-Zusage gilt fuer JEDEN Aufrufer desselben *slog.Logger,
// unabhaengig davon, welches Paket den konkreten Handle-Aufruf macht —
// siehe internal/diag's eigener Kommentar).
func TestGenericGetHandlerLogsToolCallWithName(t *testing.T) {
	var buf bytes.Buffer
	logger := diag.New(diag.LevelDebug, &buf)
	d := tagDescriptor()

	handler := genericGetHandler[fileee.Tag, tagSummary](nil, logger, d, nil)
	_, _, err := handler(context.Background(), nil, genericGetInput{ID: "   "})
	if err == nil {
		t.Fatal("erwarteter Fehler (leere Kennung) blieb aus")
	}

	if !strings.Contains(buf.String(), d.GetName) {
		t.Errorf("Protokoll enthaelt nicht den Werkzeugnamen %q: %s", d.GetName, buf.String())
	}

	logger.Info("gegenprobe", "api_token", gegenprobeSecretValue)
	if strings.Contains(buf.String(), gegenprobeSecretValue) {
		t.Errorf("Maskierung griff nicht — der Geheimwert erscheint im Protokoll: %s", buf.String())
	}
}

// TestGenericSyncHandlerLogsToolCallWithName ist dieselbe Pruefung fuer
// die syncDescriptor-Familie (genericSyncHandler, registerSync,
// read_sync.go), inklusive derselben Gegenprobe.
func TestGenericSyncHandlerLogsToolCallWithName(t *testing.T) {
	var buf bytes.Buffer
	logger := diag.New(diag.LevelDebug, &buf)
	d := tagSyncDescriptor()

	handler := genericSyncHandler[fileee.Tag, syncTagSummary](nil, logger, d)
	_, _, err := handler(context.Background(), nil, genericSyncInput{})
	if err == nil {
		t.Fatal("erwarteter Fehler (keine verifizierte Identitaet im Kontext) blieb aus")
	}

	if !strings.Contains(buf.String(), d.SyncName) {
		t.Errorf("Protokoll enthaelt nicht den Werkzeugnamen %q: %s", d.SyncName, buf.String())
	}

	logger.Info("gegenprobe", "session_secret", gegenprobeSecretValue)
	if strings.Contains(buf.String(), gegenprobeSecretValue) {
		t.Errorf("Maskierung griff nicht — der Geheimwert erscheint im Protokoll: %s", buf.String())
	}
}
