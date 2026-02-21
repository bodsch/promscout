// Package discovery provides network scanning functionality.
package discovery

import (
	"fmt"
	"net"
	"sync"

	"bodsch.me/promscout/internal/config"
	"bodsch.me/promscout/internal/logging"
)

// Scanner scans a CIDR network for open ports using
// a bounded worker pool.
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

// Scan scans all IPs in the CIDR for configured ports in parallel.
func (s *Scanner) Scan() []string {
	s.logger.Debug("starting network scan",
		"cidr", s.cfg.NetworkCIDR,
		"ports", s.cfg.Ports,
	)

	ip, ipnet, err := net.ParseCIDR(s.cfg.NetworkCIDR)
	if err != nil {
		s.logger.Error("invalid cidr", "error", err)
		return nil
	}

	var targets []string
	var mu sync.Mutex
	var wg sync.WaitGroup

	jobs := make(chan string)

	for i := 0; i < s.cfg.MaxWorkers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for addr := range jobs {
				conn, err := net.DialTimeout("tcp", addr, s.cfg.Timeout)
				if err == nil {
					conn.Close()

					s.logger.Debug("open port discovered",
						"target", addr,
					)

					mu.Lock()
					targets = append(targets, addr)
					mu.Unlock()
				}
			}
		}()
	}

	for ip := ip.Mask(ipnet.Mask); ipnet.Contains(ip); inc(ip) {
		host := ip.String()
		for _, port := range s.cfg.Ports {
			jobs <- net.JoinHostPort(host, fmt.Sprintf("%d", port))
		}
	}

	close(jobs)
	wg.Wait()

	s.logger.Debug("network scan completed",
		"discovered_targets", len(targets),
	)

	return targets
}

// inc increments an IP address.
func inc(ip net.IP) {
	for j := len(ip) - 1; j >= 0; j-- {
		ip[j]++
		if ip[j] > 0 {
			break
		}
	}
}
