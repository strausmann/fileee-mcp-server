package clientpool

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/strausmann/fileee-mcp-server/internal/accounts"
	"github.com/strausmann/gangway/identity"
	"github.com/strausmann/go-fileee/fileee"
)

// --- Fileee mock server -----------------------------------------------
//
// Mirrors the httptest.Server pattern go-fileee's own tests use
// (fileee/mockserver_test.go, fileee/service_test.go) — the wire format
// EnsureSession's full-login path drives is reverse-engineered API, not
// something this package should reinvent.

type mockRoute struct {
	status  int
	body    []byte
	cookies []*http.Cookie
}

// mockHandler routes "METHOD PATH" against routes. A route not in the
// table answers 404 with a generic ApiError body, so a missing fixture
// fails loudly instead of silently passing a test it never actually drove.
func mockHandler(t *testing.T, routes map[string]mockRoute) http.HandlerFunc {
	t.Helper()
	return func(w http.ResponseWriter, r *http.Request) {
		route, ok := routes[r.Method+" "+r.URL.Path]
		if !ok {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write([]byte(`{"apiError":"route_not_mocked","errorMessage":"no mock route for ` + r.Method + " " + r.URL.Path + `"}`))
			return
		}
		for _, c := range route.cookies {
			http.SetCookie(w, c)
		}
		if len(route.body) > 0 {
			w.Header().Set("Content-Type", "application/json")
		}
		w.WriteHeader(route.status)
		if len(route.body) > 0 {
			_, _ = w.Write(route.body)
		}
	}
}

// newTestServer starts an httptest.Server for handler and returns its URL;
// it is closed automatically at the end of the test.
func newTestServer(t *testing.T, handler http.Handler) string {
	t.Helper()
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)
	return srv.URL
}

// loginRoutes is the fixed sequence EnsureSession's full-login path drives
// when there is no stored session and no rememberMe cookie: GET
// /api/f/start for the CSRF cookie, POST /api/f/existent to check the
// account, POST /api/f/login to authenticate. Plus GET /api/f/user-session,
// the lightweight probe RefreshSession/keepalive uses once a session
// already exists.
func loginRoutes() map[string]mockRoute {
	return map[string]mockRoute{
		"GET /api/f/start":     {status: 204},
		"POST /api/f/existent": {status: 200, body: []byte(`{"existent":true,"twoFactorAuthEnabled":false}`)},
		"POST /api/f/login": {
			status:  200,
			body:    []byte(`{"loggedIn":true}`),
			cookies: []*http.Cookie{{Name: "JSESSIONID", Value: "sess-clientpool"}},
		},
		"GET /api/f/user-session": {status: 200, body: []byte(`{"authorized":true,"secondsBlocked":0}`)},
	}
}

// flakyThenWorkingHandler answers the account-existence check with "does
// not exist" for the first failFirstN attempts, then behaves exactly like
// loginRoutes for every attempt after that — used to prove a failed login
// is not cached and a later retry genuinely tries again against the very
// same server, rather than replaying a stashed failure. The returned
// counter lets a test assert on the actual number of existence-check
// attempts, since a nil error alone would not distinguish "retried and
// succeeded" from "served a stale, never-actually-logged-in client from
// the cache".
func flakyThenWorkingHandler(t *testing.T, failFirstN int) (http.HandlerFunc, *atomic.Int32) {
	t.Helper()
	var existentAttempts atomic.Int32
	good := loginRoutes()
	handler := func(w http.ResponseWriter, r *http.Request) {
		if r.Method+" "+r.URL.Path == "POST /api/f/existent" && int(existentAttempts.Add(1)) <= failFirstN {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(200)
			_, _ = w.Write([]byte(`{"existent":false,"twoFactorAuthEnabled":false}`))
			return
		}
		mockHandler(t, good)(w, r)
	}
	return handler, &existentAttempts
}

// countingUserSessionHandler behaves like loginRoutes but additionally
// counts every GET /api/f/user-session request — the request the
// keepalive-driven RefreshSession makes once a session exists.
func countingUserSessionHandler(t *testing.T, pings *atomic.Int32) http.HandlerFunc {
	t.Helper()
	routes := loginRoutes()
	inner := mockHandler(t, routes)
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method+" "+r.URL.Path == "GET /api/f/user-session" {
			pings.Add(1)
		}
		inner(w, r)
	}
}

// --- accounts.Resolver test double --------------------------------------

// countingResolver is a minimal accounts.Resolver: it resolves a fixed
// Credentials per subject and counts how many times Credentials was
// actually invoked, so a test can assert on deduplication directly instead
// of inferring it from timing or request counts on the mock server.
type countingResolver struct {
	mu    sync.Mutex
	byID  map[string]fileee.Credentials
	calls atomic.Int32
}

func newCountingResolver(byID map[string]fileee.Credentials) *countingResolver {
	if byID == nil {
		byID = map[string]fileee.Credentials{}
	}
	return &countingResolver{byID: byID}
}

