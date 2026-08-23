// White-box tests for write.go's write-class tool registration and its
// first tool, update_contact — the same pattern read_account_test.go and
// read_boxes_test.go already establish for their own bespoke handlers:
// a narrow fake (fakeContactWriteService) drives the client-resolution-
// free logic (updateContactFromService) directly, and a second, thin
// test (TestUpdateContactHandlerLehntEineLeereKennungOhneNetzwerkzugriffAb)
// exercises the one branch of updateContactHandler itself that runs
// before clientFor and is therefore reachable without a real gangway/
// HTTP round trip (see read_generic_test.go's own doc comment on why
// clientFor cannot be driven from a package-level unit test at all).
//
// Mutations-Test-Pflicht (test-coverage-pflicht.md, homelab-management
// repo): every write tool needs Happy-Path + Backend-error (4xx/5xx) +
// Network-error coverage, not just a success case — this file's three
// TestUpdateContactFromService... tests are exactly that triad.
package tools

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/strausmann/go-fileee/fileee"

	"github.com/strausmann/fileee-mcp-server/internal/clientpool"
)

func TestUpdateContactInputFeldlisteIstAbgeschlossen(t *testing.T) {
	want := []string{"ID", "FirstName", "LastName", "CompanyName", "Email", "PhoneNumber", "FaxNumber", "URL"}
	got := fieldNames(updateContactInput{})
	if !reflect.DeepEqual(got, want) {
		t.Errorf("updateContactInput-Feldliste = %v, want %v", got, want)
	}
}

func TestUpdateContactOutputFeldlisteIstAbgeschlossen(t *testing.T) {
	// KEIN Fremdtext-Feld hier — der Anzeigename landet ausschliesslich
	// gerahmt in CallToolResult.Content (siehe
	// TestUpdateContactPatchMerge unten), nie strukturiert in
	// CallToolResult.StructuredContent. Eine dritte Feldliste hier waere
	// exakt der Fehler, den die Fix-Runde behoben hat.
	want := []string{"ID", "Modified"}
	got := fieldNames(updateContactOutput{})
	if !reflect.DeepEqual(got, want) {
		t.Errorf("updateContactOutput-Feldliste = %v, want %v", got, want)
	}
}

func TestRegisterWriteToolsMeldetUpdateContactAn(t *testing.T) {
	s := mcp.NewServer(&mcp.Implementation{Name: "probe", Version: "0"}, nil)

	registerWriteTools(s, (*clientpool.Pool)(nil), discardLogger())

	names := toolNamesOf(t, s)
	if !names[ToolUpdateContact] {
		t.Errorf("Werkzeug %q wurde nicht angemeldet", ToolUpdateContact)
	}
}

// TestUpdateContactWerkzeugIstAlsSchreibendUndDestruktivAnnotiert belegt
// den Auftrag woertlich: update_contact traegt ReadOnlyHint:false (es ist
// KEIN Lesewerkzeug wie jedes andere in diesem Paket),
// DestructiveHint:true (ein Update kann bestehende Feldwerte
// ueberschreiben) und IdempotentHint:true (derselbe Patch zweimal
// angewendet aendert den Kontakt kein zweites Mal).
func TestUpdateContactWerkzeugIstAlsSchreibendUndDestruktivAnnotiert(t *testing.T) {
	var found *mcp.Tool
	for _, tool := range registeredReadTools() {
		if tool.Name == ToolUpdateContact {
			found = tool
		}
	}
	if found == nil {
		t.Fatalf("Werkzeug %q wurde nicht angemeldet", ToolUpdateContact)
	}
	if found.Annotations == nil {
		t.Fatalf("Werkzeug %q hat keine Annotations", ToolUpdateContact)
	}
	if found.Annotations.Title != "Update contact" {
		t.Errorf("Title = %q, want %q", found.Annotations.Title, "Update contact")
	}
	if found.Annotations.ReadOnlyHint {
		t.Error("ReadOnlyHint = true, want false — update_contact schreibt")
	}
	if found.Annotations.DestructiveHint == nil || !*found.Annotations.DestructiveHint {
		t.Errorf("DestructiveHint = %v, want a pointer to true — ein Update kann bestehende Werte ueberschreiben",
			found.Annotations.DestructiveHint)
	}
	if !found.Annotations.IdempotentHint {
		t.Error("IdempotentHint = false, want true — derselbe Patch zweimal aendert nichts zusaetzlich")
	}
}

