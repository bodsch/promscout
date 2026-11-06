package discovery

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"net"
	"net/http/httptest"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"bodsch.me/promscout/internal/config"
	"bodsch.me/promscout/internal/logging"

	"github.com/prometheus/client_golang/prometheus"
)

// loggedScheduler is schedulerWithCache with a JSON logger at info level
// writing into the returned buffer — the level operators run with.
func loggedScheduler(t *testing.T, ts *httptest.Server, cacheFile string) (*Scheduler, *bytes.Buffer) {
	t.Helper()

	_, portStr, err := net.SplitHostPort(ts.Listener.Addr().String())
	if err != nil {
		t.Fatalf("split host port: %v", err)
	}
	port, err := strconv.Atoi(portStr)
	if err != nil {
		t.Fatalf("atoi: %v", err)
	}

	var buf bytes.Buffer
	logger := logging.NewWithWriter(&buf, "json", "info")
	cfg := config.Config{
		NetworkCIDR: "127.0.0.1/32",
		Ports:       []int{port},
		DialTimeout: 500 * time.Millisecond,
		Timeout:     time.Second,
		MaxWorkers:  4,
	}

	s := NewScheduler(
		NewScanner(cfg, logger),
		NewValidator(cfg.Timeout, []string{"/"}, logger),
		time.Minute, 4, cacheFile, prometheus.NewRegistry(), logger,
	)
	return s, &buf
}

// logLines decodes the JSON log records written so far and empties buf.
func logLines(t *testing.T, buf *bytes.Buffer) []map[string]any {
	t.Helper()

	var lines []map[string]any
	sc := bufio.NewScanner(buf)
	for sc.Scan() {
		var m map[string]any
		if err := json.Unmarshal(sc.Bytes(), &m); err != nil {
			t.Fatalf("log line is not JSON: %q", sc.Text())
		}
		lines = append(lines, m)
	}
	buf.Reset()
	return lines
}

// withMsg returns the records whose msg equals msg.
func withMsg(lines []map[string]any, msg string) []map[string]any {
	var out []map[string]any
	for _, l := range lines {
		if l["msg"] == msg {
			out = append(out, l)
		}
	}
	return out
}

// TestTargetChangesAreLoggedWithTheirAddress is the operator's question this
// feature exists for: the dashboard shows the target count go up by one and,
// hours later, down by one — which host was it?
//
// The previous set comes from the warm-start cache, so this also covers a
// change that happened while PromScout was not running. Before, a cycle
// replaced the target set silently and the answer was nowhere.
func TestTargetChangesAreLoggedWithTheirAddress(t *testing.T) {
	ts := promServer(t)
	gone := Target{Address: "192.0.2.1:9100", Path: "/metrics", Exporter: "node", Version: "1.7.0", Source: sourceBuildInfo}

	cache := filepath.Join(t.TempDir(), "cache.json")
	if err := saveTargets(cache, []Target{gone}); err != nil {
		t.Fatalf("seed cache: %v", err)
	}

	s, buf := loggedScheduler(t, ts, cache)
	_ = logLines(t, buf) // drop the cache-load line
	s.run(context.Background())
	lines := logLines(t, buf)

	added := withMsg(lines, "target added")
	if len(added) != 1 || added[0]["target"] != ts.Listener.Addr().String() || added[0]["change"] != ChangeAdded {
		t.Errorf("'target added' lines = %v, want one for %s", added, ts.Listener.Addr())
	}
	removed := withMsg(lines, "target removed")
	if len(removed) != 1 || removed[0]["target"] != gone.Address || removed[0]["exporter"] != "node" ||
		removed[0]["version"] != "1.7.0" {
		t.Errorf("'target removed' lines = %v, want one for %s with its exporter and version", removed, gone.Address)
	}
	if sum := withMsg(lines, "target set changed"); len(sum) != 1 || sum[0]["added"] != 1.0 || sum[0]["removed"] != 1.0 {
		t.Errorf("summary lines = %v, want one with added=1 removed=1", sum)
	}

	if got := counterValue(t, s.targetChangesTotal.WithLabelValues(ChangeAdded)); got != 1 {
		t.Errorf("target_changes_total{change=added} = %v, want 1", got)
	}
	if got := counterValue(t, s.targetChangesTotal.WithLabelValues(ChangeRemoved)); got != 1 {
		t.Errorf("target_changes_total{change=removed} = %v, want 1", got)
	}
	if got := counterValue(t, s.lastTargetChange); got == 0 {
		t.Error("last_target_change_timestamp is 0 after a change")
	}

	// The same result again is not a change: no lines, no counts. A line per
	// cycle would bury the real changes within a day.
	s.run(context.Background())
	if again := logLines(t, buf); len(again) != 0 {
		t.Errorf("an unchanged cycle logged %v, want nothing at info level", again)
	}
	if got := counterValue(t, s.targetChangesTotal.WithLabelValues(ChangeAdded)); got != 1 {
		t.Errorf("target_changes_total{change=added} = %v after an unchanged cycle, want still 1", got)
	}
}

// TestFirstCycleWithoutBaselineIsNotCountedAsAdditions: without a cache there
// is nothing to compare against. Counting the first result as additions would
// make every restart look like all services appeared at once — on the
// dashboard and in any alert on the counter.
func TestFirstCycleWithoutBaselineIsNotCountedAsAdditions(t *testing.T) {
	s, buf := loggedScheduler(t, promServer(t), "")
	s.run(context.Background())
	lines := logLines(t, buf)

	if init := withMsg(lines, "initial target set published"); len(init) != 1 || init[0]["targets"] != 1.0 {
		t.Errorf("initial summary = %v, want one line with targets=1", init)
	}
	if added := withMsg(lines, "target added"); len(added) != 0 {
		t.Errorf("the first cycle logged %d additions without a baseline", len(added))
	}
	for _, kind := range AllChangeKinds {
		if got := counterValue(t, s.targetChangesTotal.WithLabelValues(kind)); got != 0 {
			t.Errorf("target_changes_total{change=%s} = %v after the first cycle, want 0", kind, got)
		}
	}
}

// TestCancelledCycleReportsNoChanges: a cancelled cycle publishes nothing
// (TestCancelledCycleKeepsTheWarmStartCache), so it must not report the
// targets it never got to as removed — that would be a false trail in exactly
// the log this feature is for.
func TestCancelledCycleReportsNoChanges(t *testing.T) {
	cache := filepath.Join(t.TempDir(), "cache.json")
	if err := saveTargets(cache, []Target{{Address: "192.0.2.1:9100", Path: "/metrics"}}); err != nil {
		t.Fatalf("seed cache: %v", err)
	}

	s, buf := loggedScheduler(t, promServer(t), cache)
	_ = logLines(t, buf)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	s.run(ctx)

	if lines := logLines(t, buf); len(lines) != 0 {
		t.Errorf("a cancelled cycle logged %v", lines)
	}
	if got := counterValue(t, s.targetChangesTotal.WithLabelValues(ChangeRemoved)); got != 0 {
		t.Errorf("target_changes_total{change=removed} = %v after a cancelled cycle, want 0", got)
	}
}
