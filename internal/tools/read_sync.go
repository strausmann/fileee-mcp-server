// read_sync.go mounts one incremental-sync tool per fileee.ReadService[T] —
// sync_tags, sync_companies, sync_document_types, sync_document_type_schemes,
// sync_contacts, sync_reminders, sync_conversations — on top of Diff, the
// third operation every one of the seven generic services offers alongside
// Query and Get (read_generic.go).
//
// syncDescriptor is deliberately NOT an extension of readServiceDescriptor
// (read_generic.go), even though the two share almost every field: a
// descriptor with both list/get AND sync fields on one type would force
// this file's registrations to wait for Aufgabe 3/4, which build the
// concrete list/get descriptors this generic type would then also need to
// carry. Keeping syncDescriptor its own, smaller type lets these seven
// tools exist independently of that work — Antrag #46 — at the cost of one
// duplicated line per service (the func(*fileee.Client) fileee.ReadService[T]
// closure appears once here and once in Aufgabe 3/4's own descriptor).
package tools

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/strausmann/go-fileee/fileee"

	"github.com/strausmann/fileee-mcp-server/internal/clientpool"
)

// encodeCursor renders a fileee.Cursor as an opaque string a caller can
// hold onto between two calls to the same sync tool, and pass back as
// genericSyncInput.Cursor unchanged. The encoding (JSON, then base64) is
// this server's own — it never crosses the wire to Fileee itself
// (buildLocalResults, go-fileee, builds Fileee's own wire form from
// cursor.Known directly) — so there is no compatibility constraint beyond
// "this server can read back what it just wrote".
func encodeCursor(c fileee.Cursor) (string, error) {
	raw, err := json.Marshal(c)
	if err != nil {
		return "", fmt.Errorf("fileee-mcp: tools: encode cursor: %w", err)
	}
	return base64.StdEncoding.EncodeToString(raw), nil
}

// decodeCursor reverses encodeCursor. Both decoding stages — base64, then
// JSON — can fail on a caller-supplied string that never came from
// encodeCursor (a typo, a cursor for a different MCP server entirely, a
// deliberately malformed value); either failure is reported as an
// ordinary error, never a panic, since this runs on every sync call with
// caller-controlled input, unlike mustNotLeakUntrustedText's
// once-at-registration checks below.
//
// Boundary case decodeCursor itself does NOT reject: a string that is
// valid base64 AND decodes to valid JSON, but the wrong shape for
// fileee.Cursor (e.g. `{}`, or JSON for some other type entirely), decodes
// cleanly — EntityType simply comes out empty. No security issue results:
// checkCursorEntityType's comparison against the caller's own EntityType
// catches it next, before any Diff call. But the actual safeguard against
// a malformed-but-well-typed cursor therefore lives in that type check,
// not in this decoding step — worth knowing before assuming decodeCursor
// itself validates shape.
func decodeCursor(encoded string) (fileee.Cursor, error) {
	raw, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil {
		return fileee.Cursor{}, fmt.Errorf("fileee-mcp: tools: decode cursor: invalid encoding: %w", err)
	}
	var c fileee.Cursor
	if err := json.Unmarshal(raw, &c); err != nil {
		return fileee.Cursor{}, fmt.Errorf("fileee-mcp: tools: decode cursor: invalid payload: %w", err)
	}
	return c, nil
}

// checkCursorEntityType decodes encoded and rejects it unless its
// EntityType matches want — the guard restService[T].Diff itself never
// applies (fileee/service.go's Diff reads only cursor.Known, never
// cursor.EntityType; verified against the actual request it builds, not
// assumed). Without this check, a cursor minted by one sync tool (e.g.
// sync_tags, EntityType "Tag") handed to a different one (sync_companies,
// wants "Company") would not fail — it would silently run Diff against
// the wrong service with the wrong "known" IDs, producing entries that
// look like real changes or, worse, silently omitting real ones.
//
// An empty encoded string means "no prior cursor" — the caller's first
// sync call for this tool — and returns a fresh fileee.NewCursor(want),
// not an error: genericSyncInput.Cursor is optional for exactly this
// reason (see that field's own doc comment).
func checkCursorEntityType(encoded string, want string) (fileee.Cursor, error) {
	if strings.TrimSpace(encoded) == "" {
		return fileee.NewCursor(want), nil
	}
	cursor, err := decodeCursor(encoded)
	if err != nil {
		return fileee.Cursor{}, err
	}
	if cursor.EntityType != want {
		return fileee.Cursor{}, fmt.Errorf(
			"cursor is for entity type %q, this tool syncs %q — pass back only a cursor this same tool returned",
			cursor.EntityType, want)
	}
	return cursor, nil
}

