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

package deployer

import (
	"context"
	"errors"
	"log/slog"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"gitlab.com/haproxy-haptic/haptic/pkg/controller/events"
	"gitlab.com/haproxy-haptic/haptic/pkg/controller/testutil"
	"gitlab.com/haproxy-haptic/haptic/pkg/dataplane"
)

// fakeRuntimeSyncer is a test double for *dataplane.Client used by the bypass.
type fakeRuntimeSyncer struct {
	sync    func() (*dataplane.SyncResult, error)
	closes  *atomic.Int32 // optional shared close counter
	onClose func()
}

func (f *fakeRuntimeSyncer) SyncRuntimeFast(_ context.Context, _ *dataplane.RuntimeServerUpdates, _ string, _ *dataplane.SyncOptions) (*dataplane.SyncResult, error) {
	return f.sync()
}

func (f *fakeRuntimeSyncer) Close() error {
	if f.closes != nil {
		f.closes.Add(1)
	}
	if f.onClose != nil {
		f.onClose()
	}
	return nil
}

func newTestBypass(newSyncer func(ctx context.Context, ep *dataplane.Endpoint) (runtimeSyncer, error)) *runtimeBypass {
	return &runtimeBypass{
		logger:      slog.Default(),
		newSyncer:   newSyncer,
		configCache: newConfigVersionCache(),
		clients:     make(map[endpointAuthority]runtimeSyncer),
	}
}

func openClientForTest(ctx context.Context, b *runtimeBypass, endpoint *dataplane.Endpoint) error {
	lease, ok := b.acquireAuthorityLease([]dataplane.Endpoint{*endpoint})
	if !ok {
		b.mu.Lock()
		closed := b.closed
		b.mu.Unlock()
		if closed {
			return errRuntimeBypassClosed
		}
		return errRuntimeBypassAuthorityChanged
	}
	_, err := b.clientForLease(ctx, endpoint, lease)
	return err
}

func threeEndpoints() []dataplane.Endpoint {
	return []dataplane.Endpoint{{URL: "http://a"}, {URL: "http://b"}, {URL: "http://c"}}
}

// depFor builds a scheduledDeployment carrying the given endpoints for an
// applyRuntimeRaw call. config/runtimeUpdates content is irrelevant to the fake.
func depFor(endpoints []dataplane.Endpoint) *scheduledDeployment {
	return &scheduledDeployment{config: "config", endpoints: endpoints}
}

func TestEndpointAuthorityIncludesClientAndPodIdentity(t *testing.T) {
	base := dataplane.Endpoint{
		URL:                  "http://a",
		Username:             "admin",
		Password:             "password",
		PodName:              "haproxy-0",
		PodUID:               "uid-1",
		DetectedMajorVersion: 3,
		DetectedMinorVersion: 2,
		DetectedFullVersion:  "v3.2.6",
	}
	tests := []struct {
		name   string
		mutate func(*dataplane.Endpoint)
	}{
		{name: "URL", mutate: func(endpoint *dataplane.Endpoint) { endpoint.URL = "http://b" }},
		{name: "username", mutate: func(endpoint *dataplane.Endpoint) { endpoint.Username = "operator" }},
		{name: "password", mutate: func(endpoint *dataplane.Endpoint) { endpoint.Password = "rotated" }},
		{name: "pod name", mutate: func(endpoint *dataplane.Endpoint) { endpoint.PodName = "haproxy-1" }},
		{name: "pod namespace", mutate: func(endpoint *dataplane.Endpoint) { endpoint.PodNamespace = "other" }},
		{name: "pod UID", mutate: func(endpoint *dataplane.Endpoint) { endpoint.PodUID = "uid-2" }},
		{name: "pod runtime", mutate: func(endpoint *dataplane.Endpoint) { endpoint.PodRuntimeID = "runtime-2" }},
		{name: "major version", mutate: func(endpoint *dataplane.Endpoint) { endpoint.DetectedMajorVersion = 4 }},
		{name: "minor version", mutate: func(endpoint *dataplane.Endpoint) { endpoint.DetectedMinorVersion = 3 }},
		{name: "full version", mutate: func(endpoint *dataplane.Endpoint) { endpoint.DetectedFullVersion = "v3.2.6-ee1" }},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			changed := base
			test.mutate(&changed)
			assert.NotEqual(t, endpointAuthorityOf(&base), endpointAuthorityOf(&changed))
		})
	}
}

