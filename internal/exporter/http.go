package exporter

import (
	"encoding/json"
	"net/http"

	"bodsch.me/promscout/internal/discovery"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

// TargetProvider supplies the currently discovered scrape targets and the
// readiness state of the discovery loop. *discovery.Scheduler satisfies
// this interface.
type TargetProvider interface {
	// Targets returns the currently validated scrape targets.
	Targets() []discovery.Target
	// Ready reports whether at least one discovery cycle has completed.
	Ready() bool
}

// HTTPExporter implements http.Handler and exposes:
//
//	"/"          → HTTP Service Discovery (JSON)
//	"/metrics"   → PromScout self-monitoring metrics
//	"/-/healthy" → liveness probe (always OK while serving)
//	"/-/ready"   → readiness probe (OK once the first scan completed)
//
// The health/readiness paths follow the Prometheus ecosystem convention
// so operators find them where they expect.
type HTTPExporter struct {
	provider TargetProvider
	metrics  http.Handler
	guessJob bool
}

// NewHTTPExporter creates a new exporter instance.
//
// Parameters:
//   - provider: source of discovered targets and readiness state.
//   - gatherer: the metrics registry to expose on /metrics. Injecting it
//     (instead of using the global default) keeps the exporter decoupled
//     from global process state and testable in isolation.
//   - guessJob: when true, a best-effort "job" label is derived from the
//     target's exposition metadata (opt-in). When false, "job" defaults
//     to "promscout" and the derived identity is only exposed via
//     __meta_promscout_* labels for the operator to use in relabeling.
//
// Returns a ready-to-serve http.Handler.
func NewHTTPExporter(provider TargetProvider, gatherer prometheus.Gatherer, guessJob bool) *HTTPExporter {
	return &HTTPExporter{
		provider: provider,
		metrics:  promhttp.HandlerFor(gatherer, promhttp.HandlerOpts{}),
		guessJob: guessJob,
	}
}

// ServeHTTP implements http.Handler.
func (h *HTTPExporter) ServeHTTP(w http.ResponseWriter, r *http.Request) {

	switch r.URL.Path {

	case "/metrics":
		h.metrics.ServeHTTP(w, r)
		return

	case "/-/healthy":
		h.handleHealthy(w)
		return

	case "/-/ready":
		h.handleReady(w)
		return

	case "/":
		h.handleServiceDiscovery(w)
		return

	default:
		http.NotFound(w, r)
	}
}

// handleServiceDiscovery writes the Prometheus HTTP-SD document.
//
// Each target always carries __metrics_path__ and, when the service could
// be identified, __meta_promscout_{exporter,version,source} meta labels
// for the operator to consume via relabel_configs. The "job" label is a
// best-effort guess only when guessJob is enabled; otherwise it stays at
// the neutral default "promscout".
func (h *HTTPExporter) handleServiceDiscovery(w http.ResponseWriter) {

	discovered := h.provider.Targets()

	response := make([]map[string]interface{}, 0, len(discovered))

	for _, t := range discovered {
		labels := map[string]string{
			"job":              h.jobLabel(t),
			"__metrics_path__": t.Path,
		}
		if t.Exporter != "" {
			labels["__meta_promscout_exporter"] = t.Exporter
		}
		if t.Version != "" {
			labels["__meta_promscout_version"] = t.Version
		}
		if t.Source != "" {
			labels["__meta_promscout_source"] = t.Source
		}

		response = append(response, map[string]interface{}{
			"targets": []string{t.Address},
			"labels":  labels,
		})
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(response)
}

// jobLabel returns the value for the "job" label. With guessJob enabled
// and a derived exporter name available, that name is used; otherwise the
// neutral default keeps the output stable and non-misleading.
func (h *HTTPExporter) jobLabel(t discovery.Target) string {
	if h.guessJob && t.Exporter != "" {
		return t.Exporter
	}
	return "promscout"
}

// handleHealthy backs the liveness probe. Reaching this handler means the
// HTTP server is up and serving, so it always reports healthy.
func (h *HTTPExporter) handleHealthy(w http.ResponseWriter) {
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte("OK\n"))
}

// handleReady backs the readiness probe. It reports ready only after the
// first discovery cycle has completed, so consumers do not scrape an
// empty (and misleading) service-discovery document during startup.
func (h *HTTPExporter) handleReady(w http.ResponseWriter) {
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	if h.provider.Ready() {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("Ready\n"))
		return
	}
	w.WriteHeader(http.StatusServiceUnavailable)
	_, _ = w.Write([]byte("Not Ready\n"))
}
