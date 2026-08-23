// write_people_test.go tests write_people.go's two reminder write tools
// (Task 3) — the same template write_test.go already establishes for
// update_contact/create_contact: a narrow fake drives the
// client-resolution-free logic directly, and a thin handler-level test
// exercises the one branch reachable without a real gangway/HTTP round
// trip.
//
// Mutations-Test-Pflicht (test-coverage-pflicht.md, homelab-management
// repo): both reminder tools get Happy-Path + Backend-error (4xx/5xx) +
// Network-error coverage, not just a success case.
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

// --- create_reminder ---

func TestCreateReminderInputFeldlisteIstAbgeschlossen(t *testing.T) {
	want := []string{"Description", "DetailedDescription", "DocumentID", "StartDate"}
	got := fieldNames(createReminderInput{})
	if !reflect.DeepEqual(got, want) {
		t.Errorf("createReminderInput-Feldliste = %v, want %v", got, want)
	}
}

func TestReminderOutputFeldlisteIstAbgeschlossen(t *testing.T) {
	// KEIN Description-Feld — die Erinnerungsbeschreibung landet
	// ausschliesslich gerahmt in CallToolResult.Content, genau wie es
	// reminderDescriptor (read_people.go) fuer list_reminders/
	// get_reminder bereits tut, nie strukturiert in
	// CallToolResult.StructuredContent.
	want := []string{"ID", "Done"}
	got := fieldNames(reminderOutput{})
	if !reflect.DeepEqual(got, want) {
		t.Errorf("reminderOutput-Feldliste = %v, want %v", got, want)
	}
}

func TestRegisterWriteToolsMeldetCreateReminderAn(t *testing.T) {
	s := mcp.NewServer(&mcp.Implementation{Name: "probe", Version: "0"}, nil)

	registerWriteTools(s, (*clientpool.Pool)(nil), discardLogger())

	names := toolNamesOf(t, s)
	if !names[ToolCreateReminder] {
		t.Errorf("Werkzeug %q wurde nicht angemeldet", ToolCreateReminder)
	}
}

// TestCreateReminderWerkzeugIstAlsSchreibendUndNichtDestruktivAnnotiert
// belegt den Auftrag woertlich: create_reminder traegt
// ReadOnlyHint:false, DestructiveHint:false (additiv, ueberschreibt
// nichts Bestehendes) und IdempotentHint:false (zweimal aufgerufen legt
// zwei Erinnerungen an, keine einzige).
func TestCreateReminderWerkzeugIstAlsSchreibendUndNichtDestruktivAnnotiert(t *testing.T) {
	var found *mcp.Tool
	for _, tool := range registeredReadTools() {
		if tool.Name == ToolCreateReminder {
			found = tool
		}
	}
	if found == nil {
		t.Fatalf("Werkzeug %q wurde nicht angemeldet", ToolCreateReminder)
	}
	if found.Annotations == nil {
		t.Fatalf("Werkzeug %q hat keine Annotations", ToolCreateReminder)
	}
	if found.Annotations.ReadOnlyHint {
		t.Error("ReadOnlyHint = true, want false — create_reminder schreibt")
	}
	if found.Annotations.DestructiveHint == nil || *found.Annotations.DestructiveHint {
		t.Errorf("DestructiveHint = %v, want a pointer to false — ein Create ist additiv, ueberschreibt nichts",
			found.Annotations.DestructiveHint)
	}
	if found.Annotations.IdempotentHint {
		t.Error("IdempotentHint = true, want false — derselbe Aufruf zweimal legt zwei Erinnerungen an")
	}
}

// fakeReminderCreateService is reminderCreateService's test double —
// narrower than fakeReminderWriteService below (only Create), the same
// "narrow the fake to what the tool actually calls" pattern
// contactCreateService/contactWriteService already establish
// (write.go/write_test.go).
type fakeReminderCreateService struct {
	createCalledWith *fileee.Reminder
	createResult     *fileee.Reminder
	createErr        error
}

