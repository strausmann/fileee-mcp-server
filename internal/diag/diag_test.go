package diag_test

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"strings"
	"testing"

	"github.com/strausmann/fileee-mcp-server/internal/diag"
)

// decodeLines splits buf's JSON-lines output (one object per log.Logger
// call, see slog.NewJSONHandler) into the decoded objects — the shape
// every test in this file asserts against, rather than string-matching
// raw output.
func decodeLines(t *testing.T, buf *bytes.Buffer) []map[string]any {
	t.Helper()
	var out []map[string]any
	for _, line := range strings.Split(strings.TrimRight(buf.String(), "\n"), "\n") {
		if line == "" {
			continue
		}
		var m map[string]any
		if err := json.Unmarshal([]byte(line), &m); err != nil {
			t.Fatalf("decodeLines: line %q is not valid JSON: %v", line, err)
		}
		out = append(out, m)
	}
	return out
}

// --- New: the two-stage level gate ------------------------------------

// TestNewGatesDebugRecordsByLevel is the reason FILEEE_LOG_LEVEL exists at
// all (see internal/config, LoadConfig's LogLevel doc comment before this
// task): a debug-level call must be silent at LevelInfo and visible at
// LevelDebug, while an info-level call is always visible.
func TestNewGatesDebugRecordsByLevel(t *testing.T) {
	tests := []struct {
		name       string
		level      diag.Level
		wantDebug  bool
		wantRecord int // total records expected (one info + maybe one debug)
	}{
		{"info level: debug is suppressed", diag.LevelInfo, false, 1},
		{"debug level: debug is shown", diag.LevelDebug, true, 2},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var buf bytes.Buffer
			logger := diag.New(tt.level, &buf)

			logger.Info("tool call succeeded", "tool", "list_documents")
			logger.Debug("tool call: arguments", "tool", "list_documents", "limit", 20)

			lines := decodeLines(t, &buf)
			if len(lines) != tt.wantRecord {
				t.Fatalf("got %d log lines, want %d: %v", len(lines), tt.wantRecord, lines)
			}

			sawDebug := false
			for _, l := range lines {
				if l["msg"] == "tool call: arguments" {
					sawDebug = true
				}
			}
			if sawDebug != tt.wantDebug {
				t.Errorf("debug record present = %v, want %v", sawDebug, tt.wantDebug)
			}
		})
	}
}

// --- redaction: the chokepoint every attribute passes through ----------

// TestNewRedactsForbiddenKeysRegardlessOfLevel is the masking guarantee
// itself: a value logged under a key that looks like a credential must
// never reach the output verbatim, whether it is logged at info or debug,
// and however the key is capitalised or composed — this is deliberately a
// substring/case-insensitive match (see internal/diag.go's own doc
// comment on forbiddenKeyFragments): it is safer to over-redact than to
// miss a variant no call site anticipated.
func TestNewRedactsForbiddenKeysRegardlessOfLevel(t *testing.T) {
	tests := []struct {
		key   string
		level diag.Level
	}{
		{"password", diag.LevelInfo},
		{"Password", diag.LevelDebug},
		{"FILEEE_PASSWORD", diag.LevelDebug},
		{"totp_seed", diag.LevelDebug},
		{"TOTPSeed", diag.LevelDebug},
		{"api_token", diag.LevelInfo},
		{"apiKey", diag.LevelDebug},
		{"authorization", diag.LevelInfo},
		{"session_cookie", diag.LevelDebug},
		{"client_secret", diag.LevelDebug},
		{"credentials", diag.LevelInfo},
	}
	for _, tt := range tests {
		t.Run(tt.key, func(t *testing.T) {
			var buf bytes.Buffer
			logger := diag.New(tt.level, &buf)

			const secretValue = "hunter2-do-not-log-me"
			logger.Log(context.Background(), slogLevelFor(tt.level), "test record", tt.key, secretValue)

			if strings.Contains(buf.String(), secretValue) {
				t.Fatalf("output contains the raw secret value for key %q: %s", tt.key, buf.String())
			}
			lines := decodeLines(t, &buf)
			if len(lines) != 1 {
				t.Fatalf("got %d lines, want 1: %v", len(lines), lines)
			}
			if got := lines[0][tt.key]; got != "***" {
				t.Errorf("lines[0][%q] = %v, want the redaction placeholder %q", tt.key, got, "***")
			}
		})
	}
}

// slogLevelFor mirrors diag.New's own internal level mapping, just enough
// for this test to log at exactly the level New would gate open for tt.level.
func slogLevelFor(level diag.Level) slog.Level {
	if level == diag.LevelDebug {
		return slog.LevelDebug
	}
	return slog.LevelInfo
}

