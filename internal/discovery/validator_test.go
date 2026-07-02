package discovery

import (
	"net/http"
	"net/http/httptest"
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
		ok, path := v.Validate(ts.Listener.Addr().String())
		if !ok || path != "/metrics" {
			t.Fatalf("Validate() = (%v, %q), want (true, \"/metrics\")", ok, path)
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
		ok, path := v.Validate(ts.Listener.Addr().String())
		if !ok || path != "/metrics" {
			t.Fatalf("Validate() = (%v, %q), want (true, \"/metrics\")", ok, path)
		}
	})

	t.Run("non-2xx status is not valid", func(t *testing.T) {
		ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			http.Error(w, "boom", http.StatusInternalServerError)
		}))
		defer ts.Close()

		v := newTestValidator([]string{"/metrics"})
		ok, path := v.Validate(ts.Listener.Addr().String())
		if ok || path != "" {
			t.Fatalf("Validate() = (%v, %q), want (false, \"\")", ok, path)
		}
	})

	t.Run("html body is not valid", func(t *testing.T) {
		ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			_, _ = w.Write([]byte("<html><body>no metrics here</body></html>"))
		}))
		defer ts.Close()

		v := newTestValidator([]string{"/", "/metrics"})
		ok, _ := v.Validate(ts.Listener.Addr().String())
		if ok {
			t.Fatalf("Validate() = true, want false for HTML body")
		}
	})

	t.Run("unreachable target is not valid", func(t *testing.T) {
		// Start and immediately close a server so the address is free;
		// the connection is refused fast instead of timing out.
		ts := httptest.NewServer(http.NotFoundHandler())
		addr := ts.Listener.Addr().String()
		ts.Close()

		v := newTestValidator([]string{"/metrics"})
		ok, _ := v.Validate(addr)
		if ok {
			t.Fatalf("Validate() = true, want false for unreachable target")
		}
	})
}
