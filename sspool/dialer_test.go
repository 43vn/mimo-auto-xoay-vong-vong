package sspool

import (
	"context"
	"testing"
)

func TestNewSSDialer(t *testing.T) {
	cfg := SSConfig{
		Server:   "127.0.0.1",
		Port:     8388,
		Password: "test-password",
		Method:   "aes-256-gcm",
	}

	dialer, err := NewSSDialer(cfg)
	if err != nil {
		t.Fatalf("NewSSDialer failed: %v", err)
	}
	if dialer == nil {
		t.Fatal("expected non-nil dialer")
	}
	if dialer.serverAddr != "127.0.0.1:8388" {
		t.Fatalf("expected serverAddr 127.0.0.1:8388, got %s", dialer.serverAddr)
	}
}

func TestNewSSDialerUnsupportedCipher(t *testing.T) {
	cfg := SSConfig{
		Server:   "127.0.0.1",
		Port:     8388,
		Password: "test-password",
		Method:   "unsupported-cipher",
	}

	_, err := NewSSDialer(cfg)
	if err == nil {
		t.Fatal("expected error for unsupported cipher")
	}
}

func TestSSDialerDialContextNoServer(t *testing.T) {
	cfg := SSConfig{
		Server:   "127.0.0.1",
		Port:     8388,
		Password: "test-password",
		Method:   "aes-256-gcm",
	}

	dialer, err := NewSSDialer(cfg)
	if err != nil {
		t.Fatalf("NewSSDialer failed: %v", err)
	}

	// Dial should fail because there's no SS server running.
	// The error must be a connection failure, not a setup failure.
	conn, err := dialer.DialContext(context.Background(), "tcp", "example.com:443")
	if err == nil {
		conn.Close()
		t.Fatal("expected connection error (no SS server running)")
	}
	if conn != nil {
		t.Fatal("expected nil conn on error")
	}
}

func TestSSPoolGetByAddr(t *testing.T) {
	pool := NewSSPool()
	pool.Add(SSConfig{Server: "1.2.3.4", Port: 8388, Password: "pass1", Method: "aes-256-gcm"})
	pool.Add(SSConfig{Server: "5.6.7.8", Port: 1080, Password: "pass2", Method: "aes-128-gcm"})

	// Find first
	cfg := pool.GetByAddr("1.2.3.4:8388")
	if cfg == nil {
		t.Fatal("expected to find config for 1.2.3.4:8388")
	}
	if cfg.Server != "1.2.3.4" || cfg.Port != 8388 {
		t.Fatalf("wrong config: got %s:%d", cfg.Server, cfg.Port)
	}

	// Find second
	cfg = pool.GetByAddr("5.6.7.8:1080")
	if cfg == nil {
		t.Fatal("expected to find config for 5.6.7.8:1080")
	}
	if cfg.Server != "5.6.7.8" || cfg.Port != 1080 {
		t.Fatalf("wrong config: got %s:%d", cfg.Server, cfg.Port)
	}

	// Not found
	cfg = pool.GetByAddr("9.9.9.9:9999")
	if cfg != nil {
		t.Fatal("expected nil for non-existent address")
	}

	// Invalid addr (no port)
	cfg = pool.GetByAddr("invalid")
	if cfg != nil {
		t.Fatal("expected nil for invalid address")
	}
}

func TestSSPoolGetByAddrEmpty(t *testing.T) {
	pool := NewSSPool()

	cfg := pool.GetByAddr("1.2.3.4:8388")
	if cfg != nil {
		t.Fatal("expected nil from empty pool")
	}
}