func (r *countingResolver) Credentials(_ context.Context, id *identity.Identity) (fileee.Credentials, error) {
	r.calls.Add(1)
	if id == nil {
		return fileee.Credentials{}, fmt.Errorf("clientpool test: nil identity: %w", accounts.ErrNoAccount)
	}
	r.mu.Lock()
	creds, ok := r.byID[id.Subject]
	r.mu.Unlock()
	if !ok {
		return fileee.Credentials{}, fmt.Errorf("clientpool test: unknown subject %q: %w", id.Subject, accounts.ErrNoAccount)
	}
	return creds, nil
}

// learn adds or overwrites subject's credentials, safe for concurrent use.
func (r *countingResolver) learn(subject string, creds fileee.Credentials) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.byID[subject] = creds
}

// --- test helper: a Pool wired against a mock server, with a session
// store scoped per test so no test ever touches go-fileee's real on-disk
// default (an OS-global path that every test in this package, all run
// with t.Parallel(), would otherwise race on).

func testPool(t *testing.T, srv string, r accounts.Resolver, extra ...Option) *Pool {
	t.Helper()
	dir := t.TempDir()
	opts := append([]Option{
		WithClientOptions(fileee.WithBaseURL(srv), fileee.WithRateLimit(1000, 1000)),
		WithSessionStore(func(accountKey string) fileee.SessionStore {
			return fileee.NewFileSessionStore(filepath.Join(dir, accountKey+".json"))
		}),
	}, extra...)
	return New(r, opts...)
}

// --- tests required by the plan -----------------------------------------

func TestSameCallerGetsTheSameClient(t *testing.T) {
	t.Parallel()

	srv := newTestServer(t, mockHandler(t, loginRoutes()))
	r := newCountingResolver(map[string]fileee.Credentials{
		"alice": {Username: "alice@example.invalid", Password: "pw"},
	})
	p := testPool(t, srv, r)
	id := &identity.Identity{Subject: "alice"}

	first, err := p.For(context.Background(), id)
	if err != nil {
		t.Fatalf("For: %v", err)
	}
	second, err := p.For(context.Background(), id)
	if err != nil {
		t.Fatalf("For: %v", err)
	}
	if first != second {
		t.Error("pool logged in twice for the same caller")
	}
}

func TestConcurrentFirstUseLogsInOnce(t *testing.T) {
	t.Parallel()

	srv := newTestServer(t, mockHandler(t, loginRoutes()))
	r := newCountingResolver(map[string]fileee.Credentials{
		"alice": {Username: "alice@example.invalid", Password: "pw"},
	})
	p := testPool(t, srv, r)
	id := &identity.Identity{Subject: "alice"}

	var wg sync.WaitGroup
	for range 50 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, _ = p.For(context.Background(), id)
		}()
	}
	wg.Wait()

	// Fifty simultaneous first calls must not become fifty logins.
	if n := r.calls.Load(); n != 1 {
		t.Errorf("resolved credentials %d times, want exactly 1", n)
	}
}

func TestDifferentCallersGetDifferentClients(t *testing.T) {
	t.Parallel()

	srv := newTestServer(t, mockHandler(t, loginRoutes()))
	r := newCountingResolver(map[string]fileee.Credentials{
		"alice": {Username: "alice@example.invalid", Password: "pw-a"},
		"bob":   {Username: "bob@example.invalid", Password: "pw-b"},
	})
	p := testPool(t, srv, r)

	a, err := p.For(context.Background(), &identity.Identity{Subject: "alice"})
	if err != nil {
		t.Fatalf("For(alice): %v", err)
	}
	b, err := p.For(context.Background(), &identity.Identity{Subject: "bob"})
	if err != nil {
		t.Fatalf("For(bob): %v", err)
	}

	if a == b {
		t.Error("two callers shared one client — one would see the other's documents")
	}
}

// --- design decision: pooled by resolved account, not by subject --------

// TestTwoSubjectsOnTheSameAccountShareOneClientAndOneLogin is the
// consequence of ADR-0012 point 6 ("ein Client je Konto") and its
// "Konsequenzen/Positiv" note that single mode must be "derselbe Codepfad
// mit genau einem Eintrag": when several different callers resolve to the
// very same Fileee account (accounts.NewSingle does exactly that for every
// subject gangway lets through), the pool must not open a second session
// for the second caller. Pooling by id.Subject alone — the simpler
// alternative — would fail this test, because two different subjects are
// two different subject keys.
func TestTwoSubjectsOnTheSameAccountShareOneClientAndOneLogin(t *testing.T) {
	t.Parallel()

	srv := newTestServer(t, mockHandler(t, loginRoutes()))
	shared := fileee.Credentials{Username: "team@example.invalid", Password: "pw"}
	r := newCountingResolver(map[string]fileee.Credentials{
		"alice": shared,
		"carol": shared,
	})
	p := testPool(t, srv, r)

	a, err := p.For(context.Background(), &identity.Identity{Subject: "alice"})
	if err != nil {
		t.Fatalf("For(alice): %v", err)
	}
	c, err := p.For(context.Background(), &identity.Identity{Subject: "carol"})
	if err != nil {
		t.Fatalf("For(carol): %v", err)
	}
	if a != c {
		t.Error("two subjects on the same account got two clients — the account was logged into twice")
	}
}

