// Copyright 2026 Philipp Hossner
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

package renderer

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	iradix "github.com/hashicorp/go-immutable-radix/v2"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"gitlab.com/haproxy-haptic/haptic/pkg/controller/helpers"
	controllerhttpstore "gitlab.com/haproxy-haptic/haptic/pkg/controller/httpstore"
	"gitlab.com/haproxy-haptic/haptic/pkg/controller/rendercontext"
	"gitlab.com/haproxy-haptic/haptic/pkg/controller/testutil"
	"gitlab.com/haproxy-haptic/haptic/pkg/controller/typebootstrap"
	"gitlab.com/haproxy-haptic/haptic/pkg/core/config"
	purehttpstore "gitlab.com/haproxy-haptic/haptic/pkg/httpstore"
	"gitlab.com/haproxy-haptic/haptic/pkg/incremental"
	k8sstore "gitlab.com/haproxy-haptic/haptic/pkg/k8s/store"
	"gitlab.com/haproxy-haptic/haptic/pkg/stores"
)

func TestRenderServiceIncrementalHTTPDoesNotExecuteForUnrelatedSemanticChange(t *testing.T) {
	fixture := newIncrementalHTTPTestFixture(t)
	assert.Equal(t, "a=first\nb=stable\n", fixture.render(t))
	assert.Equal(t, "a=first\nb=stable\n", fixture.render(t))
	requestsA := fixture.requestsA.Load()
	requestsB := fixture.requestsB.Load()

	fixture.httpComponent.GetStore().LoadFixture("https://unrelated.example.test/data", "unrelated")

	assert.Equal(t, "a=first\nb=stable\n", fixture.render(t))
	assert.Equal(t, uint64(1), fixture.service.incremental.graph.Counters(fixture.queryA).Executions)
	assert.Equal(t, uint64(1), fixture.service.incremental.graph.Counters(fixture.queryB).Executions)
	assert.Equal(t, requestsA, fixture.requestsA.Load())
	assert.Equal(t, requestsB, fixture.requestsB.Load())
}

func TestRenderServiceIncrementalHTTPAcceptedABABackdatesExactConsumer(t *testing.T) {
	fixture := newIncrementalHTTPTestFixture(t)
	assert.Equal(t, "a=first\nb=stable\n", fixture.render(t))
	assert.Equal(t, "a=first\nb=stable\n", fixture.render(t))

	fixture.bodyA.Store("intermediate")
	promoteIncrementalHTTPBody(t, fixture.httpComponent, fixture.urlA)
	fixture.bodyA.Store("first")
	promoteIncrementalHTTPBody(t, fixture.httpComponent, fixture.urlA)

	assert.Equal(t, "a=first\nb=stable\n", fixture.render(t))
	assert.Equal(t, uint64(1), fixture.service.incremental.graph.Counters(fixture.queryA).Executions)
	assert.Equal(t, uint64(1), fixture.service.incremental.graph.Counters(fixture.queryB).Executions)
	assert.Equal(t, int32(3), fixture.requestsA.Load())
	assert.Equal(t, int32(1), fixture.requestsB.Load())
}

func TestRenderServiceIncrementalHTTPSharedConsumersAcceptTokenOnlyRevision(t *testing.T) {
	fixture := newIncrementalHTTPTestFixture(t)
	baseline := prepareIncrementalHTTPSharedConsumers(t, fixture)
	advanceIncrementalHTTPAcceptedABA(t, fixture, &baseline.firstA)

	assert.Equal(t, baseline.output, fixture.render(t))
	assert.Equal(t, uint64(1), fixture.service.incremental.graph.Counters(fixture.queryA).Executions)
	assert.Equal(t, uint64(1), fixture.service.incremental.graph.Counters(fixture.queryB).Executions)
	assert.Same(t, baseline.index, fixture.service.incremental.snapshot.groupIndexes["routes"])
	assertIncrementalHTTPFixtureEffect(t, fixture, "a", &baseline.firstA)
	assertIncrementalHTTPFixtureEffect(t, fixture, "b", &baseline.firstB)
	committedSnapshot := fixture.service.incremental.snapshot
	committedIndex := committedSnapshot.groupIndexes["routes"]

	require.NoError(t, fixture.provider.GetStore("routes").Update(
		incrementalTestResource("default", "a", map[string]any{
			"url":       fixture.urlA,
			"unrelated": "changed",
		}),
		[]string{"default", "a"},
	))
	aborted, err := fixture.service.Render(t.Context(), fixture.provider, rendercontext.RenderModeReconcile)
	require.NoError(t, err)
	require.NotNil(t, aborted.InputTransaction)
	assert.Equal(t, baseline.output, aborted.HAProxyConfig)
	transaction, ok := aborted.InputTransaction.(*combinedRenderInputTransaction)
	require.True(t, ok)
	assert.False(t, transaction.incremental.groupChanged["routes"])
	assert.Same(t, committedIndex, transaction.incremental.groupIndexes["routes"])
	aborted.InputTransaction.Abort()

	assert.Same(t, committedSnapshot, fixture.service.incremental.snapshot)
	assert.Equal(t, uint64(1), fixture.service.incremental.graph.Counters(fixture.queryA).Executions)
	assert.Equal(t, uint64(1), fixture.service.incremental.graph.Counters(fixture.queryB).Executions)
	assert.Equal(t, baseline.output, fixture.render(t))
	assert.Equal(t, uint64(2), fixture.service.incremental.graph.Counters(fixture.queryA).Executions)
	assert.Equal(t, uint64(1), fixture.service.incremental.graph.Counters(fixture.queryB).Executions)
	assert.Same(t, committedIndex, fixture.service.incremental.snapshot.groupIndexes["routes"])
	assertIncrementalHTTPFixtureEffect(t, fixture, "a", &baseline.firstA)
	assertIncrementalHTTPFixtureEffect(t, fixture, "b", &baseline.firstB)

	assert.Equal(t, baseline.output, fixture.render(t))
	assert.Equal(t, uint64(2), fixture.service.incremental.graph.Counters(fixture.queryA).Executions)
	assert.Equal(t, uint64(1), fixture.service.incremental.graph.Counters(fixture.queryB).Executions)
	assert.Equal(t, int32(3), fixture.requestsA.Load())
	assert.Zero(t, fixture.requestsB.Load())
}

