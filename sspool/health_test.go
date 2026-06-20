package sspool

import (
	"context"
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

func TestHealthCheckFastContextCancel(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // immediately cancel

	servers := []SSConfig{
		{Server: "1.2.3.4", Port: 1, Password: "a", Method: "aes-256-gcm"},
		{Server: "1.2.3.5", Port: 2, Password: "b", Method: "aes-256-gcm"},
		{Server: "1.2.3.6", Port: 3, Password: "c", Method: "aes-256-gcm"},
		{Server: "1.2.3.7", Port: 4, Password: "d", Method: "aes-256-gcm"},
		{Server: "1.2.3.8", Port: 5, Password: "e", Method: "aes-256-gcm"},
	}

	alive := HealthCheckFast(ctx, servers, 1*time.Second, 5)
	if len(alive) != 0 {
		t.Errorf("HealthCheckFast with cancelled context returned %d servers, want 0", len(alive))
	}
}

func TestHealthCheckFastContextBackground(t *testing.T) {
	host, port, cleanup := startTCPServer(t)
	defer cleanup()

	servers := []SSConfig{
		{Server: host, Port: port, Password: "a", Method: "aes-256-gcm"},
	}

	alive := HealthCheckFast(context.Background(), servers, 2*time.Second, 5)
	if len(alive) != 1 {
		t.Fatalf("HealthCheckFast returned %d servers, want 1", len(alive))
	}
	if alive[0].Server != host {
		t.Errorf("alive[0].Server = %q, want %q", alive[0].Server, host)
	}
}

func TestHealthCheckFastMaxConcurrentDefault(t *testing.T) {
	host, port, cleanup := startTCPServer(t)
	defer cleanup()

	servers := []SSConfig{
		{Server: host, Port: port, Password: "a", Method: "aes-256-gcm"},
	}

	alive := HealthCheckFast(context.Background(), servers, 2*time.Second, 0)
	if len(alive) != 1 {
		t.Fatalf("HealthCheckFast with maxConcurrent=0 returned %d servers, want 1", len(alive))
	}
}

func TestHealthCheckFastMaxConcurrent1(t *testing.T) {
	host, port, cleanup := startTCPServer(t)
	defer cleanup()

	servers := []SSConfig{
		{Server: host, Port: port, Password: "a", Method: "aes-256-gcm"},
		{Server: "127.0.0.1", Port: 1, Password: "b", Method: "aes-128-gcm"}, // dead
	}

	alive := HealthCheckFast(context.Background(), servers, 500*time.Millisecond, 1)
	if len(alive) != 1 {
		t.Fatalf("HealthCheckFast with maxConcurrent=1 returned %d servers, want 1", len(alive))
	}
	if alive[0].Port != port {
		t.Errorf("alive[0].Port = %d, want %d", alive[0].Port, port)
	}
}

func TestHealthCheckFastMaxConcurrentNegative(t *testing.T) {
	host, port, cleanup := startTCPServer(t)
	defer cleanup()

	servers := []SSConfig{
		{Server: host, Port: port, Password: "a", Method: "aes-256-gcm"},
	}

	alive := HealthCheckFast(context.Background(), servers, 2*time.Second, -1)
	if len(alive) != 1 {
		t.Fatalf("HealthCheckFast with maxConcurrent=-1 returned %d servers, want 1", len(alive))
	}
}

func TestHealthCheckFastMaxConcurrentLarge(t *testing.T) {
	host, port, cleanup := startTCPServer(t)
	defer cleanup()

	servers := []SSConfig{
		{Server: host, Port: port, Password: "a", Method: "aes-256-gcm"},
	}

	// maxConcurrent > len(servers) — should not block
	alive := HealthCheckFast(context.Background(), servers, 2*time.Second, 100)
	if len(alive) != 1 {
		t.Fatalf("HealthCheckFast with maxConcurrent=100 returned %d servers, want 1", len(alive))
	}
}

func TestHealthCheckFastEmpty(t *testing.T) {
	alive := HealthCheckFast(context.Background(), nil, 1*time.Second, 5)
	if len(alive) != 0 {
		t.Errorf("HealthCheckFast(nil) returned %d servers, want 0", len(alive))
	}

	alive = HealthCheckFast(context.Background(), []SSConfig{}, 1*time.Second, 5)
	if len(alive) != 0 {
		t.Errorf("HealthCheckFast(empty) returned %d servers, want 0", len(alive))
	}
}
