// Package discovery provides orchestration logic for periodic
// network scanning and Prometheus endpoint validation.
package discovery

import (
	"context"
	"sort"
	"sync"
	"sync/atomic"
	"time"

	"bodsch.me/promscout/internal/logging"

	"github.com/prometheus/client_golang/prometheus"
)

type Scheduler struct {
	scanner         *Scanner
	validator       *Validator
	interval        time.Duration
	validateWorkers int
	cacheFile       string
	logger          *logging.Logger

	running atomic.Bool
	ready   atomic.Bool

	mu      sync.RWMutex
	targets []Target

	// --- Self Monitoring Metrics ---
	discoveryRunsTotal     prometheus.Counter
	discoverySkippedTotal  prometheus.Counter
	discoveryDuration      prometheus.Histogram
	targetsDiscoveredTotal prometheus.Gauge
	targetsValidTotal      prometheus.Gauge
	lastDiscoveryTimestamp prometheus.Gauge
	activeDiscovery        prometheus.Gauge
}

// NewScheduler creates a discovery scheduler.
//
// Parameters:
//   - scanner: the TCP port scanner (dial stage).
//   - validator: the Prometheus metrics validator (validate stage).
//   - interval: delay between discovery cycles.
//   - validateWorkers: size of the parallel validation worker pool.
//   - cacheFile: optional path for the warm-start target cache; when
//     empty, persistence is disabled. When set and readable, its
//     contents seed the initial target set on startup.
//   - reg: the registry the self-monitoring metrics are registered on.
//     Passing a dedicated registry (rather than the global default)
//     keeps the scheduler self-contained and testable.
//   - logger: structured logger.
//
// Returns a ready-to-use Scheduler with all self-monitoring metrics
// registered on reg.
func NewScheduler(
	scanner *Scanner,
	validator *Validator,
	interval time.Duration,
	validateWorkers int,
	cacheFile string,
	reg prometheus.Registerer,
	logger *logging.Logger,
) *Scheduler {

	if validateWorkers < 1 {
		validateWorkers = 1
	}

	s := &Scheduler{
		scanner:         scanner,
		validator:       validator,
		interval:        interval,
		validateWorkers: validateWorkers,
		cacheFile:       cacheFile,
		logger:          logger,
		targets:         make([]Target, 0),
	}

	// Warm start: seed targets from the cache so the HTTP-SD endpoint
	// serves results immediately instead of staying empty until the
	// first scan completes. A missing/unreadable cache is not fatal.
	if cacheFile != "" {
		if cached, err := loadTargets(cacheFile); err != nil {
			logger.Debug("no target cache loaded", "error", err, "path", cacheFile)
		} else {
			s.targets = cached
			logger.Info("loaded targets from cache",
				"count", len(cached),
				"path", cacheFile,
			)
		}
	}

	// ---- Metrics Definition ----

	s.discoveryRunsTotal = prometheus.NewCounter(
		prometheus.CounterOpts{
			Namespace: "promscout",
			Name:      "discovery_runs_total",
			Help:      "Total number of discovery cycles executed.",
		},
	)

	s.discoverySkippedTotal = prometheus.NewCounter(
		prometheus.CounterOpts{
			Namespace: "promscout",
			Name:      "discovery_skipped_total",
			Help:      "Total number of skipped discovery cycles due to overlap.",
		},
	)

	s.discoveryDuration = prometheus.NewHistogram(
		prometheus.HistogramOpts{
			Namespace: "promscout",
			Name:      "discovery_duration_seconds",
			Help:      "Duration of discovery cycles.",
			Buckets:   prometheus.DefBuckets,
		},
	)

	s.targetsDiscoveredTotal = prometheus.NewGauge(
		prometheus.GaugeOpts{
			Namespace: "promscout",
			Name:      "targets_discovered_total",
			Help:      "Number of open port targets discovered.",
		},
	)

	s.targetsValidTotal = prometheus.NewGauge(
		prometheus.GaugeOpts{
			Namespace: "promscout",
			Name:      "targets_valid_total",
			Help:      "Number of valid Prometheus targets.",
		},
	)

	s.lastDiscoveryTimestamp = prometheus.NewGauge(
		prometheus.GaugeOpts{
			Namespace: "promscout",
			Name:      "last_discovery_timestamp",
			Help:      "Unix timestamp of the last completed discovery run.",
		},
	)

	s.activeDiscovery = prometheus.NewGauge(
		prometheus.GaugeOpts{
			Namespace: "promscout",
			Name:      "active_discovery",
			Help:      "Indicates if a discovery cycle is currently running (1 = yes, 0 = no).",
		},
	)

	// Register metrics on the provided registry.
	reg.MustRegister(
		s.discoveryRunsTotal,
		s.discoverySkippedTotal,
		s.discoveryDuration,
		s.targetsDiscoveredTotal,
		s.targetsValidTotal,
		s.lastDiscoveryTimestamp,
		s.activeDiscovery,
	)

	return s
}

