// ops.go carries this server's operational tools — get_runtime_stats
// (Aufgabe C1), get_tool_manifest (Aufgabe C2) and self_check (Aufgabe
// C3). All three are read-only (ReadOnlyHint: true); the first two never
// touch Fileee account data at all, self_check is the one exception —
// see its own doc comment section below for why, and for how it avoids
// the coupling bug clientFor (read.go) has by design.
//
// The counters get_runtime_stats reports hang off logToolEnd
// (read.go) — the one place all three registration families
// (hand-written, generic, sync) already funnel through since Antrag #47,
// not something this file has to duplicate at three separate call sites.
// See recordToolCall's own doc comment for why that matters.
package tools

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sort"
	"sync"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/strausmann/gangway/identity"
	"github.com/strausmann/gangway/serve"
	"github.com/strausmann/go-fileee/fileee"
	"golang.org/x/sync/singleflight"

	"github.com/strausmann/fileee-mcp-server/internal/accounts"
	"github.com/strausmann/fileee-mcp-server/internal/clientpool"
)

// --- get_runtime_stats -----------------------------------------------------

// toolCallStats is one tool's running counters since this process
// started. LastErrorKind is classifyErr's fixed-vocabulary outcome
// ("invalid_input", "access_denied", "fileee_error", "error") — never an
// error's own message text, see recordToolCall's own doc comment for why
// that boundary is the entire point of this file.
type toolCallStats struct {
	Calls         int64
	Errors        int64
	LastErrorKind string
	LastErrorAt   time.Time
}

// runtimeStats is the process-wide, concurrency-safe call counter behind
// get_runtime_stats — a package-level var, not a struct threaded through
// every handler, because it belongs to the PROCESS, not to any one
// *mcp.Server instance: the server holds up to sixteen such instances,
// one per reachable capability combination (server.go's buildInstances,
// ADR-0011), but every one of their handlers funnels through the same
// logToolEnd regardless of which instance served a given call. One mutex
// around one map is simplest to reason about at the call volume a single
// MCP server actually sees — no need for per-tool locks or atomics.
var runtimeStats = struct {
	mu     sync.Mutex
	byTool map[string]*toolCallStats
}{byTool: make(map[string]*toolCallStats)}

// recordToolCall updates runtimeStats for one finished tool call. kind is
// classifyErr's own outcome vocabulary passed straight through by
// logToolEnd (its only caller) — "" for a successful call, otherwise one
// of classifyErr's fixed strings. It must never be given err.Error()
// itself: a raw error message can carry text Fileee's backend chose
// (classifyErr's own doc comment, read.go) or, worse, a credential-shaped
// value from wherever the error originated — exactly the leak Dockhand's
// runtime-stats tool had before it was fixed (Antrag #186 there). Routing
// every call through logToolEnd's own classifyErr call, instead of
// letting each tool decide for itself what counts as "safe to record",
// is what keeps that judgement in the one place already trusted with it.
func recordToolCall(tool, kind string, at time.Time) {
	runtimeStats.mu.Lock()
	defer runtimeStats.mu.Unlock()
	s, ok := runtimeStats.byTool[tool]
	if !ok {
		s = &toolCallStats{}
		runtimeStats.byTool[tool] = s
	}
	s.Calls++
	if kind != "" {
		s.Errors++
		s.LastErrorKind = kind
		s.LastErrorAt = at
	}
}

// runtimeStatsToolEntry is get_runtime_stats' per-tool breakdown.
// LastErrorKind/LastErrorAt stay empty for a tool that has never failed —
// json's omitempty leaves them out of the wire response rather than
// sending an empty string and the Unix-epoch zero time.
type runtimeStatsToolEntry struct {
	Tool          string `json:"tool"`
	Calls         int64  `json:"calls"`
	Errors        int64  `json:"errors"`
	LastErrorKind string `json:"lastErrorKind,omitempty"`
	LastErrorAt   string `json:"lastErrorAt,omitempty"`
}

// getRuntimeStatsInput are get_runtime_stats' parameters — deliberately
// empty, same reasoning as getAccountStatusInput (read_account.go):
// nothing to select, the snapshot always covers every tool this process
// has seen so far.
type getRuntimeStatsInput struct{}

// getRuntimeStatsOutput is get_runtime_stats' structured result.
type getRuntimeStatsOutput struct {
	TotalCalls  int64                   `json:"totalCalls"`
	TotalErrors int64                   `json:"totalErrors"`
	Tools       []runtimeStatsToolEntry `json:"tools"`
}

