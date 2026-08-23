// write_documents.go holds this server's two document write tools —
// upload_document (Task 5, write-tools-plan) and update_document (Task
// 6). Split into its own file for the same reason
// write_people.go/write_boxes.go are split out of write.go (their own
// doc comments): a fourth topic (document creation via upload/update,
// over go-fileee's *fileee.DocumentService) gets its own file rather
// than growing write.go/write_people.go/write_boxes.go further.
//
// upload_document follows none of the three shapes established so far
// (write.go's own Create-without-a-prior-Get and Get/apply/Update
// patch-merge; write_boxes.go's own membership-toggle-with-no-returned-
// entity): fileee.DocumentService.Upload (go-fileee, documents.go) is a
// single call, like create_contact/create_reminder, but its own error
// return can be fileee.ErrDuplicateDocument — a case go-fileee's own
// doc comment on Upload/UploadResult treats as neither a plain success
// nor a plain failure: the server DID recognize the uploaded content,
// it just already has a document for it (the returned id differs from
// the client-generated one this call sent). uploadDocumentFromService
// below treats that case as its own success: IsDuplicate:true, the
// SERVER's existing document ID, and a nil error — a duplicate is a
// normal, informative outcome for a caller to act on (e.g. "don't
// upload this file again"), not something this tool should report as
// call failure.
//
// The foreign-text invariant every other write tool in this package
// holds by hand (write.go's own package doc comment) is, like
// write_boxes.go's own boxDocumentOutput, trivially true here rather
// than something this file has to actively enforce: uploadDocumentOutput
// carries only ID (Fileee's own opaque identifier — the uploaded
// document's, or, on a duplicate, the pre-existing one's; never free
// text) and IsDuplicate, a bool. Title, the one piece of caller-supplied
// text this tool's INPUT carries, never comes back out of it at all —
// there is nothing here for wrapUntrustedLines to frame, so
// uploadDocumentResult below returns a plain, empty
// *mcp.CallToolResult{}, the same choice boxDocumentResult already
// makes (write_boxes.go) for the same underlying reason.
//
// update_document (Task 6), below upload_document in this file, DOES
// follow one of the shapes established elsewhere: write.go's own
// Get/apply/Update patch-merge (updateContactFromService), applied here
// to *fileee.Client.Documents.Get/Update instead of Contacts.Get/Update.
// This spec's own YAGNI scope note (task brief) limits the writable
// surface to the one clearly-writable, common field, Title — the rest
// of fileee.DocumentAttributes is a future increment, not exposed here.
// Unlike upload_document's uploadDocumentOutput, updateDocumentOutput's
// foreign-text invariant is NOT trivially true: the updated document's
// own Title is exactly as foreign here as it is on the read side
// (documentFromService, read.go, framing doc.Attributes.Title the same
// way) and must be actively kept out of updateDocumentOutput and framed
// into CallToolResult.Content instead — the same by-hand discipline
// write.go's own package doc comment describes for update_contact.
package tools

import (
	"bytes"
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"strings"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/strausmann/go-fileee/fileee"

	"github.com/strausmann/fileee-mcp-server/internal/clientpool"
)

// uploadDocumentEndpoint is upload_document's own wire endpoint for
// diagnostic logging (logToolEnd) — go-fileee's DocumentService.Upload
// (documents.go), the single multipart POST that persists the caller's
// new document (or, on a duplicate, confirms the existing one).
const uploadDocumentEndpoint = "POST /api/documents/rest"

// documentUploadService is what upload_document needs from
// *fileee.Client.Documents — narrowed to the single method this tool
// calls, the same "narrow the fake to what the tool actually calls"
// pattern contactCreateService/reminderCreateService/
// boxDocumentAddService already establish (write.go/write_people.go/
// write_boxes.go). client.Documents is a *fileee.DocumentService, whose
// method set is a superset of this interface's, so it satisfies
// documentUploadService without any adapter.
type documentUploadService interface {
	Upload(ctx context.Context, r io.Reader, meta fileee.UploadMetadata) (*fileee.UploadResult, error)
}

