// Package issued records, per verified caller identity, which document,
// contact and reminder IDs this server has actually handed out through a
// read tool — and lets a destructive tool ask, before it acts, whether the
// ID it was given is one of them.
//
// The reason this exists at all is ADR-0013 point 3: document content is
// fremdbestimmte data (a document's sender writes its text, not the
// person using this server), so an ID that merely appears inside a
// document's text — say, in a prompt-injection attempt embedded in an
// invoice — must never be an acceptable target for a mutating operation.
// Binding destructive tools to IDs this server itself delivered through a
// prior, genuine read step closes exactly that gap: an ID the model only
// ever saw as text inside someone else's document was never Record'd, so
// Check rejects it the same way it rejects any other unknown ID.
//
// Identity binding follows the same rule clientFor (internal/tools/read.go)
// already established for account resolution (ADR-0012): the caller comes
// exclusively from serve.IdentityFrom(ctx), Gangway's per-request,
// stateless read of the verified token, never cached and never substituted
// with a fixed identity. Under ADR-0015's forced statelessness a Gangway
// session opens and closes per request, so a whitelist keyed by session
// could never remember anything past the single call that populated it —
// this package keys by the verified identity's Subject instead, exactly as
// ADR-0013 point 3 specifies.
//
// This file carries recording and checking without any notion of expiry or
// of a per-identity cap on how many IDs it remembers — New already accepts
// both (ttl and maxPerIdentity), so a later change that starts enforcing
// them needs no signature change here, but for now both fields sit unused,
// each documented at its declaration.
package issued

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/strausmann/gangway/serve"
)

// ErrNotIssued reports that an id does not currently count as issued to
// the calling identity — either because this server never handed it out
// to anyone, or because it was handed out to a different identity.
// Callers check for it with errors.Is; Check's own error, which wraps
// this, deliberately makes no distinction beyond "not issued" (see
// Check's doc comment).
var ErrNotIssued = errors.New("fileee-mcp: issued: id was not handed out to this identity")

// Store remembers, per verified caller identity, which IDs this server has
// handed out through a read tool.
//
// The zero value is not usable; build one with New.
type Store struct {
	// ttl bounds how long a recorded id stays valid. Not yet evaluated —
	// Record never sets an expiry and Check never consults one; a
	// recorded id is valid indefinitely (within process lifetime) as of
	// this file. Enforcing ttl is a later change (see this package's own
	// doc comment).
	ttl time.Duration

	// maxPerIdentity bounds how many ids a single identity's bucket may
	// hold at once. Not yet evaluated — Record never enforces a cap; a
	// caller's bucket grows without bound as of this file. Enforcing this
	// cap is a later change (see this package's own doc comment).
	maxPerIdentity int

	mu sync.Mutex
	// byIdent maps a verified identity's Subject to the set of ids
	// recorded for it, each with the time.Time it was recorded at. The
	// recorded time is not read by anything in this file (see ttl above)
	// but is captured now so a later expiry check needs no change to
	// Record's own logic, only to Check's.
	byIdent map[string]map[string]time.Time
}

// New builds a Store. ttl and maxPerIdentity are accepted and stored but,
// as of this file, not yet evaluated by Record or Check — see their own
// doc comments and this package's doc comment.
func New(ttl time.Duration, maxPerIdentity int) *Store {
	return &Store{
		ttl:            ttl,
		maxPerIdentity: maxPerIdentity,
		byIdent:        map[string]map[string]time.Time{},
	}
}

// subjectOf resolves the verified caller's Subject from ctx, the same way
// clientFor (internal/tools/read.go) resolves the caller for account
// lookup: exclusively via serve.IdentityFrom(ctx), never cached, never
// substituted. Its false return is this package's single fail-closed
// gate — reached whenever ctx carries no identity at all, a nil identity,
// or an identity with an empty Subject — and both Record and Check treat
// it identically: no identity means neither "remember this" nor "let this
// through" is ever true.
func subjectOf(ctx context.Context) (string, bool) {
	id, ok := serve.IdentityFrom(ctx)
	if !ok || id == nil || id.Subject == "" {
		return "", false
	}
	return id.Subject, true
}

// Record marks ids as handed out to ctx's verified caller, so a later
// Check for the same identity and one of these ids succeeds.
//
// Without a verified identity in ctx, Record does nothing at all — not
// even into a shared, identity-less bucket (that would defeat the whole
// point: an unauthenticated recording could never be told apart from one
// made on a real caller's behalf). Empty ids are skipped; a duplicate id
// (already recorded for this identity) simply has its recorded time
// refreshed, which is never an error.
func (s *Store) Record(ctx context.Context, ids ...string) {
	subject, ok := subjectOf(ctx)
	if !ok {
		return
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	bucket := s.byIdent[subject]
	if bucket == nil {
		bucket = map[string]time.Time{}
		s.byIdent[subject] = bucket
	}
	now := time.Now()
	for _, id := range ids {
		if id == "" {
			continue
		}
		bucket[id] = now
	}
}

// Check reports whether id counts as issued to ctx's verified caller —
// nil if a prior Record call recorded id for exactly this identity, else
// an error wrapping ErrNotIssued.
//
// The error is deliberately uninformative beyond "not issued": it never
// echoes id back, and it never distinguishes "this id was never recorded
// for anyone" from "it was recorded, but for a different identity" or (once
// a later change enforces ttl) "it was recorded but has since expired".
// Any of those distinctions would let a caller probe for the existence of
// ids that belong to someone else — the exact thing this whitelist exists
// to prevent (ADR-0013 point 3; see this package's doc comment). Without a
// verified identity in ctx, or for an empty id, Check fails the same way:
// fail-closed, same error, same lack of detail.
func (s *Store) Check(ctx context.Context, id string) error {
	subject, ok := subjectOf(ctx)
	if !ok || id == "" {
		return errNotIssuedFor()
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	if _, recorded := s.byIdent[subject][id]; recorded {
		return nil
	}
	return errNotIssuedFor()
}

// errNotIssuedFor builds Check's outward-facing error: English, because it
// reaches the calling model as a tool error (this package's doc comment;
// CONTRIBUTING.md on user-facing text), wrapping ErrNotIssued so callers
// can errors.Is against it, and naming the way forward instead of just the
// refusal.
func errNotIssuedFor() error {
	return fmt.Errorf(
		"this id was not handed out by a read tool in this session; fetch it first "+
			"(for example via list_documents or get_document) and retry: %w",
		ErrNotIssued,
	)
}
