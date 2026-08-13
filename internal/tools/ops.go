// ops.go carries this server's operational tools — get_runtime_stats
// (Aufgabe C1) and get_tool_manifest (Aufgabe C2), a third,
// self_check, follows in a later task. All three are read-only
// (ReadOnlyHint: true) and never touch Fileee account data, unlike every
// other tool RegisterRead mounts — see each tool's own doc comment for
// why.
//
// The counters get_runtime_stats reports hang off logToolEnd
// (read.go) — the one place all three registration families
// (hand-written, generic, sync) already funnel through since Antrag #47,
// not something this file has to duplicate at three separate call sites.
// See recordToolCall's own doc comment for why that matters.
package tools

import (
	"context"
	"log/slog"
	"sort"
	"sync"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

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
//
// Wired in Aufgabe C2 — see that task's own commit for listMountedTools,
// getToolManifestHandler and their registration.

// registerOpsTools mounts get_runtime_stats onto s — called once from
// RegisterRead (read.go). p is currently unused by get_runtime_stats
// itself (see getRuntimeStatsHandler's own doc comment on why) but kept
// in this signature to match every other registerXxxTools function in
// this package; self_check (a later task, same file) needs it for its own
// dedicated login attempt.
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
}
