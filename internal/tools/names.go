// names.go centralizes every read tool's registered name as an exported
// constant and holds registeredReadTools(), a live probe of the tool set
// RegisterAll actually mounts (used by this package's own tests).
//
// Until the tool-exposure foundation refactor (Task 3), this file also
// held readToolNames and ReadToolKinds() — a hand-maintained
// name-to-access.KindRead map feeding Gangway's serve.WithToolKinds, kept
// deliberately independent from registeredReadTools() so a mistakenly
// added write tool couldn't classify itself as readable. Task 1 replaced
// per-tool KindRead/KindWrite authorization with a single
// access.AllowAll() instance, which made that map (and the two tests that
// cross-checked it against registeredReadTools() in both directions) dead
// code; this file no longer classifies tools at all. See
// docs/superpowers/plans/2026-08-12-fileee-werkzeuge-teil-a-lesend.md,
// "Globale Randbedingungen", for that mechanism's original reasoning.
package tools

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/strausmann/fileee-mcp-server/internal/clientpool"
)

// ToolListDocuments and ToolSearchDocuments are list_documents' and
// search_documents' registered names, exported so callers and tests don't
// have to repeat the string literals.
const (
	ToolListDocuments   = "list_documents"
	ToolSearchDocuments = "search_documents"

	// ToolSyncTags through ToolSyncConversations are the seven generic
	// sync tools' registered names (read_sync.go, Aufgabe 2b) — Diff's
	// counterpart to readServiceDescriptor's list/get pair.
	ToolSyncTags                = "sync_tags"
	ToolSyncCompanies           = "sync_companies"
	ToolSyncDocumentTypes       = "sync_document_types"
	ToolSyncDocumentTypeSchemes = "sync_document_type_schemes"
	ToolSyncContacts            = "sync_contacts"
	ToolSyncReminders           = "sync_reminders"
	ToolSyncConversations       = "sync_conversations"

	// ToolListTags through ToolGetDocumentTypeScheme are the four
	// reference-data services' registered list/get tool names
	// (read_reference.go, Aufgabe 3) — readServiceDescriptor's own
	// list/get pair, this time for Tags, Companies, DocumentTypes and
	// DocumentTypeSchemes.
	ToolListTags                = "list_tags"
	ToolGetTag                  = "get_tag"
	ToolListCompanies           = "list_companies"
	ToolGetCompany              = "get_company"
	ToolListDocumentTypes       = "list_document_types"
	ToolGetDocumentType         = "get_document_type"
	ToolListDocumentTypeSchemes = "list_document_type_schemes"
	ToolGetDocumentTypeScheme   = "get_document_type_scheme"

	// ToolListContacts through ToolGetConversation are the three
	// people-data services' registered list/get tool names
	// (read_people.go, Aufgabe 4) — readServiceDescriptor's own list/get
	// pair, this time for Contacts, Reminders and Conversations.
	ToolListContacts      = "list_contacts"
	ToolGetContact        = "get_contact"
	ToolListReminders     = "list_reminders"
	ToolGetReminder       = "get_reminder"
	ToolListConversations = "list_conversations"
	ToolGetConversation   = "get_conversation"

	// ToolGetDocument, ToolSyncDocuments and ToolListDocumentConversations
	// are the three document-detail tools' registered names
	// (Aufgabe 5-7, read.go) — bespoke handlers, not registered through
	// registerReadService/registerSync, since Document's Get/Diff and
	// Documents.Conversations don't fit either generic helper's signature.
	ToolGetDocument               = "get_document"
	ToolSyncDocuments             = "sync_documents"
	ToolListDocumentConversations = "list_document_conversations"

	// ToolListBoxes and ToolGetBox are list_boxes'/get_box's registered
	// names (Aufgabe 8, read_boxes.go) — bespoke handlers, BoxService is
	// not a fileee.ReadService[T].
	ToolListBoxes = "list_boxes"
	ToolGetBox    = "get_box"

	// ToolGetDocumentPDF, ToolGetPageImage and ToolGetPageOCR are the
	// three binary/OCR tools' registered names (Aufgabe 9-10,
	// read_binary.go) — bespoke handlers, DownloadPDF/DownloadPageImage/
	// PageOCR have no Query/Diff/Get shape.
	ToolGetDocumentPDF = "get_document_pdf"
	ToolGetPageImage   = "get_page_image"
	ToolGetPageOCR     = "get_page_ocr"

	// ToolGetAccountStatus is get_account_status' registered name
	// (Aufgabe 11, read_account.go) — bespoke handler, no ReadService[T]
	// shape and no input parameters.
	ToolGetAccountStatus = "get_account_status"

	// ToolGetRuntimeStats is get_runtime_stats' registered name (Aufgabe
	// C1, ops.go) — this server's own call-count/error diagnostics, not a
	// Fileee-backed tool at all.
	ToolGetRuntimeStats = "get_runtime_stats"

	// ToolGetToolManifest is get_tool_manifest's registered name (Aufgabe
	// C2, ops.go) — introspects the calling server instance's own live
	// tool set, not a Fileee-backed tool either.
	ToolGetToolManifest = "get_tool_manifest"

	// ToolSelfCheck is self_check's registered name (Aufgabe C3, ops.go)
	// — the one operational tool that DOES reach Fileee, deliberately: a
	// single, self-limited login attempt, kept apart from clientFor's own
	// resolution path so a login failure and a network failure are never
	// reported as the same thing (see ops.go's own doc comment section).
	ToolSelfCheck = "self_check"

	// ToolWhoami is whoami's registered name (Task 3, whoami.go) — reports
	// the caller's verified identity, its mapped fileee account
	// (its plain login email) and the server's mode/capabilities. Not
	// Fileee-backed either.
	ToolWhoami = "whoami"

	// ToolUpdateContact is update_contact's registered name (Task 1,
	// write.go) — the first write-class tool: a patch/merge update
	// (Get, apply the caller's supplied fields, Update) over
	// fileee.WriteService[fileee.Contact], the template every later
	// write tool in this file follows.
	ToolUpdateContact = "update_contact"

	// ToolCreateContact is create_contact's registered name (Task 2,
	// write.go) — the second write-class tool: a single
	// fileee.WriteService[fileee.Contact].Create call, no prior Get
	// (there is nothing to merge onto — the contact does not exist
	// yet).
	ToolCreateContact = "create_contact"

	// ToolCreateReminder and ToolUpdateReminder are create_reminder's
	// and update_reminder's registered names (Task 3, write_people.go)
	// — the third and fourth write-class tools, over
	// fileee.ReminderService the same way ToolCreateContact/
	// ToolUpdateContact are over fileee.Client.Contacts: a single
	// Create call (no prior Get) and a Get/apply/Update patch/merge,
	// respectively.
	ToolCreateReminder = "create_reminder"
	ToolUpdateReminder = "update_reminder"

	// ToolBoxAddDocument and ToolBoxRemoveDocument are
	// box_add_document's and box_remove_document's registered names
	// (Task 4, write_boxes.go) — the fifth and sixth write-class
	// tools, over fileee.BoxService: neither creates nor patch/merges
	// an entity the way every write tool above does, each is a single
	// AddDocument/RemoveDocument call toggling one document's
	// membership in one box.
	ToolBoxAddDocument    = "box_add_document"
	ToolBoxRemoveDocument = "box_remove_document"

	// ToolUploadDocument is upload_document's registered name (Task 5,
	// write_documents.go) — the seventh write-class tool, over
	// *fileee.Client.Documents.Upload: a single call, like
	// ToolCreateContact/ToolCreateReminder, but one whose own error
	// return (fileee.ErrDuplicateDocument) is treated as a normal,
	// informative success rather than a failure — see write_documents.go's
	// own package doc comment.
	ToolUploadDocument = "upload_document"
)

