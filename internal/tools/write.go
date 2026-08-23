// write.go registers this server's write-class tools — go-fileee
// mutations exposed as MCP tools, gated behind honest
// ReadOnlyHint:false/DestructiveHint/IdempotentHint annotations
// (docs/superpowers/plans/2026-08-23-fileee-mcp-write-tools.md). Unlike
// the read tools RegisterAll otherwise mounts (read.go), every handler
// here calls a mutating fileee.WriteService[T] method — Create, Update,
// or, in a later task, Delete — through the same clientFor(ctx, p)
// account-isolation path read.go's own handlers use (ADR-0012): a write
// tool never gets a different, less-verified path to the caller's
// Fileee account than a read tool does. Write tools are always mounted,
// the same way every read tool is — there is no separate opt-in gate in
// this task (see the plan's own "Architecture" note).
//
// update_contact (Task 1, this file's first tool) is a PATCH/MERGE
// update, not a PUT/replace: the caller supplies only the fields they
// want changed (updateContactInput's pointer fields, nil meaning "leave
// unchanged"), the handler loads the current contact
// (client.Contacts.Get), applies just the supplied fields onto it, and
// sends the merged entity to client.Contacts.Update. A caller who only
// wants to fix a typo'd email address never has to first fetch and
// resend every other field verbatim, and — more importantly — never
// accidentally blanks a field they didn't mean to touch by omitting it.
// Every later update-style write tool in this package follows the same
// shape (the plan's own "Shared handler pattern").
//
// A contact's own name (FirstName/LastName, or CompanyName for a
// company contact) is exactly as foreign here as it is on the read side
// (read_people.go's own doc comment on contactDescriptor/contactSummary
// — supplied by the contact itself or extracted from a document, never
// written by the account holder) and is framed the same way: NEVER as a
// structured field on updateContactOutput (which lands in
// CallToolResult.StructuredContent), only as wrapUntrustedLines'd text
// in CallToolResult.Content (ADR-0013) — exactly the same channel
// getDocumentHandler's own documentFromService (read.go) already uses
// for a document's Title, never returned as a field on
// getDocumentOutput either. Every read tool in this package keeps its
// StructuredContent 100% foreign-text-free by construction
// (mustNotLeakUntrustedLine, read_generic.go, for the generic
// descriptors; a bespoke handler like this one has no such automatic
// check, so it must hold the same line by hand — see
// updateContactResult below).
package tools

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/strausmann/go-fileee/fileee"

	"github.com/strausmann/fileee-mcp-server/internal/clientpool"
)

// updateContactEndpoint is update_contact's own wire endpoint for
// diagnostic logging (logToolEnd) — go-fileee's contactService.Update
// (contacts.go), the PUT that actually persists the caller's change.
// The Get that precedes it (client.Contacts.Get, load-before-merge) is
// deliberately not logged as its own endpoint: it is an implementation
// detail of the patch/merge shape, not a separate operation the caller
// asked for, and logToolEnd's resultCount/outcome already describe the
// call as a whole — the same "log the operation, not every backend
// round trip inside it" choice getAccountStatusEndpoint's own doc
// comment (read_account.go) makes for its own single-call case.
const updateContactEndpoint = "PUT /api/contacts/rest/:id"

// contactWriteService is what update_contact needs from
// *fileee.Client.Contacts — narrowed to the two methods this tool
// calls, the same pattern every other bespoke handler in this package
// uses to keep its fake test double small (accountStatusService,
// read_account.go; boxReadService, read_boxes.go). client.Contacts is a
// fileee.WriteService[fileee.Contact], whose method set is a superset
// of this interface's, so it satisfies contactWriteService without any
// adapter.
type contactWriteService interface {
	Get(ctx context.Context, id string) (*fileee.Contact, error)
	Update(ctx context.Context, entity *fileee.Contact) (*fileee.Contact, error)
}