type incrementalHTTPSharedConsumerBaseline struct {
	output string
	firstA incrementalHTTPEffect
	firstB incrementalHTTPEffect
	index  *incrementalGroupIndex
}

func prepareIncrementalHTTPSharedConsumers(
	t *testing.T,
	fixture *incrementalHTTPTestFixture,
) *incrementalHTTPSharedConsumerBaseline {
	t.Helper()
	require.NoError(t, fixture.provider.GetStore("routes").Update(
		incrementalTestResource("default", "b", map[string]any{"url": fixture.urlA}),
		[]string{"default", "b"},
	))
	const output = "a=first\nb=first\n"
	assert.Equal(t, output, fixture.render(t))
	assert.Equal(t, output, fixture.render(t))
	assert.Equal(t, uint64(1), fixture.service.incremental.graph.Counters(fixture.queryA).Executions)
	assert.Equal(t, uint64(1), fixture.service.incremental.graph.Counters(fixture.queryB).Executions)
	assert.Equal(t, int32(1), fixture.requestsA.Load())
	assert.Zero(t, fixture.requestsB.Load())
	firstA := incrementalHTTPFixtureEffect(t, fixture, "a")
	firstB := incrementalHTTPFixtureEffect(t, fixture, "b")
	require.Equal(t, firstA.inputID, firstB.inputID)
	require.True(t, sameHTTPSnapshot(&firstA.snapshot, &firstB.snapshot))
	return &incrementalHTTPSharedConsumerBaseline{
		output: output,
		firstA: firstA,
		firstB: firstB,
		index:  fixture.service.incremental.snapshot.groupIndexes["routes"],
	}
}

func TestRenderServiceIncrementalHTTPDeletePromotesCachedOwnerAndReaddRestoresWinner(t *testing.T) {
	fixture := newIncrementalHTTPTestFixture(t)
	baseline := prepareIncrementalHTTPSharedConsumers(t, fixture)
	component := fixture.service.incremental.components["routes"]
	assertIncrementalHTTPGroupWinner(t, fixture.service, baseline.firstA.inputID, "b", 2)
	baselineEffects := authenticatedIncrementalHTTPEffectTuples(t, fixture.service)
	require.Len(t, baselineEffects, 1)

	routes := fixture.provider.GetStore("routes")
	require.NoError(t, routes.Delete("default", "b", []string{"default", "b"}))
	deleted := renderIncrementalHTTPTestResult(t, fixture.service, fixture.provider)
	assert.Equal(t, "a=first\n", deleted.HAProxyConfig)
	assert.Equal(t, uint64(1), fixture.service.incremental.graph.Counters(fixture.queryA).Executions)
	assert.Zero(t, fixture.service.incremental.graph.Counters(fixture.queryB).Executions)
	assert.Equal(t, int32(1), fixture.requestsA.Load())
	assert.Zero(t, fixture.requestsB.Load())
	assertIncrementalHTTPGroupWinner(t, fixture.service, baseline.firstA.inputID, "a", 1)
	assertIncrementalHTTPFixtureEffect(t, fixture, "a", &baseline.firstA)
	assert.Equal(t, baselineEffects, authenticatedIncrementalHTTPEffectTuples(t, fixture.service))
	_, resultFound := fixture.service.incremental.snapshot.results.Get(
		resultKey(&component, "routes", "default", "b"),
	)
	assert.False(t, resultFound)
	_, effectFound := fixture.service.incremental.snapshot.httpEffects.Get(
		resultKey(&component, "routes", "default", "b"),
	)
	assert.False(t, effectFound)
	_, ownerFound := fixture.service.incremental.snapshot.groupIndexes["routes"].instances.Root().Get(
		incrementalGroupInstanceKey(incrementalGroupInstanceID{
			component: component.name, source: "routes", namespace: "default", name: "b",
		}),
	)
	assert.False(t, ownerFound)

	require.NoError(t, routes.Add(
		incrementalTestResource("default", "b", map[string]any{"url": fixture.urlA}),
		[]string{"default", "b"},
	))
	readded := renderIncrementalHTTPTestResult(t, fixture.service, fixture.provider)
	assert.Equal(t, baseline.output, readded.HAProxyConfig)
	assert.Equal(t, uint64(1), fixture.service.incremental.graph.Counters(fixture.queryA).Executions)
	assert.Equal(t, uint64(1), fixture.service.incremental.graph.Counters(fixture.queryB).Executions)
	assert.Equal(t, int32(1), fixture.requestsA.Load())
	assert.Zero(t, fixture.requestsB.Load())
	assertIncrementalHTTPGroupWinner(t, fixture.service, baseline.firstA.inputID, "b", 2)
	assertIncrementalHTTPFixtureEffect(t, fixture, "a", &baseline.firstA)
	assertIncrementalHTTPFixtureEffect(t, fixture, "b", &baseline.firstB)
	assert.Equal(t, baselineEffects, authenticatedIncrementalHTTPEffectTuples(t, fixture.service))

	oracle := NewRenderService(&RenderServiceConfig{
		Engine: fixture.service.engine, Config: fixture.service.config,
		Logger: fixture.service.logger, HTTPStoreComponent: fixture.httpComponent,
	})
	oracleResult := renderIncrementalHTTPTestResult(t, oracle, fixture.provider)
	assertRenderResultObservablesEqual(t, oracleResult, readded)
	oracleEffects := authenticatedIncrementalHTTPEffectTuples(t, oracle)
	require.Len(t, oracleEffects, 1)
	oracleInputID := oracleEffects[0].inputID
	oracleEffects[0].inputID = baselineEffects[0].inputID
	assert.Equal(t, baselineEffects, oracleEffects)
	assertIncrementalHTTPGroupWinner(t, oracle, oracleInputID, "b", 2)
	assert.Equal(t, int32(1), fixture.requestsA.Load())
	assert.Zero(t, fixture.requestsB.Load())
}

