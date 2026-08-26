// White-box tests for read_sync.go's cursor codec, its EntityType guard,
// and registerSync's own leak check (shared with registerReadService via
// mustNotLeakUntrustedText — see that function's own doc comment in
// read_generic.go).
package tools

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/strausmann/go-fileee/fileee"

	"github.com/strausmann/fileee-mcp-server/internal/clientpool"
)

// --- Schritt 1: Cursor-Kodierung und Typpruefung ---

func TestCursorRundlaufErhaeltDenWert(t *testing.T) {
	original := fileee.NewCursor("Tag")
	original.Known["t1"] = 3

	encoded, err := encodeCursor(original)
	if err != nil {
		t.Fatalf("encodeCursor: %v", err)
	}
	decoded, err := decodeCursor(encoded)
	if err != nil {
		t.Fatalf("decodeCursor: %v", err)
	}
	if decoded.EntityType != original.EntityType || decoded.Known["t1"] != original.Known["t1"] {
		t.Errorf("Rundlauf verlor Daten: %+v != %+v", decoded, original)
	}
}

func TestDecodeCursorLehntUnsinnAb(t *testing.T) {
	if _, err := decodeCursor("kein-gueltiger-cursor"); err == nil {
		t.Error("decodeCursor akzeptierte eine ungueltige Zeichenkette")
	}
}

func TestSyncLehntVertauschtenCursorAb(t *testing.T) {
	falscher, err := encodeCursor(fileee.NewCursor("Tag"))
	if err != nil {
		t.Fatalf("encodeCursor: %v", err)
	}
	// syncDescriptor fuer Company erwartet EntityType "Company" -- ein
	// Tag-Cursor muss VOR jedem Diff-Aufruf abgelehnt werden, nicht erst an
	// der Gegenseite.
	if _, err := checkCursorEntityType(falscher, "Company"); err == nil {
		t.Error("ein Cursor fuer einen anderen Dienst wurde nicht abgelehnt")
	}
}

func TestCheckCursorEntityTypeLiefertErstabgleichBeiLeererZeichenkette(t *testing.T) {
	cursor, err := checkCursorEntityType("", "Tag")
	if err != nil {
		t.Fatalf("checkCursorEntityType: %v", err)
	}
	if cursor.EntityType != "Tag" {
		t.Errorf("EntityType = %q, want %q", cursor.EntityType, "Tag")
	}
	if len(cursor.Known) != 0 {
		t.Errorf("Erstabgleich-Cursor traegt bereits bekannte Eintraege: %+v", cursor.Known)
	}
}

func TestCheckCursorEntityTypeAkzeptiertPassendenCursor(t *testing.T) {
	original := fileee.NewCursor("Company")
	original.Known["c1"] = 5
	encoded, err := encodeCursor(original)
	if err != nil {
		t.Fatalf("encodeCursor: %v", err)
	}

	cursor, err := checkCursorEntityType(encoded, "Company")
	if err != nil {
		t.Fatalf("checkCursorEntityType: %v", err)
	}
	if cursor.Known["c1"] != 5 {
		t.Errorf("Known[c1] = %d, want 5", cursor.Known["c1"])
	}
}

// --- syncDescriptor / registerSync ---
//
// These tests exercise the REAL production descriptors (tagSyncDescriptor
// et al., read_sync.go) directly rather than a local test fixture —
// unlike read_generic_test.go's tagDescriptor(), which stands in for a
// descriptor Aufgabe 3 has not written yet, tagSyncDescriptor() already
// exists in this package by the time this file compiles (it is this
// file's own deliverable), so there is nothing left to stand in for.

func TestRegisterSyncMeldetDasWerkzeugAn(t *testing.T) {
	s := mcp.NewServer(&mcp.Implementation{Name: "probe", Version: "0"}, nil)

	registerSync(s, (*clientpool.Pool)(nil), discardLogger(), tagSyncDescriptor())

	names := toolNamesOf(t, s)
	if !names["sync_tags"] {
		t.Error("sync_tags wurde nicht angemeldet")
	}
}

