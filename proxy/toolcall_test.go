package proxy

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
)

// TestHandleChatToolCallsPreserved simulates real OpenCode payload:
// user -> assistant(tool_calls) -> tool(tool_call_id).
// ALL per-message fields (tool_calls, tool_call_id) MUST survive the proxy.
func TestHandleChatToolCallsPreserved(t *testing.T) {
	var capturedBody []byte
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		capturedBody, _ = io.ReadAll(r.Body)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(map[string]interface{}{
			"choices": []map[string]interface{}{
				{"message": map[string]string{"role": "assistant", "content": "done"}},
			},
		})
	}))
	defer upstream.Close()

	srv := newTestServer(t, upstream.URL)

	reqBody := `{
		"model": "mimo-auto",
		"messages": [
			{"role": "user", "content": [{"type": "text", "text": "get weather"}]},
			{"role": "assistant", "content": null, "tool_calls": [{"id": "call_abc123", "type": "function", "function": {"name": "get_weather", "arguments": "{}"}}]},
			{"role": "tool", "tool_call_id": "call_abc123", "content": "sunny, 25C"}
		],
		"tools": [{"type": "function", "function": {"name": "get_weather", "parameters": {}}}],
		"temperature": 0.7,
		"stream": false
	}`

	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", bytes.NewReader([]byte(reqBody)))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	srv.handleChat(w, req)

	resp := w.Result()
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		respBody, _ := io.ReadAll(resp.Body)
		t.Fatalf("expected 200, got %d: %s", resp.StatusCode, string(respBody))
	}

	var forwarded map[string]interface{}
	if err := json.Unmarshal(capturedBody, &forwarded); err != nil {
		t.Fatalf("forwarded body not valid JSON: %v", err)
	}

	messages := forwarded["messages"].([]interface{})

	// Position 0: system (injected)
	sysMsg := messages[0].(map[string]interface{})
	if sysMsg["role"] != "system" {
		t.Fatalf("pos 0: expected role=system, got %v", sysMsg["role"])
	}

	// Position 1: user with array content
	userMsg := messages[1].(map[string]interface{})
	contentArr := userMsg["content"].([]interface{})
	if len(contentArr) != 1 {
		t.Fatalf("pos 1: expected content array len 1, got %d", len(contentArr))
	}

	// Position 2: assistant with tool_calls — MUST preserve
	assistantMsg := messages[2].(map[string]interface{})
	if assistantMsg["content"] != nil {
		t.Fatalf("pos 2: expected content null, got %v", assistantMsg["content"])
	}
	toolCalls := assistantMsg["tool_calls"].([]interface{})
	if len(toolCalls) != 1 {
		t.Fatalf("pos 2: expected 1 tool_call, got %d", len(toolCalls))
	}
	tc := toolCalls[0].(map[string]interface{})
	if tc["id"] != "call_abc123" {
		t.Fatalf("pos 2: expected tool_call id=call_abc123, got %v", tc["id"])
	}

	// Position 3: tool result with tool_call_id — MUST preserve
	toolMsg := messages[3].(map[string]interface{})
	if toolMsg["role"] != "tool" {
		t.Fatalf("pos 3: expected role=tool, got %v", toolMsg["role"])
	}
	if toolMsg["tool_call_id"] != "call_abc123" {
		t.Fatalf("pos 3: expected tool_call_id=call_abc123, got %v", toolMsg["tool_call_id"])
	}

	// Top-level extra fields preserved
	tools := forwarded["tools"].([]interface{})
	if len(tools) != 1 {
		t.Fatalf("expected tools preserved, got %v", forwarded["tools"])
	}
	temp := forwarded["temperature"].(float64)
	if temp != 0.7 {
		t.Fatalf("expected temperature=0.7, got %v", temp)
	}
}
