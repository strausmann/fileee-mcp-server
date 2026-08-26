// Package diag builds this server's diagnostic logger.
//
// Before this package existed, the server logged nothing about a tool
// call at all: which tool ran, whether it worked, how the request was
// forwarded to Fileee. FILEEE_LOG_LEVEL was loaded (internal/config) but
// had no consumer — see that package's own LogLevel doc comment for the
// history.
//
// What New returns has two properties, both load-bearing:
//
//   - Level-gated in two stages (see Level): the default, LevelInfo, is
//     metadata about a call — tool name, duration, outcome, the Fileee
//     endpoint reached and its HTTP status, a result count, a resolved
//     capability set. LevelDebug adds the caller-supplied tool arguments
//     on top.
//   - Masked at a single chokepoint every attribute passes through
//     (redactingHandler, below), regardless of which package logged it,
//     regardless of level: any attribute whose key looks like it carries
//     a credential is replaced with a fixed placeholder before it ever
//     reaches the log output. This is deliberate defense in depth — the
//     operator who asked for this diagnostic log was explicit that it
//     must never depend on every future call site remembering which
//     field names are safe (see the task this package was built for).
//     Document content and Fileee response bodies are a related but
//     different concern this package cannot enforce by itself: nothing
//     here can tell a document title from an ordinary string. That
//     boundary is upheld by never handing such values to this logger in
//     the first place (see internal/tools, classifyErr) — this package
//     only guarantees the credential-shaped half.
package diag

import (
	"context"
	"io"
	"log/slog"
	"strings"
)

// Level selects how much this server's diagnostic logger reports about
// each tool call.
type Level string

const (
	// LevelInfo is the default stage: tool name, duration, outcome
	// (success, or a fixed-vocabulary kind of failure — never Fileee's
	// own response text), the Fileee endpoint a call reached and its
	// HTTP status, how many results a successful call returned, and
	// which capability set/tool count Gangway's selector resolved for a
	// caller. Never a caller-supplied argument value.
	LevelInfo Level = "info"
	// LevelDebug adds the caller-supplied tool arguments (search terms,
	// paging offsets, document IDs) to everything LevelInfo already
	// logs — still subject to this package's masking (see the package
	// doc comment), still never a document's content or a Fileee
	// response body.
	LevelDebug Level = "debug"
)

// forbiddenKeyFragments are lower-cased substrings that must never appear
// verbatim as a logged attribute's value, whatever key carries it and
// whatever level it is logged at. Matched as a substring rather than an
// exact key, deliberately over-inclusive: it is far cheaper to redact one
// field too many than to let one secret-shaped field through because its
// name did not match exactly (e.g. a future FILEEE_ACCOUNT_<KEY>_TOTP_SEED
// forwarded verbatim as "totpSeed" or "TOTP_Seed" — see
// TestNewRedactsForbiddenKeysRegardlessOfLevel for the exact variants this
// covers today).
var forbiddenKeyFragments = []string{
	"password",
	"secret",
	"totp",
	"seed",
	"token",
	"authorization",
	"apikey",
	"api_key",
	"credential",
	"cookie",
}

// redactedValue replaces a matched attribute's value. A fixed marker
// rather than omitting the attribute entirely: an operator reading the
// log can see that a value existed and was withheld, not that the field
// was simply never populated — the two look identical only if the
// placeholder is missing.
const redactedValue = "***"

// New builds this server's diagnostic logger: out receives one JSON
// object per log line — structured, unlike the plain
// fmt.Fprintf-to-stdout/stderr lines cmd/fileee-mcp-server/main.go's own
// startup/shutdown messages use, which this package does not replace —
// gated at level, wrapped in the masking guarantee this package's own
// doc comment describes.
func New(level Level, out io.Writer) *slog.Logger {
	base := slog.NewJSONHandler(out, &slog.HandlerOptions{Level: slogLevel(level)})
	return slog.New(&redactingHandler{base: base})
}

