// Package rotator provides a round-robin rotator for a pool of upstream addresses.
// It supports manual trigger and optional auto-rotate on a timer.
package rotator

import (
	"sync"
	"time"
)

// Rotator manages round-robin rotation over a pool of upstream addresses.
type Rotator struct {
	pool      []string
	index     int
	mu        sync.RWMutex
	triggerCh chan struct{}
	stopCh    chan struct{}
	wg        sync.WaitGroup
	interval  time.Duration
	onRotate  func(addr string)
}

// New creates a Rotator with the given pool, optional auto-rotate interval,
// and optional onRotate callback. If interval is 0, auto-rotate is disabled
// but Trigger() still works via a background listener.
// The onRotate callback is called on each rotation (manual or auto).
func New(pool []string, interval time.Duration, onRotate func(addr string)) *Rotator {
	r := &Rotator{
		pool:      pool,
		interval:  interval,
		onRotate:  onRotate,
		triggerCh: make(chan struct{}, 1),
		stopCh:    make(chan struct{}),
	}

	// Always start the listener so Trigger() works regardless of interval.
	r.wg.Add(1)
	go r.listener()

	return r
}

// Current returns the current upstream address without advancing.
// Returns empty string if pool is empty.
func (r *Rotator) Current() string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	if len(r.pool) == 0 {
		return ""
	}
	return r.pool[r.index]
}

// Next advances to the next upstream and returns it (round-robin).
// Returns empty string if pool is empty.
func (r *Rotator) Next() string {
	r.mu.Lock()
	defer r.mu.Unlock()
	if len(r.pool) == 0 {
		return ""
	}
	addr := r.pool[r.index]
	r.index = (r.index + 1) % len(r.pool)
	if r.onRotate != nil {
		r.onRotate(addr)
	}
	return addr
}

// Trigger sends a non-blocking manual rotation request.
// If auto-rotate is running, the rotation will happen shortly.
// If no auto-rotate, the rotation happens synchronously via Next().
func (r *Rotator) Trigger() {
	select {
	case r.triggerCh <- struct{}{}:
	default:
		// Already a pending trigger, skip
	}
}

// Stop shuts down the auto-rotate goroutine and waits for it to finish.
func (r *Rotator) Stop() {
	close(r.stopCh)
	r.wg.Wait()
}

// Len returns the number of items in the pool.
func (r *Rotator) Len() int {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return len(r.pool)
}

// Index returns the current position in the pool (0-based).
func (r *Rotator) Index() int {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.index
}

// Update replaces the pool with a new set of addresses and resets
// the index to 0. Safe for concurrent use.
func (r *Rotator) Update(newPool []string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.pool = newPool
	r.index = 0
}

func (r *Rotator) listener() {
	defer r.wg.Done()

	if r.interval > 0 {
		ticker := time.NewTicker(r.interval)
		defer ticker.Stop()
		for {
			select {
			case <-r.stopCh:
				return
			case <-ticker.C:
				r.rotate()
			case <-r.triggerCh:
				r.rotate()
			}
		}
	}

	// No auto-rotate: just listen for triggers and stop.
	for {
		select {
		case <-r.stopCh:
			return
		case <-r.triggerCh:
			r.rotate()
		}
	}
}

// Remove removes an address from the rotator pool.
// If the current index points to an element at or after the removed position,
// it is adjusted to avoid skipping an element. Safe for concurrent use.
func (r *Rotator) Remove(addr string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	for i, a := range r.pool {
		if a == addr {
			r.pool = append(r.pool[:i], r.pool[i+1:]...)
			if len(r.pool) == 0 {
				r.index = 0
				return
			}
			if i < r.index {
				r.index--
			} else if i == r.index && r.index >= len(r.pool) {
				r.index = 0
			}
			return
		}
	}
}

// RemoveCurrent atomically returns the current address and removes it from the pool.
// Returns empty string if pool is safe. Safe for concurrent use.
// This avoids the TOCTOU race between Current() + Remove().
func (r *Rotator) RemoveCurrent() string {
	r.mu.Lock()
	defer r.mu.Unlock()
	if len(r.pool) == 0 {
		return ""
	}
	addr := r.pool[r.index]
	r.pool = append(r.pool[:r.index], r.pool[r.index+1:]...)
	if len(r.pool) == 0 {
		r.index = 0
	} else if r.index >= len(r.pool) {
		r.index = 0
	}
	return addr
}

func (r *Rotator) rotate() {
	r.mu.Lock()
	defer r.mu.Unlock()
	if len(r.pool) == 0 {
		return
	}
	r.index = (r.index + 1) % len(r.pool)
	if r.onRotate != nil {
		r.onRotate(r.pool[r.index])
	}
}