func (f *fakeReminderCreateService) Create(_ context.Context, entity *fileee.Reminder) (*fileee.Reminder, error) {
	f.createCalledWith = entity
	if f.createErr != nil {
		return nil, f.createErr
	}
	if f.createResult != nil {
		return f.createResult, nil
	}
	return entity, nil
}

// TestCreateReminderHappyPath is the task's own named case: Create is
// called with a *fileee.Reminder carrying Description and StartDate,
// the result surfaces the new ID+Done structured and the reminder's own
// description framed in CallToolResult.Content — never as a field of
// reminderOutput (see this file's own doc comment and write.go's
// package doc comment on the foreign-text invariant).
func TestCreateReminderHappyPath(t *testing.T) {
	service := &fakeReminderCreateService{
		createResult: &fileee.Reminder{ID: "r-neu", Description: "Rechnung bezahlen", Done: false},
	}
	in := createReminderInput{Description: "Rechnung bezahlen", StartDate: "2026-09-01"}

	result, out, err := createReminderFromService(context.Background(), service, in)
	if err != nil {
		t.Fatalf("createReminderFromService: %v", err)
	}

	if service.createCalledWith == nil {
		t.Fatal("Create wurde nicht aufgerufen")
	}
	if service.createCalledWith.Description != "Rechnung bezahlen" {
		t.Errorf("Create erhielt Description = %q, want %q", service.createCalledWith.Description, "Rechnung bezahlen")
	}
	if service.createCalledWith.StartDate != "2026-09-01" {
		t.Errorf("Create erhielt StartDate = %q, want %q", service.createCalledWith.StartDate, "2026-09-01")
	}

	if out.ID != "r-neu" {
		t.Errorf("out.ID = %q, want %q", out.ID, "r-neu")
	}
	if out.Done {
		t.Error("out.Done = true, want false")
	}
	// Struktur-Teil bleibt frei von der fremdbestimmten Beschreibung —
	// dieselbe Pruefung wie TestCreateContactHappyPath (write_test.go).
	if strings.Contains(fmt.Sprint(out), "Rechnung bezahlen") {
		t.Errorf("out = %+v enthaelt die fremdbestimmte Beschreibung strukturiert — die gehoert "+
			"ausschliesslich gerahmt in CallToolResult.Content, nie in StructuredContent", out)
	}

	if len(result.Content) != 1 {
		t.Fatalf("Content hat %d Eintraege, want 1", len(result.Content))
	}
	text, ok := result.Content[0].(*mcp.TextContent)
	if !ok {
		t.Fatalf("Content[0] ist %T, want *mcp.TextContent", result.Content[0])
	}
	if !strings.Contains(text.Text, "Rechnung bezahlen") {
		t.Errorf("Content enthaelt nicht die Beschreibung %q: %q", "Rechnung bezahlen", text.Text)
	}
	if !strings.Contains(text.Text, "<untrusted_external_content") {
		t.Errorf("Content ist nicht als fremdbestimmter Text gerahmt (ADR-0013): %q", text.Text)
	}
}

func TestCreateReminderFromServiceWickeltEinenNetzwerkfehlerMitDemWerkzeugnamenEin(t *testing.T) {
	networkErr := errors.New("dial tcp: connection refused")
	service := &fakeReminderCreateService{createErr: networkErr}

	_, out, err := createReminderFromService(context.Background(), service, createReminderInput{Description: "x"})
	if err == nil {
		t.Fatal("erwarteter Fehler blieb aus")
	}
	if !errors.Is(err, networkErr) {
		t.Errorf("Fehler wickelt %v nicht ein, bekam: %v", networkErr, err)
	}
	if !strings.Contains(err.Error(), ToolCreateReminder) {
		t.Errorf("Fehlermeldung %q enthaelt nicht den Werkzeugnamen %q", err.Error(), ToolCreateReminder)
	}
	if (out != reminderOutput{}) {
		t.Errorf("out = %+v, want den Nullwert — kein Teilerfolg bei einem gescheiterten Create", out)
	}
}

