package store

import (
	"testing"
)

// TestNewMemoryStore verifies store creation.
func TestNewMemoryStore(t *testing.T) {
	store := NewMemoryStore(2)
	if store == nil {
		t.Fatal("NewMemoryStore returned nil")
	}

	if store.Size() != 0 {
		t.Errorf("expected size 0, got %d", store.Size())
	}
}

// TestMemoryStore_AddAndGet verifies adding and retrieving resources.
func TestMemoryStore_AddAndGet(t *testing.T) {
	store := NewMemoryStore(2)

	resource := map[string]string{
		"name": "test-resource",
		"data": "test-data",
	}

	// Add resource
	err := store.Add(resource, []string{"default", "test-resource"})
	if err != nil {
		t.Fatalf("Add failed: %v", err)
	}

	// Verify size
	if store.Size() != 1 {
		t.Errorf("expected size 1, got %d", store.Size())
	}

	// Get resource with exact match
	results, err := store.Get("default", "test-resource")
	if err != nil {
		t.Fatalf("Get failed: %v", err)
	}

	if len(results) != 1 {
		t.Fatalf("expected 1 result, got %d", len(results))
	}

	retrieved := results[0].(map[string]string)
	if retrieved["name"] != "test-resource" {
		t.Errorf("unexpected resource: %v", retrieved)
	}
}

// TestMemoryStore_PartialMatch verifies partial key matching.
func TestMemoryStore_PartialMatch(t *testing.T) {
	store := NewMemoryStore(2)

	// Add multiple resources in same namespace
	resources := []struct {
		keys []string
		name string
	}{
		{[]string{"default", "resource-1"}, "resource-1"},
		{[]string{"default", "resource-2"}, "resource-2"},
		{[]string{"kube-system", "resource-3"}, "resource-3"},
	}

	for _, r := range resources {
		resource := map[string]string{"name": r.name}
		if err := store.Add(resource, r.keys); err != nil {
			t.Fatalf("Add failed: %v", err)
		}
	}

	// Get all resources in "default" namespace
	results, err := store.Get("default")
	if err != nil {
		t.Fatalf("Get failed: %v", err)
	}

	if len(results) != 2 {
		t.Fatalf("expected 2 results, got %d", len(results))
	}
}

// TestMemoryStore_Update verifies resource updates.
func TestMemoryStore_Update(t *testing.T) {
	store := NewMemoryStore(2)

	resource := map[string]string{"version": "v1"}
	keys := []string{"default", "test"}

	// Add resource
	if err := store.Add(resource, keys); err != nil {
		t.Fatalf("Add failed: %v", err)
	}

	// Update resource
	updatedResource := map[string]string{"version": "v2"}
	if err := store.Update(updatedResource, keys); err != nil {
		t.Fatalf("Update failed: %v", err)
	}

	// Verify update
	results, err := store.Get(keys...)
	if err != nil {
		t.Fatalf("Get failed: %v", err)
	}

	if len(results) != 1 {
		t.Fatalf("expected 1 result, got %d", len(results))
	}

	retrieved := results[0].(map[string]string)
	if retrieved["version"] != "v2" {
		t.Errorf("expected version='v2', got %q", retrieved["version"])
	}
}

// TestMemoryStore_Delete verifies resource deletion.
func TestMemoryStore_Delete(t *testing.T) {
	store := NewMemoryStore(2)

	resource := map[string]string{"name": "test"}
	keys := []string{"default", "test"}

	// Add resource
	if err := store.Add(resource, keys); err != nil {
		t.Fatalf("Add failed: %v", err)
	}

	// Verify exists
	if store.Size() != 1 {
		t.Errorf("expected size 1, got %d", store.Size())
	}

	// Delete resource
	if err := store.Delete(keys...); err != nil {
		t.Fatalf("Delete failed: %v", err)
	}

	// Verify deleted
	if store.Size() != 0 {
		t.Errorf("expected size 0, got %d", store.Size())
	}

	// Verify not found
	results, err := store.Get(keys...)
	if err != nil {
		t.Fatalf("Get failed: %v", err)
	}

	if len(results) != 0 {
		t.Errorf("expected 0 results after delete, got %d", len(results))
	}
}

