package sspool

import (
	"fmt"
	"net"
	"strconv"
	"sync"
	"time"
)

// SSPool is a concurrent-safe pool of Shadowsocks servers with round-robin rotation.
type SSPool struct {
	mu        sync.RWMutex
	servers   []SSConfig
	index     int
	deadAddrs map[string]time.Time
}

// NewSSPool creates an empty SSPool.
func NewSSPool() *SSPool {
	return &SSPool{
		deadAddrs: make(map[string]time.Time),
	}
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

// ReplaceAll atomically replaces all servers and resets the round-robin index.
func (p *SSPool) ReplaceAll(servers []SSConfig) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.servers = servers
	p.index = 0
}

// Snapshot returns a copy of all servers for safe concurrent access.
func (p *SSPool) Snapshot() []SSConfig {
	p.mu.RLock()
	defer p.mu.RUnlock()
	result := make([]SSConfig, len(p.servers))
	copy(result, p.servers)
	return result
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

// MarkDead marks the given host:port address as dead.
func (p *SSPool) MarkDead(addr string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.deadAddrs[addr] = time.Now()
}

// UnmarkDead removes the given address from the dead list.
func (p *SSPool) UnmarkDead(addr string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	delete(p.deadAddrs, addr)
}

// IsDead returns true if the given address is currently marked as dead.
func (p *SSPool) IsDead(addr string) bool {
	p.mu.RLock()
	defer p.mu.RUnlock()
	_, ok := p.deadAddrs[addr]
	return ok
}

// SnapshotAlive returns a copy of all non-dead servers.
func (p *SSPool) SnapshotAlive() []SSConfig {
	p.mu.RLock()
	defer p.mu.RUnlock()

	result := make([]SSConfig, 0, len(p.servers))
	for _, s := range p.servers {
		addr := fmt.Sprintf("%s:%d", s.Server, s.Port)
		if _, dead := p.deadAddrs[addr]; !dead {
			result = append(result, s)
		}
	}
	return result
}
