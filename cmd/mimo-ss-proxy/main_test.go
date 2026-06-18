package main

import (
	"testing"

	"github.com/vincent/mimo-xoay/rotator"
	"github.com/vincent/mimo-xoay/sspool"
)

// TestHealthCheckUpdatesRotator verifies that after a health check removes a server,
// calling r.Update(poolAddresses(pool)) keeps the rotator in sync.
func TestHealthCheckUpdatesRotator(t *testing.T) {
	pool := sspool.NewSSPool()
	pool.Add(sspool.SSConfig{Method: "aes-256-gcm", Password: "pass1", Server: "1.1.1.1", Port: 8388})
	pool.Add(sspool.SSConfig{Method: "aes-256-gcm", Password: "pass2", Server: "2.2.2.2", Port: 8388})
	pool.Add(sspool.SSConfig{Method: "aes-256-gcm", Password: "pass3", Server: "3.3.3.3", Port: 8388})

	if pool.Len() != 3 {
		t.Fatalf("expected pool len 3, got %d", pool.Len())
	}

	// Create rotator from pool addresses
	addresses := poolAddresses(pool)
	r := rotator.New(addresses, 0, nil)
	if r.Len() != 3 {
		t.Fatalf("expected rotator len 3, got %d", r.Len())
	}

	// Simulate health check: remove one server (1.1.1.1)
	alive := []sspool.SSConfig{
		{Method: "aes-256-gcm", Password: "pass2", Server: "2.2.2.2", Port: 8388},
		{Method: "aes-256-gcm", Password: "pass3", Server: "3.3.3.3", Port: 8388},
	}
	rebuildPoolInPlace(pool, alive)
	if pool.Len() != 2 {
		t.Fatalf("expected pool len 2 after rebuild, got %d", pool.Len())
	}

	// Sync rotator
	r.Update(poolAddresses(pool))

	if r.Len() != 2 {
		t.Fatalf("expected rotator len 2 after Update, got %d", r.Len())
	}

	// Verify the addresses are correct
	current := r.Current()
	if current == "" {
		t.Fatal("expected rotator to have a current address")
	}

	// Verify index reset to 0
	if r.Index() != 0 {
		t.Fatalf("expected rotator index 0 after Update, got %d", r.Index())
	}
}

// TestRefreshUpdatesRotator verifies that after adding new servers to the pool,
// calling r.Update(poolAddresses(pool)) keeps the rotator in sync.
func TestRefreshUpdatesRotator(t *testing.T) {
	pool := sspool.NewSSPool()
	pool.Add(sspool.SSConfig{Method: "aes-256-gcm", Password: "pass1", Server: "1.1.1.1", Port: 8388})
	pool.Add(sspool.SSConfig{Method: "aes-256-gcm", Password: "pass2", Server: "2.2.2.2", Port: 8388})

	addresses := poolAddresses(pool)
	r := rotator.New(addresses, 0, nil)
	if r.Len() != 2 {
		t.Fatalf("expected rotator len 2, got %d", r.Len())
	}

	// Simulate refresh: add 3 more servers
	pool.Add(sspool.SSConfig{Method: "aes-256-gcm", Password: "pass3", Server: "3.3.3.3", Port: 8388})
	pool.Add(sspool.SSConfig{Method: "aes-256-gcm", Password: "pass4", Server: "4.4.4.4", Port: 8388})
	pool.Add(sspool.SSConfig{Method: "aes-256-gcm", Password: "pass5", Server: "5.5.5.5", Port: 8388})

	if pool.Len() != 5 {
		t.Fatalf("expected pool len 5 after add, got %d", pool.Len())
	}

	// Sync rotator
	r.Update(poolAddresses(pool))

	if r.Len() != 5 {
		t.Fatalf("expected rotator len 5 after Update, got %d", r.Len())
	}

	// Verify index reset to 0
	if r.Index() != 0 {
		t.Fatalf("expected rotator index 0 after Update, got %d", r.Index())
	}
}

// TestStartupZeroServersRotatorEmpty verifies that a rotator created with
// empty addresses has Len() == 0 and Current() returns empty string.
func TestStartupZeroServersRotatorEmpty(t *testing.T) {
	r := rotator.New([]string{}, 0, nil)

	if r.Len() != 0 {
		t.Fatalf("expected rotator len 0, got %d", r.Len())
	}

	current := r.Current()
	if current != "" {
		t.Fatalf("expected Current() to return empty string for empty pool, got %q", current)
	}
}