// uploadDocumentInput is upload_document's parameters. Both fields are
// genuinely required for the call to mean anything at all — the same
// reasoning boxDocumentInput's own doc comment gives for BoxID/
// DocumentID (write_boxes.go): there is no patch/merge shape here, and
// no sensible default for either "what is this document called" or
// "what bytes are in it" — so neither field carries omitempty, and the
// go-sdk's schema-validation layer rejecting a call that omits either
// entirely, before uploadDocumentHandler ever runs, is the correct
// behavior here.
type uploadDocumentInput struct {
	// Title is the new document's title.
	Title string `json:"title"`
	// ContentBase64 is the document's file content, base64-encoded.
	// MCP tool calls are text-based (JSON), so the raw file bytes travel
	// as a base64 string rather than binary — the same reasoning this
	// server's own get_document_pdf/get_page_image return their binary
	// payload as an mcp.EmbeddedResource/mcp.ImageContent rather than a
	// plain byte-array field (read_binary.go), just for the inbound
	// direction instead of the outbound one.
	ContentBase64 string `json:"contentBase64"`
}

// uploadDocumentOutput is upload_document's structured result. See this
// file's own package doc comment for why it deliberately carries no
// Title/display-name field: Title never comes back out of this call at
// all, so there is nothing here that could leak foreign text into
// CallToolResult.StructuredContent.
type uploadDocumentOutput struct {
	// ID is the uploaded document's ID — or, when IsDuplicate is true,
	// the SERVER's own existing document's ID, not the client-generated
	// one this call sent (go-fileee's own doc comment on
	// fileee.UploadResult).
	ID string `json:"id"`
	// IsDuplicate is true when the server recognized the uploaded
	// content as a document that already exists in the account. This is
	// not an error — see this file's own package doc comment.
	IsDuplicate bool `json:"isDuplicate"`
}

// uploadDocumentResult builds uploadDocumentHandler's success return
// from res, the *fileee.UploadResult service.Upload handed back (on
// either a fresh upload or a server-detected duplicate), and
// isDuplicate, the caller's own classification of which case this is —
// split out so both branches of uploadDocumentFromService below share a
// single call site, the same reasoning boxDocumentResult gives for
// serving both addBoxDocumentFromService and
// removeBoxDocumentFromService (write_boxes.go's own doc comment).
// Returns a plain, empty *mcp.CallToolResult{} rather than calling
// wrapUntrustedLines — there is no foreign text in this call's shape to
// frame (this file's own package doc comment) — the same choice
// boxDocumentResult already makes (write_boxes.go) for the same
// underlying reason.
//
// Defensiver Nil-Check: go-fileee garantiert heute (Upload/UploadResult-
// Doc-Kommentar, "Result befüllen UND Fehler liefern"), dass res UND
// res.Document auf BEIDEN Erfolgspfaden (frischer Upload wie erkanntes
// Duplikat) nicht nil sind — uploadDocumentFromService unten ruft diese
// Funktion ausschliesslich auf genau diesen beiden Pfaden auf. Der
// Vertrag hält heute nachweislich; er ist aber eine stille Zusage einer
// fremden Bibliothek, keine vom Compiler erzwungene Garantie. Ein Panic
// in einem Tool-Handler beendet nachweislich den GESAMTEN Prozess (kein
// recover() im go-sdk-Dispatch, internal/jsonrpc2/conn.go handleAsync
// startet die Handler-Goroutine ohne recover) — eine künftige, stille
// Vertragsverletzung von go-fileee wäre damit ein harter Totalausfall
// (DoS) statt eines normalen Fehlers. Der Check hier tauscht das gegen
// einen gewöhnlichen, für den Aufrufer sichtbaren Fehler ein.
func uploadDocumentResult(res *fileee.UploadResult, isDuplicate bool) (*mcp.CallToolResult, uploadDocumentOutput, error) {
	if res == nil || res.Document == nil {
		return nil, uploadDocumentOutput{}, fmt.Errorf("fileee-mcp: tools: %s: go-fileee returned no document result", ToolUploadDocument)
	}
	return &mcp.CallToolResult{}, uploadDocumentOutput{ID: res.Document.ID, IsDuplicate: isDuplicate}, nil
}