// TestRuntimeBypass_AppliesPerEndpoint verifies one SyncRuntimeFast per endpoint,
// that the per-endpoint client is opened once (persistent), and that Close()
// shuts every cached client down.
func TestRuntimeBypass_AppliesPerEndpoint(t *testing.T) {
	var calls atomic.Int32
	var opens atomic.Int32
	var closes atomic.Int32

	b := newTestBypass(func(_ context.Context, _ *dataplane.Endpoint) (runtimeSyncer, error) {
		opens.Add(1)
		return &fakeRuntimeSyncer{
			closes: &closes,
			sync: func() (*dataplane.SyncResult, error) {
				calls.Add(1)
				return &dataplane.SyncResult{Success: true}, nil
			},
		}, nil
	})

	b.applyRuntimeRaw(context.Background(), depFor(threeEndpoints()), bypassPush{body: "config"})

	// applyRuntimeRaw is synchronous — every endpoint has applied by the time it
	// returns.
	assert.Equal(t, int32(3), calls.Load(), "each endpoint must get exactly one runtime apply")
	// Persistent clients: opened once per endpoint, NOT closed after each apply.
	assert.Equal(t, int32(3), opens.Load(), "one client opened per endpoint")
	assert.Equal(t, int32(0), closes.Load(), "clients are persistent — not closed per apply")

	// A second apply over the same endpoints reuses the cached clients.
	b.applyRuntimeRaw(context.Background(), depFor(threeEndpoints()), bypassPush{body: "config"})
	assert.Equal(t, int32(6), calls.Load())
	assert.Equal(t, int32(3), opens.Load(), "second apply reuses persistent clients — no reopen")

	b.Close()
	assert.Equal(t, int32(3), closes.Load(), "Close shuts down every cached client")
}

// TestRuntimeBypass_RestampOnlyOnAuthoritativeApply pins the version-header
// re-stamp gate: only the AUTHORITATIVE runtime-raw lane apply (partial=false,
// dispatched strictly outside any in-flight structural deploy) may set
// SyncOptions.RestampVersionHeader. A partial fast-track apply can race an
// in-flight structural reload, and a re-stamped header would let the next sync
// trust an empty diff over a `set server` the reload swallowed — so it must
// leave the pod's config headerless (the structural sync then force-reloads).
func TestRuntimeBypass_RestampOnlyOnAuthoritativeApply(t *testing.T) {
	for _, tc := range []struct {
		name        string
		partial     bool
		wantRestamp bool
	}{
		{name: "authoritative apply re-stamps", partial: false, wantRestamp: true},
		{name: "partial apply stays headerless", partial: true, wantRestamp: false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var gotRestamp atomic.Bool
			b := newTestBypass(func(_ context.Context, _ *dataplane.Endpoint) (runtimeSyncer, error) {
				return &optsRecordingSyncer{onSync: func(opts *dataplane.SyncOptions) {
					gotRestamp.Store(opts.RestampVersionHeader)
				}}, nil
			})

			b.applyRuntimeRaw(context.Background(), depFor([]dataplane.Endpoint{{URL: "http://a"}}), bypassPush{body: "config", partial: tc.partial})

			assert.Equal(t, tc.wantRestamp, gotRestamp.Load())
		})
	}
}

// optsRecordingSyncer records the SyncOptions each SyncRuntimeFast call receives.
type optsRecordingSyncer struct {
	onSync func(opts *dataplane.SyncOptions)
}

func (s *optsRecordingSyncer) SyncRuntimeFast(_ context.Context, _ *dataplane.RuntimeServerUpdates, _ string, opts *dataplane.SyncOptions) (*dataplane.SyncResult, error) {
	s.onSync(opts)
	return &dataplane.SyncResult{Success: true}, nil
}

func (s *optsRecordingSyncer) Close() error { return nil }

// TestRuntimeBypass_BlocksUntilAllEndpointsDone verifies applyRuntimeRaw does not
// return until every endpoint's apply has completed — the synchronous contract
// the deploy loop relies on to serialize the runtime-raw apply after the in-flight
// deploy's reload.
func TestRuntimeBypass_BlocksUntilAllEndpointsDone(t *testing.T) {
	var inProgress atomic.Int32
	var maxConcurrent atomic.Int32
	var completed atomic.Int32

	b := newTestBypass(func(_ context.Context, _ *dataplane.Endpoint) (runtimeSyncer, error) {
		return &fakeRuntimeSyncer{sync: func() (*dataplane.SyncResult, error) {
			n := inProgress.Add(1)
			for {
				m := maxConcurrent.Load()
				if n <= m || maxConcurrent.CompareAndSwap(m, n) {
					break
				}
			}
			time.Sleep(20 * time.Millisecond)
			inProgress.Add(-1)
			completed.Add(1)
			return &dataplane.SyncResult{Success: true}, nil
		}}, nil
	})

	b.applyRuntimeRaw(context.Background(), depFor(threeEndpoints()), bypassPush{body: "config"})

	// On return, all three applies have finished (the call blocked on them).
	assert.Equal(t, int32(3), completed.Load(), "applyRuntimeRaw must block until every endpoint completes")
	assert.Equal(t, int32(0), inProgress.Load(), "no apply may still be running after applyRuntimeRaw returns")
	assert.Equal(t, int32(3), maxConcurrent.Load(), "endpoints apply in parallel (one goroutine each)")
}