func advanceIncrementalHTTPAcceptedABA(
	t *testing.T,
	fixture *incrementalHTTPTestFixture,
	previous *incrementalHTTPEffect,
) {
	t.Helper()
	fixture.bodyA.Store("intermediate")
	promoteIncrementalHTTPBody(t, fixture.httpComponent, fixture.urlA)
	fixture.bodyA.Store("first")
	promoteIncrementalHTTPBody(t, fixture.httpComponent, fixture.urlA)
	descriptor, err := purehttpstore.DescribeSource(purehttpstore.FetchOptions{Critical: true}, nil)
	require.NoError(t, err)
	accepted, found := fixture.httpComponent.AcceptedSnapshot(fixture.urlA, descriptor)
	require.True(t, found)
	require.False(t, sameHTTPSnapshot(&previous.snapshot, &accepted))
	require.True(t, sameHTTPReusableSnapshot(&previous.snapshot, &accepted))
}

func TestIncrementalHTTPChangeLookupIsExactAcrossDeclarationsForOneURL(t *testing.T) {
	url := "https://same.example.test/data"
	declarations := []struct {
		options purehttpstore.FetchOptions
		auth    *purehttpstore.AuthConfig
	}{
		{options: purehttpstore.FetchOptions{Critical: true}},
		{options: purehttpstore.FetchOptions{Timeout: time.Second}},
		{options: purehttpstore.FetchOptions{Retries: 1}},
		{options: purehttpstore.FetchOptions{RetryDelay: time.Second}},
		{options: purehttpstore.FetchOptions{Delay: time.Minute}},
		{auth: &purehttpstore.AuthConfig{Type: purehttpstore.AuthTypeBasic, Username: "user", Password: "one"}},
		{auth: &purehttpstore.AuthConfig{Type: purehttpstore.AuthTypeBearer, Token: "one"}},
		{auth: &purehttpstore.AuthConfig{Type: purehttpstore.AuthTypeBearer, Token: "two"}},
		{auth: &purehttpstore.AuthConfig{Type: purehttpstore.AuthTypeHeader, Headers: map[string]string{"X-Key": "one"}}},
		{auth: &purehttpstore.AuthConfig{Type: purehttpstore.AuthTypeHeader, Headers: map[string]string{"X-Key": "two"}}},
	}
	state := newHTTPRegistryTestState()
	retained := map[uint64]struct{}{}
	specs := make([]httpInputSpec, 0, len(declarations))
	for index := range declarations {
		descriptor, err := purehttpstore.DescribeSource(declarations[index].options, declarations[index].auth)
		require.NoError(t, err)
		spec, _, err := state.acquireHTTPInput(httpInputIdentity{url: url, descriptor: descriptor})
		require.NoError(t, err)
		retained[spec.id] = struct{}{}
		specs = append(specs, spec)
	}
	require.Len(t, retained, len(declarations))

	affected := state.httpInputsForChange(&purehttpstore.SemanticChange{
		URL:                url,
		PreviousDescriptor: specs[6].descriptor,
		Descriptor:         specs[7].descriptor,
	})

	require.Len(t, affected, 2)
	assert.Equal(t, []uint64{specs[6].id, specs[7].id}, []uint64{affected[0].id, affected[1].id})
	state.finishHTTPInputs(retained, nil, iradix.New[*iradix.Tree[incrementalHTTPEffect]](), true)
	assert.Empty(t, state.httpSpecs)
}

func TestIncrementalHTTPNegativeProofRejectsExactABAAndJournalLoss(t *testing.T) {
	options := purehttpstore.FetchOptions{Critical: true}
	descriptor, err := purehttpstore.DescribeSource(options, nil)
	require.NoError(t, err)

	t.Run("unrelated change", func(t *testing.T) {
		component := newIncrementalHTTPProofComponent(t, 0)
		runtime, revision := newIncrementalHTTPNegativeProof(t, component, "https://missing.example.test", descriptor)
		component.GetStore().LoadFixture("https://unrelated.example.test", "value")

		verified, err := runtime.verifyResources(t.Context(), []incremental.InputRevision{revision})
		require.NoError(t, err)
		assert.True(t, verified)
	})

	t.Run("present evicted ABA", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			_, _ = w.Write([]byte("present"))
		}))
		t.Cleanup(server.Close)
		component := newIncrementalHTTPProofComponent(t, -time.Hour)
		runtime, revision := newIncrementalHTTPNegativeProof(t, component, server.URL, descriptor)
		_, err := component.GetStore().Fetch(t.Context(), server.URL, options, nil)
		require.NoError(t, err)
		require.Equal(t, []string{server.URL}, component.GetStore().EvictUnused())

		verified, err := runtime.verifyResources(t.Context(), []incremental.InputRevision{revision})
		require.NoError(t, err)
		assert.False(t, verified)
	})

	t.Run("journal overflow", func(t *testing.T) {
		component := newIncrementalHTTPProofComponent(t, 0)
		runtime, revision := newIncrementalHTTPNegativeProof(t, component, "https://missing.example.test", descriptor)
		for index := range 4097 {
			component.GetStore().LoadFixture(fmt.Sprintf("https://unrelated.example.test/%04d", index), "value")
		}

		verified, err := runtime.verifyResources(t.Context(), []incremental.InputRevision{revision})
		require.NoError(t, err)
		assert.False(t, verified)
	})
}

