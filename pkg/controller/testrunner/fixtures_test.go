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

package testrunner

import (
	"context"
	"log/slog"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"

	"gitlab.com/haproxy-haptic/haptic/pkg/controller/rendercontext"
	"gitlab.com/haproxy-haptic/haptic/pkg/core/config"
	"gitlab.com/haproxy-haptic/haptic/pkg/dataplane"
	"gitlab.com/haproxy-haptic/haptic/pkg/dataplane/auxiliaryfiles"
	"gitlab.com/haproxy-haptic/haptic/pkg/stores"
	"gitlab.com/haproxy-haptic/haptic/pkg/templating"
)

// =============================================================================
// collectResourceTypes Tests
// =============================================================================

func TestCollectResourceTypes(t *testing.T) {
	tests := []struct {
		name        string
		fixtureMaps []map[string][]interface{}
		wantTypes   map[string]bool
	}{
		{
			name:        "empty input",
			fixtureMaps: []map[string][]interface{}{},
			wantTypes:   map[string]bool{},
		},
		{
			name: "single fixture map",
			fixtureMaps: []map[string][]interface{}{
				{
					"ingresses": {},
					"services":  {},
				},
			},
			wantTypes: map[string]bool{
				"ingresses": true,
				"services":  true,
			},
		},
		{
			name: "multiple fixture maps with overlap",
			fixtureMaps: []map[string][]interface{}{
				{
					"ingresses": {},
					"services":  {},
				},
				{
					"services": {},
					"secrets":  {},
				},
			},
			wantTypes: map[string]bool{
				"ingresses": true,
				"services":  true,
				"secrets":   true,
			},
		},
		{
			name: "nil maps are handled",
			fixtureMaps: []map[string][]interface{}{
				nil,
				{
					"ingresses": {},
				},
			},
			wantTypes: map[string]bool{
				"ingresses": true,
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := collectResourceTypes(tt.fixtureMaps...)
			assert.Equal(t, tt.wantTypes, result)
		})
	}
}

// =============================================================================
// buildResourceIdentity Tests
// =============================================================================

func TestBuildResourceIdentity(t *testing.T) {
	tests := []struct {
		name         string
		resourceType string
		resourceMap  map[string]interface{}
		wantIdentity string
	}{
		{
			name:         "basic resource",
			resourceType: "ingresses",
			resourceMap: map[string]interface{}{
				"apiVersion": "networking.k8s.io/v1",
				"kind":       "Ingress",
				"metadata": map[string]interface{}{
					"name":      "my-ingress",
					"namespace": "default",
				},
			},
			wantIdentity: "ingresses|networking.k8s.io/v1|Ingress|default|my-ingress",
		},
		{
			name:         "cluster-scoped resource",
			resourceType: "clusterroles",
			resourceMap: map[string]interface{}{
				"apiVersion": "rbac.authorization.k8s.io/v1",
				"kind":       "ClusterRole",
				"metadata": map[string]interface{}{
					"name": "admin",
				},
			},
			wantIdentity: "clusterroles|rbac.authorization.k8s.io/v1|ClusterRole||admin",
		},
		{
			name:         "resource with empty metadata",
			resourceType: "services",
			resourceMap: map[string]interface{}{
				"apiVersion": "v1",
				"kind":       "Service",
				"metadata":   map[string]interface{}{},
			},
			wantIdentity: "services|v1|Service||",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			resource := &unstructured.Unstructured{Object: tt.resourceMap}
			result := buildResourceIdentity(tt.resourceType, resource)
			assert.Equal(t, tt.wantIdentity, result)
		})
	}
}

// =============================================================================
// mergeResourceType Tests
// =============================================================================

func TestMergeResourceType(t *testing.T) {
	tests := []struct {
		name            string
		resourceType    string
		globalResources []interface{}
		testResources   []interface{}
		testIdentities  map[string]interface{}
		wantCount       int
	}{
		{
			name:            "empty global and test",
			resourceType:    "ingresses",
			globalResources: nil,
			testResources:   nil,
			testIdentities:  map[string]interface{}{},
			wantCount:       0,
		},
		{
			name:         "only global resources",
			resourceType: "ingresses",
			globalResources: []interface{}{
				map[string]interface{}{
					"apiVersion": "networking.k8s.io/v1",
					"kind":       "Ingress",
					"metadata": map[string]interface{}{
						"name":      "global-ingress",
						"namespace": "default",
					},
				},
			},
			testResources:  nil,
			testIdentities: map[string]interface{}{},
			wantCount:      1,
		},
		{
			name:            "only test resources",
			resourceType:    "services",
			globalResources: nil,
			testResources: []interface{}{
				map[string]interface{}{
					"apiVersion": "v1",
					"kind":       "Service",
					"metadata": map[string]interface{}{
						"name":      "test-service",
						"namespace": "default",
					},
				},
			},
			testIdentities: map[string]interface{}{},
			wantCount:      1,
		},
		{
			name:         "test overrides global",
			resourceType: "services",
			globalResources: []interface{}{
				map[string]interface{}{
					"apiVersion": "v1",
					"kind":       "Service",
					"metadata": map[string]interface{}{
						"name":      "my-service",
						"namespace": "default",
					},
					"spec": map[string]interface{}{
						"clusterIP": "10.0.0.1",
					},
				},
			},
			testResources: []interface{}{
				map[string]interface{}{
					"apiVersion": "v1",
					"kind":       "Service",
					"metadata": map[string]interface{}{
						"name":      "my-service",
						"namespace": "default",
					},
					"spec": map[string]interface{}{
						"clusterIP": "10.0.0.2", // Different IP
					},
				},
			},
			testIdentities: map[string]interface{}{
				"services|v1|Service|default|my-service": struct{}{},
			},
			wantCount: 1, // Only test resource, global was overridden
		},
		{
			name:         "global and test merged",
			resourceType: "services",
			globalResources: []interface{}{
				map[string]interface{}{
					"apiVersion": "v1",
					"kind":       "Service",
					"metadata": map[string]interface{}{
						"name":      "global-service",
						"namespace": "default",
					},
				},
			},
			testResources: []interface{}{
				map[string]interface{}{
					"apiVersion": "v1",
					"kind":       "Service",
					"metadata": map[string]interface{}{
						"name":      "test-service",
						"namespace": "default",
					},
				},
			},
			testIdentities: map[string]interface{}{
				"services|v1|Service|default|test-service": struct{}{},
			},
			wantCount: 2, // Both resources included
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := mergeResourceType(tt.resourceType, tt.globalResources, tt.testResources, tt.testIdentities)
			assert.Len(t, result, tt.wantCount)
		})
	}
}