// fakeContactWriteService is contactWriteService's test double.
type fakeContactWriteService struct {
	getCalled bool
	getResult *fileee.Contact
	getErr    error

	updateCalledWith *fileee.Contact
	updateResult     *fileee.Contact
	updateErr        error
}

func (f *fakeContactWriteService) Get(_ context.Context, _ string) (*fileee.Contact, error) {
	f.getCalled = true
	if f.getErr != nil {
		return nil, f.getErr
	}
	if f.getResult == nil {
		return &fileee.Contact{}, nil
	}
	// A copy, not the fixture's own pointer: a real HTTP round trip hands
	// back a freshly decoded value too, and applyContactPatch mutating a
	// shared fixture pointer would make later assertions on getResult
	// itself unreliable.
	cp := *f.getResult
	return &cp, nil
}

func (f *fakeContactWriteService) Update(_ context.Context, entity *fileee.Contact) (*fileee.Contact, error) {
	f.updateCalledWith = entity
	if f.updateErr != nil {
		return nil, f.updateErr
	}
	if f.updateResult != nil {
		return f.updateResult, nil
	}
	return entity, nil
}

// TestUpdateContactPatchMerge is the task's own named test: only the
// caller-supplied field (Email) changes, every other field the existing
// contact already carried (FirstName) survives untouched into the
// Update call, and the tool's result surfaces the updated contact's
// display name — framed, in CallToolResult.Content, exactly the way
// TestDocumentFromServiceLiefertZusammenfassungUndGerahmtenTitelBeiErfolg
// (read_document_test.go) already proves for a document's Title, NEVER
// as a field of the returned updateContactOutput (which lands in
// CallToolResult.StructuredContent — see this file's own doc comment
// and write.go's package doc comment on why that channel stays
// foreign-text-free for every tool in this package, write tools
// included).
func TestUpdateContactPatchMerge(t *testing.T) {
	service := &fakeContactWriteService{
		getResult: &fileee.Contact{ID: "c1", FirstName: "Alice", LastName: "Nachname", Email: "alt@x.de"},
		updateResult: &fileee.Contact{
			ID: "c1", FirstName: "Alice", LastName: "Nachname", Email: "neu@x.de", Modified: "2026-08-23T00:00:00Z",
		},
	}
	in := updateContactInput{ID: "c1", Email: ptr("neu@x.de")}

	result, out, err := updateContactFromService(context.Background(), service, in)
	if err != nil {
		t.Fatalf("updateContactFromService: %v", err)
	}

	if !service.getCalled {
		t.Error("Get wurde nicht aufgerufen — Patch/Merge braucht den aktuellen Kontakt zuerst")
	}
	if service.updateCalledWith == nil {
		t.Fatal("Update wurde nicht aufgerufen")
	}
	if service.updateCalledWith.FirstName != "Alice" {
		t.Errorf("Update erhielt FirstName = %q, want %q (unveraendert, nicht im Patch enthalten)",
			service.updateCalledWith.FirstName, "Alice")
	}
	if service.updateCalledWith.Email != "neu@x.de" {
		t.Errorf("Update erhielt Email = %q, want %q (die einzige angeforderte Aenderung)",
			service.updateCalledWith.Email, "neu@x.de")
	}

	if out.ID != "c1" {
		t.Errorf("out.ID = %q, want %q", out.ID, "c1")
	}
	if out.Modified != "2026-08-23T00:00:00Z" {
		t.Errorf("out.Modified = %q, want %q", out.Modified, "2026-08-23T00:00:00Z")
	}
	// Struktur-Teil (CallToolResult.StructuredContent) bleibt frei vom
	// fremdbestimmten Anzeigenamen — dieselbe Pruefung wie
	// TestDocumentFromServiceLiefertZusammenfassungUndGerahmtenTitelBeiErfolg
	// (read_document_test.go) fuer ein Dokument-Title.
	if strings.Contains(fmt.Sprint(out), "Alice") {
		t.Errorf("out = %+v enthaelt den fremdbestimmten Anzeigenamen strukturiert — der gehoert "+
			"ausschliesslich gerahmt in CallToolResult.Content, nie in StructuredContent", out)
	}

	if len(result.Content) != 1 {
		t.Fatalf("Content hat %d Eintraege, want 1", len(result.Content))
	}
	text, ok := result.Content[0].(*mcp.TextContent)
	if !ok {
		t.Fatalf("Content[0] ist %T, want *mcp.TextContent", result.Content[0])
	}
	if !strings.Contains(text.Text, "Alice") {
		t.Errorf("Content enthaelt nicht den Anzeigenamen %q: %q", "Alice", text.Text)
	}
	if !strings.Contains(text.Text, "<untrusted_external_content") {
		t.Errorf("Content ist nicht als fremdbestimmter Text gerahmt (ADR-0013): %q", text.Text)
	}
}