// TestMemoryStore_List verifies listing all resources.
func TestMemoryStore_List(t *testing.T) {
	store := NewMemoryStore(2)

	// Add multiple resources
	for i := 0; i < 5; i++ {
		resource := map[string]int{"index": i}
		keys := []string{"default", string(rune('a' + i))}
		if err := store.Add(resource, keys); err != nil {
			t.Fatalf("Add failed: %v", err)
		}
	}

	// List all
	results, err := store.List()
	if err != nil {
		t.Fatalf("List failed: %v", err)
	}

	if len(results) != 5 {
		t.Errorf("expected 5 results, got %d", len(results))
	}
}

// TestMemoryStore_Clear verifies clearing the store.
func TestMemoryStore_Clear(t *testing.T) {
	store := NewMemoryStore(2)

	// Add resources
	for i := 0; i < 3; i++ {
		resource := map[string]int{"index": i}
		keys := []string{"default", string(rune('a' + i))}
		if err := store.Add(resource, keys); err != nil {
			t.Fatalf("Add failed: %v", err)
		}
	}

	// Verify size
	if store.Size() != 3 {
		t.Errorf("expected size 3, got %d", store.Size())
	}

	// Clear
	if err := store.Clear(); err != nil {
		t.Fatalf("Clear failed: %v", err)
	}

	// Verify empty
	if store.Size() != 0 {
		t.Errorf("expected size 0 after clear, got %d", store.Size())
	}
}

// TestMemoryStore_WrongKeyCount verifies error on wrong key count.
func TestMemoryStore_WrongKeyCount(t *testing.T) {
	store := NewMemoryStore(2)

	resource := map[string]string{"name": "test"}

	// Try to add with wrong number of keys
	err := store.Add(resource, []string{"only-one-key"})
	if err == nil {
		t.Error("expected error for wrong key count")
	}

	// Try to delete with wrong number of keys
	err = store.Delete("only-one-key")
	if err == nil {
		t.Error("expected error for wrong key count")
	}

	// Try to update with wrong number of keys
	err = store.Update(resource, []string{"only-one-key"})
	if err == nil {
		t.Error("expected error for wrong key count in Update")
	}
}

// TestNewMemoryStore_MinKeys verifies store creation with numKeys < 1 defaults to 1.
func TestNewMemoryStore_MinKeys(t *testing.T) {
	tests := []struct {
		name         string
		numKeys      int
		expectedKeys int
	}{
		{name: "zero keys defaults to 1", numKeys: 0, expectedKeys: 1},
		{name: "negative keys defaults to 1", numKeys: -1, expectedKeys: 1},
		{name: "one key stays 1", numKeys: 1, expectedKeys: 1},
		{name: "two keys stays 2", numKeys: 2, expectedKeys: 2},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			store := NewMemoryStore(tt.numKeys)
			if store == nil {
				t.Fatal("NewMemoryStore returned nil")
			}

			// Verify the store works with the expected number of keys
			resource := map[string]string{"name": "test"}
			keys := make([]string, tt.expectedKeys)
			for i := range keys {
				keys[i] = "key" + string(rune('A'+i))
			}

			err := store.Add(resource, keys)
			if err != nil {
				t.Errorf("Add with %d keys failed: %v", tt.expectedKeys, err)
			}
		})
	}
}

