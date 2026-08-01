// Copyright 2025 Philipp Hossner
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0

package deployer

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"gitlab.com/haproxy-haptic/haptic/pkg/dataplane"
	"gitlab.com/haproxy-haptic/haptic/pkg/dataplane/parser/parserconfig"
)

// The activation proof and the version cache have different lifetimes, so
// updating one must not silently drop the other.
//
// A dropped proof is not a cache miss that costs a fetch — it makes the next
// sync force a reload against a config it just verified, on every sync.
func TestConfigVersionCache_ActivationProofOutlivesAVersionUpdate(t *testing.T) {
	c := newConfigVersionCache()
	const ep = "http://10.0.0.1:5555/v3"

	c.setActivated(ep, "proof-1")
	require.Equal(t, "proof-1", c.activated(ep))

	// A normal post-sync version-cache update must leave the proof alone.
	c.set(ep, 7, &parserconfig.StructuredConfig{}, "content-abc")
	assert.Equal(t, "proof-1", c.activated(ep),
		"a version-cache update must not drop the activation proof")

	version, _, checksum := c.get(ep)
	assert.Equal(t, int64(7), version)
	assert.Equal(t, "content-abc", checksum)
}

// Clearing the proof is the half of the fix that matters on the error path: a
// skip_version push writes its body to disk even when the runtime actions fail,
// so a failed apply leaves content on disk that no worker ever loaded. Keeping a
// stale proof would let the next sync short-circuit an empty diff over that
// parked content — the #112 stall.
func TestConfigVersionCache_EmptyProofClearsActivation(t *testing.T) {
	c := newConfigVersionCache()
	const ep = "http://10.0.0.1:5555/v3"

	c.setActivated(ep, "proof-1")
	c.setActivated(ep, "")
	assert.Empty(t, c.activated(ep), "an empty proof must clear, not be ignored")

	// Clearing an endpoint that was never recorded must not fabricate an entry.
	c.setActivated("http://10.0.0.2:5555/v3", "")
	assert.Empty(t, c.activated("http://10.0.0.2:5555/v3"))
}

// invalidate() drops the proof with the rest of the entry. After a failed sync
// nothing about the pod's running state is provable — the push may have reached
// disk regardless of the error.
func TestConfigVersionCache_InvalidateDropsActivation(t *testing.T) {
	c := newConfigVersionCache()
	const ep = "http://10.0.0.1:5555/v3"

	c.set(ep, 7, &parserconfig.StructuredConfig{}, "content-abc")
	c.setActivated(ep, "proof-1")

	c.invalidate(ep)
	assert.Empty(t, c.activated(ep), "a failed sync must leave nothing provable behind")
}

// An unknown endpoint has no proof, which the orchestrator reads as "force a
// reload before trusting an empty diff" — the safe default.
func TestConfigVersionCache_UnknownEndpointHasNoProof(t *testing.T) {
	c := newConfigVersionCache()
	assert.Empty(t, c.activated("http://nobody:5555/v3"))
}

// A failed bypass apply must CLEAR the endpoint's activation proof.
//
// This is #112 item 3, and the reason it matters is counter-intuitive: the push
// failing does not mean nothing was written. The dataplane writes a
// skip_version body to disk even when the runtime actions it carries return
// 500, so a failed apply can leave structural content on disk that no worker
// ever loaded. If the proof survived that, the next sync would diff
// desired-vs-disk to empty, short-circuit on the stale proof, and report success
// over parked content — the exact stall the provenance guard exists to close.
func TestRuntimeBypass_FailedApplyClearsActivationProof(t *testing.T) {
	recorded := map[string]string{}
	b := newTestBypass(func(_ context.Context, _ *dataplane.Endpoint) (runtimeSyncer, error) {
		return &fakeRuntimeSyncer{sync: func() (*dataplane.SyncResult, error) {
			return nil, errors.New("cannot execute SetServerState: connection refused")
		}}, nil
	})
	b.recordActivation = func(endpointURL, proof string) { recorded[endpointURL] = proof }

	b.applyRuntimeRaw(context.Background(), depFor([]dataplane.Endpoint{{URL: "http://a"}}),
		bypassPush{body: "config"})

	proof, ok := recorded["http://a"]
	require.True(t, ok, "a failed apply must report the endpoint, not stay silent")
	assert.Empty(t, proof,
		"a failed apply must clear the proof — its body may still have reached disk")
}

// A successful apply records what it proved, so the reload-free path stays
// reload-free: the bypass legitimately changes the on-disk bytes, and without a
// fresh proof the next sync would force a reload against them.
func TestRuntimeBypass_SuccessfulApplyRecordsActivationProof(t *testing.T) {
	recorded := map[string]string{}
	b := newTestBypass(func(_ context.Context, _ *dataplane.Endpoint) (runtimeSyncer, error) {
		return &fakeRuntimeSyncer{sync: func() (*dataplane.SyncResult, error) {
			return &dataplane.SyncResult{Success: true, ActivatedConfigChecksum: "proof-after-apply"}, nil
		}}, nil
	})
	b.recordActivation = func(endpointURL, proof string) { recorded[endpointURL] = proof }

	b.applyRuntimeRaw(context.Background(), depFor([]dataplane.Endpoint{{URL: "http://a"}}),
		bypassPush{body: "config"})

	assert.Equal(t, "proof-after-apply", recorded["http://a"],
		"a successful apply must record its proof, or every bypass costs the next sync a reload")
}

// A nil recorder must be safe — the bypass is constructed without one in tests
// and in any wiring that does not share the deployer's cache.
func TestRuntimeBypass_NilActivationRecorderIsSafe(t *testing.T) {
	b := newTestBypass(func(_ context.Context, _ *dataplane.Endpoint) (runtimeSyncer, error) {
		return &fakeRuntimeSyncer{sync: func() (*dataplane.SyncResult, error) {
			return nil, errors.New("boom")
		}}, nil
	})
	require.NotPanics(t, func() {
		b.applyRuntimeRaw(context.Background(), depFor([]dataplane.Endpoint{{URL: "http://a"}}),
			bypassPush{body: "config"})
	})
}
