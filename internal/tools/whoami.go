// whoami.go carries this server's whoami meta-tool — reports the caller's
// verified identity, the fileee account it maps to, and the server's
// account mode, without ever touching Fileee itself.
package tools

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/strausmann/gangway/identity"
	"github.com/strausmann/gangway/serve"

	"github.com/strausmann/fileee-mcp-server/internal/accounts"
	"github.com/strausmann/fileee-mcp-server/internal/clientpool"
)

// ServerInfo carries the per-instance fact whoami reports that is not
// derivable from the request: the server's account mode ("single" /
// "multi"). Filled in at registration time.
type ServerInfo struct {
	Mode string
}

// whoamiInput is whoami's parameters — deliberately empty, like the other
// meta-tools: it reports on the caller, there is nothing to select.
type whoamiInput struct{}

// whoamiAccount is the fileee account whoami resolved for the caller.
// Configured is false when the caller's subject maps to no account;
// Username is then empty. Username is the account's own fileee login email
// — the caller is that account's owner, so showing it plainly (never
// masked) is acceptable; the password and TOTP seed are never included.
type whoamiAccount struct {
	Configured bool   `json:"configured"`
	Username   string `json:"username,omitempty"`
}

// whoamiOutput is whoami's structured result: the verified identity subject,
// the mapped account, and the server's account mode.
type whoamiOutput struct {
	Identity string        `json:"identity"`
	Account  whoamiAccount `json:"account"`
	Mode     string        `json:"mode"`
}

// whoamiResultFor is whoami's logic below identity resolution — split out of
// the handler so a test can drive it with a plain *identity.Identity and a
// resolver-backed Pool, exactly as selfCheckResultFor is (there is no
// exported way to place an identity into a context for serve.IdentityFrom).
// It never returns the password or TOTP seed, and an unmapped subject is a
// normal result (Configured:false), not an error.
func whoamiResultFor(ctx context.Context, p *clientpool.Pool, info ServerInfo, id *identity.Identity) (whoamiOutput, error) {
	out := whoamiOutput{Identity: id.Subject, Mode: info.Mode}

	username, err := p.AccountUsername(ctx, id)
	if err != nil {
		if errors.Is(err, accounts.ErrNoAccount) {
			out.Account = whoamiAccount{Configured: false}
			return out, nil
		}
		return whoamiOutput{}, fmt.Errorf("fileee-mcp: tools: resolve fileee account: %w", err)
	}

	out.Account = whoamiAccount{Configured: true, Username: username}
	return out, nil
}

// getWhoamiHandler resolves whoami. It reads the verified identity from the
// context via serve.IdentityFrom (never auth.TokenInfoFromContext, ADR-0012
// P2), then defers to whoamiResultFor. The identity subject is returned to
// the caller (who is that subject) but never logged (ADR-0012 P10).
func getWhoamiHandler(p *clientpool.Pool, info ServerInfo, logger *slog.Logger) mcp.ToolHandlerFor[whoamiInput, whoamiOutput] {
	return func(ctx context.Context, _ *mcp.CallToolRequest, _ whoamiInput) (*mcp.CallToolResult, whoamiOutput, error) {
		start := time.Now()
		logToolStart(ctx, logger, ToolWhoami)

		id, ok := serve.IdentityFrom(ctx)
		if !ok {
			err := fmt.Errorf("fileee-mcp: tools: no verified identity in context")
			logToolEnd(ctx, logger, ToolWhoami, start, "", 0, err)
			return nil, whoamiOutput{}, err
		}

		out, err := whoamiResultFor(ctx, p, info, id)
		if err != nil {
			logToolEnd(ctx, logger, ToolWhoami, start, "", 0, err)
			return nil, whoamiOutput{}, err
		}

		logToolEnd(ctx, logger, ToolWhoami, start, "", 1, nil)
		return &mcp.CallToolResult{}, out, nil
	}
}

// registerWhoami mounts whoami onto s — called from registerOpsTools.
func registerWhoami(s *mcp.Server, p *clientpool.Pool, info ServerInfo, logger *slog.Logger) {
	mcp.AddTool(s, &mcp.Tool{
		Name: ToolWhoami,
		Description: "Report which verified identity this call is authenticated as and which " +
			"fileee account it maps to on this server, plus the server's account mode. Returns " +
			"the caller's identity subject, whether a fileee account is configured for it and " +
			"— if so — that account's username (its fileee login email; never the password or " +
			"two-factor secret), and the account mode (single or multi). Use it to confirm who " +
			"the server thinks you are. It makes no call to fileee and reflects only what this " +
			"server already knows about the calling identity.",
		Annotations: &mcp.ToolAnnotations{ReadOnlyHint: true, Title: "Who am I"},
	}, getWhoamiHandler(p, info, logger))
}
