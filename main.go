package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"bodsch.me/promscout/internal/config"
	"bodsch.me/promscout/internal/discovery"
	"bodsch.me/promscout/internal/exporter"
	"bodsch.me/promscout/internal/logging"
	"bodsch.me/promscout/internal/monitoring"
	"bodsch.me/promscout/pkg/version"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/collectors"
)

func main() {

	fs := flag.NewFlagSet(os.Args[0], flag.ExitOnError)

	showVersion := fs.Bool("version", false, "Print version information and exit")

	cfg, err := config.Load(fs, os.Args[1:])
	if err != nil {
		fmt.Fprintf(os.Stderr, "config error: %v\n", err)
		os.Exit(1)
	}

	if *showVersion {
		printVersion()
		return
	}

	logger := logging.New(cfg.LogFormat, cfg.LogLevel)

	// Use a dedicated registry instead of the global default so the
	// metrics wiring is self-contained. Go runtime and process metrics
	// are registered explicitly to preserve the previous /metrics output.
	registry := prometheus.NewRegistry()
	registry.MustRegister(
		collectors.NewGoCollector(),
		collectors.NewProcessCollector(collectors.ProcessCollectorOpts{}),
	)
	if err := monitoring.RegisterBuildInfo(registry); err != nil {
		logger.Error("registering build info failed", "error", err)
		os.Exit(1)
	}

	scanner := discovery.NewScanner(cfg, logger)
	validator := discovery.NewValidator(cfg.Timeout, cfg.MetricsPaths, logger)
	scheduler := discovery.NewScheduler(
		scanner,
		validator,
		cfg.Interval,
		cfg.ValidateWorkers,
		cfg.CacheFile,
		registry,
		logger,
	)

	exp := exporter.NewHTTPExporter(scheduler, registry, cfg.GuessJob)

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	server := &http.Server{
		Addr:    cfg.ListenAddress,
		Handler: exp,

		// Timeouts protect against slow-client (Slowloris) resource
		// exhaustion. ReadHeaderTimeout in particular bounds how long a
		// client may take to send request headers.
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       10 * time.Second,
		WriteTimeout:      30 * time.Second,
		IdleTimeout:       60 * time.Second,
	}

	ln, err := net.Listen("tcp", cfg.ListenAddress)
	if err != nil {
		logger.Error("http server error", "error", err)
		os.Exit(1)
	}

	go scheduler.Start(ctx)

	logger.Info("service starting",
		"cidr", cfg.NetworkCIDR,
		"interval", cfg.Interval,
	)

	if err := serve(ctx, server, ln, shutdownGrace, logger); err != nil {
		logger.Error("http server error", "error", err)
		os.Exit(1)
	}
}

// shutdownGrace bounds the graceful shutdown so a stuck connection cannot
// block process termination indefinitely.
const shutdownGrace = 10 * time.Second

// serve runs server on ln until ctx is cancelled, then shuts it down
// gracefully and returns only once in-flight requests have finished.
//
// Waiting matters: Shutdown makes Serve return immediately, so a caller that
// exits when Serve returns cuts off every request still being answered.
//
// Parameters:
//   - ctx: cancelled on SIGINT/SIGTERM to start the shutdown.
//   - server: the configured HTTP server.
//   - ln: the listener to serve on; serve takes ownership of it.
//   - grace: upper bound for draining in-flight requests.
//   - logger: structured logger.
//
// Returns nil after a clean shutdown, the Serve error if serving failed, or a
// wrapped context.DeadlineExceeded if requests outlived the grace period.
func serve(ctx context.Context, server *http.Server, ln net.Listener, grace time.Duration, logger *logging.Logger) error {
	serveErr := make(chan error, 1)
	go func() { serveErr <- server.Serve(ln) }()

	select {
	case err := <-serveErr:
		return err
	case <-ctx.Done():
	}

	logger.Info("shutting down http server")

	shutdownCtx, cancel := context.WithTimeout(context.Background(), grace)
	defer cancel()

	if err := server.Shutdown(shutdownCtx); err != nil {
		return fmt.Errorf("graceful shutdown: %w", err)
	}
	if err := <-serveErr; !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	return nil
}

func printVersion() {
	fmt.Println("PromScout")
	fmt.Printf("Version:   %s\n", version.Version)
	fmt.Printf("Commit:    %s\n", version.GitCommit)
	fmt.Printf("BuildDate: %s\n", version.BuildDate)
}
