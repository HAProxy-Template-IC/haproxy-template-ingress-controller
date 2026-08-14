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

func TestConfigVersionCache_CommitCarriesActivationProof(t *testing.T) {
	c := newConfigVersionCache()
	ep := cacheEndpoint("http://10.0.0.1:5555/v3")

	snapshot := commitTestObservation(t, c, ep, 7, &parserconfig.StructuredConfig{}, "proof-1", "content-abc", "proof-1")
	assert.Equal(t, "proof-1", snapshot.activatedChecksum)
	assert.Equal(t, int64(7), snapshot.version)
	assert.Equal(t, "content-abc", snapshot.contentChecksum)
}

// Clearing the proof is the half of the fix that matters on the error path: a
// skip_version push writes its body to disk even when the runtime actions fail,
// so a failed apply leaves content on disk that no worker ever loaded. Keeping a
// stale proof would let the next sync short-circuit an empty diff over that
// parked content — the #112 stall.
func TestConfigVersionCache_EmptyProofClearsActivation(t *testing.T) {
	c := newConfigVersionCache()
	ep := cacheEndpoint("http://10.0.0.1:5555/v3")

	commitTestObservation(t, c, ep, 7, &parserconfig.StructuredConfig{}, "proof-1", "content-abc", "proof-1")
	generation, ok := c.beginRuntimeMutation(ep)
	require.True(t, ok)
	require.True(t, c.finishRuntimeMutation(ep, generation, ""))
	assert.Empty(t, c.snapshot(ep).activatedChecksum, "an empty proof must clear, not be ignored")

	// Clearing an endpoint that was never recorded must not fabricate an entry.
	unknown := cacheEndpoint("http://10.0.0.2:5555/v3")
	generation, ok = c.beginRuntimeMutation(unknown)
	require.True(t, ok)
	require.True(t, c.finishRuntimeMutation(unknown, generation, ""))
	assert.Empty(t, c.snapshot(unknown).activatedChecksum)
}

func TestConfigVersionCache_AbortDropsActivation(t *testing.T) {
	c := newConfigVersionCache()
	ep := cacheEndpoint("http://10.0.0.1:5555/v3")

	commitTestObservation(t, c, ep, 7, &parserconfig.StructuredConfig{}, "proof-1", "content-abc", "proof-1")
	require.True(t, c.abortSync(ep, c.snapshot(ep).generation))
	assert.Empty(t, c.snapshot(ep).activatedChecksum, "a failed sync must leave nothing provable behind")
}

// An unknown endpoint has no proof, which the orchestrator reads as "force a
// reload before trusting an empty diff" — the safe default.
func TestConfigVersionCache_UnknownEndpointHasNoProof(t *testing.T) {
	c := newConfigVersionCache()
	assert.Empty(t, c.snapshot(cacheEndpoint("http://nobody:5555/v3")).activatedChecksum)
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
	b := newTestBypass(func(_ context.Context, _ *dataplane.Endpoint) (runtimeSyncer, error) {
		return &fakeRuntimeSyncer{sync: func() (*dataplane.SyncResult, error) {
			return nil, errors.New("cannot execute SetServerState: connection refused")
		}}, nil
	})
	endpoint := dataplane.Endpoint{URL: "http://a"}

	b.applyRuntimeRaw(context.Background(), depFor([]dataplane.Endpoint{endpoint}),
		bypassPush{body: "config"})

	assert.Empty(t, b.configCache.snapshot(&endpoint).activatedChecksum,
		"a failed apply must clear the proof — its body may still have reached disk")
}

// A successful apply records what it proved, so the reload-free path stays
// reload-free: the bypass legitimately changes the on-disk bytes, and without a
// fresh proof the next sync would force a reload against them.
func TestRuntimeBypass_SuccessfulApplyRecordsActivationProof(t *testing.T) {
	b := newTestBypass(func(_ context.Context, _ *dataplane.Endpoint) (runtimeSyncer, error) {
		return &fakeRuntimeSyncer{sync: func() (*dataplane.SyncResult, error) {
			return &dataplane.SyncResult{Success: true, ActivatedConfigChecksum: "proof-after-apply"}, nil
		}}, nil
	})
	endpoint := dataplane.Endpoint{URL: "http://a"}

	b.applyRuntimeRaw(context.Background(), depFor([]dataplane.Endpoint{endpoint}),
		bypassPush{body: "config"})

	assert.Equal(t, "proof-after-apply", b.configCache.snapshot(&endpoint).activatedChecksum,
		"a successful apply must record its proof, or every bypass costs the next sync a reload")
}
