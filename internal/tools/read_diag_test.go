// This file covers this package's diagnostic-log integration: what
// list_documents/search_documents actually write through the *slog.Logger
// RegisterAll now takes, driven end-to-end through a real MCP client
// exactly like read_test.go's own tests — reusing that file's helpers
// (newIsolationServer, newErrorServer, newRejectedLoginServer,
// newGangwayServer, connectAs, mustCall) rather than duplicating them.
// internal/diag's own tests already cover the masking guarantee itself in
// isolation; what belongs here is proving it is actually WIRED — that
// this package's real handlers log through it, not a parallel, untested
// path.
package tools_test

import (
	"bytes"
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/strausmann/fileee-mcp-server/internal/accounts"
	"github.com/strausmann/fileee-mcp-server/internal/clientpool"
	"github.com/strausmann/fileee-mcp-server/internal/diag"
	"github.com/strausmann/fileee-mcp-server/internal/tools"
	"github.com/strausmann/gangway/identity/testidp"
	"github.com/strausmann/go-fileee/fileee"
)

// decodeLogLines parses buf's JSON-lines diagnostic output (one object
// per logged record, see internal/diag.New) into the decoded objects.
func decodeLogLines(t *testing.T, buf *bytes.Buffer) []map[string]any {
	t.Helper()
	var out []map[string]any
	for _, line := range strings.Split(strings.TrimRight(buf.String(), "\n"), "\n") {
		if line == "" {
			continue
		}
		var m map[string]any
		if err := json.Unmarshal([]byte(line), &m); err != nil {
			t.Fatalf("decodeLogLines: line %q is not valid JSON: %v", line, err)
		}
		out = append(out, m)
	}
	return out
}

// setupLoggedListDocuments wires a real gangway+MCP stack around a single
// list_documents-capable pool, exactly like TestListDocumentsIsolatesCallersByAccount
// does, but with a *bytes.Buffer-backed diagnostic logger at level instead
// of testLogger(t)'s discarded one — so the caller can inspect what got
// logged.
func setupLoggedListDocuments(t *testing.T, level diag.Level, queryBody string) (*mcp.ClientSession, *bytes.Buffer) {
	t.Helper()
	var buf bytes.Buffer
	logger := diag.New(level, &buf)

	srv := newIsolationServer(t, fixtureAccount{username: "alice@example.invalid", password: "pw", queryBody: queryBody})
	pool := testPool(t, srv, accounts.NewSingle(fileee.Credentials{Username: "alice@example.invalid", Password: "pw"}))
	mcpServer := mcp.NewServer(&mcp.Implementation{Name: "test", Version: "0.0.0"}, nil)
	tools.RegisterAll(mcpServer, pool, tools.ServerInfo{}, logger, testRec(t))

	idp := testidp.New(t)
	httpSrv := newGangwayServer(t, idp, mcpServer)
	return connectAs(t, httpSrv, idp, "any-subject"), &buf
}

// --- info: name/duration/outcome, never the caller's arguments ----------