// registeredReadTools mounts RegisterAll onto a throwaway server and reads
// its tools back over an in-memory client-server connection — the ground
// truth of what RegisterAll actually mounts. descriptions_test.go's
// description-length check runs against this list, because that question
// ("what does a caller see") is exactly what a real tools/list call
// answers; response_body_safety_test.go's
// TestRegisteredResponseBodyTypesCoversEveryReadTool cross-checks its own
// registeredResponseBodyTypes list's length against this same live set,
// so neither a forgotten nor a stale response-body-type entry goes
// unnoticed.
//
// go-sdk v1.7.0's *mcp.Server keeps its registered tools in an unexported
// featureSet (see mcp.Server.AddTool/listTools) with no public accessor —
// there is no "probe.Tools()". Introspecting a server's own tool set
// therefore means acting like a real MCP client and asking it, the same
// pattern this repo's own tests already use for exactly this purpose
// (internal/server/server_test.go, toolNamesOf).
//
// p is (*clientpool.Pool)(nil): none of RegisterAll's tool handlers run
// during registration or during a tools/list round-trip — only AddTool's
// schema derivation and the ListTools call below do, neither of which
// touches p. logger is a discarding *slog.Logger for the same reason:
// nothing here calls a handler, so nothing here logs.
func registeredReadTools() []*mcp.Tool {
	ctx := context.Background()

	probe := mcp.NewServer(&mcp.Implementation{Name: "probe", Version: "0"}, nil)
	RegisterAll(probe, (*clientpool.Pool)(nil), ServerInfo{}, slog.New(slog.DiscardHandler))

	clientTransport, serverTransport := mcp.NewInMemoryTransports()
	serverSession, err := probe.Connect(ctx, serverTransport, nil)
	if err != nil {
		panic(fmt.Errorf("fileee-mcp: tools: registeredReadTools: connect probe server: %w", err))
	}
	// A close failure on a throwaway in-memory session that already served its
	// one tools/list round-trip changes nothing about the result already
	// captured below — same reasoning as cmd/fileee-mcp-server/main.go's
	// resp.Body.Close() on a healthcheck response already fully read.
	defer func() { _ = serverSession.Close() }()

	client := mcp.NewClient(&mcp.Implementation{Name: "registered-read-tools-probe", Version: "0"}, nil)
	clientSession, err := client.Connect(ctx, clientTransport, nil)
	if err != nil {
		panic(fmt.Errorf("fileee-mcp: tools: registeredReadTools: connect probe client: %w", err))
	}
	defer func() { _ = clientSession.Close() }()

	res, err := clientSession.ListTools(ctx, nil)
	if err != nil {
		panic(fmt.Errorf("fileee-mcp: tools: registeredReadTools: list tools: %w", err))
	}
	return res.Tools
}