// uploadDocumentFromService is uploadDocumentHandler's logic below
// client resolution AND below the base64 decode — split out so a test
// can drive it against a documentUploadService fake
// (fakeDocumentUploadService, write_documents_test.go) instead of a
// live *fileee.Client, the same pattern addBoxDocumentFromService
// already establishes (write_boxes.go). decoded is the already-decoded
// file content (uploadDocumentHandler's own base64.StdEncoding.
// DecodeString result) — this function never sees the base64 string
// itself, so a bug here can never re-introduce the "upload the raw
// base64 text instead of the decoded bytes" mistake this file's own
// counter-check test guards against.
//
// A fileee.ErrDuplicateDocument from service.Upload is deliberately
// NOT treated as this function's own error (see this file's own
// package doc comment): res is non-nil on that path too (go-fileee's
// own doc comment on Upload/UploadResult — "Result befüllen UND Fehler
// liefern"), so uploadDocumentResult(res, true) reports the SERVER's
// existing document ID with IsDuplicate:true and a nil error. Any other
// error (a network failure, or a genuine backend rejection) is wrapped
// and returned as this function's own error, exactly like every other
// *FromService function in this package.
func uploadDocumentFromService(ctx context.Context, service documentUploadService, decoded []byte, in uploadDocumentInput) (*mcp.CallToolResult, uploadDocumentOutput, error) {
	res, err := service.Upload(ctx, bytes.NewReader(decoded), fileee.UploadMetadata{Title: in.Title})
	if errors.Is(err, fileee.ErrDuplicateDocument) {
		return uploadDocumentResult(res, true)
	}
	if err != nil {
		return nil, uploadDocumentOutput{}, fmt.Errorf("fileee-mcp: tools: %s: %w", ToolUploadDocument, err)
	}
	return uploadDocumentResult(res, false)
}

// uploadDocumentHandler resolves upload_document. The base64 decode
// runs before clientFor — the same order every other handler in this
// package uses for its own required-input validation (write.go's own
// doc comment on updateContactHandler) — so a caller's malformed
// ContentBase64 is rejected without spending a login round trip on it,
// and without ever calling service.Upload at all, and so this path is
// testable without a *clientpool.Pool at all (see
// write_documents_test.go).
func uploadDocumentHandler(p *clientpool.Pool, logger *slog.Logger) mcp.ToolHandlerFor[uploadDocumentInput, uploadDocumentOutput] {
	return func(ctx context.Context, _ *mcp.CallToolRequest, in uploadDocumentInput) (*mcp.CallToolResult, uploadDocumentOutput, error) {
		start := time.Now()
		// No slog.String args here (unlike updateContactHandler's own
		// slog.String("id", ...)): Title is exactly as caller-supplied,
		// potentially foreign text as createContactHandler's own
		// FirstName/LastName (write.go's own doc comment on that
		// handler), and ContentBase64 is raw file content that must
		// never land in a diagnostic log regardless of size or
		// sensitivity — the same caution applies to this server's own
		// diagnostic logs as to its structured tool output.
		logToolStart(ctx, logger, ToolUploadDocument)

		decoded, err := base64.StdEncoding.DecodeString(in.ContentBase64)
		if err != nil {
			wrapped := fmt.Errorf("fileee-mcp: tools: %s: contentBase64 is not valid base64: %w", ToolUploadDocument, err)
			logToolEnd(ctx, logger, ToolUploadDocument, start, "", 0, wrapped)
			return nil, uploadDocumentOutput{}, wrapped
		}

		client, err := clientFor(ctx, p)
		if err != nil {
			logToolEnd(ctx, logger, ToolUploadDocument, start, "", 0, err)
			return nil, uploadDocumentOutput{}, err
		}
		result, out, err := uploadDocumentFromService(ctx, client.Documents, decoded, in)
		logToolEnd(ctx, logger, ToolUploadDocument, start, uploadDocumentEndpoint, 1, err)
		return result, out, err
	}
}

// updateDocumentEndpoint is update_document's own wire endpoint for
// diagnostic logging (logToolEnd) — go-fileee's DocumentService.Update
// (documents.go), the PUT that persists the caller's Title change. The
// Get that precedes it (client.Documents.Get, load-before-merge) is
// deliberately not logged as its own endpoint — the same "log the
// operation, not every backend round trip inside it" choice write.go's
// own updateContactEndpoint doc comment makes for update_contact.
const updateDocumentEndpoint = "PUT /api/documents/rest/:id"

