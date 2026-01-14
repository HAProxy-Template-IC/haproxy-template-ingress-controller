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

package watcher

import (
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"gitlab.com/haproxy-haptic/haptic/pkg/k8s/types"
)

// mockStore implements types.Store for testing.
type mockStore struct{}

func (m *mockStore) Get(_ ...string) ([]interface{}, error)             { return nil, nil }
func (m *mockStore) List() ([]interface{}, error)                       { return nil, nil }
func (m *mockStore) Add(_ interface{}, _ []string) error                { return nil }
func (m *mockStore) Update(_ interface{}, _ []string) error             { return nil }
func (m *mockStore) Delete(_ ...string) error                           { return nil }
func (m *mockStore) Clear() error                                       { return nil }
func (m *mockStore) GetByPartialKey(_ ...string) ([]interface{}, error) { return nil, nil }

// callbackRecorder provides thread-safe callback recording with channel-based waiting.
// This eliminates flaky time.Sleep-based tests by using proper synchronization.
type callbackRecorder struct {
	mu       sync.Mutex
	received []types.ChangeStats
	times    []time.Time
	notify   chan struct{}
}

func newCallbackRecorder() *callbackRecorder {
	return &callbackRecorder{
		notify: make(chan struct{}, 100), // Buffered to avoid blocking
	}
}

func (r *callbackRecorder) callback(_ types.Store, stats types.ChangeStats) {
	r.mu.Lock()
	r.received = append(r.received, stats)
	r.times = append(r.times, time.Now())
	r.mu.Unlock()

	// Signal that a callback was received
	select {
	case r.notify <- struct{}{}:
	default:
		// Channel full, that's fine
	}
}

// waitForCallbacks waits until at least n callbacks have been received or timeout.
func (r *callbackRecorder) waitForCallbacks(t *testing.T, n int) {
	t.Helper()
	deadline := time.After(2 * time.Second)

	for {
		r.mu.Lock()
		count := len(r.received)
		r.mu.Unlock()

		if count >= n {
			return
		}

		select {
		case <-r.notify:
			// Got a callback, check count again
		case <-deadline:
			r.mu.Lock()
			actual := len(r.received)
			r.mu.Unlock()
			t.Fatalf("timeout waiting for %d callbacks, got %d", n, actual)
		}
	}
}

// getReceived returns a copy of received callbacks.
func (r *callbackRecorder) getReceived() []types.ChangeStats {
	r.mu.Lock()
	defer r.mu.Unlock()
	result := make([]types.ChangeStats, len(r.received))
	copy(result, r.received)
	return result
}

// getTimes returns a copy of callback times.
func (r *callbackRecorder) getTimes() []time.Time {
	r.mu.Lock()
	defer r.mu.Unlock()
	result := make([]time.Time, len(r.times))
	copy(result, r.times)
	return result
}

// clear resets the recorder.
func (r *callbackRecorder) clear() {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.received = nil
	r.times = nil
}

func TestNewDebouncer(t *testing.T) {
	store := &mockStore{}
	callback := func(types.Store, types.ChangeStats) {}

	debouncer := NewDebouncer(100*time.Millisecond, callback, store, false)

	require.NotNil(t, debouncer)
	assert.Equal(t, 100*time.Millisecond, debouncer.interval)
	assert.True(t, debouncer.syncMode, "should start in sync mode")
	assert.False(t, debouncer.suppressDuringSync)
}

func TestDebouncer_RecordCreate(t *testing.T) {
	store := &mockStore{}
	recorder := newCallbackRecorder()

	debouncer := NewDebouncer(50*time.Millisecond, recorder.callback, store, false)
	debouncer.SetSyncMode(false) // Enable callbacks

	// Record creates
	debouncer.RecordCreate()
	debouncer.RecordCreate()
	debouncer.RecordCreate()

	// Wait for callback (with leading edge, first fires immediately, rest batched)
	recorder.waitForCallbacks(t, 1)

	received := recorder.getReceived()
	require.Len(t, received, 1)
	assert.Equal(t, 3, received[0].Created)
	assert.Equal(t, 0, received[0].Modified)
	assert.Equal(t, 0, received[0].Deleted)
}

