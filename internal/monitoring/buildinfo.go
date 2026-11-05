// Package monitoring provides process-level self-monitoring metrics.
package monitoring

import (
	"runtime"

	"bodsch.me/promscout/pkg/version"

	"github.com/prometheus/client_golang/prometheus"
)

// RegisterBuildInfo registers promscout_build_info, a constant gauge of 1
// whose labels carry the build metadata from pkg/version.
//
// Besides answering "which version runs where", the series identifies a
// scrape target as PromScout: go_* and process_* carry no service prefix, so
// dashboards join them on (job, instance) with this metric to keep exactly
// the PromScout targets.
//
// Parameters:
//   - reg: the registry to register the metric on.
//
// Returns an error if a metric with the same name is already registered.
func RegisterBuildInfo(reg prometheus.Registerer) error {
	g := prometheus.NewGauge(prometheus.GaugeOpts{
		Namespace: "promscout",
		Name:      "build_info",
		Help:      "Build information of the running PromScout binary; always 1.",
		ConstLabels: prometheus.Labels{
			"version":    version.Version,
			"revision":   version.GitCommit,
			"build_date": version.BuildDate,
			"goversion":  runtime.Version(),
		},
	})
	g.Set(1)
	return reg.Register(g)
}
