package tools

// untrusted_structured_test.go pins the two-channel response shape of every
// tool that returns foreign text (issue #82, new finding of 2026-10-24):
//
//   - StructuredContent carries the framed block in its own schema field
//     "untrustedText", and nothing of the foreign text outside that field;
//   - Content carries two blocks: the unframed JSON of the structured output
//     (WITHOUT untrustedText), then the framed block, byte-identical to the
//     field's value.
//
// Why the table drives the *FromService functions with fakes and not a real
// client session per tool: a session needs one wire fixture per tool, and the
// *FromService seam is exactly where each handler builds its result. The
// session-level proof for list_documents lives in read_test.go; the
// tools/list schema proof for ALL tools lives in
// TestEveryForeignTextToolDeclaresUntrustedTextInItsOutputSchema.

import (
	"context"
	"encoding/json"
	"regexp"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/strausmann/go-fileee/fileee"
)

// openTagPattern matches the opening tag of a framed block and captures its
// boundary value.
var openTagPattern = regexp.MustCompile(`<untrusted_external_content boundary="([0-9a-f]+)">`)

// assertDualChannel checks the contract above for one tool result. out is the
// typed structured output the handler returned; marker is a unique string the
// fixture placed in the foreign text.
func assertDualChannel(t *testing.T, res *mcp.CallToolResult, out any, marker string) {
	t.Helper()

	raw, err := json.Marshal(out)
	if err != nil {
		t.Fatalf("marshal structured output: %v", err)
	}
	var structured map[string]any
	if err := json.Unmarshal(raw, &structured); err != nil {
		t.Fatalf("unmarshal structured output: %v", err)
	}

	// (i) structuredContent carries the framed block, with boundary and marker.
	untrusted, _ := structured["untrustedText"].(string)
	if untrusted == "" {
		t.Fatalf("structured output has no untrustedText field: %s", raw)
	}
	if !strings.Contains(untrusted, marker) {
		t.Errorf("untrustedText does not carry the foreign text %q: %q", marker, untrusted)
	}
	if n := len(openTagPattern.FindAllString(untrusted, -1)); n != 1 {
		t.Errorf("untrustedText holds %d real opening tags, want exactly 1: %q", n, untrusted)
	}
	if !strings.HasPrefix(untrusted, "<untrusted_external_content boundary=") ||
		!strings.HasSuffix(untrusted, "</untrusted_external_content boundary="+boundaryOf(t, untrusted)+">") {
		t.Errorf("untrustedText is not exactly one framed block: %q", untrusted)
	}

	// The foreign text must be nowhere in the structured output except inside
	// untrustedText.
	delete(structured, "untrustedText")
	rest, _ := json.Marshal(structured)
	if strings.Contains(string(rest), marker) {
		t.Errorf("foreign text leaked outside untrustedText into the structured output: %s", rest)
	}

	// (ii) content holds exactly two blocks: JSON first, framed text second.
	if len(res.Content) != 2 {
		t.Fatalf("Content has %d blocks, want 2 (JSON, framed text)", len(res.Content))
	}
	jsonBlock := textOf(t, res.Content[0])
	framedBlock := textOf(t, res.Content[1])

	var decoded map[string]any
	if err := json.Unmarshal([]byte(jsonBlock), &decoded); err != nil {
		t.Fatalf("Content[0] is not a JSON object: %v: %q", err, jsonBlock)
	}
	if len(decoded) == 0 {
		t.Error("Content[0] JSON is empty, want the structured metadata")
	}
	// (iii) the JSON block is the structured output minus untrustedText, and
	// carries no foreign text and no frame.
	if _, has := decoded["untrustedText"]; has {
		t.Error("Content[0] JSON still contains untrustedText")
	}
	if strings.Contains(jsonBlock, marker) || strings.Contains(jsonBlock, "untrusted_external_content") {
		t.Errorf("Content[0] JSON block carries foreign text or a frame: %q", jsonBlock)
	}
	restJSON, _ := json.Marshal(structured)
	if !jsonEqual(t, jsonBlock, string(restJSON)) {
		t.Errorf("Content[0] = %s, want the structured output without untrustedText = %s", jsonBlock, restJSON)
	}

	// The framed block in Content is byte-identical to the field (same boundary).
	if !strings.HasSuffix(framedBlock, untrusted) {
		t.Errorf("Content[1] does not end with the untrustedText block\nContent[1]: %q\nfield: %q", framedBlock, untrusted)
	}
}

func boundaryOf(t *testing.T, framed string) string {
	t.Helper()
	m := openTagPattern.FindStringSubmatch(framed)
	if m == nil {
		t.Fatalf("no opening tag in %q", framed)
	}
	return `"` + m[1] + `"`
}

func textOf(t *testing.T, c mcp.Content) string {
	t.Helper()
	tc, ok := c.(*mcp.TextContent)
	if !ok {
		t.Fatalf("content block is %T, want *mcp.TextContent", c)
	}
	return tc.Text
}

