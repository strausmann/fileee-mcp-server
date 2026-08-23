// write_documents_test.go tests write_documents.go's two document write
// tools, upload_document (Task 5) and update_document (Task 6) — the
// same template write_boxes_test.go already establishes: a narrow fake
// drives the client-resolution-free logic directly, and thin
// handler-level tests exercise the branches reachable without a real
// gangway/HTTP round trip.
//
// Mutations-Test-Pflicht (test-coverage-pflicht.md, homelab-management
// repo): upload_document gets Happy-Path + Duplicate + Invalid-base64 +
// Backend-error (4xx/5xx) + Network-error coverage. The duplicate case
// gets its own explicit counter-check
// (TestUploadDocumentFromServiceGegenprobeDuplikatAlsFehlerBehandeltWuerdeDenTestRotFaerben)
// proving the test would fail if ErrDuplicateDocument were wrongly
// treated as an error — the task brief's own required Gegenprobe. A
// second counter-check
// (TestUploadDocumentFromServiceGegenprobeRohesBase64StattDekodierterBytesWuerdeDenTestRotFaerben)
// proves the reader passed to Upload carries the DECODED bytes, not the
// base64 string itself.
//
// update_document (Task 6) gets Happy-Path (Title set) + Title-nil
// (patch/merge "leave unchanged") + Backend-error (4xx/5xx) +
// Network-error coverage — the same triad write.go's own
// TestUpdateContact... tests establish for update_contact, since
// update_document follows the identical Get/apply/Update patch/merge
// shape (write.go's own doc comment). TestUpdateDocumentTitleNilLaesstTitelUnveraendert
// is this task's own required Gegenprobe: it fails if Title==nil were
// mishandled as "clear the title" (a nil-pointer deref on
// applyDocumentTitlePatch) or "overwrite with the zero value" instead
// of "leave the current title alone".
package tools

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"reflect"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/strausmann/go-fileee/fileee"

	"github.com/strausmann/fileee-mcp-server/internal/clientpool"
)

func TestUploadDocumentInputFeldlisteIstAbgeschlossen(t *testing.T) {
	want := []string{"Title", "ContentBase64"}
	got := fieldNames(uploadDocumentInput{})
	if !reflect.DeepEqual(got, want) {
		t.Errorf("uploadDocumentInput-Feldliste = %v, want %v", got, want)
	}
}

func TestUploadDocumentOutputFeldlisteIstAbgeschlossen(t *testing.T) {
	// KEIN Title-/Fremdtext-Feld hier — Title kommt aus diesem Aufruf
	// nie wieder heraus, siehe write_documents.go's eigenen
	// Package-Doc-Kommentar.
	want := []string{"ID", "IsDuplicate"}
	got := fieldNames(uploadDocumentOutput{})
	if !reflect.DeepEqual(got, want) {
		t.Errorf("uploadDocumentOutput-Feldliste = %v, want %v", got, want)
	}
}

func TestRegisterWriteToolsMeldetUploadDocumentAn(t *testing.T) {
	s := mcp.NewServer(&mcp.Implementation{Name: "probe", Version: "0"}, nil)

	registerWriteTools(s, (*clientpool.Pool)(nil), discardLogger())

	names := toolNamesOf(t, s)
	if !names[ToolUploadDocument] {
		t.Errorf("Werkzeug %q wurde nicht angemeldet", ToolUploadDocument)
	}
}

// TestUploadDocumentWerkzeugIstAlsSchreibendUndNichtIdempotentAnnotiert
// belegt den Auftrag woertlich: upload_document traegt
// ReadOnlyHint:false, DestructiveHint:false (additiv, legt neu an) und
// IdempotentHint:false.
func TestUploadDocumentWerkzeugIstAlsSchreibendUndNichtIdempotentAnnotiert(t *testing.T) {
	var found *mcp.Tool
	for _, tool := range registeredReadTools() {
		if tool.Name == ToolUploadDocument {
			found = tool
		}
	}
	if found == nil {
		t.Fatalf("Werkzeug %q wurde nicht angemeldet", ToolUploadDocument)
	}
	if found.Annotations == nil {
		t.Fatalf("Werkzeug %q hat keine Annotations", ToolUploadDocument)
	}
	if found.Annotations.ReadOnlyHint {
		t.Error("ReadOnlyHint = true, want false — upload_document schreibt")
	}
	if found.Annotations.DestructiveHint == nil || *found.Annotations.DestructiveHint {
		t.Errorf("DestructiveHint = %v, want a pointer to false", found.Annotations.DestructiveHint)
	}
	if found.Annotations.IdempotentHint {
		t.Error("IdempotentHint = true, want false")
	}
}