func TestRenderServiceIncrementalHTTPAbortDoesNotPublishSpeculativeReferences(t *testing.T) {
	fixture := newIncrementalHTTPTestFixture(t)
	assert.Equal(t, "a=first\nb=stable\n", fixture.render(t))
	assert.Equal(t, "a=first\nb=stable\n", fixture.render(t))
	assert.ElementsMatch(t, []string{fixture.urlA, fixture.urlB}, incrementalHTTPRegistryURLs(fixture.service.incremental))
	committedCandidate := fixture.service.exactCycleCandidate
	committedLeaseToken := fixture.service.incremental.snapshot.httpCursor.token

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("replacement"))
	}))
	t.Cleanup(server.Close)
	require.NoError(t, fixture.provider.GetStore("routes").Update(
		incrementalTestResource("default", "a", map[string]any{"url": server.URL}),
		[]string{"default", "a"},
	))

	result, err := fixture.service.Render(t.Context(), fixture.provider, rendercontext.RenderModeReconcile)
	require.NoError(t, err)
	require.NotNil(t, result.InputTransaction)
	assert.Equal(t, "a=replacement\nb=stable\n", result.HAProxyConfig)
	assert.ElementsMatch(t,
		[]string{fixture.urlA, fixture.urlB},
		incrementalHTTPRegistryURLs(fixture.service.incremental),
	)
	result.InputTransaction.Abort()

	assert.ElementsMatch(t, []string{fixture.urlA, fixture.urlB}, incrementalHTTPRegistryURLs(fixture.service.incremental))
	assert.Equal(t, uint64(1), fixture.service.incremental.graph.Counters(fixture.queryA).Executions)
	assert.Equal(t, uint64(1), fixture.service.incremental.graph.Counters(fixture.queryB).Executions)
	descriptor, err := purehttpstore.DescribeSource(purehttpstore.FetchOptions{Critical: true}, nil)
	require.NoError(t, err)
	assert.False(t, fixture.httpComponent.GetStore().AcceptedSnapshot(server.URL, descriptor).Found)
	assert.False(t, fixture.httpComponent.GetStore().HasActiveLease(server.URL))
	assert.Same(t, committedCandidate, fixture.service.exactCycleCandidate)
	assert.Equal(t, committedLeaseToken, fixture.service.incremental.snapshot.httpCursor.token)

	cold, err := fixture.service.Render(t.Context(), fixture.provider, rendercontext.RenderModeReconcile)
	require.NoError(t, err)
	assert.Equal(t, "a=replacement\nb=stable\n", cold.HAProxyConfig)
	require.NoError(t, cold.InputTransaction.Commit(t.Context()))
	assert.Equal(t, uint64(1), fixture.service.incremental.graph.Counters(fixture.queryA).Executions)
	assert.True(t, fixture.httpComponent.GetStore().HasActiveLease(server.URL))
	assert.True(t, fixture.httpComponent.GetStore().AcceptedSnapshot(server.URL, descriptor).Found)
	require.NotNil(t, fixture.service.exactCycleCandidate)
	assert.Equal(t, exactCycleCandidateOutputOnly, fixture.service.exactCycleCandidate.mode)

	reused, err := fixture.service.Render(t.Context(), fixture.provider, rendercontext.RenderModeReconcile)
	require.NoError(t, err)
	assert.Equal(t, cold.HAProxyConfig, reused.HAProxyConfig)
	assert.Same(t, cold.CycleSnapshot, reused.CycleSnapshot)
	assert.Nil(t, reused.Plan)
	reusedTransaction, ok := reused.InputTransaction.(*combinedRenderInputTransaction)
	require.True(t, ok)
	require.NotNil(t, reusedTransaction.incremental)
	assert.True(t, reusedTransaction.incremental.exactCycleOutputOnlyReplay)
	assert.Empty(t, reusedTransaction.incremental.freshResults)
	assert.Empty(t, reusedTransaction.incremental.httpExecuted)
	require.NoError(t, reused.InputTransaction.Commit(t.Context()))
	assert.Equal(t, uint64(1), fixture.service.incremental.graph.Counters(fixture.queryA).Executions)
	assert.Equal(t, uint64(1), fixture.service.incremental.graph.Counters(fixture.queryB).Executions)
	assert.True(t, fixture.httpComponent.GetStore().HasActiveLease(server.URL))
}

func TestIncrementalHTTPRelevantChangeIsAcknowledgedOnlyByCachePublication(t *testing.T) {
	tests := []struct {
		name string
		run  func(*testing.T, *incrementalHTTPTestFixture, *RenderResult)
	}{
		{
			name: "abort",
			run: func(_ *testing.T, _ *incrementalHTTPTestFixture, result *RenderResult) {
				result.InputTransaction.Abort()
			},
		},
		{
			name: "admission commit",
			run: func(t *testing.T, _ *incrementalHTTPTestFixture, result *RenderResult) {
				t.Helper()
				require.NoError(t, result.InputTransaction.Commit(t.Context()))
			},
		},
		{
			name: "cancelled commit",
			run: func(t *testing.T, _ *incrementalHTTPTestFixture, result *RenderResult) {
				t.Helper()
				ctx, cancel := context.WithCancel(t.Context())
				cancel()
				require.Error(t, result.InputTransaction.Commit(ctx))
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			fixture := newIncrementalHTTPTestFixture(t)
			assert.Equal(t, "a=first\nb=stable\n", fixture.render(t))
			assert.Equal(t, "a=first\nb=stable\n", fixture.render(t))
			state := fixture.service.incremental
			baseToken := state.snapshot.httpCursor.token
			fixture.bodyA.Store("changed")
			promoteIncrementalHTTPBody(t, fixture.httpComponent, fixture.urlA)
			require.Len(t, incrementalActiveLeaseChanges(t, fixture, baseToken), 1)

			mode := rendercontext.RenderModeReconcile
			if test.name == "admission commit" {
				mode = rendercontext.RenderModeAdmission
			}
			result, err := fixture.service.Render(t.Context(), fixture.provider, mode)
			require.NoError(t, err)
			require.NotNil(t, result.InputTransaction)
			assert.Equal(t, "a=changed\nb=stable\n", result.HAProxyConfig)
			test.run(t, fixture, result)

			assert.Equal(t, baseToken, state.snapshot.httpCursor.token)
			require.Len(t, incrementalActiveLeaseChanges(t, fixture, baseToken), 1)
		})
	}
}