// =============================================================================
// mergeFixtures Tests
// =============================================================================

func TestMergeFixtures(t *testing.T) {
	tests := []struct {
		name           string
		globalFixtures map[string][]interface{}
		testFixtures   map[string][]interface{}
		wantTypes      []string
	}{
		{
			name:           "both empty",
			globalFixtures: map[string][]interface{}{},
			testFixtures:   map[string][]interface{}{},
			wantTypes:      []string{},
		},
		{
			name: "only global fixtures with resources",
			globalFixtures: map[string][]interface{}{
				"services": {
					map[string]interface{}{
						"apiVersion": "v1",
						"kind":       "Service",
						"metadata":   map[string]interface{}{"name": "svc1", "namespace": "default"},
					},
				},
			},
			testFixtures: map[string][]interface{}{},
			wantTypes:    []string{"services"},
		},
		{
			name:           "only test fixtures with resources",
			globalFixtures: map[string][]interface{}{},
			testFixtures: map[string][]interface{}{
				"ingresses": {
					map[string]interface{}{
						"apiVersion": "networking.k8s.io/v1",
						"kind":       "Ingress",
						"metadata":   map[string]interface{}{"name": "ing1", "namespace": "default"},
					},
				},
			},
			wantTypes: []string{"ingresses"},
		},
		{
			name: "merges different resource types",
			globalFixtures: map[string][]interface{}{
				"services": {
					map[string]interface{}{
						"apiVersion": "v1",
						"kind":       "Service",
						"metadata":   map[string]interface{}{"name": "svc1", "namespace": "default"},
					},
				},
			},
			testFixtures: map[string][]interface{}{
				"ingresses": {
					map[string]interface{}{
						"apiVersion": "networking.k8s.io/v1",
						"kind":       "Ingress",
						"metadata":   map[string]interface{}{"name": "ing1", "namespace": "default"},
					},
				},
			},
			wantTypes: []string{"services", "ingresses"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := MergeFixtures(tt.globalFixtures, tt.testFixtures)

			// Check all expected types are present
			for _, wantType := range tt.wantTypes {
				_, exists := result[wantType]
				assert.True(t, exists, "expected resource type %s to be in merged fixtures", wantType)
			}

			// Check no extra types
			assert.Len(t, result, len(tt.wantTypes))
		})
	}
}

// =============================================================================
// buildFixtureIdentityMap Tests
// =============================================================================

func TestBuildFixtureIdentityMap(t *testing.T) {
	fixtures := map[string][]interface{}{
		"services": {
			map[string]interface{}{
				"apiVersion": "v1",
				"kind":       "Service",
				"metadata": map[string]interface{}{
					"name":      "svc1",
					"namespace": "default",
				},
			},
			map[string]interface{}{
				"apiVersion": "v1",
				"kind":       "Service",
				"metadata": map[string]interface{}{
					"name":      "svc2",
					"namespace": "other",
				},
			},
		},
	}

	result := buildFixtureIdentityMap(fixtures)

	// Should have 2 entries
	assert.Len(t, result, 2)

	// Check expected identities exist
	_, exists := result["services|v1|Service|default|svc1"]
	assert.True(t, exists, "should contain svc1 identity")

	_, exists = result["services|v1|Service|other|svc2"]
	assert.True(t, exists, "should contain svc2 identity")
}

func TestBuildFixtureIdentityMap_InvalidResources(t *testing.T) {
	fixtures := map[string][]interface{}{
		"services": {
			"invalid-string-resource", // Not a map
			map[string]interface{}{
				"apiVersion": "v1",
				"kind":       "Service",
				"metadata": map[string]interface{}{
					"name":      "valid-svc",
					"namespace": "default",
				},
			},
		},
	}

	result := buildFixtureIdentityMap(fixtures)

	// Should only have the valid resource
	assert.Len(t, result, 1)
	_, exists := result["services|v1|Service|default|valid-svc"]
	assert.True(t, exists, "should contain valid-svc identity")
}

// =============================================================================
// mergeHTTPFixtures Tests
// =============================================================================

func TestMergeHTTPFixtures(t *testing.T) {
	tests := []struct {
		name           string
		globalFixtures []config.HTTPResourceFixture
		testFixtures   []config.HTTPResourceFixture
		wantCount      int
		wantURLs       []string
	}{
		{
			name:           "both empty",
			globalFixtures: nil,
			testFixtures:   nil,
			wantCount:      0,
			wantURLs:       []string{},
		},
		{
			name: "only global fixtures",
			globalFixtures: []config.HTTPResourceFixture{
				{URL: "http://example.com/global.txt", Content: "global content"},
			},
			testFixtures: nil,
			wantCount:    1,
			wantURLs:     []string{"http://example.com/global.txt"},
		},
		{
			name:           "only test fixtures",
			globalFixtures: nil,
			testFixtures: []config.HTTPResourceFixture{
				{URL: "http://example.com/test.txt", Content: "test content"},
			},
			wantCount: 1,
			wantURLs:  []string{"http://example.com/test.txt"},
		},
		{
			name: "test overrides global for same URL",
			globalFixtures: []config.HTTPResourceFixture{
				{URL: "http://example.com/data.txt", Content: "global content"},
			},
			testFixtures: []config.HTTPResourceFixture{
				{URL: "http://example.com/data.txt", Content: "test content"},
			},
			wantCount: 1,
			wantURLs:  []string{"http://example.com/data.txt"},
		},
		{
			name: "merges different URLs",
			globalFixtures: []config.HTTPResourceFixture{
				{URL: "http://example.com/global.txt", Content: "global"},
			},
			testFixtures: []config.HTTPResourceFixture{
				{URL: "http://example.com/test.txt", Content: "test"},
			},
			wantCount: 2,
			wantURLs:  []string{"http://example.com/global.txt", "http://example.com/test.txt"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := MergeHTTPFixtures(tt.globalFixtures, tt.testFixtures)

			assert.Len(t, result, tt.wantCount)

			// Check all expected URLs are present
			urlSet := make(map[string]bool)
			for _, fixture := range result {
				urlSet[fixture.URL] = true
			}

			for _, wantURL := range tt.wantURLs {
				assert.True(t, urlSet[wantURL], "expected URL %s to be in merged fixtures", wantURL)
			}
		})
	}
}