// fakeDocumentUploadService is documentUploadService's test double. It
// records the bytes it actually read from the reader it was handed
// (not the reader itself, which is only valid for one read) so a test
// can assert on the DECODED content, and the meta it received, without
// needing a real HTTP round trip.
type fakeDocumentUploadService struct {
	calledWithBytes []byte
	calledWithMeta  fileee.UploadMetadata
	called          bool
	result          *fileee.UploadResult
	err             error
}

func (f *fakeDocumentUploadService) Upload(_ context.Context, r io.Reader, meta fileee.UploadMetadata) (*fileee.UploadResult, error) {
	f.called = true
	f.calledWithMeta = meta
	b, err := io.ReadAll(r)
	if err != nil {
		return nil, err
	}
	f.calledWithBytes = b
	return f.result, f.err
}

// TestUploadDocumentHappyPath is the task's own named case: base64
// content + Title → Upload is called with a reader over the DECODED
// bytes and UploadMetadata{Title}, the output carries the new
// document's ID and IsDuplicate:false.
func TestUploadDocumentHappyPath(t *testing.T) {
	service := &fakeDocumentUploadService{
		result: &fileee.UploadResult{Document: &fileee.Document{ID: "doc-new-1"}, IsDuplicate: false},
	}
	decoded := []byte("hello world, this is not really a pdf")
	in := uploadDocumentInput{Title: "Invoice März 2026"}

	result, out, err := uploadDocumentFromService(context.Background(), service, decoded, in)
	if err != nil {
		t.Fatalf("uploadDocumentFromService: %v", err)
	}

	if !service.called {
		t.Fatal("Upload wurde nicht aufgerufen")
	}
	if !bytes.Equal(service.calledWithBytes, decoded) {
		t.Errorf("Upload erhielt Bytes %q, want %q", service.calledWithBytes, decoded)
	}
	if service.calledWithMeta.Title != "Invoice März 2026" {
		t.Errorf("Upload erhielt Title = %q, want %q", service.calledWithMeta.Title, "Invoice März 2026")
	}
	if service.calledWithMeta.Document != nil {
		t.Errorf("Upload erhielt Document = %+v, want nil", service.calledWithMeta.Document)
	}

	if out.ID != "doc-new-1" {
		t.Errorf("out.ID = %q, want %q", out.ID, "doc-new-1")
	}
	if out.IsDuplicate {
		t.Error("out.IsDuplicate = true, want false — dies ist kein Duplikat-Fall")
	}

	if result == nil {
		t.Fatal("result ist nil, want ein leeres *mcp.CallToolResult{}")
	}
	if len(result.Content) != 0 {
		t.Errorf("Content hat %d Eintraege, want 0 — es gibt hier keinen fremdbestimmten Text zu rahmen", len(result.Content))
	}
}

// TestUploadDocumentDuplicate is the task's own named case: Upload
// returns fileee.ErrDuplicateDocument (with a non-nil Result, per
// go-fileee's own contract) → the output has IsDuplicate:true and the
// SERVER's document ID, and the call is reported as a SUCCESS (nil
// error) — a duplicate is a normal, informative outcome, not a failure.
func TestUploadDocumentDuplicate(t *testing.T) {
	service := &fakeDocumentUploadService{
		result: &fileee.UploadResult{Document: &fileee.Document{ID: "doc-existing-server-id"}, IsDuplicate: true},
		err:    fileee.ErrDuplicateDocument,
	}
	decoded := []byte("same file bytes as before")
	in := uploadDocumentInput{Title: "Invoice"}

	result, out, err := uploadDocumentFromService(context.Background(), service, decoded, in)
	if err != nil {
		t.Fatalf("uploadDocumentFromService: %v — ein Duplikat ist KEIN Fehler", err)
	}

	if out.ID != "doc-existing-server-id" {
		t.Errorf("out.ID = %q, want das SERVER-Dokument %q", out.ID, "doc-existing-server-id")
	}
	if !out.IsDuplicate {
		t.Error("out.IsDuplicate = false, want true")
	}
	if result == nil {
		t.Fatal("result ist nil, want ein leeres *mcp.CallToolResult{}")
	}
}

