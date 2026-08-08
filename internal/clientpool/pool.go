// Package clientpool caches one authenticated go-fileee client per Fileee
// account and serializes the login that creates it.
//
// A Fileee access is not a bearer token but a logged-in connection carrying
// session cookies (go-fileee, package doc, section "Authentifizierung").
// Establishing one costs the service a real login round-trip, and — per
// go-fileee's own rate limiting (go-fileee ADR-0005) and Fileee's account
// lockout on concurrent logins (ADR-0012, point 6, of this repository) —
// doing it more than once for the same account is not just wasteful but
// something Fileee actively penalizes. This package makes sure it happens
// at most once per account, however many callers ask for it at once.
package clientpool

import (
	"context"
	"fmt"
	"sync"
	"time"

	"golang.org/x/sync/singleflight"

	"github.com/strausmann/fileee-mcp-server/internal/accounts"
	"github.com/strausmann/gangway/identity"
	"github.com/strausmann/go-fileee/fileee"
)

// pooledClient is one cached, already logged-in connection, plus the means
// to stop the keepalive goroutine that may be holding it warm.
type pooledClient struct {
	client *fileee.Client
	stop   func()
}

// Pool caches one *fileee.Client per Fileee account and serializes the
// login that creates it.
//
// Accounts are keyed by their resolved fileee.Credentials.Username, not by
// the caller identity that led there (see accountKey) — the consequence,
// deliberate and required by ADR-0012 point 6, is that every caller
// resolving to the same account (single mode maps every subject to the one
// configured account; multi mode can map several subjects to one account
// as well, see ADR-0012 point 4) shares the very same connection rather
// than opening a second session for it.
//
// Two independent levels of deduplication follow from that:
//
//   - bySubject groups concurrent For calls that carry the very same caller
//     identity, before anything about the target account is even known —
//     without it, N concurrent first-time calls for one caller would each
//     resolve credentials and race to build a client.
//   - byAccount groups concurrent build attempts for the same resolved
//     account, even across different caller identities. This is the case
//     ADR-0012 point 6 names directly: two simultaneous logins against the
//     very same account are exactly the pattern that trips Fileee's own
//     account lockout (secondsBlocked). It matters most in single mode,
//     where every caller resolves to the one configured account — without
//     this second level, each new caller racing on its first request would
//     open its own session against that shared account. bySubject alone
//     cannot catch this, because it only groups calls that share a subject.
//
// A resolver failure (accounts.ErrNoAccount) and a failed login are never
// cached: singleflight discards its entry for a key as soon as the call for
// that key returns, successful or not, so the very next call starts a
// completely fresh attempt. Only a client that has actually completed
// EnsureSession is stored — a transient network failure or a temporary
// account lock must not make the account permanently unusable for the life
// of the process.
type Pool struct {
	resolver accounts.Resolver

	bySubject singleflight.Group
	byAccount singleflight.Group

	clientOptions     []fileee.Option
	sessionStoreFor   func(accountKey string) fileee.SessionStore
	keepaliveInterval time.Duration

	mu      sync.Mutex
	clients map[string]*pooledClient
	closed  bool
}

// Option configures a Pool built by New.
type Option func(*Pool)

// WithClientOptions passes opts to fileee.NewClient for every account this pool
// builds a client for — for settings that do not vary per account, such as
// fileee.WithBaseURL and fileee.WithRateLimit in tests, or
// fileee.WithBackoff/fileee.WithLogger/fileee.WithUserAgent in production.
//
// It is not the way to give an account its own session file — see
// WithSessionStore for that; a fileee.WithSessionStore passed here would
// apply the very same store to every account this pool serves.
func WithClientOptions(opts ...fileee.Option) Option {
	return func(p *Pool) { p.clientOptions = append(p.clientOptions, opts...) }
}

// WithSessionStore lets the caller supply a session-persistence store per
// account, keyed the same way this pool itself keys accounts (see the Pool
// doc on accountKey — the resolved Fileee username, not the shorter
// FILEEE_ACCOUNT_<KEY> prefix from ADR-0012 point 8, which accounts.Resolver
// does not hand to this package). storeFor is called once, right before the
// first login attempt for that account.
//
// Without this option, fileee.NewClient falls back to its own default: a single
// file shared by every client this pool builds, under the OS user cache
// directory. That default is safe only when this pool ever serves one
// account (FILEEE_MODE=single) — in multi mode every account would read and
// overwrite that same file. A caller wiring this pool for multi mode must
// set this.
func WithSessionStore(storeFor func(accountKey string) fileee.SessionStore) Option {
	return func(p *Pool) { p.sessionStoreFor = storeFor }
}

// WithKeepalive holds each account's session warm by calling
// (*fileee.Client).StartKeepAlive with d once its first login succeeds, so
// an incoming request does not pay the reauthentication latency. d<=0 (the
// default) starts no keepalive goroutine at all — the session is then
// re-established lazily, on whichever call first needs it once it goes
// stale, exactly as fileee.NewClient's own default behaves without this option.
func WithKeepalive(d time.Duration) Option {
	return func(p *Pool) { p.keepaliveInterval = d }
}

// New returns a Pool that resolves accounts through r. It performs no I/O
// and cannot fail: nothing is built or logged into until the first call to
// For.
func New(r accounts.Resolver, opts ...Option) *Pool {
	p := &Pool{
		resolver: r,
		clients:  make(map[string]*pooledClient),
	}
	for _, opt := range opts {
		opt(p)
	}
	return p
}

