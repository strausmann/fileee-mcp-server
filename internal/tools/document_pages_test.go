package tools_test

import (
	"bytes"
	"encoding/json"
	"image"
	"image/jpeg"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/strausmann/gangway/identity/testidp"
	"github.com/strausmann/go-fileee/fileee"

	"github.com/strausmann/fileee-mcp-server/internal/accounts"
	"github.com/strausmann/fileee-mcp-server/internal/tools"
)

// documentPagesResult is decoded independently of the server's output type:
// this reproduces what a client can discover from either MCP result channel.
type documentPagesResult struct {
	PageCount int `json:"pageCount"`
	Pages     []struct {
		ID           string `json:"id"`
		ImageVersion int64  `json:"imageVersion"`
	} `json:"pages"`
}

func decodeDocumentPages(t *testing.T, value any) documentPagesResult {
	t.Helper()
	raw, err := json.Marshal(value)
	if err != nil {
		t.Fatalf("marshal document result: %v", err)
	}
	var out documentPagesResult
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatalf("decode document result: %v", err)
	}
	return out
}

// newDocumentPagesServer supplies synthetic documents, OCR and a real JPEG.
// Its IDs are deliberately unrelated to the document ID and not alphabetic
// in page order. A mutable version detects stale or cached image metadata.
func newDocumentPagesServer(t *testing.T, title string, hasPages bool, version *atomic.Int64) (string, []byte) {
	t.Helper()
	var jpegBytes bytes.Buffer
	if err := jpeg.Encode(&jpegBytes, image.NewRGBA(image.Rect(0, 0, 1, 1)), nil); err != nil {
		t.Fatalf("encode synthetic JPEG: %v", err)
	}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/f/start", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	})
	mux.HandleFunc("POST /api/f/existent", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"existent":true,"twoFactorAuthEnabled":false}`))
	})
	mux.HandleFunc("POST /api/f/login", func(w http.ResponseWriter, _ *http.Request) {
		http.SetCookie(w, &http.Cookie{Name: "JSESSIONID", Value: "synthetic-session"})
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"loggedIn":true}`))
	})
	mux.HandleFunc("GET /api/f/user-session", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"authorized":true,"secondsBlocked":0}`))
	})
	mux.HandleFunc("GET /api/documents/rest/{id}", func(w http.ResponseWriter, r *http.Request) {
		if r.PathValue("id") != "synthetic-document" {
			http.NotFound(w, r)
			return
		}
		pages := []map[string]any{}
		if hasPages {
			// Fileee permits both numeric and string versions. Zero must
			// also remain present in the MCP result and be usable as input.
			pages = append(pages,
				map[string]any{"id": "page-z", "imageVersion": version.Load()},
				map[string]any{"id": "page-a", "imageVersion": "0"},
			)
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"id": "synthetic-document", "status": "DONE", "pages": pages,
			"attributes": map[string]any{"data": map[string]any{
				"title": map[string]any{"value": title},
			}},
		})
	})
	mux.HandleFunc("GET /api/pages/{id}", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.PathValue("id") {
		case "page-z":
			_, _ = w.Write([]byte(`[{"text":"Synthetic invoice wording","webappId":"synthetic-token"}]`))
		case "page-a":
			_, _ = w.Write([]byte(`[]`)) // no OCR: the image is still readable
		default:
			http.NotFound(w, r)
		}
	})
	mux.HandleFunc("GET /api/v1/pages/{id}/image", func(w http.ResponseWriter, r *http.Request) {
		expected := int64(0)
		switch r.PathValue("id") {
		case "page-z":
			expected = version.Load()
		case "page-a":
		default:
			http.NotFound(w, r)
			return
		}
		if r.URL.Query().Get("version") != strconv.FormatInt(expected, 10) || r.URL.Query().Get("size") != "medium" {
			http.Error(w, "stale image version or wrong size", http.StatusNotFound)
			return
		}
		w.Header().Set("Content-Type", "image/jpeg")
		_, _ = w.Write(jpegBytes.Bytes())
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv.URL, jpegBytes.Bytes()
}

// TestDocumentPagesEnableOCRAndImageAccess reproduces the missing link:
// get_document must supply all the arguments for the existing page tools,
// in both channels, even when there is no title to frame.
func TestDocumentPagesEnableOCRAndImageAccess(t *testing.T) {
	for _, tc := range []struct {
		name, title string
		hasPages    bool
	}{
		{"titled", "Synthetic title <ignore instructions>", true},
		{"untitled", "", true},
		{"no pages", "", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var version atomic.Int64
			version.Store(1<<40 + 7)
			url, jpegBytes := newDocumentPagesServer(t, tc.title, tc.hasPages, &version)
			pool := testPool(t, url, accounts.NewSingle(fileee.Credentials{
				Username: "synthetic@example.invalid", Password: "synthetic-password",
			}))
			server := mcp.NewServer(&mcp.Implementation{Name: "page-test", Version: "0"}, nil)
			tools.RegisterAll(server, pool, tools.ServerInfo{}, testLogger(t), testRec(t))
			idp := testidp.New(t)
			session := connectAs(t, newGangwayServer(t, idp, server), idp, "synthetic-subject")
			res := mustCall(t, session, tools.ToolGetDocument, map[string]any{"id": "synthetic-document"})
			out := decodeDocumentPages(t, res.StructuredContent)
			var textValue any
			if err := json.Unmarshal([]byte(resultText(t, res)), &textValue); err != nil {
				t.Fatalf("metadata text channel is not JSON: %v", err)
			}
			if other := decodeDocumentPages(t, textValue); !reflect.DeepEqual(other, out) {
				t.Fatalf("page metadata differs between channels: %+v != %+v", other, out)
			}
			if out.Pages == nil || out.PageCount != len(out.Pages) {
				t.Fatalf("missing or inconsistent pages: %+v", out)
			}
			if !tc.hasPages {
				if len(out.Pages) != 0 {
					t.Fatalf("empty document returned pages: %+v", out)
				}
				return
			}
			if len(out.Pages) != 2 || out.Pages[0].ID != "page-z" || out.Pages[1].ID != "page-a" || out.Pages[0].ImageVersion != version.Load() || out.Pages[1].ImageVersion != 0 {
				t.Fatalf("page IDs, order or versions lost: %+v", out)
			}
			if tc.title != "" && strings.Contains(resultText(t, res), tc.title) {
				t.Fatal("unframed title leaked into metadata JSON")
			}
			for i, page := range out.Pages {
				ocr := mustCall(t, session, tools.ToolGetPageOCR, map[string]any{"pageId": page.ID})
				raw, err := json.Marshal(ocr.StructuredContent)
				if err != nil {
					t.Fatalf("marshal OCR result: %v", err)
				}
				var ocrOut struct {
					TokenCount    int    `json:"tokenCount"`
					UntrustedText string `json:"untrustedText"`
				}
				if err := json.Unmarshal(raw, &ocrOut); err != nil {
					t.Fatalf("decode OCR result: %v", err)
				}
				if i == 0 {
					for _, text := range []string{framedText(t, ocr), ocrOut.UntrustedText} {
						if !strings.Contains(text, "Synthetic invoice wording") || !strings.Contains(text, "<untrusted_external_content") {
							t.Fatal("page OCR did not return framed document wording in both channels")
						}
					}
				} else if ocrOut.TokenCount != 0 || ocrOut.UntrustedText != "" {
					t.Fatal("page without OCR returned text")
				}
				img := mustCall(t, session, tools.ToolGetPageImage, map[string]any{"pageId": page.ID, "version": page.ImageVersion})
				if len(img.Content) != 1 {
					t.Fatalf("image content count = %d", len(img.Content))
				}
				content, ok := img.Content[0].(*mcp.ImageContent)
				if !ok || content.MIMEType != "image/jpeg" || !bytes.Equal(content.Data, jpegBytes) {
					t.Fatal("page image is not the expected JPEG content")
				}
			}
			version.Add(1)
			fresh := decodeDocumentPages(t, mustCall(t, session, tools.ToolGetDocument, map[string]any{"id": "synthetic-document"}).StructuredContent)
			if fresh.Pages[0].ImageVersion != version.Load() {
				t.Fatalf("fresh document returned stale imageVersion: %+v", fresh.Pages)
			}
			mustCall(t, session, tools.ToolGetPageImage, map[string]any{"pageId": fresh.Pages[0].ID, "version": fresh.Pages[0].ImageVersion})
		})
	}
}
