package sspool

import (
	"encoding/base64"
	"fmt"
	"net"
	"net/url"
	"strconv"
	"strings"
)

// SSConfig holds a single Shadowsocks upstream server configuration.
type SSConfig struct {
	Server   string // host
	Port     int    // port
	Password string
	Method   string // cipher method, e.g. "aes-256-gcm"
}

// Key returns a unique dedup key for this server configuration.
func (c SSConfig) Key() string {
	return fmt.Sprintf("%s:%d:%s:%s", c.Server, c.Port, c.Password, c.Method)
}

// SOCKS5Config holds config for a SOCKS5 proxy.
type SOCKS5Config struct {
	Server   string
	Port     int
	Username string
	Password string
}

// HTTPConfig holds config for an HTTP/HTTPS CONNECT proxy.
type HTTPConfig struct {
	Server   string
	Port     int
	Username string
	Password string
	Scheme   string // "http" or "https"
}

// ProxyConfig is a unified config for any proxy type.
type ProxyConfig struct {
	Type     string // "ss", "socks5", "http", "https", "direct"
	Server   string
	Port     int
	Username string // for HTTP/SOCKS5 auth
	Password string
	Method   string // SS cipher method (only when Type=="ss")
}

// ParseSSServer parses a Shadowsocks URI of the form:
//
//	ss://method:password@host:port
//
// or with base64-encoded userinfo:
//
//	ss://base64(method:password)@host:port
func ParseSSServer(raw string) (SSConfig, error) {
	if raw == "" {
		return SSConfig{}, fmt.Errorf("empty URI")
	}

	// Must start with ss://
	if !strings.HasPrefix(raw, "ss://") {
		return SSConfig{}, fmt.Errorf("URI must start with ss://")
	}

	// Parse as URL to extract userinfo, host, port
	u, err := url.Parse(raw)
	if err != nil {
		return SSConfig{}, fmt.Errorf("invalid URI: %w", err)
	}

	if u.Host == "" {
		return SSConfig{}, fmt.Errorf("missing host")
	}

	// Split host:port
	host, portStr, err := net.SplitHostPort(u.Host)
	if err != nil {
		return SSConfig{}, fmt.Errorf("invalid host:port: %w", err)
	}
	if host == "" {
		return SSConfig{}, fmt.Errorf("missing host")
	}

	port, err := strconv.Atoi(portStr)
	if err != nil {
		return SSConfig{}, fmt.Errorf("invalid port: %w", err)
	}

	// Parse userinfo (method:password)
	userinfo := u.User.String()
	if userinfo == "" {
		return SSConfig{}, fmt.Errorf("missing userinfo (method:password)")
	}

	method, password, err := parseSSUserinfo(userinfo)
	if err != nil {
		return SSConfig{}, fmt.Errorf("invalid userinfo: %w", err)
	}

	return SSConfig{
		Server:   host,
		Port:     port,
		Password: password,
		Method:   method,
	}, nil
}

// parseSSUserinfo parses "method:password" or base64-encoded "method:password".
func parseSSUserinfo(raw string) (method, password string, err error) {
	decoded, err := base64.StdEncoding.DecodeString(raw)
	if err == nil {
		parts := strings.SplitN(string(decoded), ":", 2)
		if len(parts) == 2 {
			return parts[0], parts[1], nil
		}
	}

	parts := strings.SplitN(raw, ":", 2)
	if len(parts) == 2 {
		return parts[0], parts[1], nil
	}

	return "", "", fmt.Errorf("expected method:password format")
}

