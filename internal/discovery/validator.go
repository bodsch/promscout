package discovery

import (
	"bufio"
	"context"
	"io"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"

	"bodsch.me/promscout/internal/logging"
)

// maxValidateBytes bounds how much of a response body is inspected while
// probing for the Prometheus exposition format. It is a safeguard for the
// negative case only: a positive match exits early and usually reads just
// the first few lines. It is intentionally generous (far above a typical
// comment preamble) so a valid endpoint is never rejected merely because
// its first metric line sits deep in the body.
const maxValidateBytes = 1 << 20 // 1 MiB

// Service metadata sources, reported via ProbeResult.Source.
const (
	sourceTargetInfo = "target_info"
	sourceBuildInfo  = "build_info"
	sourcePrefix     = "prefix"
)

// reServiceName extracts the OpenTelemetry service_name label value.
// reVersion extracts a version label value, anchored to a label boundary
// ({ or ,) so it does not accidentally match e.g. goversion="...".
var (
	reServiceName = regexp.MustCompile(`service_name="((?:[^"\\]|\\.)*)"`)
	reVersion     = regexp.MustCompile(`[{,]version="((?:[^"\\]|\\.)*)"`)
)

// ignoredPrefixes are metric namespaces present on virtually every Go
// exporter; they carry no service identity and must not win the
// namespace-prefix heuristic.
var ignoredPrefixes = map[string]bool{
	"go":       true,
	"process":  true,
	"promhttp": true,
	"scrape":   true,
}

// ProbeResult describes a validated Prometheus endpoint together with the
// service metadata derived from its exposition body.
type ProbeResult struct {
	// Path is the metrics path that validated.
	Path string
	// Exporter is the derived service/exporter name; empty if unknown.
	Exporter string
	// Version is the build version if one was discovered; empty otherwise.
	Version string
	// Source records how Exporter was derived (see source* constants).
	Source string
}

// Validator validates Prometheus endpoints.
type Validator struct {
	timeout      time.Duration
	client       *http.Client
	metricsPaths []string
	logger       *logging.Logger
}

// NewValidator creates a new metrics validator.
func NewValidator(timeout time.Duration, paths []string, logger *logging.Logger) *Validator {
	return &Validator{
		timeout:      timeout,
		metricsPaths: paths,
		logger:       logger,
		client: &http.Client{
			Timeout: timeout,
		},
	}
}

// Validate checks whether a target exposes a valid Prometheus metrics endpoint.
//
// Parameters:
//   - ctx: cancellation context propagated to each HTTP probe; cancelling
//     it aborts the in-flight request and stops the remaining probes.
//   - target: the "host:port" address to probe.
//
// It returns true and a ProbeResult (matching path plus any derived
// service metadata) for the first path that serves a valid Prometheus
// exposition body, otherwise false and a zero ProbeResult.
func (v *Validator) Validate(ctx context.Context, target string) (bool, ProbeResult) {
	for _, path := range v.metricsPaths {
		if ctx.Err() != nil {
			return false, ProbeResult{}
		}

		url := "http://" + target + path

		v.logger.Debug("probe url", "url", url)

		req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
		if err != nil {
			continue
		}

		resp, err := v.client.Do(req)
		if err != nil {
			continue
		}

		if resp.StatusCode < 200 || resp.StatusCode >= 300 {
			_ = resp.Body.Close()
			continue
		}

		// Inspect the body as a stream and stop at the first decisive
		// line. The body is bounded by maxValidateBytes as a DoS guard;
		// a valid endpoint typically matches within the first few lines.
		ok, meta := analyzeExposition(io.LimitReader(resp.Body, maxValidateBytes))
		_ = resp.Body.Close()

		if ok {
			v.logger.Debug("metrics endpoint validated",
				"target", target,
				"path", path,
				"exporter", meta.exporter,
				"source", meta.source,
			)
			return true, ProbeResult{
				Path:     path,
				Exporter: meta.exporter,
				Version:  meta.version,
				Source:   meta.source,
			}
		}
	}

	return false, ProbeResult{}
}

// isPrometheusFormat performs strict validation of the Prometheus
// exposition format to avoid HTML false positives. It is a convenience
// wrapper over analyzeExposition for string inputs.
func isPrometheusFormat(body string) bool {
	ok, _ := analyzeExposition(strings.NewReader(body))
	return ok
}

// expositionMeta is the internal, derived service identity of a body.
type expositionMeta struct {
	exporter string
	version  string
	source   string
}

