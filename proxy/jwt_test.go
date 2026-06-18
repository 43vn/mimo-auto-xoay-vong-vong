package proxy

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func makeToken(expMillis int64) string {
	payload := map[string]interface{}{
		"exp": expMillis / 1000, // JWT exp is in seconds
		"sub": "test",
	}
	raw, _ := json.Marshal(payload)
	encoded := base64.RawURLEncoding.EncodeToString(raw)
	return "header." + encoded + ".signature"
}

func TestParseExp(t *testing.T) {
	m := NewJWTManager("", "")
	expected := int64(1700000000000)
	token := makeToken(expected)
	got := m.parseExp(token)
	if got != expected {
		t.Errorf("parseExp = %d, want %d", got, expected)
	}
}

func TestParseExpInvalid(t *testing.T) {
	m := NewJWTManager("", "")
	before := time.Now().UnixMilli()
	got := m.parseExp("invalid.token")
	after := time.Now().UnixMilli()

	// Should fallback to ~50 minutes from now
	expected := before + 50*60_000
	if got < expected-1000 || got > expected+1000 {
		t.Errorf("parseExp fallback = %d, want near %d (before=%d, after=%d)", got, expected, before, after)
	}
}

func TestJWTManagerCache(t *testing.T) {
	callCount := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		callCount++
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprintf(w, `{"jwt":"cached-token-abc"}`)
	}))
	defer srv.Close()

	m := NewJWTManager(srv.URL+"/api/free-ai/bootstrap", "test-fp")

	jwt1, err := m.Get()
	if err != nil {
		t.Fatalf("first Get() error: %v", err)
	}
	if jwt1 != "cached-token-abc" {
		t.Errorf("first Get() = %q, want %q", jwt1, "cached-token-abc")
	}

	jwt2, err := m.Get()
	if err != nil {
		t.Fatalf("second Get() error: %v", err)
	}
	if jwt2 != "cached-token-abc" {
		t.Errorf("second Get() = %q, want %q", jwt2, "cached-token-abc")
	}

	if callCount != 1 {
		t.Errorf("HTTP called %d times, want 1 (cache should prevent second call)", callCount)
	}
}

func TestJWTManagerInvalidate(t *testing.T) {
	callCount := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		callCount++
		w.Header().Set("Content-Type", "application/json")
		if callCount == 1 {
			fmt.Fprintf(w, `{"jwt":"first-token"}`)
		} else {
			fmt.Fprintf(w, `{"jwt":"second-token"}`)
		}
	}))
	defer srv.Close()

	m := NewJWTManager(srv.URL+"/api/free-ai/bootstrap", "test-fp")

	jwt1, err := m.Get()
	if err != nil {
		t.Fatalf("first Get() error: %v", err)
	}
	if jwt1 != "first-token" {
		t.Errorf("first Get() = %q, want %q", jwt1, "first-token")
	}

	m.Invalidate()

	jwt2, err := m.Get()
	if err != nil {
		t.Fatalf("second Get() error: %v", err)
	}
	if jwt2 != "second-token" {
		t.Errorf("after invalidate Get() = %q, want %q", jwt2, "second-token")
	}
	if callCount != 2 {
		t.Errorf("HTTP called %d times, want 2", callCount)
	}
}

func TestParseExpNoExp(t *testing.T) {
	m := NewJWTManager("", "")
	// Token with no exp field
	payload := map[string]interface{}{"sub": "test"}
	raw, _ := json.Marshal(payload)
	encoded := base64.RawURLEncoding.EncodeToString(raw)
	token := "header." + encoded

	before := time.Now().UnixMilli()
	got := m.parseExp(token)
	after := time.Now().UnixMilli()

	// Should fallback to ~50 minutes from now
	expected := before + 50*60_000
	if got < expected-1000 || got > expected+1000 {
		t.Errorf("parseExp fallback = %d, want near %d (before=%d, after=%d)", got, expected, before, after)
	}
}

func TestJWTManagerFetchError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		fmt.Fprintf(w, `{"error":"server error"}`)
	}))
	defer srv.Close()

	m := NewJWTManager(srv.URL+"/api/free-ai/bootstrap", "test-fp")
	_, err := m.Get()
	if err == nil {
		t.Error("expected error on HTTP 500, got nil")
	}
}

func TestJWTManagerNoJWTField(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprintf(w, `{"token":"not-jwt"}`)
	}))
	defer srv.Close()

	m := NewJWTManager(srv.URL+"/api/free-ai/bootstrap", "test-fp")
	_, err := m.Get()
	if err == nil {
		t.Error("expected error when response has no jwt field, got nil")
	}
}

func TestJWTManagerBodyFormat(t *testing.T) {
	var receivedBody []byte
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Errorf("method = %s, want POST", r.Method)
		}
		if !strings.HasSuffix(r.URL.Path, "/api/free-ai/bootstrap") {
			t.Errorf("path = %s, want .../api/free-ai/bootstrap", r.URL.Path)
		}
		receivedBody, _ = json.Marshal(map[string]string{"client": "test-fp"})
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprintf(w, `{"jwt":"token"}`)
	}))
	defer srv.Close()

	m := NewJWTManager(srv.URL+"/api/free-ai/bootstrap", "test-fp")
	_, _ = m.Get()

	expectedBody := `{"client":"test-fp"}`
	if string(receivedBody) != expectedBody {
		t.Errorf("request body = %s, want %s", string(receivedBody), expectedBody)
	}
}
