// response_body_safety_test.go is the structural guardrail described in
// .claude/rules/api-response-dto-boundary.md (homelab-management repo):
// every MCP tool response body in this server MUST be a dedicated DTO,
// never a go-fileee domain/library type that carries its own MarshalJSON —
// such a type serializes itself by its own logic, ignoring the DTO's
// struct tags, and regularly hands back more than intended.
//
// # Why this test exists (audited, not hypothetical here)
//
// strausmann/fileee-server hit exactly this failure mode on PR #38 (Issue
// #37): documentListBody.Items was []fileee.Document, and fileee.Document's
// own MarshalJSON (go-fileee, fileee/types.go) unconditionally reconstructs
// the full Fileee wire envelope {"attributes":{"data":{...}}} — including
// RawExtra, Fileee's raw, unmodelled extraction payload — for EVERY marshal,
// as a slice element, a struct field, or a bare value, REGARDLESS of any
// `json:"-"` tag on the field that holds it (a custom MarshalJSON method
// bypasses struct-tag-driven marshaling entirely; that tag only controls
// encoding/json's DEFAULT field-by-field marshaling). Every document
// returned by GET /v1/documents therefore leaked full financial PII (IBAN,
// amounts, customer number, invoice number/date, sender, ...), completely
// ungated. The same audit found an identical, independent leak on
// GET /v1/companies (fileee.Company/CompanyAttributes carry the same
// pattern: IBANs, VAT IDs, emails, phone numbers, tax IDs).
//
// This server (fileee-mcp-server) was built after that incident and, by
// design, never returns a go-fileee type directly: every tool projects onto
// a hand-written summary/detail struct (documentSummary, companySummary,
// boxDetail, ...) built field-by-field from primitive Go types, with
// UntrustedLine/PoisonProbe (read_generic.go, read_sync.go) as a second,
// independent check that foreign free text specifically never leaks into a
// structured field. An audit of every RegisterAll tool's response body
// (Aufgabe: fileee-mcp-server PII-Leak-Audit, homelab-management repo,
// 2026-08-14) confirmed this by hand across all tools registered at the
// time (35) — found no go-fileee Marshaler type reachable from any
// response body, directly or transitively. registeredReadTools() mounts
// 40 tools today (32 fileee-backed read tools, 4 operational tools:
// get_runtime_stats, get_tool_manifest, self_check, whoami, plus four
// write-class tools so far: update_contact and create_contact (Task
// 1/2, write.go), create_reminder and update_reminder (Task 3,
// write_people.go) — the count grew after that audit,
// registeredResponseBodyTypes below reflects the current set. The same
// guardrail applies to write tools' response bodies unchanged:
// updateContactOutput/createContactOutput/reminderOutput are exactly as much
// hand-written, all-primitive DTOs as any read tool's output.
//
// A hand audit is a snapshot, not a standing guarantee. This test is the
// mechanical guardrail that keeps that finding true going forward: it
// walks the full type graph of every currently-registered response body
// (struct fields, slice/array/map elements, pointer indirection) and fails
// the moment ANY type belonging to go-fileee's `fileee` package that
// implements json.Marshaler shows up ANYWHERE in that graph — whether or
// not it happens to carry PII today. Ported from fileee-server's own
// TestNoFileeeMarshalerTypeInAnyResponseBody (cmd/fileee-server/
// response_body_safety_test.go, same PR #38) rather than reinvented, so
// both sibling servers share one proven mechanism against the same class
// of bug in go-fileee's own types.
package tools

import (
	"encoding/json"
	"reflect"
	"sort"
	"strings"
	"testing"

	"github.com/strausmann/go-fileee/fileee"
)

