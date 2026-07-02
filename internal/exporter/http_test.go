package exporter

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"bodsch.me/promscout/internal/discovery"
)

// fakeProvider is a test double for TargetProvider.
type fakeProvider struct {
	targets []discovery.Target
}

func (f fakeProvider) Targets() []discovery.Target { return f.targets }

type sdEntry struct {
	Targets []string          `json:"targets"`
	Labels  map[string]string `json:"labels"`
}

func TestServeHTTPServiceDiscovery(t *testing.T) {
	exp := NewHTTPExporter(fakeProvider{targets: []discovery.Target{
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
	exp := NewHTTPExporter(fakeProvider{})

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	rec := httptest.NewRecorder()
	exp.ServeHTTP(rec, req)

	body := strings.TrimSpace(rec.Body.String())
	if body != "[]" {
		t.Errorf("empty target body = %q, want []", body)
	}
}

func TestServeHTTPMetricsRoute(t *testing.T) {
	exp := NewHTTPExporter(fakeProvider{})

	req := httptest.NewRequest(http.MethodGet, "/metrics", nil)
	rec := httptest.NewRecorder()
	exp.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("/metrics status = %d, want 200", rec.Code)
	}
}

func TestServeHTTPUnknownRouteIs404(t *testing.T) {
	exp := NewHTTPExporter(fakeProvider{})

	req := httptest.NewRequest(http.MethodGet, "/does-not-exist", nil)
	rec := httptest.NewRecorder()
	exp.ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Errorf("unknown route status = %d, want 404", rec.Code)
	}
}
