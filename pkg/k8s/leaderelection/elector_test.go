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

package leaderelection

import (
	"context"
	"log/slog"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"k8s.io/client-go/kubernetes/fake"
)

func validConfig() *Config {
	return &Config{
		Enabled:         true,
		Identity:        "test-pod-1",
		LeaseName:       "test-lease",
		LeaseNamespace:  "default",
		LeaseDuration:   15 * time.Second,
		RenewDeadline:   10 * time.Second,
		RetryPeriod:     2 * time.Second,
		ReleaseOnCancel: true,
	}
}

// =============================================================================
// New() Tests - Configuration Validation
// =============================================================================

func TestNew_Success(t *testing.T) {
	clientset := fake.NewSimpleClientset()
	callbacks := Callbacks{}

	elector, err := New(validConfig(), clientset, callbacks, nil)

	require.NoError(t, err)
	require.NotNil(t, elector)
	assert.False(t, elector.IsLeader())
	assert.Empty(t, elector.GetLeader())
}

func TestNew_NilConfig(t *testing.T) {
	clientset := fake.NewSimpleClientset()
	callbacks := Callbacks{}

	elector, err := New(nil, clientset, callbacks, nil)

	require.Error(t, err)
	assert.Nil(t, elector)
	assert.Contains(t, err.Error(), "config cannot be nil")
}

func TestNew_DisabledConfig(t *testing.T) {
	clientset := fake.NewSimpleClientset()
	callbacks := Callbacks{}
	config := validConfig()
	config.Enabled = false

	elector, err := New(config, clientset, callbacks, nil)

	require.Error(t, err)
	assert.Nil(t, elector)
	assert.Contains(t, err.Error(), "leader election is not enabled")
}

func TestNew_EmptyIdentity(t *testing.T) {
	clientset := fake.NewSimpleClientset()
	callbacks := Callbacks{}
	config := validConfig()
	config.Identity = ""

	elector, err := New(config, clientset, callbacks, nil)

	require.Error(t, err)
	assert.Nil(t, elector)
	assert.Contains(t, err.Error(), "identity cannot be empty")
}

func TestNew_EmptyLeaseName(t *testing.T) {
	clientset := fake.NewSimpleClientset()
	callbacks := Callbacks{}
	config := validConfig()
	config.LeaseName = ""

	elector, err := New(config, clientset, callbacks, nil)

	require.Error(t, err)
	assert.Nil(t, elector)
	assert.Contains(t, err.Error(), "lease name cannot be empty")
}

func TestNew_EmptyLeaseNamespace(t *testing.T) {
	clientset := fake.NewSimpleClientset()
	callbacks := Callbacks{}
	config := validConfig()
	config.LeaseNamespace = ""

	elector, err := New(config, clientset, callbacks, nil)

	require.Error(t, err)
	assert.Nil(t, elector)
	assert.Contains(t, err.Error(), "lease namespace cannot be empty")
}

func TestNew_NilClientset(t *testing.T) {
	callbacks := Callbacks{}

	elector, err := New(validConfig(), nil, callbacks, nil)

	require.Error(t, err)
	assert.Nil(t, elector)
	assert.Contains(t, err.Error(), "clientset cannot be nil")
}

func TestNew_NilLogger(t *testing.T) {
	clientset := fake.NewSimpleClientset()
	callbacks := Callbacks{}

	// Nil logger should be accepted (uses default)
	elector, err := New(validConfig(), clientset, callbacks, nil)

	require.NoError(t, err)
	require.NotNil(t, elector)
}

func TestNew_WithCustomLogger(t *testing.T) {
	clientset := fake.NewSimpleClientset()
	callbacks := Callbacks{}
	logger := slog.Default()

	elector, err := New(validConfig(), clientset, callbacks, logger)

	require.NoError(t, err)
	require.NotNil(t, elector)
}

// =============================================================================
// State Accessor Tests
// =============================================================================