// For returns the pooled, authenticated client for id's account, resolving
// the account and logging in on first use and reusing the same connection
// on every call after that — including calls for a different id that
// resolves to the same account (see the Pool doc).
//
// It returns an error wrapping accounts.ErrNoAccount when id has no account
// mapped to it, and an error wrapping whatever go-fileee reported when the
// account is known but the login itself fails. Neither case is cached; see
// the Pool doc.
func (p *Pool) For(ctx context.Context, id *identity.Identity) (*fileee.Client, error) {
	if id == nil || id.Subject == "" {
		return nil, fmt.Errorf("fileee-mcp: clientpool: no verified identity")
	}

	v, err, _ := p.bySubject.Do(id.Subject, func() (any, error) {
		return p.forSubject(ctx, id)
	})
	if err != nil {
		return nil, err
	}
	return v.(*fileee.Client), nil
}

// forSubject resolves id's account and returns its pooled client. It runs
// at most once per subject at a time (see the Pool doc on bySubject) —
// singleflight.Group.Do guarantees that only one caller's fn actually
// executes per key; every other concurrent caller for the same key waits
// and receives that one execution's result.
func (p *Pool) forSubject(ctx context.Context, id *identity.Identity) (*fileee.Client, error) {
	creds, err := p.resolver.Credentials(ctx, id)
	if err != nil {
		return nil, err
	}

	key, err := accountKey(creds)
	if err != nil {
		return nil, err
	}

	if client, ok := p.cached(key); ok {
		return client, nil
	}

	// A second, independent dedup: this same key can also be reached by a
	// different subject that resolves to the same account (see the Pool
	// doc on byAccount). bySubject above cannot catch that on its own,
	// because it only groups calls that share id.Subject.
	v, err, _ := p.byAccount.Do(key, func() (any, error) {
		return p.buildAndLogin(ctx, key, creds)
	})
	if err != nil {
		return nil, err
	}
	return v.(*fileee.Client), nil
}

// cached returns the already-pooled client for key, if any.
func (p *Pool) cached(key string) (*fileee.Client, bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	pc, ok := p.clients[key]
	if !ok {
		return nil, false
	}
	return pc.client, true
}

// buildAndLogin constructs a client for creds and establishes a real
// session before returning it.
//
// Nothing is cached unless EnsureSession actually succeeds — see the Pool
// doc for why. It re-checks the cache first: byAccount.Do only prevents
// concurrent duplicate execution for key, it does not guarantee the cache
// was still empty by the time this particular call started running — an
// earlier call for the same key can have finished and populated the cache
// between forSubject's own check and byAccount.Do actually scheduling this
// function.
func (p *Pool) buildAndLogin(ctx context.Context, key string, creds fileee.Credentials) (*fileee.Client, error) {
	if client, ok := p.cached(key); ok {
		return client, nil
	}

	opts := p.clientOptions
	if p.sessionStoreFor != nil {
		// Copy before appending: p.clientOptions is shared by every
		// account this pool serves, and append may otherwise reuse
		// (and race on) its backing array across concurrent accounts.
		opts = append(append([]fileee.Option{}, p.clientOptions...), fileee.WithSessionStore(p.sessionStoreFor(key)))
	}

	client, err := fileee.NewClient(creds, opts...)
	if err != nil {
		return nil, fmt.Errorf("fileee-mcp: clientpool: build client for account: %w", err)
	}
	if err := client.EnsureSession(ctx); err != nil {
		return nil, fmt.Errorf("fileee-mcp: clientpool: login for account: %w", err)
	}

	stop := func() {}
	if p.keepaliveInterval > 0 {
		// context.Background(), not ctx: ctx is the request that happened
		// to trigger this login and is typically cancelled the moment
		// that request finishes, which would kill the keepalive almost as
		// soon as it started. The goroutine's lifetime is the pool's, not
		// any one caller's — Close is the only intended way to stop it.
		stop = client.StartKeepAlive(context.Background(), p.keepaliveInterval)
	}

	p.mu.Lock()
	p.clients[key] = &pooledClient{client: client, stop: stop}
	closed := p.closed
	p.mu.Unlock()
	if closed {
		// Close ran while this login was still in flight. This entry was
		// never in the map for Close to find and stop, so it is stopped
		// here instead — otherwise its keepalive goroutine would outlive
		// Close indefinitely.
		stop()
	}
	return client, nil
}

// Close stops the keepalive goroutine of every pooled client. It is safe to
// call more than once, and safe to call whether or not WithKeepalive was
// ever used. It does not stop For from building new clients afterwards;
// call it once, right before the process exits.
func (p *Pool) Close() {
	p.mu.Lock()
	p.closed = true
	stops := make([]func(), 0, len(p.clients))
	for _, pc := range p.clients {
		stops = append(stops, pc.stop)
	}
	p.mu.Unlock()

	for _, stop := range stops {
		stop()
	}
}

// accountKey derives the pool's cache key from creds. It is the account's
// Fileee username, not the short account key configured in FILEEE_ACCOUNTS
// (ADR-0012, point 8) — accounts.Resolver returns credentials only, and
// this package never sees that shorter key. Two different subjects that
// resolve to accounts with the same Username therefore share a client
// deliberately (see the Pool doc); an empty Username can only mean a broken
// accounts.Resolver — config.LoadConfig itself never lets one through — and
// is refused here rather than silently pooled under a blank key that any
// other broken account could collide on.
func accountKey(creds fileee.Credentials) (string, error) {
	if creds.Username == "" {
		return "", fmt.Errorf("fileee-mcp: clientpool: resolved account has no username")
	}
	return creds.Username, nil
}
