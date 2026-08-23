// read_generic.go: seven Fileee services share fileee.ReadService[T]
// (Query/Diff/Get, see go-fileee's own doc comment on that interface).
// Instead of fourteen near-identical handlers, one generic helper mounts
// them all — the differences between services live exclusively in a
// descriptor, never in copy-pasted handler bodies.
package tools

import (
	"context"
	"fmt"
	"log/slog"
	"reflect"
	"strings"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/strausmann/go-fileee/fileee"

	"github.com/strausmann/fileee-mcp-server/internal/clientpool"
)

// readServiceDescriptor describes how one ReadService[T] appears as a
// list/get tool pair.
//
// S is Summarize's return type — a per-service, JSON-tagged struct (see
// Aufgabe 3/4's own descriptors), deliberately NOT map[string]any: a map
// return value would still work, but every caller of a generated tool
// (including the model reading its schema) would see an untyped object
// with no field list, and a typo in a map key would compile without
// complaint and simply vanish from the output. A concrete struct type
// parameter gives the SDK's schema derivation the same real field names
// and types documentSummary already gives list_documents (read.go), for
// every service this helper mounts, at the same one-descriptor cost.
//
// Summarize must return only fields Fileee itself assigns — never a value
// that could contain third-party free text (a document's title, a
// contact's note). UntrustedLine is the deliberate escape hatch for that
// foreign content: it never appears in Summarize's return value, only in
// the accompanying text content, framed by wrapUntrustedLines (ADR-0013;
// see also listDocumentsHandler's own doc comment for why the same rule
// applies there).
//
// Nothing about Summarize's and UntrustedLine's SIGNATURES enforces that
// separation — they are two independent functions a descriptor author
// writes by hand, and nothing stops one from accidentally also returning
// what the other frames (the entity's foreign text would then reach the
// model twice: once framed as untrusted, once again, unframed, inside
// StructuredContent). Two things enforce it in practice instead:
//
//  1. PoisonProbe (below), checked once by registerReadService itself, at
//     registration — not per request, and never against real Fileee data
//     (see mustNotLeakUntrustedLine's own doc comment for the reasoning
//     behind that split).
//  2. Every descriptor's own registration test (the pattern
//     TestRegisterReadServiceMeldetListeUndDetailAn already establishes)
//     exercises registerReadService, and therefore exercises 1 for free —
//     there is no separate assertion to remember.
//
// Neither of these — no mechanism this package can build — can decide
// WHETHER a given T carries foreign text at all; that is domain knowledge
// (does the account holder or a third party choose this value?), not
// something derivable from a type or a running value. That classification
// is made once, by hand, at each descriptor's UntrustedLine field itself —
// see that field's own doc comment for the two ways to make it, and for
// the boundary that stays a matter of author judgement no matter which
// (Aufgabe 3's own tag/company/document-type/scheme descriptors are the
// concrete case: their names are chosen by the account holder, per the
// concept document, not by a third party — so they leave UntrustedLine
// nil, deliberately, not as an oversight).
type readServiceDescriptor[T any, S any] struct {
	// ListName and GetName are the registered tool names.
	ListName string
	GetName  string
	// ListTitle and GetTitle are each tool's Annotations.Title — the
	// short, human-facing name a client shows or gates on (Task 4, the
	// MCP connector standard), independent of the Description text below.
	ListTitle string
	GetTitle  string
	// ListDescription and GetDescription are each tool's Description, held
	// to the same four-part standard (what, returns, when, does-not) and
	// minimum length descriptions_test.go checks for RegisterAll's own
	// tools — checked there again once Aufgabe 3/4 wire a service through
	// this helper into RegisterAll.
	ListDescription string
	GetDescription  string
	// Service resolves this descriptor's ReadService[T] from an
	// already-authenticated client — a field access on *fileee.Client
	// (e.g. func(c *fileee.Client) fileee.ReadService[fileee.Tag] { return
	// c.Tags }) in production, and a fake ignoring its argument entirely
	// in a test that only cares about a service's Query/Get behaviour (see
	// read_generic_test.go).
	Service func(*fileee.Client) fileee.ReadService[T]
	// Summarize renders one entity's Fileee-owned fields as S — never
	// foreign free text, see this type's own doc comment.
	Summarize func(*T) S
	// UntrustedLine renders one entity's foreign free text as a single
	// line for the untrusted block wrapUntrustedLines builds. There are
	// two distinct ways for it to say "nothing to frame here", and they
	// mean different things:
	//
	//   - Set, but returning "" for a PARTICULAR entity: this entity of an
	//     otherwise foreign-text-carrying type just happens to have none
	//     right now (list_documents' own title field, empty for a
	//     document with no title, is this case — see
	//     listDocumentsHandler's own doc comment). mustNotLeakUntrustedLine
	//     still runs and still requires PoisonProbe.
	//   - Left nil: this TYPE never carries foreign text at all — every
	//     field Summarize could possibly expose is chosen by the account
	//     holder, not by a third party (Aufgabe 3's tag/company/
	//     document-type/scheme descriptors are the concrete case: their
	//     names are the account holder's own, per the concept document).
	//     mustNotLeakUntrustedLine skips its check entirely then — there
	//     is nothing to verify a leak against — and PoisonProbe must be
	//     nil too (see that field's own doc comment).
	//
	// This second case is a judgement call this package cannot verify —
	// see readServiceDescriptor's own doc comment for why no mechanism
	// here can decide it for you.
	UntrustedLine func(*T) string
	// PoisonProbe constructs a T whose foreign-text source — whichever
	// field(s) UntrustedLine actually reads — is set to the marker it is
	// given. registerReadService calls it once, at registration, to prove
	// Summarize never reproduces what UntrustedLine frames (see
	// mustNotLeakUntrustedLine).
	//
	// Required whenever UntrustedLine is set (a nil PoisonProbe then
	// panics at registration rather than silently skipping the check —
	// see UntrustedLine's own doc comment for why the two fields track
	// each other, and names.go's reasoning for the same "silent gap must
	// be loud" principle applied to readToolNames). Must itself be nil
	// when UntrustedLine is nil: a set PoisonProbe with a nil
	// UntrustedLine panics too — that combination reads as "I started
	// wiring the foreign-text check and stopped partway", the one half of
	// this mistake a mechanism CAN catch (see UntrustedLine's own doc
	// comment on the half it cannot).
	//
	// Example, for a descriptor whose UntrustedLine composes "Max " +
	// contact.LastName:
	//
	//	PoisonProbe: func(marker string) *fileee.Contact {
	//		return &fileee.Contact{LastName: marker}
	//	}
	PoisonProbe func(marker string) *T
}