// runtimeStatsSnapshot copies runtimeStats under its own lock into a
// plain value, sorted by tool name for a deterministic result — the lock
// is held only for the copy itself, never while building the sorted
// slice or summing totals.
func runtimeStatsSnapshot() getRuntimeStatsOutput {
	runtimeStats.mu.Lock()
	byTool := make(map[string]toolCallStats, len(runtimeStats.byTool))
	for name, s := range runtimeStats.byTool {
		byTool[name] = *s
	}
	runtimeStats.mu.Unlock()

	names := make([]string, 0, len(byTool))
	for name := range byTool {
		names = append(names, name)
	}
	sort.Strings(names)

	out := getRuntimeStatsOutput{Tools: make([]runtimeStatsToolEntry, 0, len(names))}
	for _, name := range names {
		s := byTool[name]
		out.TotalCalls += s.Calls
		out.TotalErrors += s.Errors
		entry := runtimeStatsToolEntry{Tool: name, Calls: s.Calls, Errors: s.Errors, LastErrorKind: s.LastErrorKind}
		if !s.LastErrorAt.IsZero() {
			entry.LastErrorAt = formatTime(s.LastErrorAt)
		}
		out.Tools = append(out.Tools, entry)
	}
	return out
}

// getRuntimeStatsHandler resolves get_runtime_stats. No client
// resolution at all — the snapshot comes entirely from this process's own
// in-memory counters, never from Fileee, so there is no clientFor call
// and nothing that can fail here. The snapshot is taken BEFORE this call
// itself is recorded (that happens afterwards, in the logToolEnd call
// below, the same as every other tool) — this call's own entry in the
// counters therefore reflects everything up to but not including itself.
func getRuntimeStatsHandler(logger *slog.Logger) mcp.ToolHandlerFor[getRuntimeStatsInput, getRuntimeStatsOutput] {
	return func(ctx context.Context, _ *mcp.CallToolRequest, _ getRuntimeStatsInput) (*mcp.CallToolResult, getRuntimeStatsOutput, error) {
		start := time.Now()
		logToolStart(ctx, logger, ToolGetRuntimeStats)
		out := runtimeStatsSnapshot()
		logToolEnd(ctx, logger, ToolGetRuntimeStats, start, "", len(out.Tools), nil)
		return &mcp.CallToolResult{}, out, nil
	}
}

// --- get_tool_manifest -------------------------------------------------

// toolManifestEntry is get_tool_manifest's per-tool entry. Kind is
// access.ToolKind's own string value ("read" today — Teil B, a later
// phase of this project, would add "write"), taken from ReadToolKinds()
// rather than re-derived here, so this can never disagree with what
// Gangway's own authorization middleware actually enforces (server.go,
// serve.WithToolKinds(tools.ReadToolKinds())).
type toolManifestEntry struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	Kind        string `json:"kind"`
}

// getToolManifestInput are get_tool_manifest's parameters — deliberately
// empty, same reasoning as getRuntimeStatsInput above: the manifest
// always covers every tool the calling instance currently has mounted.
type getToolManifestInput struct{}

// getToolManifestOutput is get_tool_manifest's structured result.
type getToolManifestOutput struct {
	Total int                 `json:"total"`
	Tools []toolManifestEntry `json:"tools"`
}

// listMountedTools opens a throwaway, in-process client session against s
// itself and reads back exactly the tools currently mounted on it — the
// same in-memory round trip registeredReadTools() (names.go) already
// uses against a disposable PROBE server, applied here instead to the
// real, already-registered server instance a running get_tool_manifest
// handler was given.
//
// Because get_tool_manifest and get_runtime_stats are themselves
// registered on s before any request is ever served (registerOpsTools
// runs inside RegisterRead, before s.Connect is ever called for a real
// caller), this round trip counts both of them automatically — no
// separate self-reference to remember, unlike Dockhand's hand-maintained
// META_TOOL_NAMES list, which stayed a manually kept constant even after
// its 292-vs-298 fix (Antrag #186 there, and the grundlage document this
// task is based on, docs/research/2026-08-12-fileee-betriebswerkzeuge-
// grundlage.md, Frage 2, in the homelab-management repo).
//
// This was flagged in that same document as plausible but NOT verified
// by execution — a second, independent in-memory session on a server
// that may already have live client sessions attached, opened from
// inside one of that server's own handlers. TestGetToolManifest...
// (ops_test.go) is that verification: it drives this function through a
// live handler call against a server RegisterRead already fully wired,
// and the result matches an independently taken toolNamesOf() reading of
// the same server exactly. No problem was found.
func listMountedTools(ctx context.Context, s *mcp.Server) ([]*mcp.Tool, error) {
	clientTransport, serverTransport := mcp.NewInMemoryTransports()
	serverSession, err := s.Connect(ctx, serverTransport, nil)
	if err != nil {
		return nil, fmt.Errorf("fileee-mcp: tools: %s: connect probe session: %w", ToolGetToolManifest, err)
	}
	defer func() { _ = serverSession.Close() }()

	client := mcp.NewClient(&mcp.Implementation{Name: "tool-manifest-probe", Version: "0"}, nil)
	clientSession, err := client.Connect(ctx, clientTransport, nil)
	if err != nil {
		return nil, fmt.Errorf("fileee-mcp: tools: %s: connect probe client: %w", ToolGetToolManifest, err)
	}
	defer func() { _ = clientSession.Close() }()

	res, err := clientSession.ListTools(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("fileee-mcp: tools: %s: list tools: %w", ToolGetToolManifest, err)
	}
	return res.Tools, nil
}