// registeredResponseBodyTypes is the complete list of every Go type used as
// a CallToolResult.StructuredContent output across this server's read
// tools (RegisterAll, read.go) — the S/Output type parameter for the
// generic list/get/sync tools (read_generic.go/read_sync.go), and every
// bespoke handler's own output struct. Binary tools (get_document_pdf,
// get_page_image) return their payload as mcp.EmbeddedResource/
// mcp.ImageContent, not a go-fileee-typed StructuredContent value, but
// their own small metadata structs (getDocumentPDFOutput/
// getPageImageOutput — sizeBytes only) are still listed for completeness.
//
// MUST be updated whenever a new tool with a new output type is
// registered — see TestNoFileeeMarshalerTypeInAnyResponseBody below, which
// walks exactly this list. One entry per registered tool name, in the same
// order RegisterAll mounts the corresponding tools, so the two lists can be
// compared by eye.
var registeredResponseBodyTypes = []reflect.Type{
	reflect.TypeOf(listDocumentsOutput{}),                              // list_documents
	reflect.TypeOf(searchDocumentsOutput{}),                            // search_documents
	reflect.TypeOf(genericSyncOutput[syncTagSummary]{}),                // sync_tags
	reflect.TypeOf(genericSyncOutput[syncCompanySummary]{}),            // sync_companies
	reflect.TypeOf(genericSyncOutput[syncDocumentTypeSummary]{}),       // sync_document_types
	reflect.TypeOf(genericSyncOutput[syncDocumentTypeSchemeSummary]{}), // sync_document_type_schemes
	reflect.TypeOf(genericSyncOutput[syncContactSummary]{}),            // sync_contacts
	reflect.TypeOf(genericSyncOutput[syncReminderSummary]{}),           // sync_reminders
	reflect.TypeOf(genericSyncOutput[syncConversationSummary]{}),       // sync_conversations
	reflect.TypeOf(genericListOutput[referenceTagSummary]{}),           // list_tags
	reflect.TypeOf(genericGetOutput[referenceTagSummary]{}),            // get_tag
	reflect.TypeOf(genericListOutput[companySummary]{}),                // list_companies
	reflect.TypeOf(genericGetOutput[companySummary]{}),                 // get_company
	reflect.TypeOf(genericListOutput[documentTypeSummary]{}),           // list_document_types
	reflect.TypeOf(genericGetOutput[documentTypeSummary]{}),            // get_document_type
	reflect.TypeOf(genericListOutput[documentTypeSchemeSummary]{}),     // list_document_type_schemes
	reflect.TypeOf(genericGetOutput[documentTypeSchemeSummary]{}),      // get_document_type_scheme
	reflect.TypeOf(genericListOutput[contactSummary]{}),                // list_contacts
	reflect.TypeOf(genericGetOutput[contactSummary]{}),                 // get_contact
	reflect.TypeOf(genericListOutput[reminderSummary]{}),               // list_reminders
	reflect.TypeOf(genericGetOutput[reminderSummary]{}),                // get_reminder
	reflect.TypeOf(genericListOutput[conversationSummary]{}),           // list_conversations
	reflect.TypeOf(genericGetOutput[conversationSummary]{}),            // get_conversation
	reflect.TypeOf(getDocumentOutput{}),                                // get_document
	reflect.TypeOf(genericSyncOutput[documentSummary]{}),               // sync_documents
	reflect.TypeOf(listDocumentConversationsOutput{}),                  // list_document_conversations
	reflect.TypeOf(listBoxesOutput{}),                                  // list_boxes
	reflect.TypeOf(boxDetail{}),                                        // get_box
	reflect.TypeOf(getDocumentPDFOutput{}),                             // get_document_pdf
	reflect.TypeOf(getPageImageOutput{}),                               // get_page_image
	reflect.TypeOf(getPageOCROutput{}),                                 // get_page_ocr
	reflect.TypeOf(getAccountStatusOutput{}),                           // get_account_status
	reflect.TypeOf(getRuntimeStatsOutput{}),                            // get_runtime_stats
	reflect.TypeOf(getToolManifestOutput{}),                            // get_tool_manifest
	reflect.TypeOf(getSelfCheckOutput{}),                               // self_check
	reflect.TypeOf(whoamiOutput{}),                                     // whoami
	reflect.TypeOf(updateContactOutput{}),                              // update_contact
	reflect.TypeOf(createContactOutput{}),                              // create_contact
	reflect.TypeOf(reminderOutput{}),                                   // create_reminder
	reflect.TypeOf(reminderOutput{}),                                   // update_reminder
}