// genericListInput are every generic list tool's parameters — the same
// shape listDocumentsInput already uses (read.go), so a caller learns one
// paging convention instead of one per service.
type genericListInput struct {
	// Limit caps how many entries this call returns. 0 (the default) uses
	// defaultLimit; anything above maxLimit is capped, not refused.
	Limit int `json:"limit,omitempty" jsonschema:"maximum number of entries to return (default 20, max 100)"`
	// Start is the zero-based offset into the caller's full entry list,
	// for paging past Limit.
	Start int `json:"start,omitempty" jsonschema:"zero-based offset into the result list, for paging"`
}

// genericListOutput is a generic list tool's structured result
// (CallToolResult.StructuredContent). S is the same summary type its
// readServiceDescriptor uses — see that type's own doc comment on why.
type genericListOutput[S any] struct {
	// Entries are the returned entries' structured summaries, in the order
	// Fileee returned them.
	Entries []S `json:"entries"`
	// TotalRows is the caller's total entry count, independent of how many
	// this one call actually returned.
	TotalRows int `json:"totalRows"`
}

// genericGetInput are every generic get tool's parameters.
type genericGetInput struct {
	// ID identifies the entry to load. Required; an empty ID is rejected
	// before any network access (see genericGetHandler).
	ID string `json:"id" jsonschema:"identifier of the entry to load"`
}

