package accounts

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/strausmann/gangway/identity"
	"github.com/strausmann/go-fileee/fileee"
)

func TestSingleAlwaysReturnsTheOneAccount(t *testing.T) {
	t.Parallel()

	want := fileee.Credentials{Username: "u", Password: "p"}
	r := NewSingle(want)

	got, err := r.Credentials(context.Background(), &identity.Identity{Subject: "anyone"})
	if err != nil {
		t.Fatalf("Credentials: %v", err)
	}
	if got.Username != want.Username {
		t.Errorf("Username = %q, want %q", got.Username, want.Username)
	}
}

func TestMultiMapsSubjectToItsOwnAccount(t *testing.T) {
	t.Parallel()

	r := NewMulti(map[string]fileee.Credentials{
		"alice": {Username: "alice@example.com"},
		"bob":   {Username: "bob@example.com"},
	})

	got, err := r.Credentials(context.Background(), &identity.Identity{Subject: "bob"})
	if err != nil {
		t.Fatalf("Credentials: %v", err)
	}
	if got.Username != "bob@example.com" {
		t.Errorf("Username = %q, want bob's", got.Username)
	}
}

func TestMultiRefusesAnUnknownSubject(t *testing.T) {
	t.Parallel()

	r := NewMulti(map[string]fileee.Credentials{"alice": {Username: "alice@example.com"}})

	// No fallback to a default account — an unknown caller gets nothing
	// (ADR-0012, point 4), rather than someone else's account.
	_, err := r.Credentials(context.Background(), &identity.Identity{Subject: "mallory"})
	if !errors.Is(err, ErrNoAccount) {
		t.Errorf("err = %v, want ErrNoAccount", err)
	}
}

// TestErrorNeverLeaksCredentials is the core assertion of this package: an
// unknown caller's rejection must never surface any secret field of any
// account in the map — not just the one account a smaller test happens to
// use. The check list is built from every field of every account in
// accountsBySubject, not hardcoded, so a regression that leaks a field of
// an account other than the one used to construct the map cannot go
// unnoticed just because that other account isn't mentioned by name here.
func TestErrorNeverLeaksCredentials(t *testing.T) {
	t.Parallel()

	accountsBySubject := map[string]fileee.Credentials{
		"alice": {
			Username: "alice@example.com",
			Password: "super-secret-value",
			TOTPSeed: "JBSWY3DPEHPK3PXP",
		},
		"bob": {
			Username: "bob@example.com",
			Password: "another-secret-value",
			TOTPSeed: "MFRGGZDFMZTWQ2LK",
		},
	}
	r := NewMulti(accountsBySubject)

	_, err := r.Credentials(context.Background(), &identity.Identity{Subject: "mallory"})
	if err == nil {
		t.Fatal("want an error")
	}

	for subject, creds := range accountsBySubject {
		for _, leaked := range []string{creds.Username, creds.Password, creds.TOTPSeed} {
			if strings.Contains(err.Error(), leaked) {
				t.Errorf("error message leaked a credential field of account %q: %q appeared in %q",
					subject, leaked, err.Error())
			}
		}
	}
}

// TestMultiRefusesAnEmptySubject closes a gap the plan's test list did not
// cover: an empty string is a perfectly valid Go map key, so without an
// explicit guard, a caller with no subject at all silently mapped to
// whatever account happened to be stored under "" — a realistic outcome of
// a configuration bug (an unset field while building the map, or a parsing
// error that yields an empty key), not a contrived one.
func TestMultiRefusesAnEmptySubject(t *testing.T) {
	t.Parallel()

	r := NewMulti(map[string]fileee.Credentials{
		"":      {Username: "orphaned-real-account@example.com", Password: "real-secret"},
		"alice": {Username: "alice@example.com"},
	})

	_, err := r.Credentials(context.Background(), &identity.Identity{Subject: ""})
	if !errors.Is(err, ErrNoAccount) {
		t.Errorf("err = %v, want ErrNoAccount", err)
	}
}

// TestMultiRefusesANilIdentity guards the same trust boundary against a nil
// *identity.Identity: Credentials must return ErrNoAccount, not panic.
func TestMultiRefusesANilIdentity(t *testing.T) {
	t.Parallel()

	r := NewMulti(map[string]fileee.Credentials{"alice": {Username: "alice@example.com"}})

	_, err := r.Credentials(context.Background(), nil)
	if !errors.Is(err, ErrNoAccount) {
		t.Errorf("err = %v, want ErrNoAccount", err)
	}
}
