package templating

import (
	"sync"

	"golang.org/x/sync/singleflight"
)

// SharedContext provides thread-safe caching with compute-once semantics.
// It is used for sharing data between parallel template renders within a single
// reconciliation cycle.
//
// The API is intentionally minimal to prevent race conditions:
//   - ComputeIfAbsent stores values atomically (uses singleflight)
//   - Get provides read-only access to existing values
//   - No Set method exists - prevents racy check-then-act patterns
//   - The wasComputed return value enables deduplication (FirstSeen pattern)
type SharedContext struct {
	mu    sync.RWMutex
	data  map[string]interface{}
	group singleflight.Group
}

// NewSharedContext creates a new thread-safe shared context.
func NewSharedContext() *SharedContext {
	return &SharedContext{
		data: make(map[string]interface{}),
	}
}

// Get returns the value for key, or nil if not found.
// This is a read-only operation - use ComputeIfAbsent for initialization.
func (s *SharedContext) Get(key string) interface{} {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.data[key]
}

// ComputeIfAbsent returns the value for key, computing it if not present.
// Uses singleflight to ensure only one goroutine computes for a given key.
// Other goroutines waiting for the same key will receive the computed result.
//
// Returns (value, wasComputed) where:
//   - value: the stored value (either existing or newly computed)
//   - wasComputed: true only for the goroutine that actually ran compute()
//
// Use wasComputed for deduplication (FirstSeen pattern):
//
//	_, wasFirst := shared.ComputeIfAbsent("seen:"+key, func() any { return true })
//	if wasFirst {
//	    // First occurrence
//	}
//
// Use with nil fallback for read-only access:
//
//	val, _ := shared.ComputeIfAbsent("key", func() any { return nil })
//
// IMPORTANT: The compute function is called WITHOUT holding the mutex, so it
// may safely call ComputeIfAbsent for other keys (nested/recursive calls).
func (s *SharedContext) ComputeIfAbsent(key string, compute func() interface{}) (interface{}, bool) {
	// Fast path: check if already computed
	s.mu.RLock()
	if val, ok := s.data[key]; ok {
		s.mu.RUnlock()
		return val, false // Already exists, didn't compute
	}
	s.mu.RUnlock()

	// Track if THIS goroutine computed the value. Each goroutine has its own
	// weComputed variable, and only the goroutine whose closure runs will set it.
	// Note: singleflight's `shared` return value is true for ALL callers when
	// there are duplicates (including the one that ran the function), so we
	// can't rely on it.
	var weComputed bool

	// Slow path: use singleflight to compute exactly once
	r, _, _ := s.group.Do(key, func() (interface{}, error) {
		// Double-check under lock before computing
		s.mu.Lock()
		if val, ok := s.data[key]; ok {
			s.mu.Unlock()
			return val, nil // Found in double-check, weComputed stays false
		}
		s.mu.Unlock()

		// Mark that THIS goroutine is computing
		weComputed = true

		// Compute WITHOUT holding lock - allows nested ComputeIfAbsent calls
		val := compute()

		// Store result under lock
		s.mu.Lock()
		s.data[key] = val
		s.mu.Unlock()

		return val, nil
	})

	return r, weComputed
}
