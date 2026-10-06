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
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/require"

	"gitlab.com/haproxy-haptic/haptic/pkg/controller/events"
	controllerhttpstore "gitlab.com/haproxy-haptic/haptic/pkg/controller/httpstore"
	"gitlab.com/haproxy-haptic/haptic/pkg/controller/rendercontext"
	"gitlab.com/haproxy-haptic/haptic/pkg/controller/testutil"
	purehttpstore "gitlab.com/haproxy-haptic/haptic/pkg/httpstore"
	k8sstore "gitlab.com/haproxy-haptic/haptic/pkg/k8s/store"
)

// A render that withholds new content stays on the warm graph and renders the
// pending source as unavailable; the next render fetches and accepts it.
func TestWithheldCandidateRendersWarmWithoutTheContent(t *testing.T) {
	pages := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("page"))
	}))
	t.Cleanup(pages.Close)
	service, provider, _ := newNonCriticalIncrementalHTTPService(t, pages.URL+"/a", false)
	renderAndCommitIncrementalCacheReady(t, service, provider)
	renderAndCommitIncrementalCacheReady(t, service, provider)
	routes, ok := provider.GetStore("routes").(*k8sstore.MemoryStore)
	require.True(t, ok)
	require.NoError(t, routes.Add(
		incrementalTestResource("default", "b", map[string]any{"url": pages.URL + "/b"}),
		[]string{"default", "b"},
	))

	withheld, err := service.Render(controllerhttpstore.WithCandidatesWithheld(t.Context()), provider,
		rendercontext.RenderModeReconcile)
	require.NoError(t, err)
	require.Equal(t, "warm", withheld.CacheState)
	require.False(t, withheld.InputTransaction.HasCandidates())
	require.Contains(t, withheld.HAProxyConfig, "a=page")
	require.Contains(t, withheld.HAProxyConfig, "b=\n")
	require.NoError(t, withheld.InputTransaction.Commit(t.Context()))
	_, accepted := service.httpStoreComponent.GetStore().Get(pages.URL + "/b")
	require.False(t, accepted)

	accepting, err := service.Render(t.Context(), provider, rendercontext.RenderModeReconcile)
	require.NoError(t, err)
	require.True(t, accepting.InputTransaction.HasCandidates())
	require.Contains(t, accepting.HAProxyConfig, "b=page")
	require.NoError(t, accepting.InputTransaction.Commit(t.Context()))
	_, accepted = service.httpStoreComponent.GetStore().Get(pages.URL + "/b")
	require.True(t, accepted)
	require.NoError(t, service.RetireIncrementalCache())
}

// Two cold first renders, like the leader's and the render warmer's on one
// replica: the newer one owns the cache build, so the older one commits its
// HTTP candidates without the cache instead of failing on a cache conflict.
// They used to abort each other until one gave up.
func TestColdCacheRaceLoserStillAcceptsItsCandidates(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("body"))
	}))
	t.Cleanup(server.Close)
	service, provider, _ := newNonCriticalIncrementalHTTPService(t, server.URL, true)

	older, err := service.Render(t.Context(), provider, rendercontext.RenderModeReconcile)
	require.NoError(t, err)
	newer, err := service.Render(t.Context(), provider, rendercontext.RenderModeReconcile)
	require.NoError(t, err)
	require.True(t, older.InputTransaction.HasCandidates())
	require.True(t, newer.InputTransaction.HasCandidates())

	require.NoError(t, older.InputTransaction.Commit(t.Context()))
	_, accepted := service.httpStoreComponent.GetStore().Get(server.URL)
	require.True(t, accepted)
	require.ErrorIs(t, newer.InputTransaction.Commit(t.Context()), purehttpstore.ErrInputsMoved)

	settled, err := service.Render(t.Context(), provider, rendercontext.RenderModeReconcile)
	require.NoError(t, err)
	require.False(t, settled.InputTransaction.HasCandidates())
	require.NoError(t, settled.InputTransaction.Commit(t.Context()))
	waitForIncrementalCache(t, service)
	require.NoError(t, service.RetireIncrementalCache())
}

// seedWarmCacheWithoutRouteB leaves a published graph cache that holds only
// route a, so adding route b makes the next render fetch an initial candidate.
func seedWarmCacheWithoutRouteB(t *testing.T) (*incrementalHTTPTestFixture, *k8sstore.MemoryStore) {
	t.Helper()
	fixture := newIncrementalHTTPTestFixture(t)
	routes, ok := fixture.provider.GetStore("routes").(*k8sstore.MemoryStore)
	require.True(t, ok)
	seedExactCycleForceColdGraphCandidate(t, fixture, routes)
	require.NoError(t, routes.Add(
		incrementalTestResource("default", "b", map[string]any{"url": fixture.urlB}),
		[]string{"default", "b"},
	))
	return fixture, routes
}

func (f *incrementalHTTPTestFixture) acceptedB() bool {
	_, accepted := f.httpComponent.GetStore().Get(f.urlB)
	return accepted
}