// updateContactInput is update_contact's parameters — a patch/merge: ID
// selects the contact, every other field is a pointer so a caller can
// tell "change this to an empty string" (a non-nil pointer to "") apart
// from "leave this field alone" (nil) — a plain string field could never
// make that distinction, and would force every caller to resend the
// contact's entire current state just to change one field.
type updateContactInput struct {
	// ID identifies the contact to update — required, the same ID
	// list_contacts/get_contact returns.
	ID string `json:"id"`
	// FirstName, if set, replaces the contact's first name.
	FirstName *string `json:"firstName,omitempty"`
	// LastName, if set, replaces the contact's last name.
	LastName *string `json:"lastName,omitempty"`
	// CompanyName, if set, replaces the contact's company name (for a
	// company contact).
	CompanyName *string `json:"companyName,omitempty"`
	// Email, if set, replaces the contact's email address.
	Email *string `json:"email,omitempty"`
	// PhoneNumber, if set, replaces the contact's phone number.
	PhoneNumber *string `json:"phoneNumber,omitempty"`
	// FaxNumber, if set, replaces the contact's fax number.
	FaxNumber *string `json:"faxNumber,omitempty"`
	// URL, if set, replaces the contact's own URL.
	URL *string `json:"url,omitempty"`
}

// updateContactOutput is update_contact's structured result. It
// deliberately excludes every field updateContactInput can set as a
// plain field — all either the contact's own supplied data or extracted
// from a document (contactDescriptor's own doc comment, read_people.go)
// — never Fileee's own. The updated contact's own display name is
// returned too, but NEVER as a field here (that would leak foreign text
// into CallToolResult.StructuredContent) — only as clearly marked,
// untrusted text in CallToolResult.Content, exactly like every read
// tool's own UntrustedLine/wrapUntrustedLines convention (see this
// file's own doc comment, updateContactResult below, and ADR-0013).
type updateContactOutput struct {
	// ID is the updated contact's ID, unchanged by this call.
	ID string `json:"id"`
	// Modified is Fileee's own record of when the update was applied.
	Modified string `json:"modified"`
}

// applyContactPatch applies in's supplied (non-nil) fields onto cur —
// the "merge" half of update_contact's patch/merge shape. Every field
// in's zero value (nil) leaves the corresponding field on cur
// untouched; cur already carries the contact's current, unrelated
// fields from the Get that preceded this call, so a field the caller
// never mentioned survives the round trip unchanged.
func applyContactPatch(cur *fileee.Contact, in updateContactInput) {
	if in.FirstName != nil {
		cur.FirstName = *in.FirstName
	}
	if in.LastName != nil {
		cur.LastName = *in.LastName
	}
	if in.CompanyName != nil {
		cur.CompanyName = *in.CompanyName
	}
	if in.Email != nil {
		cur.Email = *in.Email
	}
	if in.PhoneNumber != nil {
		cur.PhoneNumber = *in.PhoneNumber
	}
	if in.FaxNumber != nil {
		cur.FaxNumber = *in.FaxNumber
	}
	if in.URL != nil {
		cur.URL = *in.URL
	}
}

// contactDisplayName renders c's own display name the same way
// contactDescriptor's UntrustedLine does (read_people.go) — first name
// + last name, trimmed — but falls back to the company name when that
// is empty: a company contact (ContactType company) typically has no
// FirstName/LastName at all, and an empty framed block would tell the
// caller nothing about which contact this result belongs to.
func contactDisplayName(c *fileee.Contact) string {
	name := strings.TrimSpace(c.FirstName + " " + c.LastName)
	if name == "" {
		return c.CompanyName
	}
	return name
}

// updateContactResult builds updateContactHandler's success return from
// upd, the contact fileee.Contacts.Update handed back — split out from
// updateContactFromService so wrapUntrustedLines' own error path (see
// newUntrustedBoundary's doc comment, read.go) has a single call site,
// independent of the two backend calls around it.
//
// The updated contact's own display name goes into result.Content via
// wrapUntrustedLines (read_generic.go) — the exact same call
// documentFromService (read.go) already makes for a document's Title —
// never into a field of the returned updateContactOutput: that struct
// lands in CallToolResult.StructuredContent, and every tool in this
// package keeps that channel free of foreign text (see this file's own
// package doc comment).
func updateContactResult(upd *fileee.Contact) (*mcp.CallToolResult, updateContactOutput, error) {
	result, err := wrapUntrustedLines([]string{contactDisplayName(upd)})
	if err != nil {
		return nil, updateContactOutput{}, fmt.Errorf("fileee-mcp: tools: %s: %w", ToolUpdateContact, err)
	}
	return result, updateContactOutput{ID: upd.ID, Modified: upd.Modified}, nil
}

