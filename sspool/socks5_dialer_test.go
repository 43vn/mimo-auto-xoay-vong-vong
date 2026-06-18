package sspool

import (
	"context"
	"fmt"
	"net"
	"testing"
)

func TestNewSOCKSDialer(t *testing.T) {
	tests := []struct {
		name    string
		cfg     SOCKS5Config
		wantErr bool
	}{
		{
			name:    "valid config",
			cfg:     SOCKS5Config{Server: "127.0.0.1", Port: 1080},
			wantErr: false,
		},
		{
			name:    "empty server",
			cfg:     SOCKS5Config{Server: "", Port: 1080},
			wantErr: true,
		},
		{
			name:    "zero port",
			cfg:     SOCKS5Config{Server: "127.0.0.1", Port: 0},
			wantErr: true,
		},
		{
			name:    "negative port",
			cfg:     SOCKS5Config{Server: "127.0.0.1", Port: -1},
			wantErr: true,
		},
		{
			name: "with auth",
			cfg:  SOCKS5Config{Server: "127.0.0.1", Port: 1080, Username: "user", Password: "pass"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dialer, err := NewSOCKSDialer(tt.cfg)
			if (err != nil) != tt.wantErr {
				t.Fatalf("NewSOCKSDialer() error = %v, wantErr %v", err, tt.wantErr)
			}
			if !tt.wantErr && dialer == nil {
				t.Fatal("NewSOCKSDialer() returned nil dialer without error")
			}
		})
	}
}

func TestSOCKSDialerDialContext(t *testing.T) {
	// Create a mock SOCKS5 server
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("failed to create listener: %v", err)
	}
	defer listener.Close()

	// Handle connections in a goroutine
	go func() {
		for {
			conn, err := listener.Accept()
			if err != nil {
				return // listener closed
			}
			go handleMockSOCKS5(conn, false)
		}
	}()

	// Parse the server address
	serverAddr := listener.Addr().String()
	host, portStr, err := net.SplitHostPort(serverAddr)
	if err != nil {
		t.Fatalf("failed to parse server address: %v", err)
	}
	var port int
	_, err = fmt.Sscanf(portStr, "%d", &port)
	if err != nil {
		t.Fatalf("failed to parse port: %v", err)
	}

	// Create the SOCKS5 dialer
	dialer, err := NewSOCKSDialer(SOCKS5Config{
		Server: host,
		Port:   port,
	})
	if err != nil {
		t.Fatalf("NewSOCKSDialer() error = %v", err)
	}

	// Test DialContext
	ctx := context.Background()
	conn, err := dialer.DialContext(ctx, "tcp", "example.com:443")
	if err != nil {
		t.Fatalf("DialContext() error = %v", err)
	}
	defer conn.Close()

	// Verify the connection is usable by writing data
	_, err = conn.Write([]byte("GET / HTTP/1.1\r\nHost: example.com\r\n\r\n"))
	if err != nil {
		t.Errorf("write to connection failed: %v", err)
	}
}

func TestSOCKSDialerDialContextWithAuth(t *testing.T) {
	// Create a mock SOCKS5 server with auth
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("failed to create listener: %v", err)
	}
	defer listener.Close()

	// Handle connections in a goroutine
	go func() {
		for {
			conn, err := listener.Accept()
			if err != nil {
				return // listener closed
			}
			go handleMockSOCKS5(conn, true)
		}
	}()

	// Parse the server address
	serverAddr := listener.Addr().String()
	host, portStr, err := net.SplitHostPort(serverAddr)
	if err != nil {
		t.Fatalf("failed to parse server address: %v", err)
	}
	var port int
	_, err = fmt.Sscanf(portStr, "%d", &port)
	if err != nil {
		t.Fatalf("failed to parse port: %v", err)
	}

	// Create the SOCKS5 dialer with auth
	dialer, err := NewSOCKSDialer(SOCKS5Config{
		Server:   host,
		Port:     port,
		Username: "testuser",
		Password: "testpass",
	})
	if err != nil {
		t.Fatalf("NewSOCKSDialer() error = %v", err)
	}

	// Test DialContext
	ctx := context.Background()
	conn, err := dialer.DialContext(ctx, "tcp", "example.com:443")
	if err != nil {
		t.Fatalf("DialContext() error = %v", err)
	}
	defer conn.Close()

	// Verify the connection is usable
	_, err = conn.Write([]byte("GET / HTTP/1.1\r\nHost: example.com\r\n\r\n"))
	if err != nil {
		t.Errorf("write to connection failed: %v", err)
	}
}

func handleMockSOCKS5(conn net.Conn, requireAuth bool) {
	defer conn.Close()

	// Use readFull to ensure we read complete messages from the stream.
	buf := make([]byte, 256)

	// Read greeting: version (1) + nmethods (1) + methods (nmethods)
	_, err := readFull(conn, buf[:2])
	if err != nil {
		return
	}
	if buf[0] != 0x05 {
		return // not SOCKS5
	}
	nmethods := int(buf[1])
	if nmethods > 0 {
		_, err = readFull(conn, buf[:nmethods])
		if err != nil {
			return
		}
	}

	// Send available methods
	if requireAuth {
		// Require username/password auth
		conn.Write([]byte{0x05, 0x02})
	} else {
		// No authentication required
		conn.Write([]byte{0x05, 0x00})
	}

	// If auth is required, handle username/password auth
	if requireAuth {
		// Read auth subnegotiation: version (1) + ulen (1)
		_, err = readFull(conn, buf[:2])
		if err != nil {
			return
		}
		if buf[0] != 0x01 {
			return
		}
		ulen := int(buf[1])

		// Read username
		_, err = readFull(conn, buf[:ulen])
		if err != nil {
			return
		}
		username := string(buf[:ulen])

		// Read plen (1 byte)
		_, err = readFull(conn, buf[:1])
		if err != nil {
			return
		}
		plen := int(buf[0])

		// Read password
		_, err = readFull(conn, buf[:plen])
		if err != nil {
			return
		}
		password := string(buf[:plen])

		// Check credentials
		if username != "testuser" || password != "testpass" {
			conn.Write([]byte{0x01, 0x01}) // failure
			return
		}
		conn.Write([]byte{0x01, 0x00}) // success
	}

	// Read connect request header: version (1) + cmd (1) + rsv (1) + atyp (1)
	_, err = readFull(conn, buf[:4])
	if err != nil {
		return
	}
	if buf[0] != 0x05 || buf[1] != 0x01 {
		return
	}

	// Parse address based on type
	addrType := buf[3]
	switch addrType {
	case 0x01: // IPv4
		_, err = readFull(conn, buf[:6]) // 4 bytes IPv4 + 2 bytes port
	case 0x03: // Domain
		_, err = readFull(conn, buf[:1]) // domain length
		if err != nil {
			return
		}
		domainLen := int(buf[0])
		_, err = readFull(conn, buf[:domainLen+2]) // domain + 2 bytes port
	case 0x04: // IPv6
		_, err = readFull(conn, buf[:18]) // 16 bytes IPv6 + 2 bytes port
	default:
		return
	}
	if err != nil {
		return
	}

	// Send success reply with dummy bind address (0.0.0.0:0)
	// Reply format: version, reply, reserved, address type, bind address, bind port
	reply := []byte{0x05, 0x00, 0x00, 0x01, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00}
	conn.Write(reply)

	// For testing, just keep the connection open and echo any data
	// In a real proxy, this would relay to the target
	buf2 := make([]byte, 4096)
	for {
		n, err := conn.Read(buf2)
		if err != nil {
			return
		}
		// Echo back for test verification
		conn.Write(buf2[:n])
	}
}