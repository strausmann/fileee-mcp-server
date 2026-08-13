// read_account.go wires get_account_status — a bespoke handler,
// *fileee.Client.AccountStatus has no ReadService[T] shape at all:
// AccountStatus is not an entity with its own ID, there is exactly one
// value per account (Feldnamen-Recherche, Abschnitt AccountStatus), so
// there is no list/get pair and no input parameters — get_account_status
// takes none.
//
// AccountStatus carries no foreign text (Feldnamen-Recherche, same
// section): every field is the account holder's own subscription/
// license metadata, including Problem — a status message Fileee itself
// generates, not text copied from a document or a third party. No
// UntrustedLine/PoisonProbe needed.
package tools

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/strausmann/go-fileee/fileee"

	"github.com/strausmann/fileee-mcp-server/internal/clientpool"
)

// getAccountStatusEndpoint is get_account_status' own wire endpoint —
// go-fileee's auth.accountStatus (client.go, auth.go) — logged as fixed,
// per-tool metadata the same way every other endpoint constant in this
// package is.
const getAccountStatusEndpoint = "GET /api/f/account-status"

// accountStatusService is what get_account_status needs from
// *fileee.Client — narrowed to the one method this tool calls, the same
// pattern every other bespoke handler in this package uses to keep its
// fake test double small.
type accountStatusService interface {
	AccountStatus(ctx context.Context) (*fileee.AccountStatus, error)
}

// getAccountStatusInput are get_account_status' parameters — deliberately
// empty. There is exactly one account status per caller; nothing to
// select.
type getAccountStatusInput struct{}

// getAccountStatusOutput is get_account_status' structured result — every
// field fileee.AccountStatus itself carries (see this file's own doc
// comment on why none of them are foreign text). PayedUntil and
// NextLicenseRefill are formatted via formatTime, same as
// documentSummary's own Created/Modified — AccountStatus is one of only
// two Fileee types (alongside Document) whose timestamps are actual
// time.Time values rather than strings (Feldnamen-Recherche).
type getAccountStatusOutput struct {
	AccountTypeID      string  `json:"accountTypeId"`
	SubscriptionName   string  `json:"subscriptionName"`
	SubscriptionFreq   string  `json:"subscriptionFreq"`
	SubscriptionAmount float64 `json:"subscriptionAmount"`
	PayedUntil         string  `json:"payedUntil,omitempty"`
	NextLicenseRefill  string  `json:"nextLicenseRefill,omitempty"`
	Problem            string  `json:"problem,omitempty"`
}

// formatTimePtr formats t via formatTime, or returns "" for a nil
// pointer — fileee.AccountStatus.PayedUntil/NextLicenseRefill are
// *time.Time (nil when not applicable, e.g. no active subscription),
// unlike Document's own plain time.Time fields formatTime was written
// for.
func formatTimePtr(t *time.Time) string {
	if t == nil {
		return ""
	}
	return formatTime(*t)
}

// accountStatusDetail renders as as getAccountStatusOutput — a pure
// function, independent of client resolution.
func accountStatusDetail(as *fileee.AccountStatus) getAccountStatusOutput {
	return getAccountStatusOutput{
		AccountTypeID:      as.AccountTypeID,
		SubscriptionName:   as.SubscriptionName,
		SubscriptionFreq:   as.SubscriptionFreq,
		SubscriptionAmount: as.SubscriptionAmount,
		PayedUntil:         formatTimePtr(as.PayedUntil),
		NextLicenseRefill:  formatTimePtr(as.NextLicenseRefill),
		Problem:            as.Problem,
	}
}

// getAccountStatusHandler resolves get_account_status. No input to
// validate — client resolution is the only failure path before the
// backend call.
func getAccountStatusHandler(p *clientpool.Pool, logger *slog.Logger) mcp.ToolHandlerFor[getAccountStatusInput, getAccountStatusOutput] {
	return func(ctx context.Context, _ *mcp.CallToolRequest, _ getAccountStatusInput) (*mcp.CallToolResult, getAccountStatusOutput, error) {
		start := time.Now()
		logToolStart(ctx, logger, ToolGetAccountStatus)

		client, err := clientFor(ctx, p)
		if err != nil {
			logToolEnd(ctx, logger, ToolGetAccountStatus, start, "", 0, err)
			return nil, getAccountStatusOutput{}, err
		}
		result, out, err := accountStatusFromService(ctx, client)
		logToolEnd(ctx, logger, ToolGetAccountStatus, start, getAccountStatusEndpoint, 1, err)
		return result, out, err
	}
}

// accountStatusFromService is getAccountStatusHandler's logic below
// client resolution — split out so a test can drive it against an
// accountStatusService fake (fakeAccountStatusService,
// read_account_test.go) instead of a live *fileee.Client.
func accountStatusFromService(ctx context.Context, service accountStatusService) (*mcp.CallToolResult, getAccountStatusOutput, error) {
	as, err := service.AccountStatus(ctx)
	if err != nil {
		return nil, getAccountStatusOutput{}, fmt.Errorf("fileee-mcp: tools: %s: %w", ToolGetAccountStatus, err)
	}
	return &mcp.CallToolResult{}, accountStatusDetail(as), nil
}

// registerAccountTools mounts get_account_status onto s — called once
// from RegisterRead (read.go).
func registerAccountTools(s *mcp.Server, p *clientpool.Pool, logger *slog.Logger) {
	mcp.AddTool(s, &mcp.Tool{
		Name: ToolGetAccountStatus,
		Description: "Return the calling user's Fileee account status — subscription type, " +
			"name, billing frequency and amount, license validity and refill dates, and any " +
			"account problem Fileee itself is reporting. Takes no parameters; there is exactly " +
			"one account status per caller. Use it to check subscription/license standing before " +
			"relying on other tools continuing to work. It does not return any per-document " +
			"information and does not accept an account or user ID — it always answers for the " +
			"calling identity.",
		Annotations: &mcp.ToolAnnotations{ReadOnlyHint: true},
	}, getAccountStatusHandler(p, logger))
}