// genericGetOutput is a generic get tool's structured result
// (CallToolResult.StructuredContent).
type genericGetOutput[S any] struct {
	// Entry is the loaded entry's structured summary.
	Entry S `json:"entry"`
}

// registerReadService mounts d.ListName and d.GetName onto s, resolving
// their Fileee connection through p on every call (see clientFor) — the
// same pattern RegisterAll uses for list_documents/search_documents,
// generalized over any fileee.ReadService[T].
//
// logger receives d.ListName's and d.GetName's diagnostic log exactly the
// way listDocumentsHandler/searchDocumentsHandler already do (read.go,
// logToolStart/logToolEnd) — passed straight through to
// genericListHandler/genericGetHandler, never rebuilt here. Aufgabe 2c
// closed the gap where this parameter did not exist yet (#45); see this
// function's own callers for why threading it through matters (RegisterAll's
// own doc comment, read.go).
//
// It panics — like mcp.AddTool itself already does for a malformed tool —
// if d fails mustNotLeakUntrustedLine's check. That check runs once, here,
// not per request: see that function's own doc comment for why a
// per-request version would be worse than the bug it replaces.
func registerReadService[T any, S any](s *mcp.Server, p *clientpool.Pool, logger *slog.Logger, d readServiceDescriptor[T, S]) {
	mustNotLeakUntrustedLine(d)

	mcp.AddTool(s, &mcp.Tool{
		Name:        d.ListName,
		Description: d.ListDescription,
		Annotations: &mcp.ToolAnnotations{ReadOnlyHint: true, Title: d.ListTitle},
	}, genericListHandler(p, logger, d))

	mcp.AddTool(s, &mcp.Tool{
		Name:        d.GetName,
		Description: d.GetDescription,
		Annotations: &mcp.ToolAnnotations{ReadOnlyHint: true, Title: d.GetTitle},
	}, genericGetHandler(p, logger, d))
}

// mustNotLeakUntrustedLine proves, once, that d.Summarize never reproduces
// what d.UntrustedLine frames — using a synthetic marker constructed by
// d.PoisonProbe, never real Fileee data.
//
// Why a marker instead of comparing UntrustedLine's actual output against
// Summarize's actual output directly: UntrustedLine's text can be COMPOSED
// from several fields ("Max " + contact.LastName is the case that broke an
// earlier, exact-equality version of this check — Summarize returning only
// LastName is a real, partial leak that never equals the whole line). A
// fixed, unpredictable marker embedded in the one field UntrustedLine
// actually reads sidesteps that: it must appear, whole, inside
// UntrustedLine's output BY CONSTRUCTION (checked below, and PoisonProbe
// setting the wrong field is itself a bug this catches), and it can then
// appear ANYWHERE inside Summarize's rendered fields — the whole line, a
// substring of it, doesn't matter — while still being unable to
// coincidentally match a real, unrelated field: reused from newUntrustedBoundary
// (read.go) specifically for its "nobody could have predicted this ahead
// of the call that produces it" property, the same property that makes it
// safe to test with strings.Contains here without the false-positive risk
// a real word ("Rechnung" turning up in two unrelated fields, a case this
// package hit for real during review) would carry.
//
// Why once, at registration, not per request against real entities: a
// leaking descriptor is a property of its CODE (does Summarize reproduce
// UntrustedLine's source or not) — true for every entity of that type or
// none, never data-dependent. A per-request version would need to compare
// against the CALL's actual UntrustedLine output rather than a marker,
// reintroducing the exact substring-vs-exact tradeoff this function's
// marker design exists to avoid, and would turn a coincidental match in a
// real caller's real documents into a rejected, working tool call —
// worse than the silent duplication this exists to catch. This runs once
// per process (registration time), so its cost is irrelevant regardless.
//
// d.UntrustedLine == nil is not a degenerate input to reject — it is the
// descriptor's own declaration that this T never carries foreign text at
// all (see that field's own doc comment), and this function has nothing
// to check a leak against in that case: it returns immediately, requiring
// PoisonProbe to be nil too (a set one with a nil UntrustedLine panics —
// see PoisonProbe's own doc comment on why that combination is worth
// catching even though the reverse mistake, both left nil for a type that
// actually does carry foreign text, is not something any mechanism here
// can detect).
func mustNotLeakUntrustedLine[T any, S any](d readServiceDescriptor[T, S]) {
	mustNotLeakUntrustedText("readServiceDescriptor", d.ListName+"/"+d.GetName, d.UntrustedLine, d.PoisonProbe, d.Summarize)
}