// TestToolCallLoggingAtInfoOmitsArgumentsButReportsTheOutcome is the first
// acceptance test the task calls for: at FILEEE_LOG_LEVEL=info, a
// successful list_documents call logs its name, duration, a successful
// outcome and a result count — but never the caller-supplied Start/Limit
// arguments, which only appear at debug (see the companion test below).
func TestToolCallLoggingAtInfoOmitsArgumentsButReportsTheOutcome(t *testing.T) {
	docBody := `{"rows":[{"id":"doc-1","status":"DONE"}],"totalRows":1}`
	session, buf := setupLoggedListDocuments(t, diag.LevelInfo, docBody)

	res := mustCall(t, session, tools.ToolListDocuments, map[string]any{"start": 5, "limit": 10})
	if resultText(t, res) == "" {
		t.Fatal("expected non-empty result text")
	}

	lines := decodeLogLines(t, buf)
	if len(lines) != 1 {
		t.Fatalf("got %d log lines at info, want exactly 1 (the outcome line, no argument line): %v", len(lines), lines)
	}
	line := lines[0]

	if line["msg"] != "tool call succeeded" {
		t.Errorf(`msg = %v, want "tool call succeeded"`, line["msg"])
	}
	if line["tool"] != tools.ToolListDocuments {
		t.Errorf("tool = %v, want %q", line["tool"], tools.ToolListDocuments)
	}
	if _, ok := line["duration_ms"]; !ok {
		t.Error("duration_ms is missing from the outcome line")
	}
	if line["outcome"] != "ok" {
		t.Errorf(`outcome = %v, want "ok"`, line["outcome"])
	}
	if line["result_count"] != float64(1) {
		t.Errorf("result_count = %v, want 1", line["result_count"])
	}
	if line["fileee_endpoint"] != "POST /api/documents/rest/query" {
		t.Errorf("fileee_endpoint = %v, want the Fileee query endpoint", line["fileee_endpoint"])
	}
	if line["http_status"] != float64(200) {
		t.Errorf("http_status = %v, want 200", line["http_status"])
	}

	// The actual acceptance criterion: NEITHER the raw JSON keys "start"/
	// "limit" NOR their values appear anywhere in the info-level output.
	raw := buf.String()
	for _, forbidden := range []string{`"start"`, `"limit"`, `"args"`} {
		if strings.Contains(raw, forbidden) {
			t.Errorf("info-level output contains %s — arguments must only appear at debug: %s", forbidden, raw)
		}
	}
}

// --- debug: the same outcome line, PLUS the arguments -------------------

// TestToolCallLoggingAtDebugAlsoLogsArguments is the second acceptance
// test: at FILEEE_LOG_LEVEL=debug, the caller-supplied arguments appear —
// nested under "args", using the tool's own JSON field names — in
// addition to everything the info-level test above already checks.
func TestToolCallLoggingAtDebugAlsoLogsArguments(t *testing.T) {
	docBody := `{"rows":[{"id":"doc-1","status":"DONE"}],"totalRows":1}`
	session, buf := setupLoggedListDocuments(t, diag.LevelDebug, docBody)

	mustCall(t, session, tools.ToolListDocuments, map[string]any{"start": 5, "limit": 10})

	lines := decodeLogLines(t, buf)
	if len(lines) != 2 {
		t.Fatalf("got %d log lines at debug, want exactly 2 (arguments, then outcome): %v", len(lines), lines)
	}

	argLine := lines[0]
	if argLine["msg"] != "tool call: arguments" {
		t.Fatalf(`lines[0].msg = %v, want "tool call: arguments"`, argLine["msg"])
	}
	args, ok := argLine["args"].(map[string]any)
	if !ok {
		t.Fatalf(`lines[0]["args"] = %#v, want a nested object`, argLine["args"])
	}
	if args["start"] != float64(5) {
		t.Errorf(`args["start"] = %v, want 5`, args["start"])
	}
	if args["limit"] != float64(10) {
		t.Errorf(`args["limit"] = %v, want 10`, args["limit"])
	}

	if lines[1]["msg"] != "tool call succeeded" {
		t.Errorf(`lines[1].msg = %v, want "tool call succeeded"`, lines[1]["msg"])
	}
}