// TestRuntimeBypass_EvictsStaleClients verifies an apply whose endpoint set no
// longer contains a previously-seen endpoint closes that endpoint's client.
func TestRuntimeBypass_EvictsStaleClients(t *testing.T) {
	var closes atomic.Int32

	b := newTestBypass(func(_ context.Context, _ *dataplane.Endpoint) (runtimeSyncer, error) {
		return &fakeRuntimeSyncer{
			closes: &closes,
			sync:   func() (*dataplane.SyncResult, error) { return &dataplane.SyncResult{Success: true}, nil },
		}, nil
	})

	b.applyRuntimeRaw(context.Background(), depFor(threeEndpoints()), bypassPush{body: "config"})
	b.mu.Lock()
	require.Len(t, b.clients, 3, "three clients cached")
	b.mu.Unlock()

	// Next apply only mentions endpoint a — b and c are stale and must be closed.
	remaining := []dataplane.Endpoint{{URL: "http://a"}}
	b.replaceEndpointAuthorities(remaining)
	b.applyRuntimeRaw(context.Background(), depFor(remaining), bypassPush{body: "config"})
	assert.Equal(t, int32(2), closes.Load(), "the two absent endpoints' clients are closed")
	b.mu.Lock()
	endpointA := dataplane.Endpoint{URL: "http://a"}
	_, hasA := b.clients[endpointAuthorityOf(&endpointA)]
	n := len(b.clients)
	b.mu.Unlock()
	assert.True(t, hasA, "the surviving endpoint keeps its client")
	assert.Equal(t, 1, n)
}

func TestRuntimeBypass_ReplacesClientAfterCredentialRotation(t *testing.T) {
	var history []string

	b := newTestBypass(func(_ context.Context, ep *dataplane.Endpoint) (runtimeSyncer, error) {
		password := ep.Password
		history = append(history, "open:"+password)
		return &fakeRuntimeSyncer{
			sync: func() (*dataplane.SyncResult, error) {
				history = append(history, "sync:"+password)
				return &dataplane.SyncResult{Success: true}, nil
			},
			onClose: func() {
				history = append(history, "close:"+password)
			},
		}, nil
	})

	endpoint := dataplane.Endpoint{
		URL:                  "http://a",
		Username:             "admin",
		Password:             "old-password",
		PodName:              "haproxy-0",
		PodNamespace:         "haptic",
		PodUID:               "uid-1",
		DetectedMajorVersion: 3,
		DetectedMinorVersion: 2,
		DetectedFullVersion:  "3.2.1",
	}
	b.applyRuntimeRaw(context.Background(), depFor([]dataplane.Endpoint{endpoint}), bypassPush{body: "config"})

	endpoint.Password = "new-password"
	b.replaceEndpointAuthorities([]dataplane.Endpoint{endpoint})
	b.applyRuntimeRaw(context.Background(), depFor([]dataplane.Endpoint{endpoint}), bypassPush{body: "config"})
	b.applyRuntimeRaw(context.Background(), depFor([]dataplane.Endpoint{endpoint}), bypassPush{body: "config"})

	assert.Equal(t, []string{
		"open:old-password",
		"sync:old-password",
		"close:old-password",
		"open:new-password",
		"sync:new-password",
		"sync:new-password",
	}, history)
	b.mu.Lock()
	_, hasRotatedClient := b.clients[endpointAuthorityOf(&endpoint)]
	n := len(b.clients)
	b.mu.Unlock()
	assert.True(t, hasRotatedClient)
	assert.Equal(t, 1, n)
}

func TestRuntimeBypass_ReplacesClientAfterPodReplacement(t *testing.T) {
	var opens atomic.Int32
	var closes atomic.Int32
	b := newTestBypass(func(_ context.Context, _ *dataplane.Endpoint) (runtimeSyncer, error) {
		opens.Add(1)
		return &fakeRuntimeSyncer{
			closes: &closes,
			sync:   func() (*dataplane.SyncResult, error) { return &dataplane.SyncResult{Success: true}, nil },
		}, nil
	})

	endpoint := dataplane.Endpoint{URL: "http://a", Username: "admin", Password: "password", PodUID: "uid-1"}
	b.applyRuntimeRaw(context.Background(), depFor([]dataplane.Endpoint{endpoint}), bypassPush{body: "config"})
	endpoint.PodUID = "uid-2"
	b.replaceEndpointAuthorities([]dataplane.Endpoint{endpoint})
	b.applyRuntimeRaw(context.Background(), depFor([]dataplane.Endpoint{endpoint}), bypassPush{body: "config"})

	assert.Equal(t, int32(2), opens.Load())
	assert.Equal(t, int32(1), closes.Load())
}