// TestUpdateContactFromServiceWickeltEinenNetzwerkfehlerBeimLadenMitDemWerkzeugnamenEin
// ist die Network-Error-Haelfte der Mutations-Test-Pflicht: Get selbst
// scheitert (z.B. eine abgebrochene Verbindung), Update wird gar nicht
// erst aufgerufen — der Ausgangszustand des Kontakts bleibt unangetastet.
func TestUpdateContactFromServiceWickeltEinenNetzwerkfehlerBeimLadenMitDemWerkzeugnamenEin(t *testing.T) {
	networkErr := errors.New("dial tcp: connection refused")
	service := &fakeContactWriteService{getErr: networkErr}

	_, out, err := updateContactFromService(context.Background(), service, updateContactInput{ID: "c1"})
	if err == nil {
		t.Fatal("erwarteter Fehler blieb aus")
	}
	if !errors.Is(err, networkErr) {
		t.Errorf("Fehler wickelt %v nicht ein, bekam: %v", networkErr, err)
	}
	if !strings.Contains(err.Error(), ToolUpdateContact) {
		t.Errorf("Fehlermeldung %q enthaelt nicht den Werkzeugnamen %q", err.Error(), ToolUpdateContact)
	}
	if service.updateCalledWith != nil {
		t.Error("Update wurde aufgerufen, obwohl Get bereits scheiterte — keine Mutation ohne geladenen Ausgangszustand")
	}
	if (out != updateContactOutput{}) {
		t.Errorf("out = %+v, want den Nullwert — kein Teilerfolg bei einem gescheiterten Get", out)
	}
}

// TestUpdateContactFromServiceWickeltEinenGegenseitenFehlerBeimSpeichernMitDemWerkzeugnamenEin
// ist die Backend-error(4xx/5xx)-Haelfte: Get liefert den aktuellen
// Kontakt, Update scheitert an der Gegenseite (fileee.APIError, z.B. 409
// Conflict oder 500). Der Auftrag ist eindeutig: "no partial-state
// claim" — das Ergebnis behauptet an keiner Stelle, der Kontakt sei
// (teilweise) geaendert worden.
func TestUpdateContactFromServiceWickeltEinenGegenseitenFehlerBeimSpeichernMitDemWerkzeugnamenEin(t *testing.T) {
	backendErr := &fileee.APIError{HTTPStatus: 409, Code: "CONFLICT", Message: "version mismatch"}
	service := &fakeContactWriteService{
		getResult: &fileee.Contact{ID: "c1", FirstName: "Alice", Email: "alt@x.de"},
		updateErr: backendErr,
	}

	_, out, err := updateContactFromService(context.Background(), service, updateContactInput{ID: "c1", Email: ptr("neu@x.de")})
	if err == nil {
		t.Fatal("erwarteter Fehler blieb aus")
	}
	if !errors.Is(err, backendErr) {
		t.Errorf("Fehler wickelt %v nicht ein, bekam: %v", backendErr, err)
	}
	if !strings.Contains(err.Error(), ToolUpdateContact) {
		t.Errorf("Fehlermeldung %q enthaelt nicht den Werkzeugnamen %q", err.Error(), ToolUpdateContact)
	}
	if !service.getCalled {
		t.Error("Get wurde nicht aufgerufen")
	}
	if service.updateCalledWith == nil {
		t.Fatal("Update wurde nicht aufgerufen, obwohl Get erfolgreich war")
	}
	if (out != updateContactOutput{}) {
		t.Errorf("out = %+v, want den Nullwert — Update scheiterte, kein Teilerfolg zu behaupten", out)
	}
}

