// This file covers what AttachMCPSelector's closure (New, server.go) logs
// through the diagnostic logger internal/diag builds — the resolved
// capability set and tool count on a normal call, and the missing scope
// name(s) when scopesSatisfied rejects a caller. Both driven through New()
// itself (WithLogOutput redirects the logger to a buffer this file can
// read) rather than calling the selector closure directly, since it is an
// unexported func value with no name of its own to test against —
// TestNewRegistersReadToolsUsableThroughTheRealWiring above is the
// precedent for exercising New()'s own wiring end-to-end instead of
// rebuilding it by hand.
package server

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/strausmann/fileee-mcp-server/internal/clientpool"
	"github.com/strausmann/fileee-mcp-server/internal/tools"
	"github.com/strausmann/gangway/identity/testidp"
	"github.com/strausmann/go-fileee/fileee"
)

// decodeSelectorLogLines parses buf's JSON-lines diagnostic output —
// gangway's OWN access log (accesslog.Middleware, plain NGINX-combined
// text, see that package's doc comment) is written to a separate stream
// this test never touches, so every line here is this server's own
// structured diagnostic output.
func decodeSelectorLogLines(t *testing.T, buf *bytes.Buffer) []map[string]any {
	t.Helper()
	var out []map[string]any
	for _, line := range strings.Split(strings.TrimRight(buf.String(), "\n"), "\n") {
		if line == "" {
			continue
		}
		var m map[string]any
		if err := json.Unmarshal([]byte(line), &m); err != nil {
			t.Fatalf("decodeSelectorLogLines: line %q is not valid JSON: %v", line, err)
		}
		out = append(out, m)
	}
	return out
}

// callAsSubject connects to httpSrv with a freshly minted token for
// subject and calls list_documents — enough to drive AttachMCPSelector's
// closure once per test, the way a real caller would.
func callAsSubject(
	t *testing.T, ctx context.Context, httpSrv *httptest.Server, idp *testidp.IDP,
	subject string, extraClaims map[string]any,
) (*mcp.CallToolResult, error) {
	t.Helper()
	claims := map[string]any{
		"iss": idp.URL(), "aud": "fileee-mcp-server", "sub": subject,
		"exp": time.Now().Add(time.Hour).Unix(),
	}
	for k, v := range extraClaims {
		claims[k] = v
	}
	token := idp.Token(claims)
	client := mcp.NewClient(&mcp.Implementation{Name: "selector-log-test", Version: "0.0.0"}, nil)
	transport := &mcp.StreamableClientTransport{
		Endpoint:             httpSrv.URL + "/mcp",
		HTTPClient:           &http.Client{Transport: bearerRoundTripper{token: token}},
		DisableStandaloneSSE: true,
	}
	session, err := client.Connect(ctx, transport, nil)
	if err != nil {
		return nil, err
	}
	defer func() { _ = session.Close() }()
	return session.CallTool(ctx, &mcp.CallToolParams{Name: tools.ToolListDocuments})
}

