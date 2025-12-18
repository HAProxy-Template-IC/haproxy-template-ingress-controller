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

	"haproxy-template-ic/pkg/k8s/types"
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
	var mu sync.Mutex
	var received []types.ChangeStats

	callback := func(_ types.Store, stats types.ChangeStats) {
		mu.Lock()
		received = append(received, stats)
		mu.Unlock()
	}

	debouncer := NewDebouncer(50*time.Millisecond, callback, store, false)
	debouncer.SetSyncMode(false) // Enable callbacks

	// Record creates
	debouncer.RecordCreate()
	debouncer.RecordCreate()
	debouncer.RecordCreate()

	// Wait for debounce to fire
	time.Sleep(100 * time.Millisecond)

	mu.Lock()
	require.Len(t, received, 1)
	assert.Equal(t, 3, received[0].Created)
	assert.Equal(t, 0, received[0].Modified)
	assert.Equal(t, 0, received[0].Deleted)
	mu.Unlock()
}

func TestDebouncer_RecordUpdate(t *testing.T) {
	store := &mockStore{}
	var mu sync.Mutex
	var received []types.ChangeStats

	callback := func(_ types.Store, stats types.ChangeStats) {
		mu.Lock()
		received = append(received, stats)
		mu.Unlock()
	}

	debouncer := NewDebouncer(50*time.Millisecond, callback, store, false)
	debouncer.SetSyncMode(false)

	debouncer.RecordUpdate()
	debouncer.RecordUpdate()

	time.Sleep(100 * time.Millisecond)

	mu.Lock()
	require.Len(t, received, 1)
	assert.Equal(t, 0, received[0].Created)
	assert.Equal(t, 2, received[0].Modified)
	assert.Equal(t, 0, received[0].Deleted)
	mu.Unlock()
}

func TestDebouncer_RecordDelete(t *testing.T) {
	store := &mockStore{}
	var mu sync.Mutex
	var received []types.ChangeStats

	callback := func(_ types.Store, stats types.ChangeStats) {
		mu.Lock()
		received = append(received, stats)
		mu.Unlock()
	}

	debouncer := NewDebouncer(50*time.Millisecond, callback, store, false)
	debouncer.SetSyncMode(false)

	debouncer.RecordDelete()

	time.Sleep(100 * time.Millisecond)

	mu.Lock()
	require.Len(t, received, 1)
	assert.Equal(t, 0, received[0].Created)
	assert.Equal(t, 0, received[0].Modified)
	assert.Equal(t, 1, received[0].Deleted)
	mu.Unlock()
}

func TestDebouncer_MixedOperations(t *testing.T) {
	store := &mockStore{}
	var mu sync.Mutex
	var received []types.ChangeStats

	callback := func(_ types.Store, stats types.ChangeStats) {
		mu.Lock()
		received = append(received, stats)
		mu.Unlock()
	}

	debouncer := NewDebouncer(50*time.Millisecond, callback, store, false)
	debouncer.SetSyncMode(false)

	// Mix of operations
	debouncer.RecordCreate()
	debouncer.RecordUpdate()
	debouncer.RecordUpdate()
	debouncer.RecordDelete()
	debouncer.RecordCreate()

	time.Sleep(100 * time.Millisecond)

	mu.Lock()
	require.Len(t, received, 1)
	assert.Equal(t, 2, received[0].Created)
	assert.Equal(t, 2, received[0].Modified)
	assert.Equal(t, 1, received[0].Deleted)
	mu.Unlock()
}

func TestDebouncer_DebounceBatching(t *testing.T) {
	store := &mockStore{}
	var callCount atomic.Int32

	callback := func(_ types.Store, _ types.ChangeStats) {
		callCount.Add(1)
	}

	// Use longer debounce interval to handle race detector overhead (2-10x slower)
	debouncer := NewDebouncer(200*time.Millisecond, callback, store, false)
	debouncer.SetSyncMode(false)

	// Record many changes in quick succession
	for i := 0; i < 10; i++ {
		debouncer.RecordCreate()
		time.Sleep(10 * time.Millisecond) // Less than debounce interval
	}

	// Wait for final debounce (longer to account for race detector overhead)
	time.Sleep(300 * time.Millisecond)

	// Should batch into single callback
	assert.Equal(t, int32(1), callCount.Load())
}