func TestUpdateContactHandlerLehntEineLeereKennungOhneNetzwerkzugriffAb(t *testing.T) {
	handler := updateContactHandler(nil, discardLogger())

	_, _, err := handler(context.Background(), nil, updateContactInput{ID: "  "})
	if err == nil {
		t.Fatal("erwarteter Fehler blieb aus")
	}
	if !strings.Contains(err.Error(), ToolUpdateContact) {
		t.Errorf("Fehlermeldung %q enthaelt nicht den Werkzeugnamen %q", err.Error(), ToolUpdateContact)
	}
}

// TestApplyContactPatchAendertNurGesetzteFelder prueft die Merge-Logik
// isoliert von jedem Backend-Aufruf: ein Patch mit nur einem gesetzten
// Zeiger-Feld darf kein anderes Feld auf cur beruehren.
func TestApplyContactPatchAendertNurGesetzteFelder(t *testing.T) {
	cur := &fileee.Contact{
		ID: "c1", FirstName: "Alice", LastName: "Nachname", CompanyName: "ACME",
		Email: "alt@x.de", PhoneNumber: "0123", FaxNumber: "0456", URL: "https://alt.example",
	}
	applyContactPatch(cur, updateContactInput{ID: "c1", Email: ptr("neu@x.de")})

	if cur.Email != "neu@x.de" {
		t.Errorf("Email = %q, want %q", cur.Email, "neu@x.de")
	}
	want := fileee.Contact{
		ID: "c1", FirstName: "Alice", LastName: "Nachname", CompanyName: "ACME",
		Email: "neu@x.de", PhoneNumber: "0123", FaxNumber: "0456", URL: "https://alt.example",
	}
	if *cur != want {
		t.Errorf("applyContactPatch veraenderte mehr als das angeforderte Feld: got %+v, want %+v", *cur, want)
	}
}

// TestApplyContactPatchAendertAlleGesetztenFelder ist die Gegenprobe zum
// vorigen Test: ein Patch, der jedes Feld setzt, muss auch jedes Feld
// aendern — sonst waere obiger Test nur zufaellig gruen, weil die
// Funktion schlicht gar nichts tut.
func TestApplyContactPatchAendertAlleGesetztenFelder(t *testing.T) {
	cur := &fileee.Contact{ID: "c1"}
	applyContactPatch(cur, updateContactInput{
		ID:          "c1",
		FirstName:   ptr("Neu-Vorname"),
		LastName:    ptr("Neu-Nachname"),
		CompanyName: ptr("Neu-Firma"),
		Email:       ptr("neu@x.de"),
		PhoneNumber: ptr("0999"),
		FaxNumber:   ptr("0888"),
		URL:         ptr("https://neu.example"),
	})

	want := fileee.Contact{
		ID: "c1", FirstName: "Neu-Vorname", LastName: "Neu-Nachname", CompanyName: "Neu-Firma",
		Email: "neu@x.de", PhoneNumber: "0999", FaxNumber: "0888", URL: "https://neu.example",
	}
	if *cur != want {
		t.Errorf("got %+v, want %+v", *cur, want)
	}
}

// TestContactDisplayNameFaelltAufDenFirmennamenZurueck belegt den
// Rueckfall auf CompanyName: ein Firmenkontakt (ContactType company)
// traegt typischerweise keinen Vor-/Nachnamen, ein leerer gerahmter
// Block waere fuer den Aufrufer nutzlos.
func TestContactDisplayNameFaelltAufDenFirmennamenZurueck(t *testing.T) {
	got := contactDisplayName(&fileee.Contact{CompanyName: "ACME GmbH"})
	if got != "ACME GmbH" {
		t.Errorf("contactDisplayName = %q, want %q", got, "ACME GmbH")
	}
}

func TestContactDisplayNameBevorzugtVorUndNachname(t *testing.T) {
	got := contactDisplayName(&fileee.Contact{FirstName: "Alice", LastName: "Nachname", CompanyName: "ACME GmbH"})
	if got != "Alice Nachname" {
		t.Errorf("contactDisplayName = %q, want %q", got, "Alice Nachname")
	}
}

// ptr is a tiny generic helper turning a value into a pointer inline —
// updateContactInput's patch fields are all pointers, and every test in
// this file above needs several one-off pointers to string literals.
func ptr[T any](v T) *T { return &v }