// fakeSyncService ist fakeReadService's Gegenstueck fuer die sync-eigenen Fehlerpfad-Tests: kein
// eigener Typ noetig, fakeReadService (read_generic_test.go) implementiert bereits
// fileee.ReadService[T] vollstaendig inklusive Diff.

func TestSyncFromServiceWickeltEinenGegenseitenFehlerMitDemWerkzeugnamenEin(t *testing.T) {
	backendErr := errors.New("Gegenseite antwortet nicht")
	d := tagSyncDescriptor()
	service := &fakeReadService[fileee.Tag]{diffErr: backendErr}

	_, _, err := syncFromService(context.Background(), d, service, fileee.NewCursor("Tag"))
	if err == nil {
		t.Fatal("erwarteter Fehler blieb aus")
	}
	if !errors.Is(err, backendErr) {
		t.Errorf("Fehler wickelt %v nicht ein, bekam: %v", backendErr, err)
	}
	if !strings.Contains(err.Error(), d.SyncName) {
		t.Errorf("Fehlermeldung %q enthaelt nicht den Werkzeugnamen %q", err.Error(), d.SyncName)
	}
}

func TestGenericSyncHandlerLehntVertauschtenCursorOhneNetzwerkzugriffAb(t *testing.T) {
	d := tagSyncDescriptor()
	falscher, err := encodeCursor(fileee.NewCursor("Company"))
	if err != nil {
		t.Fatalf("encodeCursor: %v", err)
	}
	// p bleibt nil: erreicht der Handler clientFor doch noch, bricht der Test mit einer
	// Nil-Pointer-Dereferenzierung ab statt still zu bestehen -- das ist der Beleg, dass der
	// vertauschte Cursor VOR jedem Netzwerkzugriff abgefangen wird (derselbe Aufbau wie
	// TestGenericGetHandlerLehntEineLeereKennungOhneNetzwerkzugriffAb in read_generic_test.go).
	handler := genericSyncHandler[fileee.Tag, syncTagSummary](nil, discardLogger(), d)

	_, _, err = handler(context.Background(), nil, genericSyncInput{Cursor: falscher})
	if err == nil {
		t.Fatal("erwarteter Fehler blieb aus")
	}
	if !strings.Contains(err.Error(), d.SyncName) {
		t.Errorf("Fehlermeldung %q enthaelt nicht den Werkzeugnamen %q", err.Error(), d.SyncName)
	}
}

func TestGenericSyncHandlerLehntUngueltigenCursorOhneNetzwerkzugriffAb(t *testing.T) {
	d := tagSyncDescriptor()
	handler := genericSyncHandler[fileee.Tag, syncTagSummary](nil, discardLogger(), d)

	_, _, err := handler(context.Background(), nil, genericSyncInput{Cursor: "kein-gueltiger-cursor"})
	if err == nil {
		t.Fatal("erwarteter Fehler blieb aus")
	}
	if !strings.Contains(err.Error(), d.SyncName) {
		t.Errorf("Fehlermeldung %q enthaelt nicht den Werkzeugnamen %q", err.Error(), d.SyncName)
	}
}

func TestSyncFromServiceKodiertDenFolgeCursor(t *testing.T) {
	d := tagSyncDescriptor()
	service := &fakeReadService[fileee.Tag]{
		diffResult: &fileee.DiffResult[fileee.Tag]{
			Rows:      []fileee.Tag{{ID: "t1", Name: "Rechnung"}},
			TotalRows: 1,
			NextCursor: fileee.Cursor{
				EntityType: "Tag",
				Known:      map[string]int64{"t1": 3},
			},
		},
	}

	_, out, err := syncFromService(context.Background(), d, service, fileee.NewCursor("Tag"))
	if err != nil {
		t.Fatalf("syncFromService: %v", err)
	}
	if len(out.Entries) != 1 || out.Entries[0].ID != "t1" {
		t.Errorf("Entries = %+v, want ein Eintrag mit ID t1", out.Entries)
	}
	if out.TotalRows != 1 {
		t.Errorf("TotalRows = %d, want 1", out.TotalRows)
	}

	decoded, err := decodeCursor(out.NextCursor)
	if err != nil {
		t.Fatalf("decodeCursor(NextCursor): %v", err)
	}
	if decoded.EntityType != "Tag" || decoded.Known["t1"] != 3 {
		t.Errorf("dekodierter Folge-Cursor = %+v, will EntityType=Tag Known[t1]=3", decoded)
	}
}

