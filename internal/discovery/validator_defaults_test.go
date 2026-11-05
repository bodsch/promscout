package discovery

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"bodsch.me/promscout/internal/logging"
)

const promTestBody = "# HELP go_info x\n# TYPE go_info gauge\ngo_info 1\n"

// TestValidatorIgnoresProxyEnvironment covers a default nobody chose: an
// http.Client without its own Transport uses http.DefaultTransport, which
// routes every request through HTTP_PROXY.
//
// The scanner dials hosts directly, so on a machine with a proxy configured —
// the normal state on corporate networks — the two stages measured different
// things: the port was open on the scanned host, but the metrics check was
// answered by the proxy. A proxy that serves anything with 200 turns every open
// port into a "valid target"; one that cannot reach the scanned network makes
// every real exporter disappear.
//
// net/http reads the proxy environment exactly once per process, so the check
// runs in a child process with HTTP_PROXY set before anything touched it. The
// target is a TEST-NET address, because Go never proxies loopback requests.
func TestValidatorIgnoresProxyEnvironment(t *testing.T) {
	if os.Getenv("PROMSCOUT_PROXY_CHILD") == "1" {
		v := NewValidator(500*time.Millisecond, []string{"/metrics"}, logging.New("text", "error"))
		if ok, _ := v.Validate(context.Background(), "192.0.2.1:9100"); ok {
			t.Fatal("192.0.2.1 validated: the probe was answered by HTTP_PROXY, not by the scanned host")
		}
		return
	}

	var proxyHits atomic.Int64
	proxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		proxyHits.Add(1)
		_, _ = w.Write([]byte(promTestBody))
	}))
	t.Cleanup(proxy.Close)

	cmd := exec.Command(os.Args[0], "-test.run=^TestValidatorIgnoresProxyEnvironment$", "-test.count=1")
	cmd.Env = append(os.Environ(),
		"PROMSCOUT_PROXY_CHILD=1",
		"HTTP_PROXY="+proxy.URL, "http_proxy="+proxy.URL,
		"NO_PROXY=", "no_proxy=",
	)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("child process failed: %v\n%s", err, out)
	}
	if n := proxyHits.Load(); n != 0 {
		t.Errorf("the proxy received %d probe(s); the validator must talk to the scanned host directly", n)
	}
}

// TestValidateDoesNotFollowRedirects covers the second default of the stock
// client: it follows up to ten redirects, to any host.
//
// Every host in the CIDR gets to answer the probe, so any of them could send
// the validator elsewhere — outside the scanned range, to a link-local metadata
// service — and have the body it fetched there accepted as its own. The derived
// exporter name and version then appear as __meta_promscout_* labels on the
// unauthenticated HTTP-SD endpoint. The published target would also be the
// redirecting address, not the one that actually serves metrics.
func TestValidateDoesNotFollowRedirects(t *testing.T) {
	var elsewhereHits atomic.Int64
	elsewhere := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		elsewhereHits.Add(1)
		_, _ = w.Write([]byte(promTestBody))
	}))
	t.Cleanup(elsewhere.Close)

	redirector := httptest.NewServer(http.RedirectHandler(elsewhere.URL+"/metrics", http.StatusFound))
	t.Cleanup(redirector.Close)

	v := NewValidator(time.Second, []string{"/"}, logging.New("text", "error"))
	if ok, res := v.Validate(context.Background(), redirector.Listener.Addr().String()); ok {
		t.Errorf("Validate() = (true, %+v) for a host that only redirects elsewhere", res)
	}
	if n := elsewhereHits.Load(); n != 0 {
		t.Errorf("the redirect target received %d request(s); the validator must not leave the scanned host", n)
	}
}