// =============================================================================
// fixtureToString Tests
// =============================================================================

func TestFixtureToString(t *testing.T) {
	tests := []struct {
		name    string
		input   interface{}
		want    string
		wantErr bool
	}{
		{
			name:    "string input",
			input:   "http://example.com",
			want:    "http://example.com",
			wantErr: false,
		},
		{
			name:    "stringer input",
			input:   stringerType{value: "formatted value"},
			want:    "formatted value",
			wantErr: false,
		},
		{
			name:    "integer input",
			input:   42,
			want:    "",
			wantErr: true,
		},
		{
			name:    "nil input",
			input:   nil,
			want:    "",
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := fixtureToString(tt.input)

			if tt.wantErr {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
				assert.Equal(t, tt.want, result)
			}
		})
	}
}

// stringerType implements fmt.Stringer for testing.
type stringerType struct {
	value string
}

func (s stringerType) String() string {
	return s.value
}

// =============================================================================
// FixtureHTTPStoreWrapper Tests
// =============================================================================

func TestFixtureHTTPStoreWrapper_Fetch(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))

	fixtures := []config.HTTPResourceFixture{
		{URL: "http://example.com/data.txt", Content: "test content"},
		{URL: "http://example.com/whitespace.txt", Content: "   "},
	}

	store := CreateHTTPStoreFromFixtures(fixtures, logger)
	wrapper := NewFixtureHTTPStoreWrapper(store, logger)

	t.Run("fetch existing URL", func(t *testing.T) {
		result, err := wrapper.Fetch("http://example.com/data.txt")
		require.NoError(t, err)
		assert.Equal(t, "test content", result)
	})

	t.Run("fetch whitespace content URL", func(t *testing.T) {
		result, err := wrapper.Fetch("http://example.com/whitespace.txt")
		require.NoError(t, err)
		assert.Equal(t, "   ", result)
	})

	t.Run("fetch missing URL", func(t *testing.T) {
		_, err := wrapper.Fetch("http://missing.example.com/not-found.txt")
		require.Error(t, err)
		assert.Contains(t, err.Error(), "no fixture defined for URL")
	})

	t.Run("fetch with no arguments", func(t *testing.T) {
		_, err := wrapper.Fetch()
		require.Error(t, err)
		assert.Contains(t, err.Error(), "requires at least 1 argument")
	})

	t.Run("fetch with invalid URL type", func(t *testing.T) {
		_, err := wrapper.Fetch(12345)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "url must be a string")
	})

	t.Run("fetch with options (options are ignored)", func(t *testing.T) {
		// Options should be ignored in fixture mode
		result, err := wrapper.Fetch("http://example.com/data.txt", map[string]interface{}{"delay": "5m"})
		require.NoError(t, err)
		assert.Equal(t, "test content", result)
	})
}

// =============================================================================
// createHTTPStoreFromFixtures Tests
// =============================================================================

func TestCreateHTTPStoreFromFixtures(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))

	fixtures := []config.HTTPResourceFixture{
		{URL: "http://example.com/1.txt", Content: "content1"},
		{URL: "http://example.com/2.txt", Content: "content2"},
	}

	store := CreateHTTPStoreFromFixtures(fixtures, logger)

	// Verify all fixtures are loaded
	for _, fixture := range fixtures {
		content, ok := store.Get(fixture.URL)
		assert.True(t, ok, "fixture for %s should exist", fixture.URL)
		assert.Equal(t, fixture.Content, content)
	}

	// Verify non-existent URL returns false
	_, ok := store.Get("http://example.com/missing.txt")
	assert.False(t, ok, "missing URL should not exist")
}

// =============================================================================
// createStoresFromFixtures Tests
// =============================================================================

func TestCreateStoresFromFixtures_HAProxyPods(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))

	runner := &Runner{
		config: &config.Config{
			WatchedResources: map[string]config.WatchedResource{
				"services": {
					APIVersion: "v1",
					Resources:  "services",
					IndexBy:    []string{"metadata.namespace", "metadata.name"},
				},
			},
		},
		logger: logger,
	}

	fixtures := map[string][]interface{}{
		"haproxy-pods": {
			map[string]interface{}{
				"metadata": map[string]interface{}{
					"name":      "haproxy-pod-1",
					"namespace": "haproxy-system",
				},
			},
		},
	}

	storeMap, err := runner.CreateStoresFromFixtures(fixtures)
	require.NoError(t, err)

	// Verify haproxy-pods store exists
	haproxyPodStore := storeMap["haproxy-pods"]
	require.NotNil(t, haproxyPodStore)

	// Verify pod was added
	pods, err := haproxyPodStore.List()
	require.NoError(t, err)
	assert.Len(t, pods, 1)
}

