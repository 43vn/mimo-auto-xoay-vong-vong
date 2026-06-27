package proxy

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// helper to create a mock HTTP response with SSE body
func mockSSEResponse(body string) *http.Response {
	return &http.Response{
		StatusCode: http.StatusOK,
		Body:       io.NopCloser(strings.NewReader(body)),
		Header:     make(http.Header),
	}
}

// TestStreamSSEWithPreCheck_Clean verifies pre-check passes for clean SSE
func TestStreamSSEWithPreCheck_Clean(t *testing.T) {
	body := "data: {\"content\": \"hello world\"}\n\ndata: [DONE]\n\n"
	resp := mockSSEResponse(body)

	result, err := streamSSEWithPreCheck(resp, 5)
	if err != nil {
		t.Fatalf("pre-check error: %v", err)
	}
	if !result.Clean {
		t.Fatal("expected clean pre-check")
	}

	// Stream the rest
	w := httptest.NewRecorder()
	err = streamSSEFromScanner(result.Scanner, w, 5*time.Second, "test", 0)
	if err != nil {
		t.Fatalf("stream error: %v", err)
	}

	output := w.Body.String()
	if !strings.Contains(output, "hello world") {
		t.Errorf("expected 'hello world' in output, got: %s", output)
	}
	if !strings.Contains(output, "[DONE]") {
		t.Errorf("expected [DONE] in output, got: %s", output)
	}
}

// TestStreamSSEWithPreCheck_ComplianceBlock verifies pre-check catches compliance block
func TestStreamSSEWithPreCheck_ComplianceBlock(t *testing.T) {
	body := "data: {\"error\":{\"code\":\"441\",\"message\":\"Detected high-frequency non-compliant requests\"}}\n\ndata: [DONE]\n\n"
	resp := mockSSEResponse(body)

	result, err := streamSSEWithPreCheck(resp, 5)
	if err != nil {
		t.Fatalf("pre-check error: %v", err)
	}
	if result.Clean {
		t.Fatal("expected compliance block to be detected")
	}
}

// TestStreamSSEFromScanner_MidStreamComplianceBlock verifies mid-stream compliance sends [DONE]
func TestStreamSSEFromScanner_MidStreamComplianceBlock(t *testing.T) {
	// First 5 lines clean, then compliance block on line 6
	body := "data: {\"content\": \"a\"}\n\ndata: {\"content\": \"b\"}\n\ndata: {\"content\": \"c\"}\n\ndata: {\"content\": \"d\"}\n\ndata: {\"content\": \"e\"}\n\ndata: {\"error\":{\"message\":\"Detected high-frequency non-compliant requests\"}}\n\ndata: [DONE]\n\n"
	resp := mockSSEResponse(body)

	// Pre-check passes (first 5 lines clean)
	result, err := streamSSEWithPreCheck(resp, 5)
	if err != nil {
		t.Fatalf("pre-check error: %v", err)
	}
	if !result.Clean {
		t.Fatal("expected clean pre-check")
	}

	// Stream — should detect compliance block mid-stream
	w := httptest.NewRecorder()
	err = streamSSEFromScanner(result.Scanner, w, 5*time.Second, "test", 0)
	if err != ErrComplianceBlock {
		t.Fatalf("expected ErrComplianceBlock, got: %v", err)
	}

	output := w.Body.String()
	// Should contain [DONE] (clean end for client)
	if !strings.Contains(output, "[DONE]") {
		t.Errorf("expected [DONE] in output, got: %s", output)
	}
	// Should NOT contain compliance text (not forwarded to client)
	if strings.Contains(output, "high-frequency") {
		t.Errorf("compliance text should NOT be forwarded to client, got: %s", output)
	}
}

// TestStreamSSEWithPreCheck_ShortStream verifies pre-check works with < 5 lines
func TestStreamSSEWithPreCheck_ShortStream(t *testing.T) {
	body := "data: {\"content\": \"short\"}\n\ndata: [DONE]\n\n"
	resp := mockSSEResponse(body)

	result, err := streamSSEWithPreCheck(resp, 5)
	if err != nil {
		t.Fatalf("pre-check error: %v", err)
	}
	if !result.Clean {
		t.Fatal("expected clean pre-check")
	}

	// Stream remaining — should get [DONE]
	w := httptest.NewRecorder()
	err = streamSSEFromScanner(result.Scanner, w, 5*time.Second, "test", 0)
	if err != nil {
		t.Fatalf("stream error: %v", err)
	}
	output := w.Body.String()
	if !strings.Contains(output, "[DONE]") {
		t.Errorf("expected [DONE] in output, got: %s", output)
	}
}

// TestStreamSSEWithPreCheck_ShortStreamComplianceBlock verifies short stream with compliance block
func TestStreamSSEWithPreCheck_ShortStreamComplianceBlock(t *testing.T) {
	body := "data: {\"error\":{\"message\":\"Detected high-frequency non-compliant\"}}\n\n"
	resp := mockSSEResponse(body)

	result, err := streamSSEWithPreCheck(resp, 5)
	if err != nil {
		t.Fatalf("pre-check error: %v", err)
	}
	if result.Clean {
		t.Fatal("expected compliance block to be detected in short stream")
	}
}