// TestMemoryStore_GetErrors verifies error handling in Get.
func TestMemoryStore_GetErrors(t *testing.T) {
	store := NewMemoryStore(2)

	// Add a resource first
	resource := map[string]string{"name": "test"}
	if err := store.Add(resource, []string{"default", "test"}); err != nil {
		t.Fatalf("Add failed: %v", err)
	}

	tests := []struct {
		name      string
		keys      []string
		wantError bool
		errorMsg  string
	}{
		{
			name:      "no keys provided",
			keys:      []string{},
			wantError: true,
			errorMsg:  "at least one key required",
		},
		{
			name:      "too many keys",
			keys:      []string{"a", "b", "c"},
			wantError: true,
			errorMsg:  "too many keys",
		},
		{
			name:      "valid single key (partial match)",
			keys:      []string{"default"},
			wantError: false,
		},
		{
			name:      "valid exact keys",
			keys:      []string{"default", "test"},
			wantError: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := store.Get(tt.keys...)
			if tt.wantError {
				if err == nil {
					t.Errorf("expected error but got nil")
				} else if !contains(err.Error(), tt.errorMsg) {
					t.Errorf("expected error containing %q, got %q", tt.errorMsg, err.Error())
				}
			} else {
				if err != nil {
					t.Errorf("unexpected error: %v", err)
				}
			}
		})
	}
}

// TestMemoryStore_UpdateToNewKey verifies Update creates when no resources exist with key.
func TestMemoryStore_UpdateToNewKey(t *testing.T) {
	store := NewMemoryStore(2)

	resource := map[string]interface{}{
		"metadata": map[string]interface{}{
			"namespace": "default",
			"name":      "new-resource",
		},
		"version": "v1",
	}

	RunUpdateToNewKeyTest(t, store, store, resource, []string{"default", "new-resource"})
}

// TestMemoryStore_DeleteNonExistent verifies deleting non-existent resource is a no-op.
func TestMemoryStore_DeleteNonExistent(t *testing.T) {
	store := NewMemoryStore(2)

	resource := map[string]string{"name": "test"}

	RunDeleteNonExistentTest(t, store, store, resource,
		[]string{"default", "test"},
		[]string{"other", "nonexistent"})
}

// TestMemoryStore_ListRebuildsCache verifies List properly rebuilds when dirty.
func TestMemoryStore_ListRebuildsCache(t *testing.T) {
	store := NewMemoryStore(2)

	// Add multiple resources
	for i := 0; i < 3; i++ {
		resource := map[string]interface{}{
			"metadata": map[string]interface{}{
				"namespace": "default",
				"name":      string(rune('a' + i)),
			},
		}
		if err := store.Add(resource, []string{"default", string(rune('a' + i))}); err != nil {
			t.Fatalf("Add failed: %v", err)
		}
	}

	// First List call builds cache
	results1, err := store.List()
	if err != nil {
		t.Fatalf("List failed: %v", err)
	}
	if len(results1) != 3 {
		t.Errorf("expected 3 results, got %d", len(results1))
	}

	// Second List call uses cache (no dirty flag)
	results2, err := store.List()
	if err != nil {
		t.Fatalf("List failed: %v", err)
	}
	if len(results2) != 3 {
		t.Errorf("expected 3 results, got %d", len(results2))
	}

	// Add another resource (makes dirty)
	resource := map[string]interface{}{
		"metadata": map[string]interface{}{
			"namespace": "default",
			"name":      "d",
		},
	}
	if err := store.Add(resource, []string{"default", "d"}); err != nil {
		t.Fatalf("Add failed: %v", err)
	}

	// Third List call rebuilds cache
	results3, err := store.List()
	if err != nil {
		t.Fatalf("List failed: %v", err)
	}
	if len(results3) != 4 {
		t.Errorf("expected 4 results after add, got %d", len(results3))
	}
}