func TestCreateStoresFromFixtures_InvalidResource(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))

	runner := &Runner{
		config: &config.Config{
			WatchedResources: map[string]config.WatchedResource{
				"services": {
					APIVersion: "v1",
					Resources:  "services",
					IndexBy:    []string{"metadata.namespace", "metadata.name"},
				},
			},
		},
		logger: logger,
	}

	fixtures := map[string][]interface{}{
		"haproxy-pods": {
			"invalid-not-a-map", // Should cause error
		},
	}

	_, err := runner.CreateStoresFromFixtures(fixtures)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "not a map")
}

func TestCreateStoresFromFixtures_UnknownResourceType(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))

	runner := &Runner{
		config: &config.Config{
			WatchedResources: map[string]config.WatchedResource{}, // No watched resources
		},
		logger: logger,
	}

	fixtures := map[string][]interface{}{
		"unknown-resource-type": {
			map[string]interface{}{
				"metadata": map[string]interface{}{
					"name":      "test",
					"namespace": "default",
				},
			},
		},
	}

	_, err := runner.CreateStoresFromFixtures(fixtures)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "not found in watched resources")
}

func TestCreateStoresFromFixtures_EmptyStoresCreated(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))

	runner := &Runner{
		config: &config.Config{
			WatchedResources: map[string]config.WatchedResource{
				"services": {
					APIVersion: "v1",
					Resources:  "services",
					IndexBy:    []string{"metadata.namespace", "metadata.name"},
				},
				"ingresses": {
					APIVersion: "networking.k8s.io/v1",
					Resources:  "ingresses",
					IndexBy:    []string{"metadata.namespace", "metadata.name"},
				},
			},
		},
		logger: logger,
	}

	// Empty fixtures should still create empty stores for all watched resources
	fixtures := map[string][]interface{}{}

	storeMap, err := runner.CreateStoresFromFixtures(fixtures)
	require.NoError(t, err)

	// All watched resources should have stores
	assert.Contains(t, storeMap, "services")
	assert.Contains(t, storeMap, "ingresses")
	assert.Contains(t, storeMap, "haproxy-pods")

	// Stores should be empty
	svcs, _ := storeMap["services"].List()
	assert.Empty(t, svcs)

	ings, _ := storeMap["ingresses"].List()
	assert.Empty(t, ings)
}

func TestCreateStoresFromFixtures_TypeMetaInference(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))

	runner := &Runner{
		config: &config.Config{
			WatchedResources: map[string]config.WatchedResource{
				"services": {
					APIVersion: "v1",
					Resources:  "services",
					IndexBy:    []string{"metadata.namespace", "metadata.name"},
				},
			},
		},
		logger: logger,
	}

	// Fixture without apiVersion and kind - should be inferred
	fixtures := map[string][]interface{}{
		"services": {
			map[string]interface{}{
				"metadata": map[string]interface{}{
					"name":      "my-service",
					"namespace": "default",
				},
				"spec": map[string]interface{}{
					"clusterIP": "10.0.0.1",
				},
			},
		},
	}

	storeMap, err := runner.CreateStoresFromFixtures(fixtures)
	require.NoError(t, err)

	// Verify service was added
	svcs, err := storeMap["services"].List()
	require.NoError(t, err)
	assert.Len(t, svcs, 1)

	// Note: We can't easily verify apiVersion/kind were set without more complex assertions
	// The main test is that the fixture was successfully added to the store
}

// =============================================================================
// sortSnippetNames Tests
// =============================================================================

func TestSortSnippetNames(t *testing.T) {
	tests := []struct {
		name     string
		snippets map[string]config.TemplateSnippet
		want     []string
	}{
		{
			name:     "empty snippets",
			snippets: map[string]config.TemplateSnippet{},
			want:     []string{},
		},
		{
			name: "single snippet",
			snippets: map[string]config.TemplateSnippet{
				"only-snippet": {},
			},
			want: []string{"only-snippet"},
		},
		{
			name: "alphabetical sorting",
			snippets: map[string]config.TemplateSnippet{
				"zebra": {},
				"alpha": {},
				"mike":  {},
			},
			want: []string{"alpha", "mike", "zebra"},
		},
		{
			name: "ordering encoded in names with numbers",
			snippets: map[string]config.TemplateSnippet{
				"features-500-ssl":            {},
				"features-050-initialization": {},
				"features-150-crtlist":        {},
			},
			want: []string{
				"features-050-initialization",
				"features-150-crtlist",
				"features-500-ssl",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := rendercontext.SortSnippetNames(tt.snippets)
			assert.Equal(t, tt.want, result)
		})
	}
}

// =============================================================================
// storeAuxiliaryFiles Tests
// =============================================================================