// getToolManifestHandler resolves get_tool_manifest. s is the same
// *mcp.Server RegisterRead was given to mount every tool onto — this
// handler closes over it so listMountedTools always asks the live
// instance, never a separately built copy that could drift from it.
//
// No client resolution here either, same reasoning as
// getRuntimeStatsHandler: introspecting s is entirely local, nothing
// here ever talks to Fileee.
func getToolManifestHandler(s *mcp.Server, logger *slog.Logger) mcp.ToolHandlerFor[getToolManifestInput, getToolManifestOutput] {
	return func(ctx context.Context, _ *mcp.CallToolRequest, _ getToolManifestInput) (*mcp.CallToolResult, getToolManifestOutput, error) {
		start := time.Now()
		logToolStart(ctx, logger, ToolGetToolManifest)

		mounted, err := listMountedTools(ctx, s)
		if err != nil {
			logToolEnd(ctx, logger, ToolGetToolManifest, start, "", 0, err)
			return nil, getToolManifestOutput{}, err
		}

		kinds := ReadToolKinds()
		out := getToolManifestOutput{Total: len(mounted), Tools: make([]toolManifestEntry, 0, len(mounted))}
		for _, tool := range mounted {
			kind := ""
			if k, ok := kinds[tool.Name]; ok {
				kind = string(k)
			}
			out.Tools = append(out.Tools, toolManifestEntry{Name: tool.Name, Description: tool.Description, Kind: kind})
		}
		sort.Slice(out.Tools, func(i, j int) bool { return out.Tools[i].Name < out.Tools[j].Name })

		logToolEnd(ctx, logger, ToolGetToolManifest, start, "", len(out.Tools), nil)
		return &mcp.CallToolResult{}, out, nil
	}
}

// --- self_check ---------------------------------------------------------
//
// clientFor (read.go) — and the Pool.For/buildAndLogin it resolves
// through — deliberately couples reachability and login together: a
// cache miss calls EnsureSession immediately, and a login failure comes
// back wrapped into the same generic "resolve fileee client" error a
// network failure would produce. That is the right behaviour for every
// OTHER tool in this package (a caller of list_documents does not care
// WHY its data is unavailable, only that it is), but it is exactly wrong
// for a diagnostic tool whose entire purpose is telling those two things
// apart — a self-check built on clientFor would reproduce the Dockhand
// bug this task exists to avoid: a wrong password reported as "not
// reachable" (Aufgabe C3 auftrag).
//
// self_check therefore never calls clientFor/Pool.For. It resolves the
// caller's account key the same way clientFor does (ResolveAccountKey,
// clientpool/pool.go — a pure, local lookup, no network I/O) and then
// attempts a single, dedicated, uncached login (ProbeLogin, same file) —
// bypassing Pool.For's own cache entirely, since a self-check answering
// from a connection that has been sitting cached for hours would prove
// nothing about the account's state right now.
//
// The result is classified into exactly three states (classifySelfCheckOutcome)
// and self-limited to at most one real login attempt per account per
// selfCheckMinInterval (selfCheckResultFor) — Fileee's own account lock
// on repeated failed logins is the risk that limit exists to avoid
// (auftrag: "Fileee kennt eine eigene Sperre bei zu vielen
// Anmeldeversuchen").

