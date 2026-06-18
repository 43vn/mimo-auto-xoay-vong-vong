package main

import (
	"bufio"
	"flag"
	"fmt"
	"log"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/vincent/mimo-xoay/proxy"
	"github.com/vincent/mimo-xoay/rotator"
	"github.com/vincent/mimo-xoay/sspool"
)

var (
	validModes = map[string]bool{
		"direct": true, "auto": true, "custom": true,
		"fallback": true, "fallback-proxy": true,
	}
)

func main() {
	mode := flag.String("mode", "direct", "Proxy mode: direct, auto, custom, fallback, fallback-proxy")
	proxyURL := flag.String("proxy", "", "SS server URL for custom/fallback-proxy mode (ss://method:password@host:port)")
	port := flag.Int("port", 18084, "HTTP listen port")
	proxyTimeout := flag.Int("proxy-timeout", 0, "Upstream proxy timeout in seconds (0 = default 60s)")
	proxyFile := flag.String("proxy-file", "", "File containing SS servers (one per line)")
	saveProxy := flag.String("save-proxy", "", "Save proxy pool to file")
	disableRateLimit := flag.Bool("disable-rate-limit", false, "Disable local rate limiter window (rely on upstream only)")
	flag.Parse()

	if !validModes[*mode] {
		log.Fatalf("invalid mode %q: must be direct, auto, custom, fallback, or fallback-proxy", *mode)
	}

	if (*mode == "custom" || *mode == "fallback-proxy") && *proxyURL == "" && *proxyFile == "" {
		log.Fatalf("--proxy or --proxy-file is required for --mode %s", *mode)
	}

	pool := sspool.NewSSPool()

	// Load upstreams based on mode
	switch *mode {
	case "auto", "fallback", "fallback-proxy":
		servers, err := fetchServers(*proxyURL, *proxyFile)
		if err != nil {
			log.Printf("[WARN] failed to fetch upstreams: %v", err)
		} else {
			for _, s := range servers {
				pool.Add(s)
			}
			log.Printf("[Pool] loaded %d upstreams", pool.Len())
		}
	case "custom":
		if *proxyURL != "" {
			cfg, err := sspool.ParseSSServer(*proxyURL)
			if err != nil {
				log.Fatalf("invalid --proxy URL: %v", err)
			}
			pool.Add(cfg)
		}
		if *proxyFile != "" {
			loadFromFile(pool, *proxyFile)
		}
	}

	// Health check at startup
	if pool.Len() > 0 {
		alive := sspool.HealthCheck(allServers(pool), 5*time.Second)
		pool = rebuildPool(alive)
		log.Printf("[Health] %d upstreams alive after check", pool.Len())
	}

	// Build rotator pool (string addresses)
	addresses := poolAddresses(pool)
	r := rotator.New(addresses, 0, func(addr string) {
		log.Printf("[Rotator] switched to %s", addr)
	})

	// JWT manager
	jwtMgr := proxy.NewJWTManager(proxy.BootstrapURL, generateFingerprint())

	// Build server options
	opts := &proxy.Options{
		DisableRateLimit: *disableRateLimit,
	}
	// mode=auto disables the 2s min-interval between requests
	if *mode == "auto" {
		opts.MinInterval = 0
	} else {
		opts.MinInterval = 2 * time.Second
	}
	if *proxyTimeout > 0 {
		opts.ProxyTimeout = time.Duration(*proxyTimeout) * time.Second
	} else {
		opts.ProxyTimeout = proxy.ProxyTimeout
	}

	// Create and start server
	srv := proxy.NewServer(pool, jwtMgr, r, *port, opts)

	// Background: health check every 60s
	go func() {
		ticker := time.NewTicker(60 * time.Second)
		defer ticker.Stop()
		for range ticker.C {
			current := allServers(pool)
			alive := sspool.HealthCheck(current, 5*time.Second)
			rebuildPoolInPlace(pool, alive)
		}
	}()

	// Background: refresh pool every 300s (auto/fallback modes)
	if *mode == "auto" || *mode == "fallback" || *mode == "fallback-proxy" {
		go func() {
			ticker := time.NewTicker(300 * time.Second)
			defer ticker.Stop()
			for range ticker.C {
				newServers, err := fetchServers("", "")
				if err != nil {
					log.Printf("[Refresh] fetch failed: %v", err)
					continue
				}
				added := 0
				for _, s := range newServers {
					if pool.Len() == 0 || pool.Get() != nil {
						pool.Add(s)
						added++
					}
				}
				if added > 0 {
					log.Printf("[Refresh] added %d new upstreams (total: %d)", added, pool.Len())
				}
				// Save if requested
				if *saveProxy != "" {
					savePoolToFile(pool, *saveProxy)
				}
			}
		}()
	}

	// Save proxy pool if requested
	if *saveProxy != "" && pool.Len() > 0 {
		savePoolToFile(pool, *saveProxy)
	}

	// Print startup banner
	modeDesc := map[string]string{
		"direct":         "direct (no proxy)",
		"auto":           fmt.Sprintf("auto (rotate from pool, %d upstreams)", pool.Len()),
		"custom":         fmt.Sprintf("custom (%s)", *proxyURL),
		"fallback":       fmt.Sprintf("fallback (direct -> pool, %d upstreams)", pool.Len()),
		"fallback-proxy": fmt.Sprintf("fallback-proxy (%s -> pool)", *proxyURL),
	}

	fmt.Printf("[+] MiMo SS Proxy is running!\n")
	fmt.Printf(" -> Proxy Mode: %s\n", modeDesc[*mode])
	fmt.Printf(" -> Proxy Timeout: %ds\n", int(opts.ProxyTimeout.Seconds()))
	fmt.Printf(" -> Rate Limit: %s\n", rateLimitDesc(*disableRateLimit, *mode))
	fmt.Printf(" -> Max concurrent: 8\n")
	fmt.Printf(" -> OpenAI Endpoint: http://localhost:%d/v1/chat/completions\n", *port)
	fmt.Printf(" -> API Key: any string\n")
	fmt.Printf(" -> Model: mimo-auto (or anything)\n")

	// Graceful shutdown
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		<-sigCh
		log.Println("[Shutdown] received signal, shutting down...")
		r.Stop()
		srv.Shutdown(nil)
	}()

	if err := srv.Start(); err != nil {
		log.Fatalf("server error: %v", err)
	}
}

