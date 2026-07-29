package discovery

import (
	"context"
	"net"
	"strconv"
	"testing"
	"time"

	"bodsch.me/promscout/internal/config"
	"bodsch.me/promscout/internal/logging"
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

func newTestScanner(cfg config.Config) *Scanner {
	return NewScanner(cfg, logging.New("text", "error"))
}

func collect(ch <-chan string) []string {
	var out []string
	for addr := range ch {
		out = append(out, addr)
	}
	return out
}

func TestScanFindsOpenPort(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer func() { _ = ln.Close() }()

	_, portStr, err := net.SplitHostPort(ln.Addr().String())
	if err != nil {
		t.Fatalf("split host port: %v", err)
	}
	port, err := strconv.Atoi(portStr)
	if err != nil {
		t.Fatalf("atoi: %v", err)
	}

	// /32 restricts the scan to exactly the loopback address.
	s := newTestScanner(config.Config{
		NetworkCIDR: "127.0.0.1/32",
		Ports:       []int{port},
		DialTimeout: 500 * time.Millisecond,
		MaxWorkers:  4,
	})

	got := collect(s.Scan(context.Background()))

	want := net.JoinHostPort("127.0.0.1", portStr)
	if len(got) != 1 || got[0] != want {
		t.Fatalf("Scan() = %v, want [%s]", got, want)
	}
}

func TestScanNoOpenPort(t *testing.T) {
	// Bind and immediately close to obtain a port that refuses fast.
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	_, portStr, _ := net.SplitHostPort(ln.Addr().String())
	port, _ := strconv.Atoi(portStr)
	_ = ln.Close()

	s := newTestScanner(config.Config{
		NetworkCIDR: "127.0.0.1/32",
		Ports:       []int{port},
		DialTimeout: 500 * time.Millisecond,
		MaxWorkers:  4,
	})

	if got := collect(s.Scan(context.Background())); len(got) != 0 {
		t.Fatalf("Scan() = %v, want no results", got)
	}
}

func TestScanInvalidCIDRClosesChannel(t *testing.T) {
	s := newTestScanner(config.Config{
		NetworkCIDR: "not-a-cidr",
		Ports:       []int{9100},
		DialTimeout: 100 * time.Millisecond,
		MaxWorkers:  2,
	})

	// A closed channel must yield no values and not block.
	if got := collect(s.Scan(context.Background())); len(got) != 0 {
		t.Fatalf("Scan() with invalid CIDR = %v, want empty", got)
	}
}

func TestScanCancelledContext(t *testing.T) {
	s := newTestScanner(config.Config{
		NetworkCIDR: "10.0.0.0/16",
		Ports:       []int{9100, 9115},
		DialTimeout: 100 * time.Millisecond,
		MaxWorkers:  8,
	})

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // cancel before consuming

	// The scan must terminate (channel closes) despite the large CIDR.
	done := make(chan struct{})
	go func() {
		collect(s.Scan(ctx))
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("Scan() did not terminate on cancelled context")
	}
}