// TestMemoryStore_NonUniqueKeys verifies multiple resources with same index keys.
// This simulates real-world scenarios like multiple EndpointSlices for a single service.
func TestMemoryStore_NonUniqueKeys(t *testing.T) {
	store := NewMemoryStore(1) // Index by service name only

	// Add multiple EndpointSlices for the same service
	// They share the same index key (service-name) but have different namespace+name
	resources := []map[string]interface{}{
		{
			"metadata": map[string]interface{}{
				"namespace": "default",
				"name":      "nginx-slice-1",
			},
			"endpoints": []string{"10.0.0.1:80"},
		},
		{
			"metadata": map[string]interface{}{
				"namespace": "default",
				"name":      "nginx-slice-2",
			},
			"endpoints": []string{"10.0.0.2:80"},
		},
		{
			"metadata": map[string]interface{}{
				"namespace": "default",
				"name":      "nginx-slice-3",
			},
			"endpoints": []string{"10.0.0.3:80"},
		},
	}

	// Add all resources with the same index key
	for _, res := range resources {
		if err := store.Add(res, []string{"nginx"}); err != nil {
			t.Fatalf("Add failed: %v", err)
		}
	}

	// Verify all 3 resources are stored
	if store.Size() != 3 {
		t.Errorf("expected size 3, got %d", store.Size())
	}

	// Get should return all resources with the same index key
	results, err := store.Get("nginx")
	if err != nil {
		t.Fatalf("Get failed: %v", err)
	}

	if len(results) != 3 {
		t.Fatalf("expected 3 results, got %d", len(results))
	}

	// Verify we got all three slices
	names := make(map[string]bool)
	for _, res := range results {
		r := res.(map[string]interface{})
		metadata := r["metadata"].(map[string]interface{})
		name := metadata["name"].(string)
		names[name] = true
	}

	expectedNames := []string{"nginx-slice-1", "nginx-slice-2", "nginx-slice-3"}
	for _, expectedName := range expectedNames {
		if !names[expectedName] {
			t.Errorf("expected to find resource %q in results", expectedName)
		}
	}
}

// TestMemoryStore_UpdateWithNonUniqueKeys verifies updating specific resource
// when multiple resources share the same index keys.
func TestMemoryStore_UpdateWithNonUniqueKeys(t *testing.T) {
	store := NewMemoryStore(1) // Index by service name only

	// Add two EndpointSlices for the same service
	slice1 := map[string]interface{}{
		"metadata": map[string]interface{}{
			"namespace": "default",
			"name":      "nginx-slice-1",
		},
		"version": "v1",
	}
	slice2 := map[string]interface{}{
		"metadata": map[string]interface{}{
			"namespace": "default",
			"name":      "nginx-slice-2",
		},
		"version": "v1",
	}

	if err := store.Add(slice1, []string{"nginx"}); err != nil {
		t.Fatalf("Add slice1 failed: %v", err)
	}
	if err := store.Add(slice2, []string{"nginx"}); err != nil {
		t.Fatalf("Add slice2 failed: %v", err)
	}

	// Update slice1 specifically
	updatedSlice1 := map[string]interface{}{
		"metadata": map[string]interface{}{
			"namespace": "default",
			"name":      "nginx-slice-1",
		},
		"version": "v2",
	}

	if err := store.Update(updatedSlice1, []string{"nginx"}); err != nil {
		t.Fatalf("Update failed: %v", err)
	}

	// Verify both slices still exist
	results, err := store.Get("nginx")
	if err != nil {
		t.Fatalf("Get failed: %v", err)
	}

	if len(results) != 2 {
		t.Fatalf("expected 2 results after update, got %d", len(results))
	}

	// Find slice1 and verify it was updated
	var foundSlice1 *map[string]interface{}
	var foundSlice2 *map[string]interface{}

	for _, res := range results {
		r := res.(map[string]interface{})
		metadata := r["metadata"].(map[string]interface{})
		name := metadata["name"].(string)

		switch name {
		case "nginx-slice-1":
			foundSlice1 = &r
		case "nginx-slice-2":
			foundSlice2 = &r
		}
	}

	if foundSlice1 == nil {
		t.Fatal("slice1 not found after update")
	}
	if foundSlice2 == nil {
		t.Fatal("slice2 not found after update")
	}

	// Verify slice1 was updated
	if (*foundSlice1)["version"] != "v2" {
		t.Errorf("slice1 version: expected 'v2', got %q", (*foundSlice1)["version"])
	}

	// Verify slice2 was NOT updated
	if (*foundSlice2)["version"] != "v1" {
		t.Errorf("slice2 version: expected 'v1', got %q", (*foundSlice2)["version"])
	}
}

