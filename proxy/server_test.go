package proxy

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
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
