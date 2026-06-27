package proxy

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"strings"
	"time"

	"github.com/vincent/mimo-xoay/rotator"
	"github.com/vincent/mimo-xoay/sspool"
)

const (
	BaseURL      = "https://api.xiaomimimo.com"
	BootstrapURL = BaseURL + "/api/free-ai/bootstrap"

	MaxConcurrent      = 8
	ProxyTimeout       = 60 * time.Second  // total timeout for non-streaming requests
	ThinkingTimeout    = 120 * time.Second // abort SSE if no content within this duration
	TimeoutRetries     = 5
	JWTRefreshRetries  = 5
	RateLimitMax       = 8
	RateLimitWindow    = 60 * time.Second
	RateLimitMinInt    = 2 * time.Second
	RateLimitRetries   = 2
	RateLimitRetryDelay = 120 * time.Second

	// Streaming-specific timeouts (applied at transport level, NOT Client.Timeout)
	StreamHeaderTimeout  = 30 * time.Second  // max wait for upstream response headers
	StreamDialTimeout    = 15 * time.Second  // max wait for TCP+SS tunnel establishment
	StreamTLSTimeout     = 10 * time.Second  // max wait for TLS handshake
	StreamIdleTimeout    = 300 * time.Second // close idle streaming connections after this
	StreamIdleReadTimeout = 5 * time.Minute  // kill stream if no data from upstream for this long
)

// Options configures the Server.
type Options struct {
	DisableRateLimit    bool              // skip local rate limiter; rely on upstream only
	MinInterval         time.Duration     // minimum interval between local requests (0 = disabled)
	ProxyTimeout        time.Duration     // total timeout for non-streaming requests (0 = default)
	RateLimitRetryDelay time.Duration     // delay between 429 retry attempts (0 = default 120s)
	SmartRouter         *SmartRouter      // for custom/fallback-proxy modes (may be nil)
	FallbackHandler     *FallbackHandler  // for fallback-proxy mode (may be nil)
	BlacklistMgr        *BlacklistManager // persistent blacklist manager (may be nil)
}

// Server is the HTTP proxy server that forwards requests to the upstream API.
type Server struct {
	pool                *sspool.SSPool
	jwtMgr              *JWTManager
	rateLimiter         *RateLimiter
	rotator             *rotator.Rotator
	blacklistMgr        *BlacklistManager
	semaphore           chan struct{}
	fingerprint         string
	port                int
	baseURL             string // override for testing
	httpServer          *http.Server
	proxyTimeout        time.Duration
	rateLimitRetryDelay time.Duration
	smartRouter         *SmartRouter     // for custom/fallback-proxy modes
	fallbackHandler     *FallbackHandler // for fallback-proxy mode
}

// NewServer creates a new proxy Server.
func NewServer(pool *sspool.SSPool, jwtMgr *JWTManager, r *rotator.Rotator, port int, opts *Options) *Server {
	if opts == nil {
		opts = &Options{}
	}
	minInt := opts.MinInterval
	proxyTimeout := opts.ProxyTimeout
	rateLimitRetryDelay := opts.RateLimitRetryDelay

	fp := generateFingerprint()
	s := &Server{
		pool:                pool,
		jwtMgr:              jwtMgr,
		rotator:             r,
		semaphore:           make(chan struct{}, MaxConcurrent),
		fingerprint:         fp,
		port:                port,
		baseURL:             BaseURL,
		proxyTimeout:        proxyTimeout,
		rateLimitRetryDelay: rateLimitRetryDelay,
		smartRouter:         opts.SmartRouter,
		fallbackHandler:     opts.FallbackHandler,
		blacklistMgr:        opts.BlacklistMgr,
	}
	if opts.DisableRateLimit {
		s.rateLimiter = nil
	} else {
		s.rateLimiter = NewRateLimiter(RateLimitMax, RateLimitWindow, minInt)
	}
	if s.proxyTimeout <= 0 {
		s.proxyTimeout = ProxyTimeout
	}
	if s.rateLimitRetryDelay <= 0 {
		s.rateLimitRetryDelay = RateLimitRetryDelay
	}
	// Initialize httpServer here so the field is set before any goroutine reads it.
	mux := http.NewServeMux()
	mux.HandleFunc("/v1/chat/completions", s.handleChat)
	mux.HandleFunc("/v1/models", s.handleModels)
	mux.HandleFunc("/", s.route)
	s.httpServer = &http.Server{
		Addr:    fmt.Sprintf(":%d", port),
		Handler: mux,
	}

	// Route JWT bootstrap through configured proxy or SS tunnel when available
	jwtMgr.customDo = func(url, contentType string, body io.Reader) (*http.Response, error) {
		var client *http.Client
		var proxyAddr string
		usedRotator := false

		// Phase 1: Try SmartRouter (for custom/fallback-proxy modes with SOCKS5/HTTP/SS)
		if s.smartRouter != nil && s.smartRouter.Len() > 0 {
			best := s.smartRouter.SelectBest()
			if best != nil && best.Dialer != nil {
				proxyAddr = best.Address
				client = &http.Client{
					Timeout: 15 * time.Second,
					Transport: &http.Transport{
						DialContext: best.Dialer.DialContext,
					},
				}
			}
		}

		// Phase 2: Fallback to SS rotator tunnel
		if client == nil && s.rotator != nil && s.rotator.Len() > 0 {
			upstreamAddr := s.rotator.Current()
			if upstreamAddr != "" {
				proxyAddr = upstreamAddr
				dialer := s.createSSDialer(upstreamAddr)
				if dialer != nil {
					usedRotator = true
					proxyAddr = upstreamAddr
					client = &http.Client{
						Timeout: 15 * time.Second,
						Transport: &http.Transport{
							DialContext: dialer.DialContext,
						},
					}
				}
			}
		}

		// Phase 3: Direct connection (no proxy)
		if client == nil {
			proxyAddr = ""
			client = &http.Client{Timeout: 15 * time.Second}
		}

		req, reqErr := http.NewRequest("POST", url, body)
		if reqErr != nil {
			return nil, reqErr
		}
		req.Header.Set("Content-Type", contentType)
		req.Header.Set("User-Agent", "mimocode/0.1.3")
		req.Header.Set("Accept", "*/*")
		resp, err := client.Do(req)
		if err != nil {
			return nil, err
		}

		// Detect 403 from bootstrap endpoint and mark dead the proxy
		if resp.StatusCode == http.StatusForbidden && proxyAddr != "" {
			if s.smartRouter != nil {
				s.smartRouter.MarkDead(proxyAddr)
			}
			if s.pool != nil {
				s.markAddrDead(proxyAddr)
			}
			if usedRotator && s.rotator != nil {
				log.Printf("[INFO] proxy %s banned (403 on bootstrap), removing from rotator", proxyAddr)
				s.rotator.Remove(proxyAddr)
			}
		}

		return resp, nil
	}

	return s
}

