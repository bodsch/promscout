// Package api exposes the public Service API for the Prometheus
// network-based service discovery tool.
package api

import (
	"context"
	"net/http"

	"bodsch.me/promscout/internal/config"
	"bodsch.me/promscout/internal/discovery"
	"bodsch.me/promscout/internal/exporter"
	"bodsch.me/promscout/internal/logging"
)

// Service represents the main application service.
type Service struct {
	cfg       config.Config
	logger    *logging.Logger
	scheduler *discovery.Scheduler
	exporter  *exporter.HTTPExporter
}

// NewService creates a fully configured service instance.
func NewService(cfg config.Config) (*Service, error) {
	logger := logging.New(cfg.LogFormat, cfg.LogLevel)

	scanner := discovery.NewScanner(cfg, logger)
	validator := discovery.NewValidator(cfg.Timeout, cfg.MetricsPaths, logger)
	scheduler := discovery.NewScheduler(scanner, validator, cfg.Interval, logger)

	exp := exporter.NewHTTPExporter(cfg.ListenAddress, scheduler)

	return &Service{
		cfg:       cfg,
		logger:    logger,
		scheduler: scheduler,
		exporter:  exp,
	}, nil
}

// Start launches scheduler and HTTP server.
func (s *Service) Start(ctx context.Context) error {
	s.logger.Info("service starting",
		"cidr", s.cfg.NetworkCIDR,
		"interval", s.cfg.Interval,
	)

	go s.scheduler.Start(ctx)

	server := &http.Server{
		Addr:    s.cfg.ListenAddress,
		Handler: s.exporter,
	}

	go func() {
		<-ctx.Done()
		_ = server.Shutdown(context.Background())
	}()

	return server.ListenAndServe()
}
