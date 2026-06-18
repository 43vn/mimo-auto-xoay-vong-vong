package proxy

import (
	"bufio"
	"bytes"
	"io"
	"log"
	"net/http"
	"regexp"
	"strings"
	"time"
)

// contentPattern matches "content" : "..." in JSON lines.
var contentPattern = regexp.MustCompile(`"content"\s*:\s*"((?:[^"\\]|\\.)*)"`)

// streamSSE proxies SSE chunks from upstream to client with thinking timeout.
// If no content appears within thinkingTimeout, the stream is aborted with [DONE].
// ALWAYS sends [DONE] on ANY stream end (EOF, error, timeout) so the client
// never hangs waiting for more data.
// proxyInfo is optional — if non-empty, it is logged with SSE events for debugging.
func streamSSE(resp *http.Response, w http.ResponseWriter, thinkingTimeout time.Duration, proxyInfo string) error {
	thinkingStart := time.Now()
	hasContent := false
	totalLines := 0

	if proxyInfo != "" {
		log.Printf("[SSE] stream started (proxy: %s)", proxyInfo)
	} else {
		log.Printf("[SSE] stream started (direct connection)")
	}

	scanner := bufio.NewScanner(resp.Body)
	// Increase buffer size for large SSE lines
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)

	for scanner.Scan() {
		line := scanner.Bytes()
		// Re-add the newline that Scanner strips
		lineWithNL := append(line, '\n')

		totalLines++

		if !hasContent {
			if m := contentPattern.FindSubmatch(lineWithNL); m != nil {
				// Skip if preceded by "reasoning_" (e.g. "reasoning_content" field)
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
			// Client disconnected — can't send [DONE], just return
			if proxyInfo != "" {
				log.Printf("[SSE] client write error after %d lines: %v (proxy: %s)", totalLines, err, proxyInfo)
			} else {
				log.Printf("[SSE] client write error after %d lines: %v", totalLines, err)
			}
			return err
		}

		// Check for thinking timeout only if we haven't seen content yet
		if !hasContent && time.Since(thinkingStart) > thinkingTimeout {
			log.Printf("[SSE] thinking timeout after %d lines (%v), sending [DONE]", totalLines, time.Since(thinkingStart))
			sendDone(w)
			return nil
		}
	}

	// Stream ended — ALWAYS send [DONE] for clean client termination.
	// Without this, client hangs forever waiting for more data.
	if err := scanner.Err(); err != nil && err != io.EOF {
		// Upstream connection dropped or timed out mid-stream.
		if proxyInfo != "" {
			log.Printf("[SSE] upstream read error after %d lines (content seen: %v, proxy: %s): %v", totalLines, hasContent, proxyInfo, err)
		} else {
			log.Printf("[SSE] upstream read error after %d lines (content seen: %v): %v", totalLines, hasContent, err)
		}
	} else {
		// Normal EOF — upstream closed connection (possibly without sending [DONE])
		if proxyInfo != "" {
			log.Printf("[SSE] upstream EOF after %d lines (content seen: %v, elapsed: %v, proxy: %s)", totalLines, hasContent, time.Since(thinkingStart), proxyInfo)
		} else {
			log.Printf("[SSE] upstream EOF after %d lines (content seen: %v, elapsed: %v)", totalLines, hasContent, time.Since(thinkingStart))
		}
	}
	sendDone(w)
	return nil
}

// sendDone writes a final [DONE] SSE event and flushes. Best-effort — errors are logged.
func sendDone(w http.ResponseWriter) {
	w.Write([]byte("\ndata: [DONE]\n\n"))
	if flusher, ok := w.(http.Flusher); ok {
		flusher.Flush()
	}
}

// isStreamTimeoutError checks if an error is a context/deadline timeout during body reads.
func isStreamTimeoutError(err error) bool {
	if err == nil {
		return false
	}
	msg := err.Error()
	return strings.Contains(msg, "context deadline exceeded") ||
		strings.Contains(msg, "Client.Timeout") ||
		strings.Contains(msg, "context cancellation")
}
