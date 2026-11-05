package config

import (
	"flag"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// loadArgs runs Load against a fresh flag set.
func loadArgs(t *testing.T, args ...string) (Config, error) {
	t.Helper()
	return Load(flag.NewFlagSet("test", flag.ContinueOnError), args)
}

// loadYAML writes body to a temp file and loads it via -config.
func loadYAML(t *testing.T, body string) (Config, error) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}
	return loadArgs(t, "-config", path)
}

// TestLoadRejectsValuesTheServiceCannotRunWith covers configuration that used
// to be accepted silently and only failed later, somewhere less helpful.
//
// Each case names what happened in production before Load validated:
//
//   - a non-positive interval reached time.NewTicker, which panics: the
//     process died with a stack trace right after starting its first cycle;
//   - a non-positive timeout reached http.Client, where zero or negative means
//     "no timeout": one host that accepts the connection and never answers
//     held a validation worker for ever;
//   - an unparsable CIDR was only logged by the scanner, so every cycle
//     "completed" with nothing, the service reported ready, and the empty
//     result overwrote the warm-start cache;
//   - a metrics path without a leading slash was glued onto host:port, so
//     "@other-host/" turned the scanned address into userinfo and sent the
//     probe to a different host altogether;
//   - an unknown log level or format silently fell back to info/json.
func TestLoadRejectsValuesTheServiceCannotRunWith(t *testing.T) {
	cases := []struct {
		name string
		args []string
		yaml string
		want string
	}{
		{name: "zero interval via CLI", args: []string{"-interval", "0"}, want: "interval"},
		{name: "negative interval via YAML", yaml: "interval: -5s\n", want: "interval"},
		{name: "zero timeout via CLI", args: []string{"-timeout", "0"}, want: "timeout"},
		{name: "negative timeout via YAML", yaml: "timeout: -1s\n", want: "timeout"},
		{name: "negative dial timeout", args: []string{"-dial-timeout", "-1s"}, want: "dial_timeout"},
		{name: "unparsable CIDR", args: []string{"-cidr", "192.168.0.0/33"}, want: "network_cidr"},
		{name: "port zero", yaml: "ports: [0]\n", want: "port"},
		{name: "port above range", yaml: "ports: [70000]\n", want: "port"},
		{name: "path without slash", yaml: "metrics_paths: [\"@evil.example/\"]\n", want: "metrics path"},
		{name: "zero scan workers via CLI", args: []string{"-workers", "0"}, want: "max_workers"},
		{name: "zero validate workers via CLI", args: []string{"-validate-workers", "0"}, want: "validate_workers"},
		{name: "unknown log level", args: []string{"-log-level", "verbose"}, want: "log_level"},
		{name: "unknown log format", args: []string{"-log-format", "txt"}, want: "log_format"},
		{name: "empty listen address", args: []string{"-listen", ""}, want: "listen_address"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var err error
			if tc.yaml != "" {
				_, err = loadYAML(t, tc.yaml)
			} else {
				_, err = loadArgs(t, tc.args...)
			}
			if err == nil {
				t.Fatalf("Load accepted the configuration; the service would start and fail later")
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Errorf("error %q does not name the offending setting %q", err, tc.want)
			}
		})
	}
}

// TestLoadReportsEveryInvalidSetting: an operator fixing a config one error
// per restart is slow and annoying; all problems come back at once.
func TestLoadReportsEveryInvalidSetting(t *testing.T) {
	_, err := loadArgs(t, "-interval", "0", "-cidr", "nope", "-log-format", "xml")
	if err == nil {
		t.Fatal("Load accepted three invalid settings")
	}
	for _, want := range []string{"interval", "network_cidr", "log_format"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q is missing %q", err, want)
		}
	}
}

// TestLoadAcceptsTheShippedExamples pins the other side: validation must not
// reject what the project itself documents. config.example and the defaults are
// what operators copy.
func TestLoadAcceptsTheShippedExamples(t *testing.T) {
	if _, err := loadArgs(t); err != nil {
		t.Fatalf("the built-in defaults are rejected: %v", err)
	}
	if _, err := loadArgs(t, "-config", filepath.Join("..", "..", "config.example")); err != nil {
		t.Fatalf("config.example is rejected: %v", err)
	}
	// dial_timeout 0 is documented in the scanner as "fall back to timeout".
	if _, err := loadArgs(t, "-dial-timeout", "0"); err != nil {
		t.Fatalf("dial_timeout 0 is rejected although the scanner falls back to timeout: %v", err)
	}
	// The logger lower-cases the level, so this has always worked.
	if _, err := loadArgs(t, "-log-level", "DEBUG"); err != nil {
		t.Fatalf("an upper-case log level is rejected: %v", err)
	}
}