// TestSelectorLogsResolvedCapabilitiesAndToolCount is the acceptance test
// for "die aufgeloeste Faehigkeitsmenge und die Werkzeuganzahl erscheinen"
// (info level): a caller resolving to the default read-only ceiling gets
// a log line naming "read" and a tool_count of 2 — exactly
// len(tools.ReadToolKinds()), see toolCountFor's own doc comment on why
// that count is never a hand-maintained literal.
func TestSelectorLogsResolvedCapabilitiesAndToolCount(t *testing.T) {
	fileeeMock := newFileeeMock(t)
	cfg, idp := testConfigWithIDP(t)

	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)

	var buf bytes.Buffer
	s, err := New(ctx, cfg,
		WithLogOutput(&buf),
		WithPoolOptions(
			clientpool.WithClientOptions(fileee.WithBaseURL(fileeeMock), fileee.WithRateLimit(1000, 1000)),
			clientpool.WithSessionStore(func(accountKey string) fileee.SessionStore {
				return fileee.NewFileSessionStore(filepath.Join(t.TempDir(), accountKey+".json"))
			}),
		))
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	httpSrv := httptest.NewServer(s.Handler())
	t.Cleanup(httpSrv.Close)

	// "abc123" is testConfigWithIDP's MCP_ALLOWED_SUBJECTS entry.
	res, err := callAsSubject(t, ctx, httpSrv, idp, "abc123", nil)
	if err != nil {
		t.Fatalf("CallTool: %v", err)
	}
	if res.IsError {
		t.Fatalf("CallTool: IsError = true, content = %+v", res.Content)
	}

	var selectorLine map[string]any
	for _, line := range decodeSelectorLogLines(t, &buf) {
		if line["msg"] == "mcp selector: resolved capabilities" {
			selectorLine = line
			break
		}
	}
	if selectorLine == nil {
		t.Fatalf("no \"mcp selector: resolved capabilities\" line in the diagnostic log: %s", buf.String())
	}
	if selectorLine["capabilities"] != "read" {
		t.Errorf(`capabilities = %v, want "read" (testConfigWithIDP's default FILEEE_CAPABILITIES)`, selectorLine["capabilities"])
	}
	if selectorLine["tool_count"] != float64(len(tools.ReadToolKinds())) {
		t.Errorf("tool_count = %v, want %d", selectorLine["tool_count"], len(tools.ReadToolKinds()))
	}
}

// TestSelectorLogsMissingScopeOnRejection is the acceptance test for "eine
// Ablehnung durch scopesSatisfied erscheint mit dem Namen des fehlenden
// Scopes" — mirroring TestNewRefusesACallerWithoutTheRequiredScope in
// scopes_test.go, this time asserting on the diagnostic log rather than
// on the failed MCP handshake. The caller's token carries "user.read", the
// server requires "mcp.access" — the log line must name the ACTUAL missing
// scope, not just report a rejection happened.
func TestSelectorLogsMissingScopeOnRejection(t *testing.T) {
	fileeeMock := newFileeeMock(t)
	cfg, idp := testConfigWithIDPAndRequiredScopes(t, "mcp.access")

	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)

	var buf bytes.Buffer
	s, err := New(ctx, cfg,
		WithLogOutput(&buf),
		WithPoolOptions(
			clientpool.WithClientOptions(fileee.WithBaseURL(fileeeMock), fileee.WithRateLimit(1000, 1000)),
			clientpool.WithSessionStore(func(accountKey string) fileee.SessionStore {
				return fileee.NewFileSessionStore(filepath.Join(t.TempDir(), accountKey+".json"))
			}),
		))
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	httpSrv := httptest.NewServer(s.Handler())
	t.Cleanup(httpSrv.Close)

	// "abc123" is testConfigWithIDPAndRequiredScopes' MCP_ALLOWED_SUBJECTS
	// entry; the token carries a DIFFERENT scope than the one required.
	_, err = callAsSubject(t, ctx, httpSrv, idp, "abc123", map[string]any{"scp": "user.read"})
	if err == nil {
		t.Fatal("Connect/CallTool succeeded with a token missing the required scope")
	}

	var rejectLine map[string]any
	for _, line := range decodeSelectorLogLines(t, &buf) {
		if line["msg"] == "mcp selector: caller rejected: required scope missing" {
			rejectLine = line
			break
		}
	}
	if rejectLine == nil {
		t.Fatalf("no \"mcp selector: caller rejected: required scope missing\" line in the diagnostic log: %s", buf.String())
	}
	missing, ok := rejectLine["missing_scopes"].([]any)
	if !ok || len(missing) != 1 || missing[0] != "mcp.access" {
		t.Errorf(`missing_scopes = %v, want ["mcp.access"]`, rejectLine["missing_scopes"])
	}
}
