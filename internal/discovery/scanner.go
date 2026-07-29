// Package discovery provides network scanning functionality.
package discovery

import (
	"context"
	"net"
	"strconv"
	"sync"

	"bodsch.me/promscout/internal/config"
	"bodsch.me/promscout/internal/logging"
)

// Scanner scans a CIDR network for open TCP ports using a bounded worker
// pool and streams every discovered address as soon as it is found.
type Scanner struct {
	cfg    config.Config
	logger *logging.Logger
}

// NewScanner creates a new network scanner.
func NewScanner(cfg config.Config, logger *logging.Logger) *Scanner {
	return &Scanner{
		cfg:    cfg,
		logger: logger,
	}
}

// Scan probes every IP in the configured CIDR for every configured port
// and streams the addresses of open ports on the returned channel.
//
// The scan runs asynchronously: the channel is returned immediately and
// closed once every combination has been probed or ctx is cancelled.
// Returning a stream (rather than a slice) lets a consumer validate open
// ports while the scan is still running, forming a two-stage pipeline.
//
// Parameters:
//   - ctx: cancellation context; cancelling it stops the scan early and
//     causes the channel to be closed.
//
// Returns a receive-only channel of "host:port" strings for open ports.
// The channel is always closed exactly once, including on CIDR errors.
func (s *Scanner) Scan(ctx context.Context) <-chan string {
	workers := s.cfg.MaxWorkers
	if workers < 1 {
		workers = 1
	}

	out := make(chan string, workers)

	ip, ipnet, err := net.ParseCIDR(s.cfg.NetworkCIDR)
	if err != nil {
		s.logger.Error("invalid cidr", "error", err)
		close(out)
		return out
	}

	dialTimeout := s.cfg.DialTimeout
	if dialTimeout <= 0 {
		dialTimeout = s.cfg.Timeout
	}

	s.logger.Debug("starting network scan",
		"cidr", s.cfg.NetworkCIDR,
		"ports", s.cfg.Ports,
		"workers", workers,
		"dial_timeout", dialTimeout,
	)

	jobs := make(chan string, workers)

	// Producer: enumerate every host:port combination and feed the pool.
	// A cancelled context aborts enumeration so the workers can drain.
	go func() {
		defer close(jobs)
		for cur := ip.Mask(ipnet.Mask); ipnet.Contains(cur); inc(cur) {
			host := cur.String()
			for _, port := range s.cfg.Ports {
				addr := net.JoinHostPort(host, strconv.Itoa(port))
				select {
				case jobs <- addr:
				case <-ctx.Done():
					return
				}
			}
		}
	}()

	// Dial workers: probe each address with a short, dedicated timeout.
	var wg sync.WaitGroup
	dialer := &net.Dialer{}

	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for addr := range jobs {
				dctx, cancel := context.WithTimeout(ctx, dialTimeout)
				conn, err := dialer.DialContext(dctx, "tcp", addr)
				cancel()
				if err != nil {
					continue
				}
				_ = conn.Close()

				s.logger.Debug("open port discovered", "target", addr)

				select {
				case out <- addr:
				case <-ctx.Done():
					return
				}
			}
		}()
	}

	// Closer: close the output stream once all workers have finished.
	go func() {
		wg.Wait()
		close(out)
		s.logger.Debug("network scan completed")
	}()

	return out
}

// inc increments an IP address in place, carrying across octets.
func inc(ip net.IP) {
	for j := len(ip) - 1; j >= 0; j-- {
		ip[j]++
		if ip[j] > 0 {
			break
		}
	}
}
