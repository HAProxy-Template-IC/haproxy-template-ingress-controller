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
	sync   func() (*dataplane.SyncResult, error)
	closes *atomic.Int32 // optional shared close counter
}

func (f *fakeRuntimeSyncer) SyncRuntimeFast(_ context.Context, _ *dataplane.RuntimeServerUpdates, _ string, _ *dataplane.SyncOptions) (*dataplane.SyncResult, error) {
	return f.sync()
}

func (f *fakeRuntimeSyncer) Close() error {
	if f.closes != nil {
		f.closes.Add(1)
	}
	return nil
}

func newTestBypass(newSyncer func(ctx context.Context, ep *dataplane.Endpoint) (runtimeSyncer, error)) *runtimeBypass {
	return &runtimeBypass{
		logger:    slog.Default(),
		newSyncer: newSyncer,
		clients:   make(map[string]runtimeSyncer),
	}
}

func threeEndpoints() []dataplane.Endpoint {
	return []dataplane.Endpoint{{URL: "http://a"}, {URL: "http://b"}, {URL: "http://c"}}
}

// depFor builds a scheduledDeployment carrying the given endpoints for an
// applyRuntimeRaw call. config/runtimeUpdates content is irrelevant to the fake.
func depFor(endpoints []dataplane.Endpoint) *scheduledDeployment {
	return &scheduledDeployment{config: "config", endpoints: endpoints}
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

	b.applyRuntimeRaw(context.Background(), depFor(threeEndpoints()), false)

	// applyRuntimeRaw is synchronous — every endpoint has applied by the time it
	// returns.
	assert.Equal(t, int32(3), calls.Load(), "each endpoint must get exactly one runtime apply")
	// Persistent clients: opened once per endpoint, NOT closed after each apply.
	assert.Equal(t, int32(3), opens.Load(), "one client opened per endpoint")
	assert.Equal(t, int32(0), closes.Load(), "clients are persistent — not closed per apply")

	// A second apply over the same endpoints reuses the cached clients.
	b.applyRuntimeRaw(context.Background(), depFor(threeEndpoints()), false)
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

			b.applyRuntimeRaw(context.Background(), depFor([]dataplane.Endpoint{{URL: "http://a"}}), tc.partial)

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

	b.applyRuntimeRaw(context.Background(), depFor(threeEndpoints()), false)

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

	b.applyRuntimeRaw(context.Background(), depFor(threeEndpoints()), false)
	b.mu.Lock()
	require.Len(t, b.clients, 3, "three clients cached")
	b.mu.Unlock()

	// Next apply only mentions endpoint a — b and c are stale and must be closed.
	b.applyRuntimeRaw(context.Background(), depFor([]dataplane.Endpoint{{URL: "http://a"}}), false)
	assert.Equal(t, int32(2), closes.Load(), "the two absent endpoints' clients are closed")
	b.mu.Lock()
	_, hasA := b.clients["http://a"]
	n := len(b.clients)
	b.mu.Unlock()
	assert.True(t, hasA, "the surviving endpoint keeps its client")
	assert.Equal(t, 1, n)
}

// TestRuntimeBypass_SwallowsSyncError verifies an apply error on one endpoint
// is swallowed (best-effort) and does not stop the others.
func TestRuntimeBypass_SwallowsSyncError(t *testing.T) {
	var calls atomic.Int32

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

	b.applyRuntimeRaw(context.Background(), depFor(threeEndpoints()), false)

	assert.Equal(t, int32(3), calls.Load(),
		"the failing endpoint must not prevent the others from applying")
}

// TestRuntimeBypass_SwallowsClientOpenError verifies a client-open failure is
// swallowed and the remaining endpoints still apply.
func TestRuntimeBypass_SwallowsClientOpenError(t *testing.T) {
	var calls atomic.Int32

	b := newTestBypass(func(_ context.Context, ep *dataplane.Endpoint) (runtimeSyncer, error) {
		if ep.URL == "http://a" {
			return nil, errors.New("dial refused")
		}
		return &fakeRuntimeSyncer{sync: func() (*dataplane.SyncResult, error) {
			calls.Add(1)
			return &dataplane.SyncResult{Success: true}, nil
		}}, nil
	})

	b.applyRuntimeRaw(context.Background(), depFor(threeEndpoints()), false)

	assert.Equal(t, int32(2), calls.Load(),
		"two endpoints apply; the one whose client failed to open is skipped")
}

