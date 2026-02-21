package discovery

import (
	"bufio"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"bodsch.me/promscout/internal/logging"
)

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
// It returns true and the valid path if successful.
func (v *Validator) Validate(target string) (bool, string) {
	for _, path := range v.metricsPaths {
		url := "http://" + target + path

		v.logger.Debug("probe url", "url", url)

		resp, err := v.client.Get(url)
		if err != nil {
			continue
		}

		if resp.StatusCode < 200 || resp.StatusCode >= 300 {
			resp.Body.Close()
			continue
		}

		reader := bufio.NewReader(io.LimitReader(resp.Body, 8192))
		data, err := io.ReadAll(reader)
		resp.Body.Close()

		if err != nil {
			continue
		}

		body := string(data)

		v.logger.Debug("result", "data", body)

		if isPrometheusFormat(body) {
			v.logger.Debug("metrics endpoint validated",
				"target", target,
				"path", path,
			)
			return true, path
		}
	}

	return false, ""
}

// isPrometheusFormat performs strict validation of
// Prometheus exposition format to avoid HTML false positives.
func isPrometheusFormat(body string) bool {
	lower := strings.ToLower(body)

	// Explicit HTML detection
	if strings.Contains(lower, "<html") ||
		strings.Contains(lower, "<head") ||
		strings.Contains(lower, "<body") {
		return false
	}

	// Strong indicator: HELP or TYPE
	if strings.Contains(body, "# HELP") ||
		strings.Contains(body, "# TYPE") {
		return true
	}

	lines := strings.Split(body, "\n")

	for _, line := range lines {
		line = strings.TrimSpace(line)

		if line == "" {
			continue
		}

		// Skip comments
		if strings.HasPrefix(line, "#") {
			continue
		}

		// Must contain exactly 2 fields minimum
		fields := strings.Fields(line)
		if len(fields) < 2 {
			continue
		}

		// First field must not contain HTML markers
		if strings.Contains(fields[0], "<") ||
			strings.Contains(fields[0], ">") {
			continue
		}

		// Second field must be a valid float
		if _, err := strconv.ParseFloat(fields[1], 64); err != nil {
			continue
		}

		// Looks like a valid metric line
		return true
	}

	return false
}