// syncDescriptor describes one ReadService[T].Diff call as a sync tool —
// this file's own, smaller counterpart to readServiceDescriptor
// (read_generic.go; see this file's own doc comment for why they are two
// types, not one).
//
// UntrustedLine and PoisonProbe carry exactly the same meaning and the
// same requirement (PoisonProbe is mandatory whenever UntrustedLine is
// set, and forbidden when it is nil) as readServiceDescriptor's own
// fields of the same name — see those fields' doc comments
// (read_generic.go) for the full reasoning; it is not repeated here.
// registerSync enforces it via mustNotLeakUntrustedText, the same
// once-at-registration check registerReadService runs, extracted so both
// descriptor types can share it without sharing a struct type Go's
// generics cannot express (no structural typing across distinct named
// structs).
//
// Four of the seven services this file mounts carry foreign text a third
// party chose (a contact's own name, a reminder's description that may be
// copied from a document, a conversation's subject line chosen by
// whoever is on the other end, and — corrected after Aufgabe 3's own
// field research, docs/research/2026-08-12-fileee-go-library-feldnamen.md
// — a company's name when Fileee extracted it from a document rather
// than the account holder entering it themselves) and therefore set both
// fields; the other three (Tag, DocumentType, DocumentTypeScheme) are,
// like Aufgabe 3's own list/get descriptors for the same three types,
// entirely the account holder's own naming and leave both nil.
type syncDescriptor[T any, S any] struct {
	// SyncName is the registered tool name.
	SyncName string
	// SyncTitle is this tool's Annotations.Title — the short, human-facing
	// name a client shows or gates on (Task 4, the MCP connector
	// standard), independent of SyncDescription below.
	SyncTitle string
	// SyncDescription is this tool's Description, held to the same
	// four-part standard (what, returns, when, does-not) and minimum
	// length descriptions_test.go checks for every tool RegisterAll
	// mounts — checked there since registerSyncTools is wired into
	// RegisterAll (read.go).
	SyncDescription string
	// EntityType must match the library's own convention name for this
	// resource (e.g. "Tag", "Company", "Contact" — see the NewCursor calls
	// throughout go-fileee for the full, authoritative list; there is no
	// exported constant for it). checkCursorEntityType compares a
	// caller-supplied cursor's own EntityType against this value before
	// ever calling Diff.
	EntityType string
	// Service resolves this descriptor's ReadService[T] from an
	// already-authenticated client — see readServiceDescriptor.Service's
	// own doc comment (read_generic.go) for the identical pattern this
	// field follows.
	Service func(*fileee.Client) fileee.ReadService[T]
	// Summarize renders one entity's Fileee-owned fields as S — never
	// foreign free text, see readServiceDescriptor.Summarize's own doc
	// comment (read_generic.go) for why, and this type's own doc comment
	// for which of the seven services this applies to.
	Summarize func(*T) S
	// UntrustedLine and PoisonProbe: see readServiceDescriptor's fields of
	// the same name (read_generic.go) for their full doc comments — the
	// contract is identical here.
	UntrustedLine func(*T) string
	PoisonProbe   func(marker string) *T
}

// genericSyncInput are every generic sync tool's parameters.
type genericSyncInput struct {
	// Cursor is the opaque value a previous call to this SAME tool
	// returned as NextCursor. Omit it (or pass an empty string) for the
	// very first sync call, which then runs a full initial sync — see
	// checkCursorEntityType. Passing a cursor another tool returned (a
	// mismatched EntityType) is rejected before any network access.
	Cursor string `json:"cursor,omitempty" jsonschema:"opaque cursor returned by a previous call to this same tool; omit for the initial sync"`
}

