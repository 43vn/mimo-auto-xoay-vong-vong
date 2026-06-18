package sspool

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
)

// shadowmereServer represents a single server entry from the shadowmere.xyz API.
type shadowmereServer struct {
	Server     string `json:"server"`
	ServerPort int    `json:"server_port"`
	Password   string `json:"password"`
	Method     string `json:"method"`
}

// FetchFromShadowmere fetches SS server list from the shadowmere.xyz API,
// parses the JSON response, deduplicates, and returns SSConfig slice.
// The API returns a flat list — we pass ALL servers to the health checker
// which determines which ones are actually alive via TCP dial.
func FetchFromShadowmere(apiURL string) ([]SSConfig, error) {
	resp, err := http.Get(apiURL)
	if err != nil {
		return nil, fmt.Errorf("HTTP request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("HTTP %d: %s", resp.StatusCode, string(body))
	}

	var raw []shadowmereServer
	if err := json.NewDecoder(resp.Body).Decode(&raw); err != nil {
		return nil, fmt.Errorf("JSON decode failed: %w", err)
	}

	// Convert to SSConfig and dedup
	seen := make(map[string]bool)
	servers := make([]SSConfig, 0, len(raw))
	for _, s := range raw {
		cfg := SSConfig{
			Server:   s.Server,
			Port:     s.ServerPort,
			Password: s.Password,
			Method:   s.Method,
		}
		k := cfg.Key()
		if !seen[k] {
			seen[k] = true
			servers = append(servers, cfg)
		}
	}

	return servers, nil
}