// selfCheckMinInterval bounds how often self_check may attempt a fresh
// login for the SAME resolved account. A call for an account whose last
// real attempt happened less than this ago gets that attempt's own
// result back (Cached: true) instead of triggering a second one — the
// self-limiting behaviour the auftrag requires ("höchstens ein echter
// Versuch je Zeitfenster, danach das zwischengespeicherte Ergebnis mit
// Zeitstempel"). A minute is short enough that a caller re-checking after
// fixing a credential sees a fresh result well within the same
// troubleshooting session, and long enough that no plausible polling
// interval turns this into repeated live login attempts against Fileee.
const selfCheckMinInterval = time.Minute

// probeLoginEndpoint is the Fileee wire endpoint a self_check attempt
// ultimately reaches on failure or success alike — go-fileee's
// Client.Login ends at POST /api/f/login for an account that exists and
// is not blocked (fileee/auth.go); logged as fixed, per-tool metadata,
// the same convention every other endpoint constant in this package
// follows (see read.go's own doc comment on listDocumentsEndpoint).
const probeLoginEndpoint = "POST /api/f/login"

// getSelfCheckInput are self_check's parameters — deliberately empty,
// same reasoning as getRuntimeStatsInput/getToolManifestInput above:
// there is exactly one Fileee account per caller, nothing to select.
type getSelfCheckInput struct{}

// getSelfCheckOutput is self_check's structured result. Reachable and
// AuthValid are deliberately two independent booleans rather than one
// combined flag — collapsing "reachable but the login was rejected" and
// "not reachable at all" into a single bit is exactly the Dockhand bug
// this tool exists to avoid reproducing (see this section's own doc
// comment above). Detail is fixed, human-readable text drawn from
// classifySelfCheckOutcome's own small vocabulary — never the
// counterparty's own error text; that boundary is a dedicated,
// adversarial test (TestSelfCheckGibtNieDenFehlertextDerGegenseiteWeiter,
// ops_test.go), the same guarantee logToolEnd already gives
// get_runtime_stats' own output. SecondsBlocked is the one exception to
// "fixed vocabulary only" — a structured, safe integer straight from
// Fileee's own *fileee.BlockedError, not counterparty text, and the only
// actionable number self_check can give a caller stuck in that state
// (omitted for every other outcome).
type getSelfCheckOutput struct {
	Overall        string `json:"overall"`
	Reachable      bool   `json:"reachable"`
	AuthValid      bool   `json:"authValid"`
	Detail         string `json:"detail"`
	SecondsBlocked int    `json:"secondsBlocked,omitempty"`
	CheckedAt      string `json:"checkedAt"`
	Cached         bool   `json:"cached"`
}

// classifySelfCheckOutcome maps the error a login attempt returned (or
// nil, on success) onto self_check's four states. It never reads err's
// own message — only errors.Is/errors.As against go-fileee's own
// exported sentinels and error types — so nothing Fileee's backend chose
// to put in an error string can ever reach an output field; a network
// failure, an unexpected 5xx, or any other error this function does not
// specifically recognise all fall into the same "down" bucket rather
// than leaking their own text through a default case.
//
//	ok        reachable, login succeeded
//	degraded  reachable, login rejected (wrong password, invalid/expired
//	          two-factor secret) — the case Dockhand itself got wrong
//	blocked   reachable, but Fileee has temporarily locked the account
//	          out after too many attempts (*fileee.BlockedError) —
//	          SecondsBlocked carries how much longer, straight from
//	          Fileee's own response; this must never be reported as
//	          "degraded" (wrong credentials), since a caller told that
//	          would try replacing a correct password, which only makes a
//	          temporary lockout longer
//	down      not reachable at all — network problem, or Fileee itself
//	          unavailable
//
// Deliberately not implemented here: using SecondsBlocked as the actual
// wait time before selfCheckResultFor allows another real attempt
// (selfCheckMinInterval stays fixed regardless of a reported lockout) —
// flagged as an open follow-up, not built into this pass.
func classifySelfCheckOutcome(err error) getSelfCheckOutput {
	switch {
	case err == nil:
		return getSelfCheckOutput{Overall: "ok", Reachable: true, AuthValid: true, Detail: "reachable, login valid"}
	case errors.Is(err, fileee.ErrInvalidCredentials), errors.Is(err, fileee.ErrTwoFactorInvalid):
		return getSelfCheckOutput{Overall: "degraded", Reachable: true, AuthValid: false, Detail: "reachable, login invalid"}
	default:
		var blocked *fileee.BlockedError
		if errors.As(err, &blocked) {
			return getSelfCheckOutput{
				Overall:        "blocked",
				Reachable:      true,
				AuthValid:      false,
				Detail:         "reachable, account temporarily blocked by fileee",
				SecondsBlocked: blocked.SecondsBlocked,
			}
		}
		return getSelfCheckOutput{Overall: "down", Reachable: false, AuthValid: false, Detail: "not reachable"}
	}
}

