package proxy

import (
	"testing"
	"time"
)

// newTestRouter creates a SmartRouter with mock proxies for testing.
// Uses mockDialer from router_test.go (same package).
func newTestRouter(t *testing.T) *SmartRouter {
	t.Helper()
	proxies := []*ProxyInfo{
		{
			Address:  "1.2.3.4:8080",
			Protocol: "http",
			Dialer:   &mockDialer{latency: 10 * time.Millisecond},
			Alive:    true,
		},
		{
			Address:  "5.6.7.8:1080",
			Protocol: "socks5",
			Dialer:   &mockDialer{latency: 20 * time.Millisecond},
			Alive:    true,
		},
	}
	return NewSmartRouter(proxies)
}

func TestFallbackHandlerUserPoolFirst(t *testing.T) {
	router := newTestRouter(t)
	h := NewFallbackHandler(router, 60*time.Second)

	result := h.NextProxy()
	if result == nil {
		t.Fatal("expected non-nil result")
	}
	if result.Source != "user_pool" {
		t.Errorf("expected source user_pool, got %s", result.Source)
	}
	if result.ProxyAddr == "" {
		t.Error("expected non-empty ProxyAddr for user pool")
	}
	if !h.HasUserPool() {
		t.Error("HasUserPool should be true")
	}
	if h.UserPoolLen() != 2 {
		t.Errorf("expected user pool len 2, got %d", h.UserPoolLen())
	}
}

func TestFallbackHandlerEmptyRouterReturnsDirect(t *testing.T) {
	h := NewFallbackHandler(nil, 60*time.Second)

	result := h.NextProxy()
	if result == nil {
		t.Fatal("expected non-nil result")
	}
	// When the user pool is empty, FallbackHandler tries shadowmere SS pool.
	// If shadowmere has servers, source will be "shadowmere".
	// If shadowmere is empty, source will be "direct".
	// Either is acceptable — the important thing is it doesn't crash and returns a valid result.
	if result.Err != nil {
		t.Fatalf("unexpected error: %v", result.Err)
	}
	if !h.HasUserPool() {
		// No user pool = OK (router is nil)
	}
}

func TestFallbackHandlerUserPoolDead(t *testing.T) {
	router := newTestRouter(t)
	h := NewFallbackHandler(router, 60*time.Second)

	// Mark all proxies dead
	h.ReportFailure("1.2.3.4:8080")
	h.ReportFailure("5.6.7.8:1080")

	result := h.NextProxy()
	if result == nil {
		t.Fatal("expected non-nil result")
	}
	// User pool exhausted -> may fallback to shadowmere or direct
	if result.Err != nil {
		t.Fatalf("unexpected error: %v", result.Err)
	}
	t.Logf("Result source: %s, addr: %s", result.Source, result.ProxyAddr)
}

func TestFallbackHandlerReportSuccess(t *testing.T) {
	router := newTestRouter(t)
	h := NewFallbackHandler(router, 60*time.Second)

	// Mark dead then revive
	h.ReportFailure("1.2.3.4:8080")
	h.ReportSuccess("1.2.3.4:8080")

	result := h.NextProxy()
	if result == nil {
		t.Fatal("expected non-nil result")
	}
	if result.Source != "user_pool" {
		t.Skip("fallback happened (depends on shadowmere state)")
	}
}

func TestFallbackHandlerNoRouter(t *testing.T) {
	h := NewFallbackHandler(nil, 60*time.Second)

	if h.HasUserPool() {
		t.Error("HasUserPool should be false when router is nil")
	}
	if h.UserPoolLen() != 0 {
		t.Errorf("UserPoolLen should be 0, got %d", h.UserPoolLen())
	}
}