// updateContactFromService is updateContactHandler's logic below client
// resolution — split out so a test can drive it against a
// contactWriteService fake (fakeContactWriteService, write_test.go)
// instead of a live *fileee.Client, the same pattern
// accountStatusFromService (read_account.go) and boxFromService
// (read_boxes.go) already establish.
//
// A Get failure and an Update failure are both reported as this
// function's own error, wrapped with the tool's name — never as a
// partial success: if Get succeeds but Update fails, this function does
// not claim the contact was partially changed (it wasn't; Update either
// replaces the whole entity server-side or fails outright), it reports
// the Update error and nothing else.
func updateContactFromService(ctx context.Context, service contactWriteService, in updateContactInput) (*mcp.CallToolResult, updateContactOutput, error) {
	cur, err := service.Get(ctx, in.ID)
	if err != nil {
		return nil, updateContactOutput{}, fmt.Errorf("fileee-mcp: tools: %s: %w", ToolUpdateContact, err)
	}
	applyContactPatch(cur, in)
	upd, err := service.Update(ctx, cur)
	if err != nil {
		return nil, updateContactOutput{}, fmt.Errorf("fileee-mcp: tools: %s: %w", ToolUpdateContact, err)
	}
	return updateContactResult(upd)
}

// updateContactHandler resolves update_contact. The empty-ID check runs
// before clientFor — the same order genericGetHandler already uses for
// its own required parameter (read_generic.go) — so a caller's input
// mistake is rejected without spending a login round trip on it, and so
// this path is testable without a *clientpool.Pool at all (see
// write_test.go).
func updateContactHandler(p *clientpool.Pool, logger *slog.Logger) mcp.ToolHandlerFor[updateContactInput, updateContactOutput] {
	return func(ctx context.Context, _ *mcp.CallToolRequest, in updateContactInput) (*mcp.CallToolResult, updateContactOutput, error) {
		start := time.Now()
		logToolStart(ctx, logger, ToolUpdateContact, slog.String("id", in.ID))

		if strings.TrimSpace(in.ID) == "" {
			err := fmt.Errorf("fileee-mcp: tools: %s: id must not be empty", ToolUpdateContact)
			logToolEnd(ctx, logger, ToolUpdateContact, start, "", 0, err)
			return nil, updateContactOutput{}, err
		}

		client, err := clientFor(ctx, p)
		if err != nil {
			logToolEnd(ctx, logger, ToolUpdateContact, start, "", 0, err)
			return nil, updateContactOutput{}, err
		}
		result, out, err := updateContactFromService(ctx, client.Contacts, in)
		logToolEnd(ctx, logger, ToolUpdateContact, start, updateContactEndpoint, 1, err)
		return result, out, err
	}
}

// createContactEndpoint is create_contact's own wire endpoint for
// diagnostic logging (logToolEnd) — go-fileee's contactService.Create
// (contacts.go), the single POST that persists the caller's new
// contact. Unlike update_contact there is no preceding Get to fold
// into the same logged call: a contact that does not exist yet has
// nothing to merge onto.
const createContactEndpoint = "POST /api/contacts/rest"

// contactCreateService is what create_contact needs from
// *fileee.Client.Contacts — narrowed to the single method this tool
// calls, the same "narrow the fake to what the tool actually calls"
// pattern contactWriteService above establishes for update_contact.
// client.Contacts is a fileee.WriteService[fileee.Contact], whose
// method set is a superset of this interface's, so it satisfies
// contactCreateService without any adapter.
type contactCreateService interface {
	Create(ctx context.Context, entity *fileee.Contact) (*fileee.Contact, error)
}

