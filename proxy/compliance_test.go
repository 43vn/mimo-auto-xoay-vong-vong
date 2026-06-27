package proxy

import (
	"encoding/json"
	"testing"
)

func TestDetectComplianceBlock(t *testing.T) {
	tests := []struct {
		name     string
		body     string
		expected bool
	}{
		{
			name:     "exact match - full message",
			body:     "Detected high-frequency non-compliant requests from you. Please consciously comply with the platform usage agreement. If you need to appeal, contact us through the official website channels.",
			expected: true,
		},
		{
			name:     "partial match - key phrase",
			body:     "Error: Detected high-frequency non-compliant requests",
			expected: true,
		},
		{
			name:     "match - compliance phrase",
			body:     "Please comply with the platform usage agreement",
			expected: true,
		},
		{
			name:     "no match - normal response",
			body:     `{"id":"chatcmpl-123","choices":[{"message":{"role":"assistant","content":"Hello!"}}]}`,
			expected: false,
		},
		{
			name:     "no match - empty body",
			body:     "",
			expected: false,
		},
		{
			name:     "no match - unrelated error",
			body:     `{"error":"invalid request"}`,
			expected: false,
		},
		{
			name:     "case insensitive check",
			body:     "detected high-frequency non-compliant requests from you",
			expected: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := detectComplianceBlock([]byte(tt.body))
			if got != tt.expected {
				t.Errorf("detectComplianceBlock() = %v, want %v", got, tt.expected)
			}
		})
	}
}

func TestStripComplianceBlock(t *testing.T) {
	tests := []struct {
		name            string
		body            string
		expectChanged   bool
		expectClean     bool
	}{
		{
			name: "removes message that is only compliance text",
			body: `{"messages":[{"role":"user","content":"Detected high-frequency non-compliant requests from you."}]}`,
			expectChanged: false,
			expectClean:   false,
		},
		{
			name: "preserves normal messages",
			body: `{"messages":[{"role":"user","content":"Hello, how are you?"}]}`,
			expectChanged: false,
			expectClean:   true,
		},
		{
			name: "strips only compliance lines, keeps other content",
			body: `{"messages":[{"role":"user","content":"Hello\nDetected high-frequency non-compliant requests from you.\nHow are you?"}]}`,
			expectChanged: true,
			expectClean:   true,
		},
		{
			name: "returns original on empty body",
			body: "",
			expectChanged: false,
			expectClean:   true,
		},
		{
			name: "returns original on no messages",
			body: `{"model":"gpt-4"}`,
			expectChanged: false,
			expectClean:   true,
		},
		{
			name: "removes compliance message, keeps other messages",
			body: `{"messages":[{"role":"user","content":"Detected high-frequency non-compliant requests from you."},{"role":"assistant","content":"Hello!"},{"role":"user","content":"How are you?"}]}`,
			expectChanged: true,
			expectClean:   true,
		},
		{
			name: "returns original if all messages are compliance only",
			body: `{"messages":[{"role":"user","content":"Detected high-frequency non-compliant requests from you."}]}`,
			expectChanged: false,
			expectClean:   false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := stripComplianceBlock([]byte(tt.body))
			if err != nil {
				t.Fatalf("stripComplianceBlock() error: %v", err)
			}

			if tt.expectClean && detectComplianceBlock(result) {
				t.Errorf("expected clean output, but compliance block still detected")
			}

			if !tt.expectChanged && string(result) != tt.body {
				t.Errorf("expected unchanged output, but body was modified")
			}

			// Verify JSON is still valid
			if len(result) > 0 {
				var raw map[string]json.RawMessage
				if err := json.Unmarshal(result, &raw); err != nil {
					t.Errorf("output is not valid JSON: %v", err)
				}
			}
		})
	}
}