// TestMemoryStore_DeleteWithNonUniqueKeys verifies that Delete removes ALL
// resources matching the provided keys.
func TestMemoryStore_DeleteWithNonUniqueKeys(t *testing.T) {
	store := NewMemoryStore(1)

	// Add multiple resources with the same index key
	slice1 := map[string]interface{}{
		"metadata": map[string]interface{}{
			"namespace": "default",
			"name":      "nginx-slice-1",
		},
	}
	slice2 := map[string]interface{}{
		"metadata": map[string]interface{}{
			"namespace": "default",
			"name":      "nginx-slice-2",
		},
	}

	if err := store.Add(slice1, []string{"nginx"}); err != nil {
		t.Fatalf("Add slice1 failed: %v", err)
	}
	if err := store.Add(slice2, []string{"nginx"}); err != nil {
		t.Fatalf("Add slice2 failed: %v", err)
	}

	// Verify both exist
	if store.Size() != 2 {
		t.Errorf("expected size 2 before delete, got %d", store.Size())
	}

	// Delete all resources with this index key
	if err := store.Delete("nginx"); err != nil {
		t.Fatalf("Delete failed: %v", err)
	}

	// Verify ALL resources with this key were deleted
	if store.Size() != 0 {
		t.Errorf("expected size 0 after delete, got %d", store.Size())
	}

	results, err := store.Get("nginx")
	if err != nil {
		t.Fatalf("Get failed: %v", err)
	}

	if len(results) != 0 {
		t.Errorf("expected 0 results after delete, got %d", len(results))
	}
}

// TestMemoryStore_ListWithNonUniqueKeys verifies List correctly flattens
// all resources when multiple resources share index keys.
func TestMemoryStore_ListWithNonUniqueKeys(t *testing.T) {
	store := NewMemoryStore(1)

	// Add resources for two different services
	// nginx service has 3 slices, apache service has 2 slices
	resources := []struct {
		serviceName string
		sliceName   string
	}{
		{"nginx", "nginx-slice-1"},
		{"nginx", "nginx-slice-2"},
		{"nginx", "nginx-slice-3"},
		{"apache", "apache-slice-1"},
		{"apache", "apache-slice-2"},
	}

	for _, r := range resources {
		resource := map[string]interface{}{
			"metadata": map[string]interface{}{
				"namespace": "default",
				"name":      r.sliceName,
			},
			"service": r.serviceName,
		}
		if err := store.Add(resource, []string{r.serviceName}); err != nil {
			t.Fatalf("Add failed: %v", err)
		}
	}

	// List should return all 5 resources (flattened from the 2 composite keys)
	results, err := store.List()
	if err != nil {
		t.Fatalf("List failed: %v", err)
	}

	if len(results) != 5 {
		t.Fatalf("expected 5 results from List, got %d", len(results))
	}

	// Verify all slice names are present
	names := make(map[string]bool)
	for _, res := range results {
		r := res.(map[string]interface{})
		metadata := r["metadata"].(map[string]interface{})
		name := metadata["name"].(string)
		names[name] = true
	}

	expectedNames := []string{
		"nginx-slice-1", "nginx-slice-2", "nginx-slice-3",
		"apache-slice-1", "apache-slice-2",
	}
	for _, expectedName := range expectedNames {
		if !names[expectedName] {
			t.Errorf("expected to find resource %q in List results", expectedName)
		}
	}
}

// TestMemoryStore_UpdateCreatesIfNotExists verifies Update adds resource
// if it doesn't exist (even with non-unique index keys).
func TestMemoryStore_UpdateCreatesIfNotExists(t *testing.T) {
	store := NewMemoryStore(1)

	// Add one resource
	slice1 := map[string]interface{}{
		"metadata": map[string]interface{}{
			"namespace": "default",
			"name":      "nginx-slice-1",
		},
	}

	if err := store.Add(slice1, []string{"nginx"}); err != nil {
		t.Fatalf("Add failed: %v", err)
	}

	// Update a different resource (doesn't exist yet)
	slice2 := map[string]interface{}{
		"metadata": map[string]interface{}{
			"namespace": "default",
			"name":      "nginx-slice-2",
		},
	}

	if err := store.Update(slice2, []string{"nginx"}); err != nil {
		t.Fatalf("Update failed: %v", err)
	}

	// Verify both resources exist now
	results, err := store.Get("nginx")
	if err != nil {
		t.Fatalf("Get failed: %v", err)
	}

	if len(results) != 2 {
		t.Fatalf("expected 2 results, got %d", len(results))
	}
}

