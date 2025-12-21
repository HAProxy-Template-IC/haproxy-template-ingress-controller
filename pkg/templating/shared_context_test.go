package templating

import (
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func TestSharedContext_ComputeIfAbsent_Basic(t *testing.T) {
	ctx := NewSharedContext()

	// First call should compute and return wasComputed=true
	computeCount := 0
	result, wasComputed := ctx.ComputeIfAbsent("key", func() interface{} {
		computeCount++
		return "computed"
	})
	assert.Equal(t, "computed", result)
	assert.True(t, wasComputed, "first call should have wasComputed=true")
	assert.Equal(t, 1, computeCount)

	// Second call should return cached value with wasComputed=false
	result, wasComputed = ctx.ComputeIfAbsent("key", func() interface{} {
		computeCount++
		return "should not be called"
	})
	assert.Equal(t, "computed", result)
	assert.False(t, wasComputed, "second call should have wasComputed=false")
	assert.Equal(t, 1, computeCount) // Still 1
}

func TestSharedContext_FirstSeenPattern(t *testing.T) {
	ctx := NewSharedContext()

	// First time should return wasComputed=true
	_, wasFirst := ctx.ComputeIfAbsent("seen:key1", func() interface{} { return true })
	assert.True(t, wasFirst, "first occurrence should have wasComputed=true")

	// Second time should return wasComputed=false
	_, wasFirst = ctx.ComputeIfAbsent("seen:key1", func() interface{} { return true })
	assert.False(t, wasFirst, "second occurrence should have wasComputed=false")

	// Different key should return wasComputed=true
	_, wasFirst = ctx.ComputeIfAbsent("seen:key2", func() interface{} { return true })
	assert.True(t, wasFirst, "different key should have wasComputed=true")
}

func TestSharedContext_FirstSeenPattern_Concurrent(t *testing.T) {
	ctx := NewSharedContext()
	const numGoroutines = 100

	var firstCount atomic.Int32
	var wg sync.WaitGroup
	wg.Add(numGoroutines)

	for i := 0; i < numGoroutines; i++ {
		go func() {
			defer wg.Done()
			_, wasFirst := ctx.ComputeIfAbsent("seen:concurrent_key", func() interface{} {
				return true
			})
			if wasFirst {
				firstCount.Add(1)
			}
		}()
	}

	wg.Wait()

	// Exactly one goroutine should see wasComputed=true
	assert.Equal(t, int32(1), firstCount.Load())
}

func TestSharedContext_ComputeIfAbsent_Concurrent(t *testing.T) {
	ctx := NewSharedContext()
	const numGoroutines = 100

	var computeCount atomic.Int32
	var wg sync.WaitGroup
	wg.Add(numGoroutines)

	for i := 0; i < numGoroutines; i++ {
		go func() {
			defer wg.Done()
			result, _ := ctx.ComputeIfAbsent("expensive_key", func() interface{} {
				computeCount.Add(1)
				// Simulate expensive computation
				time.Sleep(10 * time.Millisecond)
				return "expensive_result"
			})
			// All goroutines should get the same result
			assert.Equal(t, "expensive_result", result)
		}()
	}

	wg.Wait()

	// Only one goroutine should have computed (singleflight)
	assert.Equal(t, int32(1), computeCount.Load())

	// Verify value is still stored
	result, wasComputed := ctx.ComputeIfAbsent("expensive_key", func() interface{} {
		return "should not be called"
	})
	assert.Equal(t, "expensive_result", result)
	assert.False(t, wasComputed)
}

func TestSharedContext_ComputeIfAbsent_DifferentKeys(t *testing.T) {
	ctx := NewSharedContext()

	var computeCount atomic.Int32
	var wg sync.WaitGroup
	wg.Add(3)

	// Three different keys should all compute
	for i := 0; i < 3; i++ {
		go func() {
			defer wg.Done()
			_, wasComputed := ctx.ComputeIfAbsent("key"+string(rune('a'+i)), func() interface{} {
				computeCount.Add(1)
				return i
			})
			assert.True(t, wasComputed, "each unique key should have wasComputed=true")
		}()
	}

	wg.Wait()

	// All three should have computed
	assert.Equal(t, int32(3), computeCount.Load())
}

func TestSharedContext_Get(t *testing.T) {
	ctx := NewSharedContext()

	// Get returns nil for missing key
	assert.Nil(t, ctx.Get("missing"))

	// After ComputeIfAbsent, Get returns the value
	ctx.ComputeIfAbsent("key", func() interface{} { return "value" })
	assert.Equal(t, "value", ctx.Get("key"))

	// Get returns nil stored values correctly
	ctx.ComputeIfAbsent("nil_key", func() interface{} { return nil })
	assert.Nil(t, ctx.Get("nil_key"))
}

func TestSharedContext_NilFallback(t *testing.T) {
	ctx := NewSharedContext()

	// Pre-populate a value
	ctx.ComputeIfAbsent("exists", func() interface{} { return "value" })

	// Read existing value with nil fallback
	result, wasComputed := ctx.ComputeIfAbsent("exists", func() interface{} { return nil })
	assert.Equal(t, "value", result)
	assert.False(t, wasComputed)

	// Read non-existing value with nil fallback - stores nil
	result, wasComputed = ctx.ComputeIfAbsent("missing", func() interface{} { return nil })
	assert.Nil(t, result)
	assert.True(t, wasComputed)
}

func TestSharedContext_NestedComputeIfAbsent(t *testing.T) {
	// This test verifies that ComputeIfAbsent can be called from within
	// a compute function without causing a deadlock.
	ctx := NewSharedContext()

	// Outer computation calls ComputeIfAbsent for a different key
	result, wasComputed := ctx.ComputeIfAbsent("outer", func() interface{} {
		// Nested call - this would deadlock if lock was held during compute
		inner, innerWasComputed := ctx.ComputeIfAbsent("inner", func() interface{} {
			return "inner_value"
		})
		assert.True(t, innerWasComputed, "nested compute should be first")
		return "outer_" + inner.(string)
	})

	assert.Equal(t, "outer_inner_value", result)
	assert.True(t, wasComputed)

	// Verify both values are stored
	result2, wasComputed2 := ctx.ComputeIfAbsent("inner", func() interface{} {
		return "should not compute"
	})
	assert.Equal(t, "inner_value", result2)
	assert.False(t, wasComputed2)
}

func TestSharedContext_NestedComputeIfAbsent_DeepNesting(t *testing.T) {
	ctx := NewSharedContext()

	// Test deep nesting (3 levels)
	result, _ := ctx.ComputeIfAbsent("level1", func() interface{} {
		result2, _ := ctx.ComputeIfAbsent("level2", func() interface{} {
			result3, _ := ctx.ComputeIfAbsent("level3", func() interface{} {
				return "deepest"
			})
			return "level2_" + result3.(string)
		})
		return "level1_" + result2.(string)
	})

	assert.Equal(t, "level1_level2_deepest", result)
}
