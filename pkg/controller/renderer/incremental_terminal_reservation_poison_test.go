// Copyright 2026 Philipp Hossner
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
// http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package renderer

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"gitlab.com/haproxy-haptic/haptic/pkg/controller/rendercontext"
	"gitlab.com/haproxy-haptic/haptic/pkg/httpstore"
)

func TestWarmGraphTerminalReservationCorruptionPublishesNothing(t *testing.T) {
	fixture := newIncrementalHTTPTestFixture(t)
	assert.Equal(t, "a=first\nb=stable\n", fixture.render(t))
	assert.Equal(t, "a=first\nb=stable\n", fixture.render(t))

	service := fixture.service
	baseGraphGeneration := service.incremental.graph.Generation()
	baseSnapshot := service.incremental.snapshot
	baseHTTP := captureIncrementalHTTPOwnership(service.incremental)
	baseEffectA := incrementalHTTPFixtureEffect(t, fixture, "a")
	baseEffectB := incrementalHTTPFixtureEffect(t, fixture, "b")
	baseOutput := captureRenderServicePublication(service)

	descriptor, err := httpstore.DescribeSource(httpstore.FetchOptions{Critical: true}, nil)
	require.NoError(t, err)
	acceptedA := fixture.httpComponent.GetStore().AcceptedSnapshot(fixture.urlA, descriptor)
	acceptedB := fixture.httpComponent.GetStore().AcceptedSnapshot(fixture.urlB, descriptor)
	require.True(t, acceptedA.Found)
	require.True(t, acceptedB.Found)
	require.True(t, fixture.httpComponent.GetStore().HasActiveLease(fixture.urlA))
	require.True(t, fixture.httpComponent.GetStore().HasActiveLease(fixture.urlB))

	require.NoError(t, fixture.provider.GetStore("routes").Update(
		incrementalTestResource("default", "a", map[string]any{"url": fixture.urlB}),
		[]string{"default", "a"},
	))
	result, err := service.Render(t.Context(), fixture.provider, rendercontext.RenderModeReconcile)
	require.NoError(t, err)
	assert.Equal(t, "a=stable\nb=stable\n", result.HAProxyConfig)
	transaction, ok := result.InputTransaction.(*combinedRenderInputTransaction)
	require.True(t, ok)
	require.NotNil(t, transaction.incremental)
	require.NotNil(t, transaction.incremental.graphSession)

	reservation := transaction.publications.reservation
	require.NotNil(t, reservation)
	require.Equal(t, renderOutputReservationReady, renderOutputReservationState(reservation.state.Load()))
	corrupted := false
	err = commitWithCorruptedTerminalReservation(t.Context(), transaction, func() error {
		if !reservation.state.CompareAndSwap(
			uint32(renderOutputReservationPublishing),
			uint32(renderOutputReservationAborted),
		) {
			return errors.New("render output reservation did not reach terminal publication")
		}
		corrupted = true
		return nil
	})
	require.ErrorContains(t, err, "render output reservation changed during publication")
	assert.True(t, corrupted)
	assert.Equal(t, renderOutputReservationAborted, renderOutputReservationState(reservation.state.Load()))

	assert.Equal(t, baseGraphGeneration, service.incremental.graph.Generation())
	assert.Same(t, baseSnapshot, service.incremental.snapshot)
	assert.Equal(t, baseHTTP, captureIncrementalHTTPOwnership(service.incremental))
	assertIncrementalHTTPFixtureEffect(t, fixture, "a", &baseEffectA)
	assertIncrementalHTTPFixtureEffect(t, fixture, "b", &baseEffectB)
	assert.Equal(t, acceptedA, fixture.httpComponent.GetStore().AcceptedSnapshot(fixture.urlA, descriptor))
	assert.Equal(t, acceptedB, fixture.httpComponent.GetStore().AcceptedSnapshot(fixture.urlB, descriptor))
	assert.True(t, fixture.httpComponent.GetStore().HasActiveLease(fixture.urlA))
	assert.True(t, fixture.httpComponent.GetStore().HasActiveLease(fixture.urlB))
	assertRenderServicePublication(t, service, baseOutput)

	service.planMu.Lock()
	_, retained := service.outputReservations[reservation.generation]
	service.planMu.Unlock()
	assert.False(t, retained)
}

