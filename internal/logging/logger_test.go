package logging

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
	"time"
)

// TestJSONDurationsUseGoSyntax: the JSON handler used to write
// "interval":60000000000. A duration must read the way it is configured, or
// an operator comparing the log with the config has to count zeros.
func TestJSONDurationsUseGoSyntax(t *testing.T) {
	var buf bytes.Buffer
	NewWithWriter(&buf, "json", "info").Info("service starting",
		"interval", time.Minute,
		"group", map[string]any{"untouched": 1}, // non-duration values pass through
	)
	NewWithWriter(&buf, "json", "info").WithGroup("scan").Info("nested", "dial_timeout", 800*time.Millisecond)

	lines := strings.Split(strings.TrimSpace(buf.String()), "\n")
	if len(lines) != 2 {
		t.Fatalf("got %d log lines, want 2: %q", len(lines), buf.String())
	}

	var top map[string]any
	if err := json.Unmarshal([]byte(lines[0]), &top); err != nil {
		t.Fatalf("not JSON: %v", err)
	}
	if top["interval"] != "1m0s" {
		t.Errorf("interval = %#v, want \"1m0s\"", top["interval"])
	}

	// Durations inside a group are rewritten too: ReplaceAttr sees nested
	// attributes, and a log line must not mix both notations.
	var nested struct {
		Scan map[string]any `json:"scan"`
	}
	if err := json.Unmarshal([]byte(lines[1]), &nested); err != nil {
		t.Fatalf("not JSON: %v", err)
	}
	if got := nested.Scan["dial_timeout"]; got != "800ms" {
		t.Errorf("scan.dial_timeout = %#v, want \"800ms\"", got)
	}
}

// TestTextAndJSONRenderDurationsAlike: both formats must show the same value,
// so switching log_format does not change how a log is searched.
func TestTextAndJSONRenderDurationsAlike(t *testing.T) {
	var buf bytes.Buffer
	NewWithWriter(&buf, "text", "info").Info("x", "elapsed", 1500*time.Millisecond)
	if !strings.Contains(buf.String(), "elapsed=1.5s") {
		t.Errorf("text output %q does not contain elapsed=1.5s", buf.String())
	}
}

// TestLevelFiltering covers the documented levels and the fallback: anything
// unknown is info. Config validation rejects unknown levels, so the fallback
// only matters for callers outside it.
func TestLevelFiltering(t *testing.T) {
	cases := []struct {
		level             string
		debugOut, infoOut bool
	}{
		{"debug", true, true},
		{"DEBUG", true, true},
		{"info", false, true},
		{"error", false, false},
		{"bogus", false, true},
	}
	for _, tc := range cases {
		t.Run(tc.level, func(t *testing.T) {
			var buf bytes.Buffer
			l := NewWithWriter(&buf, "json", tc.level)
			l.Debug("d")
			if got := buf.Len() > 0; got != tc.debugOut {
				t.Errorf("debug written = %v, want %v", got, tc.debugOut)
			}
			buf.Reset()
			l.Info("i")
			if got := buf.Len() > 0; got != tc.infoOut {
				t.Errorf("info written = %v, want %v", got, tc.infoOut)
			}
		})
	}
}
