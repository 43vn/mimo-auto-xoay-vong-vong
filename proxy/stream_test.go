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

// TestStreamSSE_ReasoningContentSkip verifies that reasoning_content field is skipped
func TestStreamSSE_ReasoningContentSkip(t *testing.T) {
	// SSE line with reasoning_content field
	body := "data: {\"reasoning_content\": \"some reasoning text\"}\n\n"
	resp := mockSSEResponse(body)
	w := httptest.NewRecorder()

	// Use a short thinking timeout that will expire before content detection
	err := streamSSE(resp, w, 10*time.Millisecond, "test")
	if err != nil {
		t.Fatalf("streamSSE returned error: %v", err)
	}

	// The output should contain the original line
	output := w.Body.String()
	if !strings.Contains(output, "reasoning_content") {
		t.Errorf("expected output to contain reasoning_content, got: %s", output)
	}

	// Verify that hasContent stayed false by checking that [DONE] was sent
	// (thinking timeout triggered because no content was detected)
	if !strings.Contains(output, "[DONE]") {
		t.Errorf("expected [DONE] in output due to thinking timeout, got: %s", output)
	}
}

// TestStreamSSE_ContentDetected verifies that actual content sets hasContent=true
func TestStreamSSE_ContentDetected(t *testing.T) {
	// SSE line with content field
	body := "data: {\"content\": \"hello world\"}\n\n"
	resp := mockSSEResponse(body)
	w := httptest.NewRecorder()

	// Use a long thinking timeout - should not trigger because content is detected
	err := streamSSE(resp, w, 5*time.Second, "test")
	if err != nil {
		t.Fatalf("streamSSE returned error: %v", err)
	}

	// The output should contain the original line
	output := w.Body.String()
	if !strings.Contains(output, "content") {
		t.Errorf("expected output to contain content, got: %s", output)
	}

	// Verify that [DONE] was sent at the end of stream
	if !strings.Contains(output, "[DONE]") {
		t.Errorf("expected [DONE] in output, got: %s", output)
	}
}

// TestStreamSSE_EmptyContent verifies that null/empty content does NOT set hasContent
func TestStreamSSE_EmptyContent(t *testing.T) {
	// Test cases: null content and empty string content
	testCases := []struct {
		name string
		body string
	}{
		{
			name: "null content",
			body: "data: {\"content\": null}\n\n",
		},
		{
			name: "empty string content",
			body: "data: {\"content\": \"\"}\n\n",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			resp := mockSSEResponse(tc.body)
			w := httptest.NewRecorder()

			// Use a short thinking timeout - should trigger because content is empty/null
			err := streamSSE(resp, w, 10*time.Millisecond, "test")
			if err != nil {
				t.Fatalf("streamSSE returned error: %v", err)
			}

			// The output should contain the original line
			output := w.Body.String()
			if !strings.Contains(output, "content") {
				t.Errorf("expected output to contain content, got: %s", output)
			}

			// Verify that [DONE] was sent due to thinking timeout
			// (hasContent stayed false because content was null/empty)
			if !strings.Contains(output, "[DONE]") {
				t.Errorf("expected [DONE] in output due to thinking timeout, got: %s", output)
			}
		})
	}
}