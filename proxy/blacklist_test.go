package proxy

import (
	"os"
	"path/filepath"
	"testing"
)

func TestBlacklistManager_Add_And_IsBlacklisted(t *testing.T) {
	dir := t.TempDir()
	bm := NewBlacklistManager(dir)
	defer bm.Stop()

	// Add by host:port
	bm.Add("1.2.3.4:5600")
	if !bm.IsBlacklisted("1.2.3.4:5600") {
		t.Error("expected blacklisted")
	}
	// Same IP, different port — should also be blacklisted
	if !bm.IsBlacklisted("1.2.3.4:8080") {
		t.Error("expected same IP different port to be blacklisted")
	}
	// Different IP — should not be blacklisted
	if bm.IsBlacklisted("5.6.7.8:5600") {
		t.Error("expected different IP to NOT be blacklisted")
	}
}

func TestBlacklistManager_Remove(t *testing.T) {
	dir := t.TempDir()
	bm := NewBlacklistManager(dir)
	defer bm.Stop()

	bm.Add("1.2.3.4:5600")
	if !bm.IsBlacklisted("1.2.3.4:5600") {
		t.Error("expected blacklisted")
	}

	bm.Remove("1.2.3.4:9999") // same IP, different port
	if bm.IsBlacklisted("1.2.3.4:5600") {
		t.Error("expected removed")
	}
}

func TestBlacklistManager_Save_And_Load(t *testing.T) {
	dir := t.TempDir()

	// Create and save
	bm1 := NewBlacklistManager(dir)
	bm1.Add("10.0.0.1:5600")
	bm1.Add("10.0.0.2:5600")
	if err := bm1.Save(); err != nil {
		t.Fatalf("save: %v", err)
	}
	bm1.Stop()

	// Load from file
	bm2 := NewBlacklistManager(dir)
	defer bm2.Stop()

	if !bm2.IsBlacklisted("10.0.0.1:5600") {
		t.Error("expected 10.0.0.1 to be blacklisted after load")
	}
	if !bm2.IsBlacklisted("10.0.0.2:5600") {
		t.Error("expected 10.0.0.2 to be blacklisted after load")
	}
	if bm2.IsBlacklisted("10.0.0.3:5600") {
		t.Error("expected 10.0.0.3 to NOT be blacklisted")
	}
}

func TestBlacklistManager_BackwardCompatibility(t *testing.T) {
	dir := t.TempDir()

	// Simulate old config file with expires field
	oldJSON := `[{"ip":"1.2.3.4","expires":"2020-01-01T00:00:00Z"},{"ip":"5.6.7.8","expires":"2099-01-01T00:00:00Z"}]`
	os.WriteFile(filepath.Join(dir, BlacklistFilename), []byte(oldJSON), 0644)

	bm := NewBlacklistManager(dir)
	defer bm.Stop()

	// Both should be loaded as permanent (expires ignored)
	if !bm.IsBlacklisted("1.2.3.4:5600") {
		t.Error("expected old entry to be blacklisted (permanent)")
	}
	if !bm.IsBlacklisted("5.6.7.8:5600") {
		t.Error("expected old entry to be blacklisted (permanent)")
	}
}

func TestBlacklistManager_Len(t *testing.T) {
	dir := t.TempDir()
	bm := NewBlacklistManager(dir)
	defer bm.Stop()

	bm.Add("1.2.3.4:5600")
	bm.Add("5.6.7.8:5600")
	if bm.Len() != 2 {
		t.Errorf("expected 2, got %d", bm.Len())
	}

	bm.Remove("1.2.3.4:5600")
	if bm.Len() != 1 {
		t.Errorf("expected 1, got %d", bm.Len())
	}
}

func TestExtractIP(t *testing.T) {
	tests := []struct {
		addr string
		want string
	}{
		{"1.2.3.4:5600", "1.2.3.4"},
		{"[::1]:8080", "::1"},
		{"1.2.3.4", "1.2.3.4"},
		{"", ""},
		{"hostname:5600", ""}, // not an IP
	}
	for _, tt := range tests {
		got := extractIP(tt.addr)
		if got != tt.want {
			t.Errorf("extractIP(%q) = %q, want %q", tt.addr, got, tt.want)
		}
	}
}
