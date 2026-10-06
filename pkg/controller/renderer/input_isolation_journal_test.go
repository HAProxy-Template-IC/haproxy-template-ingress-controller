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
	"testing"

	"github.com/stretchr/testify/require"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"

	"gitlab.com/haproxy-haptic/haptic/pkg/controller/rendercontext"

	k8sstore "gitlab.com/haproxy-haptic/haptic/pkg/k8s/store"
	"gitlab.com/haproxy-haptic/haptic/pkg/stores"
)

func TestInputIsolationJournalSupportsBatchesWithoutAcceptingSiblingHistory(t *testing.T) {
	live := k8sstore.NewMemoryStore(2)
	advance := func(base *k8sstore.SnapshotBranch) *k8sstore.SnapshotBranch {
		snapshot, err := live.Pin()
		require.NoError(t, err)
		changes, err := base.Changes(t.Context(), snapshot, []string{"metadata.namespace", "metadata.name"})
		require.NoError(t, err)
		return base.Apply(changes)
	}
	add := func(name string) {
		require.NoError(t, live.Add(ingressBackendIngressResource(name, "echo", nil), []string{"default", name}))
	}
	add("a")
	initial := advance(k8sstore.NewSnapshotBranch(2))
	add("b")
	sibling := advance(initial)
	add("c")
	candidate := advance(initial)
	changes, complete := journalChangesThrough(candidate, initial.Sequence(), candidate.Sequence())
	require.True(t, complete, "a batch after an unselected sibling must preserve exact incremental proofs")
	require.Len(t, changes, 2)
	_, complete = journalChangesThrough(candidate, sibling.Sequence(), candidate.Sequence())
	require.False(t, complete, "a sibling cannot authorize an incremental proof")
	_, complete = journalChangesThrough(candidate, initial.Sequence(), sibling.Sequence())
	require.False(t, complete, "the end of a range must also belong to the selected history")
}

func isolatedChartBranches(t *testing.T) (fixture *ingressBackendChartFixture, capture func() map[string]*k8sstore.SnapshotBranch, publish func(map[string]*k8sstore.SnapshotBranch)) {
	t.Helper()
	fixture = newIngressBackendChartFixture(t)
	observed := fixture.provider
	branches := map[string]*k8sstore.SnapshotBranch{}
	for alias := range fixture.config.WatchedResources {
		branches[alias] = k8sstore.NewSnapshotBranch(len(fixture.config.WatchedResources[alias].IndexBy))
	}
	capture = func() map[string]*k8sstore.SnapshotBranch {
		selected := map[string]*k8sstore.SnapshotBranch{}
		for alias := range fixture.config.WatchedResources {
			snapshot, err := observed.GetStore(alias).(stores.SnapshotProvider).Pin()
			require.NoError(t, err)
			changes, err := branches[alias].Changes(t.Context(), snapshot, fixture.config.WatchedResources[alias].IndexBy)
			require.NoError(t, err)
			selected[alias] = branches[alias].Apply(changes)
		}
		return selected
	}
	publish = func(selected map[string]*k8sstore.SnapshotBranch) {
		branches = selected
		values := map[string]stores.Store{}
		for alias, branch := range branches {
			values[alias] = branch
		}
		fixture.provider = stores.NewRealStoreProvider(values)
	}
	return fixture, capture, publish
}

func TestInputIsolationBranchesPreserveWarmRendering(t *testing.T) {
	fixture, capture, publish := isolatedChartBranches(t)
	fixture.addService(t, sslPassthroughService("echo", "http", 80))
	fixture.addEndpoint(t, sslPassthroughEndpoint("echo", "http", 8080, "10.0.0.1"))
	fixture.addIngress(t, ingressBackendIngressResource("a", "echo", nil))
	fixture.addIngress(t, ingressBackendIngressResource("b", "echo", nil))
	publish(capture())
	fixture.renderAndCommit(t)
	fixture.addIngress(t, ingressBackendIngressResource("c", "echo", nil))
	_ = capture()
	fixture.addIngress(t, ingressBackendIngressResource("d", "echo", nil))
	publish(capture())
	updated := fixture.renderAndCommit(t)
	require.NotEqual(t, "cold", updated.CacheState, "a selected batch must reuse the existing render graph")
	require.Contains(t, updated.HAProxyConfig, "backend default_d_svc_echo_http")
	fixture.assertExecutionsForIngress(t, "a", 0)
	fixture.assertExecutionsForIngress(t, "b", 0)
}

func TestInputIsolationAdmissionRetainsWarmGraphAfterConcurrentAcceptance(t *testing.T) {
	fixture, capture, publish := isolatedChartBranches(t)
	fixture.addService(t, sslPassthroughService("echo", "http", 80))
	fixture.addEndpoint(t, sslPassthroughEndpoint("echo", "http", 8080, "10.0.0.1"))
	fixture.addIngress(t, ingressBackendIngressResource("stable", "echo", nil))
	publish(capture())
	fixture.renderAndCommit(t)
	fixture.addIngress(t, ingressBackendIngressResource("observed", "echo", nil))
	admissionBranches := capture()
	fixture.addIngress(t, ingressBackendIngressResource("later", "echo", nil))
	publish(capture())
	fixture.renderAndCommit(t)
	before := fixture.executions(ingressBackendComponent, "stable")
	values := map[string]stores.Store{}
	for alias, branch := range admissionBranches {
		values[alias] = branch
	}
	overlay := stores.NewOverlayStoreProvider(stores.NewRealStoreProvider(values), stores.NewValidationContext(map[string]*stores.StoreOverlay{
		"ingresses": stores.NewStoreOverlayForCreate(&unstructured.Unstructured{Object: ingressBackendIngressResource("proposed", "echo", nil)}),
	}))
	result, err := fixture.service.Render(t.Context(), overlay, rendercontext.RenderModeAdmission, rendercontext.WithAdmissionSubject("ingresses", "default", "proposed"))
	require.NoError(t, err)
	defer result.InputTransaction.Abort()
	require.NotEqual(t, "cold", result.CacheState, "admission must not rebuild every resource when reconciliation advances")
	require.Contains(t, result.HAProxyConfig, "backend default_proposed_svc_echo_http")
	require.Contains(t, result.HAProxyConfig, "backend default_observed_svc_echo_http")
	require.NotContains(t, result.HAProxyConfig, "backend default_later_svc_echo_http")
	require.Equal(t, before, fixture.executions(ingressBackendComponent, "stable"))
}
