package sspool

import (
	"context"
	"encoding/base64"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestNewHTTPDialer(t *testing.T) {
	tests := []struct {
		name    string
		cfg     HTTPConfig
		wantErr bool
	}{
		{
			name:    "valid config",
			cfg:     HTTPConfig{Server: "127.0.0.1", Port: 8080},
			wantErr: false,
		},
		{
			name:    "empty server",
			cfg:     HTTPConfig{Server: "", Port: 8080},
			wantErr: true,
		},
		{
			name:    "zero port",
			cfg:     HTTPConfig{Server: "127.0.0.1", Port: 0},
			wantErr: true,
		},
		{
			name:    "negative port",
			cfg:     HTTPConfig{Server: "127.0.0.1", Port: -1},
			wantErr: true,
		},
		{
			name: "with auth",
			cfg:  HTTPConfig{Server: "127.0.0.1", Port: 8080, Username: "user", Password: "pass"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dialer, err := NewHTTPDialer(tt.cfg)
			if (err != nil) != tt.wantErr {
				t.Fatalf("NewHTTPDialer() error = %v, wantErr %v", err, tt.wantErr)
			}
			if !tt.wantErr && dialer == nil {
				t.Fatal("NewHTTPDialer() returned nil dialer without error")
			}
		})
	}
}

func TestHTTPDialerDialContext(t *testing.T) {
	// Create a mock HTTP CONNECT proxy
	proxyServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodConnect {
			t.Errorf("expected CONNECT method, got %s", r.Method)
			http.Error(w, "Method Not Allowed", http.StatusMethodNotAllowed)
			return
		}
		if r.URL.Host == "" {
			t.Error("expected Host header")
			http.Error(w, "Bad Request", http.StatusBadRequest)
			return
		}

		// Hijack the connection to simulate a successful CONNECT
		hijacker, ok := w.(http.Hijacker)
		if !ok {
			t.Error("ResponseWriter does not support hijacking")
			http.Error(w, "Internal Server Error", http.StatusInternalServerError)
			return
		}

		conn, bufrw, err := hijacker.Hijack()
		if err != nil {
			t.Errorf("hijack failed: %v", err)
			return
		}
		defer conn.Close()

		// Send 200 Connection Established
		_, err = bufrw.WriteString("HTTP/1.1 200 Connection Established\r\n\r\n")
		if err != nil {
			t.Errorf("write response failed: %v", err)
			return
		}
		bufrw.Flush()

		// Read some data from the client to verify connection works
		bufrw.ReadString('\n')
	}))
	defer proxyServer.Close()

	// Parse the proxy server address
	proxyAddr := proxyServer.Listener.Addr().String()
	host, portStr, err := net.SplitHostPort(proxyAddr)
	if err != nil {
		t.Fatalf("failed to parse proxy address: %v", err)
	}
	var port int
	_, err = fmt.Sscanf(portStr, "%d", &port)
	if err != nil {
		t.Fatalf("failed to parse port: %v", err)
	}

	// Create the HTTP dialer
	dialer, err := NewHTTPDialer(HTTPConfig{
		Server: host,
		Port:   port,
	})
	if err != nil {
		t.Fatalf("NewHTTPDialer() error = %v", err)
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

func TestHTTPDialerDialContextWithAuth(t *testing.T) {
	// Create a mock HTTP CONNECT proxy with auth
	proxyServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Check for Proxy-Authorization header
		auth := r.Header.Get("Proxy-Authorization")
		if auth == "" {
			http.Error(w, "Proxy Authentication Required", http.StatusProxyAuthRequired)
			return
		}
		expected := "Basic " + base64.StdEncoding.EncodeToString([]byte("testuser:testpass"))
		if auth != expected {
			http.Error(w, "Forbidden", http.StatusForbidden)
			return
		}

		// Hijack the connection
		hijacker, ok := w.(http.Hijacker)
		if !ok {
			http.Error(w, "Internal Server Error", http.StatusInternalServerError)
			return
		}

		conn, bufrw, err := hijacker.Hijack()
		if err != nil {
			t.Errorf("hijack failed: %v", err)
			return
		}
		defer conn.Close()

		// Send 200 Connection Established
		_, err = bufrw.WriteString("HTTP/1.1 200 Connection Established\r\n\r\n")
		if err != nil {
			t.Errorf("write response failed: %v", err)
			return
		}
		bufrw.Flush()

		// Read some data from the client
		bufrw.ReadString('\n')
	}))
	defer proxyServer.Close()

	// Parse the proxy server address
	proxyAddr := proxyServer.Listener.Addr().String()
	host, portStr, err := net.SplitHostPort(proxyAddr)
	if err != nil {
		t.Fatalf("failed to parse proxy address: %v", err)
	}
	var port int
	_, err = fmt.Sscanf(portStr, "%d", &port)
	if err != nil {
		t.Fatalf("failed to parse port: %v", err)
	}

	// Create the HTTP dialer with auth
	dialer, err := NewHTTPDialer(HTTPConfig{
		Server:   host,
		Port:     port,
		Username: "testuser",
		Password: "testpass",
	})
	if err != nil {
		t.Fatalf("NewHTTPDialer() error = %v", err)
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