func jsonEqual(t *testing.T, a, b string) bool {
	t.Helper()
	var x, y any
	if err := json.Unmarshal([]byte(a), &x); err != nil {
		t.Fatalf("unmarshal %q: %v", a, err)
	}
	if err := json.Unmarshal([]byte(b), &y); err != nil {
		t.Fatalf("unmarshal %q: %v", b, err)
	}
	xb, _ := json.Marshal(x)
	yb, _ := json.Marshal(y)
	return string(xb) == string(yb)
}

// TestForeignTextToolsReturnItInBothChannels runs the contract over EVERY
// *FromService seam that frames foreign text — one case per tool, so removing
// the helper call from a single handler turns exactly that case red.
func TestForeignTextToolsReturnItInBothChannels(t *testing.T) {
	ctx := context.Background()
	const marker = "Quartalsabrechnung-ZZ9"

	cases := []struct {
		name string
		run  func(t *testing.T) (*mcp.CallToolResult, any)
	}{
		{"list_documents", func(t *testing.T) (*mcp.CallToolResult, any) {
			res, out, err := listDocumentsResult(&fileee.QueryResult[fileee.Document]{
				TotalRows: 1,
				Rows:      []fileee.Document{{ID: "d1", Status: "DONE", Attributes: fileee.DocumentAttributes{Title: marker}}},
			})
			must(t, err)
			return res, out
		}},
		{"get_document", func(t *testing.T) (*mcp.CallToolResult, any) {
			svc := &fakeDocumentService{getResult: &fileee.Document{ID: "d1", Status: "DONE", Attributes: fileee.DocumentAttributes{Title: marker}}}
			res, out, err := documentFromService(ctx, svc, "d1", nil)
			must(t, err)
			return res, out
		}},
		{"sync_documents", func(t *testing.T) (*mcp.CallToolResult, any) {
			svc := &fakeDocumentService{diffResult: &fileee.DiffResult[fileee.Document]{
				Rows:       []fileee.Document{{ID: "d1", Status: "DONE", Attributes: fileee.DocumentAttributes{Title: marker}}},
				TotalRows:  1,
				NextCursor: fileee.NewCursor("Document"),
			}}
			res, out, err := documentsSyncFromService(ctx, svc, fileee.NewCursor("Document"))
			must(t, err)
			return res, out
		}},
		{"list_document_conversations", func(t *testing.T) (*mcp.CallToolResult, any) {
			svc := &fakeDocumentService{conversationsResult: []fileee.Conversation{{ID: "c1", Title: marker, ConversationType: "SHARE", Kind: "DIRECT"}}}
			res, out, err := documentConversationsFromService(ctx, svc, "d1")
			must(t, err)
			return res, out
		}},
		{"get_page_ocr", func(t *testing.T) (*mcp.CallToolResult, any) {
			svc := &fakeDocumentBinaryService{ocrResult: []fileee.OCRToken{{Text: marker, WebappID: "w1", Left: 1}}}
			res, out, err := documentPageOCRFromService(ctx, svc, "p1")
			must(t, err)
			return res, out
		}},
		{"generic list_*", func(t *testing.T) (*mcp.CallToolResult, any) {
			svc := &fakeReadService[fileee.Tag]{queryResult: &fileee.QueryResult[fileee.Tag]{
				TotalRows: 1, Rows: []fileee.Tag{{ID: "t1", Name: marker}},
			}}
			res, out, err := listFromService(ctx, tagDescriptor(), svc, genericListInput{})
			must(t, err)
			return res, out
		}},
		{"generic get_*", func(t *testing.T) (*mcp.CallToolResult, any) {
			svc := &fakeReadService[fileee.Tag]{getResult: &fileee.Tag{ID: "t1", Name: marker}}
			res, out, err := getFromService(ctx, tagDescriptor(), svc, "t1", nil)
			must(t, err)
			return res, out
		}},
		{"generic sync_*", func(t *testing.T) (*mcp.CallToolResult, any) {
			svc := &fakeReadService[fileee.Contact]{diffResult: &fileee.DiffResult[fileee.Contact]{
				Rows: []fileee.Contact{{ID: "c1", FirstName: marker}}, TotalRows: 1, NextCursor: fileee.NewCursor("Contact"),
			}}
			res, out, err := syncFromService(ctx, contactSyncDescriptor(), svc, fileee.NewCursor("Contact"))
			must(t, err)
			return res, out
		}},
		{"update_contact", func(t *testing.T) (*mcp.CallToolResult, any) {
			svc := &fakeContactWriteService{getResult: &fileee.Contact{ID: "c1"}, updateResult: &fileee.Contact{ID: "c1", FirstName: marker}}
			res, out, err := updateContactFromService(ctx, svc, updateContactInput{ID: "c1"})
			must(t, err)
			return res, out
		}},
		{"create_contact", func(t *testing.T) (*mcp.CallToolResult, any) {
			svc := &fakeContactCreateService{createResult: &fileee.Contact{ID: "c2", FirstName: marker}}
			res, out, err := createContactFromService(ctx, svc, createContactInput{FirstName: marker})
			must(t, err)
			return res, out
		}},
		{"create_reminder", func(t *testing.T) (*mcp.CallToolResult, any) {
			svc := &fakeReminderCreateService{createResult: &fileee.Reminder{ID: "r1", Description: marker}}
			res, out, err := createReminderFromService(ctx, svc, createReminderInput{Description: marker})
			must(t, err)
			return res, out
		}},
		{"update_reminder", func(t *testing.T) (*mcp.CallToolResult, any) {
			svc := &fakeReminderWriteService{getResult: &fileee.Reminder{ID: "r1"}, updateResult: &fileee.Reminder{ID: "r1", Description: marker}}
			res, out, err := updateReminderFromService(ctx, svc, updateReminderInput{ID: "r1"})
			must(t, err)
			return res, out
		}},
		{"update_document", func(t *testing.T) (*mcp.CallToolResult, any) {
			svc := &fakeDocumentUpdateService{
				getResult:    &fileee.Document{ID: "d1"},
				updateResult: &fileee.Document{ID: "d1", Attributes: fileee.DocumentAttributes{Title: marker}},
			}
			res, out, err := updateDocumentFromService(ctx, svc, updateDocumentInput{ID: "d1", Title: ptr(marker)})
			must(t, err)
			return res, out
		}},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			res, out := tc.run(t)
			assertDualChannel(t, res, out, marker)
		})
	}
}

