// Package discovery defines shared discovery types.
package discovery

// Target represents a discovered Prometheus scrape target.
type Target struct {
	Address string
	Path    string
}