// generateFingerprint creates a random alphanumeric fingerprint
// matching the format <26chars> (mixed case alphanumeric).
func generateFingerprint() string {
	const chars = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789"
	b := make([]byte, 26)
	buf := make([]byte, 26)
	rand.Read(buf)
	for i := range b {
		b[i] = chars[int(buf[i])%len(chars)]
	}
	return string(b)
}



// Start starts the HTTP server and blocks until shutdown.
func (s *Server) Start() error {
	log.Printf("proxy server listening on %s", s.httpServer.Addr)
	err := s.httpServer.ListenAndServe()
	if err == http.ErrServerClosed {
		return nil
	}
	return err
}

// route dispatches to the appropriate handler.
func (s *Server) route(w http.ResponseWriter, r *http.Request) {
	switch {
	case r.Method == http.MethodOptions:
		s.handleOptions(w, r)
	case r.URL.Path == "/v1/models" && r.Method == http.MethodGet:
		s.handleModels(w, r)
	case r.URL.Path == "/v1/chat/completions" && r.Method == http.MethodPost:
		s.handleChat(w, r)
	default:
		s.handleNotFound(w, r)
	}
}

// handleChat handles POST /v1/chat/completions.
func (s *Server) handleChat(w http.ResponseWriter, r *http.Request) {
	// Rate limit check
	if s.rateLimiter != nil && !s.rateLimiter.Allow() {
		http.Error(w, `{"error":"rate limit exceeded"}`, http.StatusTooManyRequests)
		return
	}

	// Concurrency limit
	select {
	case s.semaphore <- struct{}{}:
		defer func() { <-s.semaphore }()
	default:
		http.Error(w, `{"error":"too many concurrent requests"}`, http.StatusServiceUnavailable)
		return
	}

	// Read request body
	body, err := io.ReadAll(r.Body)
	if err != nil {
		http.Error(w, `{"error":"failed to read request body"}`, http.StatusBadRequest)
		return
	}
	defer r.Body.Close()

	// Parse as raw map — preserve ALL fields from client
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(body, &raw); err != nil {
		log.Printf("[WARN] JSON parse failed: %v", err)
		http.Error(w, `{"error":"invalid JSON"}`, http.StatusBadRequest)
		return
	}

	// Validate messages exist
	msgsRaw, ok := raw["messages"]
	if !ok || string(msgsRaw) == "null" || string(msgsRaw) == "[]" {
		http.Error(w, `{"error":"messages array is required and must not be empty"}`, http.StatusBadRequest)
		return
	}

	// Inject system message at position 0, preserving ALL original fields
	modifiedBody, err := injectSystemMessage(body)
	if err != nil {
		http.Error(w, `{"error":"failed to marshal request"}`, http.StatusInternalServerError)
		return
	}

	// Strip compliance block text from prompt to avoid triggering upstream detector
	if stripped, stripErr := stripComplianceBlock(modifiedBody); stripErr == nil && len(stripped) > 0 {
		modifiedBody = stripped
	}

	// Determine streaming
	var isStream bool
	if streamRaw, ok := raw["stream"]; ok {
		json.Unmarshal(streamRaw, &isStream)
	}
	isStream = isStream || r.Header.Get("Accept") == "text/event-stream" ||
		r.Header.Get("x-stream") == "true"

	// Get JWT — retry up to JWTRefreshRetries times with proxy rotation
	// (bootstrap request goes through proxy; if proxy is bad, rotate and retry)
	var jwt string
	for attempt := 0; attempt < JWTRefreshRetries; attempt++ {
		jwt, err = s.jwtMgr.Get()
		if err == nil {
			break
		}

		// Check if proxy pool is exhausted
		poolLen := 0
		if s.rotator != nil {
			poolLen = s.rotator.Len()
		}
		if poolLen == 0 {
			log.Printf("[JWT] proxy pool empty after %d attempts, giving up", attempt+1)
			break
		}

		// Rotate to next proxy for next bootstrap attempt
		var nextAddr string
		if s.rotator != nil && poolLen > 0 {
			nextAddr = s.rotator.Next()
		}
		idx := 0
		if s.rotator != nil {
			idx = s.rotator.Index()
		}
		log.Printf("[JWT] attempt %d/%d failed: %v — rotating to proxy[%d/%d] %s",
			attempt+1, JWTRefreshRetries, err, idx, poolLen, nextAddr)

		// Invalidate cached JWT so next Get() triggers a fresh bootstrap
		s.jwtMgr.Invalidate()
	}
	if err != nil {
		log.Printf("[WARN] JWT fetch failed after %d attempts: %v", JWTRefreshRetries, err)
		http.Error(w, `{"error":"authentication failed"}`, http.StatusUnauthorized)
		return
	}

	// Build upstream headers
	upstreamHeaders := http.Header{
		"Authorization":       {fmt.Sprintf("Bearer %s", jwt)},
		"User-Agent":          {"mimocode/0.1.3 ai-sdk/provider-utils/4.0.23 runtime/bun/1.3.14"},
		"Accept":              {"*/*"},
		"x-session-affinity":  {"ses_" + s.fingerprint},
		"X-Mimo-Source":       {"mimocode-cli-free"},
		"Content-Type":        {"application/json"},
		"Connection":          {"keep-alive"},
	}
	if isStream {
		upstreamHeaders.Set("Accept", "text/event-stream")
	}

	// Forward to upstream with retry
	var resp *http.Response
	chatURL := s.baseURL + "/api/free-ai/openai/chat"

	for retry := 0; retry <= TimeoutRetries; retry++ {
		resp, err = s.forwardRequest(chatURL, modifiedBody, upstreamHeaders, isStream)
		if err == nil {
			break
		}
		poolLen := 0
		if s.rotator != nil {
			poolLen = s.rotator.Len()
		}
		var nextAddr string
		if s.rotator != nil && poolLen > 0 {
			nextAddr = s.rotator.Next()
		}
		idx := 0
		if s.rotator != nil {
			idx = s.rotator.Index()
		}
		log.Printf("[TIMEOUT] retry %d/%d failed: %v -> rotate proxy[%d/%d] %s", retry+1, TimeoutRetries, err, idx, poolLen, nextAddr)
	}
	if err != nil {
		http.Error(w, `{"error":"upstream request failed after retries"}`, http.StatusBadGateway)
		return
	}
	// NOTE: Do NOT use defer resp.Body.Close() here — resp may be reassigned
	// by 401/429 handling below. Close explicitly at each exit point instead.
	defer func() {
		if resp != nil && resp.Body != nil {
			resp.Body.Close()
		}
	}()

	// Handle 401 — JWT expired, try refreshing up to JWTRefreshRetries times
	// (rotate proxy + refresh JWT each attempt, stop early if pool exhausted)
	if resp.StatusCode == http.StatusUnauthorized {
		for attempt := 0; attempt < JWTRefreshRetries; attempt++ {
			// Check if proxy pool is exhausted
			poolLen := 0
			if s.rotator != nil {
				poolLen = s.rotator.Len()
			}
			if poolLen == 0 {
				log.Printf("[401] proxy pool empty after %d attempts, giving up", attempt)
				break
			}

			// Rotate to next proxy
			var nextAddr string
			if s.rotator != nil && poolLen > 0 {
				nextAddr = s.rotator.Next()
			}
			idx := 0
			if s.rotator != nil {
				idx = s.rotator.Index()
			}
			log.Printf("[401] attempt %d/%d: refreshing JWT + rotating to proxy[%d/%d] %s",
				attempt+1, JWTRefreshRetries, idx, poolLen, nextAddr)

			// Refresh JWT
			s.jwtMgr.Invalidate()
			newJwt, jwtErr := s.jwtMgr.Get()
			if jwtErr != nil {
				log.Printf("[401] JWT refresh failed on attempt %d/%d: %v", attempt+1, JWTRefreshRetries, jwtErr)
				continue
			}
			upstreamHeaders.Set("Authorization", fmt.Sprintf("Bearer %s", newJwt))

			// Retry request
			resp.Body.Close()
			resp, err = s.forwardRequest(chatURL, modifiedBody, upstreamHeaders, isStream)
			if err != nil {
				log.Printf("[401] retry %d/%d failed: %v", attempt+1, JWTRefreshRetries, err)
				continue
			}
			if resp.StatusCode != http.StatusUnauthorized {
				log.Printf("[401] retry %d/%d OK (status %d)", attempt+1, JWTRefreshRetries, resp.StatusCode)
				break
			}
			log.Printf("[401] still 401 after retry %d/%d", attempt+1, JWTRefreshRetries)
		}
		// If still 401 after all retries, return error to client
		if resp.StatusCode == http.StatusUnauthorized {
			log.Printf("[401] all %d retries exhausted, returning 401 to client", JWTRefreshRetries)
			resp.Body.Close()
			http.Error(w, `{"error":"JWT refresh failed after retries"}`, http.StatusUnauthorized)
			return
		}
	}

	// Handle 403 — proxy banned, blacklist + rotate + refresh JWT + retry
	if resp.StatusCode == http.StatusForbidden {
		log.Printf("[INFO] upstream returned 403, proxy banned, rotating...")

		// Blacklist current proxy
		if s.rotator != nil && s.rotator.Len() > 0 {
			currentAddr := s.rotator.Current()
			if currentAddr != "" {
				s.rotator.Remove(currentAddr)
				s.markAddrDead(currentAddr)
			}
		}

		// Refresh JWT (will use new proxy via customDo)
		s.jwtMgr.Invalidate()
		jwt, err = s.jwtMgr.Get()
		if err != nil {
			log.Printf("[WARN] JWT refresh after 403 failed: %v", err)
			http.Error(w, `{"error":"authentication failed"}`, http.StatusUnauthorized)
			return
		}
		upstreamHeaders.Set("Authorization", fmt.Sprintf("Bearer %s", jwt))

		// Rotate to next proxy
		if s.rotator != nil && s.rotator.Len() > 0 {
			s.rotator.Next()
		}

		// Retry
		resp.Body.Close()
		resp, err = s.forwardRequest(chatURL, modifiedBody, upstreamHeaders, isStream)
		if err != nil {
			log.Printf("[WARN] upstream request failed after 403 retry: %v", err)
			http.Error(w, `{"error":"upstream request failed after proxy rotation"}`, http.StatusBadGateway)
			return
		}
		if resp.StatusCode == http.StatusForbidden {
			log.Printf("[WARN] still 403 after proxy rotation and JWT refresh")
		}
	}

	// Handle 429 — rate limited, rotate proxy + refresh JWT + retry (like Python)
	if resp.StatusCode == http.StatusTooManyRequests {
		poolLen := 0
		if s.rotator != nil {
			poolLen = s.rotator.Len()
		}
		log.Printf("[429] rate limited, rotating proxy (pool: %d)", poolLen)
		for attempt := 0; attempt < RateLimitRetries; attempt++ {
			// Rotate to next proxy
			var nextAddr string
			if s.rotator != nil && s.rotator.Len() > 0 {
				nextAddr = s.rotator.Next()
			}
			idx := 0
			if s.rotator != nil {
				idx = s.rotator.Index()
			}
			if poolLen > 1 {
				// Rotated to a different proxy — retry immediately with minimal cooldown
				log.Printf("[429] rotated to proxy[%d/%d] %s, retrying immediately...", idx, poolLen, nextAddr)
				time.Sleep(1 * time.Second)
			} else if poolLen == 1 {
				// Same proxy (pool=1) — wait full delay before retry
				log.Printf("[429] no rotation possible (pool: 1), waiting %ds...", s.rateLimitRetryDelay/time.Second)
				time.Sleep(s.rateLimitRetryDelay)
			} else {
				// No proxy — wait full delay
				log.Printf("[429] no proxy available (pool: 0), waiting %ds...", s.rateLimitRetryDelay/time.Second)
				time.Sleep(s.rateLimitRetryDelay)
			}

			// Refresh JWT (like Python: invalidate_jwt + get_jwt)
			s.jwtMgr.Invalidate()
			newJwt, jwtErr := s.jwtMgr.Get()
			if jwtErr != nil {
				log.Printf("[429] JWT refresh failed: %v", jwtErr)
				continue
			}
			upstreamHeaders.Set("Authorization", fmt.Sprintf("Bearer %s", newJwt))

			resp.Body.Close()
			resp, err = s.forwardRequest(chatURL, modifiedBody, upstreamHeaders, isStream)
			if err != nil {
				log.Printf("[429] retry %d/%d failed: %v", attempt+1, RateLimitRetries, err)
				continue
			}
			if resp.StatusCode != http.StatusTooManyRequests {
				log.Printf("[429] retry %d/%d OK (status %d)", attempt+1, RateLimitRetries, resp.StatusCode)
				break
			}
			log.Printf("[429] still 429 after retry %d/%d", attempt+1, RateLimitRetries)
		}
		if resp.StatusCode == http.StatusTooManyRequests {
			// If FallbackHandler is available, try shadowmere SS pool before giving up
			if s.fallbackHandler != nil {
				log.Printf("[429] all %d retries exhausted, trying shadowmere fallback...", RateLimitRetries)
				s.jwtMgr.Invalidate()
				newJwt, jwtErr := s.jwtMgr.Get()
				if jwtErr == nil {
					upstreamHeaders.Set("Authorization", fmt.Sprintf("Bearer %s", newJwt))
				}
				resp.Body.Close()
				resp, err = s.forwardRequest(chatURL, modifiedBody, upstreamHeaders, isStream)
				if err == nil && resp.StatusCode != http.StatusTooManyRequests {
					log.Printf("[429] shadowmere fallback OK (status %d)", resp.StatusCode)
					goto after429
				}
				if resp != nil {
					resp.Body.Close()
				}
			}
			log.Printf("[429] all retries and fallbacks exhausted")
			http.Error(w, `{"error":"rate limit exceeded after retries"}`, http.StatusTooManyRequests)
			return
		}
	}
after429:

	// Forward response headers
	forwardHeaders(w, resp)
	// Add CORS
	w.Header().Set("Access-Control-Allow-Origin", "*")

	if isStream {
		// Scenario 1: Pre-check — buffer first 5 lines BEFORE writing headers to client
		const preCheckLines = 5
		preResult, preErr := streamSSEWithPreCheck(resp, preCheckLines)

		if preErr != nil {
			// Upstream read error during pre-check
			log.Printf("[WARN] pre-check read error: %v", preErr)
			resp.Body.Close()
			http.Error(w, `{"error":"upstream read error"}`, http.StatusBadGateway)
			return
		}

		if !preResult.Clean {
			// Block detected in pre-check → retry cycle
			log.Printf("[COMPLIANCE] pre-check blocked (reason: %s), starting retry cycle", preResult.BlockReason)
			resp.Body.Close()

			const maxRetries = 5
			for retry := 0; retry < maxRetries; retry++ {
				if s.rotator != nil && s.rotator.Len() == 0 {
					break
				}

				// Log current proxy
				if s.rotator != nil {
					idx := s.rotator.Index()
					addr := s.rotator.Current()
					log.Printf("[COMPLIANCE] retry %d/%d: using proxy[%d] %s", retry+1, maxRetries, idx, addr)
				}

				// Refresh JWT AFTER rotation
				s.jwtMgr.Invalidate()
				newJwt, jwtErr := s.jwtMgr.Get()
				if jwtErr != nil {
					log.Printf("[COMPLIANCE] JWT refresh failed on retry %d/%d: %v", retry+1, maxRetries, jwtErr)
					if preResult.BlockReason == "compliance" {
						// Compliance: blacklist + rotate
						if s.rotator != nil && s.rotator.Len() > 0 {
							deadAddr := s.rotator.RemoveCurrent()
							if deadAddr != "" {
								s.blacklistAddr(deadAddr)
							}
						}
					} else {
						// Auth: just rotate (don't blacklist)
						if s.rotator != nil && s.rotator.Len() > 0 {
							s.rotator.RemoveCurrent()
						}
					}
					continue
				}
				upstreamHeaders.Set("Authorization", fmt.Sprintf("Bearer %s", newJwt))

				if retry > 0 {
					time.Sleep(1 * time.Second)
				}

				// Retry request
				resp, err = s.forwardRequest(chatURL, modifiedBody, upstreamHeaders, isStream)
				if err != nil {
					log.Printf("[COMPLIANCE] retry %d/%d forward failed: %v", retry+1, maxRetries, err)
					continue
				}

				// Pre-check the retry response
				retryResult, retryPreErr := streamSSEWithPreCheck(resp, preCheckLines)
				if retryPreErr != nil {
					log.Printf("[COMPLIANCE] retry %d/%d pre-check error: %v", retry+1, maxRetries, retryPreErr)
					resp.Body.Close()
					continue
				}

				if !retryResult.Clean {
					log.Printf("[COMPLIANCE] retry %d/%d still blocked (reason: %s)", retry+1, maxRetries, retryResult.BlockReason)
					resp.Body.Close()
					if retryResult.BlockReason == "compliance" {
						if s.rotator != nil && s.rotator.Len() > 0 {
							deadAddr := s.rotator.RemoveCurrent()
							if deadAddr != "" {
								s.blacklistAddr(deadAddr)
							}
						}
					} else {
						// Auth error: rotate without blacklist
						if s.rotator != nil && s.rotator.Len() > 0 {
							s.rotator.RemoveCurrent()
						}
					}
					continue
				}

				// Success — pre-check passed, write headers + stream to client
				log.Printf("[COMPLIANCE] retry %d/%d succeeded, streaming", retry+1, maxRetries)
				w.Header().Set("Content-Type", "text/event-stream")
				w.Header().Set("Cache-Control", "no-cache")
				w.Header().Set("Connection", "keep-alive")
				w.WriteHeader(resp.StatusCode)
				if flusher, ok := w.(http.Flusher); ok {
					flusher.Flush()
				}
				proxyInfo := s.currentProxyInfo()
				if streamErr := streamSSEFromScanner(retryResult.Scanner, w, ThinkingTimeout, proxyInfo, 0); streamErr != nil {
					if streamErr == ErrAuthError {
						log.Printf("[AUTH] mid-stream auth error on retry, refreshing JWT for next request")
						s.jwtMgr.Invalidate()
					}
				}
				return
			}

			// All retries exhausted
			log.Printf("[COMPLIANCE] all retries exhausted")
			if preResult.BlockReason == "auth" {
				http.Error(w, `{"error":"auth error after retry"}`, http.StatusUnauthorized)
			} else {
				http.Error(w, `{"error":"compliance block after proxy rotation"}`, http.StatusForbidden)
			}
			return
		}

		// Pre-check passed — clean response, write headers + stream
		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("Cache-Control", "no-cache")
		w.Header().Set("Connection", "keep-alive")
		w.WriteHeader(resp.StatusCode)
		if flusher, ok := w.(http.Flusher); ok {
			flusher.Flush()
		}
		proxyInfo := s.currentProxyInfo()
		if streamErr := streamSSEFromScanner(preResult.Scanner, w, ThinkingTimeout, proxyInfo, 0); streamErr != nil {
			if streamErr == ErrComplianceBlock {
				log.Printf("[COMPLIANCE] mid-stream compliance block, blacklisting proxy")
				if s.rotator != nil && s.rotator.Len() > 0 {
					deadAddr := s.rotator.RemoveCurrent()
					if deadAddr != "" {
						s.blacklistAddr(deadAddr)
					}
				}
			} else {
				log.Printf("[WARN] stream error: %v", streamErr)
				s.markProxyDead(streamErr)
			}
		}
	} else {
		respBody, readErr := io.ReadAll(resp.Body)
		resp.Body.Close()
		if readErr != nil {
			log.Printf("[WARN] failed to read upstream response: %v", readErr)
			w.WriteHeader(resp.StatusCode)
			return
		}

		// Check for compliance block in non-streaming response
		// Detect regardless of status code — upstream may return 200/403/441 with compliance text
		if detectComplianceBlock(respBody) {
			// Log for debugging (first 500 chars)
			debugBody := respBody
			if len(debugBody) > 500 {
				debugBody = debugBody[:500]
			}
			log.Printf("[COMPLIANCE] compliance block detected in non-streaming response")

			// Blacklist current proxy (atomic remove — advances rotator to next)
			if s.rotator != nil && s.rotator.Len() > 0 {
				deadAddr := s.rotator.RemoveCurrent()
				if deadAddr != "" {
					s.blacklistAddr(deadAddr)
				}
			}

			const maxComplianceRetries = 5
			for retry := 0; retry < maxComplianceRetries; retry++ {
				if s.rotator != nil && s.rotator.Len() == 0 {
					break
				}

				// Log current proxy
				if s.rotator != nil {
					idx := s.rotator.Index()
					addr := s.rotator.Current()
					log.Printf("[COMPLIANCE] retry %d/%d: using proxy[%d] %s", retry+1, maxComplianceRetries, idx, addr)
				}

				// Refresh JWT AFTER rotation — fetch through current (new) proxy
				s.jwtMgr.Invalidate()
				newJwt, jwtErr := s.jwtMgr.Get()
				if jwtErr != nil {
					log.Printf("[COMPLIANCE] JWT refresh failed on retry %d/%d: %v, marking dead + rotating", retry+1, maxComplianceRetries, jwtErr)
					// This proxy is bad — mark dead + rotate to next
					if s.rotator != nil && s.rotator.Len() > 0 {
						deadAddr := s.rotator.RemoveCurrent()
						if deadAddr != "" {
							s.markAddrDead(deadAddr)
						}
					}
					continue
				}
				upstreamHeaders.Set("Authorization", fmt.Sprintf("Bearer %s", newJwt))

				// Backoff between retries
				if retry > 0 {
					time.Sleep(1 * time.Second)
				}

				if resp != nil && resp.Body != nil {
					resp.Body.Close()
				}
				resp, err = s.forwardRequest(chatURL, modifiedBody, upstreamHeaders, isStream)
				if err != nil {
					log.Printf("[COMPLIANCE] retry %d/%d failed: %v", retry+1, maxComplianceRetries, err)
					resp = nil
					continue
				}

				retryBody, readErr := io.ReadAll(resp.Body)
				resp.Body.Close()
				if readErr != nil {
					log.Printf("[COMPLIANCE] retry %d/%d read error: %v", retry+1, maxComplianceRetries, readErr)
					continue
				}

				if detectComplianceBlock(retryBody) {
					debugRetry := retryBody
					if len(debugRetry) > 500 {
						debugRetry = debugRetry[:500]
					}
					log.Printf("[COMPLIANCE] retry %d/%d still compliance-blocked", retry+1, maxComplianceRetries)
					// Blacklist this proxy too (atomic) — advances rotator to next
					if s.rotator != nil && s.rotator.Len() > 0 {
						deadAddr := s.rotator.RemoveCurrent()
						if deadAddr != "" {
							s.blacklistAddr(deadAddr)
						}
					}
					// JWT will be refreshed at the top of next loop iteration
					continue
				}

				// Success — forward clean response
				log.Printf("[COMPLIANCE] retry %d/%d succeeded (status %d)", retry+1, maxComplianceRetries, resp.StatusCode)
				w.WriteHeader(resp.StatusCode)
				w.Write(retryBody)
				return
			}

			// All retries failed
			http.Error(w, `{"error":"compliance block after proxy rotation"}`, http.StatusForbidden)
			return
		}

		w.WriteHeader(resp.StatusCode)
		w.Write(respBody)
	}
}

