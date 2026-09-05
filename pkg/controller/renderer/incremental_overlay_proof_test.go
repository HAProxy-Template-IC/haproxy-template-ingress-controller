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

	"gitlab.com/haproxy-haptic/haptic/pkg/controller/rendercontext"
	"gitlab.com/haproxy-haptic/haptic/pkg/incremental"
	"gitlab.com/haproxy-haptic/haptic/pkg/stores"
)

func TestAdmissionCommitAcceptsUnrelatedSameStoreMutation(t *testing.T) {
	fixture := newAmbientResourceFixture(t, false)
	assert.Equal(t, "route=v1\n", fixture.render(t))

	result := renderAmbientAdmission(t, fixture, fixture.provider)
	assert.Equal(t, "route=v1\n", result.HAProxyConfig)
	require.NoError(t, fixture.services.Add(
		incrementalTestResource("default", "unrelated", map[string]any{"value": "changed"}),
		[]string{"default", "unrelated"},
	))
	require.NoError(t, result.InputTransaction.Commit(t.Context()))
}

func TestAdmissionCommitRejectsVisibleSameStoreMutation(t *testing.T) {
	fixture := newAmbientResourceFixture(t, false)
	assert.Equal(t, "route=v1\n", fixture.render(t))

	result := renderAmbientAdmission(t, fixture, fixture.provider)
	assert.Equal(t, "route=v1\n", result.HAProxyConfig)
	require.NoError(t, fixture.services.Update(
		incrementalTestResource("default", "service", map[string]any{"value": "v2"}),
		[]string{"default", "service"},
	))
	require.ErrorIs(t, result.InputTransaction.Commit(t.Context()), incremental.ErrRevisionConflict)
}

func TestAdmissionOverlayCommitAcceptsUnrelatedSameStoreMutation(t *testing.T) {
	fixture := newAmbientResourceFixture(t, false)
	assert.Equal(t, "route=v1\n", fixture.render(t))

	provider := ambientOverlayProvider(fixture, stores.NewStoreOverlayForUpdate(
		&unstructured.Unstructured{Object: incrementalTestResource(
			"default", "service", map[string]any{"value": "proposed"},
		)},
	))
	result := renderAmbientAdmission(t, fixture, provider)
	assert.Equal(t, "route=proposed\n", result.HAProxyConfig)
	require.NoError(t, fixture.services.Add(
		incrementalTestResource("default", "unrelated", map[string]any{"value": "changed"}),
		[]string{"default", "unrelated"},
	))
	require.NoError(t, result.InputTransaction.Commit(t.Context()))
	assert.Equal(t, "route=v1\n", fixture.render(t))
}

func TestAdmissionOverlayCommitRejectsVisibleSameStoreMutation(t *testing.T) {
	fixture := newAmbientResourceFixture(t, false)
	assert.Equal(t, "route=v1\n", fixture.render(t))

	provider := ambientOverlayProvider(fixture, stores.NewStoreOverlayForCreate(
		&unstructured.Unstructured{Object: incrementalTestResource(
			"default", "proposal", map[string]any{"value": "candidate"},
		)},
	))
	result := renderAmbientAdmission(t, fixture, provider)
	assert.Equal(t, "route=v1\n", result.HAProxyConfig)
	require.NoError(t, fixture.services.Update(
		incrementalTestResource("default", "service", map[string]any{"value": "v2"}),
		[]string{"default", "service"},
	))
	require.ErrorIs(t, result.InputTransaction.Commit(t.Context()), incremental.ErrRevisionConflict)
}

func TestAdmissionOverlayCommitAcceptsMutationMaskedByOverlay(t *testing.T) {
	fixture := newAmbientResourceFixture(t, false)
	assert.Equal(t, "route=v1\n", fixture.render(t))

	provider := ambientOverlayProvider(fixture, stores.NewStoreOverlayForUpdate(
		&unstructured.Unstructured{Object: incrementalTestResource(
			"default", "service", map[string]any{"value": "proposed"},
		)},
	))
	result := renderAmbientAdmission(t, fixture, provider)
	assert.Equal(t, "route=proposed\n", result.HAProxyConfig)
	require.NoError(t, fixture.services.Update(
		incrementalTestResource("default", "service", map[string]any{"value": "v2"}),
		[]string{"default", "service"},
	))
	require.NoError(t, result.InputTransaction.Commit(t.Context()))
	assert.Equal(t, "route=v2\n", fixture.render(t))
}

func TestAdmissionSourceOverlayCommitAcceptsMaskedSourceMutation(t *testing.T) {
	fixture := newAmbientResourceFixture(t, false)
	assert.Equal(t, "route=v1\n", fixture.render(t))

	provider := stores.NewOverlayStoreProvider(
		fixture.provider,
		stores.NewValidationContext(map[string]*stores.StoreOverlay{
			"routes": stores.NewStoreOverlayForUpdate(&unstructured.Unstructured{Object: incrementalTestResource(
				"default", "route", map[string]any{"backend": "proposal"},
			)}),
		}),
	)
	result := renderAmbientAdmission(t, fixture, provider)
	assert.Equal(t, "route=<missing>\n", result.HAProxyConfig)
	require.NoError(t, fixture.routes.Update(
		incrementalTestResource("default", "route", map[string]any{"backend": "live"}),
		[]string{"default", "route"},
	))
	require.NoError(t, result.InputTransaction.Commit(t.Context()))
	assert.Equal(t, "route=<missing>\n", fixture.render(t))
}

func TestAdmissionSourceOverlayCommitRejectsMembershipMutation(t *testing.T) {
	fixture := newAmbientResourceFixture(t, false)
	assert.Equal(t, "route=v1\n", fixture.render(t))

	provider := stores.NewOverlayStoreProvider(
		fixture.provider,
		stores.NewValidationContext(map[string]*stores.StoreOverlay{
			"routes": stores.NewStoreOverlayForUpdate(&unstructured.Unstructured{Object: incrementalTestResource(
				"default", "route", map[string]any{"backend": "service"},
			)}),
		}),
	)
	result := renderAmbientAdmission(t, fixture, provider)
	assert.Equal(t, "route=v1\n", result.HAProxyConfig)
	require.NoError(t, fixture.routes.Add(
		incrementalTestResource("default", "added", map[string]any{"backend": "service"}),
		[]string{"default", "added"},
	))
	require.ErrorIs(t, result.InputTransaction.Commit(t.Context()), incremental.ErrRevisionConflict)
}

func ambientOverlayProvider(
	fixture *ambientResourceFixture,
	overlay *stores.StoreOverlay,
) stores.StoreProvider {
	return stores.NewOverlayStoreProvider(
		fixture.provider,
		stores.NewValidationContext(map[string]*stores.StoreOverlay{"services": overlay}),
	)
}

func renderAmbientAdmission(
	t *testing.T,
	fixture *ambientResourceFixture,
	provider stores.StoreProvider,
) *RenderResult {
	t.Helper()
	result, err := fixture.service.Render(t.Context(), provider, rendercontext.RenderModeAdmission)
	require.NoError(t, err)
	require.NotNil(t, result.InputTransaction)
	return result
}
