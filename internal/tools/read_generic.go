// read_generic.go: seven Fileee services share fileee.ReadService[T]
// (Query/Diff/Get, see go-fileee's own doc comment on that interface).
// Instead of fourteen near-identical handlers, one generic helper mounts
// them all — the differences between services live exclusively in a
// descriptor, never in copy-pasted handler bodies.
package tools

import (
	"context"
	"fmt"
	"strings"

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
// applies there). Returning "" from UntrustedLine means the entity carries
// no foreign text worth framing — search_documentsHandler already makes
// exactly that call for its own service, see its own doc comment.
type readServiceDescriptor[T any, S any] struct {
	// ListName and GetName are the registered tool names.
	ListName string
	GetName  string
	// ListDescription and GetDescription are each tool's Description, held
	// to the same four-part standard (what, returns, when, does-not) and
	// minimum length descriptions_test.go checks for RegisterRead's own
	// tools — checked there again once Aufgabe 3/4 wire a service through
	// this helper into RegisterRead.
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
	// line for the untrusted block wrapUntrustedLines builds, or "" if
	// this entity carries none.
	UntrustedLine func(*T) string
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
// same pattern RegisterRead uses for list_documents/search_documents,
// generalized over any fileee.ReadService[T].
func registerReadService[T any, S any](s *mcp.Server, p *clientpool.Pool, d readServiceDescriptor[T, S]) {
	mcp.AddTool(s, &mcp.Tool{
		Name:        d.ListName,
		Description: d.ListDescription,
		Annotations: &mcp.ToolAnnotations{ReadOnlyHint: true},
	}, genericListHandler(p, d))

	mcp.AddTool(s, &mcp.Tool{
		Name:        d.GetName,
		Description: d.GetDescription,
		Annotations: &mcp.ToolAnnotations{ReadOnlyHint: true},
	}, genericGetHandler(p, d))
}

// genericListHandler resolves d.ListName. It splits client resolution
// (clientFor, which needs a real, gangway-verified caller identity — see
// that function's own doc comment) from the actual Query/summarize/frame
// logic (listFromService), so the latter stays testable against a fake
// fileee.ReadService[T] without needing a live Fileee login (see
// read_generic_test.go).
func genericListHandler[T any, S any](p *clientpool.Pool, d readServiceDescriptor[T, S]) mcp.ToolHandlerFor[genericListInput, genericListOutput[S]] {
	return func(ctx context.Context, _ *mcp.CallToolRequest, in genericListInput) (*mcp.CallToolResult, genericListOutput[S], error) {
		client, err := clientFor(ctx, p)
		if err != nil {
			return nil, genericListOutput[S]{}, err
		}
		return listFromService(ctx, d, d.Service(client), in)
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
		if line := d.UntrustedLine(&entry); line != "" {
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
func genericGetHandler[T any, S any](p *clientpool.Pool, d readServiceDescriptor[T, S]) mcp.ToolHandlerFor[genericGetInput, genericGetOutput[S]] {
	return func(ctx context.Context, _ *mcp.CallToolRequest, in genericGetInput) (*mcp.CallToolResult, genericGetOutput[S], error) {
		if strings.TrimSpace(in.ID) == "" {
			return nil, genericGetOutput[S]{}, fmt.Errorf("fileee-mcp: tools: %s: id must not be empty", d.GetName)
		}

		client, err := clientFor(ctx, p)
		if err != nil {
			return nil, genericGetOutput[S]{}, err
		}
		return getFromService(ctx, d, d.Service(client), in.ID)
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
	result, err := wrapUntrustedLines([]string{d.UntrustedLine(entry)})
	if err != nil {
		return nil, genericGetOutput[S]{}, fmt.Errorf("fileee-mcp: tools: %s: %w", d.GetName, err)
	}
	return result, out, nil
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