// slogLevel maps this package's own two-stage Level onto the standard
// library's finer-grained slog.Level. Anything other than LevelDebug
// resolves to slog.LevelInfo — including an empty Level a caller built by
// hand rather than through config.LoadConfig (which already rejects
// anything but LevelInfo/LevelDebug, see internal/config, LoadConfig): a
// logger built with no valid level configured should default to the
// quieter stage, not the noisier one.
func slogLevel(level Level) slog.Level {
	if level == LevelDebug {
		return slog.LevelDebug
	}
	return slog.LevelInfo
}

// redactingHandler wraps a base slog.Handler and rewrites every attribute
// whose key matches isForbiddenKey to redactedValue before delegating to
// base — the single chokepoint every attribute passes through, mentioned
// in this package's own doc comment, regardless of which call site
// produced it (a single Info/Debug call, or an attribute attached earlier
// via slog.Logger.With) or what level it was logged at.
type redactingHandler struct {
	base slog.Handler
}

// Enabled defers to base — this handler adds masking, not its own level
// policy; New's slog.HandlerOptions.Level on base is what actually gates
// LevelInfo vs LevelDebug.
func (h *redactingHandler) Enabled(ctx context.Context, level slog.Level) bool {
	return h.base.Enabled(ctx, level)
}

// Handle rebuilds r's attributes through redact before delegating — a
// fresh slog.Record rather than mutating r in place, since slog.Record's
// own attribute storage is not meant to be rewritten after the fact (see
// slog.Record's doc comment on Clone for the same concern in the
// opposite direction).
func (h *redactingHandler) Handle(ctx context.Context, r slog.Record) error {
	nr := slog.NewRecord(r.Time, r.Level, r.Message, r.PC)
	r.Attrs(func(a slog.Attr) bool {
		nr.AddAttrs(redact(a))
		return true
	})
	return h.base.Handle(ctx, nr)
}

// WithAttrs redacts attrs before handing them to base — the other path an
// attribute can reach a handler through besides a single log call (see
// slog.Logger.With). Skipping this method would let a child logger built
// with, say, With("session_token", tok) bypass the masking entirely: those
// attributes are stored on the handler itself, not passed through Handle
// again for each subsequent record.
func (h *redactingHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	out := make([]slog.Attr, len(attrs))
	for i, a := range attrs {
		out[i] = redact(a)
	}
	return &redactingHandler{base: h.base.WithAttrs(out)}
}

// WithGroup keeps this handler in the wrapper chain — an unwrapped
// h.base.WithGroup(name) would return base's own concrete handler type,
// and every later Handle/WithAttrs call on the result would skip
// redaction entirely.
func (h *redactingHandler) WithGroup(name string) slog.Handler {
	return &redactingHandler{base: h.base.WithGroup(name)}
}

// redact masks a's value if a.Key matches isForbiddenKey, and recurses
// into a group-typed value (slog.KindGroup — e.g. a call's arguments
// logged via slog.Group, see internal/tools) so a forbidden key nested
// inside one is caught exactly as one at the top level would be.
func redact(a slog.Attr) slog.Attr {
	a.Value = a.Value.Resolve()
	if isForbiddenKey(a.Key) {
		return slog.String(a.Key, redactedValue)
	}
	if a.Value.Kind() != slog.KindGroup {
		return a
	}
	group := a.Value.Group()
	out := make([]slog.Attr, len(group))
	for i, ga := range group {
		out[i] = redact(ga)
	}
	return slog.Attr{Key: a.Key, Value: slog.GroupValue(out...)}
}

// isForbiddenKey reports whether key contains any of forbiddenKeyFragments,
// case-insensitively.
func isForbiddenKey(key string) bool {
	lower := strings.ToLower(key)
	for _, frag := range forbiddenKeyFragments {
		if strings.Contains(lower, frag) {
			return true
		}
	}
	return false
}