// injectSystemMessage parses the original JSON body, prepends a system message to the
// messages array, and re-marshals the full request — preserving ALL extra fields
// at every level (top-level and per-message like tool_calls, tool_call_id, etc).
func injectSystemMessage(body []byte) ([]byte, error) {
	// Parse the original body as a map to preserve all top-level fields
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(body, &raw); err != nil {
		return nil, fmt.Errorf("parse body: %w", err)
	}

	// Parse messages as raw JSON array — each message stays as raw bytes
	var messages []json.RawMessage
	if err := json.Unmarshal(raw["messages"], &messages); err != nil {
		return nil, fmt.Errorf("parse messages: %w", err)
	}

	// Build system message as raw JSON
	sysContent, _ := json.Marshal(systemMessage)
	sysMsg, _ := json.Marshal(map[string]json.RawMessage{
		"role":    []byte(`"system"`),
		"content": sysContent,
	})

	// Build new messages array: [system, ...original]
	var buf bytes.Buffer
	buf.WriteByte('[')
	buf.Write(sysMsg)
	for _, msg := range messages {
		buf.WriteByte(',')
		buf.Write(msg)
	}
	buf.WriteByte(']')

	// Replace messages key in the raw map
	raw["messages"] = buf.Bytes()

	// Re-marshal the full request
	return json.Marshal(raw)
}