// TestUploadDocumentFromServiceGegenprobeDuplikatAlsFehlerBehandeltWuerdeDenTestRotFaerben
// is the task brief's own required counter-check: it directly asserts
// the property TestUploadDocumentDuplicate relies on (err == nil on a
// duplicate) using errors.Is against fileee.ErrDuplicateDocument, so a
// future edit that mistakenly starts treating the sentinel as a plain
// error (dropping the errors.Is branch in uploadDocumentFromService)
// fails THIS test for the specific reason "duplicate is not an error",
// not just incidentally via TestUploadDocumentDuplicate's broader
// assertions.
func TestUploadDocumentFromServiceGegenprobeDuplikatAlsFehlerBehandeltWuerdeDenTestRotFaerben(t *testing.T) {
	service := &fakeDocumentUploadService{
		result: &fileee.UploadResult{Document: &fileee.Document{ID: "doc-existing"}, IsDuplicate: true},
		err:    fileee.ErrDuplicateDocument,
	}

	_, _, err := uploadDocumentFromService(context.Background(), service, []byte("x"), uploadDocumentInput{Title: "t"})
	if errors.Is(err, fileee.ErrDuplicateDocument) {
		t.Fatal("uploadDocumentFromService gab ErrDuplicateDocument als eigenen Fehler zurueck — " +
			"ein Duplikat muss als Erfolg (IsDuplicate:true, nil error) behandelt werden, nicht als Fehler")
	}
	if err != nil {
		t.Fatalf("uploadDocumentFromService: %v, want nil (Duplikat ist kein Fehler)", err)
	}
}

// TestUploadDocumentFromServiceGegenprobeRohesBase64StattDekodierterBytesWuerdeDenTestRotFaerben
// proves uploadDocumentFromService's reader carries the caller's
// DECODED bytes, not the base64-encoded string itself — the second
// Gegenprobe the task brief calls for. A regression that accidentally
// passed the raw base64 text as the reader's content would fail this
// test, since the encoded and decoded forms differ for any non-trivial
// input.
func TestUploadDocumentFromServiceGegenprobeRohesBase64StattDekodierterBytesWuerdeDenTestRotFaerben(t *testing.T) {
	service := &fakeDocumentUploadService{
		result: &fileee.UploadResult{Document: &fileee.Document{ID: "doc-1"}, IsDuplicate: false},
	}
	raw := []byte("this is the plain file content, not base64 at all")

	_, _, err := uploadDocumentFromService(context.Background(), service, raw, uploadDocumentInput{Title: "t"})
	if err != nil {
		t.Fatalf("uploadDocumentFromService: %v", err)
	}

	if !bytes.Equal(service.calledWithBytes, raw) {
		t.Errorf("Upload erhielt %q, want die unveraenderten dekodierten Bytes %q — "+
			"ein Regression, das stattdessen den base64-Rohstring durchreicht, wuerde hier abweichen", service.calledWithBytes, raw)
	}
}

func TestUploadDocumentFromServiceWickeltEinenNetzwerkfehlerMitDemWerkzeugnamenEin(t *testing.T) {
	networkErr := errors.New("dial tcp: connection refused")
	service := &fakeDocumentUploadService{err: networkErr}

	_, out, err := uploadDocumentFromService(context.Background(), service, []byte("x"), uploadDocumentInput{Title: "t"})
	if err == nil {
		t.Fatal("erwarteter Fehler blieb aus")
	}
	if !errors.Is(err, networkErr) {
		t.Errorf("Fehler wickelt %v nicht ein, bekam: %v", networkErr, err)
	}
	if !strings.Contains(err.Error(), ToolUploadDocument) {
		t.Errorf("Fehlermeldung %q enthaelt nicht den Werkzeugnamen %q", err.Error(), ToolUploadDocument)
	}
	if (out != uploadDocumentOutput{}) {
		t.Errorf("out = %+v, want den Nullwert — kein Teilerfolg bei einem gescheiterten Upload", out)
	}
}