// TestSearchDocumentsTermIsDebugOnly is the search-specific half of the
// same acceptance criterion, using search_documents' own argument (a free-
// text search term, arguably more sensitive than list_documents' bare
// paging offsets — see internal/diag's own doc comment on why a search
// term is content, not mere operating metadata) instead of Start/Limit.
func TestSearchDocumentsTermIsDebugOnly(t *testing.T) {
	const term = "very-specific-invoice-number-42"
	searchBody := `{"rows":[{"id":"doc-1"}],"totalRows":1}`

	runSearch := func(level diag.Level) *bytes.Buffer {
		var buf bytes.Buffer
		logger := diag.New(level, &buf)

		srv := newIsolationServer(t, fixtureAccount{username: "alice@example.invalid", password: "pw", queryBody: searchBody})
		pool := testPool(t, srv, accounts.NewSingle(fileee.Credentials{Username: "alice@example.invalid", Password: "pw"}))
		mcpServer := mcp.NewServer(&mcp.Implementation{Name: "test", Version: "0.0.0"}, nil)
		tools.RegisterAll(mcpServer, pool, tools.ServerInfo{}, logger, testRec(t))

		idp := testidp.New(t)
		httpSrv := newGangwayServer(t, idp, mcpServer)
		session := connectAs(t, httpSrv, idp, "any-subject")

		mustCall(t, session, tools.ToolSearchDocuments, map[string]any{"term": term})
		return &buf
	}

	if buf := runSearch(diag.LevelInfo); strings.Contains(buf.String(), term) {
		t.Errorf("info-level output contains the search term %q — it must only appear at debug: %s", term, buf.String())
	}
	if buf := runSearch(diag.LevelDebug); !strings.Contains(buf.String(), term) {
		t.Errorf("debug-level output does not contain the search term %q: %s", term, buf.String())
	}
}

// --- never a response body or document content, at any level ------------

// TestToolCallLoggingNeverContainsDocumentContentOrResponseBody is the
// task's explicit safety requirement, checked at BOTH levels: a
// document's title (foreign content, ADR-0013) and Fileee's own backend
// error message must never reach the diagnostic log, however deep the
// caller turns FILEEE_LOG_LEVEL up. The title is exercised through a
// successful list_documents call; the backend error message through a
// failing one (newErrorServer) — classifyErr (read.go) reduces failures
// to a fixed-vocabulary "outcome" kind and, when known, a bare HTTP status
// integer, never the backend's own error text.
func TestToolCallLoggingNeverContainsDocumentContentOrResponseBody(t *testing.T) {
	const secretTitle = "Do-Not-Log-Me Bank Statement 2026"
	const secretBackendMessage = "do-not-log-me: internal database constraint violated"

	t.Run("document title, successful call", func(t *testing.T) {
		docBody := `{"rows":[{"id":"doc-1","status":"DONE",` +
			`"attributes":{"data":{"title":{"value":"` + secretTitle + `"}}}}],"totalRows":1}`

		for _, level := range []diag.Level{diag.LevelInfo, diag.LevelDebug} {
			session, buf := setupLoggedListDocuments(t, level, docBody)
			mustCall(t, session, tools.ToolListDocuments, nil)
			if strings.Contains(buf.String(), secretTitle) {
				t.Errorf("level=%q: diagnostic log contains the document title: %s", level, buf.String())
			}
		}
	})

	t.Run("fileee backend error body, failing call", func(t *testing.T) {
		body := `{"errorCode":"INTERNAL","errorMessage":"` + secretBackendMessage + `"}`

		for _, level := range []diag.Level{diag.LevelInfo, diag.LevelDebug} {
			var buf bytes.Buffer
			logger := diag.New(level, &buf)

			srv := newErrorServer(t, http.StatusInternalServerError, body)
			pool := testPool(t, srv, accounts.NewSingle(fileee.Credentials{Username: "alice@example.invalid", Password: "pw"}))
			mcpServer := mcp.NewServer(&mcp.Implementation{Name: "test", Version: "0.0.0"}, nil)
			tools.RegisterAll(mcpServer, pool, tools.ServerInfo{}, logger, testRec(t))

			idp := testidp.New(t)
			httpSrv := newGangwayServer(t, idp, mcpServer)
			session := connectAs(t, httpSrv, idp, "any-subject")

			res, err := session.CallTool(t.Context(), &mcp.CallToolParams{Name: tools.ToolListDocuments})
			if err != nil {
				t.Fatalf("CallTool: %v", err)
			}
			if !res.IsError {
				t.Fatal("res.IsError = false, want true when Fileee's own query endpoint answers 500")
			}

			if strings.Contains(buf.String(), secretBackendMessage) {
				t.Errorf("level=%q: diagnostic log contains Fileee's backend error message: %s", level, buf.String())
			}

			lines := decodeLogLines(t, &buf)
			last := lines[len(lines)-1]
			if last["outcome"] != "fileee_error" {
				t.Errorf(`level=%q: outcome = %v, want "fileee_error"`, level, last["outcome"])
			}
			if last["http_status"] != float64(http.StatusInternalServerError) {
				t.Errorf("level=%q: http_status = %v, want %d", level, last["http_status"], http.StatusInternalServerError)
			}
		}
	})
}

