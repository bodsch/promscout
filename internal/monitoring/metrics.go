// Package monitoring provides self-monitoring metrics
// for the Prometheus service discovery application.
package monitoring

import (
	"github.com/prometheus/client_golang/prometheus"
)

type Metrics struct {
	DiscoveryRunsTotal      prometheus.Counter
	DiscoverySkippedTotal   prometheus.Counter
	DiscoveryDuration       prometheus.Histogram
	TargetsDiscoveredTotal  prometheus.Gauge
	TargetsValidTotal       prometheus.Gauge
	LastDiscoveryTimestamp  prometheus.Gauge
	ActiveDiscovery         prometheus.Gauge
}

// New creates and registers all metrics.
func New() *Metrics {
	m := &Metrics{
		DiscoveryRunsTotal: prometheus.NewCounter(
			prometheus.CounterOpts{
				Namespace: "sd",
				Name:      "discovery_runs_total",
				Help:      "Total number of discovery cycles executed.",
			},
		),
		DiscoverySkippedTotal: prometheus.NewCounter(
			prometheus.CounterOpts{
				Namespace: "sd",
				Name:      "discovery_skipped_total",
				Help:      "Total number of skipped discovery cycles due to overlap.",
			},
		),
		DiscoveryDuration: prometheus.NewHistogram(
			prometheus.HistogramOpts{
				Namespace: "sd",
				Name:      "discovery_duration_seconds",
				Help:      "Duration of discovery cycles.",
				Buckets:   prometheus.DefBuckets,
			},
		),
		TargetsDiscoveredTotal: prometheus.NewGauge(
			prometheus.GaugeOpts{
				Namespace: "sd",
				Name:      "targets_discovered_total",
				Help:      "Number of open port targets discovered.",
			},
		),
		TargetsValidTotal: prometheus.NewGauge(
			prometheus.GaugeOpts{
				Namespace: "sd",
				Name:      "targets_valid_total",
				Help:      "Number of valid Prometheus targets.",
			},
		),
		LastDiscoveryTimestamp: prometheus.NewGauge(
			prometheus.GaugeOpts{
				Namespace: "sd",
				Name:      "last_discovery_timestamp",
				Help:      "Unix timestamp of the last completed discovery run.",
			},
		),
		ActiveDiscovery: prometheus.NewGauge(
			prometheus.GaugeOpts{
				Namespace: "sd",
				Name:      "active_discovery",
				Help:      "Indicates if a discovery cycle is currently running (1 = yes, 0 = no).",
			},
		),
	}

	prometheus.MustRegister(
		m.DiscoveryRunsTotal,
		m.DiscoverySkippedTotal,
		m.DiscoveryDuration,
		m.TargetsDiscoveredTotal,
		m.TargetsValidTotal,
		m.LastDiscoveryTimestamp,
		m.ActiveDiscovery,
	)

	return m
}