// documentUpdateService is what update_document needs from
// *fileee.Client.Documents — narrowed to the two methods this tool
// calls, the same pattern contactWriteService establishes for
// update_contact (write.go). client.Documents is a
// *fileee.DocumentService, whose method set is a superset of this
// interface's, so it satisfies documentUpdateService without any
// adapter.
type documentUpdateService interface {
	Get(ctx context.Context, id string) (*fileee.Document, error)
	Update(ctx context.Context, doc *fileee.Document) (*fileee.Document, error)
}

// updateDocumentInput is update_document's parameters — a patch/merge,
// the same shape updateContactInput establishes (write.go): ID selects
// the document, Title is a pointer so a caller can tell "leave the
// title alone" (nil) apart from "set the title" (a non-nil pointer,
// even to an empty string). This spec's own YAGNI scope note (task
// brief) keeps this to the one clearly-writable, common field — the
// rest of fileee.DocumentAttributes is a future increment, not exposed
// here.
type updateDocumentInput struct {
	// ID identifies the document to update — required, the same ID
	// get_document/list_documents/search_documents returns.
	ID string `json:"id"`
	// Title, if set, replaces the document's title.
	Title *string `json:"title,omitempty"`
}

// updateDocumentOutput is update_document's structured result. It
// deliberately carries NO Title field — the same foreign-text
// invariant every other write tool in this package holds by hand
// (write.go's own package doc comment, and this file's own package doc
// comment on update_document specifically): a document's Title is
// exactly as foreign here as it is on the read side
// (getDocumentHandler/documentFromService, read.go, framing
// doc.Attributes.Title the same way) and goes into
// CallToolResult.Content via wrapUntrustedLines instead (see
// updateDocumentResult below), never into a field here.
type updateDocumentOutput struct {
	// ID is the updated document's ID, unchanged by this call.
	ID string `json:"id"`
}

// applyDocumentTitlePatch applies in's supplied (non-nil) Title onto
// cur — the "merge" half of update_document's patch/merge shape,
// analogous to applyContactPatch (write.go) but for the single field
// this tool's YAGNI scope covers. in.Title == nil leaves cur.Attributes.Title
// entirely untouched.
func applyDocumentTitlePatch(cur *fileee.Document, in updateDocumentInput) {
	if in.Title != nil {
		cur.Attributes.Title = *in.Title
	}
}

// updateDocumentResult builds updateDocumentHandler's success return
// from upd, the document fileee.Documents.Update handed back — the
// same split updateContactResult establishes (write.go), so
// wrapUntrustedLines' own error path has a single call site.
//
// upd's own Title (post-update — the new title if the caller set one
// via in.Title, the unchanged existing title otherwise) goes into
// result.Content via wrapUntrustedLines — the exact same call
// documentFromService (read.go) makes for a document's Title on the
// read side — never into a field of the returned updateDocumentOutput
// (see that type's own doc comment above).
func updateDocumentResult(upd *fileee.Document) (*mcp.CallToolResult, updateDocumentOutput, error) {
	result, err := wrapUntrustedLines([]string{upd.Attributes.Title})
	if err != nil {
		return nil, updateDocumentOutput{}, fmt.Errorf("fileee-mcp: tools: %s: %w", ToolUpdateDocument, err)
	}
	return result, updateDocumentOutput{ID: upd.ID}, nil
}

// updateDocumentFromService is updateDocumentHandler's logic below
// client resolution — split out so a test can drive it against a
// documentUpdateService fake (fakeDocumentUpdateService,
// write_documents_test.go) instead of a live *fileee.Client, the same
// pattern updateContactFromService establishes (write.go).
//
// A Get failure and an Update failure are both reported as this
// function's own error, wrapped with the tool's name — never as a
// partial success, the same "no partial-state claim"
// updateContactFromService's own doc comment establishes. Get is
// ALWAYS called, and Update is ALWAYS called after a successful Get —
// even when in.Title is nil (no change requested) — mirroring
// update_contact's own unconditional Update call rather than
// short-circuiting on "nothing to patch" (see updateContactFromService,
// write.go).
func updateDocumentFromService(ctx context.Context, service documentUpdateService, in updateDocumentInput) (*mcp.CallToolResult, updateDocumentOutput, error) {
	cur, err := service.Get(ctx, in.ID)
	if err != nil {
		return nil, updateDocumentOutput{}, fmt.Errorf("fileee-mcp: tools: %s: %w", ToolUpdateDocument, err)
	}
	applyDocumentTitlePatch(cur, in)
	upd, err := service.Update(ctx, cur)
	if err != nil {
		return nil, updateDocumentOutput{}, fmt.Errorf("fileee-mcp: tools: %s: %w", ToolUpdateDocument, err)
	}
	return updateDocumentResult(upd)
}

