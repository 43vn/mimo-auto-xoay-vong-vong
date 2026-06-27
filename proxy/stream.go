package proxy

import (
	"bufio"
	"bytes"
	"fmt"
	"io"
	"log"
	"net/http"
	"regexp"
	"strings"
	"time"
)

// contentPattern matches "content" : "..." in JSON lines.
var contentPattern = regexp.MustCompile(`"content"\s*:\s*"((?:[^"\\]|\\.)*)"`)

// PreCheckResult holds the result of streamSSEWithPreCheck.
type PreCheckResult struct {
	Scanner     *bufio.Scanner // continue streaming from remaining data
	Clean       bool           // true if pre-check passed
	BlockReason string         // "compliance", "auth", or "" if clean
}

// streamSSEWithPreCheck reads from upstream body, checks the first checkLines
// for compliance block. Returns a scanner positioned after the checked lines.
func streamSSEWithPreCheck(resp *http.Response, checkLines int) (*PreCheckResult, error) {
	// Read all data from body, split into lines
	allData, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("upstream read error: %w", err)
	}

	// Split into lines preserving newlines
	allLines := bytes.Split(allData, []byte("\n"))

	// Check first checkLines (or fewer if short stream)
	checkedCount := checkLines
	if checkedCount > len(allLines) {
		checkedCount = len(allLines)
	}

	for i := 0; i < checkedCount; i++ {
		lineWithNL := append(allLines[i], '\n')
		if detectComplianceBlock(lineWithNL) {
			log.Printf("[COMPLIANCE] pre-check line %d compliance block detected", i+1)
			return &PreCheckResult{Clean: false, BlockReason: "compliance"}, nil
		}
		if detectAuthError(lineWithNL) {
			log.Printf("[AUTH] pre-check line %d auth error detected (Invalid Token)", i+1)
			return &PreCheckResult{Clean: false, BlockReason: "auth"}, nil
		}
	}

	// Build remaining data: ALL lines (checked lines are clean, pass them through)
	var remaining []byte
	for i := 0; i < len(allLines); i++ {
		remaining = append(remaining, allLines[i]...)
		if i < len(allLines)-1 {
			remaining = append(remaining, '\n')
		}
	}
	// Preserve trailing newline if original had it
	if bytes.HasSuffix(allData, []byte("\n")) && !bytes.HasSuffix(remaining, []byte("\n")) {
		remaining = append(remaining, '\n')
	}

	scanner := bufio.NewScanner(bytes.NewReader(remaining))
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)

	return &PreCheckResult{
		Scanner: scanner,
		Clean:   true,
	}, nil
}

// streamSSEFromScanner continues streaming from an existing scanner.
// Mid-stream compliance block → writes [DONE] to client (clean end).
func streamSSEFromScanner(scanner *bufio.Scanner, w http.ResponseWriter, thinkingTimeout time.Duration, proxyInfo string, initialLines int) error {
	thinkingStart := time.Now()
	hasContent := false
	totalLines := initialLines

	if proxyInfo != "" {
		log.Printf("[SSE] stream resumed (proxy: %s)", proxyInfo)
	} else {
		log.Printf("[SSE] stream resumed (direct connection)")
	}

	for scanner.Scan() {
		line := scanner.Bytes()
		lineWithNL := append(line, '\n')
		totalLines++

		// Mid-stream check: compliance block → blacklist, auth error → [DONE] + refresh
		if detectComplianceBlock(lineWithNL) {
			log.Printf("[COMPLIANCE] mid-stream compliance block at line %d (proxy: %s)", totalLines, proxyInfo)
			sendDone(w)
			return ErrComplianceBlock
		}
		if detectAuthError(lineWithNL) {
			log.Printf("[AUTH] mid-stream auth error at line %d (proxy: %s)", totalLines, proxyInfo)
			sendDone(w)
			return ErrAuthError
		}

		if !hasContent {
			if m := contentPattern.FindSubmatch(lineWithNL); m != nil {
				matchStart := bytes.Index(lineWithNL, m[0])
				if matchStart >= 10 && string(lineWithNL[matchStart-10:matchStart]) == "reasoning_" {
					// skip
				} else {
					val := m[1]
					if len(val) > 0 && string(val) != "null" {
						hasContent = true
					}
				}
			}
		}

		if _, err := w.Write(lineWithNL); err != nil {
			if proxyInfo != "" {
				log.Printf("[SSE] client write error after %d lines: %v (proxy: %s)", totalLines, err, proxyInfo)
			} else {
				log.Printf("[SSE] client write error after %d lines: %v", totalLines, err)
			}
			return err
		}

		if !hasContent && time.Since(thinkingStart) > thinkingTimeout {
			log.Printf("[SSE] thinking timeout after %d lines (%v), sending [DONE]", totalLines, time.Since(thinkingStart))
			sendDone(w)
			return nil
		}
	}

	// Stream ended
	if err := scanner.Err(); err != nil && err != io.EOF {
		if proxyInfo != "" {
			log.Printf("[SSE] upstream read error after %d lines (content seen: %v, proxy: %s): %v", totalLines, hasContent, proxyInfo, err)
		} else {
			log.Printf("[SSE] upstream read error after %d lines (content seen: %v): %v", totalLines, hasContent, err)
		}
	} else {
		if proxyInfo != "" {
			log.Printf("[SSE] upstream EOF after %d lines (content seen: %v, elapsed: %v, proxy: %s)", totalLines, hasContent, time.Since(thinkingStart), proxyInfo)
		} else {
			log.Printf("[SSE] upstream EOF after %d lines (content seen: %v, elapsed: %v)", totalLines, hasContent, time.Since(thinkingStart))
		}
	}
	sendDone(w)
	return nil
}

// sendDone writes a final [DONE] SSE event and flushes.
func sendDone(w http.ResponseWriter) {
	w.Write([]byte("\ndata: [DONE]\n\n"))
	if flusher, ok := w.(http.Flusher); ok {
		flusher.Flush()
	}
}

// isStreamTimeoutError checks if an error is a context/deadline timeout.
func isStreamTimeoutError(err error) bool {
	if err == nil {
		return false
	}
	msg := err.Error()
	return strings.Contains(msg, "context deadline exceeded") ||
		strings.Contains(msg, "Client.Timeout") ||
		strings.Contains(msg, "context cancellation")
}
