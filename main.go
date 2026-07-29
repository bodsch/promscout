package main

import (
	"context"
	"flag"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"bodsch.me/promscout/internal/config"
	"bodsch.me/promscout/internal/discovery"
	"bodsch.me/promscout/internal/exporter"
	"bodsch.me/promscout/internal/logging"
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

	go func() {
		<-ctx.Done()
		logger.Info("shutting down http server")

		// Bound the graceful shutdown so a stuck connection cannot block
		// process termination indefinitely.
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()

		if err := server.Shutdown(shutdownCtx); err != nil {
			logger.Error("graceful shutdown failed", "error", err)
		}
	}()

	go scheduler.Start(ctx)

	logger.Info("service starting",
		"cidr", cfg.NetworkCIDR,
		"interval", cfg.Interval,
	)

	if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		logger.Error("http server error", "error", err)
		os.Exit(1)
	}
}

func printVersion() {
	fmt.Println("PromScout")
	fmt.Printf("Version:   %s\n", version.Version)
	fmt.Printf("Commit:    %s\n", version.GitCommit)
	fmt.Printf("BuildDate: %s\n", version.BuildDate)
}
