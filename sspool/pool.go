package sspool

import (
	"net"
	"strconv"
	"sync"
)

// SSPool is a concurrent-safe pool of Shadowsocks servers with round-robin rotation.
type SSPool struct {
	mu      sync.RWMutex
	servers []SSConfig
	index   int
}

// NewSSPool creates an empty SSPool.
func NewSSPool() *SSPool {
	return &SSPool{}
}

// Add adds a server to the pool. Duplicates (same Key) are ignored.
func (p *SSPool) Add(s SSConfig) {
	p.mu.Lock()
	defer p.mu.Unlock()
	for _, existing := range p.servers {
		if existing.Key() == s.Key() {
			return
		}
	}
	p.servers = append(p.servers, s)
}

// Remove removes a server by its dedup key.
func (p *SSPool) Remove(key string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	for i, s := range p.servers {
		if s.Key() == key {
			p.servers = append(p.servers[:i], p.servers[i+1:]...)
			// Adjust index if needed
			if p.index >= len(p.servers) && len(p.servers) > 0 {
				p.index = p.index % len(p.servers)
			}
			return
		}
	}
}

// Get returns the current server without advancing the index.
// Returns nil if the pool is empty.
func (p *SSPool) Get() *SSConfig {
	p.mu.RLock()
	defer p.mu.RUnlock()
	if len(p.servers) == 0 {
		return nil
	}
	s := p.servers[p.index%len(p.servers)]
	return &s
}

// Next advances the round-robin index and returns the next server.
// Returns nil if the pool is empty.
func (p *SSPool) Next() *SSConfig {
	p.mu.Lock()
	defer p.mu.Unlock()
	if len(p.servers) == 0 {
		return nil
	}
	p.index = (p.index + 1) % len(p.servers)
	s := p.servers[p.index]
	return &s
}

// Rotate returns the current server and advances to the next one.
// This is a convenience method combining Get + Next.
// Returns nil if the pool is empty.
func (p *SSPool) Rotate() *SSConfig {
	p.mu.Lock()
	defer p.mu.Unlock()
	if len(p.servers) == 0 {
		return nil
	}
	s := p.servers[p.index%len(p.servers)]
	p.index = (p.index + 1) % len(p.servers)
	return &s
}

// Len returns the number of servers in the pool.
func (p *SSPool) Len() int {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return len(p.servers)
}

// Dedup removes duplicate servers from the pool (keeps first occurrence).
func (p *SSPool) Dedup() {
	p.mu.Lock()
	defer p.mu.Unlock()
	seen := make(map[string]bool, len(p.servers))
	unique := make([]SSConfig, 0, len(p.servers))
	for _, s := range p.servers {
		k := s.Key()
		if !seen[k] {
			seen[k] = true
			unique = append(unique, s)
		}
	}
	p.servers = unique
	if p.index >= len(p.servers) && len(p.servers) > 0 {
		p.index = p.index % len(p.servers)
	}
}

// GetByAddr returns the SSConfig matching the given host:port address.
// Returns nil if no match is found.
func (p *SSPool) GetByAddr(addr string) *SSConfig {
	p.mu.RLock()
	defer p.mu.RUnlock()
	host, portStr, err := net.SplitHostPort(addr)
	if err != nil {
		return nil
	}
	port, err := strconv.Atoi(portStr)
	if err != nil {
		return nil
	}
	for _, s := range p.servers {
		if s.Server == host && s.Port == port {
			return &s
		}
	}
	return nil
}
