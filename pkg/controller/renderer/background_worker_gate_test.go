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

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"

	"github.com/haproxy-haptic/HAPTIC/pkg/controller/rendercontext"
	"github.com/haproxy-haptic/HAPTIC/pkg/stores"
)

func TestBackgroundWorkerGateKeepsACPUPerAdmission(t *testing.T) {
	gate := newBackgroundWorkerGate(4)
	for range 3 {
		require.True(t, gate.TryAcquire())
	}
	assert.False(t, gate.TryAcquire(), "the batch's own first worker uses the fourth CPU")

	gate.Release()
	gate.Release()
	done := gate.admissionRendering()
	assert.True(t, gate.TryAcquire())
	assert.False(t, gate.TryAcquire(), "one CPU stays free for the rendering admission")

	done()
	assert.True(t, gate.TryAcquire())

	single := newBackgroundWorkerGate(1)
	assert.False(t, single.TryAcquire(), "one CPU leaves no extra workers")
}

// TestReconcileBatchesYieldToAdmission pins which renders are gated: a
// reconcile's batches give way to admissions, an admission's never do.
func TestReconcileBatchesYieldToAdmission(t *testing.T) {
	fixture := newActivationFixture(t)
	fixture.add(t, activationResource("route", "v1", true))
	fixture.renderAndCommit(t)
	fixture.engine.takeBatchGates()

	fixture.update(t, activationResource("route", "v2", true))
	fixture.renderAndCommit(t)
	reconcile := fixture.engine.takeBatchGates()
	require.NotEmpty(t, reconcile, "a warm reconcile renders the changed route in a batch")
	assert.NotContains(t, reconcile, false)

	admissionProvider := stores.NewOverlayStoreProvider(
		fixture.provider,
		stores.NewValidationContext(map[string]*stores.StoreOverlay{
			"routes": stores.NewStoreOverlayForUpdate(&unstructured.Unstructured{
				Object: activationResource("route", "v3", true),
			}),
		}),
	)
	renderActivation(t, false, fixture.service, admissionProvider, rendercontext.RenderModeAdmission,
		rendercontext.WithAdmissionSubject("routes", "default", "route"))
	admission := fixture.engine.takeBatchGates()
	require.NotEmpty(t, admission, "the admission renders the proposed route in a batch")
	assert.NotContains(t, admission, true)
}