func TestRuntimeBypass_CloseDiscardsClientOpeningInFlight(t *testing.T) {
	started := make(chan struct{})
	release := make(chan struct{})
	var closes atomic.Int32
	b := newTestBypass(func(_ context.Context, _ *dataplane.Endpoint) (runtimeSyncer, error) {
		close(started)
		<-release
		return &fakeRuntimeSyncer{
			closes: &closes,
			sync:   func() (*dataplane.SyncResult, error) { return &dataplane.SyncResult{Success: true}, nil },
		}, nil
	})

	result := make(chan error, 1)
	go func() {
		result <- openClientForTest(t.Context(), b, &dataplane.Endpoint{URL: "http://a"})
	}()
	select {
	case <-started:
	case <-time.After(testutil.LongTimeout):
		t.Fatal("client construction did not start")
	}
	b.Close()
	close(release)

	select {
	case err := <-result:
		assert.ErrorIs(t, err, errRuntimeBypassClosed)
	case <-time.After(testutil.LongTimeout):
		t.Fatal("client construction did not finish")
	}
	assert.Equal(t, int32(1), closes.Load())
	b.mu.Lock()
	assert.Empty(t, b.clients)
	b.mu.Unlock()
}

func TestRuntimeBypass_CloseFencesBlockedSyncResult(t *testing.T) {
	bus := testutil.NewTestBus()
	appliedCh := bus.SubscribeTypes("blocked-applied", 10, events.EventTypeConfigAppliedToPod)
	publishCh := bus.SubscribeTypes("blocked-publish", 10, events.EventTypeDeployedConfigPublishRequest)
	bus.Start()

	started := make(chan struct{})
	release := make(chan struct{})
	done := make(chan struct{})
	var metrics atomic.Int32
	b := newTestBypass(func(_ context.Context, _ *dataplane.Endpoint) (runtimeSyncer, error) {
		return &fakeRuntimeSyncer{sync: func() (*dataplane.SyncResult, error) {
			close(started)
			<-release
			return &dataplane.SyncResult{Success: true, ActivatedConfigChecksum: "retired-proof"}, nil
		}}, nil
	})
	b.eventBus = bus
	b.recordFastPath = func(_ int, _ bool) { metrics.Add(1) }
	endpoint := dataplane.Endpoint{
		URL: "http://a", PodName: "haproxy-0", PodNamespace: "haptic", PodUID: "uid-1",
	}
	commitTestObservation(t, b.configCache, &endpoint, 2, newTestConfig(), "old-proof", "content", "old-proof")
	dep := &scheduledDeployment{
		config:                 "config",
		contentChecksum:        "checksum",
		lane:                   laneRuntimeRaw,
		runtimeConfigName:      "cfg",
		runtimeConfigNamespace: "haptic",
		endpoints:              []dataplane.Endpoint{endpoint},
	}

	go func() {
		defer close(done)
		b.applyRuntimeRaw(context.Background(), dep, bypassPush{body: dep.config})
	}()
	select {
	case <-started:
	case <-time.After(testutil.LongTimeout):
		t.Fatal("runtime sync did not block")
	}
	b.Close()
	close(release)
	select {
	case <-done:
	case <-time.After(testutil.LongTimeout):
		t.Fatal("runtime apply did not return")
	}

	assert.Zero(t, metrics.Load())
	assert.Nil(t, b.configCache.snapshot(&endpoint).parsedConfig)
	assert.Empty(t, b.configCache.snapshot(&endpoint).activatedChecksum)
	testutil.AssertNoEvent[*events.ConfigAppliedToPodEvent](t, appliedCh, testutil.NoEventTimeout)
	testutil.AssertNoEvent[*events.DeployedConfigPublishRequest](t, publishCh, testutil.NoEventTimeout)
}

func TestRuntimeBypass_InvalidatesCacheBeforePodWrite(t *testing.T) {
	started := make(chan struct{})
	release := make(chan struct{})
	b := newTestBypass(func(_ context.Context, _ *dataplane.Endpoint) (runtimeSyncer, error) {
		return &fakeRuntimeSyncer{sync: func() (*dataplane.SyncResult, error) {
			close(started)
			<-release
			return &dataplane.SyncResult{Success: true, ActivatedConfigChecksum: "runtime-proof"}, nil
		}}, nil
	})
	endpoint := dataplane.Endpoint{URL: "http://a"}
	commitTestObservation(t, b.configCache, &endpoint, 2, newTestConfig(), "old-proof", "content", "old-proof")
	done := make(chan struct{})
	go func() {
		defer close(done)
		b.applyRuntimeRaw(context.Background(), depFor([]dataplane.Endpoint{endpoint}), bypassPush{body: "config"})
	}()

	select {
	case <-started:
	case <-time.After(testutil.LongTimeout):
		t.Fatal("runtime sync did not start")
	}
	duringWrite := b.configCache.snapshot(&endpoint)
	assert.Nil(t, duringWrite.parsedConfig)
	assert.Empty(t, duringWrite.activatedChecksum)

	close(release)
	select {
	case <-done:
	case <-time.After(testutil.LongTimeout):
		t.Fatal("runtime sync did not finish")
	}
	afterWrite := b.configCache.snapshot(&endpoint)
	assert.Nil(t, afterWrite.parsedConfig)
	assert.Equal(t, "runtime-proof", afterWrite.activatedChecksum)
}

