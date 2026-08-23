// write_boxes.go holds this server's two box-membership write tools —
// box_add_document and box_remove_document (Task 4, write-tools-plan).
// Split into its own file for the same reason write_people.go is split
// out of write.go (that file's own doc comment): a third topic
// (box-document membership, over fileee.BoxService) gets its own file
// rather than growing write.go or write_people.go further.
//
// Unlike every write tool before this one (update_contact/
// create_contact, write.go; create_reminder/update_reminder,
// write_people.go), neither tool here follows write.go's two shared
// handler shapes at all: box_add_document/box_remove_document do not
// create or patch/merge an ENTITY (a Box, a Contact, a Reminder) — they
// toggle a single document's MEMBERSHIP in a box, via go-fileee's own
// fileee.BoxService.AddDocument/RemoveDocument (go-fileee/fileee/
// boxes.go), each a single call that returns only an error, no updated
// entity to report back. There is nothing to Get first (no patch/merge:
// membership is either added or removed, not merged onto a prior
// state) and nothing structured to return beyond an echo of what the
// caller asked for plus a completion flag — see boxDocumentOutput below.
//
// The foreign-text invariant this package's every other write tool
// holds by hand (write.go's own package doc comment) is trivially true
// here rather than something this file has to actively enforce:
// boxDocumentOutput carries only BoxID, DocumentID (both supplied BY
// the caller, not read back from Fileee — read_boxes.go's own doc
// comment on why a box's own BoxName needs no UntrustedLine either, a
// box's ID is Fileee's own opaque identifier, never free text) and a
// bool. There is no foreign text ANYWHERE in this call's shape to frame
// — unlike updateContactResult/createContactResult/reminderResult,
// which each build a CallToolResult.Content block via
// wrapUntrustedLines for exactly that reason, boxDocumentResult below
// returns a plain, empty *mcp.CallToolResult{} — the same choice
// accountStatusFromService already makes (read_account.go) for the
// same underlying reason: nothing foreign to carry.
package tools

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/strausmann/fileee-mcp-server/internal/clientpool"
)

// addBoxDocumentEndpoint and removeBoxDocumentEndpoint are
// box_add_document's and box_remove_document's own wire endpoints for
// diagnostic logging (logToolEnd) — go-fileee's boxService.AddDocument/
// RemoveDocument (go-fileee/fileee/boxes.go), which deliberately do NOT
// use the /rest prefix every other endpoint constant in this package
// carries (that file's own doc comment on documentInBoxPath).
const (
	addBoxDocumentEndpoint    = "POST /api/fileeeboxes/:boxId/:documentId"
	removeBoxDocumentEndpoint = "DELETE /api/fileeeboxes/:boxId/:documentId"
)

// boxDocumentAddService is what box_add_document needs from
// *fileee.Client's Boxes field — narrowed to the single method this
// tool calls, the same "narrow the fake to what the tool actually
// calls" pattern contactCreateService/reminderCreateService already
// establish (write.go/write_people.go). client.Boxes is a
// fileee.BoxService, whose method set is a superset of this
// interface's, so it satisfies boxDocumentAddService without any
// adapter.
type boxDocumentAddService interface {
	AddDocument(ctx context.Context, boxID, documentID string) error
}

// boxDocumentRemoveService is what box_remove_document needs from
// *fileee.Client's Boxes field — narrowed to the single method this
// tool calls, the same pattern boxDocumentAddService establishes above.
// client.Boxes satisfies boxDocumentRemoveService without any adapter,
// for the same reason.
type boxDocumentRemoveService interface {
	RemoveDocument(ctx context.Context, boxID, documentID string) error
}

// boxDocumentInput is both box_add_document's and box_remove_document's
// shared parameters. Unlike updateContactInput/updateReminderInput
// (write.go/write_people.go), there is no patch/merge shape here to
// motivate pointer fields: BoxID and DocumentID are both genuinely
// required for either call to mean anything at all (there is no
// "leave this field alone" case — a membership toggle always names
// both a box and a document), so both are plain, non-omitempty
// strings — the same reasoning createReminderInput's own doc comment
// gives for its own required Description field (write_people.go): the
// go-sdk's schema-validation layer rejecting a call that omits either
// field entirely, before either handler ever runs, is the correct
// behavior here.
type boxDocumentInput struct {
	// BoxID identifies the box — required, the same ID
	// list_boxes/get_box returns.
	BoxID string `json:"boxId"`
	// DocumentID identifies the document — required, the same ID
	// list_documents/get_document returns.
	DocumentID string `json:"documentId"`
}