func TestStoreAuxiliaryFiles(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))

	runner := &Runner{
		config: &config.Config{},
		logger: logger,
	}

	t.Run("nil auxiliary files", func(t *testing.T) {
		result := &TestResult{}
		runner.storeAuxiliaryFiles(result, nil)

		assert.Nil(t, result.RenderedMaps)
		assert.Nil(t, result.RenderedFiles)
		assert.Nil(t, result.RenderedCerts)
	})

	t.Run("empty auxiliary files", func(t *testing.T) {
		result := &TestResult{}
		aux := &dataplane.AuxiliaryFiles{}
		runner.storeAuxiliaryFiles(result, aux)

		assert.Nil(t, result.RenderedMaps)
		assert.Nil(t, result.RenderedFiles)
		assert.Nil(t, result.RenderedCerts)
	})

	t.Run("stores map files", func(t *testing.T) {
		result := &TestResult{}
		aux := &dataplane.AuxiliaryFiles{
			MapFiles: []auxiliaryfiles.MapFile{
				{Path: "backends.map", Content: "host1 backend1\nhost2 backend2"},
				{Path: "paths.map", Content: "/api api-backend"},
			},
		}
		runner.storeAuxiliaryFiles(result, aux)

		require.NotNil(t, result.RenderedMaps)
		assert.Len(t, result.RenderedMaps, 2)
		assert.Equal(t, "host1 backend1\nhost2 backend2", result.RenderedMaps["backends.map"])
		assert.Equal(t, "/api api-backend", result.RenderedMaps["paths.map"])
	})

	t.Run("stores general files", func(t *testing.T) {
		result := &TestResult{}
		aux := &dataplane.AuxiliaryFiles{
			GeneralFiles: []auxiliaryfiles.GeneralFile{
				{Filename: "blocklist.txt", Content: "192.168.1.0/24\n10.0.0.0/8"},
			},
		}
		runner.storeAuxiliaryFiles(result, aux)

		require.NotNil(t, result.RenderedFiles)
		assert.Len(t, result.RenderedFiles, 1)
		assert.Equal(t, "192.168.1.0/24\n10.0.0.0/8", result.RenderedFiles["blocklist.txt"])
	})

	t.Run("stores SSL certificates", func(t *testing.T) {
		result := &TestResult{}
		aux := &dataplane.AuxiliaryFiles{
			SSLCertificates: []auxiliaryfiles.SSLCertificate{
				{Path: "/certs/server.pem", Content: "-----BEGIN CERTIFICATE-----\nMIIB..."},
			},
		}
		runner.storeAuxiliaryFiles(result, aux)

		require.NotNil(t, result.RenderedCerts)
		assert.Len(t, result.RenderedCerts, 1)
		assert.Equal(t, "-----BEGIN CERTIFICATE-----\nMIIB...", result.RenderedCerts["/certs/server.pem"])
	})

	t.Run("stores all types together", func(t *testing.T) {
		result := &TestResult{}
		aux := &dataplane.AuxiliaryFiles{
			MapFiles: []auxiliaryfiles.MapFile{
				{Path: "hosts.map", Content: "host-content"},
			},
			GeneralFiles: []auxiliaryfiles.GeneralFile{
				{Filename: "acl.txt", Content: "acl-content"},
			},
			SSLCertificates: []auxiliaryfiles.SSLCertificate{
				{Path: "/certs/tls.pem", Content: "cert-content"},
			},
		}
		runner.storeAuxiliaryFiles(result, aux)

		require.NotNil(t, result.RenderedMaps)
		require.NotNil(t, result.RenderedFiles)
		require.NotNil(t, result.RenderedCerts)
		assert.Len(t, result.RenderedMaps, 1)
		assert.Len(t, result.RenderedFiles, 1)
		assert.Len(t, result.RenderedCerts, 1)
	})
}

// =============================================================================
// hasRenderingErrorAssertions Tests
// =============================================================================

func TestHasRenderingErrorAssertions(t *testing.T) {
	tests := []struct {
		name       string
		assertions []config.ValidationAssertion
		want       bool
	}{
		{
			name:       "empty assertions",
			assertions: []config.ValidationAssertion{},
			want:       false,
		},
		{
			name: "no rendering_error target",
			assertions: []config.ValidationAssertion{
				{Type: "contains", Target: "haproxy.cfg", Pattern: "backend"},
				{Type: "equals", Target: "map:hosts.map", Expected: "content"},
			},
			want: false,
		},
		{
			name: "has rendering_error target",
			assertions: []config.ValidationAssertion{
				{Type: "contains", Target: "haproxy.cfg", Pattern: "backend"},
				{Type: "contains", Target: "rendering_error", Pattern: "Service .* not found"},
			},
			want: true,
		},
		{
			name: "only rendering_error target",
			assertions: []config.ValidationAssertion{
				{Type: "contains", Target: "rendering_error", Pattern: "expected error message"},
			},
			want: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := hasRenderingErrorAssertions(tt.assertions)
			assert.Equal(t, tt.want, result)
		})
	}
}

// =============================================================================
// renderAuxiliaryFiles Tests
// =============================================================================

// =============================================================================
// runAssertion Tests
// =============================================================================

func TestRunAssertion(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))

	runner := &Runner{
		config: &config.Config{},
		logger: logger,
	}

	ctx := context.Background()
	haproxyConfig := "frontend test\n  bind *:80\nbackend mybackend\n  server s1 127.0.0.1:8080"

	t.Run("contains assertion", func(t *testing.T) {
		assertion := &config.ValidationAssertion{
			Type:    "contains",
			Target:  "haproxy.cfg",
			Pattern: "backend mybackend",
		}

		result := runner.runAssertion(ctx, assertion, haproxyConfig, nil, nil, "", nil, nil)
		assert.True(t, result.Passed)
		assert.Equal(t, "contains", result.Type)
	})

	t.Run("not_contains assertion", func(t *testing.T) {
		assertion := &config.ValidationAssertion{
			Type:    "not_contains",
			Target:  "haproxy.cfg",
			Pattern: "error",
		}

		result := runner.runAssertion(ctx, assertion, haproxyConfig, nil, nil, "", nil, nil)
		assert.True(t, result.Passed)
		assert.Equal(t, "not_contains", result.Type)
	})

	t.Run("match_count assertion", func(t *testing.T) {
		assertion := &config.ValidationAssertion{
			Type:     "match_count",
			Target:   "haproxy.cfg",
			Pattern:  "server",
			Expected: "1",
		}

		result := runner.runAssertion(ctx, assertion, haproxyConfig, nil, nil, "", nil, nil)
		assert.True(t, result.Passed)
		assert.Equal(t, "match_count", result.Type)
	})

	t.Run("equals assertion", func(t *testing.T) {
		assertion := &config.ValidationAssertion{
			Type:     "equals",
			Target:   "haproxy.cfg",
			Expected: haproxyConfig,
		}

		result := runner.runAssertion(ctx, assertion, haproxyConfig, nil, nil, "", nil, nil)
		assert.True(t, result.Passed)
		assert.Equal(t, "equals", result.Type)
	})

	t.Run("jsonpath assertion", func(t *testing.T) {
		assertion := &config.ValidationAssertion{
			Type:     "jsonpath",
			JSONPath: "{.test}",
			Expected: "value",
		}

		templateCtx := map[string]interface{}{
			"test": "value",
		}

		result := runner.runAssertion(ctx, assertion, haproxyConfig, nil, templateCtx, "", nil, nil)
		assert.True(t, result.Passed)
		assert.Equal(t, "jsonpath", result.Type)
	})

	t.Run("match_order assertion", func(t *testing.T) {
		assertion := &config.ValidationAssertion{
			Type:     "match_order",
			Target:   "haproxy.cfg",
			Patterns: []string{"frontend", "backend"},
		}

		result := runner.runAssertion(ctx, assertion, haproxyConfig, nil, nil, "", nil, nil)
		assert.True(t, result.Passed)
		assert.Equal(t, "match_order", result.Type)
	})

	t.Run("unknown assertion type", func(t *testing.T) {
		assertion := &config.ValidationAssertion{
			Type: "invalid_type",
		}

		result := runner.runAssertion(ctx, assertion, haproxyConfig, nil, nil, "", nil, nil)
		assert.False(t, result.Passed)
		assert.Contains(t, result.Error, "unknown assertion type")
	})
}

