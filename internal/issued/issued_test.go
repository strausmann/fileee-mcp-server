package issued

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/strausmann/gangway/access"
	"github.com/strausmann/gangway/identity/testidp"
	"github.com/strausmann/gangway/serve"
)

// --- identity-bearing contexts: the real mechanism, not a fabricated one -
//
// The task this file was built from named the helper below
// serve.ContextWithIdentity. No such exported function exists in
// github.com/strausmann/gangway v0.5.0 (verified against the module
// source: serve/serve.go's full exported symbol list carries only
// IdentityFrom and TokenFrom, never a setter; the context key type
// (identityKey{}) is unexported). gangway's own test suite documents why
// deliberately, on TestAuthenticateAllowsValidTokenAndPublishesIdentity's
// doc comment (serve/serve_test.go): "there is no longer a way to attach a
// bare http.HandlerFunc that reads the context directly" — the identity is
// placed into ctx exclusively by the authentication middleware AttachMCP
// installs in front of a real *mcp.Server, and is only ever observable
// from a tool handler that middleware actually invoked.
//
// This repository's own internal/tools/read_generic_test.go documents the
// same constraint from the consuming side and works around it by testing
// below the ctx boundary (calling internal helpers with an
// already-resolved value instead of a context). This package's public API
// is ctx-first by design — Record and Check both take context.Context, so
// Check can be wired straight into a tool handler's own ctx (Task 3
// onward) — so that workaround is not available for testing the public
// surface: something has to actually go through Gangway's real request
// pipeline at least once to produce a ctx serve.IdentityFrom will
// recognise.
//
// ctxMitIdentitaet is that real, minimal round trip: a throw-away Gangway
// server backed by a testidp-issued token, one *mcp.Server tool
// ("capture") whose only job is to hand the ctx it was called with back to
// this helper through a channel, and a real MCP client that calls it
// exactly once. What comes back is a genuine ctx that went through
// Gangway's own authentication and tool-authorization middleware — not a
// hand-rolled stand-in.
//
// Reading Values from the returned context after the request that
// produced it has completed is safe: context cancellation only affects
// Done()/Err(), never values attached before cancellation, and the
// buffered channel below guarantees the send happens before CallTool
// returns to this function.
func ctxMitIdentitaet(t *testing.T, subject string) context.Context {
	t.Helper()

	idp := testidp.New(t)
	captured := make(chan context.Context, 1)

	mcpServer := mcp.NewServer(&mcp.Implementation{Name: "issued-test", Version: "0.0.0"}, nil)
	mcp.AddTool(mcpServer, &mcp.Tool{Name: "capture"},
		func(ctx context.Context, _ *mcp.CallToolRequest, _ captureArgs) (*mcp.CallToolResult, any, error) {
			captured <- ctx
			return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: "ok"}}}, nil, nil
		})

	gwCfg := &serve.Config{
		Addr:            "127.0.0.1:0",
		PublicBaseURL:   "https://issued-test.example.invalid",
		IssuerURL:       idp.URL(),
		Audience:        issuedTestAudience,
		SubjectClaim:    "sub",
		AllowedPrefixes: mustPrefixes(t, "127.0.0.1/32", "::1/128"),
	}
	gw, err := serve.New(context.Background(), gwCfg, serve.WithDecider(access.AllowAll()))
	if err != nil {
		t.Fatalf("ctxMitIdentitaet(%q): serve.New: %v", subject, err)
	}
	gw.AttachMCP(mcpServer)

	srv := httptest.NewServer(gw.Handler())
	t.Cleanup(srv.Close)

	token := idp.Token(map[string]any{
		"iss": idp.URL(), "aud": issuedTestAudience, "sub": subject,
		"exp": time.Now().Add(time.Hour).Unix(),
	})
	client := mcp.NewClient(&mcp.Implementation{Name: "issued-test-client", Version: "0.0.0"}, nil)
	transport := &mcp.StreamableClientTransport{
		Endpoint:             srv.URL + "/mcp",
		HTTPClient:           &http.Client{Transport: bearerRoundTripper{token: token}},
		DisableStandaloneSSE: true,
	}
	session, err := client.Connect(context.Background(), transport, nil)
	if err != nil {
		t.Fatalf("ctxMitIdentitaet(%q): Connect: %v", subject, err)
	}
	t.Cleanup(func() { _ = session.Close() })

	res, err := session.CallTool(context.Background(), &mcp.CallToolParams{Name: "capture"})
	if err != nil {
		t.Fatalf("ctxMitIdentitaet(%q): CallTool(capture): %v", subject, err)
	}
	if res.IsError {
		t.Fatalf("ctxMitIdentitaet(%q): CallTool(capture): IsError = true, content = %+v", subject, res.Content)
	}

	return <-captured
}