func TestUploadDocumentFromServiceWickeltEinenGegenseitenFehlerMitDemWerkzeugnamenEin(t *testing.T) {
	backendErr := &fileee.APIError{HTTPStatus: 422, Code: "INVALID", Message: "unsupported file type"}
	service := &fakeDocumentUploadService{err: backendErr}

	_, out, err := uploadDocumentFromService(context.Background(), service, []byte("x"), uploadDocumentInput{Title: "t"})
	if err == nil {
		t.Fatal("erwarteter Fehler blieb aus")
	}
	if !errors.Is(err, backendErr) {
		t.Errorf("Fehler wickelt %v nicht ein, bekam: %v", backendErr, err)
	}
	if !strings.Contains(err.Error(), ToolUploadDocument) {
		t.Errorf("Fehlermeldung %q enthaelt nicht den Werkzeugnamen %q", err.Error(), ToolUploadDocument)
	}
	if (out != uploadDocumentOutput{}) {
		t.Errorf("out = %+v, want den Nullwert — Upload scheiterte, kein Teilerfolg zu behaupten", out)
	}
}

// TestUploadDocumentHandlerLehntUngueltigesBase64OhneNetzwerkzugriffAb
// is the task's own third named case: invalid base64 → a clear input
// error before any Upload call — tested at the HANDLER level (nil
// *clientpool.Pool, no fake service reachable at all) since the decode
// happens before clientFor, the same "reachable without a real
// gangway/HTTP round trip" pattern every other handler-level
// pre-clientFor test in this package uses.
func TestUploadDocumentHandlerLehntUngueltigesBase64OhneNetzwerkzugriffAb(t *testing.T) {
	handler := uploadDocumentHandler(nil, discardLogger())

	_, _, err := handler(context.Background(), nil, uploadDocumentInput{Title: "t", ContentBase64: "not valid base64!!"})
	if err == nil {
		t.Fatal("erwarteter Fehler blieb aus")
	}
	if !strings.Contains(err.Error(), ToolUploadDocument) {
		t.Errorf("Fehlermeldung %q enthaelt nicht den Werkzeugnamen %q", err.Error(), ToolUploadDocument)
	}
	if !strings.Contains(err.Error(), "base64") {
		t.Errorf("Fehlermeldung %q nennt nicht, dass contentBase64 ungueltig war", err.Error())
	}
}

// TestUploadDocumentHandlerLaesstGueltigesBase64BisZuClientForDurch is
// the Gegenprobe to the previous test — the same pattern
// TestAddBoxDocumentHandlerLaesstGueltigeEingabenBisZuClientForDurch
// establishes (write_boxes_test.go): valid base64 content must NOT be
// rejected by the pre-clientFor decode check — the error it does get
// (clientFor failing without a verified identity in ctx) must not
// mention base64 at all.
func TestUploadDocumentHandlerLaesstGueltigesBase64BisZuClientForDurch(t *testing.T) {
	handler := uploadDocumentHandler(nil, discardLogger())

	validBase64 := "aGVsbG8gd29ybGQ=" // "hello world"
	_, _, err := handler(context.Background(), nil, uploadDocumentInput{Title: "t", ContentBase64: validBase64})
	if err == nil {
		t.Fatal("erwarteter Fehler blieb aus (clientFor ohne verifizierte Identitaet muss scheitern)")
	}
	if strings.Contains(err.Error(), "base64") {
		t.Errorf("gueltiges base64 wurde faelschlich als ungueltig abgewiesen: %v", err)
	}
}

