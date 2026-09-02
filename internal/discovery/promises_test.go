package discovery

import (
	"context"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"sync"
	"testing"
	"time"

	"bodsch.me/promscout/internal/config"
	"bodsch.me/promscout/internal/logging"
	"github.com/prometheus/client_golang/prometheus"
	dto "github.com/prometheus/client_model/go"
)

// counterValue reads a collector's single value.
//
// Written out rather than pulled from prometheus/testutil, which would add a
// dependency to the module for one helper.
func counterValue(t *testing.T, c prometheus.Collector) float64 {
	t.Helper()

	ch := make(chan prometheus.Metric, 1)
	c.Collect(ch)
	close(ch)

	m, ok := <-ch
	if !ok {
		t.Fatal("the collector produced no metric")
	}

	var pb dto.Metric
	if err := m.Write(&pb); err != nil {
		t.Fatalf("writing the metric: %v", err)
	}
	switch {
	case pb.Counter != nil:
		return pb.Counter.GetValue()
	case pb.Gauge != nil:
		return pb.Gauge.GetValue()
	default:
		t.Fatalf("the metric is neither a counter nor a gauge")
		return 0
	}
}

// schedulerWithCache is newTestScheduler with a cache file, which is the
// configuration the warm start exists for.
func schedulerWithCache(t *testing.T, ts *httptest.Server, cacheFile string) *Scheduler {
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

	return NewScheduler(
		NewScanner(cfg, logger),
		NewValidator(cfg.Timeout, []string{"/"}, logger),
		time.Minute, 4, cacheFile, prometheus.NewRegistry(), logger,
	)
}

func promServer(t *testing.T) *httptest.Server {
	t.Helper()

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("# HELP go_info x\n# TYPE go_info gauge\ngo_info 1\n"))
	}))
	t.Cleanup(ts.Close)
	return ts
}

// TestCancelledCycleKeepsTheWarmStartCache is the failure the warm start was
// built to prevent, caused by the warm start's own code path.
//
// A shutdown cancels the context while a cycle is very likely still running: the
// cycle runs in its own goroutine and a /24 across several ports takes minutes.
// The scanner closes its channel on cancellation, so the workers run out of work
// and the cycle reaches its end with an empty result — which used to be published
// as if it were a measurement. s.targets was replaced, the HTTP-SD endpoint began
// answering with an empty array, and persist wrote that emptiness over the cache.
//
// The next start then had nothing to seed from, so Prometheus was handed an empty
// target list and dropped every target it had. If this fails, an ordinary restart
// silently blanks the monitoring until the first full scan completes.
func TestCancelledCycleKeepsTheWarmStartCache(t *testing.T) {
	ts := promServer(t)
	cache := filepath.Join(t.TempDir(), "targets.json")

	seeded := []Target{{Address: "10.1.1.1:9100", Path: "/metrics"}}
	if err := saveTargets(cache, seeded); err != nil {
		t.Fatalf("seeding the cache: %v", err)
	}

	s := schedulerWithCache(t, ts, cache)
	if got := s.Targets(); !reflect.DeepEqual(got, seeded) {
		t.Fatalf("warm start loaded %v, want %v", got, seeded)
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	s.run(ctx)

	if got := s.Targets(); !reflect.DeepEqual(got, seeded) {
		t.Errorf("Targets() = %v after a cancelled cycle, want the warm-start set %v", got, seeded)
	}

	onDisk, err := loadTargets(cache)
	if err != nil {
		t.Fatalf("the cache is unreadable after a cancelled cycle: %v", err)
	}
	if !reflect.DeepEqual(onDisk, seeded) {
		t.Errorf("the cache holds %v after a cancelled cycle, want %v — the next start "+
			"would come up with no targets at all", onDisk, seeded)
	}
}

// A cycle that really did run and really found nothing must still publish that,
// or a decommissioned exporter would stay in the target list for ever.
func TestCompletedCycleWithNoTargetsClearsTheCache(t *testing.T) {
	// Serves HTML, so the port is open but validation rejects it.
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("<html><body>not an exporter</body></html>"))
	}))
	defer ts.Close()

	cache := filepath.Join(t.TempDir(), "targets.json")
	if err := saveTargets(cache, []Target{{Address: "10.1.1.1:9100", Path: "/metrics"}}); err != nil {
		t.Fatalf("seeding the cache: %v", err)
	}

	s := schedulerWithCache(t, ts, cache)
	s.run(context.Background())

	if got := s.Targets(); len(got) != 0 {
		t.Errorf("Targets() = %v, want none — the cycle completed and found nothing", got)
	}
	onDisk, err := loadTargets(cache)
	if err != nil {
		t.Fatalf("loadTargets: %v", err)
	}
	if len(onDisk) != 0 {
		t.Errorf("the cache still holds %v, so a target that is gone would never leave the list", onDisk)
	}
}