// issuedTestAudience is the OIDC audience used throughout this file, both
// when building the Gangway server (serve.Config.Audience) and when
// minting a token (idp.Token's "aud" claim) — they must agree, or
// ctxMitIdentitaet would fail authentication for a reason that has
// nothing to do with what any individual test actually checks (mirrors
// internal/tools/read_test.go's testAudience).
const issuedTestAudience = "fileee-mcp-issued-test"

// captureArgs is the "capture" tool's (empty) argument type — it takes no
// input, it only exists to observe the ctx Gangway's middleware attached
// to the call that reached it.
type captureArgs struct{}

// bearerRoundTripper injects a fixed bearer token into every outgoing
// request — the same pattern internal/tools/read_test.go and
// gangway/serve/serve_test.go both use to drive a real MCP client through
// the streamable HTTP transport as an authenticated caller.
type bearerRoundTripper struct{ token string }

func (rt bearerRoundTripper) RoundTrip(r *http.Request) (*http.Response, error) {
	r = r.Clone(r.Context())
	r.Header.Set("Authorization", "Bearer "+rt.token)
	return http.DefaultTransport.RoundTrip(r)
}

// mustPrefixes parses a fixed CIDR list for cases where a parse failure
// would be a bug in this test file itself, not in the code under test
// (mirrors internal/tools/read_test.go's own mustPrefixes).
func mustPrefixes(t *testing.T, cidrs ...string) []netip.Prefix {
	t.Helper()
	out := make([]netip.Prefix, 0, len(cidrs))
	for _, c := range cidrs {
		p, err := netip.ParsePrefix(c)
		if err != nil {
			t.Fatalf("mustPrefixes(%q): %v", c, err)
		}
		out = append(out, p)
	}
	return out
}

// --- the actual Store tests ------------------------------------------------

func TestRecordUndCheckLassenEineAusgelieferteIDDurch(t *testing.T) {
	s := New(30*time.Minute, 1000)
	ctx := ctxMitIdentitaet(t, "alice")

	s.Record(ctx, "doc-1")

	if err := s.Check(ctx, "doc-1"); err != nil {
		t.Fatalf("Check nach Record: %v, want nil", err)
	}
}

func TestCheckLehntEineNieAusgelieferteIDAb(t *testing.T) {
	s := New(30*time.Minute, 1000)
	ctx := ctxMitIdentitaet(t, "alice")

	err := s.Check(ctx, "doc-unbekannt")

	if !errors.Is(err, ErrNotIssued) {
		t.Fatalf("Check ohne Record: %v, want ErrNotIssued", err)
	}
}

// Das ist die eigentliche Sicherheitszusage des Pakets: was eine Identitaet
// gelesen hat, ist fuer eine andere kein gueltiges Ziel.
func TestIdentitaetenSindGetrennt(t *testing.T) {
	s := New(30*time.Minute, 1000)
	s.Record(ctxMitIdentitaet(t, "alice"), "doc-1")

	err := s.Check(ctxMitIdentitaet(t, "bob"), "doc-1")

	if !errors.Is(err, ErrNotIssued) {
		t.Fatalf("Check fuer fremde Identitaet: %v, want ErrNotIssued", err)
	}
}

func TestOhneGeprueteIdentitaetWirdWederAufgenommenNochFreigegeben(t *testing.T) {
	s := New(30*time.Minute, 1000)
	ohne := context.Background()

	s.Record(ohne, "doc-1") // darf nichts merken

	if err := s.Check(ohne, "doc-1"); err == nil {
		t.Fatal("Check ohne Identitaet: nil, want Fehler (fail-closed)")
	}
	// und die ID darf auch fuer eine echte Identitaet nicht gueltig geworden sein
	if err := s.Check(ctxMitIdentitaet(t, "alice"), "doc-1"); !errors.Is(err, ErrNotIssued) {
		t.Fatalf("Check nach identitaetslosem Record: %v, want ErrNotIssued", err)
	}
}

func TestLeereUndIdentischeIDsWerdenSauberBehandelt(t *testing.T) {
	s := New(30*time.Minute, 1000)
	ctx := ctxMitIdentitaet(t, "alice")

	s.Record(ctx, "", "doc-1", "doc-1") // leer wird ignoriert, doppelt ist kein Fehler

	if err := s.Check(ctx, ""); err == nil {
		t.Fatal("Check mit leerer ID: nil, want Fehler")
	}
	if err := s.Check(ctx, "doc-1"); err != nil {
		t.Fatalf("Check nach doppeltem Record: %v, want nil", err)
	}
}
