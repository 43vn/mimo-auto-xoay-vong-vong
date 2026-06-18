package sspool

import (
	"net"
	"testing"
	"time"
)

func startTCPServer(t *testing.T) (string, int, func()) {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("failed to start TCP server: %v", err)
	}
	addr := ln.Addr().(*net.TCPAddr)
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			conn.Close()
		}
	}()
	return addr.IP.String(), addr.Port, func() { ln.Close() }
}

func TestHealthCheckAlive(t *testing.T) {
	host, port, cleanup := startTCPServer(t)
	defer cleanup()

	servers := []SSConfig{
		{Server: host, Port: port, Password: "a", Method: "aes-256-gcm"},
	}

	alive := HealthCheck(servers, 2*time.Second)
	if len(alive) != 1 {
		t.Fatalf("HealthCheck returned %d servers, want 1", len(alive))
	}
	if alive[0].Server != host {
		t.Errorf("alive[0].Server = %q, want %q", alive[0].Server, host)
	}
}

func TestHealthCheckDead(t *testing.T) {
	servers := []SSConfig{
		{Server: "127.0.0.1", Port: 1, Password: "a", Method: "aes-256-gcm"}, // port 1 likely unused
	}

	alive := HealthCheck(servers, 500*time.Millisecond)
	if len(alive) != 0 {
		t.Errorf("HealthCheck returned %d servers for dead server, want 0", len(alive))
	}
}

func TestHealthCheckMixed(t *testing.T) {
	host, port, cleanup := startTCPServer(t)
	defer cleanup()

	servers := []SSConfig{
		{Server: host, Port: port, Password: "a", Method: "aes-256-gcm"},
		{Server: "127.0.0.1", Port: 1, Password: "b", Method: "aes-128-gcm"}, // dead
	}

	alive := HealthCheck(servers, 500*time.Millisecond)
	if len(alive) != 1 {
		t.Fatalf("HealthCheck returned %d servers, want 1", len(alive))
	}
	if alive[0].Port != port {
		t.Errorf("alive[0].Port = %d, want %d", alive[0].Port, port)
	}
}

func TestHealthCheckEmpty(t *testing.T) {
	alive := HealthCheck(nil, 1*time.Second)
	if len(alive) != 0 {
		t.Errorf("HealthCheck(nil) returned %d servers, want 0", len(alive))
	}
}