func TestCreateReminderFromServiceWickeltEinenGegenseitenFehlerMitDemWerkzeugnamenEin(t *testing.T) {
	backendErr := &fileee.APIError{HTTPStatus: 400, Code: "INVALID_REMINDER", Message: "description required"}
	service := &fakeReminderCreateService{createErr: backendErr}

	_, out, err := createReminderFromService(context.Background(), service, createReminderInput{})
	if err == nil {
		t.Fatal("erwarteter Fehler blieb aus")
	}
	if !errors.Is(err, backendErr) {
		t.Errorf("Fehler wickelt %v nicht ein, bekam: %v", backendErr, err)
	}
	if !strings.Contains(err.Error(), ToolCreateReminder) {
		t.Errorf("Fehlermeldung %q enthaelt nicht den Werkzeugnamen %q", err.Error(), ToolCreateReminder)
	}
	if (out != reminderOutput{}) {
		t.Errorf("out = %+v, want den Nullwert — Create scheiterte, kein Teilerfolg zu behaupten", out)
	}
}

func TestCreateReminderHandlerLehntEineLeereBeschreibungOhneNetzwerkzugriffAb(t *testing.T) {
	handler := createReminderHandler(nil, discardLogger())

	_, _, err := handler(context.Background(), nil, createReminderInput{Description: "   "})
	if err == nil {
		t.Fatal("erwarteter Fehler blieb aus")
	}
	if !strings.Contains(err.Error(), ToolCreateReminder) {
		t.Errorf("Fehlermeldung %q enthaelt nicht den Werkzeugnamen %q", err.Error(), ToolCreateReminder)
	}
}

// TestCreateReminderHandlerLaesstEineGueltigeBeschreibungBisZuClientForDurch
// ist die Gegenprobe zum vorigen Test — dieselbe Pruefidee wie
// TestCreateContactHandlerAkzeptiertEinenReinenFirmenkontaktOhneNamen
// (write_test.go): eine gueltige Description darf NICHT an der
// Vor-clientFor-Pruefung scheitern. Da hier keine echte
// *clientpool.Pool bereitsteht, laeuft der Aufruf ueber die Pruefung
// hinaus bis zu clientFor und scheitert dort (context.Background()
// traegt keine verifizierte Identitaet) — das beweist bereits, dass die
// vorgelagerte Pruefung diesen Fall passieren liess, ohne einen echten
// Backend-Rundlauf zu brauchen.
func TestCreateReminderHandlerLaesstEineGueltigeBeschreibungBisZuClientForDurch(t *testing.T) {
	handler := createReminderHandler(nil, discardLogger())

	_, _, err := handler(context.Background(), nil, createReminderInput{Description: "Rechnung bezahlen"})
	if err == nil {
		t.Fatal("erwarteter Fehler blieb aus (clientFor ohne verifizierte Identitaet muss scheitern)")
	}
	if strings.Contains(err.Error(), "description must not be empty") {
		t.Errorf("eine gueltige Description wurde faelschlich als leer abgewiesen: %v", err)
	}
}

// --- update_reminder ---

func TestUpdateReminderInputFeldlisteIstAbgeschlossen(t *testing.T) {
	want := []string{"ID", "Description", "DetailedDescription", "StartDate", "Done"}
	got := fieldNames(updateReminderInput{})
	if !reflect.DeepEqual(got, want) {
		t.Errorf("updateReminderInput-Feldliste = %v, want %v", got, want)
	}
}

func TestRegisterWriteToolsMeldetUpdateReminderAn(t *testing.T) {
	s := mcp.NewServer(&mcp.Implementation{Name: "probe", Version: "0"}, nil)

	registerWriteTools(s, (*clientpool.Pool)(nil), discardLogger())

	names := toolNamesOf(t, s)
	if !names[ToolUpdateReminder] {
		t.Errorf("Werkzeug %q wurde nicht angemeldet", ToolUpdateReminder)
	}
}