// genericSyncOutput is a generic sync tool's structured result
// (CallToolResult.StructuredContent).
type genericSyncOutput[S any] struct {
	// Entries are the entities that changed since the cursor passed in —
	// on the very first call (no cursor), every entity currently in the
	// account.
	Entries []S `json:"entries"`
	// DeletedIDs are entities that existed at the previous cursor but no
	// longer do.
	DeletedIDs []string `json:"deletedIds"`
	// NextCursor is this call's own opaque cursor value — pass it back as
	// genericSyncInput.Cursor on the next call to this same tool to fetch
	// only what changed since.
	NextCursor string `json:"nextCursor"`
	// TotalRows is the caller's total, current entry count for this
	// resource, independent of how many rows this one call actually
	// returned.
	TotalRows int `json:"totalRows"`
}

// registerSync mounts d.SyncName onto s, resolving its Fileee connection
// through p on every call (see clientFor, read.go) — the same pattern
// registerReadService uses (read_generic.go), generalized over any
// fileee.ReadService[T].Diff instead of Query/Get.
//
// logger receives d.SyncName's diagnostic log, threaded straight through
// to genericSyncHandler — the same pattern registerReadService follows
// for its own two tools (read_generic.go). Aufgabe 2c closed the gap
// where this parameter did not exist yet, alongside the identical gap in
// registerReadService (#45/#46 both shipped without it).
//
// It panics — like registerReadService already does for a leaking
// list/get descriptor — if d fails mustNotLeakUntrustedText's check. That
// check runs once, here, not per request: see mustNotLeakUntrustedText's
// own doc comment (read_generic.go) for why.
func registerSync[T any, S any](s *mcp.Server, p *clientpool.Pool, logger *slog.Logger, d syncDescriptor[T, S]) {
	mustNotLeakUntrustedText("syncDescriptor", d.SyncName, d.UntrustedLine, d.PoisonProbe, d.Summarize)

	mcp.AddTool(s, &mcp.Tool{
		Name:        d.SyncName,
		Description: d.SyncDescription,
		Annotations: &mcp.ToolAnnotations{ReadOnlyHint: true, Title: d.SyncTitle},
	}, genericSyncHandler(p, logger, d))
}

// genericSyncHandler resolves d.SyncName. It checks the caller's cursor
// (checkCursorEntityType) before ever touching the network, and before
// clientFor — the same "reject cheap mistakes before spending a login
// round trip" order genericGetHandler already uses for its own required
// parameter (read_generic.go) — then splits client resolution from the
// actual Diff/summarize/frame logic (syncFromService) for the same
// testability reason listFromService does.
//
// It logs through logger the same way genericListHandler/genericGetHandler
// do (read_generic.go, see genericListHandler's own doc comment for the
// endpoint-argument caveat that applies here too): the caller's cursor is
// logged at debug only via logToolStart, on every path including a
// rejected cursor or a clientFor failure.
func genericSyncHandler[T any, S any](p *clientpool.Pool, logger *slog.Logger, d syncDescriptor[T, S]) mcp.ToolHandlerFor[genericSyncInput, genericSyncOutput[S]] {
	return func(ctx context.Context, _ *mcp.CallToolRequest, in genericSyncInput) (*mcp.CallToolResult, genericSyncOutput[S], error) {
		start := time.Now()
		logToolStart(ctx, logger, d.SyncName, slog.String("cursor", in.Cursor))

		cursor, err := checkCursorEntityType(in.Cursor, d.EntityType)
		if err != nil {
			wrapped := fmt.Errorf("fileee-mcp: tools: %s: %w", d.SyncName, err)
			logToolEnd(ctx, logger, d.SyncName, start, "", 0, wrapped)
			return nil, genericSyncOutput[S]{}, wrapped
		}

		client, err := clientFor(ctx, p)
		if err != nil {
			logToolEnd(ctx, logger, d.SyncName, start, "", 0, err)
			return nil, genericSyncOutput[S]{}, err
		}
		result, out, err := syncFromService(ctx, d, d.Service(client), cursor)
		logToolEnd(ctx, logger, d.SyncName, start, "", len(out.Entries), err)
		return result, out, err
	}
}

