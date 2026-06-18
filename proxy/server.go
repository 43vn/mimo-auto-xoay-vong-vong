package proxy

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
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
	DisableRateLimit bool          // skip local rate limiter; rely on upstream only
	MinInterval      time.Duration // minimum interval between local requests (0 = disabled)
	ProxyTimeout     time.Duration // total timeout for non-streaming requests (0 = default)
}

// Server is the HTTP proxy server that forwards requests to the upstream API.
type Server struct {
	pool         *sspool.SSPool
	jwtMgr       *JWTManager
	rateLimiter  *RateLimiter
	rotator      *rotator.Rotator
	semaphore    chan struct{}
	fingerprint  string
	port         int
	baseURL      string // override for testing
	httpServer   *http.Server
	proxyTimeout time.Duration
}

// NewServer creates a new proxy Server.
func NewServer(pool *sspool.SSPool, jwtMgr *JWTManager, r *rotator.Rotator, port int, opts *Options) *Server {
	if opts == nil {
		opts = &Options{}
	}
	minInt := opts.MinInterval
	proxyTimeout := opts.ProxyTimeout

	fp := generateFingerprint()
	s := &Server{
		pool:         pool,
		jwtMgr:       jwtMgr,
		rotator:      r,
		semaphore:    make(chan struct{}, MaxConcurrent),
		fingerprint:  fp,
		port:         port,
		baseURL:      BaseURL,
		proxyTimeout: proxyTimeout,
	}
	if opts.DisableRateLimit {
		s.rateLimiter = nil
	} else {
		s.rateLimiter = NewRateLimiter(RateLimitMax, RateLimitWindow, minInt)
	}
	if s.proxyTimeout <= 0 {
		s.proxyTimeout = ProxyTimeout
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
	return s
}

// generateFingerprint creates a random hex fingerprint.
func generateFingerprint() string {
	b := make([]byte, 16)
	rand.Read(b)
	return hex.EncodeToString(b)
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

	// Determine streaming
	var isStream bool
	if streamRaw, ok := raw["stream"]; ok {
		json.Unmarshal(streamRaw, &isStream)
	}
	isStream = isStream || r.Header.Get("Accept") == "text/event-stream" ||
		r.Header.Get("x-stream") == "true"

	// Get JWT
	jwt, err := s.jwtMgr.Get()
	if err != nil {
		log.Printf("[WARN] JWT fetch failed: %v", err)
		http.Error(w, `{"error":"authentication failed"}`, http.StatusUnauthorized)
		return
	}

	// Build upstream headers
	upstreamHeaders := http.Header{
		"Authorization":       {fmt.Sprintf("Bearer %s", jwt)},
		"User-Agent":          {"mimocode/0.1.0 ai-sdk/provider-utils/4.0.23 runtime/bun/1.3.14"},
		"Accept":              {"*/*"},
		"X-Session-Affinity":  {s.fingerprint},
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

	// Handle 401 — JWT expired
	if resp.StatusCode == http.StatusUnauthorized {
		log.Printf("[INFO] upstream returned 401, refreshing JWT")
		s.jwtMgr.Invalidate()
		jwt, err = s.jwtMgr.Get()
		if err != nil {
			http.Error(w, `{"error":"JWT refresh failed"}`, http.StatusUnauthorized)
			return
		}
		upstreamHeaders.Set("Authorization", fmt.Sprintf("Bearer %s", jwt))
		resp.Body.Close()
		resp, err = s.forwardRequest(chatURL, modifiedBody, upstreamHeaders, isStream)
		if err != nil {
			http.Error(w, `{"error":"upstream request failed after JWT refresh"}`, http.StatusBadGateway)
			return
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
			log.Printf("[429] rotate -> proxy[%d/%d] %s, waiting %ds...", idx, poolLen, nextAddr, RateLimitRetryDelay/time.Second)
			time.Sleep(RateLimitRetryDelay)

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
			log.Printf("[429] all %d retries exhausted", RateLimitRetries)
			http.Error(w, `{"error":"rate limit exceeded after retries"}`, http.StatusTooManyRequests)
			return
		}
	}

	// Forward response headers
	for key, values := range resp.Header {
		for _, v := range values {
			w.Header().Add(key, v)
		}
	}
	// Add CORS
	w.Header().Set("Access-Control-Allow-Origin", "*")

	if isStream {
		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("Cache-Control", "no-cache")
		w.Header().Set("Connection", "keep-alive")
		w.WriteHeader(resp.StatusCode)

		// Flush headers before streaming
		if flusher, ok := w.(http.Flusher); ok {
			flusher.Flush()
		}

		if err := streamSSE(resp, w, ThinkingTimeout); err != nil {
			log.Printf("[WARN] stream error: %v", err)
		}
	} else {
		w.WriteHeader(resp.StatusCode)
		respBody, err := io.ReadAll(resp.Body)
		if err != nil {
			log.Printf("[WARN] failed to read upstream response: %v", err)
			return
		}
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
// When a Shadowsocks upstream is available via the rotator, the request is tunneled through SS.
// For streaming requests, no Client.Timeout is set to avoid killing body reads mid-SSE;
// instead, transport-level timeouts handle connection/header phases only.
func (s *Server) forwardRequest(url string, body []byte, headers http.Header, isStream bool) (*http.Response, error) {
	var client *http.Client

	if s.rotator != nil && s.rotator.Len() > 0 {
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
	if s.httpServer != nil {
		return s.httpServer.Shutdown(ctx)
	}
	return nil
}