func TestRuntimeBypass_StaleResultClosesMutationWithoutProof(t *testing.T) {
	tests := []struct {
		name  string
		stale func(context.CancelFunc, *runtimeBypass, dataplane.Endpoint)
	}{
		{
			name: "cancelled context",
			stale: func(cancel context.CancelFunc, _ *runtimeBypass, _ dataplane.Endpoint) {
				cancel()
			},
		},
		{
			name: "expired authority lease",
			stale: func(_ context.CancelFunc, bypass *runtimeBypass, endpoint dataplane.Endpoint) {
				bypass.replaceEndpointAuthorities([]dataplane.Endpoint{endpoint, {URL: "http://b"}})
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			started := make(chan struct{})
			release := make(chan struct{})
			b := newTestBypass(func(_ context.Context, _ *dataplane.Endpoint) (runtimeSyncer, error) {
				return &fakeRuntimeSyncer{sync: func() (*dataplane.SyncResult, error) {
					close(started)
					<-release
					return &dataplane.SyncResult{Success: true, ActivatedConfigChecksum: "stale-proof"}, nil
				}}, nil
			})
			endpoint := dataplane.Endpoint{URL: "http://a"}
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			done := make(chan struct{})
			go func() {
				defer close(done)
				b.applyRuntimeRaw(ctx, depFor([]dataplane.Endpoint{endpoint}), bypassPush{body: "config"})
			}()

			select {
			case <-started:
			case <-time.After(testutil.LongTimeout):
				t.Fatal("runtime sync did not start")
			}
			test.stale(cancel, b, endpoint)
			close(release)
			select {
			case <-done:
			case <-time.After(testutil.LongTimeout):
				t.Fatal("runtime sync did not finish")
			}

			snapshot := b.configCache.snapshot(&endpoint)
			assert.Nil(t, snapshot.parsedConfig)
			assert.Empty(t, snapshot.activatedChecksum)
		})
	}
}

func TestRuntimeBypass_BeginLeadershipTermReopensCache(t *testing.T) {
	b := newTestBypass(func(_ context.Context, _ *dataplane.Endpoint) (runtimeSyncer, error) {
		return &fakeRuntimeSyncer{
			sync: func() (*dataplane.SyncResult, error) { return &dataplane.SyncResult{Success: true}, nil },
		}, nil
	})
	b.Close()

	err := openClientForTest(t.Context(), b, &dataplane.Endpoint{URL: "http://a"})
	assert.ErrorIs(t, err, errRuntimeBypassClosed)

	b.beginLeadershipTerm()
	err = openClientForTest(t.Context(), b, &dataplane.Endpoint{URL: "http://a"})
	require.NoError(t, err)
}

// TestRuntimeBypass_SwallowsSyncError verifies an apply error on one endpoint
// is swallowed (best-effort) and does not stop the others.
func TestRuntimeBypass_SwallowsSyncError(t *testing.T) {
	var calls atomic.Int32
	var failures atomic.Int32

	b := newTestBypass(func(_ context.Context, ep *dataplane.Endpoint) (runtimeSyncer, error) {
		url := ep.URL
		return &fakeRuntimeSyncer{sync: func() (*dataplane.SyncResult, error) {
			calls.Add(1)
			if url == "http://b" {
				return nil, errors.New("boom")
			}
			return &dataplane.SyncResult{Success: true}, nil
		}}, nil
	})
	b.recordFastPath = func(_ int, failed bool) {
		if failed {
			failures.Add(1)
		}
	}

	applied := b.applyRuntimeRaw(context.Background(), depFor(threeEndpoints()), bypassPush{body: "config"})

	assert.False(t, applied)
	assert.Equal(t, int32(3), calls.Load(),
		"the failing endpoint must not prevent the others from applying")
	assert.Equal(t, int32(1), failures.Load())
}

func TestRuntimeBypass_RejectsUnsuccessfulResult(t *testing.T) {
	var failures atomic.Int32
	b := newTestBypass(func(_ context.Context, _ *dataplane.Endpoint) (runtimeSyncer, error) {
		return &fakeRuntimeSyncer{sync: func() (*dataplane.SyncResult, error) {
			return &dataplane.SyncResult{}, nil
		}}, nil
	})
	b.recordFastPath = func(_ int, failed bool) {
		if failed {
			failures.Add(1)
		}
	}

	assert.False(t, b.applyRuntimeRaw(context.Background(), depFor(oneEndpoint()), bypassPush{body: "config"}))
	assert.Equal(t, int32(1), failures.Load())
}