// forwardRequest sends an HTTP POST to the upstream URL with the given body and headers.
// When a SmartRouter is available (custom/fallback-proxy modes), it selects the best proxy.
// Otherwise when a rotator with SS upstream is available, the request is tunneled through SS.
// For streaming requests, no Client.Timeout is set to avoid killing body reads mid-SSE;
// instead, transport-level timeouts handle connection/header phases only.
func (s *Server) forwardRequest(url string, body []byte, headers http.Header, isStream bool) (*http.Response, error) {
	var client *http.Client

	// Phase 1: Try SmartRouter (for custom/fallback-proxy modes with mixed protocols)
	if s.smartRouter != nil && s.smartRouter.Len() > 0 {
		best := s.smartRouter.SelectBest()
		if best != nil && best.Dialer != nil {
			client = s.buildClient(best.Dialer.DialContext, isStream)
		}
	}

	// Phase 2: Fall back to SS rotator (for auto/fallback modes)
	if client == nil && s.rotator != nil && s.rotator.Len() > 0 {
		upstreamAddr := s.rotator.Current()
		if upstreamAddr != "" {
			dialer := s.createSSDialer(upstreamAddr)
			if dialer != nil {
				client = s.buildClient(dialer.DialContext, isStream)
			}
		}
	}

	if client == nil {
		if isStream {
			// Wrap default dialer with deadlineConn too
			defaultDial := (&net.Dialer{Timeout: StreamDialTimeout}).DialContext
			wrappedDial := func(ctx context.Context, network, addr string) (net.Conn, error) {
				conn, err := defaultDial(ctx, network, addr)
				if err != nil {
					return nil, err
				}
				return &deadlineConn{Conn: conn, timeout: StreamIdleReadTimeout}, nil
			}
			client = &http.Client{
				Transport: &http.Transport{
					DialContext:           wrappedDial,
					ResponseHeaderTimeout: StreamHeaderTimeout,
					TLSHandshakeTimeout:  StreamTLSTimeout,
					IdleConnTimeout:       StreamIdleTimeout,
				},
			}
		} else {
			client = &http.Client{Timeout: s.proxyTimeout}
		}
	}

	req, err := http.NewRequest(http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}
	req.Header = headers
	return client.Do(req)
}

