// write_boxes_test.go tests write_boxes.go's two box-membership write
// tools (Task 4) — the same template write_people_test.go already
// establishes: a narrow fake drives the client-resolution-free logic
// directly, and a thin handler-level test exercises the one branch
// reachable without a real gangway/HTTP round trip.
//
// Mutations-Test-Pflicht (test-coverage-pflicht.md, homelab-management
// repo): both box tools get Happy-Path + Backend-error (4xx/5xx) +
// Network-error coverage. Since go-fileee's own
// AddDocument/RemoveDocument return only an error (boxes.go) — there is
// no updated entity to assert on — the error-path tests are
// particularly load-bearing here: they are the only place a wrong
// return value from *FromService (a non-nil out on error, or a
// swallowed error) would be caught.
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

// --- box_add_document ---

func TestBoxDocumentInputFeldlisteIstAbgeschlossen(t *testing.T) {
	want := []string{"BoxID", "DocumentID"}
	got := fieldNames(boxDocumentInput{})
	if !reflect.DeepEqual(got, want) {
		t.Errorf("boxDocumentInput-Feldliste = %v, want %v", got, want)
	}
}

func TestBoxDocumentOutputFeldlisteIstAbgeschlossen(t *testing.T) {
	want := []string{"BoxID", "DocumentID", "Done"}
	got := fieldNames(boxDocumentOutput{})
	if !reflect.DeepEqual(got, want) {
		t.Errorf("boxDocumentOutput-Feldliste = %v, want %v", got, want)
	}
}

func TestRegisterWriteToolsMeldetBoxAddDocumentAn(t *testing.T) {
	s := mcp.NewServer(&mcp.Implementation{Name: "probe", Version: "0"}, nil)

	registerWriteTools(s, (*clientpool.Pool)(nil), ServerInfo{}, discardLogger())

	names := toolNamesOf(t, s)
	if !names[ToolBoxAddDocument] {
		t.Errorf("Werkzeug %q wurde nicht angemeldet", ToolBoxAddDocument)
	}
}

// TestBoxAddDocumentWerkzeugIstAlsSchreibendUndNichtDestruktivAnnotiert
// belegt den Auftrag woertlich: box_add_document traegt
// ReadOnlyHint:false, DestructiveHint:false (additiv, entfernt nichts
// Bestehendes) und IdempotentHint:false (der Auftrag nennt es explizit
// so).
func TestBoxAddDocumentWerkzeugIstAlsSchreibendUndNichtDestruktivAnnotiert(t *testing.T) {
	var found *mcp.Tool
	for _, tool := range registeredReadTools() {
		if tool.Name == ToolBoxAddDocument {
			found = tool
		}
	}
	if found == nil {
		t.Fatalf("Werkzeug %q wurde nicht angemeldet", ToolBoxAddDocument)
	}
	if found.Annotations == nil {
		t.Fatalf("Werkzeug %q hat keine Annotations", ToolBoxAddDocument)
	}
	if found.Annotations.ReadOnlyHint {
		t.Error("ReadOnlyHint = true, want false — box_add_document schreibt")
	}
	if found.Annotations.DestructiveHint == nil || *found.Annotations.DestructiveHint {
		t.Errorf("DestructiveHint = %v, want a pointer to false", found.Annotations.DestructiveHint)
	}
	if found.Annotations.IdempotentHint {
		t.Error("IdempotentHint = true, want false")
	}
}

// fakeBoxDocumentAddService is boxDocumentAddService's test double.
type fakeBoxDocumentAddService struct {
	calledWithBoxID      string
	calledWithDocumentID string
	called               bool
	err                  error
}

func (f *fakeBoxDocumentAddService) AddDocument(_ context.Context, boxID, documentID string) error {
	f.called = true
	f.calledWithBoxID = boxID
	f.calledWithDocumentID = documentID
	return f.err
}

// TestBoxAddDocumentHappyPath is the task's own named case:
// AddDocument(boxID, documentID) is called, the output confirms.
func TestBoxAddDocumentHappyPath(t *testing.T) {
	service := &fakeBoxDocumentAddService{}
	in := boxDocumentInput{BoxID: "box-1", DocumentID: "doc-1"}

	result, out, err := addBoxDocumentFromService(context.Background(), service, in)
	if err != nil {
		t.Fatalf("addBoxDocumentFromService: %v", err)
	}

	if !service.called {
		t.Fatal("AddDocument wurde nicht aufgerufen")
	}
	if service.calledWithBoxID != "box-1" {
		t.Errorf("AddDocument erhielt boxID = %q, want %q", service.calledWithBoxID, "box-1")
	}
	if service.calledWithDocumentID != "doc-1" {
		t.Errorf("AddDocument erhielt documentID = %q, want %q", service.calledWithDocumentID, "doc-1")
	}

	if out.BoxID != "box-1" {
		t.Errorf("out.BoxID = %q, want %q", out.BoxID, "box-1")
	}
	if out.DocumentID != "doc-1" {
		t.Errorf("out.DocumentID = %q, want %q", out.DocumentID, "doc-1")
	}
	if !out.Done {
		t.Error("out.Done = false, want true")
	}

	if result == nil {
		t.Fatal("result ist nil, want ein leeres *mcp.CallToolResult{}")
	}
	if len(result.Content) != 0 {
		t.Errorf("Content hat %d Eintraege, want 0 — es gibt hier keinen fremdbestimmten Text zu rahmen", len(result.Content))
	}
}