func TestIncrementalHTTPStaleEquivalentCacheLoserCannotRepublishLeaseToken(t *testing.T) {
	fixture := newIncrementalHTTPTestFixture(t)
	assert.Equal(t, "a=first\nb=stable\n", fixture.render(t))
	assert.Equal(t, "a=first\nb=stable\n", fixture.render(t))
	fixture.bodyA.Store("changed")
	promoteIncrementalHTTPBody(t, fixture.httpComponent, fixture.urlA)

	loser, err := fixture.service.Render(t.Context(), fixture.provider, rendercontext.RenderModeReconcile)
	require.NoError(t, err)
	winner, err := fixture.service.Render(t.Context(), fixture.provider, rendercontext.RenderModeReconcile)
	require.NoError(t, err)
	require.NoError(t, winner.InputTransaction.Commit(t.Context()))
	winnerToken := fixture.service.incremental.snapshot.httpCursor.token
	require.NoError(t, loser.InputTransaction.Commit(t.Context()))

	assert.Equal(t, winnerToken, fixture.service.incremental.snapshot.httpCursor.token)
	assert.Empty(t, incrementalActiveLeaseChanges(t, fixture, winnerToken))
}

func TestIncrementalHTTPNewerSiblingConflictCannotRepublishLeaseToken(t *testing.T) {
	fixture := newIncrementalHTTPTestFixture(t)
	assert.Equal(t, "a=first\nb=stable\n", fixture.render(t))
	assert.Equal(t, "a=first\nb=stable\n", fixture.render(t))
	fixture.bodyA.Store("changed")
	promoteIncrementalHTTPBody(t, fixture.httpComponent, fixture.urlA)

	winner, err := fixture.service.Render(t.Context(), fixture.provider, rendercontext.RenderModeReconcile)
	require.NoError(t, err)
	sibling, err := fixture.service.Render(t.Context(), fixture.provider, rendercontext.RenderModeReconcile)
	require.NoError(t, err)
	require.NoError(t, winner.InputTransaction.Commit(t.Context()))
	winnerToken := fixture.service.incremental.snapshot.httpCursor.token
	require.ErrorContains(t, sibling.InputTransaction.Commit(t.Context()), "changed while the render was running")

	assert.Equal(t, winnerToken, fixture.service.incremental.snapshot.httpCursor.token)
	assert.Empty(t, incrementalActiveLeaseChanges(t, fixture, winnerToken))
}

func TestIncrementalHTTPCommitFenceRejectsOnlyLateRelevantChanges(t *testing.T) {
	t.Run("relevant", func(t *testing.T) {
		fixture := newIncrementalHTTPTestFixture(t)
		assert.Equal(t, "a=first\nb=stable\n", fixture.render(t))
		assert.Equal(t, "a=first\nb=stable\n", fixture.render(t))
		baseToken := fixture.service.incremental.snapshot.httpCursor.token
		result, err := fixture.service.Render(t.Context(), fixture.provider, rendercontext.RenderModeReconcile)
		require.NoError(t, err)
		fixture.bodyA.Store("late")
		promoteIncrementalHTTPBody(t, fixture.httpComponent, fixture.urlA)

		require.ErrorContains(t, result.InputTransaction.Commit(t.Context()), "changed while the render was running")
		assert.Equal(t, baseToken, fixture.service.incremental.snapshot.httpCursor.token)
		require.Len(t, incrementalActiveLeaseChanges(t, fixture, baseToken), 1)
	})

	t.Run("unrelated", func(t *testing.T) {
		fixture := newIncrementalHTTPTestFixture(t)
		assert.Equal(t, "a=first\nb=stable\n", fixture.render(t))
		assert.Equal(t, "a=first\nb=stable\n", fixture.render(t))
		baseToken := fixture.service.incremental.snapshot.httpCursor.token
		result, err := fixture.service.Render(t.Context(), fixture.provider, rendercontext.RenderModeReconcile)
		require.NoError(t, err)
		fixture.httpComponent.GetStore().LoadFixture("https://unrelated.example.test/late", "late")

		require.NoError(t, result.InputTransaction.Commit(t.Context()))
		assert.Equal(t, baseToken, fixture.service.incremental.snapshot.httpCursor.token)
		assert.Empty(t, incrementalActiveLeaseChanges(t, fixture, baseToken))
	})
}

func incrementalActiveLeaseChanges(
	t *testing.T,
	fixture *incrementalHTTPTestFixture,
	token purehttpstore.ActiveLeaseToken,
) []purehttpstore.ActiveLeaseChange {
	t.Helper()
	snapshot, err := fixture.httpComponent.BeginActiveLeases(
		fixture.service.incremental.httpLeaseSet,
		token,
	)
	require.NoError(t, err)
	return snapshot.Changes()
}