// analyzeExposition streams r line by line and reports whether it looks
// like the Prometheus exposition format, along with the derived service
// metadata. Detection rules:
//
//   - a line containing an HTML marker (<html/<head/<body) → not metrics
//   - a "# HELP" / "# TYPE" line → metrics
//   - a "name value" line whose value parses as a float → metrics
//
// Service derivation, in order of confidence:
//
//  1. target_info{service_name="…"}  (OpenTelemetry resource identity)
//  2. <name>_build_info{version="…"} (classic exporter build metric)
//  3. the dominant metric namespace prefix, ignoring go_/process_/…
//
// It returns as soon as the body is valid and a strong signal (1 or 2) is
// found, so the common case reads only a few lines. target_info and
// build_info do not coexist on real targets; if both appeared, whichever
// comes first is used.
func analyzeExposition(r io.Reader) (bool, expositionMeta) {
	scanner := bufio.NewScanner(r)
	// Allow long lines (verbose HELP text) up to the byte budget.
	scanner.Buffer(make([]byte, 0, 64*1024), maxValidateBytes)

	var (
		valid          bool
		serviceName    string
		buildName      string
		buildVersion   string
		prefixCounts   = map[string]int{}
		topPrefix      string
		topPrefixCount int
	)

	for scanner.Scan() {
		raw := scanner.Text()

		// Explicit HTML detection, evaluated per line.
		lower := strings.ToLower(raw)
		if strings.Contains(lower, "<html") ||
			strings.Contains(lower, "<head") ||
			strings.Contains(lower, "<body") {
			return false, expositionMeta{}
		}

		line := strings.TrimSpace(raw)
		if line == "" {
			continue
		}

		// HELP/TYPE comments prove the format but carry no identity.
		if strings.HasPrefix(line, "# HELP") || strings.HasPrefix(line, "# TYPE") {
			valid = true
			continue
		}
		if strings.HasPrefix(line, "#") {
			continue
		}

		name := metricNameFromLine(line)

		// Strong signal 1: OpenTelemetry target_info.
		if name == "target_info" {
			valid = true
			if m := reServiceName.FindStringSubmatch(line); m != nil {
				serviceName = unescapeLabelValue(m[1])
			}
			if serviceName != "" {
				break
			}
			continue
		}

		// Strong signal 2: <name>_build_info.
		if strings.HasSuffix(name, "_build_info") {
			valid = true
			buildName = strings.TrimSuffix(name, "_build_info")
			if m := reVersion.FindStringSubmatch(line); m != nil {
				buildVersion = unescapeLabelValue(m[1])
			}
			break
		}

		// Otherwise apply the strict metric-line check.
		fields := strings.Fields(line)
		if len(fields) < 2 {
			continue
		}
		if strings.ContainsAny(fields[0], "<>") {
			continue
		}
		if _, err := strconv.ParseFloat(fields[1], 64); err != nil {
			continue
		}

		valid = true

		// Feed the namespace-prefix heuristic (weakest signal).
		if p := metricPrefix(name); p != "" && !ignoredPrefixes[p] {
			prefixCounts[p]++
			if prefixCounts[p] > topPrefixCount {
				topPrefixCount = prefixCounts[p]
				topPrefix = p
			}
		}
	}

	if !valid {
		return false, expositionMeta{}
	}

	switch {
	case serviceName != "":
		return true, expositionMeta{exporter: serviceName, source: sourceTargetInfo}
	case buildName != "":
		return true, expositionMeta{exporter: buildName, version: buildVersion, source: sourceBuildInfo}
	case topPrefix != "":
		return true, expositionMeta{exporter: topPrefix, source: sourcePrefix}
	default:
		return true, expositionMeta{}
	}
}

// metricNameFromLine returns the metric name at the start of a sample
// line, i.e. everything up to the first '{' (label set) or whitespace.
func metricNameFromLine(line string) string {
	if i := strings.IndexAny(line, "{ \t"); i >= 0 {
		return line[:i]
	}
	return line
}

// metricPrefix returns the namespace prefix of a metric name: the segment
// before the first underscore (e.g. "node" for "node_cpu_seconds_total").
func metricPrefix(name string) string {
	if i := strings.IndexByte(name, '_'); i > 0 {
		return name[:i]
	}
	return name
}

// unescapeLabelValue reverses the Prometheus label-value escaping for the
// characters that may appear in an extracted value.
func unescapeLabelValue(s string) string {
	if !strings.Contains(s, `\`) {
		return s
	}
	return strings.NewReplacer(`\\`, `\`, `\"`, `"`, `\n`, "\n").Replace(s)
}