// createContactInput is create_contact's parameters — plain fields, not
// updateContactInput's pointer-per-field patch shape: there is no
// existing contact to merge onto, so "the caller didn't mention this
// field" and "the caller wants this field empty" are the same thing
// here (an empty string either way).
type createContactInput struct {
	// FirstName is the new contact's first name.
	FirstName string `json:"firstName"`
	// LastName is the new contact's last name.
	LastName string `json:"lastName"`
	// CompanyName is the new contact's company name (for a company
	// contact).
	CompanyName string `json:"companyName,omitempty"`
	// Email is the new contact's email address.
	Email string `json:"email,omitempty"`
	// PhoneNumber is the new contact's phone number.
	PhoneNumber string `json:"phoneNumber,omitempty"`
}

// createContactOutput is create_contact's structured result — just the
// new contact's ID. Exactly like updateContactOutput (this file's own
// package doc comment, "foreign-text invariant"), it deliberately
// carries NO name/display field: the newly created contact's own
// display name is exactly as foreign here as an updated contact's is
// (supplied by the caller through this very call, but still not
// Fileee's own account-holder data) and goes into
// CallToolResult.Content via wrapUntrusted instead, never into a field
// that would land in CallToolResult.StructuredContent (see
// createContactResult below).
type createContactOutput struct {
	// ID is the newly created contact's ID.
	ID string `json:"id"`
}

// createContactResult builds createContactHandler's success return from
// created, the contact fileee.Contacts.Create handed back — the same
// split updateContactResult establishes above, so wrapUntrusted's own
// error path has a single call site independent of the backend call
// around it.
//
// The new contact's own display name goes into result.Content via
// wrapUntrusted (read.go) — the same channel updateContactResult's own
// wrapUntrustedLines call uses for an updated contact's display name —
// never into a field of the returned createContactOutput (see this
// type's own doc comment above and write.go's package doc comment on
// the foreign-text invariant).
func createContactResult(created *fileee.Contact) (*mcp.CallToolResult, createContactOutput, error) {
	result, err := wrapUntrusted(contactDisplayName(created))
	if err != nil {
		return nil, createContactOutput{}, fmt.Errorf("fileee-mcp: tools: %s: %w", ToolCreateContact, err)
	}
	return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: result}}}, createContactOutput{ID: created.ID}, nil
}

// createContactFromService is createContactHandler's logic below client
// resolution — split out so a test can drive it against a
// contactCreateService fake (fakeContactCreateService, write_test.go)
// instead of a live *fileee.Client, the same pattern
// updateContactFromService already establishes above.
func createContactFromService(ctx context.Context, service contactCreateService, in createContactInput) (*mcp.CallToolResult, createContactOutput, error) {
	created, err := service.Create(ctx, &fileee.Contact{
		FirstName:   in.FirstName,
		LastName:    in.LastName,
		CompanyName: in.CompanyName,
		Email:       in.Email,
		PhoneNumber: in.PhoneNumber,
	})
	if err != nil {
		return nil, createContactOutput{}, fmt.Errorf("fileee-mcp: tools: %s: %w", ToolCreateContact, err)
	}
	return createContactResult(created)
}

