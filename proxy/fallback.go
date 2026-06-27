package proxy

import (
	"fmt"
	"log"
	"sync"
	"time"

	"github.com/vincent/mimo-xoay/sspool"
)

// FallbackHandler implements fallback-proxy mode logic:
// user pool (SmartRouter) → shadowmere SS pool → direct.
type FallbackHandler struct {
	mu               sync.RWMutex
	router           *SmartRouter
	shadowmerePool   *sspool.SSPool
	lastShadowmere   time.Time   // last time we fetched from shadowmere
	refreshInterval  time.Duration
	fallbackEnabled  bool       // when true, can fall back to shadowmere
	directOnExhaust  bool       // when true, try direct after all fallbacks
	poolFilter       sspool.BlacklistFilter // preserved across pool refreshes
}

// NewFallbackHandler creates a new FallbackHandler.
// router: SmartRouter for user-provided proxies (may be nil).
// refreshInterval: how often to re-fetch shadowmere servers.
func NewFallbackHandler(router *SmartRouter, refreshInterval time.Duration) *FallbackHandler {
	return &FallbackHandler{
		router:          router,
		shadowmerePool:  sspool.NewSSPool(),
		refreshInterval: refreshInterval,
		fallbackEnabled: true,
		directOnExhaust: true,
	}
}

// FallbackResult describes the fallback chain result.
type FallbackResult struct {
	Source      string // "user_pool", "shadowmere", "direct"
	ProxyAddr   string // proxy address used (empty for direct)
	Status      int    // HTTP status from upstream (0 = no request made)
	Err         error
}

// NextProxy returns the next proxy to use following the fallback chain.
// Phase 1: user pool (SmartRouter.SelectBest/SelectNext)
// Phase 2: shadowmere SS pool (fetch+rotate)
// Phase 3: direct (nil dialer)
func (h *FallbackHandler) NextProxy() *FallbackResult {
	// Phase 1: User pool
	if h.router != nil {
		if p := h.router.SelectBest(); p != nil {
			return &FallbackResult{
				Source:    "user_pool",
				ProxyAddr: p.Address,
			}
		}
	}

	// Phase 2: Shadowmere SS pool
	if h.fallbackEnabled {
		h.mu.Lock()
		// Refresh shadowmere pool if empty or stale
		if h.shadowmerePool.Len() == 0 || time.Since(h.lastShadowmere) > h.refreshInterval {
			h.refreshShadowmereLocked()
		}
		h.mu.Unlock()

		h.mu.RLock()
		poolLen := h.shadowmerePool.Len()
		h.mu.RUnlock()

		if poolLen > 0 {
			h.mu.Lock()
			s := h.shadowmerePool.Next()
			h.mu.Unlock()
			if s != nil {
				addr := fmt.Sprintf("%s:%d", s.Server, s.Port)
				return &FallbackResult{
					Source:    "shadowmere",
					ProxyAddr: addr,
				}
			}
		}
	}

	// Phase 3: Direct
	if h.directOnExhaust {
		return &FallbackResult{
			Source: "direct",
		}
	}

	return &FallbackResult{
		Err: fmt.Errorf("no proxy available"),
	}
}

// ReportFailure marks a proxy as dead after a failure.
func (h *FallbackHandler) ReportFailure(addr string) {
	if h.router != nil && addr != "" {
		h.router.MarkDead(addr)
		log.Printf("[Fallback] user proxy %s marked dead", addr)
	}
}

// ReportSuccess marks a proxy as alive after a successful request.
func (h *FallbackHandler) ReportSuccess(addr string) {
	if h.router != nil && addr != "" {
		h.router.MarkAlive(addr)
	}
}

// HasUserPool returns true if the handler has any user pool proxies.
func (h *FallbackHandler) HasUserPool() bool {
	if h.router == nil {
		return false
	}
	return h.router.Len() > 0
}

// SetFilter sets a blacklist filter on the shadowmere pool.
func (h *FallbackHandler) SetFilter(f sspool.BlacklistFilter) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.poolFilter = f
	h.shadowmerePool.SetFilter(f)
}

// UserPoolLen returns the number of proxies in the user pool.
func (h *FallbackHandler) UserPoolLen() int {
	if h.router == nil {
		return 0
	}
	return h.router.Len()
}

// ShadowmerePoolLen returns the number of proxies in the shadowmere pool.
func (h *FallbackHandler) ShadowmerePoolLen() int {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return h.shadowmerePool.Len()
}

// refreshShadowmereLocked refreshes the shadowmere pool (caller must hold h.mu write lock).
func (h *FallbackHandler) refreshShadowmereLocked() {
	servers, err := sspool.FetchFromShadowmere("https://shadowmere.xyz/api/sub")
	if err != nil {
		log.Printf("[Fallback] shadowmere fetch failed: %v", err)
		return
	}
	// Rebuild pool
	newPool := sspool.NewSSPool()
	if h.poolFilter != nil {
		newPool.SetFilter(h.poolFilter)
	}
	for _, s := range servers {
		newPool.Add(s)
	}
	h.shadowmerePool = newPool
	h.lastShadowmere = time.Now()
	log.Printf("[Fallback] shadowmere pool refreshed: %d servers", h.shadowmerePool.Len())
}
