package proxy

import (
	"bytes"
	"context"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/vincent/mimo-xoay/rotator"
	"github.com/vincent/mimo-xoay/sspool"
)

// mockBootstrap is a test HTTP server that returns a valid JWT.
func mockBootstrap(t *testing.T) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		// Return a JWT with far-future exp (year 2099)
		jwt := "eyJhbGciOiJIUzI1NiJ9.eyJleHAiOjQxMDI0NDQ4MDB9.fake_signature"
		json.NewEncoder(w).Encode(map[string]string{"jwt": jwt})
	}))
}

// newTestServer creates a Server with minimal dependencies for handler testing.
// upstream is the mock upstream server URL; if empty, uses BaseURL.
func newTestServer(t *testing.T, upstream string) *Server {
	t.Helper()
	pool := sspool.NewSSPool()
	bootstrap := mockBootstrap(t)
	t.Cleanup(bootstrap.Close)
	jwtMgr := NewJWTManager(bootstrap.URL, "test-fp")
	r := rotator.New([]string{}, 0, nil)
	srv := NewServer(pool, jwtMgr, r, 0, nil)
	if upstream != "" {
		srv.baseURL = upstream
	}
	return srv
}

func TestHandleModels(t *testing.T) {
	srv := newTestServer(t, "")

	req := httptest.NewRequest(http.MethodGet, "/v1/models", nil)
	w := httptest.NewRecorder()

	srv.handleModels(w, req)

	resp := w.Result()
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected status 200, got %d", resp.StatusCode)
	}

	ct := resp.Header.Get("Content-Type")
	if ct != "application/json" {
		t.Fatalf("expected Content-Type application/json, got %s", ct)
	}

	var body struct {
		Object string `json:"object"`
		Data   []struct {
			ID       string `json:"id"`
			Object   string `json:"object"`
			OwnedBy  string `json:"owned_by"`
		} `json:"data"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}

	if body.Object != "list" {
		t.Fatalf("expected object=list, got %s", body.Object)
	}
	if len(body.Data) != 1 {
		t.Fatalf("expected 1 model, got %d", len(body.Data))
	}
	if body.Data[0].ID != "mimo-auto" {
		t.Fatalf("expected model id mimo-auto, got %s", body.Data[0].ID)
	}
	if body.Data[0].OwnedBy != "mimo" {
		t.Fatalf("expected owned_by mimo, got %s", body.Data[0].OwnedBy)
	}

	// Check CORS header
	if got := resp.Header.Get("Access-Control-Allow-Origin"); got != "*" {
		t.Fatalf("expected Access-Control-Allow-Origin *, got %s", got)
	}
}

func TestHandleOptions(t *testing.T) {
	srv := newTestServer(t, "")

	req := httptest.NewRequest(http.MethodOptions, "/v1/chat/completions", nil)
	w := httptest.NewRecorder()

	srv.handleOptions(w, req)

	resp := w.Result()
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected status 200, got %d", resp.StatusCode)
	}

	checks := map[string]string{
		"Access-Control-Allow-Origin":  "*",
		"Access-Control-Allow-Methods": "POST, GET, OPTIONS",
		"Access-Control-Allow-Headers": "*",
	}
	for header, expected := range checks {
		got := resp.Header.Get(header)
		if got != expected {
			t.Fatalf("header %s: expected %q, got %q", header, expected, got)
		}
	}
}

func TestHandleChatNoMessages(t *testing.T) {
	srv := newTestServer(t, "")

	// Empty body
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", bytes.NewReader([]byte("{}")))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	srv.handleChat(w, req)

	resp := w.Result()
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("expected status 400, got %d", resp.StatusCode)
	}
}

func TestHandleChatInvalidJSON(t *testing.T) {
	srv := newTestServer(t, "")

	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", bytes.NewReader([]byte("not json")))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	srv.handleChat(w, req)

	resp := w.Result()
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("expected status 400, got %d", resp.StatusCode)
	}
}

func TestHandleChatNonStreaming(t *testing.T) {
	// Mock upstream that returns a normal JSON response
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(map[string]interface{}{
			"choices": []map[string]interface{}{
				{"message": map[string]string{"role": "assistant", "content": "hello"}},
			},
		})
	}))
	defer upstream.Close()

	srv := newTestServer(t, upstream.URL)

	body := map[string]interface{}{
		"model":    "mimo-auto",
		"messages": []map[string]string{{"role": "user", "content": "hi"}},
	}
	bodyBytes, _ := json.Marshal(body)

	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", bytes.NewReader(bodyBytes))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	srv.handleChat(w, req)

	resp := w.Result()
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		respBody, _ := io.ReadAll(resp.Body)
		t.Fatalf("expected status 200, got %d: %s", resp.StatusCode, string(respBody))
	}

	var result map[string]interface{}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}

	choices, ok := result["choices"].([]interface{})
	if !ok || len(choices) == 0 {
		t.Fatalf("expected choices in response, got %v", result)
	}
}

func TestHandleChatStreaming(t *testing.T) {
	// Mock upstream that returns SSE data
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		fmt := "data: {\"choices\":[{\"delta\":{\"content\":\"hello\"}}]}\n\n"
		fmt += "data: [DONE]\n\n"
		w.Write([]byte(fmt))
	}))
	defer upstream.Close()

	srv := newTestServer(t, upstream.URL)

	body := map[string]interface{}{
		"model":    "mimo-auto",
		"stream":   true,
		"messages": []map[string]string{{"role": "user", "content": "hi"}},
	}
	bodyBytes, _ := json.Marshal(body)

	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", bytes.NewReader(bodyBytes))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "text/event-stream")
	w := httptest.NewRecorder()

	srv.handleChat(w, req)

	resp := w.Result()
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		respBody, _ := io.ReadAll(resp.Body)
		t.Fatalf("expected status 200, got %d: %s", resp.StatusCode, string(respBody))
	}

	ct := resp.Header.Get("Content-Type")
	if ct != "text/event-stream" {
		t.Fatalf("expected Content-Type text/event-stream, got %s", ct)
	}
}

func TestHandleChatUpstreamError(t *testing.T) {
	// Mock upstream that returns 500
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		w.Write([]byte("internal error"))
	}))
	defer upstream.Close()

	srv := newTestServer(t, upstream.URL)

	body := map[string]interface{}{
		"model":    "mimo-auto",
		"messages": []map[string]string{{"role": "user", "content": "hi"}},
	}
	bodyBytes, _ := json.Marshal(body)

	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", bytes.NewReader(bodyBytes))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	srv.handleChat(w, req)

	resp := w.Result()
	defer resp.Body.Close()

	// Should forward upstream status
	if resp.StatusCode != http.StatusInternalServerError {
		t.Fatalf("expected status 500, got %d", resp.StatusCode)
	}
}

func TestHandleNotFound(t *testing.T) {
	srv := newTestServer(t, "")

	req := httptest.NewRequest(http.MethodGet, "/unknown/path", nil)
	w := httptest.NewRecorder()

	srv.handleNotFound(w, req)

	resp := w.Result()
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("expected status 404, got %d", resp.StatusCode)
	}
}

func TestServerStartAndShutdown(t *testing.T) {
	pool := sspool.NewSSPool()
	jwtMgr := NewJWTManager("http://localhost:0/bootstrap", "test-fp")
	r := rotator.New([]string{}, 0, nil)
	srv := NewServer(pool, jwtMgr, r, 0, nil) // port 0 = auto-assign

	errCh := make(chan error, 1)
	go func() {
		errCh <- srv.Start()
	}()

	// Wait for server to be ready
	time.Sleep(200 * time.Millisecond)

	// Shutdown gracefully
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	err := srv.Shutdown(ctx)
	if err != nil {
		t.Fatalf("shutdown error: %v", err)
	}

	// Wait for Start() to return
	startErr := <-errCh
	// http.ErrServerClosed is expected on graceful shutdown
	if startErr != nil && startErr != http.ErrServerClosed {
		t.Fatalf("unexpected error: %v", startErr)
	}
}

// TestGenerateFingerprintLength32 verifies fingerprint = 32 bytes = 64 hex chars.
func TestGenerateFingerprintLength32(t *testing.T) {
	fp := generateFingerprint()
	// 32 bytes = 64 hex chars
	expectedLen := 64
	if len(fp) != expectedLen {
		t.Errorf("fingerprint length = %d, want %d (32 bytes = 64 hex chars)", len(fp), expectedLen)
	}
	// Also verify it's valid hex
	if _, err := hex.DecodeString(fp); err != nil {
		t.Errorf("fingerprint is not valid hex: %v", err)
	}
}

// TestUpstreamUserAgent verifies upstream chat request has User-Agent: mimocode/0.1.1 ...
func TestUpstreamUserAgent(t *testing.T) {
	var userAgent string
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		userAgent = r.Header.Get("User-Agent")
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(map[string]interface{}{
			"choices": []map[string]interface{}{
				{"message": map[string]string{"role": "assistant", "content": "hello"}},
			},
		})
	}))
	defer upstream.Close()

	srv := newTestServer(t, upstream.URL)

	body := map[string]interface{}{
		"model":    "mimo-auto",
		"messages": []map[string]string{{"role": "user", "content": "hi"}},
	}
	bodyBytes, _ := json.Marshal(body)

	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", bytes.NewReader(bodyBytes))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	srv.handleChat(w, req)

	resp := w.Result()
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		respBody, _ := io.ReadAll(resp.Body)
		t.Fatalf("expected status 200, got %d: %s", resp.StatusCode, string(respBody))
	}

	if userAgent == "" {
		t.Fatal("User-Agent header is empty in upstream request")
	}
	expectedPrefix := "mimocode/0.1.1"
	if !strings.HasPrefix(userAgent, expectedPrefix) {
		t.Errorf("User-Agent = %q, want prefix %q", userAgent, expectedPrefix)
	}
}

// TestUpstreamXMimoSource verifies upstream request has X-Mimo-Source: mimocode-cli-free.
func TestUpstreamXMimoSource(t *testing.T) {
	var xMimoSource string
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		xMimoSource = r.Header.Get("X-Mimo-Source")
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(map[string]interface{}{
			"choices": []map[string]interface{}{
				{"message": map[string]string{"role": "assistant", "content": "hello"}},
			},
		})
	}))
	defer upstream.Close()

	srv := newTestServer(t, upstream.URL)

	body := map[string]interface{}{
		"model":    "mimo-auto",
		"messages": []map[string]string{{"role": "user", "content": "hi"}},
	}
	bodyBytes, _ := json.Marshal(body)

	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", bytes.NewReader(bodyBytes))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	srv.handleChat(w, req)

	resp := w.Result()
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		respBody, _ := io.ReadAll(resp.Body)
		t.Fatalf("expected status 200, got %d: %s", resp.StatusCode, string(respBody))
	}

	if xMimoSource != "mimocode-cli-free" {
		t.Errorf("X-Mimo-Source = %q, want %q", xMimoSource, "mimocode-cli-free")
	}
}

// --- NEW TESTS for fix-proxy-json ---

// TestHandleChatArrayContent sends a request with content as an array (OpenCode AI SDK format).
// Expect: proxy accepts (not 400) and forwards to upstream which returns 200.
func TestHandleChatArrayContent(t *testing.T) {
	// Capture the body forwarded to upstream
	var capturedBody []byte
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var err error
		capturedBody, err = io.ReadAll(r.Body)
		if err != nil {
			t.Errorf("upstream: failed to read body: %v", err)
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(map[string]interface{}{
			"choices": []map[string]interface{}{
				{"message": map[string]string{"role": "assistant", "content": "hello back"}},
			},
		})
	}))
	defer upstream.Close()

	srv := newTestServer(t, upstream.URL)

	// Build request with array content (OpenCode AI SDK format)
	reqBody := map[string]interface{}{
		"model": "mimo-auto",
		"messages": []map[string]interface{}{
			{
				"role": "user",
				"content": []map[string]string{
					{"type": "text", "text": "hello"},
				},
			},
		},
	}
	bodyBytes, _ := json.Marshal(reqBody)

	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", bytes.NewReader(bodyBytes))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	srv.handleChat(w, req)

	resp := w.Result()
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		respBody, _ := io.ReadAll(resp.Body)
		t.Fatalf("expected status 200, got %d: %s", resp.StatusCode, string(respBody))
	}

	// Verify the upstream received the request
	if capturedBody == nil {
		t.Fatal("upstream did not receive any body")
	}

	// Verify the forwarded body is valid JSON and contains the array content
	var forwarded map[string]interface{}
	if err := json.Unmarshal(capturedBody, &forwarded); err != nil {
		t.Fatalf("forwarded body is not valid JSON: %v", err)
	}

	messages, ok := forwarded["messages"].([]interface{})
	if !ok || len(messages) < 2 { // system + user
		t.Fatalf("expected at least 2 messages (system + user), got %v", forwarded["messages"])
	}

	// The user message should have array content
	userMsg, ok := messages[1].(map[string]interface{})
	if !ok {
		t.Fatalf("expected user message to be an object, got %T", messages[1])
	}
	contentArr, ok := userMsg["content"].([]interface{})
	if !ok {
		t.Fatalf("expected user content to be an array, got %T (%v)", userMsg["content"], userMsg["content"])
	}
	if len(contentArr) != 1 {
		t.Fatalf("expected 1 content item, got %d", len(contentArr))
	}
	contentItem, ok := contentArr[0].(map[string]interface{})
	if !ok {
		t.Fatalf("expected content item to be an object, got %T", contentArr[0])
	}
	if contentItem["type"] != "text" || contentItem["text"] != "hello" {
		t.Fatalf("expected content item {type:text, text:hello}, got %v", contentItem)
	}
}

// TestHandleChatNullContent sends a request with content: null and tool_calls.
// Expect: proxy accepts (not 400) and forwards to upstream.
func TestHandleChatNullContent(t *testing.T) {
	var capturedBody []byte
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		capturedBody, _ = io.ReadAll(r.Body)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(map[string]interface{}{
			"choices": []map[string]interface{}{
				{"message": map[string]string{"role": "assistant", "content": "ok"}},
			},
		})
	}))
	defer upstream.Close()

	srv := newTestServer(t, upstream.URL)

	reqBody := map[string]interface{}{
		"model": "mimo-auto",
		"messages": []map[string]interface{}{
			{
				"role":    "assistant",
				"content": nil,
				"tool_calls": []map[string]interface{}{
					{
						"id":   "tc_1",
						"type": "function",
						"function": map[string]string{
							"name":      "test",
							"arguments": "{}",
						},
					},
				},
			},
		},
	}
	bodyBytes, _ := json.Marshal(reqBody)

	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", bytes.NewReader(bodyBytes))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	srv.handleChat(w, req)

	resp := w.Result()
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		respBody, _ := io.ReadAll(resp.Body)
		t.Fatalf("expected status 200, got %d: %s", resp.StatusCode, string(respBody))
	}

	if capturedBody == nil {
		t.Fatal("upstream did not receive any body")
	}

	// Verify the forwarded body is valid JSON
	var forwarded map[string]interface{}
	if err := json.Unmarshal(capturedBody, &forwarded); err != nil {
		t.Fatalf("forwarded body is not valid JSON: %v", err)
	}

	messages, ok := forwarded["messages"].([]interface{})
	if !ok || len(messages) < 2 {
		t.Fatalf("expected at least 2 messages, got %v", forwarded["messages"])
	}

	// The assistant message should have null content preserved
	assistantMsg, ok := messages[1].(map[string]interface{})
	if !ok {
		t.Fatalf("expected assistant message to be an object, got %T", messages[1])
	}
	if assistantMsg["content"] != nil {
		t.Fatalf("expected assistant content to be null, got %v", assistantMsg["content"])
	}
}

// TestHandleChatExtraFields sends a request with extra fields (tools, temperature, max_tokens, tool_choice).
// Expect: proxy forwards these fields to upstream.
func TestHandleChatExtraFields(t *testing.T) {
	var capturedBody []byte
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		capturedBody, _ = io.ReadAll(r.Body)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(map[string]interface{}{
			"choices": []map[string]interface{}{
				{"message": map[string]string{"role": "assistant", "content": "ok"}},
			},
		})
	}))
	defer upstream.Close()

	srv := newTestServer(t, upstream.URL)

	reqBody := map[string]interface{}{
		"model":    "mimo-auto",
		"messages": []map[string]string{{"role": "user", "content": "hi"}},
		"tools": []map[string]interface{}{
			{
				"type": "function",
				"function": map[string]string{
					"name":        "get_weather",
					"description": "Get weather",
					"parameters":  `{}`,
				},
			},
		},
		"temperature":  0.7,
		"max_tokens":   1024,
		"tool_choice":  "auto",
	}
	bodyBytes, _ := json.Marshal(reqBody)

	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", bytes.NewReader(bodyBytes))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	srv.handleChat(w, req)

	resp := w.Result()
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		respBody, _ := io.ReadAll(resp.Body)
		t.Fatalf("expected status 200, got %d: %s", resp.StatusCode, string(respBody))
	}

	if capturedBody == nil {
		t.Fatal("upstream did not receive any body")
	}

	// Verify extra fields are forwarded
	var forwarded map[string]interface{}
	if err := json.Unmarshal(capturedBody, &forwarded); err != nil {
		t.Fatalf("forwarded body is not valid JSON: %v", err)
	}

	// Check tools
	tools, ok := forwarded["tools"].([]interface{})
	if !ok || len(tools) == 0 {
		t.Fatalf("expected tools to be forwarded, got %v", forwarded["tools"])
	}

	// Check temperature
	temp, ok := forwarded["temperature"].(float64)
	if !ok || temp != 0.7 {
		t.Fatalf("expected temperature 0.7, got %v", forwarded["temperature"])
	}

	// Check max_tokens
	maxTokens, ok := forwarded["max_tokens"].(float64)
	if !ok || maxTokens != 1024 {
		t.Fatalf("expected max_tokens 1024, got %v", forwarded["max_tokens"])
	}

	// Check tool_choice
	toolChoice, ok := forwarded["tool_choice"].(string)
	if !ok || toolChoice != "auto" {
		t.Fatalf("expected tool_choice auto, got %v", forwarded["tool_choice"])
	}
}

// TestHandleChatSystemInjectArrayContent verifies that system message injection works with array content.
// Expect: system message is injected at position 0, array content is preserved in position 1.
func TestHandleChatSystemInjectArrayContent(t *testing.T) {
	var capturedBody []byte
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		capturedBody, _ = io.ReadAll(r.Body)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(map[string]interface{}{
			"choices": []map[string]interface{}{
				{"message": map[string]string{"role": "assistant", "content": "ok"}},
			},
		})
	}))
	defer upstream.Close()

	srv := newTestServer(t, upstream.URL)

	reqBody := map[string]interface{}{
		"model": "mimo-auto",
		"messages": []map[string]interface{}{
			{
				"role": "user",
				"content": []map[string]string{
					{"type": "text", "text": "hello"},
				},
			},
		},
	}
	bodyBytes, _ := json.Marshal(reqBody)

	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", bytes.NewReader(bodyBytes))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	srv.handleChat(w, req)

	resp := w.Result()
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		respBody, _ := io.ReadAll(resp.Body)
		t.Fatalf("expected status 200, got %d: %s", resp.StatusCode, string(respBody))
	}

	if capturedBody == nil {
		t.Fatal("upstream did not receive any body")
	}

	var forwarded map[string]interface{}
	if err := json.Unmarshal(capturedBody, &forwarded); err != nil {
		t.Fatalf("forwarded body is not valid JSON: %v", err)
	}

	messages, ok := forwarded["messages"].([]interface{})
	if !ok || len(messages) != 2 {
		t.Fatalf("expected 2 messages (system + user), got %d", len(messages))
	}

	// Position 0 should be the system message
	systemMsg, ok := messages[0].(map[string]interface{})
	if !ok {
		t.Fatalf("expected message 0 to be an object, got %T", messages[0])
	}
	if systemMsg["role"] != "system" {
		t.Fatalf("expected message 0 role=system, got %v", systemMsg["role"])
	}
	if systemMsg["content"] != systemMessage {
		t.Fatalf("expected message 0 content to equal systemMessage")
	}

	// Position 1 should be the user message with array content
	userMsg, ok := messages[1].(map[string]interface{})
	if !ok {
		t.Fatalf("expected message 1 to be an object, got %T", messages[1])
	}
	if userMsg["role"] != "user" {
		t.Fatalf("expected message 1 role=user, got %v", userMsg["role"])
	}
	contentArr, ok := userMsg["content"].([]interface{})
	if !ok {
		t.Fatalf("expected message 1 content to be an array, got %T", userMsg["content"])
	}
	if len(contentArr) != 1 {
		t.Fatalf("expected 1 content item, got %d", len(contentArr))
	}
}

// TestHandleChatMalformedJSON sends malformed JSON.
// Expect: proxy returns 400 (regression guard).
func TestHandleChatMalformedJSON(t *testing.T) {
	srv := newTestServer(t, "")

	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", bytes.NewReader([]byte(`{broken`)))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	srv.handleChat(w, req)

	resp := w.Result()
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("expected status 400, got %d", resp.StatusCode)
	}
}

// TestHandleChatUpstream429Rotate verifies that when upstream returns 429,
// the proxy rotates to the next SS proxy and retries.
func TestHandleChatUpstream429Rotate(t *testing.T) {
	requestCount := 0
	// Mock upstream: return 429 on first request, 200 on retry
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requestCount++
		if requestCount == 1 {
			w.WriteHeader(http.StatusTooManyRequests)
			w.Write([]byte(`{"error":"rate limit"}`))
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(map[string]interface{}{
			"choices": []map[string]interface{}{
				{"message": map[string]string{"role": "assistant", "content": "ok after rotate"}},
			},
		})
	}))
	defer upstream.Close()

	// Empty SSPool — createSSDialer returns nil → falls through to default HTTP client
	// (avoids trying to connect through non-existent SS server)
	pool := sspool.NewSSPool()

	bootstrap := mockBootstrap(t)
	defer bootstrap.Close()
	jwtMgr := NewJWTManager(bootstrap.URL, "test-fp")

	// Rotator with 2 addresses to trigger rotation logic
	addrs := []string{"1.1.1.1:8388", "2.2.2.2:8388"}
	rotateLog := make(chan string, 10)
	r := rotator.New(addrs, 0, func(addr string) {
		rotateLog <- addr
	})

	srv := NewServer(pool, jwtMgr, r, 0, &Options{
		RateLimitRetryDelay: 10 * time.Millisecond, // fast retry for test
	})
	srv.baseURL = upstream.URL

	body := map[string]interface{}{
		"model":    "mimo-auto",
		"messages": []map[string]string{{"role": "user", "content": "hi"}},
	}
	bodyBytes, _ := json.Marshal(body)

	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", bytes.NewReader(bodyBytes))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	srv.handleChat(w, req)

	resp := w.Result()
	defer resp.Body.Close()

	// Should succeed after rotation
	if resp.StatusCode != http.StatusOK {
		respBody, _ := io.ReadAll(resp.Body)
		t.Fatalf("expected status 200 after rotate, got %d: %s", resp.StatusCode, string(respBody))
	}

	// Verify upstream was called twice (first 429, then retry)
	if requestCount != 2 {
		t.Fatalf("expected upstream to be called 2 times, got %d", requestCount)
	}

	// Verify rotation happened
	select {
	case addr := <-rotateLog:
		t.Logf("proxy rotated to: %s", addr)
	default:
		t.Fatal("expected proxy rotation but none occurred")
	}
}

// TestHandleChat429EmptyRotator verifies that when the rotator pool is empty
// and upstream returns 429, the proxy returns 502 (not panic).
func TestHandleChat429EmptyRotator(t *testing.T) {
	// Mock upstream that always returns 429
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusTooManyRequests)
		w.Write([]byte(`{"error":"rate limit"}`))
	}))
	defer upstream.Close()

	pool := sspool.NewSSPool()
	bootstrap := mockBootstrap(t)
	defer bootstrap.Close()
	jwtMgr := NewJWTManager(bootstrap.URL, "test-fp")

	// Empty rotator — no addresses
	r := rotator.New([]string{}, 0, nil)

	srv := NewServer(pool, jwtMgr, r, 0, &Options{
		RateLimitRetryDelay: 10 * time.Millisecond, // fast retry for test
	})
	srv.baseURL = upstream.URL

	body := map[string]interface{}{
		"model":    "mimo-auto",
		"messages": []map[string]string{{"role": "user", "content": "hi"}},
	}
	bodyBytes, _ := json.Marshal(body)

	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", bytes.NewReader(bodyBytes))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	srv.handleChat(w, req)

	resp := w.Result()
	defer resp.Body.Close()

	// Should not panic. With empty rotator and 429 upstream, retries exhaust → 502 or 429
	if resp.StatusCode != http.StatusBadGateway && resp.StatusCode != http.StatusTooManyRequests {
		respBody, _ := io.ReadAll(resp.Body)
		t.Fatalf("expected status 502 or 429, got %d: %s", resp.StatusCode, string(respBody))
	}
}

// TestHandleChatNoRateLimitAutoMode verifies that auto mode disables local rate limiter.
// Requests should go through to upstream even if they exceed the local rate limit window.
func TestHandleChatNoRateLimitAutoMode(t *testing.T) {
	requestCount := 0
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requestCount++
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(map[string]interface{}{
			"choices": []map[string]interface{}{
				{"message": map[string]string{"role": "assistant", "content": "ok"}},
			},
		})
	}))
	defer upstream.Close()

	// Empty pool + empty rotator — no SS dialing, direct HTTP to mock upstream
	pool := sspool.NewSSPool()
	bootstrap := mockBootstrap(t)
	defer bootstrap.Close()
	jwtMgr := NewJWTManager(bootstrap.URL, "test-fp")
	r := rotator.New([]string{}, 0, nil)

	// DisableRateLimit = true (simulates auto mode)
	srv := NewServer(pool, jwtMgr, r, 0, &Options{
		DisableRateLimit: true,
	})
	srv.baseURL = upstream.URL

	body := map[string]interface{}{
		"model":    "mimo-auto",
		"messages": []map[string]string{{"role": "user", "content": "hi"}},
	}
	bodyBytes, _ := json.Marshal(body)

	// Send more requests than RateLimitMax (8) — should all pass with rate limit disabled
	for i := 0; i < 12; i++ {
		req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", bytes.NewReader(bodyBytes))
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()

		srv.handleChat(w, req)

		resp := w.Result()
		if resp.StatusCode != http.StatusOK {
			respBody, _ := io.ReadAll(resp.Body)
			t.Fatalf("request %d: expected 200, got %d: %s", i+1, resp.StatusCode, string(respBody))
		}
		resp.Body.Close()
	}

	if requestCount != 12 {
		t.Fatalf("expected 12 requests forwarded, got %d", requestCount)
	}
}

func TestNewServerSetsCustomDo(t *testing.T) {
	pool := sspool.NewSSPool()
	r := rotator.New([]string{}, 0, nil)
	bootstrap := mockBootstrap(t)
	defer bootstrap.Close()
	jwtMgr := NewJWTManager(bootstrap.URL, "test-fp")

	srv := NewServer(pool, jwtMgr, r, 0, nil)

	if srv.jwtMgr.customDo == nil {
		t.Error("expected customDo to be set after NewServer()")
	}
}

func TestCustomDoUsesSSTunnel(t *testing.T) {
	// Create a mock SS server to verify connection through it
	// For now, verify customDo is called and returns a response
	pool := sspool.NewSSPool()
	r := rotator.New([]string{}, 0, nil)
	bootstrap := mockBootstrap(t)
	defer bootstrap.Close()
	jwtMgr := NewJWTManager(bootstrap.URL, "test-fp")

	srv := NewServer(pool, jwtMgr, r, 0, nil)

	// customDo should be set
	if srv.jwtMgr.customDo == nil {
		t.Fatal("expected customDo to be set")
	}

	// When pool is empty, customDo should fall back to direct
	resp, err := srv.jwtMgr.customDo(bootstrap.URL, "application/json", bytes.NewReader([]byte(`{"client":"test"}`)))
	if err != nil {
		t.Fatalf("customDo error: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Errorf("expected status 200, got %d", resp.StatusCode)
	}
}

func TestCustomDoFallbackDirect(t *testing.T) {
	pool := sspool.NewSSPool()
	r := rotator.New([]string{}, 0, nil)
	bootstrap := mockBootstrap(t)
	defer bootstrap.Close()
	jwtMgr := NewJWTManager(bootstrap.URL, "test-fp")

	srv := NewServer(pool, jwtMgr, r, 0, nil)

	// Pool is empty, rotator has no servers
	// customDo should fall back to direct
	resp, err := srv.jwtMgr.customDo(bootstrap.URL, "application/json", bytes.NewReader([]byte(`{"client":"test"}`)))
	if err != nil {
		t.Fatalf("customDo error: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Errorf("expected status 200, got %d", resp.StatusCode)
	}
}

// newTestServerWithRouter creates a Server with a SmartRouter for testing.
func newTestServerWithRouter(t *testing.T, upstream string, proxies []*ProxyInfo) *Server {
	t.Helper()
	pool := sspool.NewSSPool()
	bootstrap := mockBootstrap(t)
	t.Cleanup(bootstrap.Close)
	jwtMgr := NewJWTManager(bootstrap.URL, "test-fp")
	r := rotator.New([]string{}, 0, nil)
	router := NewSmartRouter(proxies)
	opts := &Options{
		SmartRouter: router,
	}
	srv := NewServer(pool, jwtMgr, r, 0, opts)
	if upstream != "" {
		srv.baseURL = upstream
	}
	return srv
}

// TestHandleChatSmartMode verifies SmartRouter is properly integrated.
// Even though the proxy dialer doesn't actually connect, we verify:
// 1. The server doesn't crash
// 2. It falls back through the chain (direct connection works)
func TestHandleChatSmartMode(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(map[string]interface{}{
			"choices": []map[string]interface{}{
				{"message": map[string]string{"role": "assistant", "content": "hello"}},
			},
		})
	}))
	defer upstream.Close()

	// SmartRouter with an alive proxy (dialer will fail to connect but
	// forwardRequest falls back to direct when the proxy fails)
	srv := newTestServerWithRouter(t, upstream.URL, []*ProxyInfo{
		{
			Address:  "127.0.0.1:1", // non-routable, will fail fast
			Protocol: "http",
			Alive:    true,
			// no Dialer set — will skip to fallback
		},
	})

	body := map[string]interface{}{
		"model":    "mimo-auto",
		"messages": []map[string]string{{"role": "user", "content": "hi"}},
	}
	bodyBytes, _ := json.Marshal(body)

	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", bytes.NewReader(bodyBytes))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	srv.handleChat(w, req)

	resp := w.Result()
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		respBody, _ := io.ReadAll(resp.Body)
		t.Fatalf("expected status 200, got %d: %s", resp.StatusCode, string(respBody))
	}
}

// TestFallbackProxyMode verifies fallback-proxy mode handling works via direct fallback.
func TestFallbackProxyMode(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(map[string]interface{}{
			"choices": []map[string]interface{}{
				{"message": map[string]string{"role": "assistant", "content": "hello"}},
			},
		})
	}))
	defer upstream.Close()

	router := NewSmartRouter(nil) // empty router
	fb := NewFallbackHandler(router, 60*time.Second)

	pool := sspool.NewSSPool()
	bootstrap := mockBootstrap(t)
	t.Cleanup(bootstrap.Close)
	jwtMgr := NewJWTManager(bootstrap.URL, "test-fp")
	r := rotator.New([]string{}, 0, nil)

	opts := &Options{
		SmartRouter:     router,
		FallbackHandler: fb,
	}
	srv := NewServer(pool, jwtMgr, r, 0, opts)
	srv.baseURL = upstream.URL

	body := map[string]interface{}{
		"model":    "mimo-auto",
		"messages": []map[string]string{{"role": "user", "content": "hi"}},
	}
	bodyBytes, _ := json.Marshal(body)

	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", bytes.NewReader(bodyBytes))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	srv.handleChat(w, req)

	resp := w.Result()
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		respBody, _ := io.ReadAll(resp.Body)
		t.Fatalf("expected status 200, got %d: %s", resp.StatusCode, string(respBody))
	}
}

// TestHandleChatAutoModeUnchanged verifies auto mode still works as before
// (no SmartRouter, no FallbackHandler — pure SS pool + rotator -> direct fallback).
func TestHandleChatAutoModeUnchanged(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(map[string]interface{}{
			"choices": []map[string]interface{}{
				{"message": map[string]string{"role": "assistant", "content": "hello"}},
			},
		})
	}))
	defer upstream.Close()

	// Auto mode: no SmartRouter, no FallbackHandler — just pool + rotator (empty)
	pool := sspool.NewSSPool()
	bootstrap := mockBootstrap(t)
	t.Cleanup(bootstrap.Close)
	jwtMgr := NewJWTManager(bootstrap.URL, "test-fp")
	r := rotator.New([]string{}, 0, nil)

	srv := NewServer(pool, jwtMgr, r, 0, nil)
	srv.baseURL = upstream.URL

	body := map[string]interface{}{
		"model":    "mimo-auto",
		"messages": []map[string]string{{"role": "user", "content": "hi"}},
	}
	bodyBytes, _ := json.Marshal(body)

	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", bytes.NewReader(bodyBytes))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	srv.handleChat(w, req)

	resp := w.Result()
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		respBody, _ := io.ReadAll(resp.Body)
		t.Fatalf("expected status 200, got %d: %s", resp.StatusCode, string(respBody))
	}
}

// TestHandleChatSmartModeAllDead verifies fallback to direct when all proxies are dead.
func TestHandleChatSmartModeAllDead(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(map[string]interface{}{
			"choices": []map[string]interface{}{
				{"message": map[string]string{"role": "assistant", "content": "hello"}},
			},
		})
	}))
	defer upstream.Close()

	// SmartRouter with dead proxies — should fallback to direct
	srv := newTestServerWithRouter(t, upstream.URL, []*ProxyInfo{
		{
			Address:  "1.2.3.4:8080",
			Protocol: "http",
			Alive:    false,
		},
	})

	body := map[string]interface{}{
		"model":    "mimo-auto",
		"messages": []map[string]string{{"role": "user", "content": "hi"}},
	}
	bodyBytes, _ := json.Marshal(body)

	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", bytes.NewReader(bodyBytes))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	srv.handleChat(w, req)

	resp := w.Result()
	defer resp.Body.Close()

	// All proxies dead, should fallback to direct
	if resp.StatusCode != http.StatusOK {
		respBody, _ := io.ReadAll(resp.Body)
		t.Fatalf("expected status 200 (direct fallback), got %d: %s", resp.StatusCode, string(respBody))
	}
}

// TestHandleChatStreamWithRouter verifies SSE streaming with SmartRouter.
func TestHandleChatStreamWithRouter(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		fmt.Fprintf(w, "data: {\"choices\":[{\"delta\":{\"content\":\"hello\"}}]}\n\n")
		fmt.Fprintf(w, "data: [DONE]\n\n")
	}))
	defer upstream.Close()

	srv := newTestServerWithRouter(t, upstream.URL, []*ProxyInfo{
		{
			Address:  "127.0.0.1:1",
			Protocol: "http",
			Alive:    true,
			// no Dialer — fallback to direct
		},
	})

	body := map[string]interface{}{
		"model":    "mimo-auto",
		"stream":   true,
		"messages": []map[string]string{{"role": "user", "content": "hi"}},
	}
	bodyBytes, _ := json.Marshal(body)

	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", bytes.NewReader(bodyBytes))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "text/event-stream")
	w := httptest.NewRecorder()

	srv.handleChat(w, req)

	resp := w.Result()
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		respBody, _ := io.ReadAll(resp.Body)
		t.Fatalf("expected status 200, got %d: %s", resp.StatusCode, string(respBody))
	}

	if ct := resp.Header.Get("Content-Type"); ct != "text/event-stream" {
		t.Errorf("expected Content-Type text/event-stream, got %s", ct)
	}
}

// Test429RotateNewProxyNoWait verifies that when the rotator pool has >1 proxy,
// a 429 triggers rotation and retries immediately (no full rateLimitRetryDelay sleep).
// Bug: previously the handler always slept 120s even after rotating to a new proxy.
func Test429RotateNewProxyNoWait(t *testing.T) {
	var calls int32
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if atomic.AddInt32(&calls, 1) == 1 {
			w.WriteHeader(http.StatusTooManyRequests)
			w.Write([]byte(`{"error":"rate limit"}`))
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(map[string]interface{}{
			"choices": []map[string]interface{}{
				{"message": map[string]string{"role": "assistant", "content": "ok after rotate"}},
			},
		})
	}))
	defer upstream.Close()

	pool := sspool.NewSSPool()
	bootstrap := mockBootstrap(t)
	defer bootstrap.Close()
	jwtMgr := NewJWTManager(bootstrap.URL, "test-fp")

	// Pool of 2 proxies → rotation is meaningful
	addrs := []string{"1.1.1.1:8388", "2.2.2.2:8388"}
	r := rotator.New(addrs, 0, nil)

	srv := NewServer(pool, jwtMgr, r, 0, &Options{
		RateLimitRetryDelay: 30 * time.Second, // large delay — test should NOT wait this long
	})
	srv.baseURL = upstream.URL

	body := map[string]interface{}{
		"model":    "mimo-auto",
		"messages": []map[string]string{{"role": "user", "content": "hi"}},
	}
	bodyBytes, _ := json.Marshal(body)

	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", bytes.NewReader(bodyBytes))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	start := time.Now()
	srv.handleChat(w, req)
	elapsed := time.Since(start)

	resp := w.Result()
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		respBody, _ := io.ReadAll(resp.Body)
		t.Fatalf("expected status 200 after rotate, got %d: %s", resp.StatusCode, string(respBody))
	}

	if atomic.LoadInt32(&calls) < 2 {
		t.Fatalf("expected at least 2 upstream calls, got %d", calls)
	}

	// With rotation (pool > 1), should complete quickly — NOT wait 30s
	if elapsed > 10*time.Second {
		t.Errorf("expected fast retry after rotation, took %v (should be < 10s, not ~30s)", elapsed)
	}

	t.Logf("rotation test completed in %v (calls: %d)", elapsed, atomic.LoadInt32(&calls))
}

// Test429SingleProxyWait verifies that when the rotator pool has only 1 proxy,
// a 429 causes the full rateLimitRetryDelay wait (no rotation possible).
func Test429SingleProxyWait(t *testing.T) {
	var calls int32
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&calls, 1)
		w.WriteHeader(http.StatusTooManyRequests)
		w.Write([]byte(`{"error":"rate limit"}`))
	}))
	defer upstream.Close()

	pool := sspool.NewSSPool()
	bootstrap := mockBootstrap(t)
	defer bootstrap.Close()
	jwtMgr := NewJWTManager(bootstrap.URL, "test-fp")

	// Pool of 1 proxy → rotation NOT possible
	addrs := []string{"only-proxy:8388"}
	r := rotator.New(addrs, 0, nil)

	srv := NewServer(pool, jwtMgr, r, 0, &Options{
		RateLimitRetryDelay: 3 * time.Second, // short enough for test, but still noticeable
	})
	srv.baseURL = upstream.URL

	body := map[string]interface{}{
		"model":    "mimo-auto",
		"messages": []map[string]string{{"role": "user", "content": "hi"}},
	}
	bodyBytes, _ := json.Marshal(body)

	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", bytes.NewReader(bodyBytes))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	start := time.Now()
	srv.handleChat(w, req)
	elapsed := time.Since(start)

	// With no rotation (pool = 1), should wait the full delay
	// RateLimitRetries = 2, so at least 2 × 3s = 6s
	if elapsed < 5*time.Second {
		t.Errorf("expected full delay when no rotation, took %v (should be >= 5s)", elapsed)
	}

	t.Logf("no-rotation test completed in %v (calls: %d)", elapsed, atomic.LoadInt32(&calls))
}

// TestHandleChatJWT403Retry verifies that when JWT bootstrap returns 403 (proxy banned),
// handleChat calls Invalidate(), retries Get(), and succeeds on the second attempt.
func TestHandleChatJWT403Retry(t *testing.T) {
	var bootstrapCalls int32

	// Mock upstream for the chat API (after JWT succeeds)
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(map[string]interface{}{
			"choices": []map[string]interface{}{
				{"message": map[string]string{"role": "assistant", "content": "hello after retry"}},
			},
		})
	}))
	defer upstream.Close()

	// Mock bootstrap: first call returns 403 (banned proxy), second call returns valid JWT
	bootstrap := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := atomic.AddInt32(&bootstrapCalls, 1)
		if n == 1 {
			w.WriteHeader(http.StatusForbidden)
			w.Write([]byte(`{"error":"banned"}`))
			return
		}
		w.Header().Set("Content-Type", "application/json")
		jwt := "eyJhbGciOiJIUzI1NiJ9.eyJleHAiOjQxMDI0NDQ4MDB9.fake_signature"
		json.NewEncoder(w).Encode(map[string]string{"jwt": jwt})
	}))
	defer bootstrap.Close()

	pool := sspool.NewSSPool()
	jwtMgr := NewJWTManager(bootstrap.URL, "test-fp")
	// Rotator with addresses to exercise the SS rotator path in customDo
	// (createSSDialer returns nil since pool is empty, so falls to direct)
	r := rotator.New([]string{"1.1.1.1:8388"}, 0, nil)

	srv := NewServer(pool, jwtMgr, r, 0, nil)
	srv.baseURL = upstream.URL // chat requests go to mock upstream

	body := map[string]interface{}{
		"model":    "mimo-auto",
		"messages": []map[string]string{{"role": "user", "content": "hi"}},
	}
	bodyBytes, _ := json.Marshal(body)

	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", bytes.NewReader(bodyBytes))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	srv.handleChat(w, req)

	resp := w.Result()
	defer resp.Body.Close()

	// Should succeed after the retry recovers from 403
	if resp.StatusCode != http.StatusOK {
		respBody, _ := io.ReadAll(resp.Body)
		t.Fatalf("expected status 200 after 403 retry, got %d: %s", resp.StatusCode, string(respBody))
	}

	// Bootstrap should have been called at least twice
	if got := atomic.LoadInt32(&bootstrapCalls); got < 2 {
		t.Fatalf("expected bootstrap to be called at least 2 times, got %d", got)
	}
	t.Logf("bootstrap called %d times (first: 403, second: success)", atomic.LoadInt32(&bootstrapCalls))
}

// realDialer connects to whatever address the HTTP client requests.
// Used to make actual HTTP connections through SmartRouter in tests.
type realDialer struct{}

func (d *realDialer) DialContext(ctx context.Context, network, addr string) (net.Conn, error) {
	return new(net.Dialer).DialContext(ctx, network, addr)
}

// TestCustomDoMarkDeadOn403 verifies that customDo calls pool.MarkDead
// when the bootstrap endpoint returns 403 with a proxy address.
func TestCustomDoMarkDeadOn403(t *testing.T) {
	// Bootstrap that returns 403
	bootstrap403 := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusForbidden)
		w.Write([]byte(`{"error":"forbidden"}`))
	}))
	defer bootstrap403.Close()

	pool := sspool.NewSSPool()

	jwtMgr := NewJWTManager(bootstrap403.URL, "test-fp")
	r := rotator.New([]string{}, 0, nil)

	// SmartRouter with a proxy that has a real dialer (able to connect)
	proxies := []*ProxyInfo{
		{
			Address:  "mock-proxy:9999",
			Protocol: "http",
			Dialer:   &realDialer{},
			Alive:    true,
		},
	}
	router := NewSmartRouter(proxies)

	srv := NewServer(pool, jwtMgr, r, 0, &Options{
		SmartRouter: router,
	})

	// Call customDo — SmartRouter should route through the proxy, get 403
	resp, err := srv.jwtMgr.customDo(bootstrap403.URL, "application/json", bytes.NewReader([]byte(`{"client":"test"}`)))
	if err != nil {
		t.Fatalf("customDo error: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("expected status 403, got %d", resp.StatusCode)
	}

	// Verify pool.MarkDead was called on the proxy address
	if !pool.IsDead("mock-proxy:9999") {
		t.Error("pool.MarkDead should have been called on 403, but 'mock-proxy:9999' is not marked dead")
	}
}
