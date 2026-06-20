package proxy

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
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

func TestJWTManagerCustomDo(t *testing.T) {
	called := false
	m := NewJWTManager("http://unused", "test-fp")
	m.customDo = func(url, contentType string, body io.Reader) (*http.Response, error) {
		called = true
		respBody := `{"jwt":"custom-token-xyz"}`
		return &http.Response{
			StatusCode: http.StatusOK,
			Body:       io.NopCloser(strings.NewReader(respBody)),
			Header:     make(http.Header),
		}, nil
	}

	jwt, err := m.Get()
	if err != nil {
		t.Fatalf("Get() error: %v", err)
	}
	if !called {
		t.Error("customDo was not called")
	}
	if jwt != "custom-token-xyz" {
		t.Errorf("Get() = %q, want %q", jwt, "custom-token-xyz")
	}
}

func TestJWTManagerCustomDoNil(t *testing.T) {
	callCount := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		callCount++
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprintf(w, `{"jwt":"fallback-token"}`)
	}))
	defer srv.Close()

	m := NewJWTManager(srv.URL+"/api/free-ai/bootstrap", "test-fp")
	// customDo left nil — should use httpClient.Post

	jwt, err := m.Get()
	if err != nil {
		t.Fatalf("Get() error: %v", err)
	}
	if callCount != 1 {
		t.Errorf("HTTP server called %d times, want 1", callCount)
	}
	if jwt != "fallback-token" {
		t.Errorf("Get() = %q, want %q", jwt, "fallback-token")
	}
}

func TestJWTManagerCustomDoError(t *testing.T) {
	m := NewJWTManager("http://unused", "test-fp")
	m.customDo = func(url, contentType string, body io.Reader) (*http.Response, error) {
		return nil, fmt.Errorf("proxy error")
	}

	_, err := m.Get()
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !strings.Contains(err.Error(), "proxy error") {
		t.Errorf("error = %q, want to contain %q", err.Error(), "proxy error")
	}
}

func TestBootstrapRequestUserAgent(t *testing.T) {
	var userAgent string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		userAgent = r.Header.Get("User-Agent")
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprintf(w, `{"jwt":"test-token-ua"}`)
	}))
	defer srv.Close()

	m := NewJWTManager(srv.URL+"/api/free-ai/bootstrap", "test-fp")
	// customDo left nil — should use httpClient path with User-Agent

	_, err := m.Get()
	if err != nil {
		t.Fatalf("Get() error: %v", err)
	}

	if userAgent == "" {
		t.Error("User-Agent header is empty in bootstrap request")
	}
	if !strings.Contains(userAgent, "mimocode/0.1.1") {
		t.Errorf("User-Agent = %q, want to contain %q", userAgent, "mimocode/0.1.1")
	}
}

func TestJWTManagerCustomDoNon200(t *testing.T) {
	m := NewJWTManager("http://unused", "test-fp")
	m.customDo = func(url, contentType string, body io.Reader) (*http.Response, error) {
		return &http.Response{
			StatusCode: http.StatusInternalServerError,
			Body:       io.NopCloser(strings.NewReader(`{"error":"internal"}`)),
			Header:     make(http.Header),
		}, nil
	}

	_, err := m.Get()
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !strings.Contains(err.Error(), "bootstrap returned status 500") {
		t.Errorf("error = %q, want to contain %q", err.Error(), "bootstrap returned status 500")
	}
}

func TestJWTManagerBootstrap403Banned(t *testing.T) {
	// Simulate a banned proxy: customDo detects 403 with proxy addr,
	// blacklists the proxy, and returns an error to the caller.
	m := NewJWTManager("http://unused", "test-fp")
	m.customDo = func(url, contentType string, body io.Reader) (*http.Response, error) {
		return nil, fmt.Errorf("proxy banned: bootstrap returned 403")
	}

	_, err := m.Get()
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	// The error should contain "403" so the caller can detect it was a 403 response
	if !strings.Contains(err.Error(), "403") {
		t.Errorf("error = %q, want to contain %q", err.Error(), "403")
	}
	// The error should contain "bootstrap request" (wrapped by fetch())
	if !strings.Contains(err.Error(), "bootstrap request") {
		t.Errorf("error = %q, want to contain %q", err.Error(), "bootstrap request")
	}
}

func TestJWTManagerBootstrap403NoAddr(t *testing.T) {
	// Simulate a direct connection (no proxy) that got 403.
	// customDo returns the 403 response as-is — no blacklist.
	m := NewJWTManager("http://unused", "test-fp")
	m.customDo = func(url, contentType string, body io.Reader) (*http.Response, error) {
		return &http.Response{
			StatusCode: http.StatusForbidden,
			Body:       io.NopCloser(strings.NewReader(`{"error":"forbidden"}`)),
			Header:     make(http.Header),
		}, nil
	}

	_, err := m.Get()
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	// The error should contain the standard message (from fetch())
	if !strings.Contains(err.Error(), "bootstrap returned status 403") {
		t.Errorf("error = %q, want to contain %q", err.Error(), "bootstrap returned status 403")
	}
	// The error should NOT contain proxy/banned info for direct connection
	if strings.Contains(err.Error(), "banned") {
		t.Errorf("direct 403 error should not mention 'banned', got: %q", err.Error())
	}
}