// --- mustNotLeakUntrustedText ueber registerSync erzwungen (dasselbe Muster wie
// registerReadService in read_generic_test.go) ---

// contactLikeSync steht fuer einen Diensttyp mit fremdbestimmtem Text -- derselbe Aufbau wie
// contactLike in read_generic_test.go, hier lokal, weil syncDescriptor einen anderen
// Feldsatz traegt als readServiceDescriptor.
type contactLikeSync struct {
	ID       string
	LastName string
}

func TestRegisterSyncPanictWennSummarizeFremdtextReproduziert(t *testing.T) {
	d := syncDescriptor[contactLikeSync, struct {
		ID       string
		LastName string // Fehler unter Test: taucht auch in UntrustedLine auf
	}]{
		SyncName:        "sync_contact_like",
		SyncDescription: descriptionFixture,
		EntityType:      "ContactLike",
		Service: func(*fileee.Client) fileee.ReadService[contactLikeSync] {
			return &fakeReadService[contactLikeSync]{}
		},
		Summarize: func(c *contactLikeSync) struct {
			ID       string
			LastName string
		} {
			return struct {
				ID       string
				LastName string
			}{ID: c.ID, LastName: c.LastName}
		},
		UntrustedLine: func(c *contactLikeSync) string { return "Max " + c.LastName },
		PoisonProbe:   func(marker string) *contactLikeSync { return &contactLikeSync{LastName: marker} },
	}

	defer func() {
		if r := recover(); r == nil {
			t.Error("erwartete Panic (Summarize reproduziert UntrustedLine's Fremdtext) blieb aus")
		}
	}()
	registerSync(mcp.NewServer(&mcp.Implementation{Name: "probe", Version: "0"}, nil), (*clientpool.Pool)(nil), discardLogger(), d)
}

func TestRegisterSyncPanictWennPoisonProbeFehlt(t *testing.T) {
	d := tagSyncDescriptorMitFremdtext()
	d.PoisonProbe = nil

	defer func() {
		if r := recover(); r == nil {
			t.Error("erwartete Panic (PoisonProbe fehlt) blieb aus")
		}
	}()
	registerSync(mcp.NewServer(&mcp.Implementation{Name: "probe", Version: "0"}, nil), (*clientpool.Pool)(nil), discardLogger(), d)
}

func TestRegisterSyncPanictWennPoisonProbeOhneUntrustedLineGesetztIst(t *testing.T) {
	d := tagSyncDescriptorMitFremdtext()
	d.UntrustedLine = nil // PoisonProbe bleibt gesetzt

	defer func() {
		if r := recover(); r == nil {
			t.Error("erwartete Panic (PoisonProbe gesetzt, UntrustedLine nil) blieb aus")
		}
	}()
	registerSync(mcp.NewServer(&mcp.Implementation{Name: "probe", Version: "0"}, nil), (*clientpool.Pool)(nil), discardLogger(), d)
}

// TestMustNotLeakUntrustedTextMeldetSyncDeskriptorTyp ist
// TestMustNotLeakUntrustedTextMeldetDenDeskriptorTyp's Gegenstueck
// (read_generic_test.go) fuer syncDescriptor -- derselbe Meldungstext-Verlust
// waere hier genauso unbemerkt geblieben, haette registerSync's eigener
// Aufruf von mustNotLeakUntrustedText "syncDescriptor" nicht mitgegeben.
func TestMustNotLeakUntrustedTextMeldetSyncDeskriptorTyp(t *testing.T) {
	d := tagSyncDescriptorMitFremdtext()
	d.PoisonProbe = nil // UntrustedLine bleibt gesetzt

	defer func() {
		r := recover()
		if r == nil {
			t.Fatal("erwartete Panic blieb aus")
		}
		msg, ok := r.(string)
		if !ok {
			t.Fatalf("Panic-Wert ist kein string: %v", r)
		}
		if !strings.Contains(msg, "syncDescriptor") {
			t.Errorf("Panic-Meldung %q nennt nicht den Deskriptor-Typ syncDescriptor", msg)
		}
	}()
	registerSync(mcp.NewServer(&mcp.Implementation{Name: "probe", Version: "0"}, nil), (*clientpool.Pool)(nil), discardLogger(), d)
}