// =============================================================================
// assertContains Tests
// =============================================================================

func TestAssertContains(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))

	runner := &Runner{
		config: &config.Config{},
		logger: logger,
	}

	t.Run("pattern found", func(t *testing.T) {
		assertion := &config.ValidationAssertion{
			Type:        "contains",
			Target:      "haproxy.cfg",
			Pattern:     "backend.*test",
			Description: "Should contain backend test",
		}

		result := runner.assertContains("frontend test\nbackend my_test", nil, assertion, "")
		assert.True(t, result.Passed)
		assert.Equal(t, "Should contain backend test", result.Description)
	})

	t.Run("pattern not found", func(t *testing.T) {
		assertion := &config.ValidationAssertion{
			Type:    "contains",
			Target:  "haproxy.cfg",
			Pattern: "backend.*missing",
		}

		result := runner.assertContains("frontend test\nbackend my_test", nil, assertion, "")
		assert.False(t, result.Passed)
		assert.Contains(t, result.Error, "pattern")
		assert.Contains(t, result.Error, "not found")
	})

	t.Run("invalid regex pattern", func(t *testing.T) {
		assertion := &config.ValidationAssertion{
			Type:    "contains",
			Target:  "haproxy.cfg",
			Pattern: "[invalid(regex",
		}

		result := runner.assertContains("frontend test", nil, assertion, "")
		assert.False(t, result.Passed)
		assert.Contains(t, result.Error, "invalid regex pattern")
	})

	t.Run("default description when empty", func(t *testing.T) {
		assertion := &config.ValidationAssertion{
			Type:        "contains",
			Target:      "haproxy.cfg",
			Pattern:     "test",
			Description: "",
		}

		result := runner.assertContains("test", nil, assertion, "")
		assert.True(t, result.Passed)
		assert.Contains(t, result.Description, "must contain pattern")
	})

	t.Run("targets rendering_error", func(t *testing.T) {
		assertion := &config.ValidationAssertion{
			Type:    "contains",
			Target:  "rendering_error",
			Pattern: "Service.*not found",
		}

		result := runner.assertContains("", nil, assertion, "Service 'api' not found")
		assert.True(t, result.Passed)
	})
}

// =============================================================================
// findMapFile Tests
// =============================================================================

func TestFindMapFile(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))

	runner := &Runner{
		config: &config.Config{},
		logger: logger,
	}

	t.Run("finds existing map file", func(t *testing.T) {
		auxFiles := &dataplane.AuxiliaryFiles{
			MapFiles: []auxiliaryfiles.MapFile{
				{Path: "hosts.map", Content: "host1 backend1"},
				{Path: "paths.map", Content: "/api api-backend"},
			},
		}

		result := runner.findMapFile("hosts.map", auxFiles)
		assert.Equal(t, "host1 backend1", result)
	})

	t.Run("returns empty for non-existent map file", func(t *testing.T) {
		auxFiles := &dataplane.AuxiliaryFiles{
			MapFiles: []auxiliaryfiles.MapFile{
				{Path: "hosts.map", Content: "host1 backend1"},
			},
		}

		result := runner.findMapFile("missing.map", auxFiles)
		assert.Equal(t, "", result)
	})

	t.Run("returns empty for nil auxiliary files", func(t *testing.T) {
		result := runner.findMapFile("hosts.map", nil)
		assert.Equal(t, "", result)
	})

	t.Run("returns empty for empty map files slice", func(t *testing.T) {
		auxFiles := &dataplane.AuxiliaryFiles{
			MapFiles: []auxiliaryfiles.MapFile{},
		}

		result := runner.findMapFile("hosts.map", auxFiles)
		assert.Equal(t, "", result)
	})
}

// =============================================================================
// renderAuxiliaryFiles Tests
// =============================================================================

