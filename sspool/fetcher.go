package sspool

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
)

// shadowmereServer represents a single server entry from the shadowmere.xyz API.
type shadowmereServer struct {
	ID          int      `json:"id"`
	Server      string   `json:"server"`
	ServerPort  int      `json:"server_port"`
	Password    string   `json:"password"`
	Method      string   `json:"method"`
	Protocol    string   `json:"protocol"`
	Alive       bool     `json:"alive"`
	Country     string   `json:"country"`
	Tags        []string `json:"tags"`
	Delay       int      `json:"delay"`
	RateLimit   *int     `json:"rate_limit"`
	AliveSince  string   `json:"alive_since"`
	LastCheck   string   `json:"last_check"`
	CreatedAt   string   `json:"created_at"`
	UpdatedAt   string   `json:"updated_at"`
}

// FetchFromShadowmere fetches SS server list from the shadowmere.xyz API,
// parses the JSON response, deduplicates, and returns SSConfig slice.
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
		if !s.Alive {
			continue
		}
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
