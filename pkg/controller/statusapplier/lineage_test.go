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

package statusapplier

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	"gitlab.com/haproxy-haptic/haptic/pkg/controller/testutil"
)

func TestApplyOnePatchDoesNotReplaySupersededWriteEcho(t *testing.T) {
	client := newFakeDynamicClientWithPatchSuccess()
	comp := newTestComponent(testutil.NewTestBus(), client, newTestResolver())
	patch := newTestPatches(map[string]map[string]any{"deployed": {"value": "first"}})[0]
	first := map[string]any{"value": "first"}
	second := map[string]any{"value": "second"}
	require.Equal(t, patchApplied, comp.applyOnePatch(context.Background(), &patch, first, "deployed"))
	firstVersion := comp.statusCache["default/my-ingress/networking.k8s.io/v1, Resource=ingresses"].latestResourceVersion
	require.Equal(t, patchApplied, comp.applyOnePatch(context.Background(), &patch, second, "deployed"))
	secondVersion := comp.statusCache["default/my-ingress/networking.k8s.io/v1, Resource=ingresses"].latestResourceVersion

	patch.ResourceVersion = firstVersion
	require.Equal(t, patchSkipped, comp.applyOnePatch(context.Background(), &patch, first, "deployed"))
	patch.ResourceVersion = secondVersion
	require.Equal(t, patchSkipped, comp.applyOnePatch(context.Background(), &patch, second, "deployed"))
	require.Len(t, client.Actions(), 2)

	patch.ResourceVersion = firstVersion
	require.Equal(t, patchApplied, comp.applyOnePatch(context.Background(), &patch, map[string]any{"value": "changed dependency"}, "deployed"))
}

func TestApplyOnePatchPreservesChangesFromCurrentBase(t *testing.T) {
	for _, intermediatePhase := range []string{"deployed", "deployFailed"} {
		t.Run(intermediatePhase, func(t *testing.T) {
			client := newFakeDynamicClientWithPatchSuccess()
			comp := newTestComponent(testutil.NewTestBus(), client, newTestResolver())
			patch := newTestPatches(map[string]map[string]any{"deployed": {"value": "ready"}})[0]
			ready := map[string]any{"value": "ready"}
			require.Equal(t, patchApplied, comp.applyOnePatch(t.Context(), &patch, ready, "deployed"))
			patch.ResourceVersion = comp.statusCache["default/my-ingress/networking.k8s.io/v1, Resource=ingresses"].latestResourceVersion
			require.Equal(t, patchApplied, comp.applyOnePatch(t.Context(), &patch, map[string]any{"value": "changed"}, intermediatePhase))
			require.Equal(t, patchApplied, comp.applyOnePatch(t.Context(), &patch, ready, "deployed"))
			require.Len(t, client.Actions(), 3)
		})
	}
}

func TestApplyOnePatchPreservesPhaseChangeFromOlderBase(t *testing.T) {
	client := newFakeDynamicClientWithPatchSuccess()
	comp := newTestComponent(testutil.NewTestBus(), client, newTestResolver())
	patch := newTestPatches(map[string]map[string]any{"deployed": {"value": "ready"}})[0]
	ready := map[string]any{"value": "ready"}
	require.Equal(t, patchApplied, comp.applyOnePatch(t.Context(), &patch, ready, "deployed"))
	firstVersion := comp.statusCache["default/my-ingress/networking.k8s.io/v1, Resource=ingresses"].latestResourceVersion
	patch.ResourceVersion = "external-update"
	require.Equal(t, patchApplied, comp.applyOnePatch(t.Context(), &patch, map[string]any{"value": "failed"}, "deployFailed"))
	patch.ResourceVersion = firstVersion
	require.Equal(t, patchApplied, comp.applyOnePatch(t.Context(), &patch, ready, "deployed"))
	require.Len(t, client.Actions(), 3)
}
