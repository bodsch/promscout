package config

import (
	"flag"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestLoadDefaults(t *testing.T) {
	fs := flag.NewFlagSet("test", flag.ContinueOnError)

	cfg, err := Load(fs, nil)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}

	if cfg.NetworkCIDR != "192.168.1.0/24" {
		t.Errorf("NetworkCIDR = %q, want default", cfg.NetworkCIDR)
	}
	if cfg.MaxWorkers != 512 {
		t.Errorf("MaxWorkers = %d, want 512", cfg.MaxWorkers)
	}
	if cfg.ValidateWorkers != 50 {
		t.Errorf("ValidateWorkers = %d, want 50", cfg.ValidateWorkers)
	}
	if cfg.DialTimeout != 800*time.Millisecond {
		t.Errorf("DialTimeout = %v, want 800ms", cfg.DialTimeout)
	}
	if cfg.CacheFile != "" {
		t.Errorf("CacheFile = %q, want empty default", cfg.CacheFile)
	}
	if cfg.GuessJob {
		t.Errorf("GuessJob = %v, want false default", cfg.GuessJob)
	}
	if cfg.ListenAddress != ":8080" {
		t.Errorf("ListenAddress = %q, want :8080", cfg.ListenAddress)
	}
	if len(cfg.Ports) != 3 {
		t.Errorf("Ports = %v, want 3 defaults", cfg.Ports)
	}
}

func TestLoadFlagsOverrideDefaults(t *testing.T) {
	fs := flag.NewFlagSet("test", flag.ContinueOnError)

	cfg, err := Load(fs, []string{
		"-cidr", "10.0.0.0/8",
		"-workers", "10",
		"-listen", "127.0.0.1:9000",
		"-timeout", "5s",
	})
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}

	if cfg.NetworkCIDR != "10.0.0.0/8" {
		t.Errorf("NetworkCIDR = %q, want 10.0.0.0/8", cfg.NetworkCIDR)
	}
	if cfg.MaxWorkers != 10 {
		t.Errorf("MaxWorkers = %d, want 10", cfg.MaxWorkers)
	}
	if cfg.ListenAddress != "127.0.0.1:9000" {
		t.Errorf("ListenAddress = %q, want 127.0.0.1:9000", cfg.ListenAddress)
	}
	if cfg.Timeout != 5*time.Second {
		t.Errorf("Timeout = %v, want 5s", cfg.Timeout)
	}
}

func TestLoadFromYAMLFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")

	yaml := `
network_cidr: 172.16.0.0/12
ports:
  - 9100
  - 9200
metrics_paths:
  - /custom
interval: 30s
timeout: 2s
dial_timeout: 500ms
max_workers: 25
validate_workers: 8
cache_file: /var/lib/promscout/targets.json
`
	if err := os.WriteFile(path, []byte(yaml), 0o600); err != nil {
		t.Fatalf("write temp config: %v", err)
	}

	fs := flag.NewFlagSet("test", flag.ContinueOnError)
	cfg, err := Load(fs, []string{"-config", path})
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}

	if cfg.NetworkCIDR != "172.16.0.0/12" {
		t.Errorf("NetworkCIDR = %q, want 172.16.0.0/12", cfg.NetworkCIDR)
	}
	if len(cfg.Ports) != 2 || cfg.Ports[1] != 9200 {
		t.Errorf("Ports = %v, want [9100 9200]", cfg.Ports)
	}
	if cfg.Interval != 30*time.Second {
		t.Errorf("Interval = %v, want 30s", cfg.Interval)
	}
	if cfg.MaxWorkers != 25 {
		t.Errorf("MaxWorkers = %d, want 25", cfg.MaxWorkers)
	}
	if cfg.ValidateWorkers != 8 {
		t.Errorf("ValidateWorkers = %d, want 8", cfg.ValidateWorkers)
	}
	if cfg.DialTimeout != 500*time.Millisecond {
		t.Errorf("DialTimeout = %v, want 500ms", cfg.DialTimeout)
	}
	if cfg.CacheFile != "/var/lib/promscout/targets.json" {
		t.Errorf("CacheFile = %q, want /var/lib/promscout/targets.json", cfg.CacheFile)
	}
	// Not specified in YAML → default retained.
	if cfg.ListenAddress != ":8080" {
		t.Errorf("ListenAddress = %q, want default :8080", cfg.ListenAddress)
	}
}