// buildClient creates an http.Client with appropriate timeouts for the request type.
// For streaming: transport-level timeouts only (no Client.Timeout) to avoid killing body reads.
// A deadlineConn wrapper resets a read deadline on every Read(), so if upstream goes
// silent for StreamIdleReadTimeout, the read fails with a timeout error instead of hanging.
// For non-streaming: total Client.Timeout to enforce a hard deadline.
func (s *Server) buildClient(dialCtx func(ctx context.Context, network, addr string) (net.Conn, error), isStream bool) *http.Client {
	if isStream {
		// Wrap dialer to apply read deadline per-Read()
		wrappedDial := func(ctx context.Context, network, addr string) (net.Conn, error) {
			conn, err := dialCtx(ctx, network, addr)
			if err != nil {
				return nil, err
			}
			return &deadlineConn{Conn: conn, timeout: StreamIdleReadTimeout}, nil
		}
		return &http.Client{
			Transport: &http.Transport{
				DialContext:           wrappedDial,
				ResponseHeaderTimeout: StreamHeaderTimeout,
				TLSHandshakeTimeout:  StreamTLSTimeout,
				IdleConnTimeout:       StreamIdleTimeout,
			},
		}
	}
	return &http.Client{
		Timeout: ProxyTimeout,
		Transport: &http.Transport{
			DialContext: dialCtx,
		},
	}
}