func TestDebouncer_RecordUpdate(t *testing.T) {
	store := &mockStore{}
	recorder := newCallbackRecorder()

	debouncer := NewDebouncer(50*time.Millisecond, recorder.callback, store, false)
	debouncer.SetSyncMode(false)

	debouncer.RecordUpdate()
	debouncer.RecordUpdate()

	recorder.waitForCallbacks(t, 1)

	received := recorder.getReceived()
	require.Len(t, received, 1)
	assert.Equal(t, 0, received[0].Created)
	assert.Equal(t, 2, received[0].Modified)
	assert.Equal(t, 0, received[0].Deleted)
}

func TestDebouncer_RecordDelete(t *testing.T) {
	store := &mockStore{}
	recorder := newCallbackRecorder()

	debouncer := NewDebouncer(50*time.Millisecond, recorder.callback, store, false)
	debouncer.SetSyncMode(false)

	debouncer.RecordDelete()

	recorder.waitForCallbacks(t, 1)

	received := recorder.getReceived()
	require.Len(t, received, 1)
	assert.Equal(t, 0, received[0].Created)
	assert.Equal(t, 0, received[0].Modified)
	assert.Equal(t, 1, received[0].Deleted)
}

func TestDebouncer_MixedOperations(t *testing.T) {
	store := &mockStore{}
	recorder := newCallbackRecorder()

	debouncer := NewDebouncer(50*time.Millisecond, recorder.callback, store, false)
	debouncer.SetSyncMode(false)

	// First create fires immediately (leading edge)
	debouncer.RecordCreate()

	// Wait for first callback
	recorder.waitForCallbacks(t, 1)

	// These operations happen within refractory period - batched together
	debouncer.RecordUpdate()
	debouncer.RecordUpdate()
	debouncer.RecordDelete()
	debouncer.RecordCreate()

	// Wait for second callback (batched)
	recorder.waitForCallbacks(t, 2)

	received := recorder.getReceived()

	// With leading-edge behavior:
	// - First callback: immediate (just the first create)
	// - Second callback: batched operations during refractory period
	require.Len(t, received, 2)

	// First callback: immediate create
	assert.Equal(t, 1, received[0].Created)
	assert.Equal(t, 0, received[0].Modified)
	assert.Equal(t, 0, received[0].Deleted)

	// Second callback: batched operations
	assert.Equal(t, 1, received[1].Created)
	assert.Equal(t, 2, received[1].Modified)
	assert.Equal(t, 1, received[1].Deleted)
}

func TestDebouncer_DebounceBatching(t *testing.T) {
	store := &mockStore{}
	var callCount atomic.Int32
	notify := make(chan struct{}, 100)

	callback := func(_ types.Store, _ types.ChangeStats) {
		callCount.Add(1)
		select {
		case notify <- struct{}{}:
		default:
		}
	}

	// Use longer debounce interval
	debouncer := NewDebouncer(200*time.Millisecond, callback, store, false)
	debouncer.SetSyncMode(false)

	// Record many changes in quick succession
	for i := 0; i < 10; i++ {
		debouncer.RecordCreate()
		time.Sleep(10 * time.Millisecond) // Less than debounce interval
	}

	// Wait for callbacks with proper synchronization
	deadline := time.After(3 * time.Second)
	expectedCallbacks := int32(2) // Leading edge: first immediate, rest batched

waitLoop:
	for callCount.Load() < expectedCallbacks {
		select {
		case <-notify:
			// Got a callback
		case <-deadline:
			// Timeout reached, exit loop and check what we have
			break waitLoop
		}
	}

	// With leading-edge behavior:
	// - First change fires immediately
	// - Subsequent changes during refractory period are batched
	// - Total: 2 callbacks (first immediate, second after refractory)
	assert.Equal(t, expectedCallbacks, callCount.Load())
}