func TestElector_IsLeader_InitialState(t *testing.T) {
	clientset := fake.NewSimpleClientset()
	callbacks := Callbacks{}

	elector, err := New(validConfig(), clientset, callbacks, nil)
	require.NoError(t, err)

	assert.False(t, elector.IsLeader())
}

func TestElector_GetLeader_InitialState(t *testing.T) {
	clientset := fake.NewSimpleClientset()
	callbacks := Callbacks{}

	elector, err := New(validConfig(), clientset, callbacks, nil)
	require.NoError(t, err)

	assert.Empty(t, elector.GetLeader())
}

// =============================================================================
// Run() Tests with Fake Clientset
// =============================================================================

func TestElector_Run_BecomesLeader(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping leader election test in short mode")
	}

	clientset := fake.NewSimpleClientset()

	var startedLeading atomic.Bool
	var newLeaderIdentity atomic.Value
	newLeaderIdentity.Store("")

	callbacks := Callbacks{
		OnStartedLeading: func(_ context.Context) {
			startedLeading.Store(true)
		},
		OnNewLeader: func(identity string) {
			newLeaderIdentity.Store(identity)
		},
	}

	config := &Config{
		Enabled:         true,
		Identity:        "test-pod-1",
		LeaseName:       "test-lease",
		LeaseNamespace:  "default",
		LeaseDuration:   5 * time.Second,
		RenewDeadline:   3 * time.Second,
		RetryPeriod:     1 * time.Second,
		ReleaseOnCancel: true,
	}

	elector, err := New(config, clientset, callbacks, nil)
	require.NoError(t, err)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	// Run elector in goroutine
	errChan := make(chan error, 1)
	go func() {
		errChan <- elector.Run(ctx)
	}()

	// Wait for leader election to complete
	deadline := time.After(8 * time.Second)
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()

	for {
		select {
		case <-deadline:
			t.Fatal("timeout waiting for leader election")
		case <-ticker.C:
			if elector.IsLeader() {
				// Verify state
				assert.True(t, startedLeading.Load())
				assert.Equal(t, "test-pod-1", elector.GetLeader())
				assert.Equal(t, "test-pod-1", newLeaderIdentity.Load())
				cancel()
				return
			}
		}
	}
}

func TestElector_Run_ContextCancellation(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping leader election test in short mode")
	}

	clientset := fake.NewSimpleClientset()
	callbacks := Callbacks{}

	config := &Config{
		Enabled:         true,
		Identity:        "test-pod-1",
		LeaseName:       "cancel-test-lease",
		LeaseNamespace:  "default",
		LeaseDuration:   5 * time.Second,
		RenewDeadline:   3 * time.Second,
		RetryPeriod:     1 * time.Second,
		ReleaseOnCancel: true,
	}

	elector, err := New(config, clientset, callbacks, nil)
	require.NoError(t, err)

	ctx, cancel := context.WithCancel(context.Background())

	errChan := make(chan error, 1)
	go func() {
		errChan <- elector.Run(ctx)
	}()

	// Give it time to start
	time.Sleep(500 * time.Millisecond)

	// Cancel context
	cancel()

	// Should exit cleanly
	select {
	case err := <-errChan:
		assert.NoError(t, err)
	case <-time.After(3 * time.Second):
		t.Fatal("timeout waiting for elector to stop")
	}
}

