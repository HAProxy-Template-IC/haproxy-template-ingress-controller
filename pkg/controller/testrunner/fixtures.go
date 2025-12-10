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
	"fmt"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"

	"haproxy-template-ic/pkg/controller/resourcestore"
	"haproxy-template-ic/pkg/core/logging"
	"haproxy-template-ic/pkg/k8s/indexer"
	"haproxy-template-ic/pkg/k8s/store"
	"haproxy-template-ic/pkg/k8s/types"
)

// mergeFixtures deep merges global fixtures with test fixtures by resource identity.
//
// Resource identity is defined by: apiVersion + kind + namespace + name
//
// Merge strategy:
//   - Global fixtures are added first
//   - Test fixtures override global fixtures when same resource identity exists
//   - Resource types not in global fixtures are taken from test fixtures
//
// Parameters:
//   - globalFixtures: Fixtures from validationTests._global
//   - testFixtures: Fixtures from specific test
//
// Returns:
//   - Merged fixtures map (resource type → list of resources)
func mergeFixtures(globalFixtures, testFixtures map[string][]interface{}) map[string][]interface{} {
	// Build identity map for test fixtures to detect overrides
	testIdentities := buildFixtureIdentityMap(testFixtures)

	// Collect all resource types from both fixtures
	allResourceTypes := collectResourceTypes(globalFixtures, testFixtures)

	// Merge fixtures for each resource type
	merged := make(map[string][]interface{})
	for resourceType := range allResourceTypes {
		mergedResources := mergeResourceType(
			resourceType,
			globalFixtures[resourceType],
			testFixtures[resourceType],
			testIdentities,
		)

		if len(mergedResources) > 0 {
			merged[resourceType] = mergedResources
		}
	}

	return merged
}

// buildFixtureIdentityMap creates a map of resource identities for deduplication.
func buildFixtureIdentityMap(fixtures map[string][]interface{}) map[string]interface{} {
	identities := make(map[string]interface{})

	for resourceType, resources := range fixtures {
		for _, resourceObj := range resources {
			resourceMap, ok := resourceObj.(map[string]interface{})
			if !ok {
				continue
			}

			resource := &unstructured.Unstructured{Object: resourceMap}
			identity := buildResourceIdentity(resourceType, resource)
			identities[identity] = resourceObj
		}
	}

	return identities
}

// collectResourceTypes collects all unique resource types from multiple fixture maps.
func collectResourceTypes(fixtureMaps ...map[string][]interface{}) map[string]bool {
	allTypes := make(map[string]bool)

	for _, fixtures := range fixtureMaps {
		for resourceType := range fixtures {
			allTypes[resourceType] = true
		}
	}

	return allTypes
}

// mergeResourceType merges global and test fixtures for a single resource type.
func mergeResourceType(
	resourceType string,
	globalResources []interface{},
	testResources []interface{},
	testIdentities map[string]interface{},
) []interface{} {
	var merged []interface{}

	// Add global fixtures that are NOT overridden by test fixtures
	for _, resourceObj := range globalResources {
		resourceMap, ok := resourceObj.(map[string]interface{})
		if !ok {
			continue
		}

		resource := &unstructured.Unstructured{Object: resourceMap}
		identity := buildResourceIdentity(resourceType, resource)

		// Only add if not overridden by test fixture
		if _, overridden := testIdentities[identity]; !overridden {
			merged = append(merged, resourceObj)
		}
	}

	// Add all test fixtures (these override global fixtures)
	merged = append(merged, testResources...)

	return merged
}

// buildResourceIdentity creates a unique identity key for a resource.
//
// Identity format: "resourceType|apiVersion|kind|namespace|name"
//
// This ensures resources are uniquely identified across different resource types
// and API versions.
func buildResourceIdentity(resourceType string, resource *unstructured.Unstructured) string {
	return fmt.Sprintf("%s|%s|%s|%s|%s",
		resourceType,
		resource.GetAPIVersion(),
		resource.GetKind(),
		resource.GetNamespace(),
		resource.GetName(),
	)
}

