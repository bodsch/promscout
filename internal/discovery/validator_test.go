package discovery

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"bodsch.me/promscout/internal/logging"
)

func TestIsPrometheusFormat(t *testing.T) {
	tests := []struct {
		name string
		body string
		want bool
	}{
		{"empty", "", false},
		{"whitespace only", "   \n\t\n", false},
		{"help comment", "# HELP go_info Information about the Go env.\n", true},
		{"type comment", "# TYPE go_info gauge\n", true},
		{"plain metric", "go_goroutines 42\n", true},
		{"float metric", "process_cpu_seconds_total 1.5\n", true},
		{"metric with labels", `go_gc_duration_seconds{quantile="0"} 0.0001` + "\n", true},
		{"html page", "<html><head><title>hi</title></head><body>x</body></html>", false},
		{"html head only", "<HEAD>x</HEAD>", false},
		{"html wins over metric marker", "<html># TYPE go_info gauge", false},
		{"non numeric value", "some_metric not_a_number\n", false},
		{"single field", "loremipsum\n", false},
		{"only comments", "# just a comment\n# another one\n", false},
		{"angle bracket in name", "<div> 1\n", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := isPrometheusFormat(tt.body); got != tt.want {
				t.Errorf("isPrometheusFormat(%q) = %v, want %v", tt.body, got, tt.want)
			}
		})
	}
}

func newTestValidator(paths []string) *Validator {
	return NewValidator(2*time.Second, paths, logging.New("text", "error"))
}

func TestAnalyzeExpositionServiceDerivation(t *testing.T) {
	tests := []struct {
		name         string
		body         string
		wantValid    bool
		wantExporter string
		wantVersion  string
		wantSource   string
	}{
		{
			name:         "target_info wins",
			body:         "# HELP x y\ntarget_info{service_name=\"checkout\",service_namespace=\"shop\"} 1\nnode_cpu 1\n",
			wantValid:    true,
			wantExporter: "checkout",
			wantSource:   "target_info",
		},
		{
			name:         "build_info name and version",
			body:         "node_exporter_build_info{branch=\"HEAD\",version=\"1.7.0\",goversion=\"go1.21\"} 1\nnode_cpu 1\n",
			wantValid:    true,
			wantExporter: "node_exporter",
			wantVersion:  "1.7.0",
			wantSource:   "build_info",
		},
		{
			name:         "dominant prefix fallback ignores runtime metrics",
			body:         "go_goroutines 5\nprocess_cpu 1\npg_up 1\npg_stat_activity 3\npg_locks 2\n",
			wantValid:    true,
			wantExporter: "pg",
			wantSource:   "prefix",
		},
		{
			name:       "valid but unidentifiable leaves metadata empty",
			body:       "# HELP x y\n# TYPE x gauge\n",
			wantValid:  true,
			wantSource: "",
		},
		{
			name:      "html is rejected",
			body:      "<html><body>nope</body></html>",
			wantValid: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ok, meta := analyzeExposition(strings.NewReader(tt.body))
			if ok != tt.wantValid {
				t.Fatalf("valid = %v, want %v", ok, tt.wantValid)
			}
			if meta.exporter != tt.wantExporter {
				t.Errorf("exporter = %q, want %q", meta.exporter, tt.wantExporter)
			}
			if meta.version != tt.wantVersion {
				t.Errorf("version = %q, want %q", meta.version, tt.wantVersion)
			}
			if meta.source != tt.wantSource {
				t.Errorf("source = %q, want %q", meta.source, tt.wantSource)
			}
		})
	}
}

