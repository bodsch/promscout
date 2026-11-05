package monitoring

import (
	"runtime"
	"testing"

	"bodsch.me/promscout/pkg/version"

	"github.com/prometheus/client_golang/prometheus"
)

// TestRegisterBuildInfoIsGathered reads the metric back through a real
// registry. The dashboards fill their instance variable from
// promscout_build_info and scope every go_*/process_* panel with it: if the
// series were missing, every one of those panels would show "No data" — which
// looks exactly like a service that has not been scraped yet.
func TestRegisterBuildInfoIsGathered(t *testing.T) {
	reg := prometheus.NewRegistry()
	if err := RegisterBuildInfo(reg); err != nil {
		t.Fatalf("RegisterBuildInfo() = %v", err)
	}

	families, err := reg.Gather()
	if err != nil {
		t.Fatalf("Gather() = %v", err)
	}
	if len(families) != 1 || families[0].GetName() != "promscout_build_info" {
		t.Fatalf("gathered %v, want exactly promscout_build_info", families)
	}

	m := families[0].GetMetric()[0]
	if v := m.GetGauge().GetValue(); v != 1 {
		t.Errorf("value = %v, want 1", v)
	}

	want := map[string]string{
		"version":    version.Version,
		"revision":   version.GitCommit,
		"build_date": version.BuildDate,
		"goversion":  runtime.Version(),
	}
	got := map[string]string{}
	for _, lp := range m.GetLabel() {
		got[lp.GetName()] = lp.GetValue()
	}
	for k, v := range want {
		if got[k] != v {
			t.Errorf("label %s = %q, want %q", k, got[k], v)
		}
	}
}

// TestRegisterBuildInfoTwiceFails: a second registration is a wiring mistake
// and must surface as an error, not as a silently duplicated series.
func TestRegisterBuildInfoTwiceFails(t *testing.T) {
	reg := prometheus.NewRegistry()
	if err := RegisterBuildInfo(reg); err != nil {
		t.Fatalf("first RegisterBuildInfo() = %v", err)
	}
	if err := RegisterBuildInfo(reg); err == nil {
		t.Fatal("second RegisterBuildInfo() = nil, want an AlreadyRegistered error")
	}
}