func TestAddBoxDocumentFromServiceWickeltEinenNetzwerkfehlerMitDemWerkzeugnamenEin(t *testing.T) {
	networkErr := errors.New("dial tcp: connection refused")
	service := &fakeBoxDocumentAddService{err: networkErr}

	_, out, err := addBoxDocumentFromService(context.Background(), service, boxDocumentInput{BoxID: "box-1", DocumentID: "doc-1"})
	if err == nil {
		t.Fatal("erwarteter Fehler blieb aus")
	}
	if !errors.Is(err, networkErr) {
		t.Errorf("Fehler wickelt %v nicht ein, bekam: %v", networkErr, err)
	}
	if !strings.Contains(err.Error(), ToolBoxAddDocument) {
		t.Errorf("Fehlermeldung %q enthaelt nicht den Werkzeugnamen %q", err.Error(), ToolBoxAddDocument)
	}
	if (out != boxDocumentOutput{}) {
		t.Errorf("out = %+v, want den Nullwert — kein Teilerfolg bei einem gescheiterten AddDocument", out)
	}
}

func TestAddBoxDocumentFromServiceWickeltEinenGegenseitenFehlerMitDemWerkzeugnamenEin(t *testing.T) {
	backendErr := &fileee.APIError{HTTPStatus: 404, Code: "NOT_FOUND", Message: "box not found"}
	service := &fakeBoxDocumentAddService{err: backendErr}

	_, out, err := addBoxDocumentFromService(context.Background(), service, boxDocumentInput{BoxID: "box-1", DocumentID: "doc-1"})
	if err == nil {
		t.Fatal("erwarteter Fehler blieb aus")
	}
	if !errors.Is(err, backendErr) {
		t.Errorf("Fehler wickelt %v nicht ein, bekam: %v", backendErr, err)
	}
	if !strings.Contains(err.Error(), ToolBoxAddDocument) {
		t.Errorf("Fehlermeldung %q enthaelt nicht den Werkzeugnamen %q", err.Error(), ToolBoxAddDocument)
	}
	if (out != boxDocumentOutput{}) {
		t.Errorf("out = %+v, want den Nullwert — AddDocument scheiterte, kein Teilerfolg zu behaupten", out)
	}
}

func TestAddBoxDocumentHandlerLehntEineLeereBoxIDOhneNetzwerkzugriffAb(t *testing.T) {
	handler := addBoxDocumentHandler(nil, discardLogger())

	_, _, err := handler(context.Background(), nil, boxDocumentInput{BoxID: "  ", DocumentID: "doc-1"})
	if err == nil {
		t.Fatal("erwarteter Fehler blieb aus")
	}
	if !strings.Contains(err.Error(), ToolBoxAddDocument) {
		t.Errorf("Fehlermeldung %q enthaelt nicht den Werkzeugnamen %q", err.Error(), ToolBoxAddDocument)
	}
}

func TestAddBoxDocumentHandlerLehntEineLeereDocumentIDOhneNetzwerkzugriffAb(t *testing.T) {
	handler := addBoxDocumentHandler(nil, discardLogger())

	_, _, err := handler(context.Background(), nil, boxDocumentInput{BoxID: "box-1", DocumentID: "  "})
	if err == nil {
		t.Fatal("erwarteter Fehler blieb aus")
	}
	if !strings.Contains(err.Error(), ToolBoxAddDocument) {
		t.Errorf("Fehlermeldung %q enthaelt nicht den Werkzeugnamen %q", err.Error(), ToolBoxAddDocument)
	}
}