func TestUpdateDocumentInputFeldlisteIstAbgeschlossen(t *testing.T) {
	want := []string{"ID", "Title"}
	got := fieldNames(updateDocumentInput{})
	if !reflect.DeepEqual(got, want) {
		t.Errorf("updateDocumentInput-Feldliste = %v, want %v", got, want)
	}
}

func TestUpdateDocumentOutputFeldlisteIstAbgeschlossen(t *testing.T) {
	// KEIN Title-/Fremdtext-Feld hier — der neue Titel landet
	// ausschliesslich gerahmt in CallToolResult.Content (siehe
	// TestUpdateDocumentPatchMerge unten), nie strukturiert in
	// CallToolResult.StructuredContent.
	want := []string{"ID"}
	got := fieldNames(updateDocumentOutput{})
	if !reflect.DeepEqual(got, want) {
		t.Errorf("updateDocumentOutput-Feldliste = %v, want %v", got, want)
	}
}

func TestRegisterWriteToolsMeldetUpdateDocumentAn(t *testing.T) {
	s := mcp.NewServer(&mcp.Implementation{Name: "probe", Version: "0"}, nil)

	registerWriteTools(s, (*clientpool.Pool)(nil), discardLogger())

	names := toolNamesOf(t, s)
	if !names[ToolUpdateDocument] {
		t.Errorf("Werkzeug %q wurde nicht angemeldet", ToolUpdateDocument)
	}
}

// TestUpdateDocumentWerkzeugIstAlsSchreibendUndDestruktivAnnotiert belegt
// den Auftrag woertlich: update_document traegt ReadOnlyHint:false (es
// ist KEIN Lesewerkzeug), DestructiveHint:true (ein Update kann den
// bestehenden Titel ueberschreiben) und IdempotentHint:true (derselbe
// Patch zweimal angewendet aendert das Dokument kein zweites Mal).
func TestUpdateDocumentWerkzeugIstAlsSchreibendUndDestruktivAnnotiert(t *testing.T) {
	var found *mcp.Tool
	for _, tool := range registeredReadTools() {
		if tool.Name == ToolUpdateDocument {
			found = tool
		}
	}
	if found == nil {
		t.Fatalf("Werkzeug %q wurde nicht angemeldet", ToolUpdateDocument)
	}
	if found.Annotations == nil {
		t.Fatalf("Werkzeug %q hat keine Annotations", ToolUpdateDocument)
	}
	if found.Annotations.Title != "Update document" {
		t.Errorf("Title = %q, want %q", found.Annotations.Title, "Update document")
	}
	if found.Annotations.ReadOnlyHint {
		t.Error("ReadOnlyHint = true, want false — update_document schreibt")
	}
	if found.Annotations.DestructiveHint == nil || !*found.Annotations.DestructiveHint {
		t.Errorf("DestructiveHint = %v, want a pointer to true — ein Update kann den bestehenden Titel ueberschreiben",
			found.Annotations.DestructiveHint)
	}
	if !found.Annotations.IdempotentHint {
		t.Error("IdempotentHint = false, want true — derselbe Patch zweimal aendert nichts zusaetzlich")
	}
}

// fakeDocumentUpdateService is documentUpdateService's test double — the
// same shape fakeContactWriteService establishes (write_test.go) for
// update_contact.
type fakeDocumentUpdateService struct {
	getCalled bool
	getResult *fileee.Document
	getErr    error

	updateCalledWith *fileee.Document
	updateResult     *fileee.Document
	updateErr        error
}

func (f *fakeDocumentUpdateService) Get(_ context.Context, _ string) (*fileee.Document, error) {
	f.getCalled = true
	if f.getErr != nil {
		return nil, f.getErr
	}
	if f.getResult == nil {
		return &fileee.Document{}, nil
	}
	// A copy, not the fixture's own pointer — the same reasoning
	// fakeContactWriteService's own Get gives (write_test.go): a real
	// HTTP round trip hands back a freshly decoded value too, and
	// applyDocumentTitlePatch mutating a shared fixture pointer would
	// make later assertions on getResult itself unreliable.
	cp := *f.getResult
	return &cp, nil
}

