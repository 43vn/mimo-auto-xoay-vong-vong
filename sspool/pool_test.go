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

func TestSSPoolReplaceAll(t *testing.T) {
	p := NewSSPool()
	s1 := SSConfig{Server: "1.1.1.1", Port: 8388, Password: "a", Method: "aes-256-gcm"}
	s2 := SSConfig{Server: "2.2.2.2", Port: 8388, Password: "b", Method: "aes-128-gcm"}
	s3 := SSConfig{Server: "3.3.3.3", Port: 8388, Password: "c", Method: "chacha20-ietf-poly1305"}
	p.Add(s1)
	p.Add(s2)
	p.Add(s3)

	// ReplaceAll with 2 new servers
	newServers := []SSConfig{
		{Server: "4.4.4.4", Port: 8388, Password: "d", Method: "aes-256-gcm"},
		{Server: "5.5.5.5", Port: 8388, Password: "e", Method: "aes-128-gcm"},
	}
	p.ReplaceAll(newServers)

	if p.Len() != 2 {
		t.Errorf("Len() after ReplaceAll = %d, want 2", p.Len())
	}

	got := p.Get()
	if got == nil {
		t.Fatal("Get() returned nil after ReplaceAll")
	}
	if got.Server != "4.4.4.4" {
		t.Errorf("Get().Server = %q, want %q (first new server)", got.Server, "4.4.4.4")
	}

	// Verify index was reset: rotating should give us the second server immediately
	next := p.Next()
	if next == nil {
		t.Fatal("Next() returned nil after ReplaceAll")
	}
	if next.Server != "5.5.5.5" {
		t.Errorf("Next().Server = %q, want %q (second new server)", next.Server, "5.5.5.5")
	}
}

func TestSSPoolReplaceAllEmpty(t *testing.T) {
	t.Run("nil slice", func(t *testing.T) {
		p := NewSSPool()
		s1 := SSConfig{Server: "1.1.1.1", Port: 8388, Password: "a", Method: "aes-256-gcm"}
		p.Add(s1)
		p.ReplaceAll(nil)
		if p.Len() != 0 {
			t.Errorf("Len() after ReplaceAll(nil) = %d, want 0", p.Len())
		}
		if p.Get() != nil {
			t.Error("Get() after ReplaceAll(nil) should return nil")
		}
	})

	t.Run("empty slice", func(t *testing.T) {
		p := NewSSPool()
		s1 := SSConfig{Server: "1.1.1.1", Port: 8388, Password: "a", Method: "aes-256-gcm"}
		p.Add(s1)
		p.ReplaceAll([]SSConfig{})
		if p.Len() != 0 {
			t.Errorf("Len() after ReplaceAll([]) = %d, want 0", p.Len())
		}
		if p.Get() != nil {
			t.Error("Get() after ReplaceAll([]) should return nil")
		}
	})
}

func TestSSPoolSnapshot(t *testing.T) {
	p := NewSSPool()
	servers := []SSConfig{
		{Server: "1.1.1.1", Port: 8388, Password: "a", Method: "aes-256-gcm"},
		{Server: "2.2.2.2", Port: 8388, Password: "b", Method: "aes-128-gcm"},
		{Server: "3.3.3.3", Port: 8388, Password: "c", Method: "chacha20-ietf-poly1305"},
	}
	for _, s := range servers {
		p.Add(s)
	}

	snap := p.Snapshot()
	if len(snap) != 3 {
		t.Fatalf("Snapshot() length = %d, want 3", len(snap))
	}

	for i, want := range servers {
		if snap[i].Server != want.Server {
			t.Errorf("Snapshot()[%d].Server = %q, want %q", i, snap[i].Server, want.Server)
		}
		if snap[i].Port != want.Port {
			t.Errorf("Snapshot()[%d].Port = %d, want %d", i, snap[i].Port, want.Port)
		}
		if snap[i].Password != want.Password {
			t.Errorf("Snapshot()[%d].Password = %q, want %q", i, snap[i].Password, want.Password)
		}
		if snap[i].Method != want.Method {
			t.Errorf("Snapshot()[%d].Method = %q, want %q", i, snap[i].Method, want.Method)
		}
	}

	// Verify modifying snapshot does not affect pool
	snap[0].Server = "modified"
	if p.Get().Server != "1.1.1.1" {
		t.Errorf("pool.Get().Server = %q after modifying snapshot, want %q (original)", p.Get().Server, "1.1.1.1")
	}
}

func TestSSPoolSnapshotIsolation(t *testing.T) {
	p := NewSSPool()
	original := SSConfig{Server: "1.1.1.1", Port: 8388, Password: "a", Method: "aes-256-gcm"}
	p.Add(original)

	snap := p.Snapshot()
	if len(snap) != 1 {
		t.Fatalf("Snapshot() length = %d, want 1", len(snap))
	}

	// Mutate the snapshot copy
	snap[0].Server = "evil"

	// Pool must remain unaffected
	got := p.Get()
	if got == nil {
		t.Fatal("Get() returned nil after snapshot isolation test")
	}
	if got.Server != "1.1.1.1" {
		t.Errorf("pool.Get().Server = %q after mutating snapshot, want %q (original)", got.Server, "1.1.1.1")
	}
}