// TestOverlappingCyclesAreSkipped is the README's "No overlapping discovery
// cycles", stated twice there and counted by its own metric, with nothing
// asserting it.
//
// Without it a scan interval shorter than a scan — the normal state for a large
// CIDR — starts a new cycle on every tick. The scans pile up, each one dialling
// every host in the range, and the service turns into a port scanner hammering
// the network it is supposed to be observing.
func TestOverlappingCyclesAreSkipped(t *testing.T) {
	ts := promServer(t)
	s := schedulerWithCache(t, ts, "")

	// Hold the first cycle in place: execute returns immediately, so the flag is
	// what the second call has to see.
	if !s.running.CompareAndSwap(false, true) {
		t.Fatal("a fresh scheduler already reports a cycle in progress")
	}

	s.execute(context.Background())

	if got := counterValue(t, s.discoverySkippedTotal); got != 1 {
		t.Errorf("skipped cycles = %v, want 1", got)
	}
	if got := counterValue(t, s.discoveryRunsTotal); got != 0 {
		t.Errorf("runs = %v, want 0 — the cycle should not have started", got)
	}

	// And once the first cycle is done the next one runs, or the service would
	// skip every cycle for the rest of its life while still reporting itself
	// ready — discovering nothing, silently, for ever.
	s.running.Store(false)

	done := make(chan struct{})
	go func() {
		defer close(done)
		for counterValue(t, s.discoveryRunsTotal) == 0 {
			time.Sleep(time.Millisecond)
		}
	}()

	s.execute(context.Background())

	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("the cycle after a finished one never started; the scheduler would " +
			"skip every cycle from here on while reporting itself healthy")
	}
}

// TestRunningIsReleasedByACancelledCycle: the release happens in a defer, and
// the cancelled path returns early. If that early return ever moved above the
// defer, the flag would stay set and discovery would stop for good.
func TestRunningIsReleasedByACancelledCycle(t *testing.T) {
	ts := promServer(t)
	s := schedulerWithCache(t, ts, "")

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	s.execute(ctx)

	deadline := time.Now().Add(5 * time.Second)
	for s.running.Load() && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if s.running.Load() {
		t.Fatal("the running flag survived a cancelled cycle; every later cycle would be skipped")
	}
	if got := counterValue(t, s.activeDiscovery); got != 0 {
		t.Errorf("active_discovery = %v after a cancelled cycle, want 0 — a dashboard "+
			"would show a scan that is not happening", got)
	}
}

// TestSaveTargetsLeavesTheOldFileIntactOnFailure is the other half of the atomic
// write. The doc comment promises that a failed write "cannot corrupt an existing
// cache"; the existing test only checked that a *successful* overwrite works and
// leaves no temp files behind.
//
// The failure is provoked with a real chmod rather than a mock, because what has
// to hold is that os.CreateTemp fails before anything touches the destination.
func TestSaveTargetsLeavesTheOldFileIntactOnFailure(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "targets.json")

	good := []Target{{Address: "10.1.1.1:9100", Path: "/metrics"}}
	if err := saveTargets(path, good); err != nil {
		t.Fatalf("first saveTargets: %v", err)
	}

	// Read and execute only: the temp file cannot be created here.
	if err := os.Chmod(dir, 0o500); err != nil {
		t.Skipf("cannot make the directory read-only: %v", err)
	}
	t.Cleanup(func() { _ = os.Chmod(dir, 0o700) })

	if err := saveTargets(path, []Target{{Address: "10.2.2.2:9100", Path: "/x"}}); err == nil {
		t.Fatal("saveTargets reported success on a read-only directory")
	}

	// The point: the old cache is still there and still readable.
	got, err := loadTargets(path)
	if err != nil {
		t.Fatalf("the previous cache became unreadable after a failed write: %v", err)
	}
	if !reflect.DeepEqual(got, good) {
		t.Errorf("loadTargets() = %v after a failed write, want the previous %v", got, good)
	}
}

// TestSaveTargetsIsSafeForAConcurrentReader is the first half: "a reader never
// observes a partially written file". A reader that catches a half-written cache
// gets a JSON error, and the caller treats that as "no warm-start data" — the
// cache silently stops working.
func TestSaveTargetsIsSafeForAConcurrentReader(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "targets.json")

	// Large enough that a non-atomic write would be visible in parts.
	big := make([]Target, 0, 500)
	for i := 0; i < 500; i++ {
		big = append(big, Target{
			Address:  "10.0.0." + strconv.Itoa(i%256) + ":9100",
			Path:     "/metrics",
			Exporter: "node_exporter",
		})
	}
	if err := saveTargets(path, big); err != nil {
		t.Fatalf("seeding: %v", err)
	}

	stop := make(chan struct{})
	var wg sync.WaitGroup

	wg.Add(1)
	go func() {
		defer wg.Done()
		for {
			select {
			case <-stop:
				return
			default:
			}
			// Either the old set or the new one; never a parse error.
			if _, err := loadTargets(path); err != nil {
				t.Errorf("a reader observed a file it could not parse: %v", err)
				return
			}
		}
	}()

	for i := 0; i < 50; i++ {
		if err := saveTargets(path, big); err != nil {
			t.Fatalf("saveTargets: %v", err)
		}
	}
	close(stop)
	wg.Wait()
}
