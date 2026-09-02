package discovery

import (
	"context"
	"net"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"bodsch.me/promscout/internal/logging"
)

// addrOf returns host:port for a test server.
func addrOf(t *testing.T, ts *httptest.Server) string {
	t.Helper()

	host, port, err := net.SplitHostPort(ts.Listener.Addr().String())
	if err != nil {
		t.Fatalf("split host port: %v", err)
	}
	return net.JoinHostPort(host, port)
}

// TestValidateStopsReadingAnEndlessBody is the safeguard the limit's own comment
// describes, with nothing asserting it.
//
// This is the one place in the service where bytes arrive from a machine nobody
// configured: the scanner dials every host in the CIDR and the validator reads
// whatever answers. One host streaming without end — a log endpoint, a
// misrouted proxy, something hostile — would otherwise be read into memory in
// full, once per worker, while the scan is holding several of them open.
//
// If this fails, a single unlucky host in the range can exhaust the process.
func TestValidateStopsReadingAnEndlessBody(t *testing.T) {
	// Written from the handler goroutine and read from the test, so atomic.
	var sent atomic.Int64

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/plain")
		// Deliberately not the Prometheus format: a positive match exits early,
		// and the limit is documented as covering the negative case.
		block := []byte(strings.Repeat("this is not an exposition format\n", 512))
		for {
			n, err := w.Write(block)
			sent.Add(int64(n))
			if err != nil {
				return
			}
			if sent.Load() > 64<<20 {
				// Far past the limit: if the validator were still reading, it
				// would keep going, and the assertion below catches it.
				return
			}
		}
	}))
	defer ts.Close()

	v := NewValidator(5*time.Second, []string{"/"}, logging.New("text", "error"))

	done := make(chan bool, 1)
	go func() {
		ok, _ := v.Validate(context.Background(), addrOf(t, ts))
		done <- ok
	}()

	select {
	case ok := <-done:
		if ok {
			t.Error("a body of nothing but prose was accepted as an exposition format")
		}
		got := sent.Load()
		t.Logf("the server managed to write %d bytes before the validator hung up", got)
		if got > 8*maxValidateBytes {
			t.Errorf("the validator consumed %d bytes of an endless body, want it to stop "+
				"near maxValidateBytes (%d)", got, maxValidateBytes)
		}
		// And it did read something: a limit that rejected on the first byte
		// would pass the assertion above while breaking every real exporter.
		if got == 0 {
			t.Error("the validator read nothing at all, so this proves nothing about the limit")
		}
	case <-time.After(20 * time.Second):
		t.Fatal("Validate never returned against a host that streams without end; " +
			"one such host in the scanned range would stall a worker for good")
	}
}

// TestValidateAcceptsAMetricAfterALongPreamble is the other half of the same
// comment: the limit is "intentionally generous ... so a valid endpoint is never
// rejected merely because its first metric line sits deep in the body".
//
// Without this, lowering the limit to make the test above cheaper would silently
// start dropping real exporters — the failure nobody notices, because the target
// simply never appears.
func TestValidateAcceptsAMetricAfterALongPreamble(t *testing.T) {
	// A preamble of HELP/TYPE comments large enough to bury the first sample,
	// which is what an exporter with thousands of documented metrics looks like.
	var b strings.Builder
	for i := 0; b.Len() < 256<<10; i++ {
		b.WriteString("# HELP some_metric_" + strconv.Itoa(i) + " a documented metric\n")
		b.WriteString("# TYPE some_metric_" + strconv.Itoa(i) + " gauge\n")
	}
	b.WriteString("go_info{version=\"go1.25\"} 1\n")
	body := b.String()

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(body))
	}))
	defer ts.Close()

	v := NewValidator(5*time.Second, []string{"/"}, logging.New("text", "error"))

	ok, _ := v.Validate(context.Background(), addrOf(t, ts))
	if !ok {
		t.Errorf("an exporter whose first sample sits %d bytes in was rejected; "+
			"maxValidateBytes is %d", len(body)-len("go_info{version=\"go1.25\"} 1\n"), maxValidateBytes)
	}
}

// TestValidateGivesUpOnASilentHost: the scanner finds an open port, so something
// accepted the connection — but accepting is not answering. A host that holds the
// connection open without replying occupies a validation worker, and the pool is
// bounded, so enough of them stop the cycle from finishing at all.
func TestValidateGivesUpOnASilentHost(t *testing.T) {
	ln, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer func() { _ = ln.Close() }()

	release := make(chan struct{})
	defer close(release)

	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			go func() {
				defer func() { _ = conn.Close() }()
				<-release
			}()
		}
	}()

	v := NewValidator(500*time.Millisecond, []string{"/"}, logging.New("text", "error"))

	done := make(chan struct{})
	start := time.Now()
	go func() {
		defer close(done)
		_, _ = v.Validate(context.Background(), ln.Addr().String())
	}()

	select {
	case <-done:
		// One attempt per configured path, so the budget scales with the paths.
		if took := time.Since(start); took > 5*time.Second {
			t.Errorf("Validate took %v against a silent host, want it bounded by the timeout", took)
		}
	case <-time.After(15 * time.Second):
		t.Fatal("Validate never returned against a host that accepts and stays silent; " +
			"enough of those in the range and no discovery cycle ever completes")
	}
}
