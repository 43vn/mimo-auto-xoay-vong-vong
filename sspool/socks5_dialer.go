package sspool

import (
	"context"
	"fmt"
	"net"
	"strconv"
)

// SOCKSDialer connects to target addresses through a SOCKS5 proxy.
type SOCKSDialer struct {
	serverAddr string // host:port of the SOCKS5 proxy
	username   string // optional username
	password   string // optional password
}

// NewSOCKSDialer creates a SOCKSDialer from a SOCKS5Config.
func NewSOCKSDialer(cfg SOCKS5Config) (*SOCKSDialer, error) {
	if cfg.Server == "" {
		return nil, fmt.Errorf("server address is required")
	}
	if cfg.Port <= 0 {
		return nil, fmt.Errorf("port must be positive")
	}

	return &SOCKSDialer{
		serverAddr: net.JoinHostPort(cfg.Server, strconv.Itoa(cfg.Port)),
		username:   cfg.Username,
		password:   cfg.Password,
	}, nil
}

// DialContext connects to addr through the SOCKS5 proxy.
// It is compatible with http.Transport.DialContext.
func (d *SOCKSDialer) DialContext(ctx context.Context, network, addr string) (net.Conn, error) {
	// Connect to the SOCKS5 server
	var nd net.Dialer
	conn, err := nd.DialContext(ctx, network, d.serverAddr)
	if err != nil {
		return nil, fmt.Errorf("dial SOCKS5 server %s: %w", d.serverAddr, err)
	}

	// Perform SOCKS5 handshake
	if err := d.socks5Handshake(conn, addr); err != nil {
		conn.Close()
		return nil, err
	}

	return conn, nil
}

// socks5Handshake performs the SOCKS5 protocol handshake.
func (d *SOCKSDialer) socks5Handshake(conn net.Conn, targetAddr string) error {
	// Step 1: Send greeting with supported auth methods
	// Method 0x00: No authentication required
	// Method 0x02: Username/Password authentication
	greeting := []byte{0x05, 0x02, 0x00, 0x02}
	if d.username == "" && d.password == "" {
		// Only offer no-auth
		greeting = []byte{0x05, 0x01, 0x00}
	}

	if _, err := conn.Write(greeting); err != nil {
		return fmt.Errorf("write greeting: %w", err)
	}

	// Step 2: Read server's chosen method
	resp := make([]byte, 2)
	if _, err := readFull(conn, resp); err != nil {
		return fmt.Errorf("read method selection: %w", err)
	}

	if resp[0] != 0x05 {
		return fmt.Errorf("invalid SOCKS version: %d", resp[0])
	}

	method := resp[1]
	switch method {
	case 0x00:
		// No authentication required, proceed
	case 0x02:
		// Username/Password authentication required
		if err := d.authenticate(conn); err != nil {
			return err
		}
	case 0xFF:
		return fmt.Errorf("no acceptable auth methods")
	default:
		return fmt.Errorf("unsupported auth method: %d", method)
	}

	// Step 3: Send connect request
	if err := d.sendConnectRequest(conn, targetAddr); err != nil {
		return err
	}

	// Step 4: Read connect response
	if err := d.readConnectResponse(conn); err != nil {
		return err
	}

	return nil
}

// authenticate performs Username/Password authentication (RFC 1929).
func (d *SOCKSDialer) authenticate(conn net.Conn) error {
	// Build auth request
	// Version: 0x01
	// Username length: 1 byte
	// Username: variable
	// Password length: 1 byte
	// Password: variable
	usernameBytes := []byte(d.username)
	passwordBytes := []byte(d.password)

	if len(usernameBytes) > 255 {
		return fmt.Errorf("username too long (max 255)")
	}
	if len(passwordBytes) > 255 {
		return fmt.Errorf("password too long (max 255)")
	}

	authRequest := make([]byte, 0, 3+len(usernameBytes)+len(passwordBytes))
	authRequest = append(authRequest, 0x01) // version
	authRequest = append(authRequest, byte(len(usernameBytes)))
	authRequest = append(authRequest, usernameBytes...)
	authRequest = append(authRequest, byte(len(passwordBytes)))
	authRequest = append(authRequest, passwordBytes...)

	if _, err := conn.Write(authRequest); err != nil {
		return fmt.Errorf("write auth request: %w", err)
	}

	// Read auth response
	resp := make([]byte, 2)
	if _, err := readFull(conn, resp); err != nil {
		return fmt.Errorf("read auth response: %w", err)
	}

	if resp[0] != 0x01 {
		return fmt.Errorf("invalid auth version: %d", resp[0])
	}
	if resp[1] != 0x00 {
		return fmt.Errorf("authentication failed")
	}

	return nil
}

