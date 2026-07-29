// Package discovery defines shared discovery types.
package discovery

// Target represents a discovered Prometheus scrape target.
//
// Exporter/Version/Source carry the service metadata derived from the
// target's exposition body (see Validator). They are optional: a target
// whose service could not be identified simply leaves them empty.
type Target struct {
	Address  string `json:"address"`
	Path     string `json:"path"`
	Exporter string `json:"exporter,omitempty"`
	Version  string `json:"version,omitempty"`
	Source   string `json:"source,omitempty"`
}