func TestRenderServiceIncrementalHTTPDoesNotCacheNonCriticalFailure(t *testing.T) {
	var requests atomic.Int32
	var available atomic.Bool
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		requests.Add(1)
		if !available.Load() {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		_, _ = w.Write([]byte("present"))
	}))
	t.Cleanup(server.Close)
	service, provider, query := newNonCriticalIncrementalHTTPService(t, server.URL)

	result, err := service.Render(t.Context(), provider, rendercontext.RenderModeReconcile)
	require.NoError(t, err)
	require.NotNil(t, result.InputTransaction)
	assert.Equal(t, "a=\n", result.HAProxyConfig)
	require.NoError(t, result.InputTransaction.Commit(t.Context()))
	assert.Equal(t, int32(2), requests.Load())
	assert.Equal(t, uint64(0), service.incremental.graph.Counters(query).Executions)

	descriptor, err := purehttpstore.DescribeSource(
		purehttpstore.FetchOptions{Retries: 1, Timeout: time.Second}, nil,
	)
	require.NoError(t, err)
	assert.False(t, service.httpStoreComponent.GetStore().AcceptedSnapshot(server.URL, descriptor).Found)

	available.Store(true)
	result, err = service.Render(t.Context(), provider, rendercontext.RenderModeReconcile)
	require.NoError(t, err)
	require.NotNil(t, result.InputTransaction)
	assert.Equal(t, "a=present\n", result.HAProxyConfig)
	assert.Equal(t, int32(3), requests.Load())
	result.InputTransaction.Abort()
	assert.False(t, service.httpStoreComponent.GetStore().AcceptedSnapshot(server.URL, descriptor).Found)

	result, err = service.Render(t.Context(), provider, rendercontext.RenderModeReconcile)
	require.NoError(t, err)
	require.NotNil(t, result.InputTransaction)
	assert.Equal(t, "a=present\n", result.HAProxyConfig)
	assert.Equal(t, int32(4), requests.Load())
	require.NoError(t, result.InputTransaction.Commit(t.Context()))
	accepted := service.httpStoreComponent.GetStore().AcceptedSnapshot(server.URL, descriptor)
	require.True(t, accepted.Found)
	assert.Equal(t, "present", accepted.Content)
	assert.Equal(t, uint64(0), service.incremental.graph.Counters(query).Executions)

	assert.Equal(t, "a=present\n", renderAndCommitIncrementalCacheReady(t, service, provider))
	assert.Equal(t, "a=present\n", renderAndCommitIncrementalCacheReady(t, service, provider))
	assert.Equal(t, int32(4), requests.Load())
	assert.Equal(t, uint64(1), service.incremental.graph.Counters(query).Executions)
}

func TestRenderServiceColdRestartRejectsExtraContextMutationWithoutPublication(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		if request.URL.Path == "/missing" {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		_, _ = fmt.Fprint(w, request.URL.Path[1:])
	}))
	t.Cleanup(server.Close)
	cfg := &config.Config{
		Dataplane: testDataplaneConfig(),
		TemplatingSettings: config.TemplatingSettings{ExtraContext: map[string]any{
			"mutate": false,
			"value":  "original",
		}},
		WatchedResources: map[string]config.WatchedResource{
			"routes": {
				APIVersion: "example.test/v1", Resources: "routes",
				IndexBy: []string{"metadata.namespace", "metadata.name"},
			},
		},
		TemplateSnippets: map[string]config.TemplateSnippet{
			"routes": {
				Name: "routes", Requires: []string{"routes"},
				Incremental: &config.IncrementalTemplate{Source: "routes"},
				Template: `{{ item | dig_string("", "metadata", "name") }}={{ http.Fetch(item | dig_string("", "spec", "url"), map[string]any{"critical": item | dig("spec", "critical") | fallback(true)}) }}/pods={{ len(controller.haproxy_pods.List()) }}
`,
			},
		},
		HAProxyConfig: config.HAProxyConfig{Template: `{% var before = tostring(extraContext["value"]) %}before={{ before }},files={{ currentFiles["gate"] }}
{{ render "routes" }}{% if tostring(extraContext["mutate"]) == "true" %}{% extraContext["value"] = "poisoned" %}{% end %}`},
	}
	declarations := helpers.BuildAdditionalDeclarations(cfg, &typebootstrap.Result{
		Types: map[string]reflect.Type{}, Kinds: map[string]string{}, Errors: map[string]error{},
	})
	engine, err := helpers.NewEngineFromConfigWithOptions(cfg, nil, nil, declarations, helpers.EngineOptions{})
	require.NoError(t, err)
	bus, logger := testutil.NewTestBusAndLogger()
	httpComponent := controllerhttpstore.New(bus, logger, -time.Hour)
	pods := k8sstore.NewMemoryStore(2)
	require.NoError(t, pods.Add(
		incrementalTestResource("default", "haproxy-0", nil),
		[]string{"default", "haproxy-0"},
	))
	var currentFilesCalls atomic.Int32
	service := NewRenderService(&RenderServiceConfig{
		Engine: engine, Config: cfg, Logger: logger, HTTPStoreComponent: httpComponent,
		HAProxyPodStore: pods,
		CurrentAuxFilesProvider: func() map[string]string {
			return map[string]string{"gate": fmt.Sprintf("snapshot-%d", currentFilesCalls.Add(1))}
		},
	})
	routes := k8sstore.NewMemoryStore(2)
	require.NoError(t, routes.Add(
		incrementalTestResource("default", "a", map[string]any{"url": server.URL + "/old"}),
		[]string{"default", "a"},
	))
	provider := stores.NewRealStoreProvider(map[string]stores.Store{"routes": routes})

	assert.Equal(t, "before=original,files=snapshot-1\na=old/pods=1\n", renderAndCommitIncrementalCacheReady(t, service, provider))
	assert.Equal(t, "before=original,files=snapshot-2\na=old/pods=1\n", renderAndCommitIncrementalCacheReady(t, service, provider))
	require.Empty(t, httpComponent.GetStore().EvictUnused())
	require.NoError(t, routes.Add(
		incrementalTestResource("default", "b", map[string]any{
			"url": server.URL + "/missing", "critical": false,
		}),
		[]string{"default", "b"},
	))
	cfg.TemplatingSettings.ExtraContext["mutate"] = true
	committedSnapshot := service.incremental.snapshot
	committedCandidate := service.exactCycleCandidate
	committedLeaseToken := committedSnapshot.httpCursor.token

	result, err := service.Render(t.Context(), provider, rendercontext.RenderModeReconcile)
	require.ErrorContains(t, err, "template mutates an immutable input")
	assert.Nil(t, result)
	assert.Equal(t, "original", cfg.TemplatingSettings.ExtraContext["value"])
	assert.Equal(t, int32(3), currentFilesCalls.Load())
	assert.Same(t, committedSnapshot, service.incremental.snapshot)
	assert.Same(t, committedCandidate, service.exactCycleCandidate)
	assert.Equal(t, committedLeaseToken, service.incremental.snapshot.httpCursor.token)

	cfg.TemplatingSettings.ExtraContext["mutate"] = false
	recovered, err := service.Render(t.Context(), provider, rendercontext.RenderModeReconcile)
	require.NoError(t, err)
	assert.Equal(t, "before=original,files=snapshot-4\na=old/pods=1\nb=/pods=1\n", recovered.HAProxyConfig)
	require.NoError(t, recovered.InputTransaction.Commit(t.Context()))
	assert.Equal(t, "original", cfg.TemplatingSettings.ExtraContext["value"])
}