// --- classifyErr's other outcome kinds, through the same real wiring ----

// TestToolCallLoggingClassifiesAccessDeniedAndInvalidInput covers the two
// outcome kinds TestToolCallLoggingNeverContainsDocumentContentOrResponseBody
// does not: an unmapped caller (accounts.ErrNoAccount, "access_denied")
// and search_documents' own empty-term validation ("invalid_input") —
// mirroring TestUnknownCallerGetsAToolErrorNotAServerError and
// TestSearchDocumentsRejectsAnEmptyTerm in read_test.go, this time
// asserting on the diagnostic log rather than the tool result.
func TestToolCallLoggingClassifiesAccessDeniedAndInvalidInput(t *testing.T) {
	t.Run("access_denied", func(t *testing.T) {
		var buf bytes.Buffer
		logger := diag.New(diag.LevelInfo, &buf)

		srv := newIsolationServer(t, fixtureAccount{
			username: "alice@example.invalid", password: "pw", queryBody: `{"rows":[],"totalRows":0}`,
		})
		pool := testPool(t, srv, accounts.NewMulti(map[string]fileee.Credentials{
			"alice-subject": {Username: "alice@example.invalid", Password: "pw"},
		}))
		mcpServer := mcp.NewServer(&mcp.Implementation{Name: "test", Version: "0.0.0"}, nil)
		tools.RegisterAll(mcpServer, pool, tools.ServerInfo{}, logger, testRec(t))

		idp := testidp.New(t)
		httpSrv := newGangwayServer(t, idp, mcpServer)
		session := connectAs(t, httpSrv, idp, "mallory-subject")

		res, err := session.CallTool(t.Context(), &mcp.CallToolParams{Name: tools.ToolListDocuments})
		if err != nil {
			t.Fatalf("CallTool: %v", err)
		}
		if !res.IsError {
			t.Fatal("res.IsError = false, want true for an unmapped caller")
		}

		lines := decodeLogLines(t, &buf)
		last := lines[len(lines)-1]
		if last["outcome"] != "access_denied" {
			t.Errorf(`outcome = %v, want "access_denied"`, last["outcome"])
		}
	})

	t.Run("invalid_input", func(t *testing.T) {
		var buf bytes.Buffer
		logger := diag.New(diag.LevelInfo, &buf)

		pool := clientpool.New(accounts.NewSingle(fileee.Credentials{Username: "a", Password: "b"}))
		mcpServer := mcp.NewServer(&mcp.Implementation{Name: "test", Version: "0.0.0"}, nil)
		tools.RegisterAll(mcpServer, pool, tools.ServerInfo{}, logger, testRec(t))

		idp := testidp.New(t)
		httpSrv := newGangwayServer(t, idp, mcpServer)
		session := connectAs(t, httpSrv, idp, "any-subject")

		res, err := session.CallTool(t.Context(), &mcp.CallToolParams{
			Name: tools.ToolSearchDocuments, Arguments: map[string]any{"term": "   "},
		})
		if err != nil {
			t.Fatalf("CallTool: %v", err)
		}
		if !res.IsError {
			t.Fatal("res.IsError = false, want true for a blank search term")
		}

		lines := decodeLogLines(t, &buf)
		last := lines[len(lines)-1]
		if last["outcome"] != "invalid_input" {
			t.Errorf(`outcome = %v, want "invalid_input"`, last["outcome"])
		}
	})
}