// mustNotLeakUntrustedText is mustNotLeakUntrustedLine's descriptor-agnostic
// core — extracted (Aufgabe 2b, Antrag #46) so syncDescriptor's own leak
// check (read_sync.go) can share it instead of re-implementing the exact
// same marker/panic logic against a differently named struct. Go's generics
// have no structural typing across distinct named struct types, so sharing
// the CHECK means sharing a function of the four fields it actually reads,
// not the two callers' descriptor types themselves.
//
// descriptorType and label together identify the caller in every panic
// message below: descriptorType names the Go struct whose field is at
// fault ("readServiceDescriptor" or "syncDescriptor", see each caller's own
// call site), label identifies which instance of it ("list_tags/get_tag"
// or "sync_tags"). The first extraction of this function (Aufgabe 2b's
// first round) dropped descriptorType entirely and reasoned the loss away
// as "cosmetic" — true only as long as exactly one descriptor type
// existed. A whole-branch review caught it once a second type
// (syncDescriptor) existed and the same wording no longer said which type
// a given panic came from; see this repo's PR #46 review thread and
// read_generic_test.go/read_sync_test.go's own message-content tests
// (TestMustNotLeakUntrustedTextMeldetDenDeskriptorTyp and its
// read_sync_test.go counterpart) for the regression test that would have
// caught it.
//
// See mustNotLeakUntrustedLine's own (now slightly stale in wording, still
// accurate in substance) doc comment above for the full reasoning behind the
// marker design and the once-at-registration timing; this function is that
// reasoning's implementation.
func mustNotLeakUntrustedText[T any, S any](descriptorType, label string, untrustedLine func(*T) string, poisonProbe func(marker string) *T, summarize func(*T) S) {
	if untrustedLine == nil {
		if poisonProbe != nil {
			panic(fmt.Sprintf(
				"fileee-mcp: tools: %s: %s.PoisonProbe is set but UntrustedLine is nil — "+
					"either wire UntrustedLine too, or remove PoisonProbe if this type truly carries no foreign text",
				label, descriptorType))
		}
		return
	}

	if poisonProbe == nil {
		panic(fmt.Sprintf(
			"fileee-mcp: tools: %s: %s.PoisonProbe is required whenever UntrustedLine is set — "+
				"without it, whether Summarize reproduces UntrustedLine's foreign text was never checked for this descriptor",
			label, descriptorType))
	}

	marker, err := newUntrustedBoundary()
	if err != nil {
		panic(fmt.Sprintf("fileee-mcp: tools: %s: %s: generate poison marker: %v", label, descriptorType, err))
	}

	entity := poisonProbe(marker)
	line := untrustedLine(entity)
	if !strings.Contains(line, marker) {
		panic(fmt.Sprintf(
			"fileee-mcp: tools: %s: %s.PoisonProbe does not set the field UntrustedLine actually reads — "+
				"UntrustedLine's rendered text %q does not contain the probe's marker",
			label, descriptorType, line))
	}

	for _, v := range summaryFieldValues(summarize(entity)) {
		if strings.Contains(v, marker) {
			panic(fmt.Sprintf(
				"fileee-mcp: tools: %s: %s.Summarize reproduces UntrustedLine's foreign text — "+
					"a summary field's rendered value contains the probe's marker",
				label, descriptorType))
		}
	}
}