// TestRuntimeBypass_SwallowsClientOpenError verifies a client-open failure is
// swallowed and the remaining endpoints still apply.
func TestRuntimeBypass_SwallowsClientOpenError(t *testing.T) {
	var calls atomic.Int32
	var failures atomic.Int32

	b := newTestBypass(func(_ context.Context, ep *dataplane.Endpoint) (runtimeSyncer, error) {
		if ep.URL == "http://a" {
			return nil, errors.New("dial refused")
		}
		return &fakeRuntimeSyncer{sync: func() (*dataplane.SyncResult, error) {
			calls.Add(1)
			return &dataplane.SyncResult{Success: true}, nil
		}}, nil
	})
	b.recordFastPath = func(_ int, failed bool) {
		if failed {
			failures.Add(1)
		}
	}

	applied := b.applyRuntimeRaw(context.Background(), depFor(threeEndpoints()), bypassPush{body: "config"})

	assert.False(t, applied)
	assert.Equal(t, int32(2), calls.Load(),
		"two endpoints apply; the one whose client failed to open is skipped")
	assert.Equal(t, int32(1), failures.Load())
}

// TestRuntimeBypass_RecoversPanic verifies a panic inside one apply is recovered
// (does not crash the process) and the other endpoints still apply.
func TestRuntimeBypass_RecoversPanic(t *testing.T) {
	var calls atomic.Int32
	var failures atomic.Int32

	b := newTestBypass(func(_ context.Context, ep *dataplane.Endpoint) (runtimeSyncer, error) {
		url := ep.URL
		return &fakeRuntimeSyncer{sync: func() (*dataplane.SyncResult, error) {
			if url == "http://b" {
				panic("kaboom")
			}
			calls.Add(1)
			return &dataplane.SyncResult{Success: true}, nil
		}}, nil
	})
	b.recordFastPath = func(_ int, failed bool) {
		if failed {
			failures.Add(1)
		}
	}

	// Must not panic out of applyRuntimeRaw (the per-endpoint recover catches it).
	var applied bool
	require.NotPanics(t, func() {
		applied = b.applyRuntimeRaw(context.Background(), depFor(threeEndpoints()), bypassPush{body: "config"})
	})
	assert.False(t, applied)
	assert.Equal(t, int32(2), calls.Load(),
		"the panicking endpoint is recovered; the others apply")
	assert.Equal(t, int32(1), failures.Load())
}

// TestRuntimeBypass_CancelledParentStopsSpawning verifies a cancelled parent
// context halts further spawning.
func TestRuntimeBypass_CancelledParentStopsSpawning(t *testing.T) {
	var calls atomic.Int32
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	b := newTestBypass(func(_ context.Context, _ *dataplane.Endpoint) (runtimeSyncer, error) {
		return &fakeRuntimeSyncer{sync: func() (*dataplane.SyncResult, error) {
			calls.Add(1)
			return &dataplane.SyncResult{Success: true}, nil
		}}, nil
	})

	b.applyRuntimeRaw(ctx, depFor(threeEndpoints()), bypassPush{body: "config"})

	assert.Equal(t, int32(0), calls.Load(), "a cancelled parent context must stop spawning applies")
}

// TestRuntimeBypass_PublishesConfigAppliedForRuntimeLane verifies that a pure
// runtime-raw lane apply — which reloads nothing, so the apply IS the complete
// deploy — advances each pod's status by publishing ConfigAppliedToPodEvent
// carrying the deploy's content checksum and pod identity.
func TestRuntimeBypass_PublishesConfigAppliedForRuntimeLane(t *testing.T) {
	bus := testutil.NewTestBus()
	appliedCh := bus.SubscribeTypes("test-applied", 50, events.EventTypeConfigAppliedToPod)
	bus.Start()

	b := newTestBypass(func(_ context.Context, _ *dataplane.Endpoint) (runtimeSyncer, error) {
		return &fakeRuntimeSyncer{sync: func() (*dataplane.SyncResult, error) {
			return &dataplane.SyncResult{Success: true}, nil
		}}, nil
	})
	b.eventBus = bus

	dep := &scheduledDeployment{
		config:                 "config",
		contentChecksum:        "checksum-xyz",
		lane:                   laneRuntimeRaw,
		runtimeConfigName:      "haptic-config-haproxycfg",
		runtimeConfigNamespace: "haptic",
		endpoints: []dataplane.Endpoint{
			{URL: "http://a", PodName: "pod-a", PodNamespace: "haptic", PodUID: "uid-a"},
			{URL: "http://b", PodName: "pod-b", PodNamespace: "haptic", PodUID: "uid-b"},
		},
	}
	b.applyRuntimeRaw(context.Background(), dep, bypassPush{body: dep.config})

	got := map[string]string{} // podName -> reported checksum
	expectedUID := map[string]string{"pod-a": "uid-a", "pod-b": "uid-b"}
	timeout := time.After(2 * time.Second)
	for len(got) < 2 {
		select {
		case ev := <-appliedCh:
			cae, ok := ev.(*events.ConfigAppliedToPodEvent)
			require.True(t, ok, "expected *ConfigAppliedToPodEvent, got %T", ev)
			assert.Equal(t, "haptic-config-haproxycfg", cae.RuntimeConfigName)
			assert.Equal(t, "haptic", cae.RuntimeConfigNamespace)
			assert.Equal(t, expectedUID[cae.PodName], cae.PodUID)
			assert.False(t, cae.IsDriftCheck, "a real runtime apply is not a drift check")
			got[cae.PodName] = cae.Checksum
		case <-timeout:
			t.Fatalf("timed out waiting for ConfigAppliedToPodEvent; got %d of 2", len(got))
		}
	}
	assert.Equal(t, "checksum-xyz", got["pod-a"])
	assert.Equal(t, "checksum-xyz", got["pod-b"])
}

