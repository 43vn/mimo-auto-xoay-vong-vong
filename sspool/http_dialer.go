package sspool

import (
	"bufio"
	"context"
	"encoding/base64"
	"fmt"
	"net"
	"net/http"
	"strconv"
)

// HTTPDialer connects to target addresses through an HTTP CONNECT proxy.
type HTTPDialer struct {
	serverAddr string // host:port of the HTTP proxy
	auth       string // optional Basic auth header value
}

// NewHTTPDialer creates an HTTPDialer from an HTTPConfig.
func NewHTTPDialer(cfg HTTPConfig) (*HTTPDialer, error) {
	if cfg.Server == "" {
		return nil, fmt.Errorf("server address is required")
	}
	if cfg.Port <= 0 {
		return nil, fmt.Errorf("port must be positive")
	}

	d := &HTTPDialer{
		serverAddr: net.JoinHostPort(cfg.Server, strconv.Itoa(cfg.Port)),
	}

	// Set up Basic auth if credentials provided
	if cfg.Username != "" {
		credentials := cfg.Username + ":" + cfg.Password
		d.auth = "Basic " + base64.StdEncoding.EncodeToString([]byte(credentials))
	}

	return d, nil
}

// DialContext connects to addr through the HTTP CONNECT proxy.
// It is compatible with http.Transport.DialContext.
func (d *HTTPDialer) DialContext(ctx context.Context, network, addr string) (net.Conn, error) {
	// Connect to the proxy server
	var nd net.Dialer
	proxyConn, err := nd.DialContext(ctx, network, d.serverAddr)
	if err != nil {
		return nil, fmt.Errorf("dial HTTP proxy %s: %w", d.serverAddr, err)
	}

	// Build HTTP CONNECT request
	req, err := http.NewRequest(http.MethodConnect, "http://"+addr, nil)
	if err != nil {
		proxyConn.Close()
		return nil, fmt.Errorf("create CONNECT request: %w", err)
	}
	req.Header.Set("Host", addr)
	if d.auth != "" {
		req.Header.Set("Proxy-Authorization", d.auth)
	}

	// Write request
	err = req.Write(proxyConn)
	if err != nil {
		proxyConn.Close()
		return nil, fmt.Errorf("write CONNECT request: %w", err)
	}

	// Read response
	br := bufio.NewReader(proxyConn)
	resp, err := http.ReadResponse(br, req)
	if err != nil {
		proxyConn.Close()
		return nil, fmt.Errorf("read CONNECT response: %w", err)
	}
	defer resp.Body.Close()

	// Check for 200 Connection Established
	if resp.StatusCode != http.StatusOK {
		proxyConn.Close()
		return nil, fmt.Errorf("CONNECT failed with status %d", resp.StatusCode)
	}

	// If the bufio reader has buffered data, we need to handle that
	// For simplicity, we'll just return the raw connection
	// The buffered reader may have consumed some data, but since we're
	// doing a CONNECT, there shouldn't be any body data after the headers
	return proxyConn, nil
}