// summaryFieldValues renders summary's exported field values as strings —
// one level deep, without recursing into nested structs/slices: every
// summary struct in this package so far (documentSummary in read.go
// included) is flat. If summary itself is not a struct (or a pointer to
// one), its single value is rendered as one candidate. Only used by
// mustNotLeakUntrustedLine.
func summaryFieldValues(summary any) []string {
	rv := reflect.ValueOf(summary)
	for rv.Kind() == reflect.Pointer {
		if rv.IsNil() {
			return nil
		}
		rv = rv.Elem()
	}
	if rv.Kind() != reflect.Struct {
		return []string{fmt.Sprint(summary)}
	}
	values := make([]string, 0, rv.NumField())
	for i := 0; i < rv.NumField(); i++ {
		if !rv.Type().Field(i).IsExported() {
			continue
		}
		values = append(values, fmt.Sprint(rv.Field(i).Interface()))
	}
	return values
}

// genericListHandler resolves d.ListName. It splits client resolution
// (clientFor, which needs a real, gangway-verified caller identity — see
// that function's own doc comment) from the actual Query/summarize/frame
// logic (listFromService), so the latter stays testable against a fake
// fileee.ReadService[T] without needing a live Fileee login (see
// read_generic_test.go).
//
// It logs through logger exactly the way listDocumentsHandler does
// (read.go): arguments once via logToolStart (debug only), outcome and
// duration once via logToolEnd, on every path including a clientFor
// failure. The Fileee wire endpoint a given service's Query call actually
// reaches is, unlike list_documents' own listDocumentsEndpoint constant
// (read.go), not something this generic layer knows per descriptor — d
// carries no such field, and go-fileee's fileee.ReadService[T] does not
// expose it either — so logToolEnd's endpoint argument is passed as "",
// deliberately, rather than a guessed or hardcoded value.
func genericListHandler[T any, S any](p *clientpool.Pool, logger *slog.Logger, d readServiceDescriptor[T, S]) mcp.ToolHandlerFor[genericListInput, genericListOutput[S]] {
	return func(ctx context.Context, _ *mcp.CallToolRequest, in genericListInput) (*mcp.CallToolResult, genericListOutput[S], error) {
		start := time.Now()
		logToolStart(ctx, logger, d.ListName, slog.Int("start", in.Start), slog.Int("limit", in.Limit))

		client, err := clientFor(ctx, p)
		if err != nil {
			logToolEnd(ctx, logger, d.ListName, start, "", 0, err)
			return nil, genericListOutput[S]{}, err
		}
		result, out, err := listFromService(ctx, d, d.Service(client), in)
		logToolEnd(ctx, logger, d.ListName, start, "", len(out.Entries), err)
		return result, out, err
	}
}

// listFromService is genericListHandler's logic below client resolution:
// query service, summarize every row into S, collect the foreign lines
// UntrustedLine hands back, and frame them (wrapUntrustedLines) — kept
// separate from genericListHandler so a test can drive it directly against
// a fake service instead of a live *fileee.Client.
func listFromService[T any, S any](ctx context.Context, d readServiceDescriptor[T, S], service fileee.ReadService[T], in genericListInput) (*mcp.CallToolResult, genericListOutput[S], error) {
	res, err := service.Query(ctx, fileee.QueryOptions{
		Start: nonNegative(in.Start),
		Limit: clampLimit(in.Limit),
	})
	if err != nil {
		return nil, genericListOutput[S]{}, fmt.Errorf("fileee-mcp: tools: %s: %w", d.ListName, err)
	}

	out := genericListOutput[S]{TotalRows: res.TotalRows, Entries: make([]S, 0, len(res.Rows))}
	lines := make([]string, 0, len(res.Rows))
	for i := range res.Rows {
		entry := res.Rows[i]
		out.Entries = append(out.Entries, d.Summarize(&entry))
		if line := untrustedLineOf(d, &entry); line != "" {
			lines = append(lines, line)
		}
	}

	result, err := wrapUntrustedLines(lines)
	if err != nil {
		return nil, genericListOutput[S]{}, fmt.Errorf("fileee-mcp: tools: %s: %w", d.ListName, err)
	}
	return result, out, nil
}