// TestNewLeavesOrdinaryKeysUnredacted is the counterpart to the redaction
// test above: this package must not become so aggressive that it also
// swallows the very data the diagnostic log exists to show (tool name,
// duration, a search term).
func TestNewLeavesOrdinaryKeysUnredacted(t *testing.T) {
	var buf bytes.Buffer
	logger := diag.New(diag.LevelDebug, &buf)

	logger.Debug("tool call: arguments", "tool", "search_documents", "term", "invoice", "limit", 20)

	lines := decodeLines(t, &buf)
	if len(lines) != 1 {
		t.Fatalf("got %d lines, want 1: %v", len(lines), lines)
	}
	if lines[0]["term"] != "invoice" {
		t.Errorf(`lines[0]["term"] = %v, want "invoice" — an ordinary field must not be redacted`, lines[0]["term"])
	}
	if lines[0]["tool"] != "search_documents" {
		t.Errorf(`lines[0]["tool"] = %v, want "search_documents"`, lines[0]["tool"])
	}
}

// TestNewRedactsNestedGroupAttributes covers slog.Group nesting: a caller
// logging a call's arguments as a group (internal/tools does exactly this
// for debug-level argument logging) must have a forbidden key inside that
// group redacted exactly as a top-level one would be — the masking
// guarantee this package promises is "regardless of level", and a nested
// group is one of the shapes that guarantee has to survive.
func TestNewRedactsNestedGroupAttributes(t *testing.T) {
	var buf bytes.Buffer
	logger := diag.New(diag.LevelDebug, &buf)

	logger.LogAttrs(context.Background(), slog.LevelDebug, "tool call: arguments",
		slog.Group("args", slog.String("term", "invoice"), slog.String("api_token", "should-not-appear")))

	if strings.Contains(buf.String(), "should-not-appear") {
		t.Fatalf("output contains the raw secret value nested inside a group: %s", buf.String())
	}
	lines := decodeLines(t, &buf)
	group, ok := lines[0]["args"].(map[string]any)
	if !ok {
		t.Fatalf(`lines[0]["args"] = %#v, want a nested object`, lines[0]["args"])
	}
	if group["api_token"] != "***" {
		t.Errorf(`group["api_token"] = %v, want the redaction placeholder`, group["api_token"])
	}
	if group["term"] != "invoice" {
		t.Errorf(`group["term"] = %v, want "invoice"`, group["term"])
	}
}

// TestNewRedactsAttributesAddedViaWith covers the other path an attribute
// can reach the handler through: logger.With(...), not just a single
// Log/Info/Debug call — slog.Handler.WithAttrs is a separate method this
// package's handler must also route through the same masking, or a
// caller building a child logger with With("token", ...) would bypass it
// entirely.
func TestNewRedactsAttributesAddedViaWith(t *testing.T) {
	var buf bytes.Buffer
	logger := diag.New(diag.LevelInfo, &buf).With("session_token", "should-not-appear")

	logger.Info("tool call succeeded", "tool", "list_documents")

	if strings.Contains(buf.String(), "should-not-appear") {
		t.Fatalf("output contains the raw secret value attached via With: %s", buf.String())
	}
	lines := decodeLines(t, &buf)
	if lines[0]["session_token"] != "***" {
		t.Errorf(`lines[0]["session_token"] = %v, want the redaction placeholder`, lines[0]["session_token"])
	}
}

// TestNewRedactsAttributesAfterWithGroup covers slog.Logger.WithGroup: a
// child logger built via WithGroup must keep going through this package's
// masking handler for every record logged afterwards — an unwrapped
// base.WithGroup(name) (skipping redactingHandler.WithGroup) would return
// the base JSON handler's own concrete type directly, and every
// subsequent Handle/WithAttrs call on it would bypass redaction entirely.
func TestNewRedactsAttributesAfterWithGroup(t *testing.T) {
	var buf bytes.Buffer
	logger := diag.New(diag.LevelDebug, &buf).WithGroup("call")

	logger.Debug("tool call: arguments", "tool", "list_documents", "api_token", "should-not-appear")

	if strings.Contains(buf.String(), "should-not-appear") {
		t.Fatalf("output contains the raw secret value after WithGroup: %s", buf.String())
	}
	lines := decodeLines(t, &buf)
	group, ok := lines[0]["call"].(map[string]any)
	if !ok {
		t.Fatalf(`lines[0]["call"] = %#v, want a nested object named after the group`, lines[0]["call"])
	}
	if group["api_token"] != "***" {
		t.Errorf(`group["api_token"] = %v, want the redaction placeholder`, group["api_token"])
	}
	if group["tool"] != "list_documents" {
		t.Errorf(`group["tool"] = %v, want "list_documents"`, group["tool"])
	}
}
