package sspool

import (
	"context"
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

// defaultMaxConcurrentChecks is the fallback concurrency limit when maxConcurrent <= 0.
const defaultMaxConcurrentChecks = 20

// HealthCheckFast performs TCP dial checks with context cancellation support
// and configurable maxConcurrent limit. It returns only servers that are reachable.
//
// If maxConcurrent <= 0, defaultMaxConcurrentChecks (20) is used.
// If ctx is cancelled, remaining checks are skipped and nil is returned.
func HealthCheckFast(ctx context.Context, servers []SSConfig, timeout time.Duration, maxConcurrent int) []SSConfig {
	if len(servers) == 0 {
		return nil
	}
	if maxConcurrent <= 0 {
		maxConcurrent = defaultMaxConcurrentChecks
	}

	type result struct {
		cfg   SSConfig
		alive bool
	}

	results := make([]result, len(servers))
	sem := make(chan struct{}, maxConcurrent)
	var wg sync.WaitGroup

	for i, s := range servers {
		select {
		case <-ctx.Done():
			continue
		default:
		}

		wg.Add(1)
		go func(idx int, cfg SSConfig) {
			defer wg.Done()

			select {
			case sem <- struct{}{}:
			case <-ctx.Done():
				return
			}
			defer func() { <-sem }()

			select {
			case <-ctx.Done():
				return
			default:
			}

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
