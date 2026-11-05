package main

import (
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"testing"
	"time"

	"bodsch.me/promscout/internal/logging"
)

// startServe runs serve on a loopback listener with handler h and returns the
// base URL, the cancel func standing in for SIGTERM, and serve's result.
func startServe(t *testing.T, h http.Handler, grace time.Duration) (string, context.CancelFunc, <-chan error) {
	t.Helper()

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)

	done := make(chan error, 1)
	go func() {
		done <- serve(ctx, &http.Server{Handler: h, ReadHeaderTimeout: time.Second}, ln, grace, logging.New("text", "error"))
	}()
	return "http://" + ln.Addr().String(), cancel, done
}

// TestServeWaitsForInFlightRequests covers the README's "graceful shutdown".
//
// net/http documents that Shutdown makes ListenAndServe return immediately and
// that the program must wait for Shutdown itself. main returned as soon as
// ListenAndServe did, so the process exited while Shutdown was still draining:
// the 10 s grace period existed only on paper, and a Prometheus fetching the
// target list during a rolling restart got a connection reset instead of an
// answer.
func TestServeWaitsForInFlightRequests(t *testing.T) {
	entered := make(chan struct{})
	release := make(chan struct{})
	h := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		close(entered)
		<-release
		_, _ = io.WriteString(w, "complete")
	})

	url, cancel, done := startServe(t, h, 5*time.Second)

	type result struct {
		body string
		err  error
	}
	resp := make(chan result, 1)
	go func() {
		r, err := http.Get(url) //nolint:noctx // bounded by the test deadlines below
		if err != nil {
			resp <- result{err: err}
			return
		}
		defer func() { _ = r.Body.Close() }()
		b, err := io.ReadAll(r.Body)
		resp <- result{body: string(b), err: err}
	}()

	select {
	case <-entered:
	case <-time.After(3 * time.Second):
		t.Fatal("the request never reached the handler")
	}

	cancel() // SIGTERM

	select {
	case err := <-done:
		t.Fatalf("serve returned (%v) while a request was still in flight; the process would exit and cut it off", err)
	case <-time.After(200 * time.Millisecond):
	}

	close(release)

	select {
	case r := <-resp:
		if r.err != nil || r.body != "complete" {
			t.Errorf("in-flight request ended with (%q, %v), want the full response", r.body, r.err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("the in-flight request never completed")
	}

	select {
	case err := <-done:
		if err != nil {
			t.Errorf("serve() = %v after a clean shutdown, want nil", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("serve did not return after the last request finished")
	}
}

// TestServeGivesUpAfterTheGracePeriod: the grace period is a bound, not a
// wish. A connection that never finishes must not keep the process alive past
// it — an orchestrator would SIGKILL it instead, without the log line.
func TestServeGivesUpAfterTheGracePeriod(t *testing.T) {
	entered := make(chan struct{})
	block := make(chan struct{})
	t.Cleanup(func() { close(block) })
	h := http.HandlerFunc(func(_ http.ResponseWriter, _ *http.Request) {
		close(entered)
		<-block
	})

	url, cancel, done := startServe(t, h, 200*time.Millisecond)
	go func() {
		if r, err := http.Get(url); err == nil { //nolint:noctx // the handler never answers
			_ = r.Body.Close()
		}
	}()
	<-entered
	cancel()

	select {
	case err := <-done:
		if !errors.Is(err, context.DeadlineExceeded) {
			t.Errorf("serve() = %v, want a deadline error naming the expired grace period", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("serve ignored its grace period")
	}
}

// TestServeReportsAListenerFailure: when the server cannot serve at all, serve
// must return the error instead of waiting for a shutdown signal that will
// never matter.
func TestServeReportsAListenerFailure(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	_ = ln.Close() // Serve on a closed listener fails immediately

	done := make(chan error, 1)
	go func() {
		done <- serve(context.Background(), &http.Server{ReadHeaderTimeout: time.Second}, ln, time.Second, logging.New("text", "error"))
	}()

	select {
	case err := <-done:
		if err == nil {
			t.Error("serve() = nil for a listener that cannot accept")
		}
	case <-time.After(3 * time.Second):
		t.Fatal("serve hung on a failed listener")
	}
}