// genericGetHandler resolves d.GetName. The empty-ID check runs before
// clientFor — the same order search_documentsHandler already uses for its
// own required parameter (read.go) — so a caller's input mistake is
// rejected without spending a login round trip on it, and so this path is
// testable without a *clientpool.Pool at all (see
// read_generic_test.go).
//
// It logs through logger the same way genericListHandler does (see that
// function's own doc comment for the endpoint-argument caveat) — the
// requested ID is logged at debug only (logToolStart), the same
// "arguments are content, not bare operating metadata" reasoning
// searchDocumentsHandler already applies to its own search term (read.go).
func genericGetHandler[T any, S any](p *clientpool.Pool, logger *slog.Logger, d readServiceDescriptor[T, S]) mcp.ToolHandlerFor[genericGetInput, genericGetOutput[S]] {
	return func(ctx context.Context, _ *mcp.CallToolRequest, in genericGetInput) (*mcp.CallToolResult, genericGetOutput[S], error) {
		start := time.Now()
		logToolStart(ctx, logger, d.GetName, slog.String("id", in.ID))

		if strings.TrimSpace(in.ID) == "" {
			err := fmt.Errorf("fileee-mcp: tools: %s: id must not be empty", d.GetName)
			logToolEnd(ctx, logger, d.GetName, start, "", 0, err)
			return nil, genericGetOutput[S]{}, err
		}

		client, err := clientFor(ctx, p)
		if err != nil {
			logToolEnd(ctx, logger, d.GetName, start, "", 0, err)
			return nil, genericGetOutput[S]{}, err
		}
		result, out, err := getFromService(ctx, d, d.Service(client), in.ID)
		logToolEnd(ctx, logger, d.GetName, start, "", 1, err)
		return result, out, err
	}
}

// getFromService is genericGetHandler's logic below client resolution —
// split out for the same testability reason as listFromService.
func getFromService[T any, S any](ctx context.Context, d readServiceDescriptor[T, S], service fileee.ReadService[T], id string) (*mcp.CallToolResult, genericGetOutput[S], error) {
	entry, err := service.Get(ctx, id)
	if err != nil {
		return nil, genericGetOutput[S]{}, fmt.Errorf("fileee-mcp: tools: %s: %w", d.GetName, err)
	}

	out := genericGetOutput[S]{Entry: d.Summarize(entry)}
	result, err := wrapUntrustedLines([]string{untrustedLineOf(d, entry)})
	if err != nil {
		return nil, genericGetOutput[S]{}, fmt.Errorf("fileee-mcp: tools: %s: %w", d.GetName, err)
	}
	return result, out, nil
}

// untrustedLineOf returns d.UntrustedLine(entity), or "" if UntrustedLine
// is nil — the descriptor's own declaration that this T carries no foreign
// text at all (see UntrustedLine's own doc comment on readServiceDescriptor).
func untrustedLineOf[T any, S any](d readServiceDescriptor[T, S], entity *T) string {
	if d.UntrustedLine == nil {
		return ""
	}
	return d.UntrustedLine(entity)
}

// wrapUntrustedLines frames lines' non-empty entries as one untrusted
// block, or produces no text content at all when none survive (an empty
// frame would be pure noise, the same call renderDocumentList already
// makes for list_documents — read.go). It composes wrapUntrusted (read.go)
// for the actual boundary generation and template; it does not reimplement
// either — the only thing this function decides is whether a block is
// worth producing at all and how to turn the result into a
// *mcp.CallToolResult.
func wrapUntrustedLines(lines []string) (*mcp.CallToolResult, error) {
	nonEmpty := make([]string, 0, len(lines))
	for _, line := range lines {
		if strings.TrimSpace(line) != "" {
			nonEmpty = append(nonEmpty, line)
		}
	}
	if len(nonEmpty) == 0 {
		return &mcp.CallToolResult{}, nil
	}
	text, err := wrapUntrusted(strings.Join(nonEmpty, "\n"))
	if err != nil {
		return nil, err
	}
	return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: text}}}, nil
}
