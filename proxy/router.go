package proxy

import (
	"net"
	"sync"
	"time"

	"github.com/vincent/mimo-xoay/sspool"
)

// ProxyInfo holds info about a single proxy endpoint
type ProxyInfo struct {
	Address   string          // "host:port"
	Protocol  string          // "ss", "socks5", "http", "https"
	Dialer    sspool.ProxyDialer
	Latency   time.Duration   // cached latency measurement
	LastCheck time.Time       // when latency was last measured
	Alive     bool            // whether the proxy responded to health check
}

// SmartRouter selects the best proxy based on latency
type SmartRouter struct {
	mu            sync.RWMutex
	proxies       []*ProxyInfo
	currentIndex  int
	directDialer  sspool.ProxyDialer // fallback: direct connection
	filter        sspool.BlacklistFilter
}

// NewSmartRouter creates router with proxy list
func NewSmartRouter(proxies []*ProxyInfo) *SmartRouter {
	return &SmartRouter{
		proxies: proxies,
	}
}

// SetFilter sets a blacklist filter. Blacklisted proxies are skipped in selection.
func (r *SmartRouter) SetFilter(f sspool.BlacklistFilter) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.filter = f
}

// SelectBest returns lowest-latency alive proxy; nil = use direct
func (r *SmartRouter) SelectBest() *ProxyInfo {
	r.mu.RLock()
	defer r.mu.RUnlock()
	
	var best *ProxyInfo
	for _, p := range r.proxies {
		if !p.Alive {
			continue
		}
		if r.filter != nil && r.filter.IsBlacklisted(p.Address) {
			continue
		}
		if best == nil || p.Latency < best.Latency {
			best = p
		}
	}
	return best
}

// SelectNext round-robin selection (fallback when smart fails)
func (r *SmartRouter) SelectNext() *ProxyInfo {
	r.mu.Lock()
	defer r.mu.Unlock()
	
	if len(r.proxies) == 0 {
		return nil
	}
	// Find next alive, non-blacklisted proxy
	start := r.currentIndex
	for {
		p := r.proxies[r.currentIndex]
		r.currentIndex = (r.currentIndex + 1) % len(r.proxies)
		if !p.Alive {
			if r.currentIndex == start {
				return nil
			}
			continue
		}
		if r.filter != nil && r.filter.IsBlacklisted(p.Address) {
			if r.currentIndex == start {
				return nil
			}
			continue
		}
		return p
	}
}

// RecordLatency update cached latency
func (r *SmartRouter) RecordLatency(addr string, d time.Duration) {
	r.mu.Lock()
	defer r.mu.Unlock()
	
	for _, p := range r.proxies {
		if p.Address == addr {
			p.Latency = d
			p.LastCheck = time.Now()
			return
		}
	}
}

// MarkDead mark proxy as dead
func (r *SmartRouter) MarkDead(addr string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	
	for _, p := range r.proxies {
		if p.Address == addr {
			p.Alive = false
			return
		}
	}
}

// MarkAlive mark proxy as alive
func (r *SmartRouter) MarkAlive(addr string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	
	for _, p := range r.proxies {
		if p.Address == addr {
			p.Alive = true
			return
		}
	}
}

// Len number of proxies
func (r *SmartRouter) Len() int {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return len(r.proxies)
}

// MeasureAll concurrently measure latency for all proxies using TCP dial
func (r *SmartRouter) MeasureAll(timeout time.Duration) {
	r.mu.RLock()
	proxies := make([]*ProxyInfo, len(r.proxies))
	copy(proxies, r.proxies)
	r.mu.RUnlock()
	
	var wg sync.WaitGroup
	for _, p := range proxies {
		wg.Add(1)
		go func(proxy *ProxyInfo) {
			defer wg.Done()
			start := time.Now()
			conn, err := net.DialTimeout("tcp", proxy.Address, timeout)
			latency := time.Since(start)
			r.mu.Lock()
			defer r.mu.Unlock()
			proxy.LastCheck = time.Now()
			if err != nil {
				proxy.Alive = false
				return
			}
			conn.Close()
			proxy.Alive = true
			proxy.Latency = latency
		}(p)
	}
	wg.Wait()
}

