// Copyright 2025 Philipp Hossner
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

package typebootstrap

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"k8s.io/apimachinery/pkg/runtime/schema"
)

// TestOfflineGVKResolver_DefaultEntries pins what the controller
// ships with out of the box. Adding a new bundled schema requires
// adding to both this list and the NewOfflineGVKResolver constructor;
// keeping the test in sync keeps the two surfaces honest about what's
// supported offline.
func TestOfflineGVKResolver_DefaultEntries(t *testing.T) {
	r := NewOfflineGVKResolver()

	gvk, err := r.Resolve(groupGatewayAPI+"/v1", "gateways")
	require.NoError(t, err)
	assert.Equal(t,
		schema.GroupVersionKind{Group: groupGatewayAPI, Version: "v1", Kind: kindGateway},
		gvk)
}

// TestOfflineGVKResolver_UnknownReturnsHelpfulError pins the
// degradation path. Resources not in the builtin table fail with an
// error message that points at the exact two places a contributor
// has to touch to add support, so a chart author hitting this for
// the first time isn't left guessing.
func TestOfflineGVKResolver_UnknownReturnsHelpfulError(t *testing.T) {
	r := NewOfflineGVKResolver()

	_, err := r.Resolve("custom.example.com/v1", "widgets")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "pkg/k8s/schemafetcher/builtin",
		"error must direct contributors to the schemas directory")
	assert.Contains(t, err.Error(), "NewOfflineGVKResolver",
		"error must direct contributors to the GVK registration function")
}

// TestOfflineGVKResolver_RegisterOverrides verifies test-side
// extensibility. Tests that need a synthetic GVK (e.g., the chart
// fixtures for a CRD that isn't bundled) can Register their own
// entries without modifying production code.
func TestOfflineGVKResolver_RegisterOverrides(t *testing.T) {
	r := NewOfflineGVKResolver().Register(
		"example.com/v1", "widgets",
		schema.GroupVersionKind{Group: "example.com", Version: "v1", Kind: "Widget"})

	gvk, err := r.Resolve("example.com/v1", "widgets")
	require.NoError(t, err)
	assert.Equal(t, "Widget", gvk.Kind)
}