// deadlineConn wraps a net.Conn and resets a read deadline on every Read().
// If no data arrives within the timeout, the next Read() returns a timeout error.
// This prevents goroutine hangs when upstream goes silent without closing the connection.
type deadlineConn struct {
	net.Conn
	timeout time.Duration
}

func (c *deadlineConn) Read(b []byte) (int, error) {
	c.Conn.SetReadDeadline(time.Now().Add(c.timeout))
	return c.Conn.Read(b)
}

// currentProxyInfo returns a human-readable string describing the current proxy configuration.
func (s *Server) currentProxyInfo() string {
	if s.smartRouter != nil && s.smartRouter.Len() > 0 {
		best := s.smartRouter.SelectBest()
		if best != nil {
			return fmt.Sprintf("%s://%s (pool: %d alive)", best.Protocol, best.Address, s.smartRouter.Len())
		}
	}
	if s.rotator != nil && s.rotator.Len() > 0 {
		return fmt.Sprintf("ss://%s (pool: %d)", s.rotator.Current(), s.rotator.Len())
	}
	return ""
}

// createSSDialer creates an SS dialer for the given upstream address.
// Returns nil if the address cannot be matched to an SS config in the pool.
func (s *Server) createSSDialer(upstreamAddr string) *sspool.SSDialer {
	if s.pool == nil {
		return nil
	}
	cfg := s.pool.GetByAddr(upstreamAddr)
	if cfg == nil {
		return nil
	}
	dialer, err := sspool.NewSSDialer(*cfg)
	if err != nil {
		return nil
	}
	return dialer
}