// TestRuntimeBypass_RecoversPanic verifies a panic inside one apply is recovered
// (does not crash the process) and the other endpoints still apply.
func TestRuntimeBypass_RecoversPanic(t *testing.T) {
	var calls atomic.Int32

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

	// Must not panic out of applyRuntimeRaw (the per-endpoint recover catches it).
	require.NotPanics(t, func() {
		b.applyRuntimeRaw(context.Background(), depFor(threeEndpoints()), false)
	})
	assert.Equal(t, int32(2), calls.Load(),
		"the panicking endpoint is recovered; the others apply")
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

	b.applyRuntimeRaw(ctx, depFor(threeEndpoints()), false)

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
			{URL: "http://a", PodName: "pod-a", PodNamespace: "haptic"},
			{URL: "http://b", PodName: "pod-b", PodNamespace: "haptic"},
		},
	}
	b.applyRuntimeRaw(context.Background(), dep, false)

	got := map[string]string{} // podName -> reported checksum
	timeout := time.After(2 * time.Second)
	for len(got) < 2 {
		select {
		case ev := <-appliedCh:
			cae, ok := ev.(*events.ConfigAppliedToPodEvent)
			require.True(t, ok, "expected *ConfigAppliedToPodEvent, got %T", ev)
			assert.Equal(t, "haptic-config-haproxycfg", cae.RuntimeConfigName)
			assert.Equal(t, "haptic", cae.RuntimeConfigNamespace)
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
	b.applyRuntimeRaw(context.Background(), dep, false)

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
	b.applyRuntimeRaw(context.Background(), dep, false)

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
	b.applyRuntimeRaw(context.Background(), dep, false)

	select {
	case ev := <-reqCh:
		t.Fatalf("structural pre-interval apply must NOT emit DeployedConfigPublishRequest, got %+v", ev)
	case <-time.After(300 * time.Millisecond):
	}
}

// TestRuntimeBypass_PartialSuppressesDeployPublishes verifies the in-flight partial
// apply (partial=true) of a laneRuntimeRaw render — applied while a SEPARATE
// structural deploy is mid-reload, which still owns completion + CR/status — keeps
// only the RuntimeFastPathResultEvent metric and suppresses BOTH deploy-owning
// publishes (ConfigAppliedToPodEvent, DeployedConfigPublishRequest). The dep is
// laneRuntimeRaw with a resolved HAProxyCfg identity — exactly the shape that WOULD
// publish on a non-partial apply — proving it is the `partial` flag, not the lane
// or a missing identity, that gates the publishes.
func TestRuntimeBypass_PartialSuppressesDeployPublishes(t *testing.T) {
	bus := testutil.NewTestBus()
	appliedCh := bus.SubscribeTypes("test-partial-applied", 50, events.EventTypeConfigAppliedToPod)
	publishCh := bus.SubscribeTypes("test-partial-publish", 50, events.EventTypeDeployedConfigPublishRequest)
	metricCh := bus.SubscribeTypes("test-partial-metric", 50, events.EventTypeRuntimeFastPathResult)
	bus.Start()

	b := newTestBypass(func(_ context.Context, _ *dataplane.Endpoint) (runtimeSyncer, error) {
		return &fakeRuntimeSyncer{sync: func() (*dataplane.SyncResult, error) {
			return &dataplane.SyncResult{Success: true, AppliedOperations: []dataplane.AppliedOperation{{}}}, nil
		}}, nil
	})
	b.eventBus = bus

	dep := &scheduledDeployment{
		config:                 "deployed-config-body",
		contentChecksum:        "checksum-xyz",
		lane:                   laneRuntimeRaw, // would publish on a non-partial apply
		runtimeConfigName:      "haptic-config-haproxycfg",
		runtimeConfigNamespace: "haptic",
		endpoints:              []dataplane.Endpoint{{URL: "http://a", PodName: "pod-a", PodNamespace: "haptic"}},
	}
	b.applyRuntimeRaw(context.Background(), dep, true) // partial

	// The metric event still fires (fire-vs-apply accounting stays correct).
	select {
	case ev := <-metricCh:
		_, ok := ev.(*events.RuntimeFastPathResultEvent)
		require.True(t, ok, "expected *RuntimeFastPathResultEvent, got %T", ev)
	case <-time.After(2 * time.Second):
		t.Fatal("partial apply must still emit RuntimeFastPathResultEvent")
	}

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