// TestRuntimeBypass_NoConfigAppliedForStructuralPreInterval verifies the
// pre-interval apply of a STRUCTURAL render's runtime subset does NOT publish
// ConfigAppliedToPodEvent: its reload is still pending, so advancing the pod's
// status would falsely claim the full config is live (the structural deploy
// publishes the truthful status after its reload). runtimeConfigName is set to
// prove it is the LANE that gates the publish, not a missing HAProxyCfg identity.
func TestRuntimeBypass_NoConfigAppliedForStructuralPreInterval(t *testing.T) {
	bus := testutil.NewTestBus()
	appliedCh := bus.SubscribeTypes("test-applied-structural", 50, events.EventTypeConfigAppliedToPod)
	bus.Start()

	b := newTestBypass(func(_ context.Context, _ *dataplane.Endpoint) (runtimeSyncer, error) {
		return &fakeRuntimeSyncer{sync: func() (*dataplane.SyncResult, error) {
			return &dataplane.SyncResult{Success: true}, nil
		}}, nil
	})
	b.eventBus = bus

	dep := &scheduledDeployment{
		config:                 "config",
		contentChecksum:        "checksum-xyz",
		lane:                   laneStructural, // pre-interval partial apply — reload pending
		runtimeConfigName:      "haptic-config-haproxycfg",
		runtimeConfigNamespace: "haptic",
		endpoints:              []dataplane.Endpoint{{URL: "http://a", PodName: "pod-a", PodNamespace: "haptic"}},
	}
	b.applyRuntimeRaw(context.Background(), dep, bypassPush{body: dep.config})

	select {
	case ev := <-appliedCh:
		t.Fatalf("structural pre-interval apply must NOT publish ConfigAppliedToPodEvent, got %T %+v", ev, ev)
	case <-time.After(300 * time.Millisecond):
		// expected: no event — the structural deploy publishes after its reload
	}
}

// TestRuntimeBypass_PublishesDeployedConfigForRuntimeLane verifies a pure
// runtime-raw lane apply emits exactly one DeployedConfigPublishRequest (once
// per apply, not per endpoint) carrying the deployed bytes + checksum, so the
// deployed checksum becomes an observable published spec.Checksum.
func TestRuntimeBypass_PublishesDeployedConfigForRuntimeLane(t *testing.T) {
	bus := testutil.NewTestBus()
	reqCh := bus.SubscribeTypes("test-deployed-publish", 50, events.EventTypeDeployedConfigPublishRequest)
	bus.Start()

	b := newTestBypass(func(_ context.Context, _ *dataplane.Endpoint) (runtimeSyncer, error) {
		return &fakeRuntimeSyncer{sync: func() (*dataplane.SyncResult, error) {
			return &dataplane.SyncResult{Success: true}, nil
		}}, nil
	})
	b.eventBus = bus

	dep := &scheduledDeployment{
		config:                 "deployed-config-body",
		contentChecksum:        "checksum-xyz",
		lane:                   laneRuntimeRaw,
		runtimeConfigName:      "haptic-config-haproxycfg",
		runtimeConfigNamespace: "haptic",
		endpoints: []dataplane.Endpoint{
			{URL: "http://a", PodName: "pod-a", PodNamespace: "haptic"},
			{URL: "http://b", PodName: "pod-b", PodNamespace: "haptic"},
		},
	}
	b.applyRuntimeRaw(context.Background(), dep, bypassPush{body: dep.config})

	select {
	case ev := <-reqCh:
		req, ok := ev.(*events.DeployedConfigPublishRequest)
		require.True(t, ok, "expected *DeployedConfigPublishRequest, got %T", ev)
		assert.Equal(t, "checksum-xyz", req.ContentChecksum)
		assert.Equal(t, "deployed-config-body", req.Config)
		assert.Equal(t, "haptic-config-haproxycfg", req.RuntimeConfigName)
	case <-time.After(2 * time.Second):
		t.Fatal("runtime-raw lane must emit a DeployedConfigPublishRequest on success")
	}
	// Exactly once — not per endpoint.
	select {
	case ev := <-reqCh:
		t.Fatalf("expected exactly one DeployedConfigPublishRequest per apply, got a second: %+v", ev)
	case <-time.After(200 * time.Millisecond):
	}
}