func TestElector_Run_OnStoppedLeadingCalled(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping leader election test in short mode")
	}

	clientset := fake.NewSimpleClientset()

	var stoppedLeading atomic.Bool

	callbacks := Callbacks{
		OnStartedLeading: func(_ context.Context) {
			// Do nothing
		},
		OnStoppedLeading: func() {
			stoppedLeading.Store(true)
		},
	}

	config := &Config{
		Enabled:         true,
		Identity:        "test-pod-stop",
		LeaseName:       "stopped-test-lease",
		LeaseNamespace:  "default",
		LeaseDuration:   5 * time.Second,
		RenewDeadline:   3 * time.Second,
		RetryPeriod:     1 * time.Second,
		ReleaseOnCancel: true,
	}

	elector, err := New(config, clientset, callbacks, nil)
	require.NoError(t, err)

	ctx, cancel := context.WithCancel(context.Background())

	errChan := make(chan error, 1)
	go func() {
		errChan <- elector.Run(ctx)
	}()

	// Wait to become leader
	deadline := time.After(8 * time.Second)
	ticker := time.NewTicker(100 * time.Millisecond)

	for {
		select {
		case <-deadline:
			ticker.Stop()
			cancel()
			t.Fatal("timeout waiting for leader election")
		case <-ticker.C:
			if elector.IsLeader() {
				ticker.Stop()
				// Cancel to trigger stopped leading
				cancel()

				// Wait for stopped leading callback
				select {
				case <-errChan:
					// Check if callback was called
					assert.True(t, stoppedLeading.Load(), "OnStoppedLeading should be called")
					return
				case <-time.After(5 * time.Second):
					t.Fatal("timeout waiting for elector to stop")
				}
			}
		}
	}
}

// =============================================================================
// Callback Tests
// =============================================================================

func TestElector_Callbacks_NilCallbacksHandledGracefully(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping leader election test in short mode")
	}

	clientset := fake.NewSimpleClientset()

	// All nil callbacks
	callbacks := Callbacks{
		OnStartedLeading: nil,
		OnStoppedLeading: nil,
		OnNewLeader:      nil,
	}

	config := &Config{
		Enabled:         true,
		Identity:        "test-pod-nil",
		LeaseName:       "nil-callback-lease",
		LeaseNamespace:  "default",
		LeaseDuration:   5 * time.Second,
		RenewDeadline:   3 * time.Second,
		RetryPeriod:     1 * time.Second,
		ReleaseOnCancel: true,
	}

	elector, err := New(config, clientset, callbacks, nil)
	require.NoError(t, err)

	ctx, cancel := context.WithTimeout(context.Background(), 6*time.Second)
	defer cancel()

	// Run should not panic with nil callbacks
	errChan := make(chan error, 1)
	go func() {
		errChan <- elector.Run(ctx)
	}()

	// Wait for leader election
	time.Sleep(4 * time.Second)

	// Should become leader without panicking
	assert.True(t, elector.IsLeader())
}

// =============================================================================
// Thread Safety Tests
// =============================================================================

func TestElector_ConcurrentAccess(t *testing.T) {
	clientset := fake.NewSimpleClientset()
	callbacks := Callbacks{}

	elector, err := New(validConfig(), clientset, callbacks, nil)
	require.NoError(t, err)

	// Concurrent access to IsLeader and GetLeader
	done := make(chan struct{})
	for i := 0; i < 10; i++ {
		go func() {
			for j := 0; j < 100; j++ {
				_ = elector.IsLeader()
				_ = elector.GetLeader()
			}
			done <- struct{}{}
		}()
	}

	// Wait for all goroutines
	for i := 0; i < 10; i++ {
		<-done
	}
}

// =============================================================================
// Config Edge Cases
// =============================================================================

func TestNew_AllConfigFieldsUsed(t *testing.T) {
	clientset := fake.NewSimpleClientset()

	config := &Config{
		Enabled:         true,
		Identity:        "pod-123",
		LeaseName:       "my-lease",
		LeaseNamespace:  "kube-system",
		LeaseDuration:   30 * time.Second,
		RenewDeadline:   20 * time.Second,
		RetryPeriod:     5 * time.Second,
		ReleaseOnCancel: false,
	}

	callbacks := Callbacks{
		OnStartedLeading: func(_ context.Context) {},
		OnStoppedLeading: func() {},
		OnNewLeader:      func(_ string) {},
	}

	elector, err := New(config, clientset, callbacks, nil)

	require.NoError(t, err)
	require.NotNil(t, elector)
}