// TestConcurrentFirstUseOfSharedAccountLogsInOnce is the concurrent variant
// of the above: two DIFFERENT subjects, same account, racing on their very
// first call. This is the scenario ADR-0012 point 6 names directly — "zwei
// gleichzeitige Logins gegen dasselbe Konto sind genau das Muster, das
// serverseitige Konto-Sperren (secondsBlocked) auslöst". Deduplication
// keyed only by id.Subject (as required for
// TestConcurrentFirstUseLogsInOnce above) would not catch this: it only
// groups calls that share the very same subject.
func TestConcurrentFirstUseOfSharedAccountLogsInOnce(t *testing.T) {
	t.Parallel()

	srv := newTestServer(t, mockHandler(t, loginRoutes()))
	shared := fileee.Credentials{Username: "team@example.invalid", Password: "pw"}
	r := newCountingResolver(map[string]fileee.Credentials{
		"alice": shared,
		"carol": shared,
	})
	p := testPool(t, srv, r)

	subjects := []string{"alice", "carol"}
	var wg sync.WaitGroup
	results := make([]*fileee.Client, 50)
	for i := range 50 {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			id := &identity.Identity{Subject: subjects[i%2]}
			c, err := p.For(context.Background(), id)
			if err == nil {
				results[i] = c
			}
		}(i)
	}
	wg.Wait()

	first := results[0]
	if first == nil {
		t.Fatal("first call returned no client")
	}
	for i, c := range results {
		if c != first {
			t.Errorf("result %d got a different client — the shared account was logged into more than once", i)
		}
	}
}

// --- design decision: a failed login is never cached ---------------------

// TestFailedLoginIsNotCachedAndCanBeRetried is the answer to "what happens
// on a failed login": neither a resolver rejection nor a failed
// EnsureSession may permanently poison an account for the life of the
// process — a transient failure must not lock a caller out until restart.
// The mock account fails the first existence check and succeeds every
// attempt after that, against the very same server and the very same Pool.
//
// A nil error on the second call alone would not be enough to prove the
// retry is real: a version that quietly cached the client from the failed
// first attempt would also return that stale client with a nil error on
// the fast cache-hit path — this test additionally asserts on the mock
// server's own attempt count to catch exactly that.
func TestFailedLoginIsNotCachedAndCanBeRetried(t *testing.T) {
	t.Parallel()

	handler, existentAttempts := flakyThenWorkingHandler(t, 1)
	srv := newTestServer(t, handler)
	r := newCountingResolver(map[string]fileee.Credentials{
		"alice": {Username: "ghost@example.invalid", Password: "pw"},
	})
	p := testPool(t, srv, r)
	id := &identity.Identity{Subject: "alice"}

	if _, err := p.For(context.Background(), id); err == nil {
		t.Fatal("For: want an error for a nonexistent account, got nil")
	}
	if _, err := p.For(context.Background(), id); err != nil {
		t.Fatalf("For after retry: %v, want the second attempt to succeed", err)
	}
	if n := existentAttempts.Load(); n != 2 {
		t.Errorf("mock server saw %d existence checks, want exactly 2 — the second For call "+
			"must have genuinely retried the login, not served a client cached from the first, failed attempt", n)
	}
}

// TestNoAccountIsNeverCached belongs to the same guarantee, one layer up:
// accounts.ErrNoAccount from the resolver is not a client-building failure
// at all, but it must be just as retryable — a resolver whose answer can
// change between calls (e.g. hot-reloaded configuration) must not be stuck
// on its first answer.
func TestNoAccountIsNeverCached(t *testing.T) {
	t.Parallel()

	srv := newTestServer(t, mockHandler(t, loginRoutes()))
	r := newCountingResolver(nil)
	p := testPool(t, srv, r)
	id := &identity.Identity{Subject: "alice"}

	if _, err := p.For(context.Background(), id); !errors.Is(err, accounts.ErrNoAccount) {
		t.Fatalf("err = %v, want ErrNoAccount", err)
	}

	r.learn("alice", fileee.Credentials{Username: "alice@example.invalid", Password: "pw"})
	if _, err := p.For(context.Background(), id); err != nil {
		t.Fatalf("For after the resolver learned the account: %v, want success", err)
	}
}

// --- guard: no verified identity ----------------------------------------

func TestForRefusesANilIdentity(t *testing.T) {
	t.Parallel()

	p := New(newCountingResolver(nil))
	if _, err := p.For(context.Background(), nil); err == nil {
		t.Fatal("For(nil): want an error, got nil")
	}
}

func TestForRefusesAnEmptySubject(t *testing.T) {
	t.Parallel()

	p := New(newCountingResolver(nil))
	if _, err := p.For(context.Background(), &identity.Identity{Subject: ""}); err == nil {
		t.Fatal("For(empty subject): want an error, got nil")
	}
}