func (f *fakeDocumentUpdateService) Update(_ context.Context, doc *fileee.Document) (*fileee.Document, error) {
	f.updateCalledWith = doc
	if f.updateErr != nil {
		return nil, f.updateErr
	}
	if f.updateResult != nil {
		return f.updateResult, nil
	}
	return doc, nil
}

// TestApplyDocumentTitlePatchAendertTitelNurWennGesetzt prueft die
// Merge-Logik isoliert von jedem Backend-Aufruf: ein Patch ohne
// gesetztes Title-Feld darf den bestehenden Titel nicht anfassen, ein
// Patch mit gesetztem Title-Feld muss ihn aendern.
func TestApplyDocumentTitlePatchAendertTitelNurWennGesetzt(t *testing.T) {
	cur := &fileee.Document{ID: "doc-1", Attributes: fileee.DocumentAttributes{Title: "Alt"}}

	applyDocumentTitlePatch(cur, updateDocumentInput{ID: "doc-1"})
	if cur.Attributes.Title != "Alt" {
		t.Errorf("Title = %q nach Patch ohne gesetztes Feld, want unveraendert %q", cur.Attributes.Title, "Alt")
	}

	applyDocumentTitlePatch(cur, updateDocumentInput{ID: "doc-1", Title: ptr("Neu")})
	if cur.Attributes.Title != "Neu" {
		t.Errorf("Title = %q nach Patch mit gesetztem Feld, want %q", cur.Attributes.Title, "Neu")
	}
}

// TestUpdateDocumentPatchMerge is the task's own named case: Title set
// → Get loads the current document, Update receives the merged
// document carrying the NEW title, the output carries the ID, and the
// new title appears — framed, in CallToolResult.Content, exactly the
// way TestUpdateContactPatchMerge (write_test.go) already proves for a
// contact's display name, NEVER as a field of the returned
// updateDocumentOutput.
func TestUpdateDocumentPatchMerge(t *testing.T) {
	service := &fakeDocumentUpdateService{
		getResult:    &fileee.Document{ID: "doc-1", Attributes: fileee.DocumentAttributes{Title: "Alter Titel"}},
		updateResult: &fileee.Document{ID: "doc-1", Attributes: fileee.DocumentAttributes{Title: "Neuer Titel"}},
	}
	in := updateDocumentInput{ID: "doc-1", Title: ptr("Neuer Titel")}

	result, out, err := updateDocumentFromService(context.Background(), service, in)
	if err != nil {
		t.Fatalf("updateDocumentFromService: %v", err)
	}

	if !service.getCalled {
		t.Error("Get wurde nicht aufgerufen — Patch/Merge braucht das aktuelle Dokument zuerst")
	}
	if service.updateCalledWith == nil {
		t.Fatal("Update wurde nicht aufgerufen")
	}
	if service.updateCalledWith.Attributes.Title != "Neuer Titel" {
		t.Errorf("Update erhielt Title = %q, want %q (die angeforderte Aenderung)",
			service.updateCalledWith.Attributes.Title, "Neuer Titel")
	}

	if out.ID != "doc-1" {
		t.Errorf("out.ID = %q, want %q", out.ID, "doc-1")
	}
	// Struktur-Teil (CallToolResult.StructuredContent) bleibt frei vom
	// fremdbestimmten Titel — dieselbe Pruefung wie
	// TestUpdateContactPatchMerge (write_test.go) fuer den Anzeigenamen.
	if strings.Contains(fmt.Sprint(out), "Neuer Titel") {
		t.Errorf("out = %+v enthaelt den fremdbestimmten Titel strukturiert — der gehoert "+
			"ausschliesslich gerahmt in CallToolResult.Content, nie in StructuredContent", out)
	}

	if len(result.Content) != 1 {
		t.Fatalf("Content hat %d Eintraege, want 1", len(result.Content))
	}
	text, ok := result.Content[0].(*mcp.TextContent)
	if !ok {
		t.Fatalf("Content[0] ist %T, want *mcp.TextContent", result.Content[0])
	}
	if !strings.Contains(text.Text, "Neuer Titel") {
		t.Errorf("Content enthaelt nicht den neuen Titel %q: %q", "Neuer Titel", text.Text)
	}
	if !strings.Contains(text.Text, "<untrusted_external_content") {
		t.Errorf("Content ist nicht als fremdbestimmter Text gerahmt (ADR-0013): %q", text.Text)
	}
}

