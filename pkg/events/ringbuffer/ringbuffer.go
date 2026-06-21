// Copyright 2025 Philipp Hossner
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

// Package ringbuffer provides a thread-safe generic ring buffer implementation.
//
// A ring buffer (circular buffer) is a fixed-size buffer that wraps around when full.
// This implementation uses Go generics to support any type and provides thread-safe
// operations through mutex locking.
//
// Example usage:
//
//	type Event struct {
//	    Timestamp time.Time
//	    Message   string
//	}
//
//	buffer := ringbuffer.New[Event](100)
//	buffer.Add(Event{Timestamp: time.Now(), Message: "Event 1"})
//	recent := buffer.GetLast(10)  // Get last 10 events
package ringbuffer

import "sync"

// RingBuffer is a thread-safe circular buffer that stores a fixed number of items.
// When the buffer is full, new items overwrite the oldest items.
//
// The buffer uses Go generics to support any type T.
type RingBuffer[T any] struct {
	items []T          // Circular buffer storage
	size  int          // Maximum capacity
	head  int          // Index where next item will be written
	count int          // Current number of items (max: size)
	mu    sync.RWMutex // Protects all fields
}

// New creates a new ring buffer with the specified capacity.
//
// The size parameter determines the maximum number of items the buffer can hold.
// Once full, adding new items will overwrite the oldest items.
//
// Example:
//
//	buffer := ringbuffer.New[string](100)  // Can hold up to 100 strings
func New[T any](size int) *RingBuffer[T] {
	return &RingBuffer[T]{
		items: make([]T, size),
		size:  size,
		head:  0,
		count: 0,
	}
}

// Add inserts an item into the buffer.
//
// If the buffer is full, the oldest item is overwritten.
// This operation is thread-safe.
//
// Example:
//
//	buffer.Add("event 1")
//	buffer.Add("event 2")
func (rb *RingBuffer[T]) Add(item T) {
	rb.mu.Lock()
	defer rb.mu.Unlock()

	// Write item at head position
	rb.items[rb.head] = item

	// Move head forward (wrap around if needed)
	rb.head = (rb.head + 1) % rb.size

	// Update count (max is size)
	if rb.count < rb.size {
		rb.count++
	}
}

// GetLast returns the n most recently added items, in chronological order.
//
// If n is greater than the number of items in the buffer, all items are returned.
// The returned slice is ordered from oldest to newest.
// This operation is thread-safe.
//
// Example:
//
//	buffer.Add("event 1")
//	buffer.Add("event 2")
//	buffer.Add("event 3")
//	recent := buffer.GetLast(2)  // Returns ["event 2", "event 3"]
func (rb *RingBuffer[T]) GetLast(n int) []T {
	rb.mu.RLock()
	defer rb.mu.RUnlock()

	if n > rb.count {
		n = rb.count
	}

	if n == 0 {
		return []T{}
	}

	result := make([]T, n)

	// Calculate starting position (n items back from head)
	start := (rb.head - n + rb.size) % rb.size

	// Copy items to result (handling wrap-around)
	for i := 0; i < n; i++ {
		idx := (start + i) % rb.size
		result[i] = rb.items[idx]
	}

	return result
}

// GetAll returns all items currently in the buffer, in chronological order.
//
// The returned slice is ordered from oldest to newest.
// This operation is thread-safe.
//
// Example:
//
//	all := buffer.GetAll()
func (rb *RingBuffer[T]) GetAll() []T {
	rb.mu.RLock()
	defer rb.mu.RUnlock()

	if rb.count == 0 {
		return []T{}
	}

	result := make([]T, rb.count)

	// If buffer not full, items are at start
	if rb.count < rb.size {
		copy(result, rb.items[:rb.count])
		return result
	}

	// Buffer is full, need to account for wrap-around
	// Items are ordered: [head...size-1, 0...head-1]
	split := rb.size - rb.head
	copy(result, rb.items[rb.head:])
	copy(result[split:], rb.items[:rb.head])

	return result
}

// Len returns the current number of items in the buffer.
//
// This operation is thread-safe.
//
// Example:
//
//	count := buffer.Len()  // Returns number of items currently stored
func (rb *RingBuffer[T]) Len() int {
	rb.mu.RLock()
	defer rb.mu.RUnlock()
	return rb.count
}