func mustSucceedTerminalPublication(err error) {
	if err != nil {
		panic(requiredRenderPublicationPanic{err: err})
	}
}

func corruptedTerminalCommitPublications(
	transaction *combinedRenderInputTransaction,
	corrupt func() error,
) incrementalCommitPublications {
	return incrementalCommitPublications{
		prepare: func() {
			mustSucceedTerminalPublication(transaction.publications.prepareTerminalResult())
		},
		validate: func() {
			mustSucceedTerminalPublication(transaction.publications.validateTerminalResult())
			mustSucceedTerminalPublication(corrupt())
		},
		commit: func() {
			mustSucceedTerminalPublication(transaction.publications.commitTerminalResult())
		},
		release: func() {
			mustSucceedTerminalPublication(transaction.publications.releaseTerminalResult())
		},
	}
}

func commitWithCorruptedTerminalReservation(
	ctx context.Context,
	transaction *combinedRenderInputTransaction,
	corrupt func() error,
) error {
	transaction.once.Do(func() {
		httpTransaction, runtime, logger := transaction.references()
		defer transaction.releaseReferences()
		defer func() {
			if recovered := recover(); recovered != nil {
				transaction.commitErr = errors.Join(
					fmt.Errorf("render input transaction panicked: %v", recovered),
					transaction.abortCandidates(httpTransaction, runtime),
				)
			}
		}()
		if runtime == nil {
			transaction.commitErr = errors.New("incremental render session is unavailable")
			return
		}
		publications := corruptedTerminalCommitPublications(transaction, corrupt)
		if err := runtime.commit(ctx, logger, httpTransaction, publications); err != nil {
			transaction.commitErr = errors.Join(
				err,
				transaction.abortCandidates(httpTransaction, runtime),
			)
		}
	})
	return transaction.commitErr
}

type incrementalHTTPOwnershipSnapshot struct {
	refs    map[uint64]uint64
	flight  map[uint64]uint64
	specs   map[uint64]httpInputSpec
	ids     map[httpInputIdentity]uint64
	byURL   map[string]map[httpstore.SourceDescriptor]uint64
	cursor  incrementalHTTPCursor
	effects int
}

func captureIncrementalHTTPOwnership(state *incrementalRenderState) incrementalHTTPOwnershipSnapshot {
	state.httpMu.Lock()
	byURL := make(map[string]map[httpstore.SourceDescriptor]uint64, len(state.httpByURL))
	for url, descriptors := range state.httpByURL {
		byURL[url] = maps.Clone(descriptors)
	}
	snapshot := incrementalHTTPOwnershipSnapshot{
		refs:   maps.Clone(state.httpRefs),
		flight: maps.Clone(state.httpFlight),
		specs:  maps.Clone(state.httpSpecs),
		ids:    maps.Clone(state.httpIDs),
		byURL:  byURL,
	}
	state.httpMu.Unlock()

	state.mu.Lock()
	snapshot.cursor = state.snapshot.httpCursor
	snapshot.effects = state.snapshot.httpEffects.Len()
	state.mu.Unlock()
	return snapshot
}

func captureRenderServicePublication(service *RenderService) renderServicePublicationState {
	service.planMu.Lock()
	defer service.planMu.Unlock()
	return service.renderServicePublicationStateLocked()
}

func assertRenderServicePublication(
	t *testing.T,
	service *RenderService,
	want renderServicePublicationState,
) {
	t.Helper()
	got := captureRenderServicePublication(service)
	assert.Same(t, want.lastPlan, got.lastPlan)
	assert.Same(t, want.lastCurrentConfigRoot, got.lastCurrentConfigRoot)
	assert.Same(t, want.lastCycleSnapshot, got.lastCycleSnapshot)
	assert.Same(t, want.lastOutputSnapshot, got.lastOutputSnapshot)
	assert.Same(t, want.lastPlanIdentity, got.lastPlanIdentity)
	assert.Same(t, want.lastRenderCache, got.lastRenderCache)
	assert.Equal(t, want.publishedOutputGeneration, got.publishedOutputGeneration)
}
