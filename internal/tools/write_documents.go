// write_documents.go holds this server's one document write tool —
// upload_document (Task 5, write-tools-plan). Split into its own file
// for the same reason write_people.go/write_boxes.go are split out of
// write.go (their own doc comments): a fourth topic (document creation
// via upload, over go-fileee's *fileee.DocumentService) gets its own
// file rather than growing write.go/write_people.go/write_boxes.go
// further.
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
package tools

import (
	"bytes"
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"log/slog"
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
func uploadDocumentResult(res *fileee.UploadResult, isDuplicate bool) (*mcp.CallToolResult, uploadDocumentOutput, error) {
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

// registerDocumentWriteTools mounts upload_document onto s — called
// once from registerWriteTools (write.go), the same call site
// registers create_reminder/update_reminder and box_add_document/
// box_remove_document from. Split into its own function (rather than
// inlined into registerWriteTools) so this file stays self-contained,
// the same reasoning registerBoxWriteTools' own doc comment gives
// (write_boxes.go).
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
}