// TestRuntimeBypass_StructuralPreInterval_NoDeployedConfigPublish verifies the
// pre-interval partial apply (laneStructural) does NOT emit a
// DeployedConfigPublishRequest — its gated structural deploy publishes after the
// reload.
func TestRuntimeBypass_StructuralPreInterval_NoDeployedConfigPublish(t *testing.T) {
	bus := testutil.NewTestBus()
	reqCh := bus.SubscribeTypes("test-deployed-publish-structural", 50, events.EventTypeDeployedConfigPublishRequest)
	bus.Start()

	b := newTestBypass(func(_ context.Context, _ *dataplane.Endpoint) (runtimeSyncer, error) {
		return &fakeRuntimeSyncer{sync: func() (*dataplane.SyncResult, error) {
			return &dataplane.SyncResult{Success: true}, nil
		}}, nil
	})
	b.eventBus = bus

	dep := &scheduledDeployment{
		config:                 "config",
		contentChecksum:        "checksum-xyz",
		lane:                   laneStructural,
		runtimeConfigName:      "haptic-config-haproxycfg",
		runtimeConfigNamespace: "haptic",
		endpoints:              []dataplane.Endpoint{{URL: "http://a", PodName: "pod-a", PodNamespace: "haptic"}},
	}
	b.applyRuntimeRaw(context.Background(), dep, bypassPush{body: dep.config})

	select {
	case ev := <-reqCh:
		t.Fatalf("structural pre-interval apply must NOT emit DeployedConfigPublishRequest, got %+v", ev)
	case <-time.After(300 * time.Millisecond):
	}
}

// TestRuntimeBypass_PartialSuppressesDeployPublishes verifies the in-flight partial
// apply (partial=true) of a laneRuntimeRaw render — applied while a SEPARATE
// structural deploy is mid-reload, which still owns completion + CR/status — keeps
// only the fast-path metric and suppresses BOTH deploy-owning
// publishes (ConfigAppliedToPodEvent, DeployedConfigPublishRequest). The dep is
// laneRuntimeRaw with a resolved HAProxyCfg identity — exactly the shape that WOULD
// publish on a non-partial apply — proving it is the `partial` flag, not the lane
// or a missing identity, that gates the publishes.
func TestRuntimeBypass_PartialSuppressesDeployPublishes(t *testing.T) {
	bus := testutil.NewTestBus()
	appliedCh := bus.SubscribeTypes("test-partial-applied", 50, events.EventTypeConfigAppliedToPod)
	publishCh := bus.SubscribeTypes("test-partial-publish", 50, events.EventTypeDeployedConfigPublishRequest)
	bus.Start()

	var fastPathFires int

	b := newTestBypass(func(_ context.Context, _ *dataplane.Endpoint) (runtimeSyncer, error) {
		return &fakeRuntimeSyncer{sync: func() (*dataplane.SyncResult, error) {
			return &dataplane.SyncResult{Success: true, AppliedOperations: []dataplane.AppliedOperation{{}}}, nil
		}}, nil
	})
	b.eventBus = bus
	b.recordFastPath = func(int, bool) { fastPathFires++ }

	dep := &scheduledDeployment{
		config:                 "deployed-config-body",
		contentChecksum:        "checksum-xyz",
		lane:                   laneRuntimeRaw, // would publish on a non-partial apply
		runtimeConfigName:      "haptic-config-haproxycfg",
		runtimeConfigNamespace: "haptic",
		endpoints:              []dataplane.Endpoint{{URL: "http://a", PodName: "pod-a", PodNamespace: "haptic"}},
	}
	b.applyRuntimeRaw(context.Background(), dep, bypassPush{body: dep.config, partial: true})

	// The metric still fires (fire-vs-apply accounting stays correct).
	require.Equal(t, 1, fastPathFires,
		"a partial apply must still be counted as a fast-path fire")

	// Neither deploy-owning publish fires — the in-flight structural deploy owns them.
	select {
	case ev := <-appliedCh:
		t.Fatalf("partial apply must NOT publish ConfigAppliedToPodEvent, got %+v", ev)
	case <-time.After(300 * time.Millisecond):
	}
	select {
	case ev := <-publishCh:
		t.Fatalf("partial apply must NOT emit DeployedConfigPublishRequest, got %+v", ev)
	case <-time.After(300 * time.Millisecond):
	}
}
