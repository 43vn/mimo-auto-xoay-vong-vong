package proxy

import (
	"sync"
	"testing"
	"time"
)

func TestRateLimiterAllow(t *testing.T) {
	rl := NewRateLimiter(3, time.Minute, 0)
	for i := 0; i < 3; i++ {
		if !rl.Allow() {
			t.Errorf("Allow() #%d = false, want true", i+1)
		}
	}
}

func TestRateLimiterDeny(t *testing.T) {
	rl := NewRateLimiter(2, time.Minute, 0)
	rl.Allow()
	rl.Allow()
	if rl.Allow() {
		t.Error("3rd Allow() = true, want false (max=2)")
	}
}

func TestRateLimiterWindow(t *testing.T) {
	rl := NewRateLimiter(2, 100*time.Millisecond, 0)
	rl.Allow()
	rl.Allow()
	// Both used, should deny now
	if rl.Allow() {
		t.Error("3rd Allow() should deny before window expires")
	}
	time.Sleep(110 * time.Millisecond)
	// Window expired, should allow again
	if !rl.Allow() {
		t.Error("Allow() after window expired should return true")
	}
}

func TestRateLimiterMinInterval(t *testing.T) {
	rl := NewRateLimiter(10, time.Minute, 50*time.Millisecond)
	if !rl.Allow() {
		t.Fatal("first Allow() should be true")
	}
	// Immediate second call should be denied by minInterval
	if rl.Allow() {
		t.Error("immediate 2nd Allow() = true, want false (minInterval not met)")
	}
	time.Sleep(60 * time.Millisecond)
	if !rl.Allow() {
		t.Error("Allow() after minInterval elapsed should return true")
	}
}

func TestRateLimiterConcurrent(t *testing.T) {
	rl := NewRateLimiter(5, time.Minute, 0)
	var wg sync.WaitGroup
	allowed := make(chan bool, 20)

	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			allowed <- rl.Allow()
		}()
	}
	wg.Wait()
	close(allowed)

	trueCount := 0
	for v := range allowed {
		if v {
			trueCount++
		}
	}
	if trueCount != 5 {
		t.Errorf("concurrent Allow() returned true %d times, want 5", trueCount)
	}
}

func TestRateLimiterReset(t *testing.T) {
	rl := NewRateLimiter(2, 100*time.Millisecond, 0)
	rl.Allow()
	rl.Allow()
	if rl.Allow() {
		t.Error("should deny when at max")
	}
	time.Sleep(120 * time.Millisecond)
	if !rl.Allow() {
		t.Error("should allow after window reset")
	}
}