func TestRenderAuxiliaryFiles(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))
	validationPaths := &dataplane.ValidationPaths{
		GeneralStorageDir: "/etc/haproxy/general",
	}

	t.Run("renders map files", func(t *testing.T) {
		templates := map[string]string{
			"haproxy.cfg":  "# placeholder",
			"backends.map": "host1 my-backend",
		}
		engine, err := templating.New(templating.EngineTypeScriggo, templates, nil, nil, nil)
		require.NoError(t, err)

		runner := &Runner{
			config: &config.Config{
				Maps: map[string]config.MapFile{
					"backends.map": {Template: "host1 my-backend"},
				},
			},
			logger: logger,
		}

		renderCtx := map[string]interface{}{}

		auxFiles, err := runner.renderAuxiliaryFiles(engine, renderCtx, validationPaths)
		require.NoError(t, err)
		require.Len(t, auxFiles.MapFiles, 1)
		assert.Equal(t, "backends.map", auxFiles.MapFiles[0].Path)
		// Scriggo adds trailing newline
		assert.Equal(t, "host1 my-backend\n", auxFiles.MapFiles[0].Content)
	})

	t.Run("renders general files", func(t *testing.T) {
		templates := map[string]string{
			"haproxy.cfg":   "# placeholder",
			"blocklist.txt": "192.168.1.100",
		}
		engine, err := templating.New(templating.EngineTypeScriggo, templates, nil, nil, nil)
		require.NoError(t, err)

		runner := &Runner{
			config: &config.Config{
				Files: map[string]config.GeneralFile{
					"blocklist.txt": {Template: "192.168.1.100"},
				},
			},
			logger: logger,
		}

		renderCtx := map[string]interface{}{}

		auxFiles, err := runner.renderAuxiliaryFiles(engine, renderCtx, validationPaths)
		require.NoError(t, err)
		require.Len(t, auxFiles.GeneralFiles, 1)
		assert.Equal(t, "blocklist.txt", auxFiles.GeneralFiles[0].Filename)
		// Scriggo adds trailing newline
		assert.Equal(t, "192.168.1.100\n", auxFiles.GeneralFiles[0].Content)
	})

	t.Run("renders SSL certificates", func(t *testing.T) {
		templates := map[string]string{
			"haproxy.cfg": "# placeholder",
			"server.pem":  "-----BEGIN CERTIFICATE-----\nMIIB...\n-----END CERTIFICATE-----",
		}
		engine, err := templating.New(templating.EngineTypeScriggo, templates, nil, nil, nil)
		require.NoError(t, err)

		runner := &Runner{
			config: &config.Config{
				SSLCertificates: map[string]config.SSLCertificate{
					"server.pem": {Template: "-----BEGIN CERTIFICATE-----\nMIIB...\n-----END CERTIFICATE-----"},
				},
			},
			logger: logger,
		}

		renderCtx := map[string]interface{}{}

		auxFiles, err := runner.renderAuxiliaryFiles(engine, renderCtx, validationPaths)
		require.NoError(t, err)
		require.Len(t, auxFiles.SSLCertificates, 1)
		assert.Equal(t, "server.pem", auxFiles.SSLCertificates[0].Path)
		// Scriggo adds trailing newline
		assert.Equal(t, "-----BEGIN CERTIFICATE-----\nMIIB...\n-----END CERTIFICATE-----\n", auxFiles.SSLCertificates[0].Content)
	})

	t.Run("returns empty when no auxiliary files", func(t *testing.T) {
		templates := map[string]string{
			"haproxy.cfg": "# placeholder",
		}
		engine, err := templating.New(templating.EngineTypeScriggo, templates, nil, nil, nil)
		require.NoError(t, err)

		runner := &Runner{
			config: &config.Config{},
			logger: logger,
		}

		auxFiles, err := runner.renderAuxiliaryFiles(engine, map[string]interface{}{}, validationPaths)
		require.NoError(t, err)
		assert.Empty(t, auxFiles.MapFiles)
		assert.Empty(t, auxFiles.GeneralFiles)
		assert.Empty(t, auxFiles.SSLCertificates)
	})

	t.Run("error on map file render failure", func(t *testing.T) {
		templates := map[string]string{
			"haproxy.cfg":  "# placeholder",
			"backends.map": `{{ fail("intentional error") }}`,
		}
		engine, err := templating.New(templating.EngineTypeScriggo, templates, nil, nil, nil)
		require.NoError(t, err)

		runner := &Runner{
			config: &config.Config{
				Maps: map[string]config.MapFile{
					"backends.map": {Template: `{{ fail("intentional error") }}`},
				},
			},
			logger: logger,
		}

		_, err = runner.renderAuxiliaryFiles(engine, map[string]interface{}{}, validationPaths)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "failed to render map file")
	})

	t.Run("error on general file render failure", func(t *testing.T) {
		templates := map[string]string{
			"haproxy.cfg":   "# placeholder",
			"blocklist.txt": `{{ fail("intentional error") }}`,
		}
		engine, err := templating.New(templating.EngineTypeScriggo, templates, nil, nil, nil)
		require.NoError(t, err)

		runner := &Runner{
			config: &config.Config{
				Files: map[string]config.GeneralFile{
					"blocklist.txt": {Template: `{{ fail("intentional error") }}`},
				},
			},
			logger: logger,
		}

		_, err = runner.renderAuxiliaryFiles(engine, map[string]interface{}{}, validationPaths)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "failed to render general file")
	})

	t.Run("error on SSL certificate render failure", func(t *testing.T) {
		templates := map[string]string{
			"haproxy.cfg": "# placeholder",
			"server.pem":  `{{ fail("intentional error") }}`,
		}
		engine, err := templating.New(templating.EngineTypeScriggo, templates, nil, nil, nil)
		require.NoError(t, err)

		runner := &Runner{
			config: &config.Config{
				SSLCertificates: map[string]config.SSLCertificate{
					"server.pem": {Template: `{{ fail("intentional error") }}`},
				},
			},
			logger: logger,
		}

		_, err = runner.renderAuxiliaryFiles(engine, map[string]interface{}{}, validationPaths)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "failed to render SSL certificate")
	})
}

// =============================================================================
// assertDeterministic Tests
// =============================================================================