// TestUpdateReminderWerkzeugIstAlsSchreibendUndDestruktivAnnotiert belegt
// den Auftrag woertlich: update_reminder traegt ReadOnlyHint:false,
// DestructiveHint:true (kann bestehende Feldwerte ueberschreiben) und
// IdempotentHint:true (derselbe Patch zweimal aendert die Erinnerung
// kein zweites Mal).
func TestUpdateReminderWerkzeugIstAlsSchreibendUndDestruktivAnnotiert(t *testing.T) {
	var found *mcp.Tool
	for _, tool := range registeredReadTools() {
		if tool.Name == ToolUpdateReminder {
			found = tool
		}
	}
	if found == nil {
		t.Fatalf("Werkzeug %q wurde nicht angemeldet", ToolUpdateReminder)
	}
	if found.Annotations == nil {
		t.Fatalf("Werkzeug %q hat keine Annotations", ToolUpdateReminder)
	}
	if found.Annotations.ReadOnlyHint {
		t.Error("ReadOnlyHint = true, want false — update_reminder schreibt")
	}
	if found.Annotations.DestructiveHint == nil || !*found.Annotations.DestructiveHint {
		t.Errorf("DestructiveHint = %v, want a pointer to true — ein Update kann bestehende Werte ueberschreiben",
			found.Annotations.DestructiveHint)
	}
	if !found.Annotations.IdempotentHint {
		t.Error("IdempotentHint = false, want true — derselbe Patch zweimal aendert nichts zusaetzlich")
	}
}

// fakeReminderWriteService is reminderWriteService's test double —
// Get+Update, the same shape fakeContactWriteService already
// establishes for update_contact (write_test.go).
type fakeReminderWriteService struct {
	getCalled bool
	getResult *fileee.Reminder
	getErr    error

	updateCalledWith *fileee.Reminder
	updateResult     *fileee.Reminder
	updateErr        error
}

func (f *fakeReminderWriteService) Get(_ context.Context, _ string) (*fileee.Reminder, error) {
	f.getCalled = true
	if f.getErr != nil {
		return nil, f.getErr
	}
	if f.getResult == nil {
		return &fileee.Reminder{}, nil
	}
	// A copy, not the fixture's own pointer — same reasoning as
	// fakeContactWriteService.Get (write_test.go): applyReminderPatch
	// mutating a shared fixture pointer would make later assertions on
	// getResult itself unreliable.
	cp := *f.getResult
	return &cp, nil
}

func (f *fakeReminderWriteService) Update(_ context.Context, entity *fileee.Reminder) (*fileee.Reminder, error) {
	f.updateCalledWith = entity
	if f.updateErr != nil {
		return nil, f.updateErr
	}
	if f.updateResult != nil {
		return f.updateResult, nil
	}
	return entity, nil
}