// Shutdown gracefully shuts down the server.
func (s *Server) Shutdown(ctx context.Context) error {
	// Save blacklist before shutdown
	if s.blacklistMgr != nil {
		if err := s.blacklistMgr.Save(); err != nil {
			log.Printf("[Blacklist] save on shutdown failed: %v", err)
		}
		s.blacklistMgr.Stop()
	}
	if s.httpServer != nil {
		return s.httpServer.Shutdown(ctx)
	}
	return nil
}

// forwardHeaders copies response headers from upstream to client,
// skipping Content-Length (we may write different size) and
// Content-Encoding (body is already decompressed).
func forwardHeaders(w http.ResponseWriter, resp *http.Response) {
	for key, values := range resp.Header {
		if key == "Content-Length" || key == "Content-Encoding" {
			continue
		}
		for _, v := range values {
			w.Header().Add(key, v)
		}
	}
}

// blacklistAddr adds an address to both the persistent blacklist and the pool's dead list.
// ONLY use when compliance block pattern is detected in response.
func (s *Server) blacklistAddr(addr string) {
	if addr == "" {
		return
	}
	if s.blacklistMgr != nil {
		s.blacklistMgr.Add(addr)
	}
	if s.pool != nil {
		s.pool.MarkDead(addr)
	}
}

