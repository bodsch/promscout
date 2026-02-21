package config

import (
	"flag"
	"fmt"
	"os"
	"time"

	"gopkg.in/yaml.v3"
)

type Config struct {
	NetworkCIDR   string        `yaml:"network_cidr"`
	Ports         []int         `yaml:"ports"`
	MetricsPaths  []string      `yaml:"metrics_paths"`
	Interval      time.Duration `yaml:"interval"`
	Timeout       time.Duration `yaml:"timeout"`
	ListenAddress string        `yaml:"listen_address"`
	LogFormat     string        `yaml:"log_format"`
	LogLevel      string        `yaml:"log_level"`
	MaxWorkers    int           `yaml:"max_workers"`
}

func Load(fs *flag.FlagSet, args []string) (Config, error) {

	cfg := Config{
		NetworkCIDR:   "192.168.1.0/24",
		Ports:         []int{9100, 9115, 9121},
		MetricsPaths:  []string{"/", "/metrics"},
		Interval:      60 * time.Second,
		Timeout:       3 * time.Second,
		ListenAddress: ":8080",
		LogFormat:     "json",
		LogLevel:      "info",
		MaxWorkers:    50,
	}

	var configFile string

	fs.StringVar(&configFile, "config", "", "Path to YAML config file")
	fs.StringVar(&cfg.NetworkCIDR, "cidr", cfg.NetworkCIDR, "Network CIDR to scan")
	fs.StringVar(&cfg.ListenAddress, "listen", cfg.ListenAddress, "HTTP listen address")
	fs.DurationVar(&cfg.Interval, "interval", cfg.Interval, "Scan interval")
	fs.DurationVar(&cfg.Timeout, "timeout", cfg.Timeout, "Connection timeout")
	fs.StringVar(&cfg.LogFormat, "log-format", cfg.LogFormat, "Log format (json|text)")
	fs.StringVar(&cfg.LogLevel, "log-level", cfg.LogLevel, "Log level (debug|info|error)")
	fs.IntVar(&cfg.MaxWorkers, "workers", cfg.MaxWorkers, "Maximum parallel scan workers")

	if err := fs.Parse(args); err != nil {
		return cfg, err
	}

	if configFile != "" {
		data, err := os.ReadFile(configFile)
		if err != nil {
			return cfg, fmt.Errorf("failed to read config file: %w", err)
		}

		var yamlCfg Config
		if err := yaml.Unmarshal(data, &yamlCfg); err != nil {
			return cfg, fmt.Errorf("failed to parse YAML config: %w", err)
		}

		cfg = mergeConfig(cfg, yamlCfg)
	}

	return cfg, nil
}

func mergeConfig(base, override Config) Config {
	if override.NetworkCIDR != "" {
		base.NetworkCIDR = override.NetworkCIDR
	}
	if len(override.Ports) > 0 {
		base.Ports = override.Ports
	}
	if len(override.MetricsPaths) > 0 {
		base.MetricsPaths = override.MetricsPaths
	}
	if override.Interval != 0 {
		base.Interval = override.Interval
	}
	if override.Timeout != 0 {
		base.Timeout = override.Timeout
	}
	if override.ListenAddress != "" {
		base.ListenAddress = override.ListenAddress
	}
	if override.LogFormat != "" {
		base.LogFormat = override.LogFormat
	}
	if override.LogLevel != "" {
		base.LogLevel = override.LogLevel
	}
	if override.MaxWorkers != 0 {
		base.MaxWorkers = override.MaxWorkers
	}
	return base
}