func TestRegisterSyncOhneFremdtextfelderBleibtSauber(t *testing.T) {
	// tagSyncDescriptor() laesst UntrustedLine/PoisonProbe bewusst nil -- Tag traegt keinen
	// Fremdtext (dieselbe Einstufung wie Aufgabe 3's tag-Deskriptor, read_generic.go). Das
	// darf NICHT paniken.
	registerSync(mcp.NewServer(&mcp.Implementation{Name: "probe", Version: "0"}, nil), (*clientpool.Pool)(nil), discardLogger(), tagSyncDescriptor())
}

// tagSyncDescriptorMitFremdtext ist tagSyncDescriptor() plus einem funktionierenden
// UntrustedLine/PoisonProbe-Paar -- Grundlage fuer die beiden Panic-Tests oben, die je genau
// ein Feld davon kaputt machen.
func tagSyncDescriptorMitFremdtext() syncDescriptor[fileee.Tag, syncTagSummary] {
	d := tagSyncDescriptor()
	d.UntrustedLine = func(tag *fileee.Tag) string { return tag.Name }
	d.PoisonProbe = func(marker string) *fileee.Tag { return &fileee.Tag{ID: "t1", Name: marker} }
	return d
}

// --- Platzhalter-Zusammenfassungsstrukturen: keine Ueberschneidung mit Summarize-Feldern ---

// TestSyncCompanySummaryEnthaeltKeinenFirmennamen ist die Regression fuer
// Aufgabe 3's eigenen Fund: companySyncDescriptor trug bis dahin weder
// UntrustedLine noch PoisonProbe, obwohl eine automatisch aus einem
// Dokument extrahierte Company (FromUserDB == false) ihren Namen vom
// Absender dieses Dokuments erbt, nicht vom Kontoinhaber — dieselbe
// Einstufung wie bei Contact. Siehe read_sync.go, syncCompanySummary's
// eigener Kommentar.
func TestSyncCompanySummaryEnthaeltKeinenFirmennamen(t *testing.T) {
	d := companySyncDescriptor()
	marker := "poison-marker-fuer-diesen-test"
	entry := d.PoisonProbe(marker)

	if !strings.Contains(d.UntrustedLine(entry), marker) {
		t.Fatalf("UntrustedLine liest nicht das von PoisonProbe gesetzte Feld")
	}
	summary := d.Summarize(entry)
	v := reflect.ValueOf(summary)
	for i := 0; i < v.NumField(); i++ {
		if s, ok := v.Field(i).Interface().(string); ok && strings.Contains(s, marker) {
			t.Fatalf("Feld %q enthaelt fremdbestimmten Text", v.Type().Field(i).Name)
		}
	}
}

func TestSyncContactSummaryEnthaeltKeinenAnzeigenamen(t *testing.T) {
	d := contactSyncDescriptor()
	marker := "poison-marker-fuer-diesen-test"
	entry := d.PoisonProbe(marker)

	if !strings.Contains(d.UntrustedLine(entry), marker) {
		t.Fatalf("UntrustedLine liest nicht das von PoisonProbe gesetzte Feld")
	}
	summary := d.Summarize(entry)
	v := reflect.ValueOf(summary)
	for i := 0; i < v.NumField(); i++ {
		if s, ok := v.Field(i).Interface().(string); ok && strings.Contains(s, marker) {
			t.Fatalf("Feld %q enthaelt fremdbestimmten Text", v.Type().Field(i).Name)
		}
	}
}

func TestSyncReminderSummaryEnthaeltKeineBeschreibung(t *testing.T) {
	d := reminderSyncDescriptor()
	marker := "poison-marker-fuer-diesen-test"
	entry := d.PoisonProbe(marker)

	if !strings.Contains(d.UntrustedLine(entry), marker) {
		t.Fatalf("UntrustedLine liest nicht das von PoisonProbe gesetzte Feld")
	}
	summary := d.Summarize(entry)
	v := reflect.ValueOf(summary)
	for i := 0; i < v.NumField(); i++ {
		if s, ok := v.Field(i).Interface().(string); ok && strings.Contains(s, marker) {
			t.Fatalf("Feld %q enthaelt fremdbestimmten Text", v.Type().Field(i).Name)
		}
	}
}

