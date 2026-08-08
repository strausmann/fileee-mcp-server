// White-box unit tests for read.go's small, pure helpers — clampLimit,
// nonNegative, formatTime. read_test.go is package tools_test (it only
// ever needs the exported RegisterRead/ReadToolKinds surface, driven
// through a real MCP client); these three helpers are unexported and
// have no other caller that exercises non-default inputs, so they need
// their own, separate, white-box file — a review finding on this
// package's first version (all three sat at 50-67% coverage: nothing
// ever fed them anything but zero values or a single real timestamp).
package tools

import (
	"testing"
	"time"
)

func TestClampLimit(t *testing.T) {
	tests := []struct {
		name      string
		requested int
		want      int
	}{
		{"zero uses the default", 0, defaultLimit},
		{"negative uses the default", -1, defaultLimit},
		{"large negative uses the default", -1000, defaultLimit},
		{"within range is passed through unchanged", 5, 5},
		{"exactly the default is passed through unchanged", defaultLimit, defaultLimit},
		{"exactly the max is passed through unchanged", maxLimit, maxLimit},
		{"just above the max is capped", maxLimit + 1, maxLimit},
		{"far above the max is capped", 1_000_000, maxLimit},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := clampLimit(tt.requested); got != tt.want {
				t.Errorf("clampLimit(%d) = %d, want %d", tt.requested, got, tt.want)
			}
		})
	}
}

func TestNonNegative(t *testing.T) {
	tests := []struct {
		name string
		n    int
		want int
	}{
		{"negative floors at zero", -1, 0},
		{"large negative floors at zero", -1000, 0},
		{"zero is unchanged", 0, 0},
		{"positive is unchanged", 42, 42},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := nonNegative(tt.n); got != tt.want {
				t.Errorf("nonNegative(%d) = %d, want %d", tt.n, got, tt.want)
			}
		})
	}
}

func TestFormatTime(t *testing.T) {
	fixed := time.Date(2026, time.August, 8, 12, 30, 0, 0, time.UTC)

	tests := []struct {
		name string
		t    time.Time
		want string
	}{
		{"zero value renders as an absent field", time.Time{}, ""},
		{"a real timestamp renders as RFC 3339", fixed, "2026-08-08T12:30:00Z"},
		{"a non-UTC timestamp keeps its own offset, not converted to UTC",
			fixed.In(time.FixedZone("CET", 3600)), "2026-08-08T13:30:00+01:00"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := formatTime(tt.t); got != tt.want {
				t.Errorf("formatTime(%v) = %q, want %q", tt.t, got, tt.want)
			}
		})
	}
}