func TestValidate(t *testing.T) {
	const promBody = "# HELP go_info x\n# TYPE go_info gauge\ngo_info 1\n"

	t.Run("valid metrics endpoint", func(t *testing.T) {
		ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path == "/metrics" {
				_, _ = w.Write([]byte(promBody))
				return
			}
			http.NotFound(w, r)
		}))
		defer ts.Close()

		v := newTestValidator([]string{"/", "/metrics"})
		ok, res := v.Validate(context.Background(), ts.Listener.Addr().String())
		if !ok || res.Path != "/metrics" {
			t.Fatalf("Validate() = (%v, %q), want (true, \"/metrics\")", ok, res.Path)
		}
	})

	t.Run("html on first path falls through to metrics", func(t *testing.T) {
		ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			switch r.URL.Path {
			case "/":
				_, _ = w.Write([]byte("<html><body>welcome</body></html>"))
			case "/metrics":
				_, _ = w.Write([]byte(promBody))
			default:
				http.NotFound(w, r)
			}
		}))
		defer ts.Close()

		v := newTestValidator([]string{"/", "/metrics"})
		ok, res := v.Validate(context.Background(), ts.Listener.Addr().String())
		if !ok || res.Path != "/metrics" {
			t.Fatalf("Validate() = (%v, %q), want (true, \"/metrics\")", ok, res.Path)
		}
	})

	t.Run("non-2xx status is not valid", func(t *testing.T) {
		ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			http.Error(w, "boom", http.StatusInternalServerError)
		}))
		defer ts.Close()

		v := newTestValidator([]string{"/metrics"})
		ok, res := v.Validate(context.Background(), ts.Listener.Addr().String())
		if ok || res.Path != "" {
			t.Fatalf("Validate() = (%v, %q), want (false, \"\")", ok, res.Path)
		}
	})

	t.Run("html body is not valid", func(t *testing.T) {
		ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			_, _ = w.Write([]byte("<html><body>no metrics here</body></html>"))
		}))
		defer ts.Close()

		v := newTestValidator([]string{"/", "/metrics"})
		ok, _ := v.Validate(context.Background(), ts.Listener.Addr().String())
		if ok {
			t.Fatalf("Validate() = true, want false for HTML body")
		}
	})

	t.Run("valid metric beyond the old 8KiB read limit", func(t *testing.T) {
		// A long comment preamble (no HELP/TYPE) pushes the first real
		// metric line well past 8 KiB. The previous fixed-size read
		// truncated it and produced a false negative.
		var sb strings.Builder
		for sb.Len() < 16*1024 {
			sb.WriteString("# padding comment line without help or type markers\n")
		}
		sb.WriteString("go_info 1\n")
		body := sb.String()

		ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path == "/metrics" {
				_, _ = w.Write([]byte(body))
				return
			}
			http.NotFound(w, r)
		}))
		defer ts.Close()

		v := newTestValidator([]string{"/metrics"})
		ok, res := v.Validate(context.Background(), ts.Listener.Addr().String())
		if !ok || res.Path != "/metrics" {
			t.Fatalf("Validate() = (%v, %q), want (true, \"/metrics\")", ok, res.Path)
		}
	})

	t.Run("large HTML body is still rejected", func(t *testing.T) {
		// Ensure the streaming detector rejects HTML even when large.
		var sb strings.Builder
		sb.WriteString("<html>\n")
		for sb.Len() < 32*1024 {
			sb.WriteString("<div>filler content</div>\n")
		}
		body := sb.String()

		ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			_, _ = w.Write([]byte(body))
		}))
		defer ts.Close()

		v := newTestValidator([]string{"/"})
		if ok, _ := v.Validate(context.Background(), ts.Listener.Addr().String()); ok {
			t.Fatal("Validate() = true, want false for large HTML body")
		}
	})

	t.Run("unreachable target is not valid", func(t *testing.T) {
		// Start and immediately close a server so the address is free;
		// the connection is refused fast instead of timing out.
		ts := httptest.NewServer(http.NotFoundHandler())
		addr := ts.Listener.Addr().String()
		ts.Close()

		v := newTestValidator([]string{"/metrics"})
		ok, _ := v.Validate(context.Background(), addr)
		if ok {
			t.Fatalf("Validate() = true, want false for unreachable target")
		}
	})
}