func TestDebouncer_Flush(t *testing.T) {
	store := &mockStore{}
	recorder := newCallbackRecorder()

	debouncer := NewDebouncer(1*time.Second, recorder.callback, store, false) // Long interval
	debouncer.SetSyncMode(false)

	debouncer.RecordCreate()
	debouncer.RecordUpdate()

	// Flush immediately without waiting
	debouncer.Flush()

	// Flush is synchronous, so callback should have been called
	received := recorder.getReceived()
	require.Len(t, received, 1)
	assert.Equal(t, 1, received[0].Created)
	assert.Equal(t, 1, received[0].Modified)
}

func TestDebouncer_FlushEmpty(t *testing.T) {
	store := &mockStore{}
	var callCount atomic.Int32

	callback := func(_ types.Store, _ types.ChangeStats) {
		callCount.Add(1)
	}

	debouncer := NewDebouncer(100*time.Millisecond, callback, store, false)

	// Flush with no changes
	debouncer.Flush()

	// Should not invoke callback for empty stats
	assert.Equal(t, int32(0), callCount.Load())
}

func TestDebouncer_Stop(t *testing.T) {
	store := &mockStore{}
	var callCount atomic.Int32
	notify := make(chan struct{}, 10)

	callback := func(_ types.Store, _ types.ChangeStats) {
		callCount.Add(1)
		select {
		case notify <- struct{}{}:
		default:
		}
	}

	debouncer := NewDebouncer(50*time.Millisecond, callback, store, false)
	debouncer.SetSyncMode(false)

	// First change fires immediately with leading-edge behavior
	debouncer.RecordCreate()

	// Wait for the immediate callback
	select {
	case <-notify:
	case <-time.After(2 * time.Second):
		t.Fatal("timeout waiting for first callback")
	}

	// Record another change (will be queued for refractory period)
	debouncer.RecordUpdate()

	// Stop before the queued callback fires
	debouncer.Stop()

	// Give time to verify no more callbacks come
	time.Sleep(150 * time.Millisecond)

	// Only the first immediate callback should have been invoked
	// The second (queued) callback should have been stopped
	assert.Equal(t, int32(1), callCount.Load())
}

func TestDebouncer_SyncMode(t *testing.T) {
	store := &mockStore{}
	recorder := newCallbackRecorder()

	debouncer := NewDebouncer(50*time.Millisecond, recorder.callback, store, false)

	// In sync mode by default
	debouncer.RecordCreate()

	// Wait for callback
	recorder.waitForCallbacks(t, 1)

	received := recorder.getReceived()
	// Callbacks should fire (suppressDuringSync=false)
	require.Len(t, received, 1)
	assert.True(t, received[0].IsInitialSync)

	// Clear for next test
	recorder.clear()

	// Switch to normal mode
	debouncer.SetSyncMode(false)

	debouncer.RecordCreate()

	recorder.waitForCallbacks(t, 1)

	received = recorder.getReceived()
	require.Len(t, received, 1)
	assert.False(t, received[0].IsInitialSync)
}

func TestDebouncer_SuppressDuringSync(t *testing.T) {
	store := &mockStore{}
	var callCount atomic.Int32
	notify := make(chan struct{}, 10)

	callback := func(_ types.Store, _ types.ChangeStats) {
		callCount.Add(1)
		select {
		case notify <- struct{}{}:
		default:
		}
	}

	debouncer := NewDebouncer(50*time.Millisecond, callback, store, true) // suppress during sync

	// In sync mode, callbacks should be suppressed but stats preserved
	debouncer.RecordCreate()

	// Brief wait - callback should NOT fire
	time.Sleep(100 * time.Millisecond)
	assert.Equal(t, int32(0), callCount.Load())

	// After sync completes, SetSyncMode(false) flushes accumulated stats
	debouncer.SetSyncMode(false)

	// Wait for the flush callback
	select {
	case <-notify:
	case <-time.After(2 * time.Second):
		t.Fatal("timeout waiting for flush callback")
	}

	// The suppressed event from sync should now be delivered
	assert.Equal(t, int32(1), callCount.Load())

	// New events after sync should also trigger callbacks
	debouncer.RecordCreate()

	select {
	case <-notify:
	case <-time.After(2 * time.Second):
		t.Fatal("timeout waiting for post-sync callback")
	}

	assert.Equal(t, int32(2), callCount.Load())
}