// A render that reads an initial HTTP candidate stays on the warm graph and
// only withholds its cache. A cold restart made acceptance need a cold render
// with no input change for its whole duration, which churn never grants (#276).
func TestInitialHTTPCandidateRendersThroughWarmGraph(t *testing.T) {
	fixture, _ := seedWarmCacheWithoutRouteB(t)

	candidate, err := fixture.service.Render(t.Context(), fixture.provider, rendercontext.RenderModeReconcile)
	require.NoError(t, err)
	require.True(t, candidate.InputTransaction.HasCandidates())
	require.Equal(t, "warm", candidate.CacheState, "the candidate render restarted cold")
	require.Contains(t, candidate.HAProxyConfig, "a=first")
	require.Contains(t, candidate.HAProxyConfig, "b=stable")
	require.NoError(t, candidate.InputTransaction.Commit(t.Context()))
	require.True(t, fixture.acceptedB())
	trigger := testutil.WaitForEvent[*events.ReconciliationTriggeredEvent](t, fixture.triggers, testutil.EventTimeout)
	require.Equal(t, "http_content_accepted", trigger.Reason, "nothing asks for the render that publishes the withheld cache")
	if published := fixture.service.exactCycleCandidate; published != nil {
		require.NotEqual(t, exactCycleCandidateOutputOnly, published.mode,
			"only a full cold render publishes an output-only successor")
	}

	accepted, err := fixture.service.Render(t.Context(), fixture.provider, rendercontext.RenderModeReconcile)
	require.NoError(t, err)
	require.False(t, accepted.InputTransaction.HasCandidates())
	require.Contains(t, accepted.HAProxyConfig, "b=stable")
	require.NoError(t, accepted.InputTransaction.Commit(t.Context()))
	waitForIncrementalCache(t, fixture.service)
	require.True(t, fixture.httpComponent.GetStore().HasActiveLease(fixture.urlB))
	require.NoError(t, fixture.service.RetireIncrementalCache())
}

// The warm candidate render accepts against its own snapshot: an input it read
// that changes before the commit does not refuse the acceptance, and the next
// render reads the moved input with the accepted content (ADR-0030).
func TestInitialHTTPCandidateWarmRenderAcceptsAgainstItsSnapshot(t *testing.T) {
	fixture, routes := seedWarmCacheWithoutRouteB(t)

	candidate, err := fixture.service.Render(t.Context(), fixture.provider, rendercontext.RenderModeReconcile)
	require.NoError(t, err)
	require.True(t, candidate.InputTransaction.HasCandidates())
	require.Equal(t, "warm", candidate.CacheState)
	require.NoError(t, routes.Update(
		incrementalTestResource("default", "b", map[string]any{"url": fixture.urlB, "noise": "moved"}),
		[]string{"default", "b"},
	))
	require.NoError(t, candidate.InputTransaction.Commit(t.Context()))
	require.True(t, fixture.acceptedB())

	next, err := fixture.service.Render(t.Context(), fixture.provider, rendercontext.RenderModeReconcile)
	require.NoError(t, err)
	require.False(t, next.InputTransaction.HasCandidates())
	require.NoError(t, next.InputTransaction.Commit(t.Context()))
	require.NoError(t, fixture.service.RetireIncrementalCache())
}

// A lost commit releases its lease changes with it: the HTTP store keeps the
// accounting of the last committed render, so the next render's removals
// match what the store holds. A conflicting render that read no candidate is
// therefore safe to deploy without its commit.
func TestAbortedLeaseChangeLeavesAccountingConsistent(t *testing.T) {
	fixture := newIncrementalHTTPTestFixture(t)
	routes, ok := fixture.provider.GetStore("routes").(*k8sstore.MemoryStore)
	require.True(t, ok)
	fixture.render(t)
	fixture.render(t)
	require.True(t, fixture.httpComponent.GetStore().HasActiveLease(fixture.urlB))

	require.NoError(t, routes.Delete("default", "b", []string{"default", "b"}))
	lost, err := fixture.service.Render(t.Context(), fixture.provider, rendercontext.RenderModeReconcile)
	require.NoError(t, err)
	require.False(t, lost.InputTransaction.HasCandidates())
	lost.InputTransaction.Abort()
	require.True(t, fixture.httpComponent.GetStore().HasActiveLease(fixture.urlB))

	require.NotContains(t, fixture.render(t), "b=")
	require.False(t, fixture.httpComponent.GetStore().HasActiveLease(fixture.urlB))

	require.NoError(t, routes.Add(
		incrementalTestResource("default", "b", map[string]any{"url": fixture.urlB}),
		[]string{"default", "b"},
	))
	require.Contains(t, fixture.render(t), "b=stable")
	require.True(t, fixture.httpComponent.GetStore().HasActiveLease(fixture.urlB))
	require.NoError(t, fixture.service.RetireIncrementalCache())
}