// createStoresFromFixtures creates resource stores from test fixtures.
//
// This converts the test fixtures (map of resource type → list of resources)
// into resource stores that can be used for template rendering.
//
// Implementation:
//   - Phase 1: Create empty stores for ALL watched resources
//   - Phase 2: Populate stores with fixture data where provided
//
// This ensures templates can safely call .List() on any watched resource type,
// even if that resource type is not present in test fixtures.
//
// Parameters:
//   - fixtures: Map of resource type names to lists of Kubernetes resources
//
// Returns:
//   - Map of resource type names to resource stores
//   - error if fixture processing fails
//
//nolint:revive // Complexity acceptable for fixture processing with indexing and type inference
func (r *Runner) createStoresFromFixtures(fixtures map[string][]interface{}) (map[string]types.Store, error) {
	stores := make(map[string]types.Store)

	// PHASE 1: Create empty stores for ALL watched resources
	// This ensures templates can safely reference any watched resource type
	for resourceType, watchedResource := range r.config.WatchedResources {
		r.logger.Log(context.Background(), logging.LevelTrace, "Creating empty store for watched resource",
			"resource_type", resourceType)

		// Use number of index fields for numKeys parameter
		numKeys := len(watchedResource.IndexBy)
		if numKeys < 1 {
			numKeys = 1
		}
		stores[resourceType] = store.NewMemoryStore(numKeys)
	}

	// Create store for haproxy-pods (special controller metadata, not a watched resource)
	r.logger.Log(context.Background(), logging.LevelTrace, "Creating empty store for haproxy-pods (controller metadata)")
	stores["haproxy-pods"] = store.NewMemoryStore(2) // namespace + name keys

	// PHASE 2: Populate stores with fixture data
	for resourceType, resources := range fixtures {
		r.logger.Log(context.Background(), logging.LevelTrace, "Populating fixture store",
			"resource_type", resourceType,
			"count", len(resources))

		// Handle haproxy-pods specially (controller metadata, not a watched resource)
		if resourceType == "haproxy-pods" {
			storeInstance := stores["haproxy-pods"]

			for i, resourceObj := range resources {
				resourceMap, ok := resourceObj.(map[string]interface{})
				if !ok {
					return nil, fmt.Errorf("haproxy-pods fixture at index %d is not a map", i)
				}

				resource := &unstructured.Unstructured{Object: resourceMap}

				// Ensure TypeMeta for haproxy-pods (Pod resources)
				if resource.GetAPIVersion() == "" {
					resource.SetAPIVersion("v1")
				}
				if resource.GetKind() == "" {
					resource.SetKind("Pod")
				}

				r.logger.Log(context.Background(), logging.LevelTrace, "Adding haproxy-pods fixture to store",
					"index", i,
					"name", resource.GetName(),
					"namespace", resource.GetNamespace())

				// Use namespace + name as keys (matches discovery component indexing)
				keys := []string{resource.GetNamespace(), resource.GetName()}

				if err := storeInstance.Add(resource, keys); err != nil {
					return nil, fmt.Errorf("failed to add haproxy-pods fixture: %w", err)
				}
			}
			continue // Skip normal watched resource processing
		}

		// Verify resource type is in watched resources
		watchedResource, exists := r.config.WatchedResources[resourceType]
		if !exists {
			return nil, fmt.Errorf("resource type %q in fixtures not found in watched resources", resourceType)
		}

		// Create indexer for this resource type
		idx, err := indexer.New(indexer.Config{
			IndexBy:      watchedResource.IndexBy,
			IgnoreFields: r.config.WatchedResourcesIgnoreFields,
		})
		if err != nil {
			return nil, fmt.Errorf("failed to create indexer for %s: %w", resourceType, err)
		}

		// Get the existing empty store created in Phase 1
		storeInstance := stores[resourceType]

		// Add all fixture resources to the store
		for i, resourceObj := range resources {
			// Convert interface{} to unstructured.Unstructured
			// The interface{} is expected to be a map[string]interface{}
			resourceMap, ok := resourceObj.(map[string]interface{})
			if !ok {
				return nil, fmt.Errorf("fixture resource at index %d in %s is not a map", i, resourceType)
			}

			resource := &unstructured.Unstructured{Object: resourceMap}

			// Ensure resource has TypeMeta (some fixtures might omit it)
			if resource.GetAPIVersion() == "" {
				resource.SetAPIVersion(watchedResource.APIVersion)
			}
			if resource.GetKind() == "" {
				// Extract kind from resource name (e.g., "ingresses" → "Ingress")
				// This is a heuristic; proper kind resolution would use RESTMapper
				kind := resourcestore.SingularizeResourceType(watchedResource.Resources)
				resource.SetKind(kind)
			}

			r.logger.Log(context.Background(), logging.LevelTrace, "Adding fixture resource to store",
				"resource_type", resourceType,
				"index", i,
				"name", resource.GetName(),
				"namespace", resource.GetNamespace())

			// Extract index keys using indexer
			keys, err := idx.ExtractKeys(resource)
			if err != nil {
				return nil, fmt.Errorf("failed to extract index keys from fixture resource: %w", err)
			}

			if err := storeInstance.Add(resource, keys); err != nil {
				return nil, fmt.Errorf("failed to add fixture resource to %s store: %w", resourceType, err)
			}
		}
	}

	return stores, nil
}