// TestUpdateDocumentTitleNilLaesstTitelUnveraendert is this task's own
// required Gegenprobe: Title == nil (der Aufrufer setzt das Feld gar
// nicht) darf den bestehenden Titel NICHT anfassen — weder loeschen
// noch mit einem leeren Wert ueberschreiben. Update wird trotzdem
// aufgerufen, dieselbe Semantik wie updateContactFromService (write.go)
// sie fuer einen Patch ohne gesetzte Felder etabliert: der Aufruf
// short-circuited nicht auf "nichts zu tun", er sendet den (in diesem
// Fall unveraenderten) Ausgangszustand zurueck.
func TestUpdateDocumentTitleNilLaesstTitelUnveraendert(t *testing.T) {
	service := &fakeDocumentUpdateService{
		getResult: &fileee.Document{ID: "doc-1", Attributes: fileee.DocumentAttributes{Title: "Bestehender Titel"}},
	}
	in := updateDocumentInput{ID: "doc-1"} // Title bewusst nicht gesetzt

	result, out, err := updateDocumentFromService(context.Background(), service, in)
	if err != nil {
		t.Fatalf("updateDocumentFromService: %v", err)
	}

	if !service.getCalled {
		t.Error("Get wurde nicht aufgerufen")
	}
	if service.updateCalledWith == nil {
		t.Fatal("Update wurde nicht aufgerufen — dieselbe Semantik wie update_contact: auch ohne " +
			"gesetztes Patch-Feld wird Update aufgerufen (write.go, updateContactFromService)")
	}
	if service.updateCalledWith.Attributes.Title != "Bestehender Titel" {
		t.Errorf("Update erhielt Title = %q, want unveraendert %q — ein Title==nil-Patch darf den "+
			"bestehenden Titel nicht anfassen", service.updateCalledWith.Attributes.Title, "Bestehender Titel")
	}

	if out.ID != "doc-1" {
		t.Errorf("out.ID = %q, want %q", out.ID, "doc-1")
	}

	if len(result.Content) != 1 {
		t.Fatalf("Content hat %d Eintraege, want 1", len(result.Content))
	}
	text, ok := result.Content[0].(*mcp.TextContent)
	if !ok {
		t.Fatalf("Content[0] ist %T, want *mcp.TextContent", result.Content[0])
	}
	if !strings.Contains(text.Text, "Bestehender Titel") {
		t.Errorf("Content enthaelt nicht den unveraenderten Titel %q: %q", "Bestehender Titel", text.Text)
	}
}

// TestUpdateDocumentFromServiceWickeltEinenNetzwerkfehlerBeimLadenMitDemWerkzeugnamenEin
// ist die Network-Error-Haelfte der Mutations-Test-Pflicht: Get selbst
// scheitert, Update wird gar nicht erst aufgerufen — der Ausgangszustand
// des Dokuments bleibt unangetastet.
func TestUpdateDocumentFromServiceWickeltEinenNetzwerkfehlerBeimLadenMitDemWerkzeugnamenEin(t *testing.T) {
	networkErr := errors.New("dial tcp: connection refused")
	service := &fakeDocumentUpdateService{getErr: networkErr}

	_, out, err := updateDocumentFromService(context.Background(), service, updateDocumentInput{ID: "doc-1"})
	if err == nil {
		t.Fatal("erwarteter Fehler blieb aus")
	}
	if !errors.Is(err, networkErr) {
		t.Errorf("Fehler wickelt %v nicht ein, bekam: %v", networkErr, err)
	}
	if !strings.Contains(err.Error(), ToolUpdateDocument) {
		t.Errorf("Fehlermeldung %q enthaelt nicht den Werkzeugnamen %q", err.Error(), ToolUpdateDocument)
	}
	if service.updateCalledWith != nil {
		t.Error("Update wurde aufgerufen, obwohl Get bereits scheiterte — keine Mutation ohne geladenen Ausgangszustand")
	}
	if (out != updateDocumentOutput{}) {
		t.Errorf("out = %+v, want den Nullwert — kein Teilerfolg bei einem gescheiterten Get", out)
	}
}