// probeLoginFunc matches (*clientpool.Pool).ProbeLogin's own signature —
// a method value (p.ProbeLogin) is directly assignable to it with no
// adapter needed, letting selfCheckResultFor's own tests substitute a
// fake login attempt instead (the auftrag's own constraint: no test may
// ever attempt a real Fileee login).
type probeLoginFunc func(ctx context.Context, id *identity.Identity) error

// selfCheckCacheEntry is one account's most recent REAL self_check
// result (never a result already served from cache itself — see
// selfCheckResultFor) together with the time that attempt ran. Kept
// separate from getSelfCheckOutput's own wire shape so the interval
// comparison in selfCheckResultFor works against an actual time.Time,
// not a reparsed copy of formatTime's own (sub-second-lossy) RFC 3339
// string.
type selfCheckCacheEntry struct {
	result getSelfCheckOutput
	at     time.Time
}

// selfCheckCache holds the most recent self_check result per resolved
// account — package-level and mutex-protected, same reasoning as
// runtimeStats above: this belongs to the PROCESS, not to any one
// *mcp.Server instance (ADR-0011, up to sixteen such instances share one
// process), and keyed on the resolved ACCOUNT rather than the caller's
// subject, because Fileee's own login-attempt limit tracks the account,
// not whichever verified identity happened to trigger the check.
var selfCheckCache = struct {
	mu        sync.Mutex
	byAccount map[string]selfCheckCacheEntry
}{byAccount: make(map[string]selfCheckCacheEntry)}

// selfCheckGroup deduplicates concurrent self_check calls for the same
// account onto a single in-flight probe attempt — the same
// singleflight.Group pattern clientpool.Pool already uses for exactly
// this reason (pool.go, bySubject/byAccount). Without it, two callers
// racing in the same instant, both finding no cache entry yet, could
// each start a real login attempt for the same account — precisely the
// "at most one real attempt per window" guarantee selfCheckMinInterval
// alone cannot give under concurrency, since checking the cache and
// writing to it are two separate steps.
var selfCheckGroup singleflight.Group

// selfCheckResultFor is self_check's own logic below identity/account
// resolution — probe is called through this function rather than
// directly by the handler so a test can drive it against a fake
// probeLoginFunc (see this file's own doc comment on probeLoginFunc)
// instead of a live *clientpool.Pool, the same split
// accountStatusFromService (read_account.go) already uses for the same
// reason.
//
// Enforces the self-limit: at most one call to probe per account within
// selfCheckMinInterval, serialised through selfCheckGroup so concurrent
// callers for the same account cannot each trigger their own attempt.
// TestSelfCheckBegrenztSichSelbst (ops_test.go) is the property this
// exists for — two calls in quick succession for the same account must
// produce exactly one call to probe.
func selfCheckResultFor(ctx context.Context, probe probeLoginFunc, id *identity.Identity, account string) getSelfCheckOutput {
	v, _, _ := selfCheckGroup.Do(account, func() (any, error) {
		selfCheckCache.mu.Lock()
		entry, ok := selfCheckCache.byAccount[account]
		selfCheckCache.mu.Unlock()

		now := time.Now()
		if ok && now.Sub(entry.at) < selfCheckMinInterval {
			cached := entry.result
			cached.Cached = true
			return cached, nil
		}

		err := probe(ctx, id)
		result := classifySelfCheckOutcome(err)
		result.CheckedAt = formatTime(now)
		result.Cached = false

		selfCheckCache.mu.Lock()
		selfCheckCache.byAccount[account] = selfCheckCacheEntry{result: result, at: now}
		selfCheckCache.mu.Unlock()

		return result, nil
	})
	return v.(getSelfCheckOutput)
}