// jsonMarshalerType is the reflect.Type of the standard library's
// json.Marshaler interface — implemented by exactly the go-fileee types
// this test hunts for (Document, Company, DocumentAttributes,
// CompanyAttributes, the unexported flexInt64 — see fileee/types.go).
var jsonMarshalerType = reflect.TypeOf((*json.Marshaler)(nil)).Elem()

// fileeePackagePath is go-fileee's fully-qualified import path, used to
// scope the search to that package specifically — this test is not
// concerned with stdlib types (e.g. json.RawMessage, which itself
// implements json.Marshaler) or any other dependency's Marshaler types.
const fileeePackagePath = "github.com/strausmann/go-fileee/fileee"

// typeImplementsJSONMarshaler reports whether t (or a pointer to t)
// implements json.Marshaler — covers both value-receiver methods
// (fileee.Document, fileee.Company, ...) and, defensively, any future
// pointer-receiver MarshalJSON implementation.
func typeImplementsJSONMarshaler(t reflect.Type) bool {
	return t.Implements(jsonMarshalerType) || reflect.PointerTo(t).Implements(jsonMarshalerType)
}

// findFileeeMarshalerTypes walks t — following struct fields (skipping
// unexported fields and fields tagged `json:"-"`, since encoding/json never
// marshals either), slice/array/map element types, and pointer
// indirection — and records every DISTINCT type belonging to go-fileee's
// `fileee` package that implements json.Marshaler into found. visited
// prevents revisiting the same type twice and guards against infinite
// recursion on self-referential types.
//
// Skipping `json:"-"`-tagged fields is deliberate and important: such a
// field's value is NEVER passed to encoding/json's marshaling machinery for
// that field, so whatever type it holds can never leak through THIS field,
// regardless of that type's own Marshaler. This is exactly the property a
// custom MarshalJSON on the OUTER struct can violate — which is the whole
// point of this test: fileee.Document/Company have `json:"-"` on their own
// Attributes field yet leak it anyway, because their OWN MarshalJSON
// ignores that tag and reconstructs the field manually. That's why this
// walker flags the OUTER type itself (Document/Company) the moment it's
// encountered — not by inspecting its json:"-" field, but by checking the
// type itself against jsonMarshalerType before descending into its fields
// at all.
func findFileeeMarshalerTypes(t reflect.Type, visited map[reflect.Type]bool, found map[reflect.Type]bool) {
	for t.Kind() == reflect.Pointer {
		t = t.Elem()
	}
	if visited[t] {
		return
	}
	visited[t] = true

	if t.PkgPath() == fileeePackagePath && typeImplementsJSONMarshaler(t) {
		found[t] = true
		// Deliberately continue descending below even though t is already
		// flagged: a Marshaler type could itself embed ANOTHER go-fileee
		// Marshaler type we'd otherwise miss, and finding every offender in
		// one run (instead of one-at-a-time across repeated test runs) is
		// strictly more useful for whoever has to fix it.
	}

	switch t.Kind() {
	case reflect.Struct:
		for i := 0; i < t.NumField(); i++ {
			f := t.Field(i)
			if !f.IsExported() {
				continue
			}
			if tag, ok := f.Tag.Lookup("json"); ok {
				name, _, _ := strings.Cut(tag, ",")
				if name == "-" {
					continue
				}
			}
			findFileeeMarshalerTypes(f.Type, visited, found)
		}
	case reflect.Slice, reflect.Array:
		findFileeeMarshalerTypes(t.Elem(), visited, found)
	case reflect.Map:
		findFileeeMarshalerTypes(t.Elem(), visited, found)
	}
}