// TestAddBoxDocumentHandlerLaesstGueltigeEingabenBisZuClientForDurch ist
// die Gegenprobe zu den beiden vorigen Tests — dieselbe Pruefidee wie
// TestCreateReminderHandlerLaesstEineGueltigeBeschreibungBisZuClientForDurch
// (write_people_test.go): gueltige BoxID+DocumentID duerfen NICHT an der
// Vor-clientFor-Pruefung scheitern.
func TestAddBoxDocumentHandlerLaesstGueltigeEingabenBisZuClientForDurch(t *testing.T) {
	handler := addBoxDocumentHandler(nil, discardLogger())

	_, _, err := handler(context.Background(), nil, boxDocumentInput{BoxID: "box-1", DocumentID: "doc-1"})
	if err == nil {
		t.Fatal("erwarteter Fehler blieb aus (clientFor ohne verifizierte Identitaet muss scheitern)")
	}
	if strings.Contains(err.Error(), "must not be empty") {
		t.Errorf("gueltige Eingaben wurden faelschlich als leer abgewiesen: %v", err)
	}
}

// --- box_remove_document ---

func TestRegisterWriteToolsMeldetBoxRemoveDocumentAn(t *testing.T) {
	s := mcp.NewServer(&mcp.Implementation{Name: "probe", Version: "0"}, nil)

	registerWriteTools(s, (*clientpool.Pool)(nil), ServerInfo{}, discardLogger())

	names := toolNamesOf(t, s)
	if !names[ToolBoxRemoveDocument] {
		t.Errorf("Werkzeug %q wurde nicht angemeldet", ToolBoxRemoveDocument)
	}
}

// TestBoxRemoveDocumentWerkzeugIstAlsSchreibendUndDestruktivAnnotiert
// belegt den Auftrag woertlich: box_remove_document traegt
// ReadOnlyHint:false, DestructiveHint:true und IdempotentHint:true (der
// Auftrag nennt es explizit so).
func TestBoxRemoveDocumentWerkzeugIstAlsSchreibendUndDestruktivAnnotiert(t *testing.T) {
	var found *mcp.Tool
	for _, tool := range registeredReadTools() {
		if tool.Name == ToolBoxRemoveDocument {
			found = tool
		}
	}
	if found == nil {
		t.Fatalf("Werkzeug %q wurde nicht angemeldet", ToolBoxRemoveDocument)
	}
	if found.Annotations == nil {
		t.Fatalf("Werkzeug %q hat keine Annotations", ToolBoxRemoveDocument)
	}
	if found.Annotations.ReadOnlyHint {
		t.Error("ReadOnlyHint = true, want false — box_remove_document schreibt")
	}
	if found.Annotations.DestructiveHint == nil || !*found.Annotations.DestructiveHint {
		t.Errorf("DestructiveHint = %v, want a pointer to true", found.Annotations.DestructiveHint)
	}
	if !found.Annotations.IdempotentHint {
		t.Error("IdempotentHint = false, want true")
	}
}

// fakeBoxDocumentRemoveService is boxDocumentRemoveService's test double.
type fakeBoxDocumentRemoveService struct {
	calledWithBoxID      string
	calledWithDocumentID string
	called               bool
	err                  error
}

func (f *fakeBoxDocumentRemoveService) RemoveDocument(_ context.Context, boxID, documentID string) error {
	f.called = true
	f.calledWithBoxID = boxID
	f.calledWithDocumentID = documentID
	return f.err
}

// TestBoxRemoveDocumentHappyPath is the task's own named case:
// RemoveDocument is called, the output confirms.
func TestBoxRemoveDocumentHappyPath(t *testing.T) {
	service := &fakeBoxDocumentRemoveService{}
	in := boxDocumentInput{BoxID: "box-1", DocumentID: "doc-1"}

	result, out, err := removeBoxDocumentFromService(context.Background(), service, in)
	if err != nil {
		t.Fatalf("removeBoxDocumentFromService: %v", err)
	}

	if !service.called {
		t.Fatal("RemoveDocument wurde nicht aufgerufen")
	}
	if service.calledWithBoxID != "box-1" {
		t.Errorf("RemoveDocument erhielt boxID = %q, want %q", service.calledWithBoxID, "box-1")
	}
	if service.calledWithDocumentID != "doc-1" {
		t.Errorf("RemoveDocument erhielt documentID = %q, want %q", service.calledWithDocumentID, "doc-1")
	}

	if out.BoxID != "box-1" {
		t.Errorf("out.BoxID = %q, want %q", out.BoxID, "box-1")
	}
	if out.DocumentID != "doc-1" {
		t.Errorf("out.DocumentID = %q, want %q", out.DocumentID, "doc-1")
	}
	if !out.Done {
		t.Error("out.Done = false, want true")
	}

	if result == nil {
		t.Fatal("result ist nil, want ein leeres *mcp.CallToolResult{}")
	}
	if len(result.Content) != 0 {
		t.Errorf("Content hat %d Eintraege, want 0 — es gibt hier keinen fremdbestimmten Text zu rahmen", len(result.Content))
	}
}

