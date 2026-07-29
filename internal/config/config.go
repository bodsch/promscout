package config

import (
	"flag"
	"fmt"
	"os"
	"time"

	"gopkg.in/yaml.v3"
)

type Config struct {
	NetworkCIDR     string        `yaml:"network_cidr"`
	Ports           []int         `yaml:"ports"`
	MetricsPaths    []string      `yaml:"metrics_paths"`
	Interval        time.Duration `yaml:"interval"`
	Timeout         time.Duration `yaml:"timeout"`
	DialTimeout     time.Duration `yaml:"dial_timeout"`
	ListenAddress   string        `yaml:"listen_address"`
	LogFormat       string        `yaml:"log_format"`
	LogLevel        string        `yaml:"log_level"`
	MaxWorkers      int           `yaml:"max_workers"`
	ValidateWorkers int           `yaml:"validate_workers"`
	CacheFile       string        `yaml:"cache_file"`
	GuessJob        bool          `yaml:"guess_job"`
}

// Load resolves the effective configuration by layering three sources
// with increasing precedence: built-in defaults, an optional YAML file
// (via -config), and explicitly-set CLI flags on top.
//
// Precedence: defaults < YAML file < CLI flags.
//
// Only flags the user actually set on the command line override the YAML
// file; flags left at their default value do not, which is what allows
// `-config file.yaml -cidr 10.0.0.0/8` to honour the CLI CIDR while
// still taking every other value from the file.
//
// Parameters:
//   - fs: the flag set to register on and parse (callers may pre-register
//     their own flags, e.g. -version, before calling Load).
//   - args: the argument slice to parse (typically os.Args[1:]).
//
// Returns the merged configuration, or an error if the flags cannot be
// parsed or the referenced config file cannot be read or decoded.
func Load(fs *flag.FlagSet, args []string) (Config, error) {

	defaults := Config{
		NetworkCIDR:     "192.168.1.0/24",
		Ports:           []int{9100, 9115, 9121},
		MetricsPaths:    []string{"/", "/metrics"},
		Interval:        60 * time.Second,
		Timeout:         3 * time.Second,
		DialTimeout:     800 * time.Millisecond,
		ListenAddress:   ":8080",
		LogFormat:       "json",
		LogLevel:        "info",
		MaxWorkers:      512,
		ValidateWorkers: 50,
		CacheFile:       "",
		GuessJob:        false,
	}

	// Flags are bound to a scratch copy so parsing them does not mutate
	// the layered result. Their default values mirror the defaults above
	// only for a helpful -help listing; the actual values are applied
	// afterwards via fs.Visit, which reports only explicitly-set flags.
	flagCfg := defaults
	var configFile string

	fs.StringVar(&configFile, "config", "", "Path to YAML config file")
	fs.StringVar(&flagCfg.NetworkCIDR, "cidr", flagCfg.NetworkCIDR, "Network CIDR to scan")
	fs.StringVar(&flagCfg.ListenAddress, "listen", flagCfg.ListenAddress, "HTTP listen address")
	fs.DurationVar(&flagCfg.Interval, "interval", flagCfg.Interval, "Scan interval")
	fs.DurationVar(&flagCfg.Timeout, "timeout", flagCfg.Timeout, "HTTP validation timeout")
	fs.DurationVar(&flagCfg.DialTimeout, "dial-timeout", flagCfg.DialTimeout, "TCP dial timeout for the port probe")
	fs.StringVar(&flagCfg.LogFormat, "log-format", flagCfg.LogFormat, "Log format (json|text)")
	fs.StringVar(&flagCfg.LogLevel, "log-level", flagCfg.LogLevel, "Log level (debug|info|error)")
	fs.IntVar(&flagCfg.MaxWorkers, "workers", flagCfg.MaxWorkers, "Maximum parallel TCP scan workers")
	fs.IntVar(&flagCfg.ValidateWorkers, "validate-workers", flagCfg.ValidateWorkers, "Maximum parallel HTTP validation workers")
	fs.StringVar(&flagCfg.CacheFile, "cache-file", flagCfg.CacheFile, "Optional path to persist discovered targets for a warm start")
	fs.BoolVar(&flagCfg.GuessJob, "guess-job", flagCfg.GuessJob, "Derive a best-effort 'job' label from the exposed metrics (opt-in)")

	if err := fs.Parse(args); err != nil {
		return defaults, err
	}

	// Layer 1 + 2: defaults, then the YAML file on top of them.
	cfg := defaults
	if configFile != "" {
		// configFile is an operator-supplied path passed via the -config
		// CLI flag; reading it is the intended behaviour, not attacker input.
		data, err := os.ReadFile(configFile) // #nosec G304 -- path is a trusted operator-provided CLI flag
		if err != nil {
			return defaults, fmt.Errorf("failed to read config file: %w", err)
		}

		var yamlCfg Config
		if err := yaml.Unmarshal(data, &yamlCfg); err != nil {
			return defaults, fmt.Errorf("failed to parse YAML config: %w", err)
		}

		cfg = mergeConfig(cfg, yamlCfg)
	}

	// Layer 3: explicitly-set CLI flags win over everything else.
	applyFlagOverrides(fs, &cfg, flagCfg)

	return cfg, nil
}

// applyFlagOverrides copies the value of every flag the user explicitly
// set on the command line from the parsed flagCfg into cfg, giving CLI
// flags the highest precedence. Flags left at their default are ignored,
// so they never clobber a value provided by the YAML file.
func applyFlagOverrides(fs *flag.FlagSet, cfg *Config, flagCfg Config) {
	fs.Visit(func(f *flag.Flag) {
		switch f.Name {
		case "cidr":
			cfg.NetworkCIDR = flagCfg.NetworkCIDR
		case "listen":
			cfg.ListenAddress = flagCfg.ListenAddress
		case "interval":
			cfg.Interval = flagCfg.Interval
		case "timeout":
			cfg.Timeout = flagCfg.Timeout
		case "dial-timeout":
			cfg.DialTimeout = flagCfg.DialTimeout
		case "log-format":
			cfg.LogFormat = flagCfg.LogFormat
		case "log-level":
			cfg.LogLevel = flagCfg.LogLevel
		case "workers":
			cfg.MaxWorkers = flagCfg.MaxWorkers
		case "validate-workers":
			cfg.ValidateWorkers = flagCfg.ValidateWorkers
		case "cache-file":
			cfg.CacheFile = flagCfg.CacheFile
		case "guess-job":
			cfg.GuessJob = flagCfg.GuessJob
		}
	})
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
	if override.DialTimeout != 0 {
		base.DialTimeout = override.DialTimeout
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
	if override.ValidateWorkers != 0 {
		base.ValidateWorkers = override.ValidateWorkers
	}
	if override.CacheFile != "" {
		base.CacheFile = override.CacheFile
	}
	// GuessJob is opt-in and defaults to false, so YAML can only enable
	// it. Disabling again is done via the -guess-job=false CLI flag,
	// which is applied afterwards with higher precedence.
	if override.GuessJob {
		base.GuessJob = true
	}
	return base
}