func TestDebouncer_FlushBypassesSuppression(t *testing.T) {
	store := &mockStore{}
	var callCount atomic.Int32

	callback := func(_ types.Store, _ types.ChangeStats) {
		callCount.Add(1)
	}

	debouncer := NewDebouncer(1*time.Second, callback, store, true) // suppress during sync

	// In sync mode with suppression
	debouncer.RecordCreate()

	// Flush should bypass suppression (synchronous)
	debouncer.Flush()

	assert.Equal(t, int32(1), callCount.Load())
}

func TestDebouncer_GetInitialCount(t *testing.T) {
	store := &mockStore{}
	callback := func(_ types.Store, _ types.ChangeStats) {}

	debouncer := NewDebouncer(1*time.Second, callback, store, true)

	// Record some creates during sync
	debouncer.RecordCreate()
	debouncer.RecordCreate()
	debouncer.RecordCreate()

	// Get initial count (before flushing)
	count := debouncer.GetInitialCount()
	assert.Equal(t, 3, count)
}

func TestDebouncer_NilCallback(t *testing.T) {
	store := &mockStore{}

	// Should not panic with nil callback
	debouncer := NewDebouncer(50*time.Millisecond, nil, store, false)
	debouncer.SetSyncMode(false)

	debouncer.RecordCreate()

	// Wait for potential callback (none should come, but shouldn't panic)
	time.Sleep(100 * time.Millisecond)

	// Test passed if no panic

	debouncer.Flush()
	// Still no panic
}

func TestDebouncer_ConcurrentAccess(t *testing.T) {
	store := &mockStore{}
	var callCount atomic.Int32

	callback := func(_ types.Store, _ types.ChangeStats) {
		callCount.Add(1)
	}

	debouncer := NewDebouncer(10*time.Millisecond, callback, store, false)
	debouncer.SetSyncMode(false)

	// Concurrent access from multiple goroutines
	var wg sync.WaitGroup
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 100; j++ {
				switch j % 3 {
				case 0:
					debouncer.RecordCreate()
				case 1:
					debouncer.RecordUpdate()
				case 2:
					debouncer.RecordDelete()
				}
			}
		}()
	}

	wg.Wait()

	// Flush to ensure all pending callbacks complete
	debouncer.Flush()

	// Should have received at least one callback (exact count depends on timing)
	assert.GreaterOrEqual(t, callCount.Load(), int32(1))
}

func TestDebouncer_ResetAfterCallback(t *testing.T) {
	store := &mockStore{}
	recorder := newCallbackRecorder()

	debouncer := NewDebouncer(50*time.Millisecond, recorder.callback, store, false)
	debouncer.SetSyncMode(false)

	// First batch
	debouncer.RecordCreate()
	debouncer.RecordCreate()

	recorder.waitForCallbacks(t, 1)

	// Wait for refractory period to expire
	time.Sleep(100 * time.Millisecond)

	// Second batch (should be independent)
	debouncer.RecordUpdate()
	debouncer.RecordDelete()

	recorder.waitForCallbacks(t, 2)

	received := recorder.getReceived()
	require.Len(t, received, 2)
	// First batch
	assert.Equal(t, 2, received[0].Created)
	assert.Equal(t, 0, received[0].Modified)
	// Second batch
	assert.Equal(t, 0, received[1].Created)
	assert.Equal(t, 1, received[1].Modified)
	assert.Equal(t, 1, received[1].Deleted)
}

