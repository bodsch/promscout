package discovery

import (
	"testing"
	"time"

	"bodsch.me/promscout/internal/config"
	"bodsch.me/promscout/internal/logging"

	"github.com/prometheus/client_golang/prometheus"
)

// TestSchedulerExposesTheDocumentedMetrics: the README lists these names and
// contrib/grafana/generate.py builds panels on them. A renamed or dropped
// metric would not fail anything else — the panel would just show "No data".
func TestSchedulerExposesTheDocumentedMetrics(t *testing.T) {
	reg := prometheus.NewRegistry()
	logger := logging.New("text", "error")
	cfg := config.Config{NetworkCIDR: "127.0.0.1/32", Ports: []int{1}, Timeout: time.Second, MaxWorkers: 1}
	NewScheduler(NewScanner(cfg, logger), NewValidator(time.Second, []string{"/"}, logger),
		time.Minute, 1, "", reg, logger)

	families, err := reg.Gather()
	if err != nil {
		t.Fatalf("Gather() = %v", err)
	}
	got := map[string]bool{}
	for _, f := range families {
		got[f.GetName()] = true
	}

	for _, name := range []string{
		"promscout_discovery_runs_total",
		"promscout_discovery_skipped_total",
		"promscout_discovery_duration_seconds",
		"promscout_targets_discovered_total",
		"promscout_targets_valid_total",
		"promscout_last_discovery_timestamp",
		"promscout_active_discovery",
		"promscout_target_changes_total",
		"promscout_last_target_change_timestamp",
	} {
		if !got[name] {
			t.Errorf("%s is documented and charted but not exposed", name)
		}
	}
}

// TestDiscoveryDurationBucketsCoverLongScans: with the default buckets (max
// 10 s) every multi-minute scan fell into +Inf, and the p95 panel showed a
// flat 10 s however long the scans really took. The largest finite bucket must
// cover a scan of several minutes.
func TestDiscoveryDurationBucketsCoverLongScans(t *testing.T) {
	const longScan = 10 * time.Minute

	top := discoveryDurationBuckets[len(discoveryDurationBuckets)-1]
	if top < longScan.Seconds() {
		t.Errorf("largest bucket is %v s; a %v scan would land in +Inf and the quantile would lie", top, longScan)
	}
	if first := discoveryDurationBuckets[0]; first > 1 {
		t.Errorf("smallest bucket is %v s; a fast /32 or /28 scan would not be resolved", first)
	}
}