// TestValidateFallsThroughARedirectToTheNextPath: refusing redirects must not
// lose exporters that redirect "/" to their real path; the configured path list
// finds them directly.
func TestValidateFallsThroughARedirectToTheNextPath(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/metrics" {
			_, _ = w.Write([]byte(promTestBody))
			return
		}
		http.Redirect(w, r, "/metrics", http.StatusFound)
	}))
	t.Cleanup(ts.Close)

	v := NewValidator(time.Second, []string{"/", "/metrics"}, logging.New("text", "error"))
	ok, res := v.Validate(context.Background(), ts.Listener.Addr().String())
	if !ok || res.Path != "/metrics" {
		t.Fatalf("Validate() = (%v, %q), want (true, \"/metrics\")", ok, res.Path)
	}
}

// TestValidateStopsOnCancellation verifies the doc comment of Validate:
// "cancelling it aborts the in-flight request and stops the remaining probes".
//
// A shutdown cancels the context while validation workers are blocked on slow
// hosts. If cancellation only took effect at the HTTP timeout, every worker
// would sit out its full timeout on every remaining path, and the process would
// outlive its shutdown grace period and be killed.
func TestValidateStopsOnCancellation(t *testing.T) {
	var hits atomic.Int64
	release := make(chan struct{})
	ts := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		select {
		case <-release:
		case <-r.Context().Done():
		}
	}))
	t.Cleanup(func() { close(release); ts.Close() })

	v := NewValidator(30*time.Second, []string{"/a", "/b", "/c"}, logging.New("text", "error"))

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan bool, 1)
	go func() {
		ok, _ := v.Validate(ctx, ts.Listener.Addr().String())
		done <- ok
	}()

	time.Sleep(100 * time.Millisecond)
	cancel()

	select {
	case ok := <-done:
		if ok {
			t.Error("a cancelled validation reported success")
		}
	case <-time.After(3 * time.Second):
		t.Fatal("Validate kept waiting after cancellation; shutdown would wait out the 30 s timeout per path")
	}
	if n := hits.Load(); n != 1 {
		t.Errorf("the host saw %d request(s), want 1: the remaining paths must not be probed after cancellation", n)
	}
}

// TestAnalyzeExpositionRejectsStatusPages: the README promises "strict
// Prometheus metrics validation", but a sample line was accepted as soon as
// its second field parsed as a number — the metric name was never checked.
//
// nginx's stub_status page has the line "16630948 16630948 31070465", which
// therefore validated as metrics, became a target, and — via the prefix
// heuristic — got the exporter name "16630948". Prometheus then scraped a page
// that fails to parse, and the target sat permanently "down" in every alert.
func TestAnalyzeExpositionRejectsStatusPages(t *testing.T) {
	cases := map[string]string{
		"nginx stub_status": "Active connections: 291 \n" +
			"server accepts handled requests\n" +
			" 16630948 16630948 31070465 \n" +
			"Reading: 6 Writing: 179 Waiting: 106 \n",
		"leading digit":     "9lives 1\n",
		"dash in name":      "my-metric 1\n",
		"dot in name":       "some.value 42\n",
		"key=value listing": "uptime=12 3\n",
	}
	for name, body := range cases {
		t.Run(name, func(t *testing.T) {
			if ok, meta := analyzeExposition(strings.NewReader(body)); ok {
				t.Errorf("accepted as metrics (exporter %q): the line has no valid metric name", meta.exporter)
			}
		})
	}
}

// TestAnalyzeExpositionStillAcceptsValidNames pins the other side of the name
// check: everything the exposition format allows must keep validating,
// including colons (recording rules) and a leading underscore.
func TestAnalyzeExpositionStillAcceptsValidNames(t *testing.T) {
	for _, body := range []string{
		"node_load1 0.5\n",
		"job:http_requests:rate5m 12\n",
		"_private_metric 1\n",
		"http_requests_total{code=\"200\"} 1027 1395066363000\n",
		"http_requests_total{path=\"/a b\",msg=\"x} \\\"y\\\"\"} 3\n",
		"foo_total 17 # {trace_id=\"KOO5S4vxi0o\"} 0.67\n",
		"up NaN\n",
		"temperature -Inf\n",
	} {
		if ok, _ := analyzeExposition(strings.NewReader(body)); !ok {
			t.Errorf("rejected valid exposition %q", body)
		}
	}
}
