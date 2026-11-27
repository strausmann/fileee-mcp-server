package tools_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/strausmann/fileee-mcp-server/internal/accounts"
	"github.com/strausmann/fileee-mcp-server/internal/tools"
	"github.com/strausmann/gangway/identity/testidp"
	"github.com/strausmann/go-fileee/fileee"
)

const reviewHTTPDocument = `{"id":"doc-example","version":0,"status":"CLASSIFIED","modified":"2026-01-02T03:04:05.456Z","pages":[{"id":"page-example","imageVersion":"7"}],"attributes":{"data":{"read":{"type":"BOOLEAN","value":false},"title":{"type":"TEXT","value":"Beispielrechnung"},"issueDate":{"type":"DATE","value":"2026-01-01"}}}}`

func reviewHTTPBackend(t *testing.T) *http.ServeMux {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/f/start", func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNoContent) })
	mux.HandleFunc("POST /api/f/existent", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, `{"existent":true,"twoFactorAuthEnabled":false}`)
	})
	mux.HandleFunc("POST /api/f/login", func(w http.ResponseWriter, _ *http.Request) {
		http.SetCookie(w, &http.Cookie{Name: "JSESSIONID", Value: "synthetic-test-session", Path: "/"})
		writeJSON(w, `{"loggedIn":true}`)
	})
	mux.HandleFunc("GET /api/f/user-session", func(w http.ResponseWriter, _ *http.Request) { writeJSON(w, `{"authorized":true,"secondsBlocked":0}`) })
	return mux
}

func reviewHTTPClient(t *testing.T, mux *http.ServeMux) *mcp.ClientSession {
	t.Helper()
	backend := httptest.NewServer(mux)
	t.Cleanup(backend.Close)
	pool := testPool(t, backend.URL, accounts.NewSingle(fileee.Credentials{Username: "fixture@example.invalid", Password: "fixture-password"}))
	server := mcp.NewServer(&mcp.Implementation{Name: "review-test", Version: "1"}, nil)
	tools.RegisterAll(server, pool, tools.ServerInfo{}, testLogger(t), testRec(t))
	idp := testidp.New(t)
	return connectAs(t, newGangwayServer(t, idp, server), idp, "fixture-subject")
}

func TestDocumentReviewThroughAuthenticatedMCP(t *testing.T) {
	mux := reviewHTTPBackend(t)
	mux.HandleFunc("POST /api/documents/rest/query", func(w http.ResponseWriter, r *http.Request) {
		var input struct {
			Criteria []struct {
				Field struct{ Value string }
				Value struct{ Value any }
			}
		}
		if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
			t.Error(err)
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		for _, filter := range input.Criteria {
			if filter.Field.Value == "DocumentField:DOCUMENT_INFORMATION__ISREAD" && filter.Value.Value == false {
				writeJSON(w, fmt.Sprintf(`{"rows":[%s],"totalRows":1}`, reviewHTTPDocument))
				return
			}
		}
		t.Error("missing Dashboard filter")
		w.WriteHeader(http.StatusBadRequest)
	})
	mux.HandleFunc("GET /api/documents/rest/doc-example", func(w http.ResponseWriter, _ *http.Request) { writeJSON(w, reviewHTTPDocument) })
	session := reviewHTTPClient(t, mux)
	queued := mustCall(t, session, tools.ToolListReviewDocuments, map[string]any{})
	if queued.IsError || !strings.Contains(everythingTheModelCanSee(t, queued), `"doc-example"`) {
		t.Fatal("Dashboard did not return the expected document")
	}
	read := mustCall(t, session, tools.ToolGetDocumentReview, map[string]any{"id": "doc-example"})
	out, ok := read.StructuredContent.(map[string]any)
	if read.IsError || !ok || out["modified"] != "2026-01-02T03:04:05.456Z" || out["inDashboard"] != true {
		t.Fatal("review did not return the verified state")
	}
	if !strings.Contains(out["untrustedText"].(string), `"issueDate":"2026-01-01"`) {
		t.Fatal("expected framed issue date")
	}
	for _, tc := range []mcp.CallToolParams{
		{Name: tools.ToolGetDocumentReview, Arguments: map[string]any{"id": "unknown"}},
		{Name: tools.ToolListReviewDocuments, Arguments: map[string]any{"state": "incorrect"}},
	} {
		result, err := session.CallTool(context.Background(), &tc)
		if err == nil && !result.IsError {
			t.Errorf("invalid request accepted by %s", tc.Name)
		}
	}
}
