package tools

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/strausmann/gangway/identity"
	"github.com/strausmann/go-fileee/fileee"

	"github.com/strausmann/fileee-mcp-server/internal/accounts"
	"github.com/strausmann/fileee-mcp-server/internal/clientpool"
)

// --- whoamiResultFor / getWhoamiHandler / registerWhoami (Task 3) ---------

func TestWhoamiResultForHappyPath(t *testing.T) {
	p := clientpool.New(accounts.NewSingle(fileee.Credentials{Username: "bjoern@strausmann.net", Password: "x"}))
	out, err := whoamiResultFor(context.Background(), p, ServerInfo{Capabilities: "read", Mode: "single"}, &identity.Identity{Subject: "caller-123"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if out.Identity != "caller-123" {
		t.Fatalf("Identity = %q, want caller-123", out.Identity)
	}
	if !out.Account.Configured {
		t.Fatalf("Account.Configured = false, want true")
	}
	if out.Account.Username != "bjoern@strausmann.net" {
		t.Fatalf("Account.Username = %q, want the account's plain email", out.Account.Username)
	}
	if out.Mode != "single" || out.Capabilities != "read" {
		t.Fatalf("Mode/Capabilities = %q/%q", out.Mode, out.Capabilities)
	}

	// The password must never appear anywhere in the marshaled output, even
	// though the account email is shown plainly.
	marshaled, err := json.Marshal(out)
	if err != nil {
		t.Fatalf("marshal output: %v", err)
	}
	if strings.Contains(string(marshaled), "\"x\"") {
		t.Fatalf("output leaked the password: %s", marshaled)
	}
}

func TestWhoamiResultForAccountNotConfigured(t *testing.T) {
	p := clientpool.New(accounts.NewMulti(map[string]fileee.Credentials{}))
	out, err := whoamiResultFor(context.Background(), p, ServerInfo{Capabilities: "read", Mode: "multi"}, &identity.Identity{Subject: "stranger"})
	if err != nil {
		t.Fatalf("not-configured must not be an error, got %v", err)
	}
	if out.Account.Configured {
		t.Fatalf("Account.Configured = true, want false for an unmapped subject")
	}
	if out.Identity != "stranger" || out.Mode != "multi" || out.Capabilities != "read" {
		t.Fatalf("identity/mode/caps should still be reported: %+v", out)
	}
}

func TestGetWhoamiHandlerRejectsWithoutIdentity(t *testing.T) {
	h := getWhoamiHandler((*clientpool.Pool)(nil), ServerInfo{Capabilities: "read", Mode: "single"}, discardLogger())
	_, _, err := h(context.Background(), nil, whoamiInput{})
	if err == nil {
		t.Fatal("getWhoamiHandler without identity in context: err = nil, want error")
	}
}

func TestRegisterOpsToolsMountsWhoami(t *testing.T) {
	s := mcp.NewServer(&mcp.Implementation{Name: "probe", Version: "0"}, nil)
	registerOpsTools(s, (*clientpool.Pool)(nil), ServerInfo{}, discardLogger())
	names := toolNamesOf(t, s)
	if !names[ToolWhoami] {
		t.Errorf("tool %q was not mounted", ToolWhoami)
	}
}
