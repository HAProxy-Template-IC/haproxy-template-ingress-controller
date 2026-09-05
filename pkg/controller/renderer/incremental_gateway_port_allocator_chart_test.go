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
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"

	"gitlab.com/haproxy-haptic/haptic/pkg/controller/rendercontext"
	"gitlab.com/haproxy-haptic/haptic/pkg/stores"
)

const (
	gatewayPodPortCandidateComponent  = "gateway-pod-port-candidates-100-gateway"
	gatewayPodPortAllocationComponent = "gateway-pod-port-allocations-200-leader"
	gatewayHostPortScopeComponent     = "gateway-host-port-scopes-100-gateway"
)

func TestGatewayPodPortMetadataOnlyExecutionScaling(t *testing.T) {
	for _, gatewayCount := range []int{300, 1000, 3000} {
		t.Run(fmt.Sprintf("gateways=%d", gatewayCount), func(t *testing.T) {
			fixture := newGatewayHostMapFixture(t)
			fixture.config.TemplatingSettings.ExtraContext["perGatewayPodPortRange"] = 10000
			for index := range gatewayCount {
				fixture.addGateway(t, gatewayHostMapGateway(
					fmt.Sprintf("gateway-%06d", index),
					fmt.Sprintf("2026-01-01T00:%02d:%02dZ", (index/60)%60, index%60),
					"", 80,
				))
			}

			cold := fixture.renderAndCommitCacheReady(t)
			last := fmt.Sprintf("gateway-%06d", gatewayCount-1)
			for _, component := range []string{
				gatewayPodPortCandidateComponent,
				gatewayPodPortAllocationComponent,
				gatewayHostPortScopeComponent,
			} {
				assert.Equal(t, uint64(1), fixture.executions(component, "gateways", last))
			}

			warm := fixture.renderAndCommitCacheReady(t)
			assert.Equal(t, cold.HAProxyConfig, warm.HAProxyConfig)
			for _, component := range []string{
				gatewayPodPortCandidateComponent,
				gatewayPodPortAllocationComponent,
				gatewayHostPortScopeComponent,
			} {
				assert.Equal(t, uint64(1), fixture.executions(component, "gateways", last))
			}

			updated := gatewayHostMapGateway(last,
				fmt.Sprintf("2026-01-01T00:%02d:%02dZ", ((gatewayCount-1)/60)%60, (gatewayCount-1)%60),
				"", 80)
			updated["metadata"].(map[string]any)["annotations"] = map[string]any{"test.haptic/value": "changed"}
			require.NoError(t, fixture.gateways.Update(updated, []string{"default", last}))
			changed := fixture.renderAndCommitCacheReady(t)
			assert.Equal(t, cold.HAProxyConfig, changed.HAProxyConfig)
			for _, component := range []string{
				gatewayPodPortCandidateComponent,
				gatewayPodPortAllocationComponent,
				gatewayHostPortScopeComponent,
			} {
				assert.Equal(t, uint64(2), fixture.executions(component, "gateways", last))
			}
			assert.Equal(t, uint64(1), fixture.executions(
				gatewayPodPortCandidateComponent, "gateways", "gateway-000000"))
			assert.Equal(t, uint64(1), fixture.executions(
				gatewayHostPortScopeComponent, "gateways", "gateway-000000"))
		})
	}
}

func TestGatewayPodPortDeletionAdmissionAndAbortStayExact(t *testing.T) {
	fixture := newGatewayHostMapFixture(t)
	fixture.addGateway(t, gatewayHostMapGateway("old", "2026-01-01T00:00:00Z", "", 80))
	fixture.addGateway(t, gatewayHostMapGateway("new", "2026-01-02T00:00:00Z", "", 80))
	live := fixture.renderAndCommitCacheReady(t)

	proposed := gatewayHostMapGateway("new", "2026-01-02T00:00:00Z", "", 80)
	proposed["metadata"].(map[string]any)["annotations"] = map[string]any{"test.haptic/value": "admission"}
	committed := fixture.service.incremental.snapshot
	overlay := stores.NewOverlayStoreProvider(fixture.provider, stores.NewValidationContext(
		map[string]*stores.StoreOverlay{"gateways": stores.NewStoreOverlayForUpdate(
			&unstructured.Unstructured{Object: proposed})},
	))
	admission, err := fixture.service.Render(t.Context(), overlay, rendercontext.RenderModeAdmission,
		rendercontext.WithAdmissionSubject("gateways", "default", "new"))
	require.NoError(t, err)
	assert.Equal(t, live.HAProxyConfig, admission.HAProxyConfig)
	require.NoError(t, admission.InputTransaction.Commit(t.Context()))
	assert.Same(t, committed, fixture.service.incremental.snapshot)

	failedUpdate := gatewayHostMapGateway("new", "2026-01-02T00:00:00Z", "", 80)
	failedUpdate["metadata"].(map[string]any)["annotations"] = map[string]any{"test.haptic/value": "abort"}
	require.NoError(t, fixture.gateways.Update(failedUpdate, []string{"default", "new"}))
	fixture.config.TemplatingSettings.ExtraContext["failAfterHostMap"] = true
	failed, err := fixture.service.Render(t.Context(), fixture.provider, rendercontext.RenderModeReconcile)
	require.ErrorContains(t, err, "forced failure after gateway host map")
	assert.Nil(t, failed)
	assert.Same(t, committed, fixture.service.incremental.snapshot)
	fixture.config.TemplatingSettings.ExtraContext["failAfterHostMap"] = false
	assert.Equal(t, live.HAProxyConfig, fixture.renderAndCommitCacheReady(t).HAProxyConfig)

	oldLeaderExecutions := fixture.executions(
		gatewayPodPortAllocationComponent, "gateways", "old")
	fixture.deleteGateway(t, "new")
	deleted := fixture.renderAndCommitCacheReady(t)
	assert.Equal(t, live.HAProxyConfig, deleted.HAProxyConfig)
	assert.Equal(t, oldLeaderExecutions+1, fixture.executions(
		gatewayPodPortAllocationComponent, "gateways", "old"))
}