// markAddrDead marks an address as dead in the pool (temporary, not blacklist).
// Use for transient errors like JWT failure, write errors, etc.
func (s *Server) markAddrDead(addr string) {
	if addr == "" {
		return
	}
	if s.smartRouter != nil {
		s.smartRouter.MarkDead(addr)
	}
	if s.pool != nil {
		s.pool.MarkDead(addr)
	}
}

// isBlacklisted checks if an address has a blacklisted IP.
func (s *Server) isBlacklisted(addr string) bool {
	if s.blacklistMgr == nil {
		return false
	}
	return s.blacklistMgr.IsBlacklisted(addr)
}

// markProxyDead marks the current proxy as dead (temporary, not blacklist)
// when a stream write error occurs. The proxy may recover later.
func (s *Server) markProxyDead(err error) {
	if !isStreamWriteError(err) {
		return
	}
	if s.rotator == nil || s.rotator.Len() == 0 {
		return
	}
	addr := s.rotator.Current()
	if addr == "" {
		return
	}
	log.Printf("[COMPLIANCE] marking proxy dead due to write error: %s", addr)
	if s.smartRouter != nil {
		s.smartRouter.MarkDead(addr)
	}
	if s.pool != nil {
		s.pool.MarkDead(addr)
	}
}

// isStreamWriteError checks if an error is a client-side write failure (broken pipe, etc).
func isStreamWriteError(err error) bool {
	if err == nil {
		return false
	}
	msg := err.Error()
	return strings.Contains(msg, "broken pipe") ||
		strings.Contains(msg, "write:") ||
		strings.Contains(msg, "connection reset")
}

// isValidSSEBuffer checks if a buffer contains valid SSE format.
// At least one non-empty, non-comment line must start with "data:", "event:", or "id:".
func isValidSSEBuffer(buf []byte) bool {
	lines := bytes.Split(buf, []byte("\n"))
	for _, line := range lines {
		trimmed := bytes.TrimSpace(line)
		if len(trimmed) == 0 {
			continue
		}
		// SSE comment line (starts with ":")
		if trimmed[0] == ':' {
			continue
		}
		// Valid SSE field: data:, event:, id:, retry:
		if bytes.HasPrefix(trimmed, []byte("data:")) ||
			bytes.HasPrefix(trimmed, []byte("event:")) ||
			bytes.HasPrefix(trimmed, []byte("id:")) ||
			bytes.HasPrefix(trimmed, []byte("retry:")) {
			return true
		}
		// JSON object — not SSE
		if trimmed[0] == '{' || trimmed[0] == '[' {
			return false
		}
	}
	return false
}