func TestSyncConversationSummaryEnthaeltKeinenBetreff(t *testing.T) {
	d := conversationSyncDescriptor()
	marker := "poison-marker-fuer-diesen-test"
	entry := d.PoisonProbe(marker)

	if !strings.Contains(d.UntrustedLine(entry), marker) {
		t.Fatalf("UntrustedLine liest nicht das von PoisonProbe gesetzte Feld")
	}
	summary := d.Summarize(entry)
	v := reflect.ValueOf(summary)
	for i := 0; i < v.NumField(); i++ {
		if s, ok := v.Field(i).Interface().(string); ok && strings.Contains(s, marker) {
			t.Fatalf("Feld %q enthaelt fremdbestimmten Text", v.Type().Field(i).Name)
		}
	}
}

// --- registerSyncTools: alle sieben Werkzeuge, keins darf beim Anmelden paniken ---

func TestRegisterSyncToolsMeldetAlleSiebenAn(t *testing.T) {
	s := mcp.NewServer(&mcp.Implementation{Name: "probe", Version: "0"}, nil)

	registerSyncTools(s, (*clientpool.Pool)(nil), discardLogger())

	names := toolNamesOf(t, s)
	want := []string{
		ToolSyncTags, ToolSyncCompanies, ToolSyncDocumentTypes, ToolSyncDocumentTypeSchemes,
		ToolSyncContacts, ToolSyncReminders, ToolSyncConversations,
	}
	for _, name := range want {
		if !names[name] {
			t.Errorf("Werkzeug %q wurde nicht angemeldet", name)
		}
	}
}

// --- Aufgabe 4: IDOf liefert die Fileee-eigene ID -----------------------

// TestSyncDeskriptorenLiefernDieFileeeEigeneIDUeberIDOf belegt Aufgabe 4's
// Pflichtfeld für alle sieben Deskriptoren dieser Datei — dasselbe Muster
// wie read_reference_test.go's/read_people_test.go's eigene Gegenstücke.
func TestSyncDeskriptorenLiefernDieFileeeEigeneIDUeberIDOf(t *testing.T) {
	if got := tagSyncDescriptor().IDOf(&fileee.Tag{ID: "tag-1"}); got != "tag-1" {
		t.Errorf("tagSyncDescriptor().IDOf = %q, want %q", got, "tag-1")
	}
	if got := companySyncDescriptor().IDOf(&fileee.Company{ID: "company-1"}); got != "company-1" {
		t.Errorf("companySyncDescriptor().IDOf = %q, want %q", got, "company-1")
	}
	if got := documentTypeSyncDescriptor().IDOf(&fileee.DocumentType{ID: "doctype-1"}); got != "doctype-1" {
		t.Errorf("documentTypeSyncDescriptor().IDOf = %q, want %q", got, "doctype-1")
	}
	if got := documentTypeSchemeSyncDescriptor().IDOf(&fileee.DocumentTypeScheme{ID: "scheme-1"}); got != "scheme-1" {
		t.Errorf("documentTypeSchemeSyncDescriptor().IDOf = %q, want %q", got, "scheme-1")
	}
	if got := contactSyncDescriptor().IDOf(&fileee.Contact{ID: "contact-1"}); got != "contact-1" {
		t.Errorf("contactSyncDescriptor().IDOf = %q, want %q", got, "contact-1")
	}
	if got := reminderSyncDescriptor().IDOf(&fileee.Reminder{ID: "reminder-1"}); got != "reminder-1" {
		t.Errorf("reminderSyncDescriptor().IDOf = %q, want %q", got, "reminder-1")
	}
	if got := conversationSyncDescriptor().IDOf(&fileee.Conversation{ID: "conversation-1"}); got != "conversation-1" {
		t.Errorf("conversationSyncDescriptor().IDOf = %q, want %q", got, "conversation-1")
	}
}
