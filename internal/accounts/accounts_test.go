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
// unknown caller's rejection must never surface any secret field of an
// account that is not theirs. The plan calls out the username explicitly
// as still a secret in this context, alongside the password and the TOTP
// seed — Credentials carries all three (go-fileee fileee.Credentials).
func TestErrorNeverLeaksCredentials(t *testing.T) {
	t.Parallel()

	r := NewMulti(map[string]fileee.Credentials{
		"alice": {
			Username: "alice@example.com",
			Password: "super-secret-value",
			TOTPSeed: "JBSWY3DPEHPK3PXP",
		},
	})

	_, err := r.Credentials(context.Background(), &identity.Identity{Subject: "mallory"})
	if err == nil {
		t.Fatal("want an error")
	}

	for _, leaked := range []string{"alice@example.com", "super-secret-value", "JBSWY3DPEHPK3PXP"} {
		if strings.Contains(err.Error(), leaked) {
			t.Errorf("error message leaked a credential field: %q appeared in %q", leaked, err.Error())
		}
	}
}