// syncFromService is genericSyncHandler's logic below client resolution
// and cursor validation: call Diff, summarize every changed row into S,
// collect the foreign lines UntrustedLine hands back, frame them
// (wrapUntrustedLines, read_generic.go), and encode the returned
// NextCursor — kept separate from genericSyncHandler so a test can drive
// it directly against a fake service and an already-validated cursor
// instead of a live *fileee.Client and a caller-supplied string.
func syncFromService[T any, S any](ctx context.Context, d syncDescriptor[T, S], service fileee.ReadService[T], cursor fileee.Cursor) (*mcp.CallToolResult, genericSyncOutput[S], error) {
	res, err := service.Diff(ctx, cursor)
	if err != nil {
		return nil, genericSyncOutput[S]{}, fmt.Errorf("fileee-mcp: tools: %s: %w", d.SyncName, err)
	}

	out := genericSyncOutput[S]{
		DeletedIDs: res.DeletedIDs,
		TotalRows:  res.TotalRows,
		Entries:    make([]S, 0, len(res.Rows)),
	}
	lines := make([]string, 0, len(res.Rows))
	for i := range res.Rows {
		entry := res.Rows[i]
		out.Entries = append(out.Entries, d.Summarize(&entry))
		if line := untrustedLineOfSync(d, &entry); line != "" {
			lines = append(lines, line)
		}
	}

	nextCursor, err := encodeCursor(res.NextCursor)
	if err != nil {
		return nil, genericSyncOutput[S]{}, fmt.Errorf("fileee-mcp: tools: %s: encode next cursor: %w", d.SyncName, err)
	}
	out.NextCursor = nextCursor

	result, err := wrapUntrustedLines(lines)
	if err != nil {
		return nil, genericSyncOutput[S]{}, fmt.Errorf("fileee-mcp: tools: %s: %w", d.SyncName, err)
	}
	return result, out, nil
}

// untrustedLineOfSync mirrors untrustedLineOf (read_generic.go) for
// syncDescriptor — the same "nil UntrustedLine means this T carries no
// foreign text at all" rule, duplicated rather than shared for the same
// reason syncDescriptor itself is its own type: there is no Go construct
// that lets one four-line function read either descriptor's UntrustedLine
// field without the field itself being on a shared, named type.
func untrustedLineOfSync[T any, S any](d syncDescriptor[T, S], entity *T) string {
	if d.UntrustedLine == nil {
		return ""
	}
	return d.UntrustedLine(entity)
}

// --- registerSyncTools: all seven services, wired ---

// The seven syncTagSummary/syncCompanySummary/… structs below are this
// file's OWN, deliberately distinctly-named placeholders — NOT the
// tagSummary/companySummary/… structs Aufgabe 3/4 build. Two reasons, not
// one:
//
//  1. read_generic_test.go (Aufgabe 2, already merged) already declares a
//     package-level tagSummary fixture for its own tests. Naming a
//     production struct tagSummary here would collide with that test
//     fixture the moment `go test` compiles this package — not a future,
//     hypothetical merge conflict, but a build failure today.
//  2. Aufgabe 3/4 have not run in this branch yet, so their eventual
//     field choices for companySummary/documentTypeSummary/… are not
//     knowable here — matching them exactly, as the plan's original
//     wording suggested, is not something this task can actually do.
//
// The Sync-prefixed names below are therefore this file's own,
// permanently distinct types — not a placeholder meant to be renamed away
// once Aufgabe 3/4 land, but the sync tools' own summaries, on purpose
// decoupled from whatever list/get chooses to expose. A little
// duplication (the same handful of fields, twice) is the trade this
// mirrors the rest of the file's own "duplicate a Service accessor
// instead of forcing an ordering dependency" choice.
type syncTagSummary struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

// syncCompanySummary deliberately omits CompanyName — corrected after
// Aufgabe 3's own field research
// (docs/research/2026-08-12-fileee-go-library-feldnamen.md): a company
// Fileee extracted from a document (FromUserDB == false) carries that
// document's sender's own chosen name, not the account holder's, the
// same FromUserDB-gated distinction syncContactSummary's own doc comment
// already applies to Contact. companySyncDescriptor's own UntrustedLine
// composes it instead, framed, never structured.
type syncCompanySummary struct {
	ID              string `json:"id"`
	ContactType     string `json:"contactType"`
	ContactStatus   string `json:"contactStatus"`
	DocumentCounter int    `json:"documentCounter"`
	FromUserDB      bool   `json:"fromUserDb"`
}