// ParseSOCKS5Server parses a SOCKS5 URI:
//
//	socks5://host:port
//	socks5://user:pass@host:port
func ParseSOCKS5Server(raw string) (SOCKS5Config, error) {
	if raw == "" {
		return SOCKS5Config{}, fmt.Errorf("empty URI")
	}
	if !strings.HasPrefix(raw, "socks5://") {
		return SOCKS5Config{}, fmt.Errorf("URI must start with socks5://")
	}

	u, err := url.Parse(raw)
	if err != nil {
		return SOCKS5Config{}, fmt.Errorf("invalid URI: %w", err)
	}
	if u.Host == "" {
		return SOCKS5Config{}, fmt.Errorf("missing host")
	}

	host, portStr, err := net.SplitHostPort(u.Host)
	if err != nil {
		return SOCKS5Config{}, fmt.Errorf("invalid host:port: %w", err)
	}
	if host == "" {
		return SOCKS5Config{}, fmt.Errorf("missing host")
	}

	port, err := strconv.Atoi(portStr)
	if err != nil {
		return SOCKS5Config{}, fmt.Errorf("invalid port: %w", err)
	}

	var username, password string
	if u.User != nil {
		username = u.User.Username()
		password, _ = u.User.Password()
	}

	return SOCKS5Config{
		Server:   host,
		Port:     port,
		Username: username,
		Password: password,
	}, nil
}

// ParseHTTPServer parses an HTTP/HTTPS proxy URI:
//
//	http://host:port
//	https://host:port
//	http://user:pass@host:port
func ParseHTTPServer(raw string) (HTTPConfig, error) {
	if raw == "" {
		return HTTPConfig{}, fmt.Errorf("empty URI")
	}
	if !strings.HasPrefix(raw, "http://") && !strings.HasPrefix(raw, "https://") {
		return HTTPConfig{}, fmt.Errorf("URI must start with http:// or https://")
	}

	u, err := url.Parse(raw)
	if err != nil {
		return HTTPConfig{}, fmt.Errorf("invalid URI: %w", err)
	}
	if u.Host == "" {
		return HTTPConfig{}, fmt.Errorf("missing host")
	}

	host, portStr, err := net.SplitHostPort(u.Host)
	if err != nil {
		return HTTPConfig{}, fmt.Errorf("invalid host:port: %w", err)
	}
	if host == "" {
		return HTTPConfig{}, fmt.Errorf("missing host")
	}

	port, err := strconv.Atoi(portStr)
	if err != nil {
		return HTTPConfig{}, fmt.Errorf("invalid port: %w", err)
	}

	var username, password string
	if u.User != nil {
		username = u.User.Username()
		password, _ = u.User.Password()
	}

	scheme := "http"
	if strings.HasPrefix(raw, "https://") {
		scheme = "https"
	}

	return HTTPConfig{
		Server:   host,
		Port:     port,
		Username: username,
		Password: password,
		Scheme:   scheme,
	}, nil
}

// ParseProxyURI auto-detects the scheme and returns a unified ProxyConfig.
func ParseProxyURI(raw string) (ProxyConfig, error) {
	if raw == "" {
		return ProxyConfig{}, fmt.Errorf("empty URI")
	}

	switch {
	case strings.HasPrefix(raw, "ss://"):
		cfg, err := ParseSSServer(raw)
		if err != nil {
			return ProxyConfig{}, err
		}
		return ProxyConfig{
			Type:     "ss",
			Server:   cfg.Server,
			Port:     cfg.Port,
			Password: cfg.Password,
			Method:   cfg.Method,
		}, nil

	case strings.HasPrefix(raw, "socks5://"):
		cfg, err := ParseSOCKS5Server(raw)
		if err != nil {
			return ProxyConfig{}, err
		}
		return ProxyConfig{
			Type:     "socks5",
			Server:   cfg.Server,
			Port:     cfg.Port,
			Username: cfg.Username,
			Password: cfg.Password,
		}, nil

	case strings.HasPrefix(raw, "http://"), strings.HasPrefix(raw, "https://"):
		cfg, err := ParseHTTPServer(raw)
		if err != nil {
			return ProxyConfig{}, err
		}
		proxyType := cfg.Scheme
		return ProxyConfig{
			Type:     proxyType,
			Server:   cfg.Server,
			Port:     cfg.Port,
			Username: cfg.Username,
			Password: cfg.Password,
		}, nil

	default:
		return ProxyConfig{}, fmt.Errorf("unsupported URI scheme %q", raw)
	}
}

// IsDirect returns true if the config indicates a direct (no-proxy) connection.
func (c ProxyConfig) IsDirect() bool {
	return c.Type == "direct" || c.Type == ""
}

// Addr returns "host:port".
func (c ProxyConfig) Addr() string {
	return net.JoinHostPort(c.Server, strconv.Itoa(c.Port))
}
