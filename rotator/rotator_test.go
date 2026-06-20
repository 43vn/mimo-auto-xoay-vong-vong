package rotator

import (
	"testing"
	"time"
)

func TestRotatorNext(t *testing.T) {
	pool := []string{"a", "b", "c"}
	r := New(pool, 0, nil)

	// 6 calls on a pool of 3 should cycle twice
	expected := []string{"a", "b", "c", "a", "b", "c"}
	for i, want := range expected {
		got := r.Next()
		if got != want {
			t.Errorf("Next() call %d: got %q, want %q", i, got, want)
		}
	}
}

func TestRotatorCurrent(t *testing.T) {
	pool := []string{"x", "y", "z"}
	r := New(pool, 0, nil)

	// Current returns first item before any Next
	if got := r.Current(); got != "x" {
		t.Errorf("Current() before Next: got %q, want %q", got, "x")
	}

	// Advance once
	r.Next()

	if got := r.Current(); got != "y" {
		t.Errorf("Current() after one Next: got %q, want %q", got, "y")
	}
}

func TestRotatorTrigger(t *testing.T) {
	pool := []string{"a", "b", "c"}
	rotated := make(chan string, 10)

	onRotate := func(addr string) {
		rotated <- addr
	}

	r := New(pool, 0, onRotate)

	// Current is "a", trigger should rotate to "b"
	r.Trigger()

	select {
	case <-rotated:
		// good
	case <-time.After(time.Second):
		t.Fatal("Trigger() did not fire onRotate callback")
	}

	if got := r.Current(); got != "b" {
		t.Errorf("Current() after Trigger: got %q, want %q", got, "b")
	}
}

func TestRotatorStop(t *testing.T) {
	pool := []string{"a", "b", "c"}
	rotations := make(chan struct{}, 20)

	onRotate := func(addr string) {
		select {
		case rotations <- struct{}{}:
		default:
		}
	}

	// Very short interval for testing
	r := New(pool, 10*time.Millisecond, onRotate)

	// Let a few rotations happen
	time.Sleep(60 * time.Millisecond)

	r.Stop()

	// Drain any pending rotations
	count := 0
	drainLoop:
	for {
		select {
		case <-rotations:
			count++
		default:
			break drainLoop
		}
	}

	if count == 0 {
		t.Error("expected at least one auto-rotation before Stop")
	}

	// After stop, no more rotations should happen
	afterStopCount := 0
	time.Sleep(50 * time.Millisecond)
	drainLoop2:
	for {
		select {
		case <-rotations:
			afterStopCount++
		default:
			break drainLoop2
		}
	}

	if afterStopCount > 0 {
		t.Errorf("got %d rotations after Stop(), want 0", afterStopCount)
	}
}

func TestRotatorSingleItem(t *testing.T) {
	pool := []string{"only"}
	r := New(pool, 0, nil)

	for i := 0; i < 5; i++ {
		got := r.Next()
		if got != "only" {
			t.Errorf("Next() call %d: got %q, want %q", i, got, "only")
		}
	}

	if got := r.Current(); got != "only" {
		t.Errorf("Current(): got %q, want %q", got, "only")
	}
}

func TestRotatorEmptyPool(t *testing.T) {
	r := New([]string{}, 0, nil)

	if got := r.Current(); got != "" {
		t.Errorf("Current() on empty pool: got %q, want %q", got, "")
	}

	if got := r.Next(); got != "" {
		t.Errorf("Next() on empty pool: got %q, want %q", got, "")
	}

	if got := r.Len(); got != 0 {
		t.Errorf("Len() on empty pool: got %d, want 0", got)
	}
}

func TestRotatorLen(t *testing.T) {
	r := New([]string{"a", "b"}, 0, nil)
	if got := r.Len(); got != 2 {
		t.Errorf("Len(): got %d, want 2", got)
	}
}

func TestRotatorUpdate(t *testing.T) {
	r := New([]string{"a", "b", "c"}, 0, nil)

	// Advance index to 1
	r.Next() // returns "a", index now 1

	// Replace pool
	r.Update([]string{"x", "y"})

	if got := r.Len(); got != 2 {
		t.Errorf("Len() after Update: got %d, want 2", got)
	}

	if got := r.Current(); got != "x" {
		t.Errorf("Current() after Update: got %q, want %q", got, "x")
	}
}

func TestRotatorUpdateEmpty(t *testing.T) {
	r := New([]string{"a", "b"}, 0, nil)

	r.Update([]string{})

	if got := r.Len(); got != 0 {
		t.Errorf("Len() after Update([]): got %d, want 0", got)
	}

	if got := r.Current(); got != "" {
		t.Errorf("Current() after Update([]): got %q, want %q", got, "")
	}
}

