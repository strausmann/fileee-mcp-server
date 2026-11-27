package tools_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/strausmann/fileee-mcp-server/internal/accounts"
	"github.com/strausmann/fileee-mcp-server/internal/clientpool"
	"github.com/strausmann/fileee-mcp-server/internal/tools"
	"github.com/strausmann/gangway/identity/testidp"
	"github.com/strausmann/go-fileee/fileee"
)

func TestContactSearchThroughMCPUsesFilteredQueriesAndOnlyReadsContacts(t *testing.T) {
	var mu sync.Mutex
	nameQueries, selfQueries, mutations := 0, 0, 0
	failNameQuery := false
	gets := make(map[string]int)
	write := func(w http.ResponseWriter, value any) {
		w.Header().Set("Content-Type", "application/json")
		if err := json.NewEncoder(w).Encode(value); err != nil {
			t.Error(err)
		}
	}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/f/start", func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNoContent) })
	mux.HandleFunc("POST /api/f/existent", func(w http.ResponseWriter, _ *http.Request) {
		write(w, map[string]any{"existent": true, "twoFactorAuthEnabled": false})
	})
	mux.HandleFunc("POST /api/f/login", func(w http.ResponseWriter, _ *http.Request) {
		http.SetCookie(w, &http.Cookie{Name: "JSESSIONID", Value: "synthetic-session", Path: "/"})
		// A user object ID is deliberately different from the ME contact ID.
		http.SetCookie(w, &http.Cookie{Name: "userId", Value: "auth-user-object", Path: "/"})
		write(w, map[string]any{"loggedIn": true})
	})
	mux.HandleFunc("GET /api/f/user-session", func(w http.ResponseWriter, _ *http.Request) {
		write(w, map[string]any{"authorized": true, "secondsBlocked": 0})
	})
	mux.HandleFunc("POST /api/companies/rest/query", func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		var request struct {
			Criteria []struct {
				Field    struct{ Value string }
				Operator string
				Value    struct{ Value string }
			}
			Start, Limit int
		}
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Error(err)
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		if len(request.Criteria) == 1 && request.Criteria[0].Field.Value == "ContactinyObjectField:CONTACT_TYPE" && request.Criteria[0].Operator == "EQ" && request.Criteria[0].Value.Value == "ContactType:ME" {
			selfQueries++
			write(w, map[string]any{"rows": []fileee.Company{{ID: "self-company", ContactType: "ME", CompanyName: "Own Person", MainContactID: "self-contact", UserPreferredContactID: "self-contact"}}, "totalRows": 1})
			return
		}
		if len(request.Criteria) != 2 || request.Criteria[0].Field.Value != "ContactinyObjectField:CONTACT_TYPE" || request.Criteria[0].Operator != "NEQ" || request.Criteria[0].Value.Value != "ContactType:ME" || request.Criteria[1].Field.Value != "ContactinyObjectField:COMPANY_NAME" || request.Criteria[1].Operator != "FUZZY" {
			t.Error("search sent an unfiltered or undocumented company query")
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		nameQueries++
		if failNameQuery {
			w.WriteHeader(http.StatusBadRequest)
			write(w, map[string]any{"errorMessage": "synthetic read failure"})
			return
		}
		write(w, map[string]any{"rows": []fileee.Company{{ID: "established", CompanyName: "EXM Mutual AG", ContactType: "COMPANY", DocumentCounter: 30, MainContactID: "main", UserPreferredContactID: "preferred"}}, "totalRows": 1})
	})
	mux.HandleFunc("GET /api/contacts/rest/{id}", func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		id := r.PathValue("id")
		gets[id]++
		if id == "self-contact" {
			write(w, &fileee.Contact{ID: id, CompanyID: "self-company", FirstName: "Own", LastName: "Person", ContactType: fileee.ContactTypeMe})
		} else if id == "main" || id == "preferred" {
			write(w, &fileee.Contact{ID: id, CompanyID: "established", CompanyName: "EXM Mutual\nExternal identity", ContactType: fileee.ContactTypeCompany, Address: fileee.Address{Street: "Synthetic Street 1"}})
		} else {
			w.WriteHeader(http.StatusNotFound)
		}
	})
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		if r.Method == http.MethodPut || r.Method == http.MethodDelete || (r.Method == http.MethodPost && !strings.HasPrefix(r.URL.Path, "/api/f/")) {
			mutations++
		}
		t.Errorf("unexpected search request: %s %s", r.Method, r.URL.Path)
		w.WriteHeader(http.StatusBadRequest)
	})
	backend := httptest.NewServer(mux)
	t.Cleanup(backend.Close)
	pool := clientpool.New(accounts.NewSingle(fileee.Credentials{Username: "synthetic@example.invalid", Password: "synthetic-test-password"}),
		clientpool.WithClientOptions(fileee.WithBaseURL(backend.URL), fileee.WithRateLimit(10000, 10000)),
		clientpool.WithSessionStore(func(string) fileee.SessionStore {
			return fileee.NewFileSessionStore(filepath.Join(t.TempDir(), "session.json"))
		}))
	t.Cleanup(pool.Close)
	s := mcp.NewServer(&mcp.Implementation{Name: "contact-search-test", Version: "1"}, nil)
	tools.RegisterAll(s, pool, tools.ServerInfo{}, testLogger(t), testRec(t))
	idp := testidp.New(t)
	cs := connectAs(t, newGangwayServer(t, idp, s), idp, "synthetic-subject")
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	list, err := cs.ListTools(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, tool := range list.Tools {
		if tool.Name == tools.ToolSearchContacts {
			found = true
			if !tool.Annotations.ReadOnlyHint {
				t.Fatal("contact search is not declared read-only")
			}
		}
	}
	if !found {
		t.Fatal("contact search was not registered")
	}
	call := func(arguments map[string]any, wantError bool) map[string]any {
		t.Helper()
		result, err := cs.CallTool(ctx, &mcp.CallToolParams{Name: tools.ToolSearchContacts, Arguments: arguments})
		if err != nil {
			t.Fatalf("search call: %v", err)
		}
		if result.IsError != wantError {
			t.Fatalf("search returned isError=%v, want %v", result.IsError, wantError)
		}
		if wantError {
			return nil
		}
		return result.StructuredContent.(map[string]any)
	}
	out := call(map[string]any{"query": "EXM"}, false)
	if out["coverageComplete"] != true || out["contactsDirectoryScanned"] != false || out["totalMatches"] != float64(2) {
		t.Fatalf("search output did not verify the focused lookup: coverage=%v matches=%v", out["coverageComplete"], out["totalMatches"])
	}
	if !strings.Contains(out["untrustedText"].(string), "Synthetic Street 1") {
		t.Fatal("identity details not returned in the framed channel")
	}
	call(map[string]any{"query": "exm", "start": 1, "limit": 1}, false)
	call(map[string]any{"query": "."}, true)
	mu.Lock()
	if nameQueries != 1 || selfQueries != 1 || gets["self-contact"] != 1 || gets["auth-user-object"] != 0 || gets["main"] != 1 || gets["preferred"] != 1 || mutations != 0 {
		t.Fatal("cached page swept or mutated records, or assumed userId was a contact ID")
	}
	failNameQuery = true
	mu.Unlock()
	call(map[string]any{"query": "EXM", "refresh": true}, true)
}