// alwaysSameAccountResolver returns the same fixed credentials for every
// call, regardless of id — including a nil id or an empty Subject. It
// models accounts.NewSingle (every subject gangway lets through maps to
// the one configured account, ADR-0012 point 4/6), and is the missing
// piece a review found: newCountingResolver, used in the two tests above,
// already refuses a nil identity and an empty subject on its own — so a
// test built against it can never prove that Pool.For's own guard is
// doing anything. Remove that guard, and TestForRefusesANilIdentity /
// TestForRefusesAnEmptySubject stay green regardless, because the
// resolver would have refused first either way. With
// alwaysSameAccountResolver, which never refuses anything on its own,
// Pool.For's guard is the ONLY thing standing between an empty identity
// and the shared account's client.
type alwaysSameAccountResolver struct {
	creds fileee.Credentials
}

func (r alwaysSameAccountResolver) Credentials(_ context.Context, _ *identity.Identity) (fileee.Credentials, error) {
	return r.creds, nil
}

// Both tests below run against a real mock server whose login always
// succeeds (loginRoutes, via testPool) — not against no server at all.
// A first version of these tests built the Pool with New(r) directly, no
// base URL configured, so a missing guard sent the (fake) credentials
// against the real https://my.fileee.com and failed on the network instead
// — "err == nil" was still false, so the test stayed green whether or not
// Pool.For's guard existed. Exactly the class of flaw the reviewer found
// in TestFailedLoginIsNotCachedAndCanBeRetried: an assertion that a
// coincidental failure elsewhere can also satisfy. With a mock server that
// answers success, removing the guard makes the call genuinely succeed —
// which is the only way an "err == nil" assertion actually proves the
// guard did something.

func TestForRefusesANilIdentityEvenWhenTheResolverWouldAccept(t *testing.T) {
	t.Parallel()

	srv := newTestServer(t, mockHandler(t, loginRoutes()))
	r := alwaysSameAccountResolver{creds: fileee.Credentials{Username: "team@example.invalid", Password: "pw"}}
	p := testPool(t, srv, r)

	if _, err := p.For(context.Background(), nil); err == nil {
		t.Fatal("For(nil) against a resolver and server that would happily serve it: want an error, got nil")
	}
}

func TestForRefusesAnEmptySubjectEvenWhenTheResolverWouldAccept(t *testing.T) {
	t.Parallel()

	srv := newTestServer(t, mockHandler(t, loginRoutes()))
	r := alwaysSameAccountResolver{creds: fileee.Credentials{Username: "team@example.invalid", Password: "pw"}}
	p := testPool(t, srv, r)

	if _, err := p.For(context.Background(), &identity.Identity{Subject: ""}); err == nil {
		t.Fatal("For(empty subject) against a resolver and server that would happily serve it: want an error, got nil")
	}
}

// --- lifecycle: keepalive goroutines can be shut down --------------------

// TestCloseStopsKeepalive is the answer to "does the pool hold sessions
// warm, and how are the goroutines that would do so shut down": a Pool
// holding every account's session warm via StartKeepAlive accumulates one
// background goroutine per account, and there must be a way to stop them —
// otherwise a Pool that outlives many accounts leaks one goroutine per
// account for the life of the process. This drives a short keepalive
// interval against a mock counting GET /api/f/user-session (the request
// RefreshSession makes once a session exists), calls Close, and asserts
// the count stops climbing.
//
// The tolerance built into this test is deliberate, not sloppiness: stop
// (go-fileee's StartKeepAlive, keepalive.go) cancels a context that the
// keepalive goroutine's select checks alongside the ticker channel —
// cancellation and an already-queued tick can be simultaneously ready, and
// select picks between ready cases at random, so at most one tick that had
// already fired before Close can still complete afterwards. Asserting a
// hard zero here would make the test flaky on a property clientpool does
// not control and go-fileee's own stop function does not promise; verified
// empirically — an earlier, stricter version of this test (exact equality)
// failed intermittently for exactly one extra ping.
func TestCloseStopsKeepalive(t *testing.T) {
	t.Parallel()

	var pings atomic.Int32
	srv := newTestServer(t, countingUserSessionHandler(t, &pings))
	r := newCountingResolver(map[string]fileee.Credentials{
		"alice": {Username: "alice@example.invalid", Password: "pw"},
	})
	const interval = 5 * time.Millisecond
	p := testPool(t, srv, r, WithKeepalive(interval))

	if _, err := p.For(context.Background(), &identity.Identity{Subject: "alice"}); err != nil {
		t.Fatalf("For: %v", err)
	}

	// Let several keepalive ticks land.
	time.Sleep(20 * interval)
	p.Close()
	justAfterClose := pings.Load()
	if justAfterClose == 0 {
		t.Fatal("keepalive never pinged the mock server — the test setup, not just Close, would be unverified")
	}

	// A short grace window for at most one tick that was already queued
	// the instant Close ran (see the doc comment above).
	time.Sleep(4 * interval)
	settled := pings.Load()
	if settled-justAfterClose > 1 {
		t.Errorf("more than one straggler ping after Close: %d before, %d after the grace window",
			justAfterClose, settled)
	}

	// A much longer window: if the goroutine were still running, an
	// interval this many multiples long would have produced several more
	// pings, not stayed exactly flat.
	time.Sleep(20 * interval)
	if got := pings.Load(); got != settled {
		t.Errorf("keepalive kept pinging long after Close: %d pings after the grace window, %d after the long wait",
			settled, got)
	}
}

