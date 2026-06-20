package proxy

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"
)

// JWTManager caches a JWT token and refreshes it when expired or invalidated.
type JWTManager struct {
	mu           sync.Mutex
	jwt          string
	exp          int64 // milliseconds since epoch
	bootstrapURL string
	fingerprint  string
	httpClient   *http.Client
	customDo     func(url, contentType string, body io.Reader) (*http.Response, error)
}

// NewJWTManager creates a JWTManager that fetches tokens from the given bootstrap URL.
func NewJWTManager(bootstrapURL, fingerprint string) *JWTManager {
	return &JWTManager{
		bootstrapURL: bootstrapURL,
		fingerprint:  fingerprint,
		httpClient:   &http.Client{Timeout: 10 * time.Second},
	}
}

// Get returns a cached JWT if still valid, otherwise fetches a new one.
func (m *JWTManager) Get() (string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.jwt != "" && time.Now().UnixMilli() < m.exp {
		return m.jwt, nil
	}

	jwt, err := m.fetch()
	if err != nil {
		return "", err
	}
	return jwt, nil
}

// Invalidate clears the cached JWT so the next Get() fetches a new one.
func (m *JWTManager) Invalidate() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.jwt = ""
	m.exp = 0
}

// parseExp extracts the "exp" claim from a JWT payload (no signature verification).
// Returns milliseconds since epoch. Falls back to 50 minutes from now on error.
func (m *JWTManager) parseExp(token string) int64 {
	parts := strings.Split(token, ".")
	if len(parts) >= 2 {
		payloadB64 := parts[1]
		// Add padding
		payloadB64 += strings.Repeat("=", (4-len(payloadB64)%4)%4)
		raw, err := base64.URLEncoding.DecodeString(payloadB64)
		if err == nil {
			var payload struct {
				Exp json.Number `json:"exp"`
			}
			if err := json.Unmarshal(raw, &payload); err == nil {
				if s := payload.Exp.String(); s != "" {
					var expSec float64
					if _, err := fmt.Sscanf(s, "%f", &expSec); err == nil {
						return int64(expSec * 1000)
					}
				}
			}
		}
	}
	return time.Now().UnixMilli() + 50*60_000
}

func (m *JWTManager) fetch() (string, error) {
	body, err := json.Marshal(map[string]string{"client": m.fingerprint})
	if err != nil {
		return "", fmt.Errorf("marshal request: %w", err)
	}

	var resp *http.Response
	if m.customDo != nil {
		resp, err = m.customDo(m.bootstrapURL, "application/json", bytes.NewReader(body))
	} else {
		req, reqErr := http.NewRequest("POST", m.bootstrapURL, bytes.NewReader(body))
		if reqErr != nil {
			return "", fmt.Errorf("create bootstrap request: %w", reqErr)
		}
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("User-Agent", "mimocode/0.1.1")
		req.Header.Set("Accept", "*/*")
		resp, err = m.httpClient.Do(req)
	}
	if err != nil {
		return "", fmt.Errorf("bootstrap request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		bodyBytes, _ := io.ReadAll(resp.Body)
		return "", fmt.Errorf("bootstrap returned status %d: %s", resp.StatusCode, strings.TrimSpace(string(bodyBytes)))
	}

	var result struct {
		JWT string `json:"jwt"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return "", fmt.Errorf("decode response: %w", err)
	}
	if result.JWT == "" {
		return "", fmt.Errorf("no jwt in bootstrap response")
	}

	m.jwt = result.JWT
	m.exp = m.parseExp(result.JWT)
	return m.jwt, nil
}
