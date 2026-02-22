// Package discovery provides orchestration logic for periodic
// network scanning and Prometheus endpoint validation.
package discovery

import (
	"context"
	"sync"
	"sync/atomic"
	"time"

	"bodsch.me/promscout/internal/logging"

	"github.com/prometheus/client_golang/prometheus"
)

type Scheduler struct {
	scanner   *Scanner
	validator *Validator
	interval  time.Duration
	logger    *logging.Logger

	running atomic.Bool

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

func NewScheduler(
	scanner *Scanner,
	validator *Validator,
	interval time.Duration,
	logger *logging.Logger,
) *Scheduler {

	s := &Scheduler{
		scanner:   scanner,
		validator: validator,
		interval:  interval,
		logger:    logger,
		targets:   make([]Target, 0),
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

	// Register metrics
	prometheus.MustRegister(
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

func (s *Scheduler) Start(ctx context.Context) {
	// Immediate first run
	s.execute()

	ticker := time.NewTicker(s.interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			s.logger.Info("scheduler stopped")
			return
		case <-ticker.C:
			s.execute()
		}
	}
}

func (s *Scheduler) execute() {
	if !s.running.CompareAndSwap(false, true) {
		s.discoverySkippedTotal.Inc()
		s.logger.Debug("previous discovery still running, skipping cycle")
		return
	}

	go func() {
		defer s.running.Store(false)
		s.run()
	}()
}

func (s *Scheduler) run() {
	s.activeDiscovery.Set(1)
	start := time.Now()

	s.logger.Debug("starting discovery cycle")
	s.discoveryRunsTotal.Inc()

	rawTargets := s.scanner.Scan()
	s.targetsDiscoveredTotal.Set(float64(len(rawTargets)))

	validTargets := make([]Target, 0, len(rawTargets))

	for _, addr := range rawTargets {
		ok, path := s.validator.Validate(addr)
		if ok {
			validTargets = append(validTargets, Target{
				Address: addr,
				Path:    path,
			})
		}
	}

	s.mu.Lock()
	s.targets = validTargets
	s.mu.Unlock()

	s.targetsValidTotal.Set(float64(len(validTargets)))
	s.lastDiscoveryTimestamp.Set(float64(time.Now().Unix()))
	s.discoveryDuration.Observe(time.Since(start).Seconds())
	s.activeDiscovery.Set(0)

	s.logger.Debug("discovery cycle completed",
		"valid_targets", len(validTargets),
	)
}

func (s *Scheduler) Targets() []Target {
	s.mu.RLock()
	defer s.mu.RUnlock()

	copied := make([]Target, len(s.targets))
	copy(copied, s.targets)

	return copied
}
