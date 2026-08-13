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
	"fmt"
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

// registerOpsTools mounts get_runtime_stats and get_tool_manifest onto s
// — called once from RegisterRead (read.go). p is currently unused by
// either tool (see their own handler doc comments on why) but kept in
// this signature to match every other registerXxxTools function in this
// package; self_check (a later task, same file) needs it for its own
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
}