type syncDocumentTypeSummary struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

type syncDocumentTypeSchemeSummary struct {
	ID string `json:"id"`
}

// syncContactSummary deliberately omits every field a third party (not
// the account holder) chose — FirstName, LastName, CompanyName, Email,
// PhoneNumber, URL and friends are all contact-supplied, not
// Fileee-assigned (fileee.Contact, go-fileee/fileee/types.go). Only
// structural, Fileee-owned metadata survives into the typed summary;
// contactSyncDescriptor's own UntrustedLine composes the display name
// instead, framed, never structured.
type syncContactSummary struct {
	ID              string `json:"id"`
	CompanyID       string `json:"companyId"`
	ContactType     string `json:"contactType"`
	ContactStatus   string `json:"contactStatus"`
	DocumentCounter int    `json:"documentCounter"`
}

// syncReminderSummary omits Description and DetailedDescription — a
// reminder's own text can be copied from the document it is attached to
// (fileee.Reminder, go-fileee/fileee/reminders.go's own doc comment), the
// same "may originate from whoever sent the document" reasoning
// list_documents already applies to a document's title (read.go).
type syncReminderSummary struct {
	ID         string `json:"id"`
	DocumentID string `json:"documentId"`
	StartDate  string `json:"startDate"`
	Done       bool   `json:"done"`
}

// syncConversationSummary omits Title — a conversation's subject line is
// chosen by whoever is on the other end of it, not by the account
// holder.
type syncConversationSummary struct {
	ID               string `json:"id"`
	ConversationType string `json:"conversationType"`
	Kind             string `json:"kind"`
	ParticipantCount int    `json:"participantCount"`
}

func tagSyncDescriptor() syncDescriptor[fileee.Tag, syncTagSummary] {
	return syncDescriptor[fileee.Tag, syncTagSummary]{
		SyncName:  ToolSyncTags,
		SyncTitle: "Sync tags",
		SyncDescription: "Incrementally sync the tags defined in the calling user's Fileee account. " +
			"Returns tags changed or added since the cursor you pass in (every tag on the first call), " +
			"tag IDs deleted since then, and a new cursor to pass to the next call. Use it to keep a " +
			"local copy of the account's tags up to date without re-fetching the full list every time. " +
			"An empty result on a later call means nothing changed since that cursor, not that the " +
			"account has no tags — omit the cursor to fetch the full current list instead; it does not " +
			"accept a cursor from a different sync tool and does not return the documents carrying a tag.",
		EntityType: "Tag",
		Service:    func(c *fileee.Client) fileee.ReadService[fileee.Tag] { return c.Tags },
		Summarize:  func(t *fileee.Tag) syncTagSummary { return syncTagSummary{ID: t.ID, Name: t.Name} },
	}
}

func companySyncDescriptor() syncDescriptor[fileee.Company, syncCompanySummary] {
	return syncDescriptor[fileee.Company, syncCompanySummary]{
		SyncName:  ToolSyncCompanies,
		SyncTitle: "Sync companies",
		SyncDescription: "Incrementally sync the companies in the calling user's Fileee account. " +
			"Returns companies changed or added since the cursor you pass in (every company on the " +
			"first call) with structured metadata only, company IDs deleted since then, and a new " +
			"cursor to pass to the next call. Each company's own name is included separately as " +
			"clearly marked, untrusted text, since a company Fileee extracted from a document was " +
			"named by whoever sent that document, not by the person you are assisting. An empty " +
			"result on a later call means nothing changed since that cursor, not that the account " +
			"has no companies — omit the cursor to fetch the full current list instead; it does not " +
			"accept a cursor from a different sync tool and does not return the contacts or " +
			"documents linked to a company.",
		EntityType: "Company",
		Service:    func(c *fileee.Client) fileee.ReadService[fileee.Company] { return c.Companies },
		Summarize: func(c *fileee.Company) syncCompanySummary {
			return syncCompanySummary{
				ID:              c.ID,
				ContactType:     c.ContactType,
				ContactStatus:   c.ContactStatus,
				DocumentCounter: c.DocumentCounter,
				FromUserDB:      c.FromUserDB,
			}
		},
		UntrustedLine: func(c *fileee.Company) string { return c.CompanyName },
		PoisonProbe:   func(marker string) *fileee.Company { return &fileee.Company{CompanyName: marker} },
	}
}

