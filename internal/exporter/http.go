package exporter

import (
	"encoding/json"
	"net/http"

	"bodsch.me/promscout/internal/discovery"

	"github.com/prometheus/client_golang/prometheus/promhttp"
)

// HTTPExporter implements http.Handler and exposes:
//
//   "/"        → HTTP Service Discovery
//   "/metrics" → PromScout self-monitoring metrics
//
type HTTPExporter struct {
	scheduler *discovery.Scheduler
}

// NewHTTPExporter creates a new exporter instance.
func NewHTTPExporter(scheduler *discovery.Scheduler) *HTTPExporter {
	return &HTTPExporter{
		scheduler: scheduler,
	}
}

// ServeHTTP implements http.Handler.
func (h *HTTPExporter) ServeHTTP(w http.ResponseWriter, r *http.Request) {

	switch r.URL.Path {

	case "/metrics":
		promhttp.Handler().ServeHTTP(w, r)
		return

	case "/":
		h.handleServiceDiscovery(w)
		return

	default:
		http.NotFound(w, r)
	}
}

func (h *HTTPExporter) handleServiceDiscovery(w http.ResponseWriter) {

	discovered := h.scheduler.Targets()

	response := make([]map[string]interface{}, 0, len(discovered))

	for _, t := range discovered {
		response = append(response, map[string]interface{}{
			"targets": []string{t.Address},
			"labels": map[string]string{
				"job":              "dynamic-discovery",
				"__metrics_path__": t.Path,
			},
		})
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(response)
}