// sendConnectRequest sends a SOCKS5 CONNECT request.
func (d *SOCKSDialer) sendConnectRequest(conn net.Conn, targetAddr string) error {
	// Parse target address
	host, portStr, err := net.SplitHostPort(targetAddr)
	if err != nil {
		return fmt.Errorf("invalid target address: %w", err)
	}

	port, err := strconv.Atoi(portStr)
	if err != nil {
		return fmt.Errorf("invalid port: %w", err)
	}

	// Build connect request
	// Version: 0x05
	// Command: 0x01 (CONNECT)
	// Reserved: 0x00
	// Address type: 0x01 (IPv4), 0x03 (Domain), 0x04 (IPv6)
	// Address: variable
	// Port: 2 bytes (big-endian)
	request := []byte{0x05, 0x01, 0x00}

	// Determine address type
	ip := net.ParseIP(host)
	if ip4 := ip.To4(); ip4 != nil {
		// IPv4
		request = append(request, 0x01)
		request = append(request, ip4...)
	} else if ip6 := ip.To16(); ip6 != nil {
		// IPv6
		request = append(request, 0x04)
		request = append(request, ip6...)
	} else {
		// Domain name
		if len(host) > 255 {
			return fmt.Errorf("domain name too long")
		}
		request = append(request, 0x03)
		request = append(request, byte(len(host)))
		request = append(request, []byte(host)...)
	}

	// Add port (big-endian)
	request = append(request, byte(port>>8), byte(port))

	if _, err := conn.Write(request); err != nil {
		return fmt.Errorf("write connect request: %w", err)
	}

	return nil
}

// readConnectResponse reads the SOCKS5 CONNECT response.
func (d *SOCKSDialer) readConnectResponse(conn net.Conn) error {
	// Read header (version, reply, reserved, address type)
	header := make([]byte, 4)
	if _, err := readFull(conn, header); err != nil {
		return fmt.Errorf("read connect response header: %w", err)
	}

	if header[0] != 0x05 {
		return fmt.Errorf("invalid SOCKS version: %d", header[0])
	}

	if header[1] != 0x00 {
		// Map reply code to error message
		var errMsg string
		switch header[1] {
		case 0x01:
			errMsg = "general SOCKS server failure"
		case 0x02:
			errMsg = "connection not allowed by ruleset"
		case 0x03:
			errMsg = "network unreachable"
		case 0x04:
			errMsg = "host unreachable"
		case 0x05:
			errMsg = "connection refused"
		case 0x06:
			errMsg = "TTL expired"
		case 0x07:
			errMsg = "command not supported"
		case 0x08:
			errMsg = "address type not supported"
		default:
			errMsg = fmt.Sprintf("unknown error: %d", header[1])
		}
		return fmt.Errorf("SOCKS5 connect failed: %s", errMsg)
	}

	// Read the bound address based on address type
	switch header[3] {
	case 0x01: // IPv4
		addr := make([]byte, 4+2) // 4 bytes IPv4 + 2 bytes port
		if _, err := readFull(conn, addr); err != nil {
			return fmt.Errorf("read IPv4 bind address: %w", err)
		}
	case 0x03: // Domain
		domainLen := make([]byte, 1)
		if _, err := readFull(conn, domainLen); err != nil {
			return fmt.Errorf("read domain length: %w", err)
		}
		addr := make([]byte, int(domainLen[0])+2) // domain + 2 bytes port
		if _, err := readFull(conn, addr); err != nil {
			return fmt.Errorf("read domain bind address: %w", err)
		}
	case 0x04: // IPv6
		addr := make([]byte, 16+2) // 16 bytes IPv6 + 2 bytes port
		if _, err := readFull(conn, addr); err != nil {
			return fmt.Errorf("read IPv6 bind address: %w", err)
		}
	default:
		return fmt.Errorf("unsupported address type: %d", header[3])
	}

	return nil
}

// readFull reads exactly len(buf) bytes from conn.
func readFull(conn net.Conn, buf []byte) (int, error) {
	total := 0
	for total < len(buf) {
		n, err := conn.Read(buf[total:])
		total += n
		if err != nil {
			return total, err
		}
	}
	return total, nil
}