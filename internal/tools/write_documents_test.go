// write_documents_test.go tests write_documents.go's one document write
// tool, upload_document (Task 5) — the same template
// write_boxes_test.go already establishes: a narrow fake drives the
// client-resolution-free logic directly, and thin handler-level tests
// exercise the branches reachable without a real gangway/HTTP round
// trip.
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
package tools

import (
	"bytes"
	"context"
	"errors"
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