// boxDocumentOutput is box_add_document's and box_remove_document's
// shared structured result — an echo of the caller's own BoxID/
// DocumentID (supplied by the caller, not read back from Fileee; see
// this file's own package doc comment on why that keeps this type free
// of foreign text without any explicit check) plus Done, true once the
// backend call has confirmed the membership change. Both tools share
// this single type rather than each declaring an identical one, the
// same choice reminderOutput already makes for create_reminder/
// update_reminder (write_people.go's own doc comment) — there
// response_body_safety_test.go's registeredResponseBodyTypes lists
// reminderOutput twice, once per tool name, since that list's own
// invariant is "one entry per registered TOOL", not "one entry per
// distinct TYPE" (its own doc comment); this file's own two entries in
// that list follow the identical shape.
type boxDocumentOutput struct {
	// BoxID is the box's ID, as supplied by the caller.
	BoxID string `json:"boxId"`
	// DocumentID is the document's ID, as supplied by the caller.
	DocumentID string `json:"documentId"`
	// Done is true once the backend call has confirmed the membership
	// change.
	Done bool `json:"done"`
}

// boxDocumentResult builds either handler's success return once its
// backend call has returned no error — split out so both
// addBoxDocumentFromService and removeBoxDocumentFromService share a
// single call site, the same reasoning reminderResult gives for
// serving both create_reminder and update_reminder (write_people.go's
// own doc comment on that function). Unlike every sibling *Result
// function in this package (updateContactResult, createContactResult,
// reminderResult), this one returns a plain, empty *mcp.CallToolResult{}
// rather than calling wrapUntrustedLines — there is no foreign text in
// this call's shape to frame (this file's own package doc comment) —
// the same choice accountStatusFromService already makes
// (read_account.go) for the same underlying reason.
func boxDocumentResult(in boxDocumentInput) (*mcp.CallToolResult, boxDocumentOutput, error) {
	return &mcp.CallToolResult{}, boxDocumentOutput{BoxID: in.BoxID, DocumentID: in.DocumentID, Done: true}, nil
}

// addBoxDocumentFromService is addBoxDocumentHandler's logic below
// client resolution — split out so a test can drive it against a
// boxDocumentAddService fake (fakeBoxDocumentAddService,
// write_boxes_test.go) instead of a live *fileee.Client, the same
// pattern createContactFromService already establishes (write.go).
func addBoxDocumentFromService(ctx context.Context, service boxDocumentAddService, in boxDocumentInput) (*mcp.CallToolResult, boxDocumentOutput, error) {
	if err := service.AddDocument(ctx, in.BoxID, in.DocumentID); err != nil {
		return nil, boxDocumentOutput{}, fmt.Errorf("fileee-mcp: tools: %s: %w", ToolBoxAddDocument, err)
	}
	return boxDocumentResult(in)
}

// addBoxDocumentHandler resolves box_add_document. Both the empty-BoxID
// and empty-DocumentID checks run before clientFor — the same order
// every other handler in this package uses for its own required
// parameters (write.go's own doc comment on updateContactHandler) — so
// a caller's input mistake is rejected without spending a login round
// trip on it, and so this path is testable without a *clientpool.Pool
// at all (see write_boxes_test.go).
func addBoxDocumentHandler(p *clientpool.Pool, logger *slog.Logger) mcp.ToolHandlerFor[boxDocumentInput, boxDocumentOutput] {
	return func(ctx context.Context, _ *mcp.CallToolRequest, in boxDocumentInput) (*mcp.CallToolResult, boxDocumentOutput, error) {
		start := time.Now()
		logToolStart(ctx, logger, ToolBoxAddDocument, slog.String("boxId", in.BoxID), slog.String("documentId", in.DocumentID))

		if strings.TrimSpace(in.BoxID) == "" {
			err := fmt.Errorf("fileee-mcp: tools: %s: boxId must not be empty", ToolBoxAddDocument)
			logToolEnd(ctx, logger, ToolBoxAddDocument, start, "", 0, err)
			return nil, boxDocumentOutput{}, err
		}
		if strings.TrimSpace(in.DocumentID) == "" {
			err := fmt.Errorf("fileee-mcp: tools: %s: documentId must not be empty", ToolBoxAddDocument)
			logToolEnd(ctx, logger, ToolBoxAddDocument, start, "", 0, err)
			return nil, boxDocumentOutput{}, err
		}

		client, err := clientFor(ctx, p)
		if err != nil {
			logToolEnd(ctx, logger, ToolBoxAddDocument, start, "", 0, err)
			return nil, boxDocumentOutput{}, err
		}
		result, out, err := addBoxDocumentFromService(ctx, client.Boxes, in)
		logToolEnd(ctx, logger, ToolBoxAddDocument, start, addBoxDocumentEndpoint, 1, err)
		return result, out, err
	}
}

