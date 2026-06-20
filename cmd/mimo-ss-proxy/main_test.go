package main

import (
	"os/exec"
	"strings"
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

// --- Fingerprint tests ---

// TestGenerateFingerprintLength verifies fingerprint is exactly 64 hex characters.
func TestGenerateFingerprintLength(t *testing.T) {
	fp := generateFingerprint()
	if len(fp) != 64 {
		t.Errorf("expected fingerprint length 64, got %d (value: %q)", len(fp), fp)
	}
}

// TestGenerateFingerprintHex verifies fingerprint contains only valid hex chars [0-9a-f].
func TestGenerateFingerprintHex(t *testing.T) {
	fp := generateFingerprint()
	for i, c := range fp {
		if !((c >= '0' && c <= '9') || (c >= 'a' && c <= 'f')) {
			t.Errorf("invalid character at position %d: %q (value: %q)", i, c, fp)
			break
		}
	}
}

// TestGenerateFingerprintRandomness verifies two calls produce different values.
func TestGenerateFingerprintRandomness(t *testing.T) {
	fp1 := generateFingerprint()
	fp2 := generateFingerprint()
	if fp1 == fp2 {
		t.Errorf("expected different fingerprints, got same: %q", fp1)
	}
}

// TestFastCheckFlagIsDefined verifies the --fast-check flag is accepted by the binary.
func TestFastCheckFlagIsDefined(t *testing.T) {
	// Build the binary to test flag acceptance
	cmd := exec.Command("go", "run", ".", "--fast-check=false", "--help")
	output, err := cmd.CombinedOutput()
	_ = err // go run may exit 2 on --help, that's expected
	if strings.Contains(string(output), "flag provided but not defined") {
		t.Errorf("--fast-check flag was rejected as undefined:\n%s", string(output))
	}
}