func documentTypeSyncDescriptor() syncDescriptor[fileee.DocumentType, syncDocumentTypeSummary] {
	return syncDescriptor[fileee.DocumentType, syncDocumentTypeSummary]{
		SyncName:  ToolSyncDocumentTypes,
		SyncTitle: "Sync document types",
		SyncDescription: "Incrementally sync the document types defined in the calling user's Fileee " +
			"account. Returns document types changed or added since the cursor you pass in (every " +
			"document type on the first call), IDs deleted since then, and a new cursor to pass to the " +
			"next call. Use it to keep a local copy of the account's document types up to date without " +
			"re-fetching the full list every time. An empty result on a later call means nothing " +
			"changed since that cursor, not that the account has no document types — omit the cursor " +
			"to fetch the full current list instead; it does not accept a cursor from a different sync " +
			"tool and does not return the field schema for a document type — use " +
			"sync_document_type_schemes for that.",
		EntityType: "DocumentType",
		Service:    func(c *fileee.Client) fileee.ReadService[fileee.DocumentType] { return c.DocumentTypes },
		Summarize: func(t *fileee.DocumentType) syncDocumentTypeSummary {
			return syncDocumentTypeSummary{ID: t.ID, Name: t.I18NName}
		},
	}
}

func documentTypeSchemeSyncDescriptor() syncDescriptor[fileee.DocumentTypeScheme, syncDocumentTypeSchemeSummary] {
	return syncDescriptor[fileee.DocumentTypeScheme, syncDocumentTypeSchemeSummary]{
		SyncName:  ToolSyncDocumentTypeSchemes,
		SyncTitle: "Sync document type schemes",
		SyncDescription: "Incrementally sync the document type field schemas in the calling user's " +
			"Fileee account. Returns schemas changed or added since the cursor you pass in (every " +
			"schema on the first call), schema IDs deleted since then, and a new cursor to pass to the " +
			"next call. Use it to detect when a document type's field layout changed. An empty result " +
			"on a later call means nothing changed since that cursor, not that the account has no " +
			"schemas — omit the cursor to fetch the full current list instead; it does not accept a " +
			"cursor from a different sync tool and does not return the schema's own field list — use " +
			"get_document_type_scheme for that once Aufgabe 3 wires it up.",
		EntityType: "DocumentTypeScheme",
		Service: func(c *fileee.Client) fileee.ReadService[fileee.DocumentTypeScheme] {
			return c.DocumentTypeSchemes
		},
		Summarize: func(t *fileee.DocumentTypeScheme) syncDocumentTypeSchemeSummary {
			return syncDocumentTypeSchemeSummary{ID: t.ID}
		},
	}
}

func contactSyncDescriptor() syncDescriptor[fileee.Contact, syncContactSummary] {
	return syncDescriptor[fileee.Contact, syncContactSummary]{
		SyncName:  ToolSyncContacts,
		SyncTitle: "Sync contacts",
		SyncDescription: "Incrementally sync the contacts in the calling user's Fileee account. " +
			"Returns contacts changed or added since the cursor you pass in (every contact on the " +
			"first call) with structured metadata only, contact IDs deleted since then, and a new " +
			"cursor to pass to the next call. Each contact's own name is included separately as " +
			"clearly marked, untrusted text, since it was supplied by that contact, not by the person " +
			"you are assisting. An empty result on a later call means nothing changed since that " +
			"cursor, not that the account has no contacts — omit the cursor to fetch the full current " +
			"list instead; it does not accept a cursor from a different sync tool.",
		EntityType: "Contact",
		Service:    func(c *fileee.Client) fileee.ReadService[fileee.Contact] { return c.Contacts },
		Summarize: func(c *fileee.Contact) syncContactSummary {
			return syncContactSummary{
				ID:              c.ID,
				CompanyID:       c.CompanyID,
				ContactType:     string(c.ContactType),
				ContactStatus:   string(c.ContactStatus),
				DocumentCounter: c.DocumentCounter,
			}
		},
		UntrustedLine: func(c *fileee.Contact) string {
			return strings.TrimSpace(c.FirstName + " " + c.LastName)
		},
		PoisonProbe: func(marker string) *fileee.Contact { return &fileee.Contact{LastName: marker} },
	}
}