func TestCloseWithoutKeepaliveIsANoop(t *testing.T) {
	t.Parallel()

	p := New(newCountingResolver(nil))
	p.Close() // must not panic on an empty pool
}

func TestCloseIsIdempotent(t *testing.T) {
	t.Parallel()

	srv := newTestServer(t, mockHandler(t, loginRoutes()))
	r := newCountingResolver(map[string]fileee.Credentials{
		"alice": {Username: "alice@example.invalid", Password: "pw"},
	})
	p := testPool(t, srv, r, WithKeepalive(5*time.Millisecond))

	if _, err := p.For(context.Background(), &identity.Identity{Subject: "alice"}); err != nil {
		t.Fatalf("For: %v", err)
	}
	p.Close()
	p.Close() // must not panic or double-stop
}

// --- network error path (mutation-path coverage: happy / error / network) --

// TestForNetworkErrorIsNotCached exercises the third mutation-path leg
// (network failure) test-coverage-pflicht.md requires for this file's
// category (Mutations-Logik) — an unreachable server must surface as an
// ordinary error, not a panic, and must not be cached any more than a
// backend-reported failure is.
func TestForNetworkErrorIsNotCached(t *testing.T) {
	t.Parallel()

	r := newCountingResolver(map[string]fileee.Credentials{
		"alice": {Username: "alice@example.invalid", Password: "pw"},
	})
	// Port 1 is reserved; nothing answers there, so the connection is
	// refused immediately instead of hanging until a timeout.
	p := testPool(t, "http://127.0.0.1:1", r)
	id := &identity.Identity{Subject: "alice"}

	if _, err := p.For(context.Background(), id); err == nil {
		t.Fatal("For against an unreachable server: want an error, got nil")
	}
}

// TestForRefusesAResolvedAccountWithNoUsername exercises accountKey's own
// guard through the public For entry point: a resolver that returns no
// error but an empty Username can only be a broken accounts.Resolver
// (config.LoadConfig itself never allows one through), and must be refused
// rather than silently pooled under a blank key — see the Pool doc on
// accountKey.
func TestForRefusesAResolvedAccountWithNoUsername(t *testing.T) {
	t.Parallel()

	r := newCountingResolver(map[string]fileee.Credentials{
		"alice": {Username: "", Password: "pw"},
	})
	p := New(r)

	if _, err := p.For(context.Background(), &identity.Identity{Subject: "alice"}); err == nil {
		t.Fatal("For with an empty resolved Username: want an error, got nil")
	}
}

// TestForSurfacesAFileeeNewValidationError covers the other half of
// fileee.New's own validation (accountKey only guards Username, not
// Password) — a resolved account missing its Password must surface
// fileee.New's own error through For, not a panic or a silently built,
// half-valid client.
func TestForSurfacesAFileeeNewValidationError(t *testing.T) {
	t.Parallel()

	r := newCountingResolver(map[string]fileee.Credentials{
		"alice": {Username: "alice@example.invalid", Password: ""},
	})
	p := New(r)

	if _, err := p.For(context.Background(), &identity.Identity{Subject: "alice"}); err == nil {
		t.Fatal("For with an empty resolved Password: want an error, got nil")
	}
}

// --- white-box tests for corners the public API rarely exercises ---------
//
// These call unexported helpers directly (this file is package clientpool,
// not clientpool_test) — the two branches below are real, deliberate
// guards, but reaching them through For alone would depend on winning a
// timing race that a test cannot reliably force.

// TestAccountKeyRefusesAnEmptyUsername is accountKey's own unit test, for
// the same reason TestForRefusesAResolvedAccountWithNoUsername exists one
// layer up: both describe the same guard, from two different entry points.
func TestAccountKeyRefusesAnEmptyUsername(t *testing.T) {
	t.Parallel()

	if _, err := accountKey(fileee.Credentials{}); err == nil {
		t.Fatal("accountKey with an empty Username: want an error, got nil")
	}
}

