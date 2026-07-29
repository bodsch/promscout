package discovery

import (
	"context"
	"net"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"
	"time"

	"bodsch.me/promscout/internal/config"
	"bodsch.me/promscout/internal/logging"

	"github.com/prometheus/client_golang/prometheus"
)

// newTestScheduler wires a scheduler against a scanner/validator for the
// loopback address of ts, using a fresh registry per test so metric
// registration never collides.
func newTestScheduler(t *testing.T, ts *httptest.Server, paths []string) *Scheduler {
	t.Helper()

	_, portStr, err := net.SplitHostPort(ts.Listener.Addr().String())
	if err != nil {
		t.Fatalf("split host port: %v", err)
	}
	port, err := strconv.Atoi(portStr)
	if err != nil {
		t.Fatalf("atoi: %v", err)
	}

	logger := logging.New("text", "error")
	cfg := config.Config{
		NetworkCIDR: "127.0.0.1/32",
		Ports:       []int{port},
		DialTimeout: 500 * time.Millisecond,
		Timeout:     time.Second,
		MaxWorkers:  4,
	}

	scanner := NewScanner(cfg, logger)
	validator := NewValidator(cfg.Timeout, paths, logger)

	return NewScheduler(
		scanner,
		validator,
		time.Minute,
		4,
		"", // no cache
		prometheus.NewRegistry(),
		logger,
	)
}

func TestSchedulerRunDiscoversAndBecomesReady(t *testing.T) {
	const promBody = "# HELP go_info x\n# TYPE go_info gauge\ngo_info 1\n"

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/metrics" {
			_, _ = w.Write([]byte(promBody))
			return
		}
		http.NotFound(w, r)
	}))
	defer ts.Close()

	s := newTestScheduler(t, ts, []string{"/", "/metrics"})

	if s.Ready() {
		t.Fatal("scheduler reported ready before any cycle ran")
	}

	s.run(context.Background())

	if !s.Ready() {
		t.Fatal("scheduler not ready after a completed cycle")
	}

	targets := s.Targets()
	if len(targets) != 1 {
		t.Fatalf("Targets() = %v, want exactly one", targets)
	}
	if targets[0].Path != "/metrics" {
		t.Errorf("target path = %q, want /metrics", targets[0].Path)
	}
}

func TestSchedulerRunNoValidTargets(t *testing.T) {
	// Server never serves valid metrics → no targets, but a cycle still
	// completes and readiness flips.
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("<html><body>nope</body></html>"))
	}))
	defer ts.Close()

	s := newTestScheduler(t, ts, []string{"/"})
	s.run(context.Background())

	if !s.Ready() {
		t.Fatal("scheduler not ready after a completed cycle")
	}
	if got := s.Targets(); len(got) != 0 {
		t.Fatalf("Targets() = %v, want none", got)
	}
}

func TestSchedulerTargetsSnapshotIsCopy(t *testing.T) {
	const promBody = "go_info 1\n"

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(promBody))
	}))
	defer ts.Close()

	s := newTestScheduler(t, ts, []string{"/"})
	s.run(context.Background())

	snap := s.Targets()
	if len(snap) != 1 {
		t.Fatalf("expected one target, got %v", snap)
	}
	// Mutating the returned slice must not affect internal state.
	snap[0].Address = "tampered"

	if again := s.Targets(); again[0].Address == "tampered" {
		t.Error("Targets() leaked internal slice; mutation was visible")
	}
}
