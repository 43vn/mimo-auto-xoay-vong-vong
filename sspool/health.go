package sspool

import (
	"fmt"
	"net"
	"sync"
	"time"
)

const maxConcurrentChecks = 20

// HealthCheck performs TCP dial checks on all servers concurrently (max 20)
// and returns only the servers that are reachable within the given timeout.
func HealthCheck(servers []SSConfig, timeout time.Duration) []SSConfig {
	if len(servers) == 0 {
		return nil
	}

	type result struct {
		cfg   SSConfig
		alive bool
	}

	results := make([]result, len(servers))
	sem := make(chan struct{}, maxConcurrentChecks)
	var wg sync.WaitGroup

	for i, s := range servers {
		wg.Add(1)
		go func(idx int, cfg SSConfig) {
			defer wg.Done()
			sem <- struct{}{}        // acquire
			defer func() { <-sem }() // release

			addr := fmt.Sprintf("%s:%d", cfg.Server, cfg.Port)
			conn, err := net.DialTimeout("tcp", addr, timeout)
			if err == nil {
				conn.Close()
				results[idx] = result{cfg: cfg, alive: true}
			} else {
				results[idx] = result{cfg: cfg, alive: false}
			}
		}(i, s)
	}

	wg.Wait()

	alive := make([]SSConfig, 0, len(servers))
	for _, r := range results {
		if r.alive {
			alive = append(alive, r.cfg)
		}
	}

	return alive
}