// TestBuildAndLoginReusesAnAlreadyCachedClientWithoutLoggingInAgain proves
// buildAndLogin's own re-check-the-cache-first guard (see its doc comment):
// byAccount.Do only prevents concurrent duplicate execution for a key, it
// does not guarantee the cache was still empty by the time a particular
// call actually started running. This test skips straight to that
// situation by seeding the cache directly, then calling buildAndLogin with
// credentials that would fail immediately if actually attempted — proving
// the fast path returned the cached client without ever trying to log in.
func TestBuildAndLoginReusesAnAlreadyCachedClientWithoutLoggingInAgain(t *testing.T) {
	t.Parallel()

	p := New(newCountingResolver(nil), WithClientOptions(fileee.WithBaseURL("http://127.0.0.1:1")))
	want := &fileee.Client{}
	p.clients["team@example.invalid"] = &pooledClient{client: want, stop: func() {}}

	got, err := p.buildAndLogin(context.Background(), "team@example.invalid", fileee.Credentials{
		Username: "team@example.invalid", Password: "pw",
	})
	if err != nil {
		t.Fatalf("buildAndLogin: %v, want the cached client without attempting a login", err)
	}
	if got != want {
		t.Error("buildAndLogin did not return the already-cached client")
	}
}

// TestBuildAndLoginStopsKeepaliveIfPoolClosedDuringLogin covers the other
// hard-to-force race in buildAndLogin: Close running while a login for a
// brand new account is still in flight. That entry is never in the map for
// Close to find and stop, so buildAndLogin must stop it itself once the
// login finishes — otherwise its keepalive goroutine would outlive Close
// for the rest of the process. Simulated directly by marking the pool
// closed before calling buildAndLogin, since forcing Close to genuinely
// interleave with an in-flight login is not something a test can reliably
// win a race to produce.
func TestBuildAndLoginStopsKeepaliveIfPoolClosedDuringLogin(t *testing.T) {
	t.Parallel()

	var pings atomic.Int32
	srv := newTestServer(t, countingUserSessionHandler(t, &pings))
	dir := t.TempDir()
	p := New(newCountingResolver(nil),
		WithClientOptions(fileee.WithBaseURL(srv), fileee.WithRateLimit(1000, 1000)),
		WithSessionStore(func(accountKey string) fileee.SessionStore {
			return fileee.NewFileSessionStore(filepath.Join(dir, accountKey+".json"))
		}),
		WithKeepalive(5*time.Millisecond),
	)
	p.closed = true

	if _, err := p.buildAndLogin(context.Background(), "alice@example.invalid", fileee.Credentials{
		Username: "alice@example.invalid", Password: "pw",
	}); err != nil {
		t.Fatalf("buildAndLogin: %v", err)
	}

	time.Sleep(40 * time.Millisecond)
	if got := pings.Load(); got != 0 {
		t.Errorf("keepalive pinged %d times after a login that started with the pool already closed, want 0", got)
	}
}

// --- ResolveAccountKey / ProbeLogin (Aufgabe C3, self_check) ---------------
//
// self_check must not reuse For's own coupling of reachability and login
// (buildAndLogin calls EnsureSession immediately on a cache miss and wraps
// whatever comes back into one generic "resolve fileee client" failure,
// see clientFor's own doc comment in internal/tools/read.go) — a
// diagnostic tool needs to tell "wrong password" apart from "network
// down", and needs to check RIGHT NOW rather than answer from a client
// For already has cached and warm. These two methods are that separate,
// uncached path: ResolveAccountKey is the cheap half (which account would
// this identity check?), ProbeLogin is the one that actually attempts a
// login, every time, against a throwaway client that never touches disk.

// TestResolveAccountKeyNeverAttemptsANetworkCall proves the "cheap half"
// claim directly: the base URL points at a closed local port, so any
// attempt to actually reach Fileee would fail immediately — a passing
// call therefore proves ResolveAccountKey never tried.
func TestResolveAccountKeyNeverAttemptsANetworkCall(t *testing.T) {
	t.Parallel()

	r := newCountingResolver(map[string]fileee.Credentials{
		"alice": {Username: "alice@example.invalid", Password: "pw"},
	})
	p := New(r, WithClientOptions(fileee.WithBaseURL("http://127.0.0.1:1")))

	key, err := p.ResolveAccountKey(context.Background(), &identity.Identity{Subject: "alice"})
	if err != nil {
		t.Fatalf("ResolveAccountKey: %v", err)
	}
	if key != "alice@example.invalid" {
		t.Errorf("key = %q, want %q", key, "alice@example.invalid")
	}
}

func TestResolveAccountKeyRefusesANilIdentity(t *testing.T) {
	t.Parallel()

	p := New(newCountingResolver(nil))
	if _, err := p.ResolveAccountKey(context.Background(), nil); err == nil {
		t.Fatal("ResolveAccountKey with a nil identity: want an error, got nil")
	}
}

func TestResolveAccountKeySurfacesANoAccountResolverError(t *testing.T) {
	t.Parallel()

	p := New(newCountingResolver(nil))
	if _, err := p.ResolveAccountKey(context.Background(), &identity.Identity{Subject: "ghost"}); !errors.Is(err, accounts.ErrNoAccount) {
		t.Errorf("err = %v, want errors.Is(err, accounts.ErrNoAccount)", err)
	}
}

