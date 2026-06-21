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

package ringbuffer

import (
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestNew(t *testing.T) {
	rb := New[int](10)
	assert.Equal(t, 0, rb.Len())
}

func TestAdd(t *testing.T) {
	rb := New[string](3)

	rb.Add("first")
	assert.Equal(t, 1, rb.Len())

	rb.Add("second")
	assert.Equal(t, 2, rb.Len())

	rb.Add("third")
	assert.Equal(t, 3, rb.Len())

	// Adding 4th item should wrap around
	rb.Add("fourth")
	assert.Equal(t, 3, rb.Len(), "size should not exceed capacity")
}

func TestGetLast(t *testing.T) {
	rb := New[int](5)

	// Empty buffer
	assert.Equal(t, []int{}, rb.GetLast(2))

	// Add some items
	rb.Add(1)
	rb.Add(2)
	rb.Add(3)

	// Get last 2
	last2 := rb.GetLast(2)
	assert.Equal(t, []int{2, 3}, last2)

	// Get more than available
	last10 := rb.GetLast(10)
	assert.Equal(t, []int{1, 2, 3}, last10)

	// Get all
	all := rb.GetLast(3)
	assert.Equal(t, []int{1, 2, 3}, all)
}

func TestGetLastWithWrapAround(t *testing.T) {
	rb := New[int](3)

	// Fill buffer
	rb.Add(1)
	rb.Add(2)
	rb.Add(3)

	// Add more to cause wrap-around
	rb.Add(4) // Overwrites 1
	rb.Add(5) // Overwrites 2

	// Should get [3, 4, 5] (oldest to newest)
	all := rb.GetLast(3)
	assert.Equal(t, []int{3, 4, 5}, all)

	// Get last 2
	last2 := rb.GetLast(2)
	assert.Equal(t, []int{4, 5}, last2)
}

func TestGetAll(t *testing.T) {
	rb := New[string](5)

	// Empty buffer
	assert.Equal(t, []string{}, rb.GetAll())

	// Partial fill
	rb.Add("a")
	rb.Add("b")
	rb.Add("c")
	assert.Equal(t, []string{"a", "b", "c"}, rb.GetAll())

	// Full buffer
	rb.Add("d")
	rb.Add("e")
	assert.Equal(t, []string{"a", "b", "c", "d", "e"}, rb.GetAll())

	// Wrap around
	rb.Add("f") // Overwrites "a"
	assert.Equal(t, []string{"b", "c", "d", "e", "f"}, rb.GetAll())

	rb.Add("g") // Overwrites "b"
	rb.Add("h") // Overwrites "c"
	assert.Equal(t, []string{"d", "e", "f", "g", "h"}, rb.GetAll())
}

func TestThreadSafety(t *testing.T) {
	rb := New[int](100)
	var wg sync.WaitGroup

	// Multiple writers
	for i := range 10 {
		wg.Add(1)
		go func(start int) {
			defer wg.Done()
			for j := range 100 {
				rb.Add(start*100 + j)
			}
		}(i)
	}

	// Multiple readers
	for range 5 {
		wg.Go(func() {
			for range 100 {
				_ = rb.GetLast(10)
				_ = rb.GetAll()
				_ = rb.Len()
			}
		})
	}

	// Should not panic or race
	wg.Wait()

	// Buffer should be at capacity
	assert.Equal(t, 100, rb.Len())
	assert.Equal(t, 100, len(rb.GetAll()))
}

func TestGenericTypes(t *testing.T) {
	// Test with struct type
	type Event struct {
		ID   int
		Name string
	}

	rb := New[Event](3)
	rb.Add(Event{ID: 1, Name: "first"})
	rb.Add(Event{ID: 2, Name: "second"})

	events := rb.GetAll()
	assert.Equal(t, 2, len(events))
	assert.Equal(t, "first", events[0].Name)
	assert.Equal(t, "second", events[1].Name)

	// Test with pointer type
	rbPtr := New[*Event](3)
	rbPtr.Add(&Event{ID: 1, Name: "ptr1"})
	rbPtr.Add(&Event{ID: 2, Name: "ptr2"})

	ptrs := rbPtr.GetAll()
	assert.Equal(t, 2, len(ptrs))
	assert.Equal(t, "ptr1", ptrs[0].Name)
}

func TestWrapAroundEdgeCases(t *testing.T) {
	rb := New[int](5)

	// Fill exactly to capacity
	for i := 1; i <= 5; i++ {
		rb.Add(i)
	}
	assert.Equal(t, []int{1, 2, 3, 4, 5}, rb.GetAll())

	// Add one more (should wrap)
	rb.Add(6)
	assert.Equal(t, []int{2, 3, 4, 5, 6}, rb.GetAll())

	// Add many more
	for i := 7; i <= 15; i++ {
		rb.Add(i)
	}
	assert.Equal(t, []int{11, 12, 13, 14, 15}, rb.GetAll())
}

func BenchmarkAdd(b *testing.B) {
	rb := New[int](1000)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		rb.Add(i)
	}
}

func BenchmarkGetLast(b *testing.B) {
	rb := New[int](1000)

	// Pre-fill buffer
	for i := range 1000 {
		rb.Add(i)
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = rb.GetLast(100)
	}
}

func BenchmarkGetAll(b *testing.B) {
	rb := New[int](1000)

	// Pre-fill buffer
	for i := range 1000 {
		rb.Add(i)
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = rb.GetAll()
	}
}
