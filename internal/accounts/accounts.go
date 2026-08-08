// Package accounts maps a verified caller identity to the Fileee account it
// may use. Gangway (see ADR-0015) deliberately does not own this step — it
// verifies who is calling, not which downstream Fileee account they may
// touch. That mapping is application-specific and lives here.
package accounts

import (
	"context"
	"errors"
	"fmt"

	"github.com/strausmann/gangway/identity"
	"github.com/strausmann/go-fileee/fileee"
)

// ErrNoAccount is returned when a verified caller has no account mapped to
// them. There is deliberately no fallback: a caller we do not know gets
// nothing, rather than someone else's documents (ADR-0012, point 4/5).
var ErrNoAccount = errors.New("fileee-mcp: no account for this subject")

// Resolver maps a verified caller identity to the Fileee credentials of the
// one account it may use.
type Resolver interface {
	// Credentials returns the Fileee credentials for id. It returns an
	// error wrapping ErrNoAccount (errors.Is still matches) when id has no
	// account mapped to it. The error never contains any credential field
	// — not the password, not the TOTP seed, and not even the username.
	Credentials(ctx context.Context, id *identity.Identity) (fileee.Credentials, error)
}

// single always resolves to the one configured account, regardless of the
// caller. Used when FILEEE_MODE=single (see internal/config): every caller
// gangway lets through shares one Fileee account.
type single struct {
	creds fileee.Credentials
}

// NewSingle returns a Resolver that always resolves to creds, regardless of
// the caller's identity.
func NewSingle(creds fileee.Credentials) Resolver {
	return single{creds: creds}
}

// Credentials implements Resolver. It never fails: a single-account
// Resolver has nobody to refuse.
func (s single) Credentials(_ context.Context, _ *identity.Identity) (fileee.Credentials, error) {
	return s.creds, nil
}

// multi maps a caller's subject claim to its own account. Used when
// FILEEE_MODE=multi: several callers share this server, each with their own
// Fileee account (ADR-0012).
type multi struct {
	bySubject map[string]fileee.Credentials
}

// NewMulti returns a Resolver that maps id.Subject to its own credentials
// via bySubject. A subject not present in bySubject yields an error
// wrapping ErrNoAccount — there is no fallback to a default account
// (ADR-0012, point 4).
func NewMulti(bySubject map[string]fileee.Credentials) Resolver {
	return multi{bySubject: bySubject}
}

// Credentials implements Resolver. The returned error, if any, names only
// the caller's subject — never a credential field of any account,
// including one that is not the caller's own.
func (m multi) Credentials(_ context.Context, id *identity.Identity) (fileee.Credentials, error) {
	// Both guards run before the map is ever consulted, so the trust
	// boundary holds regardless of what bySubject happens to contain —
	// including a "" key, which is a perfectly valid Go map key and would
	// otherwise silently match a caller with no subject at all. A blank
	// key can arise from an ordinary configuration mistake (an unset field
	// while building the map, or a parsing bug that yields an empty
	// string), so this is not a contrived case.
	if id == nil {
		return fileee.Credentials{}, fmt.Errorf("%w: no verified identity", ErrNoAccount)
	}
	if id.Subject == "" {
		return fileee.Credentials{}, fmt.Errorf("%w: subject is empty", ErrNoAccount)
	}

	creds, ok := m.bySubject[id.Subject]
	if !ok {
		// This error carries the caller's subject in the clear. That is
		// fine for the value returned here: it goes back to the caller
		// itself, who already knows their own subject. Anyone who logs
		// this error, or writes it into a 403 response, MUST shorten the
		// subject first (first 8 characters of a SHA-256 hash) — see
		// ADR-0012, point 10. With MCP_OIDC_SUBJECT_CLAIM=email, an
		// unshortened subject would otherwise put a plaintext email
		// address into a log file or an HTTP response.
		return fileee.Credentials{}, fmt.Errorf("%w: subject %q", ErrNoAccount, id.Subject)
	}
	return creds, nil
}