// TestProbeLoginDistinguishesInvalidCredentialsFromUnreachable is the
// live check this task's own grounding document flagged as unverified —
// whether fileee.ErrInvalidCredentials genuinely survives as something
// errors.Is can still see once it comes back through ProbeLogin, checked
// against a real (mocked) server exchange rather than assumed from
// reading go-fileee's source. The mock account answers "does not exist"
// on POST /api/f/existent (go-fileee's fileee/auth.go returns
// ErrInvalidCredentials directly for that response, before any login
// attempt is even sent) — chosen over a 401/403 on /api/f/login because
// it needs one fewer mock route to reach the same sentinel.
func TestProbeLoginDistinguishesInvalidCredentialsFromUnreachable(t *testing.T) {
	t.Parallel()

	routes := map[string]mockRoute{
		"GET /api/f/start":     {status: 204},
		"POST /api/f/existent": {status: 200, body: []byte(`{"existent":false,"twoFactorAuthEnabled":false}`)},
	}
	srv := newTestServer(t, mockHandler(t, routes))
	r := newCountingResolver(map[string]fileee.Credentials{
		"alice": {Username: "ghost@example.invalid", Password: "pw"},
	})
	p := New(r, WithClientOptions(fileee.WithBaseURL(srv), fileee.WithRateLimit(1000, 1000)))

	err := p.ProbeLogin(context.Background(), &identity.Identity{Subject: "alice"})
	if !errors.Is(err, fileee.ErrInvalidCredentials) {
		t.Errorf("err = %v, want errors.Is(err, fileee.ErrInvalidCredentials)", err)
	}
}

// TestProbeLoginDistinguishesTwoFactorInvalidFromUnreachable is the same
// live check for the second sentinel this task names: an account that
// requires 2FA but has no configured TOTP seed fails BEFORE any request
// to POST /api/f/login (go-fileee's own fail-fast guard, fileee/auth.go)
// — one mock route fewer again, same reasoning as above.
func TestProbeLoginDistinguishesTwoFactorInvalidFromUnreachable(t *testing.T) {
	t.Parallel()

	routes := map[string]mockRoute{
		"GET /api/f/start":     {status: 204},
		"POST /api/f/existent": {status: 200, body: []byte(`{"existent":true,"twoFactorAuthEnabled":true}`)},
	}
	srv := newTestServer(t, mockHandler(t, routes))
	r := newCountingResolver(map[string]fileee.Credentials{
		"alice": {Username: "alice@example.invalid", Password: "pw"}, // kein TOTPSeed
	})
	p := New(r, WithClientOptions(fileee.WithBaseURL(srv), fileee.WithRateLimit(1000, 1000)))

	err := p.ProbeLogin(context.Background(), &identity.Identity{Subject: "alice"})
	if !errors.Is(err, fileee.ErrTwoFactorInvalid) {
		t.Errorf("err = %v, want errors.Is(err, fileee.ErrTwoFactorInvalid)", err)
	}
}

// TestProbeLoginSucceedsAgainstAWorkingAccount is ProbeLogin's own happy
// path — belongs here as much as the two failure cases, since a self-check
// tool that only ever gets exercised on its failure branches is only half
// tested.
func TestProbeLoginSucceedsAgainstAWorkingAccount(t *testing.T) {
	t.Parallel()

	srv := newTestServer(t, mockHandler(t, loginRoutes()))
	r := newCountingResolver(map[string]fileee.Credentials{
		"alice": {Username: "alice@example.invalid", Password: "pw"},
	})
	p := New(r, WithClientOptions(fileee.WithBaseURL(srv), fileee.WithRateLimit(1000, 1000)))

	if err := p.ProbeLogin(context.Background(), &identity.Identity{Subject: "alice"}); err != nil {
		t.Errorf("ProbeLogin: %v, want nil", err)
	}
}

// TestProbeLoginReportsANetworkFailureAsNeitherSentinel is the third of
// self_check's three states: nothing at the far end at all (connection
// refused) must classify as neither ErrInvalidCredentials nor
// ErrTwoFactorInvalid — the "unreachable" bucket self_check's own
// classifier (internal/tools/ops.go) falls back to.
func TestProbeLoginReportsANetworkFailureAsNeitherSentinel(t *testing.T) {
	t.Parallel()

	r := newCountingResolver(map[string]fileee.Credentials{
		"alice": {Username: "alice@example.invalid", Password: "pw"},
	})
	p := New(r, WithClientOptions(fileee.WithBaseURL("http://127.0.0.1:1"), fileee.WithRateLimit(1000, 1000)))

	err := p.ProbeLogin(context.Background(), &identity.Identity{Subject: "alice"})
	if err == nil {
		t.Fatal("ProbeLogin against a closed port: want an error, got nil")
	}
	if errors.Is(err, fileee.ErrInvalidCredentials) || errors.Is(err, fileee.ErrTwoFactorInvalid) {
		t.Errorf("err = %v, want neither sentinel for an unreachable server", err)
	}
}

