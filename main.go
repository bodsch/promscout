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

	scanner := discovery.NewScanner(cfg, logger)
	validator := discovery.NewValidator(cfg.Timeout, cfg.MetricsPaths, logger)
	scheduler := discovery.NewScheduler(scanner, validator, cfg.Interval, logger)

	exp := exporter.NewHTTPExporter(scheduler)

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
		_ = server.Shutdown(context.Background())
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