// removeBoxDocumentFromService is removeBoxDocumentHandler's logic
// below client resolution — split out so a test can drive it against a
// boxDocumentRemoveService fake (fakeBoxDocumentRemoveService,
// write_boxes_test.go) instead of a live *fileee.Client, the same
// pattern addBoxDocumentFromService establishes above.
func removeBoxDocumentFromService(ctx context.Context, service boxDocumentRemoveService, in boxDocumentInput) (*mcp.CallToolResult, boxDocumentOutput, error) {
	if err := service.RemoveDocument(ctx, in.BoxID, in.DocumentID); err != nil {
		return nil, boxDocumentOutput{}, fmt.Errorf("fileee-mcp: tools: %s: %w", ToolBoxRemoveDocument, err)
	}
	return boxDocumentResult(in)
}

// removeBoxDocumentHandler resolves box_remove_document. Same
// before-clientFor validation order as addBoxDocumentHandler above.
func removeBoxDocumentHandler(p *clientpool.Pool, logger *slog.Logger) mcp.ToolHandlerFor[boxDocumentInput, boxDocumentOutput] {
	return func(ctx context.Context, _ *mcp.CallToolRequest, in boxDocumentInput) (*mcp.CallToolResult, boxDocumentOutput, error) {
		start := time.Now()
		logToolStart(ctx, logger, ToolBoxRemoveDocument, slog.String("boxId", in.BoxID), slog.String("documentId", in.DocumentID))

		if strings.TrimSpace(in.BoxID) == "" {
			err := fmt.Errorf("fileee-mcp: tools: %s: boxId must not be empty", ToolBoxRemoveDocument)
			logToolEnd(ctx, logger, ToolBoxRemoveDocument, start, "", 0, err)
			return nil, boxDocumentOutput{}, err
		}
		if strings.TrimSpace(in.DocumentID) == "" {
			err := fmt.Errorf("fileee-mcp: tools: %s: documentId must not be empty", ToolBoxRemoveDocument)
			logToolEnd(ctx, logger, ToolBoxRemoveDocument, start, "", 0, err)
			return nil, boxDocumentOutput{}, err
		}

		client, err := clientFor(ctx, p)
		if err != nil {
			logToolEnd(ctx, logger, ToolBoxRemoveDocument, start, "", 0, err)
			return nil, boxDocumentOutput{}, err
		}
		result, out, err := removeBoxDocumentFromService(ctx, client.Boxes, in)
		logToolEnd(ctx, logger, ToolBoxRemoveDocument, start, removeBoxDocumentEndpoint, 1, err)
		return result, out, err
	}
}

// registerBoxWriteTools mounts box_add_document and box_remove_document
// onto s — called once from registerWriteTools (write.go), the same
// call site registers create_reminder/update_reminder from. Split into
// its own function (rather than inlined into registerWriteTools) so
// this file stays self-contained, the same reasoning
// registerReminderWriteTools' own doc comment gives (write_people.go).
func registerBoxWriteTools(s *mcp.Server, p *clientpool.Pool, logger *slog.Logger) {
	mcp.AddTool(s, &mcp.Tool{
		Name: ToolBoxAddDocument,
		Description: "File a document into a box in the calling user's Fileee account. Pass boxId " +
			"and documentId (both required) — use list_boxes/get_box to find a box's ID and " +
			"list_documents/search_documents to find a document's ID. Returns the box ID, document " +
			"ID, and whether the operation completed. Calling this twice for the same box/document " +
			"pair is not guaranteed to be a no-op on the second call. It does not remove the " +
			"document from any box it is already filed in — use box_remove_document for that.",
		Annotations: &mcp.ToolAnnotations{
			Title:           "Add document to box",
			ReadOnlyHint:    false,
			DestructiveHint: boolPtr(false),
			IdempotentHint:  false,
		},
	}, addBoxDocumentHandler(p, logger))

	mcp.AddTool(s, &mcp.Tool{
		Name: ToolBoxRemoveDocument,
		Description: "Remove a document from a box in the calling user's Fileee account. Pass boxId " +
			"and documentId (both required) — use list_boxes/get_box to find a box's ID and " +
			"list_documents/search_documents to find a document's ID. Returns the box ID, document " +
			"ID, and whether the operation completed. Calling this again for a document already " +
			"removed from the box changes nothing further. It does not delete the document itself — " +
			"only its membership in this box.",
		Annotations: &mcp.ToolAnnotations{
			Title:           "Remove document from box",
			ReadOnlyHint:    false,
			DestructiveHint: boolPtr(true),
			IdempotentHint:  true,
		},
	}, removeBoxDocumentHandler(p, logger))
}