// createContactHandler resolves create_contact. The
// nothing-to-identify-the-contact-by check runs before clientFor — the
// same order updateContactHandler already uses for its own required
// parameter (empty ID) above — so a caller's input mistake (every one
// of FirstName, LastName, and CompanyName left empty, a contact with
// literally nothing to call it) is rejected without spending a login
// round trip on it, and so this path is testable without a
// *clientpool.Pool at all (see write_test.go). A caller supplying only
// CompanyName (a company contact, no personal name) is deliberately
// still accepted here — only the all-three-empty case is rejected; the
// backend's own POST is left to reject anything more specific than
// that (createContactFromService above).
func createContactHandler(p *clientpool.Pool, logger *slog.Logger) mcp.ToolHandlerFor[createContactInput, createContactOutput] {
	return func(ctx context.Context, _ *mcp.CallToolRequest, in createContactInput) (*mcp.CallToolResult, createContactOutput, error) {
		start := time.Now()
		// No slog.String args here (unlike updateContactHandler's own
		// slog.String("id", ...)): there is no server-issued ID yet to
		// log, and FirstName/LastName/CompanyName are exactly the
		// caller-supplied, potentially foreign text createContactResult
		// already keeps out of CallToolResult.StructuredContent (see
		// this file's own package doc comment) — the same caution
		// applies to this server's own diagnostic logs.
		logToolStart(ctx, logger, ToolCreateContact)

		if strings.TrimSpace(in.FirstName) == "" && strings.TrimSpace(in.LastName) == "" && strings.TrimSpace(in.CompanyName) == "" {
			err := fmt.Errorf("fileee-mcp: tools: %s: firstName, lastName, and companyName must not all be empty", ToolCreateContact)
			logToolEnd(ctx, logger, ToolCreateContact, start, "", 0, err)
			return nil, createContactOutput{}, err
		}

		client, err := clientFor(ctx, p)
		if err != nil {
			logToolEnd(ctx, logger, ToolCreateContact, start, "", 0, err)
			return nil, createContactOutput{}, err
		}
		result, out, err := createContactFromService(ctx, client.Contacts, in)
		logToolEnd(ctx, logger, ToolCreateContact, start, createContactEndpoint, 1, err)
		return result, out, err
	}
}

// registerWriteTools mounts this server's write-class tools onto s —
// called once from RegisterAll (read.go), the same call site
// registerPeopleTools/registerBoxTools/... already use for their own
// tool families. update_contact (Task 1) is the first of eight planned
// write tools (the plan's own task list); later tasks add to this
// function, they do not replace it.
func registerWriteTools(s *mcp.Server, p *clientpool.Pool, logger *slog.Logger) {
	mcp.AddTool(s, &mcp.Tool{
		Name: ToolUpdateContact,
		Description: "Update an existing contact in the calling user's Fileee account. This is " +
			"a patch/merge: pass only the fields you want to change (firstName, lastName, " +
			"companyName, email, phoneNumber, faxNumber, url) — any field you omit keeps its " +
			"current value, it is not cleared. Returns the contact's ID, its new modification " +
			"timestamp, and its own display name as clearly marked, untrusted text, since it was " +
			"supplied by the contact itself or extracted from a document, not written by the " +
			"account holder. Use list_contacts or get_contact first to find the contact's ID. It " +
			"does not create a new contact — use it only on a contact ID that already exists.",
		Annotations: &mcp.ToolAnnotations{
			Title:           "Update contact",
			ReadOnlyHint:    false,
			DestructiveHint: boolPtr(true),
			IdempotentHint:  true,
		},
	}, updateContactHandler(p, logger))

	mcp.AddTool(s, &mcp.Tool{
		Name: ToolCreateContact,
		Description: "Create a new contact in the calling user's Fileee account. Pass firstName " +
			"and lastName for a personal contact, or companyName for a company contact — at least " +
			"one of the three is required, plus optionally email, phoneNumber. Returns the new " +
			"contact's ID and, as clearly marked, untrusted text, its display name — since it was " +
			"supplied through this very call, not written by the account holder. This always " +
			"creates a new contact; it never updates an existing one — use update_contact for that.",
		Annotations: &mcp.ToolAnnotations{
			Title:           "Create contact",
			ReadOnlyHint:    false,
			DestructiveHint: boolPtr(false),
			IdempotentHint:  false,
		},
	}, createContactHandler(p, logger))
}

// boolPtr returns a pointer to v — mcp.ToolAnnotations.DestructiveHint
// is *bool (its own zero value, nil, means "unspecified/defaults to
// true" per the MCP spec, not "false"), unlike ReadOnlyHint/
// IdempotentHint, which are plain bool. A future second annotation
// needing an explicit *bool value (e.g. OpenWorldHint) reuses this
// helper rather than growing its own.
func boolPtr(v bool) *bool { return &v }