func reminderSyncDescriptor() syncDescriptor[fileee.Reminder, syncReminderSummary] {
	return syncDescriptor[fileee.Reminder, syncReminderSummary]{
		SyncName:  ToolSyncReminders,
		SyncTitle: "Sync reminders",
		SyncDescription: "Incrementally sync the reminders in the calling user's Fileee account. " +
			"Returns reminders changed or added since the cursor you pass in (every reminder on the " +
			"first call) with structured metadata only, reminder IDs deleted since then, and a new " +
			"cursor to pass to the next call. Each reminder's own description is included separately " +
			"as clearly marked, untrusted text, since it may have been copied from the document it is " +
			"attached to rather than written by the person you are assisting. An empty result on a " +
			"later call means nothing changed since that cursor, not that the account has no " +
			"reminders — omit the cursor to fetch the full current list instead; it does not accept a " +
			"cursor from a different sync tool.",
		EntityType: "Reminder",
		Service:    func(c *fileee.Client) fileee.ReadService[fileee.Reminder] { return c.Reminders },
		Summarize: func(r *fileee.Reminder) syncReminderSummary {
			return syncReminderSummary{ID: r.ID, DocumentID: r.DocumentID, StartDate: r.StartDate, Done: r.Done}
		},
		UntrustedLine: func(r *fileee.Reminder) string { return r.Description },
		PoisonProbe:   func(marker string) *fileee.Reminder { return &fileee.Reminder{Description: marker} },
	}
}

func conversationSyncDescriptor() syncDescriptor[fileee.Conversation, syncConversationSummary] {
	return syncDescriptor[fileee.Conversation, syncConversationSummary]{
		SyncName:  ToolSyncConversations,
		SyncTitle: "Sync conversations",
		SyncDescription: "Incrementally sync the conversations in the calling user's Fileee account. " +
			"Returns conversations changed or added since the cursor you pass in (every conversation " +
			"on the first call) with structured metadata only, conversation IDs deleted since then, " +
			"and a new cursor to pass to the next call. Each conversation's own subject/title is " +
			"included separately as clearly marked, untrusted text, since it was chosen by whoever is " +
			"on the other end, not by the person you are assisting. An empty result on a later call " +
			"means nothing changed since that cursor, not that the account has no conversations — " +
			"omit the cursor to fetch the full current list instead; it does not accept a cursor from " +
			"a different sync tool.",
		EntityType: "Conversation",
		Service:    func(c *fileee.Client) fileee.ReadService[fileee.Conversation] { return c.Conversations },
		Summarize: func(c *fileee.Conversation) syncConversationSummary {
			return syncConversationSummary{
				ID:               c.ID,
				ConversationType: c.ConversationType,
				Kind:             c.Kind,
				ParticipantCount: len(c.Participants),
			}
		},
		UntrustedLine: func(c *fileee.Conversation) string { return c.Title },
		PoisonProbe:   func(marker string) *fileee.Conversation { return &fileee.Conversation{Title: marker} },
	}
}

// registerSyncTools mounts all seven sync tools onto s. Split out from
// RegisterAll itself (read.go) the same way registerReferenceTools and
// registerPeopleTools will be (Aufgabe 3/4); RegisterAll calls this
// directly (read.go). This file's own tests also call it directly, without
// going through RegisterAll, to keep those tests independent of the
// unrelated tools RegisterAll mounts.
//
// logger is threaded straight through to every registerSync call — the
// same logger RegisterAll itself received, never a fresh one built here
// (see RegisterAll's own doc comment, read.go).
func registerSyncTools(s *mcp.Server, p *clientpool.Pool, logger *slog.Logger) {
	registerSync(s, p, logger, tagSyncDescriptor())
	registerSync(s, p, logger, companySyncDescriptor())
	registerSync(s, p, logger, documentTypeSyncDescriptor())
	registerSync(s, p, logger, documentTypeSchemeSyncDescriptor())
	registerSync(s, p, logger, contactSyncDescriptor())
	registerSync(s, p, logger, reminderSyncDescriptor())
	registerSync(s, p, logger, conversationSyncDescriptor())
}