// updateDocumentHandler resolves update_document. The empty-ID check
// runs before clientFor — the same order updateContactHandler already
// uses for its own required parameter (write.go) — so a caller's input
// mistake is rejected without spending a login round trip on it, and so
// this path is testable without a *clientpool.Pool at all (see
// write_documents_test.go).
func updateDocumentHandler(p *clientpool.Pool, logger *slog.Logger) mcp.ToolHandlerFor[updateDocumentInput, updateDocumentOutput] {
	return func(ctx context.Context, _ *mcp.CallToolRequest, in updateDocumentInput) (*mcp.CallToolResult, updateDocumentOutput, error) {
		start := time.Now()
		logToolStart(ctx, logger, ToolUpdateDocument, slog.String("id", in.ID))

		if strings.TrimSpace(in.ID) == "" {
			err := fmt.Errorf("fileee-mcp: tools: %s: id must not be empty", ToolUpdateDocument)
			logToolEnd(ctx, logger, ToolUpdateDocument, start, "", 0, err)
			return nil, updateDocumentOutput{}, err
		}

		client, err := clientFor(ctx, p)
		if err != nil {
			logToolEnd(ctx, logger, ToolUpdateDocument, start, "", 0, err)
			return nil, updateDocumentOutput{}, err
		}
		result, out, err := updateDocumentFromService(ctx, client.Documents, in)
		logToolEnd(ctx, logger, ToolUpdateDocument, start, updateDocumentEndpoint, 1, err)
		return result, out, err
	}
}

// registerDocumentWriteTools mounts upload_document and update_document
// onto s — called once from registerWriteTools (write.go), the same
// call site registers create_reminder/update_reminder and
// box_add_document/box_remove_document from. Split into its own
// function (rather than inlined into registerWriteTools) so this file
// stays self-contained, the same reasoning registerBoxWriteTools' own
// doc comment gives (write_boxes.go).
func registerDocumentWriteTools(s *mcp.Server, p *clientpool.Pool, logger *slog.Logger) {
	mcp.AddTool(s, &mcp.Tool{
		Name: ToolUploadDocument,
		Description: "Upload a new document into the calling user's Fileee account. Pass title " +
			"and contentBase64 (both required) — contentBase64 is the file's raw bytes, " +
			"base64-encoded, since MCP tool calls are text-based. Returns the new document's ID. " +
			"If the server recognizes the content as a document that already exists in the " +
			"account, this is reported as isDuplicate:true with the EXISTING document's ID — not " +
			"as an error; check isDuplicate rather than assuming every successful call created a " +
			"new document. Use get_document/list_documents afterwards to inspect the result.",
		Annotations: &mcp.ToolAnnotations{
			Title:           "Upload document",
			ReadOnlyHint:    false,
			DestructiveHint: boolPtr(false),
			IdempotentHint:  false,
		},
	}, uploadDocumentHandler(p, logger))

	mcp.AddTool(s, &mcp.Tool{
		Name: ToolUpdateDocument,
		Description: "Update an existing document's title in the calling user's Fileee account. " +
			"This is a patch/merge: pass title to change it — omit it to leave the title " +
			"unchanged. Returns the document's ID, and its title (the new one if you set it, " +
			"otherwise the unchanged existing one) as clearly marked, untrusted text, since it " +
			"was supplied by the document itself or extracted from it, not written by the account " +
			"holder. Use list_documents/search_documents or get_document first to find the " +
			"document's ID. It does not create a new document — use it only on a document ID that " +
			"already exists, and only to change the title; other document fields are not " +
			"supported by this tool.",
		Annotations: &mcp.ToolAnnotations{
			Title:           "Update document",
			ReadOnlyHint:    false,
			DestructiveHint: boolPtr(true),
			IdempotentHint:  true,
		},
	}, updateDocumentHandler(p, logger))
}
