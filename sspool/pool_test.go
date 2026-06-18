package sspool

import (
	"sync"
	"testing"
)

func TestSSPoolAddAndGet(t *testing.T) {
	p := NewSSPool()
	s1 := SSConfig{Server: "1.1.1.1", Port: 8388, Password: "a", Method: "aes-256-gcm"}
	s2 := SSConfig{Server: "2.2.2.2", Port: 8388, Password: "b", Method: "aes-128-gcm"}
	p.Add(s1)
	p.Add(s2)
	if p.Len() != 2 {
		t.Errorf("Len() = %d, want 2", p.Len())
	}
	cur := p.Get()
	if cur == nil {
		t.Fatal("Get() returned nil")
	}
	if cur.Server != "1.1.1.1" {
		t.Errorf("Get().Server = %q, want %q", cur.Server, "1.1.1.1")
	}
}

func TestSSPoolRemove(t *testing.T) {
	p := NewSSPool()
	s1 := SSConfig{Server: "1.1.1.1", Port: 8388, Password: "a", Method: "aes-256-gcm"}
	s2 := SSConfig{Server: "2.2.2.2", Port: 8388, Password: "b", Method: "aes-128-gcm"}
	p.Add(s1)
	p.Add(s2)
	p.Remove("1.1.1.1:8388:a:aes-256-gcm")
	if p.Len() != 1 {
		t.Errorf("Len() after Remove = %d, want 1", p.Len())
	}
	cur := p.Get()
	if cur == nil {
		t.Fatal("Get() returned nil after remove")
	}
	if cur.Server != "2.2.2.2" {
		t.Errorf("Get().Server = %q, want %q", cur.Server, "2.2.2.2")
	}
}

func TestSSPoolRemoveNonexistent(t *testing.T) {
	p := NewSSPool()
	s1 := SSConfig{Server: "1.1.1.1", Port: 8388, Password: "a", Method: "aes-256-gcm"}
	p.Add(s1)
	p.Remove("9.9.9.9:9999:x:aes-256-gcm")
	if p.Len() != 1 {
		t.Errorf("Len() = %d after removing nonexistent, want 1", p.Len())
	}
}

func TestSSPoolRotate(t *testing.T) {
	p := NewSSPool()
	servers := []SSConfig{
		{Server: "1.1.1.1", Port: 8388, Password: "a", Method: "aes-256-gcm"},
		{Server: "2.2.2.2", Port: 8388, Password: "b", Method: "aes-128-gcm"},
		{Server: "3.3.3.3", Port: 8388, Password: "c", Method: "chacha20-ietf-poly1305"},
	}
	for _, s := range servers {
		p.Add(s)
	}

	// Rotate through all servers and verify round-robin
	for cycle := 0; cycle < 2; cycle++ {
		for i, want := range servers {
			got := p.Get()
			if got == nil {
				t.Fatalf("cycle %d, iteration %d: Get() returned nil", cycle, i)
			}
			if got.Server != want.Server {
				t.Errorf("cycle %d, iteration %d: Get().Server = %q, want %q", cycle, i, got.Server, want.Server)
			}
			p.Next()
		}
	}
}

func TestSSPoolDedup(t *testing.T) {
	p := NewSSPool()
	s := SSConfig{Server: "1.1.1.1", Port: 8388, Password: "a", Method: "aes-256-gcm"}
	p.Add(s)
	p.Add(s) // duplicate
	p.Add(s) // duplicate
	if p.Len() != 1 {
		t.Errorf("Len() after adding 3 identical = %d, want 1", p.Len())
	}
}

func TestSSPoolDedupDifferentPassword(t *testing.T) {
	p := NewSSPool()
	s1 := SSConfig{Server: "1.1.1.1", Port: 8388, Password: "a", Method: "aes-256-gcm"}
	s2 := SSConfig{Server: "1.1.1.1", Port: 8388, Password: "b", Method: "aes-256-gcm"}
	p.Add(s1)
	p.Add(s2)
	// Same host:port but different password = different server
	if p.Len() != 2 {
		t.Errorf("Len() = %d, want 2 (different passwords)", p.Len())
	}
}

func TestSSPoolEmpty(t *testing.T) {
	p := NewSSPool()
	if p.Len() != 0 {
		t.Errorf("Len() on empty pool = %d, want 0", p.Len())
	}
	if p.Get() != nil {
		t.Error("Get() on empty pool should return nil")
	}
}

func TestSSPoolConcurrent(t *testing.T) {
	p := NewSSPool()
	var wg sync.WaitGroup
	// Concurrent adds
	for i := 0; i < 100; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			p.Add(SSConfig{Server: "1.1.1.1", Port: 8388, Password: "a", Method: "aes-256-gcm"})
		}(i)
	}
	wg.Wait()
	if p.Len() != 1 {
		t.Errorf("Len() after 100 concurrent adds of same server = %d, want 1", p.Len())
	}

	// Concurrent reads
	for i := 0; i < 100; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_ = p.Get()
			_ = p.Len()
		}()
	}
	wg.Wait()
}

func TestSSPoolRemoveThenAdd(t *testing.T) {
	p := NewSSPool()
	s := SSConfig{Server: "1.1.1.1", Port: 8388, Password: "a", Method: "aes-256-gcm"}
	p.Add(s)
	p.Remove(s.Key())
	if p.Len() != 0 {
		t.Errorf("Len() after remove = %d, want 0", p.Len())
	}
	// Add same server back
	p.Add(s)
	if p.Len() != 1 {
		t.Errorf("Len() after re-add = %d, want 1", p.Len())
	}
}
