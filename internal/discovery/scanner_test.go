package discovery

import (
	"net"
	"testing"
)

func TestInc(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want string
	}{
		{"simple increment", "192.168.1.1", "192.168.1.2"},
		{"last-octet rollover", "192.168.1.255", "192.168.2.0"},
		{"multi-octet rollover", "192.168.255.255", "192.169.0.0"},
		{"full rollover wraps to zero", "255.255.255.255", "0.0.0.0"},
		{"zero start", "0.0.0.0", "0.0.0.1"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ip := net.ParseIP(tt.in).To4()
			if ip == nil {
				t.Fatalf("failed to parse %q as IPv4", tt.in)
			}
			inc(ip)
			if got := ip.String(); got != tt.want {
				t.Errorf("inc(%s) = %s, want %s", tt.in, got, tt.want)
			}
		})
	}
}