// TestProbeLoginNeverPersistsASession proves ProbeLogin's own throwaway
// client never touches disk, even when the pool it belongs to WOULD
// persist real sessions there — a probe login is discarded the moment
// this call returns and has no business writing a cookie file for an
// account it may not even own for long (id.Subject could resolve to a
// different account on a later call in multi mode).
func TestProbeLoginNeverPersistsASession(t *testing.T) {
	t.Parallel()

	srv := newTestServer(t, mockHandler(t, loginRoutes()))
	dir := t.TempDir()
	r := newCountingResolver(map[string]fileee.Credentials{
		"alice": {Username: "alice@example.invalid", Password: "pw"},
	})
	p := New(r,
		WithClientOptions(fileee.WithBaseURL(srv), fileee.WithRateLimit(1000, 1000)),
		WithSessionStore(func(accountKey string) fileee.SessionStore {
			return fileee.NewFileSessionStore(filepath.Join(dir, accountKey+".json"))
		}),
	)

	if err := p.ProbeLogin(context.Background(), &identity.Identity{Subject: "alice"}); err != nil {
		t.Fatalf("ProbeLogin: %v", err)
	}

	entries, err := filepath.Glob(filepath.Join(dir, "*.json"))
	if err != nil {
		t.Fatalf("Glob: %v", err)
	}
	if len(entries) != 0 {
		t.Errorf("session directory has %d file(s) after ProbeLogin, want 0: %v", len(entries), entries)
	}
}

// concurrencyTrackingHandler wraps handler and tracks the maximum number
// of requests it was serving AT THE SAME TIME across the whole test — used
// by TestProbeLoginUndBuildAndLoginLaufenNieGleichzeitigFuerDasselbeKonto to
// prove that two real login handshakes for the same account never overlap.
// A short sleep on POST /api/f/existent (the first request either
// EnsureSession's or Login's handshake makes, see loginRoutes) widens the
// race window a real concurrency bug would need to hit — without it, two
// genuinely racing goroutines could still happen to run their few, fast
// HTTP round trips one after another by pure scheduling luck, and the test
// would pass for the wrong reason.
func concurrencyTrackingHandler(t *testing.T, routes map[string]mockRoute) (http.HandlerFunc, *atomic.Int32) {
	t.Helper()
	inner := mockHandler(t, routes)
	var inFlight atomic.Int32
	var peak atomic.Int32
	return func(w http.ResponseWriter, r *http.Request) {
		n := inFlight.Add(1)
		for {
			cur := peak.Load()
			if n <= cur || peak.CompareAndSwap(cur, n) {
				break
			}
		}
		if r.Method+" "+r.URL.Path == "POST /api/f/existent" {
			time.Sleep(20 * time.Millisecond)
		}
		inner(w, r)
		inFlight.Add(-1)
	}, &peak
}

// TestProbeLoginUndBuildAndLoginLaufenNieGleichzeitigFuerDasselbeKonto
// deckt einen Fund aus der Gegenpruefung ab (2026-08-13): ProbeLogin ging
// -- absichtlich, siehe seine eigene Doku -- nie durch p.byAccount, aber
// das nahm damit auch die MUTUAL-EXCLUSION-Eigenschaft mit, die
// byAccount fuer buildAndLogin sicherstellt. Ein Pool.For-Aufruf (echter
// Cache-Fehlschlag) und ein self_check-ProbeLogin fuer DASSELBE Konto
// konnten dadurch gleichzeitig echte Logins ausloesen -- exakt das
// Muster, das ADR-0012 Punkt 6 als Ausloeser fuer Fileee's eigene
// Kontosperre nennt, und exakt das Risiko, wegen dem self_check sich
// selbst begrenzt. loginLock schliesst diese Luecke: beide Pfade
// serialisieren jetzt gegeneinander, nicht nur jeder fuer sich (siehe
// TestSelfCheckResultForBegrenztSichSelbstUnterNebenlaeufigkeit in
// internal/tools/ops_test.go fuer die "gegen sich selbst"-Haelfte).
func TestProbeLoginUndBuildAndLoginLaufenNieGleichzeitigFuerDasselbeKonto(t *testing.T) {
	t.Parallel()

	handler, peak := concurrencyTrackingHandler(t, loginRoutes())
	srv := newTestServer(t, handler)
	r := newCountingResolver(map[string]fileee.Credentials{
		"alice": {Username: "alice@example.invalid", Password: "pw"},
	})
	p := testPool(t, srv, r)
	id := &identity.Identity{Subject: "alice"}

	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		_, _ = p.For(context.Background(), id)
	}()
	go func() {
		defer wg.Done()
		_ = p.ProbeLogin(context.Background(), id)
	}()
	wg.Wait()

	if got := peak.Load(); got > 1 {
		t.Errorf("gleichzeitig laufende Anfragen an den Mock-Server = %d, want hoechstens 1 -- "+
			"Pool.For (buildAndLogin) und ProbeLogin liefen gleichzeitig fuer dasselbe Konto", got)
	}
}
