package proxy

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"
)

// ErrComplianceBlock is returned when a stream contains a compliance block.
var ErrComplianceBlock = fmt.Errorf("compliance block detected in stream")

// ErrAuthError is returned when a stream contains an auth error (Invalid Token).
var ErrAuthError = fmt.Errorf("auth error detected in stream")

// compliancePatterns are the key phrases to detect in responses and prompts.
var compliancePatterns = []string{
	"high-frequency non-compliant",
	"Detected high-frequency",
	"comply with the platform usage agreement",
}

// detectComplianceBlock checks if the response body contains the
// "high-frequency non-compliant" detection message from upstream.
// Returns true if this is a soft-block response that should trigger proxy rotation.
func detectComplianceBlock(body []byte) bool {
	if len(body) == 0 {
		return false
	}
	lowerStr := strings.ToLower(string(body))
	for _, p := range compliancePatterns {
		if strings.Contains(lowerStr, strings.ToLower(p)) {
			return true
		}
	}
	return false
}

// authErrorPatterns are the key phrases to detect auth errors in SSE responses.
var authErrorPatterns = []string{
	"invalid token",
	"invalid_token",
	"illegal access",
	"illegal_access",
	"unauthorized",
}

// detectAuthError checks if the body contains an auth error (Invalid Token, 401).
// Returns true if this is an auth error that should trigger JWT refresh + retry.
func detectAuthError(body []byte) bool {
	if len(body) == 0 {
		return false
	}
	lowerStr := strings.ToLower(string(body))
	for _, p := range authErrorPatterns {
		if strings.Contains(lowerStr, p) {
			return true
		}
	}
	return false
}

// stripComplianceBlock removes any compliance block text from user prompt content
// to avoid triggering upstream detectors. Returns the cleaned JSON body.
// If no compliance text is found, the original body is returned unchanged.
func stripComplianceBlock(body []byte) ([]byte, error) {
	if len(body) == 0 || !detectComplianceBlock(body) {
		return body, nil
	}

	// Parse as raw map to preserve all fields
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(body, &raw); err != nil {
		return body, nil // parse error — return as-is
	}

	msgsRaw, ok := raw["messages"]
	if !ok {
		return body, nil
	}

	var messages []json.RawMessage
	if err := json.Unmarshal(msgsRaw, &messages); err != nil {
		return body, nil
	}

	// Scan each message's content field for compliance text
	changed := false
	cleanedMessages := make([]json.RawMessage, 0, len(messages))
	for _, msg := range messages {
		var msgMap map[string]json.RawMessage
		if err := json.Unmarshal(msg, &msgMap); err != nil {
			cleanedMessages = append(cleanedMessages, msg)
			continue
		}
		contentRaw, ok := msgMap["content"]
		if !ok {
			cleanedMessages = append(cleanedMessages, msg)
			continue
		}
		var content string
		if err := json.Unmarshal(contentRaw, &content); err != nil {
			cleanedMessages = append(cleanedMessages, msg)
			continue
		}
		if !detectComplianceBlock([]byte(content)) {
			cleanedMessages = append(cleanedMessages, msg)
			continue
		}

		// Strip the compliance block lines from content
		changed = true
		lines := strings.Split(content, "\n")
		var kept []string
		for _, line := range lines {
			if !detectComplianceBlock([]byte(line)) {
				kept = append(kept, line)
			}
		}
		remaining := strings.TrimSpace(strings.Join(kept, "\n"))
		if remaining == "" {
			// Content was ONLY compliance text — remove entire message from array
			continue
		}
		// Has remaining content — update message with cleaned content
		newContentRaw, _ := json.Marshal(remaining)
		msgMap["content"] = newContentRaw
		newMsg, _ := json.Marshal(msgMap)
		cleanedMessages = append(cleanedMessages, newMsg)
	}

	if !changed {
		return body, nil
	}

	// If all messages removed, return original (don't send empty array)
	if len(cleanedMessages) == 0 {
		return body, nil
	}

	// Rebuild messages array
	var buf bytes.Buffer
	buf.WriteByte('[')
	for i, msg := range cleanedMessages {
		if i > 0 {
			buf.WriteByte(',')
		}
		buf.Write(msg)
	}
	buf.WriteByte(']')
	raw["messages"] = buf.Bytes()

	return json.Marshal(raw)
}