// getSelfCheckHandler resolves self_check. Unlike every other tool in
// this package it does not call clientFor — see this section's own doc
// comment above for why — but it mirrors clientFor's own error-wording
// convention exactly (a caller genuinely unknown to the account resolver
// is reported as access denied; any other resolution failure is a plain,
// neutral one), so a caller cannot tell from the wording alone which of
// the two resolution paths served a given failure.
func getSelfCheckHandler(p *clientpool.Pool, logger *slog.Logger) mcp.ToolHandlerFor[getSelfCheckInput, getSelfCheckOutput] {
	return func(ctx context.Context, _ *mcp.CallToolRequest, _ getSelfCheckInput) (*mcp.CallToolResult, getSelfCheckOutput, error) {
		start := time.Now()
		logToolStart(ctx, logger, ToolSelfCheck)

		id, ok := serve.IdentityFrom(ctx)
		if !ok {
			err := fmt.Errorf("fileee-mcp: tools: no verified identity in context")
			logToolEnd(ctx, logger, ToolSelfCheck, start, "", 0, err)
			return nil, getSelfCheckOutput{}, err
		}

		account, err := p.ResolveAccountKey(ctx, id)
		if err != nil {
			if errors.Is(err, accounts.ErrNoAccount) {
				err = fmt.Errorf("fileee-mcp: tools: access denied: %w", err)
			} else {
				err = fmt.Errorf("fileee-mcp: tools: resolve fileee account: %w", err)
			}
			logToolEnd(ctx, logger, ToolSelfCheck, start, "", 0, err)
			return nil, getSelfCheckOutput{}, err
		}

		out := selfCheckResultFor(ctx, p.ProbeLogin, id, account)
		logToolEnd(ctx, logger, ToolSelfCheck, start, probeLoginEndpoint, 1, nil)
		return &mcp.CallToolResult{}, out, nil
	}
}

// registerOpsTools mounts get_runtime_stats, get_tool_manifest and
// self_check onto s — called once from RegisterRead (read.go).
func registerOpsTools(s *mcp.Server, p *clientpool.Pool, logger *slog.Logger) {
	mcp.AddTool(s, &mcp.Tool{
		Name: ToolGetRuntimeStats,
		Description: "Report how many times each of this server's tools has been called since " +
			"this process started, how many of those calls failed, and the outcome kind and " +
			"timestamp of each tool's most recent failure — never the failing call's own error " +
			"text. Returns per-tool call and error counts plus totals across every tool. Use it " +
			"to check whether a particular tool keeps failing, or whether the server is being used " +
			"at all. It does not persist across a restart, does not break results down by caller, " +
			"and does not include the outcome of the very call that returned this snapshot.",
		Annotations: &mcp.ToolAnnotations{ReadOnlyHint: true},
	}, getRuntimeStatsHandler(logger))

	mcp.AddTool(s, &mcp.Tool{
		Name: ToolGetToolManifest,
		Description: "List every tool actually mounted on the server instance answering this call, " +
			"with each tool's name, description and permission group. Returns the total count and " +
			"one entry per tool, always including get_runtime_stats and get_tool_manifest itself. " +
			"Use it to see exactly which tools the calling identity's server instance offers right " +
			"now, for example after a deployment. It does not report tools mounted on a different " +
			"permission-group instance than the one serving this call — the server builds one such " +
			"instance per reachable capability combination, and each caller only ever reaches its " +
			"own — and it does not claim this set is everything this server will ever offer, only " +
			"what is registered in the build answering this particular call.",
		Annotations: &mcp.ToolAnnotations{ReadOnlyHint: true},
	}, getToolManifestHandler(s, logger))

	mcp.AddTool(s, &mcp.Tool{
		Name: ToolSelfCheck,
		Description: "Check whether this server can currently reach Fileee and log in with the " +
			"calling identity's configured credentials, reporting reachability and login validity " +
			"as two independent signals instead of one combined yes/no. Returns one of four " +
			"outcomes: ok (reachable, login valid), degraded (reachable, but the login itself was " +
			"rejected — a wrong or expired password or two-factor secret, not a network problem), " +
			"blocked (reachable, but Fileee has temporarily locked the account out after too many " +
			"attempts — secondsBlocked tells you how much longer), or down (not reachable at all) " +
			"— plus a fixed detail text, when the underlying real check last ran, and whether this " +
			"call reused that result. Use it to tell a broken credential apart from a temporary " +
			"account lockout or a network/Fileee outage without guessing — do not treat a blocked " +
			"result as a wrong credential, retrying with a different password only extends the " +
			"lockout. It attempts at most one real login per resolved account within a short " +
			"window to avoid triggering Fileee's own account lock, reusing the cached result for " +
			"calls inside that window, and it never returns the counterparty's own error text — " +
			"only this fixed classification.",
		Annotations: &mcp.ToolAnnotations{ReadOnlyHint: true},
	}, getSelfCheckHandler(p, logger))
}