// withoutFrame returns out as the generic map its JSON form decodes to, with
// the framed untrustedText field removed. The older "the structured part
// carries no foreign text" tests use it: since ADR-0021 foreign text legitimately
// appears in the structured output, but ONLY inside that one framed field.
func withoutFrame(out any) map[string]any {
	raw, err := json.Marshal(out)
	if err != nil {
		panic(err)
	}
	var m map[string]any
	if err := json.Unmarshal(raw, &m); err != nil {
		panic(err)
	}
	delete(m, "untrustedText")
	return m
}

func must(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

// TestToolsWithoutForeignTextKeepTheSDKFallback: a result with nothing to
// frame stays untouched, so the SDK still adds its own JSON fallback block and
// no untrustedText key appears.
func TestToolsWithoutForeignTextKeepTheSDKFallback(t *testing.T) {
	svc := &fakeReadService[fileee.Tag]{queryResult: &fileee.QueryResult[fileee.Tag]{TotalRows: 0}}
	res, out, err := listFromService(context.Background(), tagDescriptor(), svc, genericListInput{})
	must(t, err)
	if res.Content != nil {
		t.Errorf("Content = %v, want nil so the SDK adds its JSON fallback", res.Content)
	}
	raw, _ := json.Marshal(out)
	if strings.Contains(string(raw), "untrustedText") {
		t.Errorf("structured output carries untrustedText although nothing was framed: %s", raw)
	}
}

// TestAttachUntrustedStructuredNeverPublishesUnframedText covers the helper's
// degenerate inputs: whatever goes wrong, foreign text must not end up in the
// structured output without its frame.
func TestAttachUntrustedStructuredNeverPublishesUnframedText(t *testing.T) {
	type out struct {
		untrustedTextField
		ID string `json:"id"`
	}
	for name, res := range map[string]*mcp.CallToolResult{
		"nil result":           nil,
		"no content":           {},
		"non-text block":       {Content: []mcp.Content{&mcp.ImageContent{Data: []byte("x"), MIMEType: "image/png"}}},
		"text without a frame": {Content: []mcp.Content{&mcp.TextContent{Text: "Returned 0 of 0 total document(s)."}}},
	} {
		t.Run(name, func(t *testing.T) {
			o := out{ID: "x"}
			attachUntrustedStructured(res, &o, &o.UntrustedText)
			if o.UntrustedText != "" {
				t.Errorf("UntrustedText = %q, want empty", o.UntrustedText)
			}
		})
	}

	t.Run("unmarshalable output", func(t *testing.T) {
		type bad struct {
			untrustedTextField
			C chan int `json:"c"`
		}
		b := bad{C: make(chan int)}
		res := &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: wrapForTest("x")}}}
		attachUntrustedStructured(res, &b, &b.UntrustedText)
		if b.UntrustedText != "" || len(res.Content) != 1 {
			t.Errorf("a failed marshal must leave result and field untouched, got field %q, %d blocks", b.UntrustedText, len(res.Content))
		}
	})
}

func wrapForTest(body string) string {
	return formatUntrustedBlock("0123456789abcdef0123456789abcdef", body)
}