// TestNoFileeeMarshalerTypeInAnyResponseBody is the structural guardrail
// described in the doc comment on registeredResponseBodyTypes above: it
// fails the moment ANY currently-registered response body type contains —
// directly or transitively — a go-fileee type with its own MarshalJSON.
// Run as part of the normal `go test ./...` suite, so it fails at
// development time, not first in review.
func TestNoFileeeMarshalerTypeInAnyResponseBody(t *testing.T) {
	for _, typ := range registeredResponseBodyTypes {
		t.Run(typ.String(), func(t *testing.T) {
			visited := map[reflect.Type]bool{}
			found := map[reflect.Type]bool{}
			findFileeeMarshalerTypes(typ, visited, found)
			if len(found) == 0 {
				return
			}
			names := make([]string, 0, len(found))
			for ft := range found {
				names = append(names, ft.String())
			}
			sort.Strings(names)
			t.Errorf("response body type %s contains go-fileee type(s) with their own MarshalJSON: %v — "+
				"these bypass json:\"-\" tags and leak EVERYTHING on marshal (see the incident documented "+
				"on registeredResponseBodyTypes, strausmann/fileee-server PR #38). Project onto a dedicated "+
				"summary/detail struct built field-by-field from primitive types instead — see "+
				"documentSummary/documentDetail, companySummary, boxSummary/boxDetail for the established "+
				"pattern in this package.", typ, names)
		})
	}
}

// TestFindFileeeMarshalerTypesDetectsKnownOffenders is a meta-test for the
// guardrail mechanism itself: it proves findFileeeMarshalerTypes actually
// DETECTS the exact shapes that caused the fileee-server PR #38 incident,
// so a change that accidentally weakens the walker (e.g. an overly broad
// json:"-" skip, or a Kind case that stops recursing) fails loudly instead
// of leaving TestNoFileeeMarshalerTypeInAnyResponseBody vacuously green.
// None of this server's OWN response bodies are shaped like these — that's
// the point; these are synthetic offenders, never wired into any real tool.
func TestFindFileeeMarshalerTypesDetectsKnownOffenders(t *testing.T) {
	cases := []struct {
		name string
		typ  reflect.Type
		want string // reflect.Type.String() of the expected offender
	}{
		{"direct Document", reflect.TypeOf(fileee.Document{}), "fileee.Document"},
		{"slice of Document (the PR #38 shape)", reflect.TypeOf([]fileee.Document{}), "fileee.Document"},
		{"direct Company", reflect.TypeOf(fileee.Company{}), "fileee.Company"},
		{"slice of Company (the list-companies shape)", reflect.TypeOf([]fileee.Company{}), "fileee.Company"},
		{"struct field holding a Document", reflect.TypeOf(struct{ Doc fileee.Document }{}), "fileee.Document"},
		{"pointer to Document", reflect.TypeOf(&fileee.Document{}), "fileee.Document"},
		{"map value holding a Company", reflect.TypeOf(map[string]fileee.Company{}), "fileee.Company"},
		{"Page (flexInt64 fields — no PII, still flagged)", reflect.TypeOf(fileee.Page{}), "fileee.flexInt64"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			visited := map[reflect.Type]bool{}
			found := map[reflect.Type]bool{}
			findFileeeMarshalerTypes(tc.typ, visited, found)
			if len(found) == 0 {
				t.Fatalf("expected findFileeeMarshalerTypes to flag %s, found nothing — the guardrail mechanism itself is broken", tc.want)
			}
			hit := false
			for ft := range found {
				if ft.String() == tc.want {
					hit = true
				}
			}
			if !hit {
				got := make([]string, 0, len(found))
				for ft := range found {
					got = append(got, ft.String())
				}
				t.Fatalf("expected %s among findings, got %v", tc.want, got)
			}
		})
	}
}

