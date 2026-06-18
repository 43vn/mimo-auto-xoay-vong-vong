package proxy

import (
	"context"
	"fmt"
	"net"
	"sync"
	"testing"
	"time"

	"github.com/vincent/mimo-xoay/sspool"
)

// mockDialer simulates latency by sleeping for a configurable duration.
type mockDialer struct {
	latency   time.Duration
	mu        sync.Mutex
	callCount int
}

func (d *mockDialer) DialContext(ctx context.Context, network, addr string) (net.Conn, error) {
	d.mu.Lock()
	d.callCount++
	d.mu.Unlock()
	select {
	case <-time.After(d.latency):
		// Return a mock connection that does nothing
		return &mockConn{}, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

// mockConn implements net.Conn with no-op methods.
type mockConn struct{}

func (c *mockConn) Read(b []byte) (n int, err error)  { return 0, net.ErrClosed }
func (c *mockConn) Write(b []byte) (n int, err error) { return 0, net.ErrClosed }
func (c *mockConn) Close() error                      { return nil }
func (c *mockConn) LocalAddr() net.Addr               { return &net.TCPAddr{} }
func (c *mockConn) RemoteAddr() net.Addr              { return &net.TCPAddr{} }
func (c *mockConn) SetDeadline(t time.Time) error      { return nil }
func (c *mockConn) SetReadDeadline(t time.Time) error  { return nil }
func (c *mockConn) SetWriteDeadline(t time.Time) error { return nil }

func TestSmartRouterSelectBest(t *testing.T) {
	// Create three proxies with different latencies
	proxy1 := &ProxyInfo{
		Address:  "1.1.1.1:443",
		Protocol: "ss",
		Dialer:   &mockDialer{latency: 100 * time.Millisecond},
		Alive:    true,
	}
	proxy2 := &ProxyInfo{
		Address:  "2.2.2.2:443",
		Protocol: "ss",
		Dialer:   &mockDialer{latency: 50 * time.Millisecond},
		Alive:    true,
	}
	proxy3 := &ProxyInfo{
		Address:  "3.3.3.3:443",
		Protocol: "socks5",
		Dialer:   &mockDialer{latency: 200 * time.Millisecond},
		Alive:    true,
	}

	// Set cached latencies
	proxy1.Latency = 100 * time.Millisecond
	proxy2.Latency = 50 * time.Millisecond
	proxy3.Latency = 200 * time.Millisecond

	router := NewSmartRouter([]*ProxyInfo{proxy1, proxy2, proxy3})

	// SelectBest should return the proxy with lowest latency (proxy2)
	best := router.SelectBest()
	if best == nil {
		t.Fatal("SelectBest returned nil, expected proxy2")
	}
	if best.Address != proxy2.Address {
		t.Errorf("SelectBest returned %s, expected %s", best.Address, proxy2.Address)
	}
}

func TestSmartRouterFallback(t *testing.T) {
	// All proxies dead
	proxy1 := &ProxyInfo{
		Address:  "1.1.1.1:443",
		Protocol: "ss",
		Dialer:   &mockDialer{latency: 100 * time.Millisecond},
		Alive:    false,
	}
	proxy2 := &ProxyInfo{
		Address:  "2.2.2.2:443",
		Protocol: "ss",
		Dialer:   &mockDialer{latency: 50 * time.Millisecond},
		Alive:    false,
	}

	router := NewSmartRouter([]*ProxyInfo{proxy1, proxy2})

	// SelectBest should return nil (direct connection)
	best := router.SelectBest()
	if best != nil {
		t.Errorf("SelectBest returned %s, expected nil", best.Address)
	}
}

func TestSmartRouterMeasurement(t *testing.T) {
	proxy := &ProxyInfo{
		Address:  "1.1.1.1:443",
		Protocol: "ss",
		Dialer:   &mockDialer{latency: 30 * time.Millisecond},
		Alive:    false,
	}

	router := NewSmartRouter([]*ProxyInfo{proxy})

	// Measure latency for the proxy
	router.MeasureAll(1 * time.Second)

	// After measurement, proxy should be alive and have latency recorded
	if !proxy.Alive {
		t.Error("Proxy should be alive after successful measurement")
	}
	if proxy.Latency <= 0 {
		t.Errorf("Proxy latency should be > 0, got %v", proxy.Latency)
	}
	if proxy.LastCheck.IsZero() {
		t.Error("Proxy LastCheck should be set after measurement")
	}
}

func TestSmartRouterConcurrent(t *testing.T) {
	proxies := make([]*ProxyInfo, 10)
	for i := range proxies {
		proxies[i] = &ProxyInfo{
			Address:  fmt.Sprintf("%d.%d.%d.%d:443", i, i, i, i),
			Protocol: "ss",
			Dialer:   &mockDialer{latency: time.Duration(i*10) * time.Millisecond},
			Alive:    true,
			Latency:  time.Duration(i*10) * time.Millisecond,
		}
	}

	router := NewSmartRouter(proxies)

	// Run concurrent SelectBest calls
	var wg sync.WaitGroup
	for i := 0; i < 100; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			best := router.SelectBest()
			if best == nil {
				t.Error("SelectBest returned nil")
			}
		}()
	}
	wg.Wait()
}

func TestSmartRouterEmpty(t *testing.T) {
	router := NewSmartRouter([]*ProxyInfo{})

	best := router.SelectBest()
	if best != nil {
		t.Errorf("SelectBest returned %s, expected nil", best.Address)
	}

	next := router.SelectNext()
	if next != nil {
		t.Errorf("SelectNext returned %s, expected nil", next.Address)
	}

	if router.Len() != 0 {
		t.Errorf("Len returned %d, expected 0", router.Len())
	}
}

// Ensure sspool is imported (used in other proxy files)
var _ sspool.ProxyDialer = (*mockDialer)(nil)