func TestRemoveBoxDocumentFromServiceWickeltEinenNetzwerkfehlerMitDemWerkzeugnamenEin(t *testing.T) {
	networkErr := errors.New("dial tcp: connection refused")
	service := &fakeBoxDocumentRemoveService{err: networkErr}

	_, out, err := removeBoxDocumentFromService(context.Background(), service, boxDocumentInput{BoxID: "box-1", DocumentID: "doc-1"})
	if err == nil {
		t.Fatal("erwarteter Fehler blieb aus")
	}
	if !errors.Is(err, networkErr) {
		t.Errorf("Fehler wickelt %v nicht ein, bekam: %v", networkErr, err)
	}
	if !strings.Contains(err.Error(), ToolBoxRemoveDocument) {
		t.Errorf("Fehlermeldung %q enthaelt nicht den Werkzeugnamen %q", err.Error(), ToolBoxRemoveDocument)
	}
	if (out != boxDocumentOutput{}) {
		t.Errorf("out = %+v, want den Nullwert — kein Teilerfolg bei einem gescheiterten RemoveDocument", out)
	}
}

func TestRemoveBoxDocumentFromServiceWickeltEinenGegenseitenFehlerMitDemWerkzeugnamenEin(t *testing.T) {
	backendErr := &fileee.APIError{HTTPStatus: 409, Code: "CONFLICT", Message: "document not in box"}
	service := &fakeBoxDocumentRemoveService{err: backendErr}

	_, out, err := removeBoxDocumentFromService(context.Background(), service, boxDocumentInput{BoxID: "box-1", DocumentID: "doc-1"})
	if err == nil {
		t.Fatal("erwarteter Fehler blieb aus")
	}
	if !errors.Is(err, backendErr) {
		t.Errorf("Fehler wickelt %v nicht ein, bekam: %v", backendErr, err)
	}
	if !strings.Contains(err.Error(), ToolBoxRemoveDocument) {
		t.Errorf("Fehlermeldung %q enthaelt nicht den Werkzeugnamen %q", err.Error(), ToolBoxRemoveDocument)
	}
	if (out != boxDocumentOutput{}) {
		t.Errorf("out = %+v, want den Nullwert — RemoveDocument scheiterte, kein Teilerfolg zu behaupten", out)
	}
}

func TestRemoveBoxDocumentHandlerLehntEineLeereBoxIDOhneNetzwerkzugriffAb(t *testing.T) {
	handler := removeBoxDocumentHandler(nil, discardLogger())

	_, _, err := handler(context.Background(), nil, boxDocumentInput{BoxID: "  ", DocumentID: "doc-1"})
	if err == nil {
		t.Fatal("erwarteter Fehler blieb aus")
	}
	if !strings.Contains(err.Error(), ToolBoxRemoveDocument) {
		t.Errorf("Fehlermeldung %q enthaelt nicht den Werkzeugnamen %q", err.Error(), ToolBoxRemoveDocument)
	}
}

func TestRemoveBoxDocumentHandlerLehntEineLeereDocumentIDOhneNetzwerkzugriffAb(t *testing.T) {
	handler := removeBoxDocumentHandler(nil, discardLogger())

	_, _, err := handler(context.Background(), nil, boxDocumentInput{BoxID: "box-1", DocumentID: "  "})
	if err == nil {
		t.Fatal("erwarteter Fehler blieb aus")
	}
	if !strings.Contains(err.Error(), ToolBoxRemoveDocument) {
		t.Errorf("Fehlermeldung %q enthaelt nicht den Werkzeugnamen %q", err.Error(), ToolBoxRemoveDocument)
	}
}

// TestRemoveBoxDocumentHandlerLaesstGueltigeEingabenBisZuClientForDurch ist
// die Gegenprobe zu den beiden vorigen Tests, dieselbe Pruefidee wie
// TestAddBoxDocumentHandlerLaesstGueltigeEingabenBisZuClientForDurch oben.
func TestRemoveBoxDocumentHandlerLaesstGueltigeEingabenBisZuClientForDurch(t *testing.T) {
	handler := removeBoxDocumentHandler(nil, discardLogger())

	_, _, err := handler(context.Background(), nil, boxDocumentInput{BoxID: "box-1", DocumentID: "doc-1"})
	if err == nil {
		t.Fatal("erwarteter Fehler blieb aus (clientFor ohne verifizierte Identitaet muss scheitern)")
	}
	if strings.Contains(err.Error(), "must not be empty") {
		t.Errorf("gueltige Eingaben wurden faelschlich als leer abgewiesen: %v", err)
	}
}