// TestUpdateReminderPatchMerge is the task's own named test: only
// Done→true changes, Description (the existing reminder already
// carried it) survives untouched into the Update call.
func TestUpdateReminderPatchMerge(t *testing.T) {
	service := &fakeReminderWriteService{
		getResult: &fileee.Reminder{ID: "r1", Description: "Rechnung bezahlen", Done: false},
		updateResult: &fileee.Reminder{
			ID: "r1", Description: "Rechnung bezahlen", Done: true,
		},
	}
	in := updateReminderInput{ID: "r1", Done: ptr(true)}

	result, out, err := updateReminderFromService(context.Background(), service, in)
	if err != nil {
		t.Fatalf("updateReminderFromService: %v", err)
	}

	if !service.getCalled {
		t.Error("Get wurde nicht aufgerufen — Patch/Merge braucht die aktuelle Erinnerung zuerst")
	}
	if service.updateCalledWith == nil {
		t.Fatal("Update wurde nicht aufgerufen")
	}
	if service.updateCalledWith.Description != "Rechnung bezahlen" {
		t.Errorf("Update erhielt Description = %q, want %q (unveraendert, nicht im Patch enthalten)",
			service.updateCalledWith.Description, "Rechnung bezahlen")
	}
	if !service.updateCalledWith.Done {
		t.Error("Update erhielt Done = false, want true (die einzige angeforderte Aenderung)")
	}

	if out.ID != "r1" {
		t.Errorf("out.ID = %q, want %q", out.ID, "r1")
	}
	if !out.Done {
		t.Error("out.Done = false, want true")
	}
	if strings.Contains(fmt.Sprint(out), "Rechnung") {
		t.Errorf("out = %+v enthaelt die fremdbestimmte Beschreibung strukturiert — die gehoert "+
			"ausschliesslich gerahmt in CallToolResult.Content, nie in StructuredContent", out)
	}

	if len(result.Content) != 1 {
		t.Fatalf("Content hat %d Eintraege, want 1", len(result.Content))
	}
	text, ok := result.Content[0].(*mcp.TextContent)
	if !ok {
		t.Fatalf("Content[0] ist %T, want *mcp.TextContent", result.Content[0])
	}
	if !strings.Contains(text.Text, "Rechnung bezahlen") {
		t.Errorf("Content enthaelt nicht die Beschreibung %q: %q", "Rechnung bezahlen", text.Text)
	}
	if !strings.Contains(text.Text, "<untrusted_external_content") {
		t.Errorf("Content ist nicht als fremdbestimmter Text gerahmt (ADR-0013): %q", text.Text)
	}
}

func TestUpdateReminderFromServiceWickeltEinenNetzwerkfehlerBeimLadenMitDemWerkzeugnamenEin(t *testing.T) {
	networkErr := errors.New("dial tcp: connection refused")
	service := &fakeReminderWriteService{getErr: networkErr}

	_, out, err := updateReminderFromService(context.Background(), service, updateReminderInput{ID: "r1"})
	if err == nil {
		t.Fatal("erwarteter Fehler blieb aus")
	}
	if !errors.Is(err, networkErr) {
		t.Errorf("Fehler wickelt %v nicht ein, bekam: %v", networkErr, err)
	}
	if !strings.Contains(err.Error(), ToolUpdateReminder) {
		t.Errorf("Fehlermeldung %q enthaelt nicht den Werkzeugnamen %q", err.Error(), ToolUpdateReminder)
	}
	if service.updateCalledWith != nil {
		t.Error("Update wurde aufgerufen, obwohl Get bereits scheiterte — keine Mutation ohne geladenen Ausgangszustand")
	}
	if (out != reminderOutput{}) {
		t.Errorf("out = %+v, want den Nullwert — kein Teilerfolg bei einem gescheiterten Get", out)
	}
}

func TestUpdateReminderFromServiceWickeltEinenGegenseitenFehlerBeimSpeichernMitDemWerkzeugnamenEin(t *testing.T) {
	backendErr := &fileee.APIError{HTTPStatus: 409, Code: "CONFLICT", Message: "version mismatch"}
	service := &fakeReminderWriteService{
		getResult: &fileee.Reminder{ID: "r1", Description: "Rechnung bezahlen"},
		updateErr: backendErr,
	}

	_, out, err := updateReminderFromService(context.Background(), service, updateReminderInput{ID: "r1", Done: ptr(true)})
	if err == nil {
		t.Fatal("erwarteter Fehler blieb aus")
	}
	if !errors.Is(err, backendErr) {
		t.Errorf("Fehler wickelt %v nicht ein, bekam: %v", backendErr, err)
	}
	if !strings.Contains(err.Error(), ToolUpdateReminder) {
		t.Errorf("Fehlermeldung %q enthaelt nicht den Werkzeugnamen %q", err.Error(), ToolUpdateReminder)
	}
	if !service.getCalled {
		t.Error("Get wurde nicht aufgerufen")
	}
	if service.updateCalledWith == nil {
		t.Fatal("Update wurde nicht aufgerufen, obwohl Get erfolgreich war")
	}
	if (out != reminderOutput{}) {
		t.Errorf("out = %+v, want den Nullwert — Update scheiterte, kein Teilerfolg zu behaupten", out)
	}
}

