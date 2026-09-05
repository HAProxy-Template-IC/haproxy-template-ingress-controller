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

	"gitlab.com/haproxy-haptic/haptic/pkg/templating"
)

func TestRenderResultMaterializesDetachedEventSnapshot(t *testing.T) {
	collector := templating.NewEventCollector()
	require.NoError(t, collector.Register(
		"default", "route", "example.test/v1", "Route", templating.EventTypeWarning, "Conflict", "stable",
	))
	snapshot, err := collector.Snapshot()
	require.NoError(t, err)
	result := &RenderResult{EventSnapshot: snapshot}

	first, err := result.MaterializeEvents()
	require.NoError(t, err)
	first[0].Message = "poison"
	second, err := result.MaterializeEvents()
	require.NoError(t, err)
	assert.Equal(t, "stable", second[0].Message)

	result.Events = []templating.RenderedEvent{{Name: "mutable"}}
	_, err = result.MaterializeEvents()
	require.ErrorContains(t, err, "both mutable and immutable")
}

func TestRenderResultMaterializesDetachedRenderedResourceSnapshot(t *testing.T) {
	collector := templating.NewRenderedResourceCollector()
	require.NoError(t, collector.Register("v1", "ConfigMap", "default", "settings", map[string]any{
		"data": map[string]any{"value": "stable"},
	}))
	snapshot, err := collector.Snapshot()
	require.NoError(t, err)
	result := &RenderResult{RenderedResourceSnapshot: snapshot}

	first, err := result.MaterializeRenderedResources()
	require.NoError(t, err)
	first[0].Object["data"].(map[string]any)["value"] = "poison"
	second, err := result.MaterializeRenderedResources()
	require.NoError(t, err)
	assert.Equal(t, "stable", second[0].Object["data"].(map[string]any)["value"])

	result.RenderedResources = []templating.RenderedResource{{Name: "mutable"}}
	_, err = result.MaterializeRenderedResources()
	require.ErrorContains(t, err, "both mutable and immutable")
}

func TestRenderResultCompatibilityResourcesAreDeeplyDetached(t *testing.T) {
	result := &RenderResult{RenderedResources: []templating.RenderedResource{{
		APIVersion: "v1", Kind: "ConfigMap", Namespace: "default", Name: "settings",
		Object: map[string]any{"data": map[string]any{"value": "stable"}},
	}}}
	first, err := result.MaterializeRenderedResources()
	require.NoError(t, err)
	first[0].Object["data"].(map[string]any)["value"] = "poison"
	second, err := result.MaterializeRenderedResources()
	require.NoError(t, err)
	assert.Equal(t, "stable", second[0].Object["data"].(map[string]any)["value"])
}