// TestFindFileeeMarshalerTypesJSONDashTagStopsDescentOnly proves the
// `json:"-"` skip in findFileeeMarshalerTypes only stops DESCENDING INTO
// that field — it must NOT suppress flagging the outer type itself if the
// outer type has its own MarshalJSON (exactly fileee.Document's shape:
// Attributes DocumentAttributes `json:"-"`, but Document.MarshalJSON
// ignores that tag and reconstructs it anyway — go-fileee/fileee/types.go).
func TestFindFileeeMarshalerTypesJSONDashTagStopsDescentOnly(t *testing.T) {
	visited := map[reflect.Type]bool{}
	found := map[reflect.Type]bool{}
	findFileeeMarshalerTypes(reflect.TypeOf(fileee.Document{}), visited, found)

	if !found[reflect.TypeOf(fileee.Document{})] {
		t.Fatal("fileee.Document itself must be flagged regardless of its json:\"-\"-tagged Attributes field")
	}
}

// TestNoFileeeMarshalerTypeKnownSafeTypesPassCleanly is the positive
// counterpart: proves every one of this server's actual, currently
// registered response body types produces ZERO findings — the audited
// state TestNoFileeeMarshalerTypeInAnyResponseBody itself already enforces,
// re-asserted here explicitly plus a handful of raw go-fileee types this
// server's Summarize functions read from but never return wholesale, as a
// sanity check that the walker is not over-broad and does not false-
// positive on ordinary go-fileee types that carry no custom MarshalJSON.
func TestNoFileeeMarshalerTypeKnownSafeTypesPassCleanly(t *testing.T) {
	safe := make([]reflect.Type, 0, len(registeredResponseBodyTypes)+8)
	safe = append(safe, registeredResponseBodyTypes...)
	safe = append(safe,
		reflect.TypeOf(fileee.Tag{}),
		reflect.TypeOf(fileee.Contact{}),
		reflect.TypeOf(fileee.Box{}),
		reflect.TypeOf(fileee.Reminder{}),
		reflect.TypeOf(fileee.Conversation{}),
		reflect.TypeOf(fileee.DocumentType{}),
		reflect.TypeOf(fileee.DocumentTypeScheme{}),
		reflect.TypeOf(fileee.AccountStatus{}),
		reflect.TypeOf([]fileee.OCRToken{}),
	)
	for _, typ := range safe {
		t.Run(typ.String(), func(t *testing.T) {
			visited := map[reflect.Type]bool{}
			found := map[reflect.Type]bool{}
			findFileeeMarshalerTypes(typ, visited, found)
			if len(found) != 0 {
				names := make([]string, 0, len(found))
				for ft := range found {
					names = append(names, ft.String())
				}
				t.Fatalf("expected no findings for %s, got %v", typ, names)
			}
		})
	}
}

// TestRegisteredResponseBodyTypesCoversEveryReadTool cross-checks
// registeredResponseBodyTypes' length against the live mounted tool set
// (registeredReadTools(), names.go) — the same "two independent lists must
// agree" pattern names.go's own former readToolNames/registeredReadTools()
// Gegenprobe used before names.go dropped that hand-maintained kind map
// (Task 3, foundation refactor). Every one of this server's read tools —
// including the meta tools whoami/self_check/get_runtime_stats/
// get_tool_manifest — returns a dedicated response body type, so this
// count basis stays exact rather than needing an exclusion list; if a
// future tool's response body is genuinely exempt (e.g. a binary-only
// tool with no StructuredContent at all), narrow this comparison then and
// document why here instead of silently letting the counts drift apart.
// A mismatch means either this file's list fell behind a newly registered
// tool (the guardrail above then simply never runs against it) or a tool
// was mounted/removed that this file never learned about — either way, a
// silent gap in coverage rather than a loud one.
func TestRegisteredResponseBodyTypesCoversEveryReadTool(t *testing.T) {
	if got, want := len(registeredResponseBodyTypes), len(registeredReadTools()); got != want {
		t.Errorf("registeredResponseBodyTypes has %d entries, %d tools are mounted — "+
			"a newly registered tool's response body type is missing from the guardrail list (or vice versa)", got, want)
	}
}