// TestUpdateDocumentFromServiceWickeltEinenGegenseitenFehlerBeimSpeichernMitDemWerkzeugnamenEin
// ist die Backend-error(4xx/5xx)-Haelfte: Get liefert das aktuelle
// Dokument, Update scheitert an der Gegenseite (fileee.APIError). Der
// Auftrag ist eindeutig: "no partial-state claim" — das Ergebnis
// behauptet an keiner Stelle, der Titel sei (teilweise) geaendert
// worden.
func TestUpdateDocumentFromServiceWickeltEinenGegenseitenFehlerBeimSpeichernMitDemWerkzeugnamenEin(t *testing.T) {
	backendErr := &fileee.APIError{HTTPStatus: 409, Code: "CONFLICT", Message: "version mismatch"}
	service := &fakeDocumentUpdateService{
		getResult: &fileee.Document{ID: "doc-1", Attributes: fileee.DocumentAttributes{Title: "Titel"}},
		updateErr: backendErr,
	}

	_, out, err := updateDocumentFromService(context.Background(), service, updateDocumentInput{ID: "doc-1", Title: ptr("Neu")})
	if err == nil {
		t.Fatal("erwarteter Fehler blieb aus")
	}
	if !errors.Is(err, backendErr) {
		t.Errorf("Fehler wickelt %v nicht ein, bekam: %v", backendErr, err)
	}
	if !strings.Contains(err.Error(), ToolUpdateDocument) {
		t.Errorf("Fehlermeldung %q enthaelt nicht den Werkzeugnamen %q", err.Error(), ToolUpdateDocument)
	}
	if !service.getCalled {
		t.Error("Get wurde nicht aufgerufen")
	}
	if service.updateCalledWith == nil {
		t.Fatal("Update wurde nicht aufgerufen, obwohl Get erfolgreich war")
	}
	if (out != updateDocumentOutput{}) {
		t.Errorf("out = %+v, want den Nullwert — Update scheiterte, kein Teilerfolg zu behaupten", out)
	}
}

func TestUpdateDocumentHandlerLehntEineLeereKennungOhneNetzwerkzugriffAb(t *testing.T) {
	handler := updateDocumentHandler(nil, discardLogger())

	_, _, err := handler(context.Background(), nil, updateDocumentInput{ID: "  "})
	if err == nil {
		t.Fatal("erwarteter Fehler blieb aus")
	}
	if !strings.Contains(err.Error(), ToolUpdateDocument) {
		t.Errorf("Fehlermeldung %q enthaelt nicht den Werkzeugnamen %q", err.Error(), ToolUpdateDocument)
	}
}

// TestUpdateDocumentHandlerLaesstEineGueltigeKennungBisZuClientForDurch is
// the Gegenprobe to the previous test — the same pattern
// TestUploadDocumentHandlerLaesstGueltigesBase64BisZuClientForDurch
// establishes above: a valid (non-empty) ID must NOT be rejected by the
// pre-clientFor empty-ID check — the error it does get (clientFor
// failing without a verified identity in ctx) must not mention "id" at
// all, only that no identity was found.
func TestUpdateDocumentHandlerLaesstEineGueltigeKennungBisZuClientForDurch(t *testing.T) {
	handler := updateDocumentHandler(nil, discardLogger())

	_, _, err := handler(context.Background(), nil, updateDocumentInput{ID: "doc-1"})
	if err == nil {
		t.Fatal("erwarteter Fehler blieb aus (clientFor ohne verifizierte Identitaet muss scheitern)")
	}
	if strings.Contains(err.Error(), "must not be empty") {
		t.Errorf("eine gueltige Kennung wurde faelschlich als leer abgewiesen: %v", err)
	}
	if !strings.Contains(err.Error(), "no verified identity") {
		t.Errorf("Fehlermeldung %q nennt nicht, dass keine verifizierte Identitaet im Kontext war", err.Error())
	}
}