// Start runs an immediate first discovery cycle and then repeats every
// interval until ctx is cancelled.
func (s *Scheduler) Start(ctx context.Context) {
	// Immediate first run
	s.execute(ctx)

	ticker := time.NewTicker(s.interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			s.logger.Info("scheduler stopped")
			return
		case <-ticker.C:
			s.execute(ctx)
		}
	}
}

// execute starts a discovery cycle unless one is already running, in
// which case the cycle is skipped to avoid overlapping scans.
func (s *Scheduler) execute(ctx context.Context) {
	if !s.running.CompareAndSwap(false, true) {
		s.discoverySkippedTotal.Inc()
		s.logger.Debug("previous discovery still running, skipping cycle")
		return
	}

	go func() {
		defer s.running.Store(false)
		s.run(ctx)
	}()
}

// run executes a single discovery cycle: it streams open ports from the
// scanner into a pool of validation workers, then atomically publishes
// the validated targets and (optionally) persists them.
func (s *Scheduler) run(ctx context.Context) {
	s.activeDiscovery.Set(1)
	start := time.Now()

	s.logger.Debug("starting discovery cycle")
	s.discoveryRunsTotal.Inc()

	openCh := s.scanner.Scan(ctx)

	var (
		discovered atomic.Int64
		mu         sync.Mutex
		valid      = make([]Target, 0)
		wg         sync.WaitGroup
	)

	// Validation stage: a bounded pool consumes open ports as the scan
	// produces them, so both stages overlap in a streaming pipeline.
	for i := 0; i < s.validateWorkers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for addr := range openCh {
				discovered.Add(1)
				ok, res := s.validator.Validate(ctx, addr)
				if !ok {
					continue
				}
				mu.Lock()
				valid = append(valid, Target{
					Address:  addr,
					Path:     res.Path,
					Exporter: res.Exporter,
					Version:  res.Version,
					Source:   res.Source,
				})
				mu.Unlock()
			}
		}()
	}
	wg.Wait()

	// Stable ordering keeps the HTTP-SD output and persisted cache
	// deterministic regardless of validation completion order.
	sort.Slice(valid, func(i, j int) bool {
		if valid[i].Address != valid[j].Address {
			return valid[i].Address < valid[j].Address
		}
		return valid[i].Path < valid[j].Path
	})

	s.targetsDiscoveredTotal.Set(float64(discovered.Load()))

	s.mu.Lock()
	s.targets = valid
	s.mu.Unlock()

	s.targetsValidTotal.Set(float64(len(valid)))
	s.lastDiscoveryTimestamp.Set(float64(time.Now().Unix()))
	s.discoveryDuration.Observe(time.Since(start).Seconds())
	s.activeDiscovery.Set(0)

	// The service is ready to serve service-discovery results once the
	// first cycle has fully completed.
	s.ready.Store(true)

	s.persist(valid)

	s.logger.Debug("discovery cycle completed",
		"discovered_targets", discovered.Load(),
		"valid_targets", len(valid),
	)
}

// persist writes the current target set to the configured cache file.
// It is a no-op when no cache file is configured. Persistence errors are
// logged but never interrupt the discovery loop.
func (s *Scheduler) persist(targets []Target) {
	if s.cacheFile == "" {
		return
	}
	if err := saveTargets(s.cacheFile, targets); err != nil {
		s.logger.Error("failed to persist target cache",
			"error", err,
			"path", s.cacheFile,
		)
	}
}

// Targets returns a snapshot copy of the currently validated targets.
func (s *Scheduler) Targets() []Target {
	s.mu.RLock()
	defer s.mu.RUnlock()

	copied := make([]Target, len(s.targets))
	copy(copied, s.targets)

	return copied
}

// Ready reports whether at least one discovery cycle has completed.
// It is used to back the readiness probe: until the first scan finishes,
// the service-discovery output is not yet meaningful.
func (s *Scheduler) Ready() bool {
	return s.ready.Load()
}