func TestAssertDeterministic(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))

	tmpDir := t.TempDir()
	validationPaths := &dataplane.ValidationPaths{
		SSLCertsDir:       filepath.Join(tmpDir, "ssl"),
		CRTListDir:        filepath.Join(tmpDir, "ssl"),
		MapsDir:           filepath.Join(tmpDir, "maps"),
		GeneralStorageDir: filepath.Join(tmpDir, "files"),
		ConfigFile:        filepath.Join(tmpDir, "haproxy.cfg"),
	}

	t.Run("deterministic template passes", func(t *testing.T) {
		// Template that produces identical output on every render
		templates := map[string]string{
			"haproxy.cfg": "# Deterministic config\nfrontend test\n  bind *:80",
		}
		engine, err := templating.New(templating.EngineTypeScriggo, templates, nil, nil, nil)
		require.NoError(t, err)

		runner := &Runner{
			config: &config.Config{
				HAProxyConfig: config.HAProxyConfig{
					Template: templates["haproxy.cfg"],
				},
			},
			logger: logger,
		}

		assertion := &config.ValidationAssertion{
			Type:        "deterministic",
			Description: "Template must be deterministic",
		}

		renderDeps := &RenderDependencies{
			Engine:          engine,
			Stores:          make(map[string]stores.Store),
			ValidationPaths: validationPaths,
		}

		// The template engine adds a trailing newline, so we need to match that
		firstConfig := "# Deterministic config\nfrontend test\n  bind *:80\n"
		firstAuxFiles := &dataplane.AuxiliaryFiles{} // Empty but not nil
		result := runner.assertDeterministic(assertion, firstConfig, firstAuxFiles, renderDeps)

		if !result.Passed {
			t.Logf("Assertion failed with error: %s", result.Error)
		}
		assert.True(t, result.Passed, "Expected assertion to pass, got error: %s", result.Error)
		assert.Equal(t, "deterministic", result.Type)
		assert.Equal(t, "Template must be deterministic", result.Description)
	})

	t.Run("missing render dependencies fails", func(t *testing.T) {
		runner := &Runner{
			config: &config.Config{},
			logger: logger,
		}

		ctx := context.Background()
		assertion := &config.ValidationAssertion{
			Type: "deterministic",
		}

		result := runner.runAssertion(ctx, assertion, "test config", nil, nil, "", nil, nil)

		assert.False(t, result.Passed)
		assert.Contains(t, result.Error, "requires render dependencies")
	})

	t.Run("no first render output fails", func(t *testing.T) {
		templates := map[string]string{
			"haproxy.cfg": "# test",
		}
		engine, err := templating.New(templating.EngineTypeScriggo, templates, nil, nil, nil)
		require.NoError(t, err)

		runner := &Runner{
			config: &config.Config{},
			logger: logger,
		}

		assertion := &config.ValidationAssertion{
			Type: "deterministic",
		}

		renderDeps := &RenderDependencies{
			Engine:          engine,
			Stores:          make(map[string]stores.Store),
			ValidationPaths: validationPaths,
		}

		// Empty first config and nil aux files = no first render output
		result := runner.assertDeterministic(assertion, "", nil, renderDeps)

		assert.False(t, result.Passed)
		assert.Contains(t, result.Error, "first render produced no output")
	})
}

// =============================================================================
// generateUnifiedDiff Tests
// =============================================================================

func TestGenerateUnifiedDiff(t *testing.T) {
	t.Run("shows diff between different strings", func(t *testing.T) {
		from := "line1\nline2\nline3\n"
		to := "line1\nmodified\nline3\n"

		diff := generateUnifiedDiff("file1", "file2", from, to)

		assert.Contains(t, diff, "--- file1")
		assert.Contains(t, diff, "+++ file2")
		assert.Contains(t, diff, "-line2")
		assert.Contains(t, diff, "+modified")
	})

	t.Run("returns message for identical strings", func(t *testing.T) {
		from := "identical content"
		to := "identical content"

		diff := generateUnifiedDiff("file1", "file2", from, to)

		assert.Contains(t, diff, "no visible diff")
	})
}

// =============================================================================
// compareAuxiliaryFiles Tests
// =============================================================================

func TestCompareAuxiliaryFiles(t *testing.T) {
	t.Run("both nil returns empty", func(t *testing.T) {
		result := compareAuxiliaryFiles(nil, nil)
		assert.Empty(t, result)
	})

	t.Run("one nil returns error", func(t *testing.T) {
		first := &dataplane.AuxiliaryFiles{}
		result := compareAuxiliaryFiles(first, nil)
		assert.Contains(t, result, "one render produced files")

		result = compareAuxiliaryFiles(nil, first)
		assert.Contains(t, result, "one render produced files")
	})

	t.Run("identical files returns empty", func(t *testing.T) {
		first := &dataplane.AuxiliaryFiles{
			MapFiles: []auxiliaryfiles.MapFile{
				{Path: "test.map", Content: "key value"},
			},
		}
		second := &dataplane.AuxiliaryFiles{
			MapFiles: []auxiliaryfiles.MapFile{
				{Path: "test.map", Content: "key value"},
			},
		}

		result := compareAuxiliaryFiles(first, second)
		assert.Empty(t, result)
	})

	t.Run("different map file content returns diff", func(t *testing.T) {
		first := &dataplane.AuxiliaryFiles{
			MapFiles: []auxiliaryfiles.MapFile{
				{Path: "test.map", Content: "key value1"},
			},
		}
		second := &dataplane.AuxiliaryFiles{
			MapFiles: []auxiliaryfiles.MapFile{
				{Path: "test.map", Content: "key value2"},
			},
		}

		result := compareAuxiliaryFiles(first, second)
		assert.Contains(t, result, "map files")
		assert.Contains(t, result, "differs between renders")
	})

	t.Run("missing file in second returns error", func(t *testing.T) {
		first := &dataplane.AuxiliaryFiles{
			GeneralFiles: []auxiliaryfiles.GeneralFile{
				{Filename: "test.txt", Content: "content"},
			},
		}
		second := &dataplane.AuxiliaryFiles{
			GeneralFiles: []auxiliaryfiles.GeneralFile{},
		}

		result := compareAuxiliaryFiles(first, second)
		assert.Contains(t, result, "exists in first render but not in second")
	})

	t.Run("missing file in first returns error", func(t *testing.T) {
		first := &dataplane.AuxiliaryFiles{
			GeneralFiles: []auxiliaryfiles.GeneralFile{},
		}
		second := &dataplane.AuxiliaryFiles{
			GeneralFiles: []auxiliaryfiles.GeneralFile{
				{Filename: "test.txt", Content: "content"},
			},
		}

		result := compareAuxiliaryFiles(first, second)
		assert.Contains(t, result, "exists in second render but not in first")
	})
}