func TestDebouncer_LeadingEdge_ImmediateFire(t *testing.T) {
	// Test that first change fires immediately when no recent activity
	store := &mockStore{}
	recorder := newCallbackRecorder()

	debouncer := NewDebouncer(200*time.Millisecond, recorder.callback, store, false)
	debouncer.SetSyncMode(false)

	startTime := time.Now()
	debouncer.RecordCreate()

	// Wait for callback
	recorder.waitForCallbacks(t, 1)

	times := recorder.getTimes()
	require.Len(t, times, 1, "should have received one callback")
	callbackDelay := times[0].Sub(startTime)

	// Callback should fire almost immediately (within 100ms), not after 200ms debounce interval
	assert.Less(t, callbackDelay, 100*time.Millisecond, "first change should fire immediately, not wait for debounce interval")
}

func TestDebouncer_LeadingEdge_RefractoryPeriod(t *testing.T) {
	// Test that changes within refractory period are queued and batched
	store := &mockStore{}
	recorder := newCallbackRecorder()

	debouncer := NewDebouncer(100*time.Millisecond, recorder.callback, store, false)
	debouncer.SetSyncMode(false)

	startTime := time.Now()

	// First change fires immediately
	debouncer.RecordCreate()

	// Wait for first callback
	recorder.waitForCallbacks(t, 1)

	// These changes are within refractory period - should be queued
	debouncer.RecordUpdate()
	debouncer.RecordDelete()

	// Wait for second callback (after refractory period)
	recorder.waitForCallbacks(t, 2)

	received := recorder.getReceived()
	times := recorder.getTimes()
	require.Len(t, received, 2, "should have received two callbacks")

	// First callback: immediate (just create)
	assert.Equal(t, 1, received[0].Created)
	assert.Equal(t, 0, received[0].Modified)
	assert.Equal(t, 0, received[0].Deleted)

	// Second callback: batched changes during refractory period
	assert.Equal(t, 0, received[1].Created)
	assert.Equal(t, 1, received[1].Modified)
	assert.Equal(t, 1, received[1].Deleted)

	// First callback should be very fast (immediate)
	firstCallbackDelay := times[0].Sub(startTime)
	assert.Less(t, firstCallbackDelay, 100*time.Millisecond, "first callback should be immediate")

	// Second callback should fire after refractory period expires
	timeBetweenCallbacks := times[1].Sub(times[0])
	assert.GreaterOrEqual(t, timeBetweenCallbacks, 50*time.Millisecond, "second callback should wait for refractory period")
}

func TestDebouncer_LeadingEdge_NoRecentActivity(t *testing.T) {
	// Test that after refractory period expires, next change fires immediately again
	store := &mockStore{}
	recorder := newCallbackRecorder()

	debouncer := NewDebouncer(50*time.Millisecond, recorder.callback, store, false)
	debouncer.SetSyncMode(false)

	// First change
	time1 := time.Now()
	debouncer.RecordCreate()

	// Wait for callback
	recorder.waitForCallbacks(t, 1)

	// Wait for refractory period to fully expire
	time.Sleep(100 * time.Millisecond)

	// Second change should also fire immediately
	time2 := time.Now()
	debouncer.RecordCreate()

	// Wait for second callback
	recorder.waitForCallbacks(t, 2)

	times := recorder.getTimes()
	require.Len(t, times, 2, "should have two callbacks")

	// Both callbacks should be fast (immediate)
	delay1 := times[0].Sub(time1)
	delay2 := times[1].Sub(time2)
	assert.Less(t, delay1, 100*time.Millisecond, "first callback should be immediate")
	assert.Less(t, delay2, 100*time.Millisecond, "second callback should also be immediate after refractory expires")
}
