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
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
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

func TestNew_Success(t *testing.T) {
	clientset := fake.NewClientset()
	callbacks := Callbacks{}

	elector, err := New(validConfig(), clientset, callbacks, nil)

	require.NoError(t, err)
	require.NotNil(t, elector)
}

func TestNew_NilConfig(t *testing.T) {
	clientset := fake.NewClientset()
	callbacks := Callbacks{}

	elector, err := New(nil, clientset, callbacks, nil)

	require.Error(t, err)
	assert.Nil(t, elector)
	assert.Contains(t, err.Error(), "config cannot be nil")
}

func TestNew_DisabledConfig(t *testing.T) {
	clientset := fake.NewClientset()
	callbacks := Callbacks{}
	config := validConfig()
	config.Enabled = false

	elector, err := New(config, clientset, callbacks, nil)

	require.Error(t, err)
	assert.Nil(t, elector)
	assert.Contains(t, err.Error(), "leader election is not enabled")
}

func TestNew_EmptyIdentity(t *testing.T) {
	clientset := fake.NewClientset()
	callbacks := Callbacks{}
	config := validConfig()
	config.Identity = ""

	elector, err := New(config, clientset, callbacks, nil)

	require.Error(t, err)
	assert.Nil(t, elector)
	assert.Contains(t, err.Error(), "identity cannot be empty")
}

func TestNew_EmptyLeaseName(t *testing.T) {
	clientset := fake.NewClientset()
	callbacks := Callbacks{}
	config := validConfig()
	config.LeaseName = ""

	elector, err := New(config, clientset, callbacks, nil)

	require.Error(t, err)
	assert.Nil(t, elector)
	assert.Contains(t, err.Error(), "lease name cannot be empty")
}

func TestNew_EmptyLeaseNamespace(t *testing.T) {
	clientset := fake.NewClientset()
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

func TestNew_InvalidDurations(t *testing.T) {
	tests := []struct {
		name      string
		configure func(*Config)
		want      string
	}{
		{
			name: "lease duration",
			configure: func(cfg *Config) {
				cfg.LeaseDuration = 0
			},
			want: "lease duration must be greater than zero",
		},
		{
			name: "renew deadline",
			configure: func(cfg *Config) {
				cfg.RenewDeadline = 0
			},
			want: "renew deadline must be greater than zero",
		},
		{
			name: "retry period",
			configure: func(cfg *Config) {
				cfg.RetryPeriod = 0
			},
			want: "retry period must be greater than zero",
		},
		{
			name: "lease and renew order",
			configure: func(cfg *Config) {
				cfg.LeaseDuration = cfg.RenewDeadline
			},
			want: "lease duration must be greater than renew deadline",
		},
		{
			name: "retry jitter",
			configure: func(cfg *Config) {
				cfg.RenewDeadline = 2 * time.Second
				cfg.RetryPeriod = 2 * time.Second
			},
			want: "renew deadline must be greater than retry period with jitter",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			cfg := validConfig()
			test.configure(cfg)
			elector, err := New(cfg, fake.NewClientset(), Callbacks{}, nil)
			require.ErrorContains(t, err, test.want)
			assert.Nil(t, elector)
		})
	}
}

func TestNew_NilLogger(t *testing.T) {
	clientset := fake.NewClientset()
	callbacks := Callbacks{}

	// Nil logger should be accepted (uses default)
	elector, err := New(validConfig(), clientset, callbacks, nil)

	require.NoError(t, err)
	require.NotNil(t, elector)
}

func TestNew_WithCustomLogger(t *testing.T) {
	clientset := fake.NewClientset()
	callbacks := Callbacks{}
	logger := slog.Default()

	elector, err := New(validConfig(), clientset, callbacks, logger)

	require.NoError(t, err)
	require.NotNil(t, elector)
}

func TestElector_Start_BecomesLeader(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping leader election test in short mode")
	}

	clientset := fake.NewClientset()

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

	// Start elector in goroutine
	errChan := make(chan error, 1)
	go func() {
		errChan <- elector.Start(ctx)
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
			if startedLeading.Load() {
				// Verify state
				assert.Equal(t, "test-pod-1", newLeaderIdentity.Load())
				cancel()
				return
			}
		}
	}
}

func TestElector_Start_ContextCancellation(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping leader election test in short mode")
	}

	clientset := fake.NewClientset()
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
		errChan <- elector.Start(ctx)
	}()

	// Wait until the elector has actually started leading (it writes the
	// Lease) before cancelling, so we exercise the cancel-while-leading path.
	// With a fake client, election completes in milliseconds.
	require.Eventually(t, func() bool {
		l, err := clientset.CoordinationV1().Leases("default").Get(
			context.Background(), "cancel-test-lease", metav1.GetOptions{})
		return err == nil && l.Spec.HolderIdentity != nil
	}, 3*time.Second, 20*time.Millisecond)

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

func TestElector_Start_OnStoppedLeadingCalled(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping leader election test in short mode")
	}

	clientset := fake.NewClientset()

	var startedLeading atomic.Bool
	var stoppedLeading atomic.Bool

	callbacks := Callbacks{
		OnStartedLeading: func(_ context.Context) {
			startedLeading.Store(true)
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
		errChan <- elector.Start(ctx)
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
			if startedLeading.Load() {
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

func TestElector_Callbacks_NilCallbacksHandledGracefully(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping leader election test in short mode")
	}

	clientset := fake.NewClientset()

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

	// Start should not panic with nil callbacks
	errChan := make(chan error, 1)
	go func() {
		errChan <- elector.Start(ctx)
	}()

	// Should become leader without panicking — observe via the Lease resource
	// the elector writes (no callbacks were provided to signal on). With a fake
	// client, election completes in milliseconds, so poll rather than sleep.
	var holder string
	require.Eventually(t, func() bool {
		l, err := clientset.CoordinationV1().Leases("default").Get(
			context.Background(), "nil-callback-lease", metav1.GetOptions{})
		if err != nil || l.Spec.HolderIdentity == nil {
			return false
		}
		holder = *l.Spec.HolderIdentity
		return true
	}, 3*time.Second, 20*time.Millisecond)
	assert.Equal(t, "test-pod-nil", holder)
}

func TestNew_AllConfigFieldsUsed(t *testing.T) {
	clientset := fake.NewClientset()

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