// TestMemoryStore_GetReturnsSortedResults verifies that Get() returns results
// sorted by namespace and name for deterministic ordering.
// This is critical for HAProxy server slot ordering.
func TestMemoryStore_GetReturnsSortedResults(t *testing.T) {
	store := NewMemoryStore(1)

	// Add resources in intentionally random order (by name: c, a, b, e, d)
	// to verify that Get() returns them sorted
	resources := []map[string]interface{}{
		{
			"metadata": map[string]interface{}{
				"namespace": "default",
				"name":      "slice-c",
			},
		},
		{
			"metadata": map[string]interface{}{
				"namespace": "default",
				"name":      "slice-a",
			},
		},
		{
			"metadata": map[string]interface{}{
				"namespace": "default",
				"name":      "slice-b",
			},
		},
		{
			"metadata": map[string]interface{}{
				"namespace": "default",
				"name":      "slice-e",
			},
		},
		{
			"metadata": map[string]interface{}{
				"namespace": "default",
				"name":      "slice-d",
			},
		},
	}

	for _, res := range resources {
		if err := store.Add(res, []string{"nginx"}); err != nil {
			t.Fatalf("Add failed: %v", err)
		}
	}

	// Get all resources - should be sorted by name
	results, err := store.Get("nginx")
	if err != nil {
		t.Fatalf("Get failed: %v", err)
	}

	if len(results) != 5 {
		t.Fatalf("expected 5 results, got %d", len(results))
	}

	// Verify they are in sorted order (a, b, c, d, e)
	expectedOrder := []string{"slice-a", "slice-b", "slice-c", "slice-d", "slice-e"}
	for i, res := range results {
		r := res.(map[string]interface{})
		metadata := r["metadata"].(map[string]interface{})
		name := metadata["name"].(string)
		if name != expectedOrder[i] {
			t.Errorf("position %d: expected %q, got %q", i, expectedOrder[i], name)
		}
	}
}

// TestMemoryStore_GetReturnsSortedResultsPartialMatch verifies that partial
// key matching also returns results in sorted order.
func TestMemoryStore_GetReturnsSortedResultsPartialMatch(t *testing.T) {
	store := NewMemoryStore(2)

	// Add resources across namespaces in random order
	resources := []struct {
		namespace string
		name      string
	}{
		{"default", "z-last"},
		{"alpha", "first"},
		{"beta", "middle"},
		{"default", "a-first"},
		{"alpha", "second"},
	}

	for _, r := range resources {
		res := map[string]interface{}{
			"metadata": map[string]interface{}{
				"namespace": r.namespace,
				"name":      r.name,
			},
		}
		if err := store.Add(res, []string{r.namespace, r.name}); err != nil {
			t.Fatalf("Add failed: %v", err)
		}
	}

	// Get all (partial match with empty returns all via List() behavior, so use namespace)
	// Test partial match: get all in "default" namespace
	results, err := store.Get("default")
	if err != nil {
		t.Fatalf("Get failed: %v", err)
	}

	if len(results) != 2 {
		t.Fatalf("expected 2 results for 'default' namespace, got %d", len(results))
	}

	// Verify sorted order: a-first, z-last
	r0 := results[0].(map[string]interface{})
	r1 := results[1].(map[string]interface{})
	name0 := r0["metadata"].(map[string]interface{})["name"].(string)
	name1 := r1["metadata"].(map[string]interface{})["name"].(string)

	if name0 != "a-first" {
		t.Errorf("position 0: expected 'a-first', got %q", name0)
	}
	if name1 != "z-last" {
		t.Errorf("position 1: expected 'z-last', got %q", name1)
	}
}