func TestRotatorUpdateResetsIndex(t *testing.T) {
	r := New([]string{"a", "b", "c", "d", "e"}, 0, nil)

	// Advance index to 3
	r.Next() // 0 -> 1
	r.Next() // 1 -> 2
	r.Next() // 2 -> 3

	// Replace pool with 2 items
	r.Update([]string{"a", "b"})

	if got := r.Index(); got != 0 {
		t.Errorf("Index() after Update: got %d, want 0", got)
	}
}

func TestRotatorUpdateConcurrent(t *testing.T) {
	r := New([]string{"a"}, 0, nil)
	done := make(chan struct{})

	for i := 0; i < 10; i++ {
		go func(n int) {
			defer func() { done <- struct{}{} }()
			// Each goroutine calls Update with a different slice
			s := make([]string, n)
			for j := 0; j < n; j++ {
				s[j] = "x"
			}
			r.Update(s)
		}(i)
	}

	for i := 0; i < 10; i++ {
		<-done
	}

	// Just verify the final state is valid
	if got := r.Index(); got != 0 {
		t.Errorf("Index() after concurrent updates: got %d, want 0", got)
	}
}

// --- Remove method tests ---

func TestRotatorRemoveAddress(t *testing.T) {
	pool := []string{"a", "b", "c"}
	r := New(pool, 0, nil)

	// Remove "b"
	r.Remove("b")

	// Verify pool is now 2 elements
	if got := r.Len(); got != 2 {
		t.Fatalf("Len() after Remove: got %d, want 2", got)
	}

	// Verify Next() cycles through a, c, a, c (b is gone)
	expected := []string{"a", "c", "a", "c"}
	for i, want := range expected {
		got := r.Next()
		if got != want {
			t.Errorf("Next() call %d after Remove: got %q, want %q", i, got, want)
		}
	}
}

func TestRotatorRemoveNotExist(t *testing.T) {
	pool := []string{"a", "b", "c"}
	r := New(pool, 0, nil)

	// Remove non-existent address — should be no-op
	r.Remove("nonexistent")

	if got := r.Len(); got != 3 {
		t.Fatalf("Len() after Remove nonexistent: got %d, want 3", got)
	}

	// Normal operation continues
	if got := r.Next(); got != "a" {
		t.Errorf("Next() after Remove nonexistent: got %q, want %q", got, "a")
	}
}

func TestRotatorRemoveAdjustsIndex(t *testing.T) {
	pool := []string{"a", "b", "c", "d"}
	r := New(pool, 0, nil)

	// Advance to index 2 (current is "c")
	r.Next() // returns "a", index=1
	r.Next() // returns "b", index=2

	// Remove "b" (at index 1, before current index=2) → index should decrement to 1
	r.Remove("b")
	if got := r.Index(); got != 1 {
		t.Fatalf("Index() after removing before index: got %d, want 1", got)
	}

	// Now pool is ["a", "c", "d"], index=1 → current is "c"
	if got := r.Current(); got != "c" {
		t.Errorf("Current() after Remove before index: got %q, want %q", got, "c")
	}
}

func TestRotatorRemoveAtIndex(t *testing.T) {
	pool := []string{"a", "b", "c"}
	r := New(pool, 0, nil)

	// Remove "a" at current index (0)
	r.Remove("a")

	// Pool is ["b", "c"], index should stay 0 (now points to "b")
	if got := r.Len(); got != 2 {
		t.Fatalf("Len() after Remove at index: got %d, want 2", got)
	}

	// Next should return "b"
	if got := r.Next(); got != "b" {
		t.Errorf("Next() after Remove at index: got %q, want %q", got, "b")
	}
}

func TestRotatorRemoveLastItem(t *testing.T) {
	pool := []string{"a", "b", "c"}
	r := New(pool, 0, nil)

	// Advance to index 2 (current is "c")
	r.Next() // index=1
	r.Next() // index=2

	// Remove "c" (last item, index points to it) → index should wrap to 0
	r.Remove("c")

	if got := r.Len(); got != 2 {
		t.Fatalf("Len() after Remove last: got %d, want 2", got)
	}
	if got := r.Index(); got != 0 {
		t.Errorf("Index() after removing last item: got %d, want 0", got)
	}

	// Next should return "a"
	if got := r.Next(); got != "a" {
		t.Errorf("Next() after Remove last: got %q, want %q", got, "a")
	}
}

func TestRotatorRemoveFromSingleItem(t *testing.T) {
	pool := []string{"only"}
	r := New(pool, 0, nil)

	r.Remove("only")

	if got := r.Len(); got != 0 {
		t.Fatalf("Len() after Remove only item: got %d, want 0", got)
	}
	if got := r.Current(); got != "" {
		t.Errorf("Current() after Remove only: got %q, want %q", got, "")
	}
	if got := r.Next(); got != "" {
		t.Errorf("Next() after Remove only: got %q, want %q", got, "")
	}
}
