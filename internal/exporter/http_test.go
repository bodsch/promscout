package exporter

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"bodsch.me/promscout/internal/discovery"

	"github.com/prometheus/client_golang/prometheus"
)

// fakeProvider is a test double for TargetProvider.
type fakeProvider struct {
	targets []discovery.Target
	ready   bool
}

func (f fakeProvider) Targets() []discovery.Target { return f.targets }
func (f fakeProvider) Ready() bool                 { return f.ready }

// newTestExporter builds an exporter backed by the given provider and a
// fresh, empty metrics registry, with job guessing disabled.
func newTestExporter(p TargetProvider) *HTTPExporter {
	return NewHTTPExporter(p, prometheus.NewRegistry(), false)
}

type sdEntry struct {
	Targets []string          `json:"targets"`
	Labels  map[string]string `json:"labels"`
}

func TestServeHTTPServiceDiscovery(t *testing.T) {
	exp := newTestExporter(fakeProvider{targets: []discovery.Target{
		{Address: "10.0.0.1:9100", Path: "/metrics"},
		{Address: "10.0.0.2:9115", Path: "/"},
	}})

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	rec := httptest.NewRecorder()
	exp.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	if ct := rec.Header().Get("Content-Type"); ct != "application/json" {
		t.Errorf("Content-Type = %q, want application/json", ct)
	}

	var entries []sdEntry
	if err := json.Unmarshal(rec.Body.Bytes(), &entries); err != nil {
		t.Fatalf("invalid JSON response: %v (body=%q)", err, rec.Body.String())
	}
	if len(entries) != 2 {
		t.Fatalf("got %d entries, want 2", len(entries))
	}

	first := entries[0]
	if len(first.Targets) != 1 || first.Targets[0] != "10.0.0.1:9100" {
		t.Errorf("entry[0].targets = %v, want [10.0.0.1:9100]", first.Targets)
	}
	if first.Labels["job"] != "promscout" {
		t.Errorf("entry[0].labels[job] = %q, want promscout", first.Labels["job"])
	}
	if first.Labels["__metrics_path__"] != "/metrics" {
		t.Errorf("entry[0].labels[__metrics_path__] = %q, want /metrics", first.Labels["__metrics_path__"])
	}
}

func TestServeHTTPEmptyTargetsIsEmptyArray(t *testing.T) {
	exp := newTestExporter(fakeProvider{})

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	rec := httptest.NewRecorder()
	exp.ServeHTTP(rec, req)

	body := strings.TrimSpace(rec.Body.String())
	if body != "[]" {
		t.Errorf("empty target body = %q, want []", body)
	}
}

func TestServeHTTPMetricsRoute(t *testing.T) {
	exp := newTestExporter(fakeProvider{})

	req := httptest.NewRequest(http.MethodGet, "/metrics", nil)
	rec := httptest.NewRecorder()
	exp.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("/metrics status = %d, want 200", rec.Code)
	}
}

func TestServeHTTPUnknownRouteIs404(t *testing.T) {
	exp := newTestExporter(fakeProvider{})

	req := httptest.NewRequest(http.MethodGet, "/does-not-exist", nil)
	rec := httptest.NewRecorder()
	exp.ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Errorf("unknown route status = %d, want 404", rec.Code)
	}
}

func decodeSD(t *testing.T, exp *HTTPExporter) []sdEntry {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	rec := httptest.NewRecorder()
	exp.ServeHTTP(rec, req)

	var entries []sdEntry
	if err := json.Unmarshal(rec.Body.Bytes(), &entries); err != nil {
		t.Fatalf("invalid JSON: %v (body=%q)", err, rec.Body.String())
	}
	return entries
}

func TestServeHTTPMetaLabelsAlwaysEmitted(t *testing.T) {
	// Meta labels are emitted regardless of the guessJob setting.
	exp := newTestExporter(fakeProvider{targets: []discovery.Target{
		{Address: "10.0.0.1:9100", Path: "/metrics", Exporter: "node_exporter", Version: "1.7.0", Source: "build_info"},
	}})

	entries := decodeSD(t, exp)
	if len(entries) != 1 {
		t.Fatalf("got %d entries, want 1", len(entries))
	}
	l := entries[0].Labels

	if l["__meta_promscout_exporter"] != "node_exporter" {
		t.Errorf("__meta_promscout_exporter = %q, want node_exporter", l["__meta_promscout_exporter"])
	}
	if l["__meta_promscout_version"] != "1.7.0" {
		t.Errorf("__meta_promscout_version = %q, want 1.7.0", l["__meta_promscout_version"])
	}
	if l["__meta_promscout_source"] != "build_info" {
		t.Errorf("__meta_promscout_source = %q, want build_info", l["__meta_promscout_source"])
	}
	// guessJob is off → job stays neutral.
	if l["job"] != "promscout" {
		t.Errorf("job = %q, want promscout (guessJob off)", l["job"])
	}
}

func TestServeHTTPJobGuessOptIn(t *testing.T) {
	targets := []discovery.Target{
		{Address: "10.0.0.1:9100", Path: "/metrics", Exporter: "node_exporter", Source: "build_info"},
		{Address: "10.0.0.2:9187", Path: "/metrics"}, // no exporter derived
	}

	// guessJob ON: derived exporter becomes job; undetermined stays neutral.
	exp := NewHTTPExporter(fakeProvider{targets: targets}, prometheus.NewRegistry(), true)
	entries := decodeSD(t, exp)

	if entries[0].Labels["job"] != "node_exporter" {
		t.Errorf("entry[0] job = %q, want node_exporter (guessed)", entries[0].Labels["job"])
	}
	if entries[1].Labels["job"] != "promscout" {
		t.Errorf("entry[1] job = %q, want promscout (no exporter derived)", entries[1].Labels["job"])
	}
	// Meta label must not be present when nothing was derived.
	if _, ok := entries[1].Labels["__meta_promscout_exporter"]; ok {
		t.Error("entry[1] unexpectedly has __meta_promscout_exporter")
	}
}

func TestServeHTTPHealthyAlwaysOK(t *testing.T) {
	// Liveness must not depend on readiness state.
	exp := newTestExporter(fakeProvider{ready: false})

	req := httptest.NewRequest(http.MethodGet, "/-/healthy", nil)
	rec := httptest.NewRecorder()
	exp.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("/-/healthy status = %d, want 200", rec.Code)
	}
}

func TestServeHTTPReadyReflectsProvider(t *testing.T) {
	tests := []struct {
		name     string
		ready    bool
		wantCode int
	}{
		{"not ready before first scan", false, http.StatusServiceUnavailable},
		{"ready after first scan", true, http.StatusOK},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			exp := newTestExporter(fakeProvider{ready: tt.ready})

			req := httptest.NewRequest(http.MethodGet, "/-/ready", nil)
			rec := httptest.NewRecorder()
			exp.ServeHTTP(rec, req)

			if rec.Code != tt.wantCode {
				t.Fatalf("/-/ready status = %d, want %d", rec.Code, tt.wantCode)
			}
		})
	}
}