type authenticatedIncrementalHTTPEffectTuple struct {
	group          string
	inputID        uint64
	url            string
	sourceIdentity string
	content        string
	found          bool
	cacheable      bool
	kind           purehttpstore.SnapshotKind
	revision       purehttpstore.Revision
	observation    purehttpstore.Revision
	watermark      purehttpstore.Revision
}

func authenticatedIncrementalHTTPEffectTuples(
	t *testing.T,
	service *RenderService,
) []authenticatedIncrementalHTTPEffectTuple {
	t.Helper()
	require.NotNil(t, service.incremental.snapshot)
	require.NotNil(t, service.httpStoreComponent)
	store := service.httpStoreComponent.GetStore()
	source := service.httpStoreComponent.RevisionSource()
	groups := make([]string, 0, len(service.incremental.snapshot.groupIndexes))
	for group := range service.incremental.snapshot.groupIndexes {
		groups = append(groups, group)
	}
	slices.Sort(groups)
	tuples := make([]authenticatedIncrementalHTTPEffectTuple, 0)
	for _, group := range groups {
		index := service.incremental.snapshot.groupIndexes[group]
		effects, err := index.httpEffects()
		require.NoError(t, err)
		for effectIndex := range effects {
			effect := &effects[effectIndex]
			snapshot := &effect.snapshot
			require.Equal(t, source, snapshot.StoreSource)
			require.True(t, snapshot.Cacheable)
			require.True(t, snapshot.Token.Valid())
			require.Equal(t, source, snapshot.Token.Source())
			require.Equal(t, snapshot.URL, snapshot.Token.URL())
			require.Equal(t, snapshot.Descriptor, snapshot.Token.SourceDescriptor())
			require.Equal(t, purehttpstore.SnapshotAccepted, snapshot.Token.Kind())
			require.True(t, store.VerifySnapshots([]purehttpstore.SnapshotToken{snapshot.Token}))
			observation := snapshot.ObservationToken()
			require.True(t, observation.Valid())
			require.True(t, store.VerifyObservations([]purehttpstore.ObservationToken{observation}))
			tuples = append(tuples, authenticatedIncrementalHTTPEffectTuple{
				group:          group,
				inputID:        effect.inputID,
				url:            snapshot.URL,
				sourceIdentity: snapshot.Descriptor.Identity(),
				content:        snapshot.Content,
				found:          snapshot.Found,
				cacheable:      snapshot.Cacheable,
				kind:           snapshot.Token.Kind(),
				revision:       snapshot.Token.Revision(),
				observation:    snapshot.Observation,
				watermark:      snapshot.Watermark,
			})
		}
	}
	slices.SortFunc(tuples, func(left, right authenticatedIncrementalHTTPEffectTuple) int {
		if order := strings.Compare(left.group, right.group); order != 0 {
			return order
		}
		if left.inputID < right.inputID {
			return -1
		}
		if left.inputID > right.inputID {
			return 1
		}
		if order := strings.Compare(left.url, right.url); order != 0 {
			return order
		}
		return strings.Compare(left.sourceIdentity, right.sourceIdentity)
	})
	return tuples
}

// canonicalIncrementalHTTPEffectTuples renumbers inputIDs by semantic order for
// cross-service comparison: IDs are per-service allocation handles, so a warm
// service that retired and re-registered an input legitimately differs from a
// fresh oracle. Renumbering preserves ID distinctness, so aliasing two inputs
// or splitting one still fails the comparison.
func canonicalIncrementalHTTPEffectTuples(
	t *testing.T,
	service *RenderService,
) []authenticatedIncrementalHTTPEffectTuple {
	t.Helper()
	tuples := authenticatedIncrementalHTTPEffectTuples(t, service)
	slices.SortStableFunc(tuples, func(left, right authenticatedIncrementalHTTPEffectTuple) int {
		if order := strings.Compare(left.group, right.group); order != 0 {
			return order
		}
		if order := strings.Compare(left.url, right.url); order != 0 {
			return order
		}
		if order := strings.Compare(left.sourceIdentity, right.sourceIdentity); order != 0 {
			return order
		}
		if order := strings.Compare(left.content, right.content); order != 0 {
			return order
		}
		if left.revision != right.revision {
			if left.revision < right.revision {
				return -1
			}
			return 1
		}
		if left.inputID < right.inputID {
			return -1
		}
		if left.inputID > right.inputID {
			return 1
		}
		return 0
	})
	canonical := make(map[uint64]uint64, len(tuples))
	for index := range tuples {
		id, seen := canonical[tuples[index].inputID]
		if !seen {
			id = uint64(len(canonical) + 1)
			canonical[tuples[index].inputID] = id
		}
		tuples[index].inputID = id
	}
	return tuples
}

func assertIncrementalHTTPGroupWinner(
	t *testing.T,
	service *RenderService,
	inputID uint64,
	name string,
	contributors int,
) {
	t.Helper()
	component := service.incremental.components["routes"]
	index := service.incremental.snapshot.groupIndexes[component.group]
	require.NoError(t, index.validateAuthentication())
	owners, found := index.http.Get(incrementalHTTPIdentityKey(inputID))
	require.True(t, found)
	require.Equal(t, contributors, owners.Len())
	_, winner, found := owners.Root().Maximum()
	require.True(t, found)
	expected := incrementalGroupLocationKey(incrementalGroupInstanceID{
		component: component.name,
		source:    component.source,
		namespace: "default",
		name:      name,
	}, 0)
	assert.Equal(t, string(expected), winner.location)
	assert.Equal(t, inputID, winner.value.inputID)
}

