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

	method, password, err := parseUserinfo(userinfo)
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

// parseUserinfo parses "method:password" or base64-encoded "method:password".
func parseUserinfo(raw string) (method, password string, err error) {
	decoded, err := base64.StdEncoding.DecodeString(raw)
	if err == nil {
		// Successfully decoded as base64
		parts := strings.SplitN(string(decoded), ":", 2)
		if len(parts) == 2 {
			return parts[0], parts[1], nil
		}
	}

	// Try as plain text
	parts := strings.SplitN(raw, ":", 2)
	if len(parts) == 2 {
		return parts[0], parts[1], nil
	}

	return "", "", fmt.Errorf("expected method:password format")
}


