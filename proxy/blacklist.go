package proxy

import (
	"encoding/json"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"
)

const (
	BlacklistFilename = "blacklist.json"
)

// blacklistEntry stores a single blacklisted IP.
// Expires field is kept for backward compatibility with old config files
// but is ignored — blacklist is permanent.
type blacklistEntry struct {
	IP      string `json:"ip"`
	Expires string `json:"expires,omitempty"` // ignored, legacy field
}

// BlacklistManager manages a persistent IP-based blacklist.
// Blacklist is permanent and by IP regardless of port — if 1.2.3.4:5600
// is blacklisted, 1.2.3.4:8080 is also skipped.
type BlacklistManager struct {
	mu      sync.RWMutex
	entries map[string]bool // key = IP, value = true (blacklisted)
	file    string          // path to blacklist.json
}

// NewBlacklistManager creates a new manager and loads existing blacklist from file.
func NewBlacklistManager(dataDir string) *BlacklistManager {
	file := filepath.Join(dataDir, BlacklistFilename)
	bm := &BlacklistManager{
		entries: make(map[string]bool),
		file:    file,
	}
	bm.load()
	return bm
}

// Add blacklists an IP permanently. Only the IP part is stored.
func (bm *BlacklistManager) Add(addr string) {
	ip := extractIP(addr)
	if ip == "" {
		return
	}
	bm.mu.Lock()
	defer bm.mu.Unlock()
	bm.entries[ip] = true
}

// IsBlacklisted returns true if the given address (host:port) has a blacklisted IP.
func (bm *BlacklistManager) IsBlacklisted(addr string) bool {
	ip := extractIP(addr)
	if ip == "" {
		return false
	}
	bm.mu.RLock()
	defer bm.mu.RUnlock()
	return bm.entries[ip]
}

// Remove removes an IP from the blacklist.
func (bm *BlacklistManager) Remove(addr string) {
	ip := extractIP(addr)
	if ip == "" {
		return
	}
	bm.mu.Lock()
	defer bm.mu.Unlock()
	delete(bm.entries, ip)
}

// Len returns the number of blacklisted IPs.
func (bm *BlacklistManager) Len() int {
	bm.mu.RLock()
	defer bm.mu.RUnlock()
	return len(bm.entries)
}

// Save writes the current blacklist to disk.
func (bm *BlacklistManager) Save() error {
	bm.mu.RLock()
	defer bm.mu.RUnlock()

	var list []blacklistEntry
	for ip := range bm.entries {
		list = append(list, blacklistEntry{IP: ip})
	}

	data, err := json.MarshalIndent(list, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal blacklist: %w", err)
	}

	dir := filepath.Dir(bm.file)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return fmt.Errorf("create dir: %w", err)
	}

	// Atomic write: write to temp file then rename
	tmp := bm.file + ".tmp"
	if err := os.WriteFile(tmp, data, 0644); err != nil {
		return fmt.Errorf("write temp: %w", err)
	}
	if err := os.Rename(tmp, bm.file); err != nil {
		return fmt.Errorf("rename: %w", err)
	}
	return nil
}

// load reads the blacklist from disk.
// Old entries with expires field are loaded as permanent (expires ignored).
func (bm *BlacklistManager) load() {
	data, err := os.ReadFile(bm.file)
	if err != nil {
		if os.IsNotExist(err) {
			return
		}
		fmt.Printf("[Blacklist] load error: %v\n", err)
		return
	}

	var entries []blacklistEntry
	if err := json.Unmarshal(data, &entries); err != nil {
		fmt.Printf("[Blacklist] parse error: %v\n", err)
		return
	}

	loaded := 0
	for _, entry := range entries {
		if entry.IP != "" {
			bm.entries[entry.IP] = true
			loaded++
		}
	}
	fmt.Printf("[Blacklist] loaded %d entries from %s\n", loaded, bm.file)
}

// Stop is a no-op kept for backward compatibility.
func (bm *BlacklistManager) Stop() {}

// extractIP extracts the IP from a host:port address string.
func extractIP(addr string) string {
	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		// No port — try as bare IP
		host = strings.TrimSpace(addr)
	}
	if host == "" {
		return ""
	}
	// Validate it's an IP
	ip := net.ParseIP(host)
	if ip == nil {
		return ""
	}
	return ip.String()
}