func renderIncrementalHTTPTestResult(
	t *testing.T,
	service *RenderService,
	provider stores.StoreProvider,
) *RenderResult {
	t.Helper()
	result, err := service.Render(t.Context(), provider, rendercontext.RenderModeReconcile)
	require.NoError(t, err)
	require.NoError(t, result.InputTransaction.Commit(t.Context()))
	waitForIncrementalCache(t, service)
	return result
}

func promoteIncrementalHTTPBody(
	t *testing.T,
	component *controllerhttpstore.Component,
	url string,
) {
	t.Helper()
	version, err := component.GetStore().RefreshURLVersion(t.Context(), url)
	require.NoError(t, err)
	require.NotNil(t, version)
	require.True(t, component.GetStore().PromotePendingVersion(url, version.Checksum, version.Revision))
}

func incrementalHTTPFixtureEffect(
	t *testing.T,
	fixture *incrementalHTTPTestFixture,
	name string,
) incrementalHTTPEffect {
	t.Helper()
	component := fixture.service.incremental.components["routes"]
	effects, found := fixture.service.incremental.snapshot.httpEffects.Get(
		resultKey(&component, "routes", "default", name),
	)
	require.True(t, found)
	require.Equal(t, 1, effects.Len())
	_, effect, found := effects.Root().Minimum()
	require.True(t, found)
	return effect
}

func assertIncrementalHTTPFixtureEffect(
	t *testing.T,
	fixture *incrementalHTTPTestFixture,
	name string,
	want *incrementalHTTPEffect,
) {
	t.Helper()
	got := incrementalHTTPFixtureEffect(t, fixture, name)
	require.Equal(t, want.inputID, got.inputID)
	require.True(t, sameHTTPSnapshot(&want.snapshot, &got.snapshot))
}

func newIncrementalHTTPProofComponent(t *testing.T, maxAge time.Duration) *controllerhttpstore.Component {
	t.Helper()
	bus, logger := testutil.NewTestBusAndLogger()
	return controllerhttpstore.New(bus, logger, maxAge)
}

func newIncrementalHTTPNegativeProof(
	t *testing.T,
	component *controllerhttpstore.Component,
	url string,
	descriptor purehttpstore.SourceDescriptor,
) (*incrementalRenderSession, incremental.InputRevision) {
	t.Helper()
	snapshot, found := component.AcceptedSnapshot(url, descriptor)
	require.False(t, found)
	proof := snapshot.ObservationToken()
	require.True(t, proof.Valid())
	state := newHTTPRegistryTestState()
	spec, key, err := state.acquireHTTPInput(httpInputIdentity{url: url, descriptor: descriptor})
	require.NoError(t, err)
	t.Cleanup(func() {
		state.finishHTTPInputs(map[uint64]struct{}{spec.id: {}}, nil, nil, false)
	})
	input := incremental.Input{
		Key:      key,
		Revision: httpInputRevision(component.RevisionSource(), &snapshot),
		Found:    false,
	}
	return &incrementalRenderSession{
		state:          state,
		httpComponent:  component,
		httpObserved:   map[incremental.InputKey]incremental.Input{key: input},
		httpProofs:     map[incremental.InputKey]purehttpstore.ObservationToken{key: proof},
		membershipPins: map[string]incrementalStoreCursor{},
	}, incremental.InputRevision{Key: key, Revision: input.Revision, Found: false}
}

func incrementalHTTPRegistryURLs(state *incrementalRenderState) []string {
	state.httpMu.Lock()
	defer state.httpMu.Unlock()
	urls := make([]string, 0, len(state.httpIDs))
	for identity := range state.httpIDs {
		urls = append(urls, identity.url)
	}
	slices.Sort(urls)
	return urls
}

func newNonCriticalIncrementalHTTPService(
	t *testing.T,
	url string,
) (*RenderService, stores.StoreProvider, incremental.QueryKey) {
	t.Helper()
	cfg := &config.Config{
		Dataplane: testDataplaneConfig(),
		WatchedResources: map[string]config.WatchedResource{
			"routes": {
				APIVersion: "example.test/v1",
				Resources:  "routes",
				IndexBy:    []string{"metadata.namespace", "metadata.name"},
			},
		},
		TemplateSnippets: map[string]config.TemplateSnippet{
			"routes": {
				Name:        "routes",
				Requires:    []string{"routes"},
				Incremental: &config.IncrementalTemplate{Source: "routes"},
				Template: `{{ item | dig_string("", "metadata", "name") }}={{ http.Fetch(item | dig_string("", "spec", "url"), map[string]any{"retries": 1, "timeout": "1s"}) }}
`,
			},
		},
		HAProxyConfig: config.HAProxyConfig{Template: `{{ render "routes" }}`},
	}
	declarations := helpers.BuildAdditionalDeclarations(cfg, &typebootstrap.Result{
		Types: map[string]reflect.Type{}, Kinds: map[string]string{}, Errors: map[string]error{},
	})
	engine, err := helpers.NewEngineFromConfigWithOptions(cfg, nil, nil, declarations, helpers.EngineOptions{})
	require.NoError(t, err)
	bus, logger := testutil.NewTestBusAndLogger()
	httpComponent := controllerhttpstore.New(bus, logger, 0)
	service := NewRenderService(&RenderServiceConfig{
		Engine: engine, Config: cfg, Logger: logger, HTTPStoreComponent: httpComponent,
	})
	routes := k8sstore.NewMemoryStore(2)
	require.NoError(t, routes.Add(
		incrementalTestResource("default", "a", map[string]any{"url": url}),
		[]string{"default", "a"},
	))
	provider := stores.NewRealStoreProvider(map[string]stores.Store{"routes": routes})
	tempComponent51 := service.incremental.components["routes"]
	query := componentQueryKey(&tempComponent51, "routes", "default", "a")
	return service, provider, query
}