func TestDebouncer_Flush(t *testing.T) {
	store := &mockStore{}
	var mu sync.Mutex
	var received []types.ChangeStats

	callback := func(_ types.Store, stats types.ChangeStats) {
		mu.Lock()
		received = append(received, stats)
		mu.Unlock()
	}

	debouncer := NewDebouncer(1*time.Second, callback, store, false) // Long interval
	debouncer.SetSyncMode(false)

	debouncer.RecordCreate()
	debouncer.RecordUpdate()

	// Flush immediately without waiting
	debouncer.Flush()

	mu.Lock()
	require.Len(t, received, 1)
	assert.Equal(t, 1, received[0].Created)
	assert.Equal(t, 1, received[0].Modified)
	mu.Unlock()
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

	callback := func(_ types.Store, _ types.ChangeStats) {
		callCount.Add(1)
	}

	debouncer := NewDebouncer(50*time.Millisecond, callback, store, false)
	debouncer.SetSyncMode(false)

	debouncer.RecordCreate()

	// Stop before debounce fires
	debouncer.Stop()

	time.Sleep(100 * time.Millisecond)

	// Callback should not have been invoked
	assert.Equal(t, int32(0), callCount.Load())
}

func TestDebouncer_SyncMode(t *testing.T) {
	store := &mockStore{}
	var mu sync.Mutex
	var received []types.ChangeStats

	callback := func(_ types.Store, stats types.ChangeStats) {
		mu.Lock()
		received = append(received, stats)
		mu.Unlock()
	}

	debouncer := NewDebouncer(50*time.Millisecond, callback, store, false)

	// In sync mode by default
	debouncer.RecordCreate()

	time.Sleep(100 * time.Millisecond)

	mu.Lock()
	// Callbacks should fire (suppressDuringSync=false)
	require.Len(t, received, 1)
	assert.True(t, received[0].IsInitialSync)
	mu.Unlock()

	// Clear for next test
	mu.Lock()
	received = nil
	mu.Unlock()

	// Switch to normal mode
	debouncer.SetSyncMode(false)

	debouncer.RecordCreate()

	time.Sleep(100 * time.Millisecond)

	mu.Lock()
	require.Len(t, received, 1)
	assert.False(t, received[0].IsInitialSync)
	mu.Unlock()
}

func TestDebouncer_SuppressDuringSync(t *testing.T) {
	store := &mockStore{}
	var callCount atomic.Int32

	callback := func(_ types.Store, _ types.ChangeStats) {
		callCount.Add(1)
	}

	debouncer := NewDebouncer(50*time.Millisecond, callback, store, true) // suppress during sync

	// In sync mode, callbacks should be suppressed
	debouncer.RecordCreate()

	time.Sleep(100 * time.Millisecond)

	assert.Equal(t, int32(0), callCount.Load())

	// After sync completes, callbacks should work
	debouncer.SetSyncMode(false)
	debouncer.RecordCreate()

	time.Sleep(100 * time.Millisecond)

	assert.Equal(t, int32(1), callCount.Load())
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

	// Flush should bypass suppression
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

	// Wait for final debounce
	time.Sleep(50 * time.Millisecond)

	// Should have received at least one callback (exact count depends on timing)
	assert.GreaterOrEqual(t, callCount.Load(), int32(1))
}

func TestDebouncer_ResetAfterCallback(t *testing.T) {
	store := &mockStore{}
	var mu sync.Mutex
	var received []types.ChangeStats

	callback := func(_ types.Store, stats types.ChangeStats) {
		mu.Lock()
		received = append(received, stats)
		mu.Unlock()
	}

	debouncer := NewDebouncer(50*time.Millisecond, callback, store, false)
	debouncer.SetSyncMode(false)

	// First batch
	debouncer.RecordCreate()
	debouncer.RecordCreate()

	time.Sleep(100 * time.Millisecond)

	// Second batch (should be independent)
	debouncer.RecordUpdate()
	debouncer.RecordDelete()

	time.Sleep(100 * time.Millisecond)

	mu.Lock()
	require.Len(t, received, 2)
	// First batch
	assert.Equal(t, 2, received[0].Created)
	assert.Equal(t, 0, received[0].Modified)
	// Second batch
	assert.Equal(t, 0, received[1].Created)
	assert.Equal(t, 1, received[1].Modified)
	assert.Equal(t, 1, received[1].Deleted)
	mu.Unlock()
}