func TestLoadCLIFlagsOverrideYAML(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")

	yaml := `
network_cidr: 192.168.0.0/24
interval: 30s
max_workers: 25
log_level: info
`
	if err := os.WriteFile(path, []byte(yaml), 0o600); err != nil {
		t.Fatalf("write temp config: %v", err)
	}

	fs := flag.NewFlagSet("test", flag.ContinueOnError)
	cfg, err := Load(fs, []string{
		"-config", path,
		"-cidr", "10.0.0.0/8",
		"-workers", "5",
	})
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}

	// Explicitly-set CLI flags must win over the YAML file.
	if cfg.NetworkCIDR != "10.0.0.0/8" {
		t.Errorf("NetworkCIDR = %q, want CLI override 10.0.0.0/8", cfg.NetworkCIDR)
	}
	if cfg.MaxWorkers != 5 {
		t.Errorf("MaxWorkers = %d, want CLI override 5", cfg.MaxWorkers)
	}
	// Values only present in YAML (no CLI flag) must be taken from YAML.
	if cfg.Interval != 30*time.Second {
		t.Errorf("Interval = %v, want YAML 30s", cfg.Interval)
	}
	if cfg.LogLevel != "info" {
		t.Errorf("LogLevel = %q, want YAML info", cfg.LogLevel)
	}
}

func TestLoadYAMLOverridesDefaultsWithoutCLI(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")

	// max_workers absent → default must survive; cidr present → YAML wins.
	yaml := `
network_cidr: 172.16.0.0/12
`
	if err := os.WriteFile(path, []byte(yaml), 0o600); err != nil {
		t.Fatalf("write temp config: %v", err)
	}

	fs := flag.NewFlagSet("test", flag.ContinueOnError)
	cfg, err := Load(fs, []string{"-config", path})
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}

	if cfg.NetworkCIDR != "172.16.0.0/12" {
		t.Errorf("NetworkCIDR = %q, want YAML 172.16.0.0/12", cfg.NetworkCIDR)
	}
	if cfg.MaxWorkers != 512 {
		t.Errorf("MaxWorkers = %d, want default 512", cfg.MaxWorkers)
	}
}

func TestLoadGuessJobEnabledViaYAML(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(path, []byte("guess_job: true\n"), 0o600); err != nil {
		t.Fatalf("write temp config: %v", err)
	}

	fs := flag.NewFlagSet("test", flag.ContinueOnError)
	cfg, err := Load(fs, []string{"-config", path})
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if !cfg.GuessJob {
		t.Error("GuessJob = false, want true (enabled via YAML)")
	}
}

func TestLoadGuessJobCLIDisablesYAML(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(path, []byte("guess_job: true\n"), 0o600); err != nil {
		t.Fatalf("write temp config: %v", err)
	}

	fs := flag.NewFlagSet("test", flag.ContinueOnError)
	// Explicit CLI flag must override the YAML value.
	cfg, err := Load(fs, []string{"-config", path, "-guess-job=false"})
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if cfg.GuessJob {
		t.Error("GuessJob = true, want false (disabled via CLI over YAML)")
	}
}

func TestLoadMissingConfigFile(t *testing.T) {
	fs := flag.NewFlagSet("test", flag.ContinueOnError)
	_, err := Load(fs, []string{"-config", "/nonexistent/path/config.yaml"})
	if err == nil {
		t.Fatal("Load() expected error for missing config file, got nil")
	}
}

func TestLoadInvalidYAML(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "bad.yaml")
	if err := os.WriteFile(path, []byte("network_cidr: [unterminated"), 0o600); err != nil {
		t.Fatalf("write temp config: %v", err)
	}

	fs := flag.NewFlagSet("test", flag.ContinueOnError)
	_, err := Load(fs, []string{"-config", path})
	if err == nil {
		t.Fatal("Load() expected error for invalid YAML, got nil")
	}
}

func TestMergeConfig(t *testing.T) {
	base := Config{
		NetworkCIDR:   "192.168.1.0/24",
		Ports:         []int{9100},
		Interval:      60 * time.Second,
		ListenAddress: ":8080",
		MaxWorkers:    50,
	}

	t.Run("empty override keeps base", func(t *testing.T) {
		got := mergeConfig(base, Config{})
		if got.NetworkCIDR != base.NetworkCIDR || got.MaxWorkers != base.MaxWorkers {
			t.Errorf("mergeConfig with empty override changed base: %+v", got)
		}
		if len(got.Ports) != 1 {
			t.Errorf("Ports = %v, want base unchanged", got.Ports)
		}
	})

	t.Run("non-zero fields override", func(t *testing.T) {
		override := Config{
			NetworkCIDR: "10.0.0.0/8",
			Ports:       []int{1, 2, 3},
			MaxWorkers:  99,
		}
		got := mergeConfig(base, override)
		if got.NetworkCIDR != "10.0.0.0/8" {
			t.Errorf("NetworkCIDR = %q, want overridden", got.NetworkCIDR)
		}
		if len(got.Ports) != 3 {
			t.Errorf("Ports = %v, want overridden", got.Ports)
		}
		if got.MaxWorkers != 99 {
			t.Errorf("MaxWorkers = %d, want 99", got.MaxWorkers)
		}
		// Field absent from override retains base value.
		if got.ListenAddress != ":8080" {
			t.Errorf("ListenAddress = %q, want base :8080", got.ListenAddress)
		}
	})
}