// fetchServers loads SS servers from shadowmere API and/or file.
func fetchServers(proxyURL, proxyFile string) ([]sspool.SSConfig, error) {
	var all []sspool.SSConfig

	// Fetch from shadowmere API (flat JSON array or paginated)
 servers, err := sspool.FetchFromShadowmere("https://shadowmere.xyz/api/sub")
	if err != nil {
		log.Printf("[Fetcher] shadowmere error: %v", err)
	} else {
		all = append(all, servers...)
	}

	// Load from file
	if proxyFile != "" {
		loadFromFile(nil, proxyFile) // just log, pool handled separately
	}

	return all, nil
}

// loadFromFile reads SS URIs from a file (one per line).
func loadFromFile(pool *sspool.SSPool, filePath string) {
	f, err := os.Open(filePath)
	if err != nil {
		log.Printf("[File] error opening %s: %v", filePath, err)
		return
	}
	defer f.Close()

	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		if !strings.HasPrefix(line, "ss://") {
			line = "ss://" + line
		}
		cfg, err := sspool.ParseSSServer(line)
		if err != nil {
			log.Printf("[File] skip invalid line: %v", err)
			continue
		}
		if pool != nil {
			pool.Add(cfg)
		}
	}
}

// allServers returns all servers from the pool (snapshot).
func allServers(pool *sspool.SSPool) []sspool.SSConfig {
	var servers []sspool.SSConfig
	// We need to iterate; use Get+Next pattern
	for i := 0; i < pool.Len(); i++ {
		if s := pool.Get(); s != nil {
			servers = append(servers, *s)
			pool.Next()
		}
	}
	return servers
}

// rebuildPool creates a new pool from alive servers.
func rebuildPool(alive []sspool.SSConfig) *sspool.SSPool {
	newPool := sspool.NewSSPool()
	for _, s := range alive {
		newPool.Add(s)
	}
	return newPool
}

// rebuildPoolInPlace clears and repopulates the pool.
func rebuildPoolInPlace(pool *sspool.SSPool, alive []sspool.SSConfig) {
	// Remove all, then add alive
	for pool.Len() > 0 {
		if s := pool.Get(); s != nil {
			pool.Remove(s.Key())
		} else {
			break
		}
	}
	for _, s := range alive {
		pool.Add(s)
	}
}

// poolAddresses returns all server addresses as "host:port" strings.
func poolAddresses(pool *sspool.SSPool) []string {
	var addrs []string
	for i := 0; i < pool.Len(); i++ {
		if s := pool.Get(); s != nil {
			addrs = append(addrs, fmt.Sprintf("%s:%d", s.Server, s.Port))
			pool.Next()
		}
	}
	return addrs
}

// savePoolToFile saves the current pool to a file.
func savePoolToFile(pool *sspool.SSPool, filePath string) {
	f, err := os.Create(filePath)
	if err != nil {
		log.Printf("[File] error creating %s: %v", filePath, err)
		return
	}
	defer f.Close()

	for i := 0; i < pool.Len(); i++ {
		if s := pool.Get(); s != nil {
			uri := fmt.Sprintf("ss://%s:%s@%s:%d", s.Method, s.Password, s.Server, s.Port)
			fmt.Fprintln(f, uri)
			pool.Next()
		}
	}
	log.Printf("[File] saved %d upstreams to %s", pool.Len(), filePath)
}

// generateFingerprint creates a fingerprint string.
func generateFingerprint() string {
	return fmt.Sprintf("mimo-%d", time.Now().UnixNano())
}

// rateLimitDesc returns a human-readable rate limit description.
func rateLimitDesc(disabled bool, mode string) string {
	if disabled {
		return "disabled (upstream only)"
	}
	if mode == "auto" {
		return "enabled (min-interval disabled for auto mode)"
	}
	return "enabled (2s min-interval)"
}
