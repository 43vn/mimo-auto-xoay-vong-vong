package sspool

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestFetchFromShadowmere(t *testing.T) {
	// Mock shadowmere API
	data := []shadowmereServer{
		{
			Server:     "1.2.3.4",
			ServerPort: 8388,
			Password:   "pass1",
			Method:     "aes-256-gcm",
		},
		{
			Server:     "5.6.7.8",
			ServerPort: 443,
			Password:   "pass2",
			Method:     "chacha20-ietf-poly1305",
		},
	}

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(data)
	}))
	defer ts.Close()

	servers, err := FetchFromShadowmere(ts.URL)
	if err != nil {
		t.Fatalf("FetchFromShadowmere() error: %v", err)
	}
	if len(servers) != 2 {
		t.Fatalf("got %d servers, want 2", len(servers))
	}

	// Check first server
	if servers[0].Server != "1.2.3.4" {
		t.Errorf("servers[0].Server = %q, want %q", servers[0].Server, "1.2.3.4")
	}
	if servers[0].Port != 8388 {
		t.Errorf("servers[0].Port = %d, want 8388", servers[0].Port)
	}
	if servers[0].Password != "pass1" {
		t.Errorf("servers[0].Password = %q, want %q", servers[0].Password, "pass1")
	}
	if servers[0].Method != "aes-256-gcm" {
		t.Errorf("servers[0].Method = %q, want %q", servers[0].Method, "aes-256-gcm")
	}

	// Check second server
	if servers[1].Server != "5.6.7.8" {
		t.Errorf("servers[1].Server = %q, want %q", servers[1].Server, "5.6.7.8")
	}
	if servers[1].Port != 443 {
		t.Errorf("servers[1].Port = %d, want 443", servers[1].Port)
	}
}

func TestFetchFromShadowmereDedup(t *testing.T) {
	// API returns duplicates
	data := []shadowmereServer{
		{Server: "1.2.3.4", ServerPort: 8388, Password: "pass1", Method: "aes-256-gcm"},
		{Server: "1.2.3.4", ServerPort: 8388, Password: "pass1", Method: "aes-256-gcm"},
		{Server: "1.2.3.4", ServerPort: 8388, Password: "pass1", Method: "aes-256-gcm"},
	}

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(data)
	}))
	defer ts.Close()

	servers, err := FetchFromShadowmere(ts.URL)
	if err != nil {
		t.Fatalf("FetchFromShadowmere() error: %v", err)
	}
	if len(servers) != 1 {
		t.Errorf("got %d servers after dedup, want 1", len(servers))
	}
}

func TestFetchFromShadowmereHTTPError(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "internal error", http.StatusInternalServerError)
	}))
	defer ts.Close()

	_, err := FetchFromShadowmere(ts.URL)
	if err == nil {
		t.Error("expected error for HTTP 500, got nil")
	}
}

func TestFetchFromShadowmereInvalidJSON(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte("not json"))
	}))
	defer ts.Close()

	_, err := FetchFromShadowmere(ts.URL)
	if err == nil {
		t.Error("expected error for invalid JSON, got nil")
	}
}

func TestFetchFromShadowmereEmpty(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte("[]"))
	}))
	defer ts.Close()

	servers, err := FetchFromShadowmere(ts.URL)
	if err != nil {
		t.Fatalf("FetchFromShadowmere() error: %v", err)
	}
	if len(servers) != 0 {
		t.Errorf("got %d servers from empty response, want 0", len(servers))
	}
}
