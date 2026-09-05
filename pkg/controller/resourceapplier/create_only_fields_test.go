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

package resourceapplier

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	dynamicfake "k8s.io/client-go/dynamic/fake"

	"gitlab.com/haproxy-haptic/haptic/pkg/templating"
)

var stsGVR = schema.GroupVersionResource{Group: "apps", Version: "v1", Resource: "statefulsets"}

func createOnlyObject() map[string]any {
	return map[string]any{
		"apiVersion": "apps/v1",
		"kind":       "StatefulSet",
		"metadata":   map[string]any{"name": "store", "namespace": "haptic"},
		"spec":       map[string]any{"replicas": int64(3), "serviceName": "store"},
	}
}

func createOnlyComponent(t *testing.T, existing ...runtime.Object) *Component {
	t.Helper()
	scheme := runtime.NewScheme()
	scheme.AddKnownTypeWithName(
		schema.GroupVersionKind{Group: "apps", Version: "v1", Kind: "StatefulSetList"},
		&unstructured.UnstructuredList{},
	)
	return &Component{dynamicClient: dynamicfake.NewSimpleDynamicClient(scheme, existing...)}
}

// The count is the size the object starts at. Re-sending the template's value
// on every reconcile is what overwrote an operator's `kubectl scale` a second
// after it was made, so once the object exists the apply carries the value the
// object already has.
func TestCreateOnlyFieldsFollowTheLiveObject(t *testing.T) {
	scaled := createOnlyObject()
	scaled["spec"].(map[string]any)["replicas"] = int64(0)
	component := createOnlyComponent(t, &unstructured.Unstructured{Object: scaled})
	object := createOnlyObject()

	applied, err := component.withCreateOnlyFieldsFromLive(
		t.Context(), stsGVR,
		&templating.RenderedResource{
			Namespace: "haptic", Name: "store", CreateOnlyFields: []string{"spec.replicas"},
		},
		object,
	)
	require.NoError(t, err)

	spec := applied["spec"].(map[string]any)
	assert.Equal(t, int64(0), spec["replicas"],
		"an operator's scale must be read back and re-applied, not overwritten")
	assert.Equal(t, "store", spec["serviceName"], "every other field still comes from the template")

	// prepareForApply copies only the top level, so the nested maps belong to
	// the render cache. Stripping in place emptied the field out of the cached
	// object for good and the workload came back at its kind's default of one.
	assert.Equal(t, int64(3), object["spec"].(map[string]any)["replicas"],
		"the rendered object the render cache holds must not be mutated")
}

// On creation the template's value is the one that lands, so a fresh install
// gets the size the chart ships rather than the kind's default.
func TestCreateOnlyFieldsSurviveWhenTheObjectIsAbsent(t *testing.T) {
	component := createOnlyComponent(t)
	object := createOnlyObject()

	kept, err := component.withCreateOnlyFieldsFromLive(
		t.Context(), stsGVR,
		&templating.RenderedResource{
			Namespace: "haptic", Name: "store", CreateOnlyFields: []string{"spec.replicas"},
		},
		object,
	)
	require.NoError(t, err)

	assert.Equal(t, int64(3), kept["spec"].(map[string]any)["replicas"])
}

func TestCreateOnlyFieldsRejectAMalformedPath(t *testing.T) {
	live := &unstructured.Unstructured{Object: createOnlyObject()}
	component := createOnlyComponent(t, live)

	_, err := component.withCreateOnlyFieldsFromLive(
		t.Context(), stsGVR,
		&templating.RenderedResource{
			Namespace: "haptic", Name: "store", CreateOnlyFields: []string{"spec..replicas"},
		},
		createOnlyObject(),
	)
	require.ErrorContains(t, err, "not a dotted field path")
}

// The declaration has to survive a round trip through the collector. A render
// result's resources are re-registered before they reach the applier, and a
// Register call that forgot to carry the paths dropped them silently: every
// unit test still passed, and on a live cluster `spec.replicas` was re-applied
// exactly as before.
func TestCreateOnlyFieldsSurviveReRegistration(t *testing.T) {
	collector := templating.NewRenderedResourceCollector()
	require.NoError(t, collector.RegisterWithCreateOnlyFields(
		"apps/v1", "StatefulSet", "haptic", "store", createOnlyObject(), []string{"spec.replicas"},
	))
	first := collector.Resources()
	require.Len(t, first, 1)
	require.Equal(t, []string{"spec.replicas"}, first[0].CreateOnlyFields)

	again := templating.NewRenderedResourceCollector()
	require.NoError(t, again.RegisterWithCreateOnlyFields(
		first[0].APIVersion, first[0].Kind, first[0].Namespace, first[0].Name,
		first[0].Object, first[0].CreateOnlyFields,
	))
	second := again.Resources()
	require.Len(t, second, 1)
	assert.Equal(t, []string{"spec.replicas"}, second[0].CreateOnlyFields,
		"a re-registered resource must keep the paths its configuration declared")
}

// The snapshot is the path production takes, and it rebuilt RenderedResource
// field by field — so it silently dropped the declaration while the collector
// round-trip test above still passed. Both layers need pinning: the applier is
// only ever as correct as what reaches it.
func TestCreateOnlyFieldsSurviveTheSnapshot(t *testing.T) {
	collector := templating.NewRenderedResourceCollector()
	require.NoError(t, collector.RegisterWithCreateOnlyFields(
		"apps/v1", "StatefulSet", "haptic", "store", createOnlyObject(), []string{"spec.replicas"},
	))

	snapshot, err := collector.Snapshot(nil)
	require.NoError(t, err)
	resources, err := snapshot.Resources()
	require.NoError(t, err)
	require.Len(t, resources, 1)
	assert.Equal(t, []string{"spec.replicas"}, resources[0].CreateOnlyFields,
		"the snapshot must carry the paths the configuration declared")
}