func TestUpdateReminderHandlerLehntEineLeereKennungOhneNetzwerkzugriffAb(t *testing.T) {
	handler := updateReminderHandler(nil, discardLogger())

	_, _, err := handler(context.Background(), nil, updateReminderInput{ID: "  "})
	if err == nil {
		t.Fatal("erwarteter Fehler blieb aus")
	}
	if !strings.Contains(err.Error(), ToolUpdateReminder) {
		t.Errorf("Fehlermeldung %q enthaelt nicht den Werkzeugnamen %q", err.Error(), ToolUpdateReminder)
	}
}

// TestUpdateReminderHandlerLaesstEineGueltigeKennungBisZuClientForDurch ist
// die Gegenprobe zum vorigen Test — dieselbe Pruefidee wie
// TestCreateReminderHandlerLaesstEineGueltigeBeschreibungBisZuClientForDurch
// oben: eine gueltige ID darf NICHT an der Vor-clientFor-Pruefung
// scheitern.
func TestUpdateReminderHandlerLaesstEineGueltigeKennungBisZuClientForDurch(t *testing.T) {
	handler := updateReminderHandler(nil, discardLogger())

	_, _, err := handler(context.Background(), nil, updateReminderInput{ID: "r1"})
	if err == nil {
		t.Fatal("erwarteter Fehler blieb aus (clientFor ohne verifizierte Identitaet muss scheitern)")
	}
	if strings.Contains(err.Error(), "id must not be empty") {
		t.Errorf("eine gueltige ID wurde faelschlich als leer abgewiesen: %v", err)
	}
}

// TestApplyReminderPatchAendertNurGesetzteFelder prueft die Merge-Logik
// isoliert von jedem Backend-Aufruf: ein Patch mit nur einem gesetzten
// Zeiger-Feld darf kein anderes Feld auf cur beruehren.
func TestApplyReminderPatchAendertNurGesetzteFelder(t *testing.T) {
	cur := &fileee.Reminder{
		ID: "r1", Description: "Alt", DetailedDescription: "Alt-Detail", StartDate: "2026-01-01", Done: false,
	}
	applyReminderPatch(cur, updateReminderInput{ID: "r1", Done: ptr(true)})

	if !cur.Done {
		t.Errorf("Done = %v, want true", cur.Done)
	}
	want := fileee.Reminder{
		ID: "r1", Description: "Alt", DetailedDescription: "Alt-Detail", StartDate: "2026-01-01", Done: true,
	}
	if *cur != want {
		t.Errorf("applyReminderPatch veraenderte mehr als das angeforderte Feld: got %+v, want %+v", *cur, want)
	}
}

// TestApplyReminderPatchAendertAlleGesetztenFelder ist die Gegenprobe zum
// vorigen Test: ein Patch, der jedes Feld setzt, muss auch jedes Feld
// aendern — sonst waere obiger Test nur zufaellig gruen, weil die
// Funktion schlicht gar nichts tut.
func TestApplyReminderPatchAendertAlleGesetztenFelder(t *testing.T) {
	cur := &fileee.Reminder{ID: "r1"}
	applyReminderPatch(cur, updateReminderInput{
		ID:                  "r1",
		Description:         ptr("Neu"),
		DetailedDescription: ptr("Neu-Detail"),
		StartDate:           ptr("2026-09-01"),
		Done:                ptr(true),
	})

	want := fileee.Reminder{
		ID: "r1", Description: "Neu", DetailedDescription: "Neu-Detail", StartDate: "2026-09-01", Done: true,
	}
	if *cur != want {
		t.Errorf("got %+v, want %+v", *cur, want)
	}
}
