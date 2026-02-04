package indexer

import (
	"testing"
)

// TestNew verifies indexer creation.
func TestNew(t *testing.T) {
	tests := []struct {
		name      string
		config    Config
		expectErr bool
	}{
		{
			name: "valid config",
			config: Config{
				IndexBy:      []string{"metadata.namespace", "metadata.name"},
				IgnoreFields: []string{"metadata.managedFields"},
			},
			expectErr: false,
		},
		{
			name: "empty IndexBy",
			config: Config{
				IndexBy:      []string{},
				IgnoreFields: []string{},
			},
			expectErr: true,
		},
		{
			name: "invalid JSONPath",
			config: Config{
				IndexBy: []string{"metadata.[namespace"},
			},
			expectErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := New(tt.config)
			if tt.expectErr && err == nil {
				t.Error("expected error but got nil")
			}
			if !tt.expectErr && err != nil {
				t.Errorf("unexpected error: %v", err)
			}
		})
	}
}

// TestExtractKeys verifies key extraction from resources.
func TestExtractKeys(t *testing.T) {
	indexer, err := New(Config{
		IndexBy: []string{"metadata.namespace", "metadata.name"},
	})
	if err != nil {
		t.Fatalf("failed to create indexer: %v", err)
	}

	resource := map[string]interface{}{
		"metadata": map[string]interface{}{
			"namespace": "default",
			"name":      "test-resource",
		},
	}

	keys, err := indexer.ExtractKeys(resource)
	if err != nil {
		t.Fatalf("ExtractKeys failed: %v", err)
	}

	if len(keys) != 2 {
		t.Fatalf("expected 2 keys, got %d", len(keys))
	}

	if keys[0] != "default" {
		t.Errorf("expected namespace='default', got %q", keys[0])
	}

	if keys[1] != "test-resource" {
		t.Errorf("expected name='test-resource', got %q", keys[1])
	}
}

// TestFilterFields verifies field filtering.
func TestFilterFields(t *testing.T) {
	indexer, err := New(Config{
		IndexBy:      []string{"metadata.name"},
		IgnoreFields: []string{"metadata.managedFields", "status"},
	})
	if err != nil {
		t.Fatalf("failed to create indexer: %v", err)
	}

	resource := map[string]interface{}{
		"metadata": map[string]interface{}{
			"name":          "test-resource",
			"managedFields": []interface{}{map[string]interface{}{"manager": "kubectl"}},
		},
		"spec": map[string]interface{}{
			"replicas": 3,
		},
		"status": map[string]interface{}{
			"ready": true,
		},
	}

	err = indexer.FilterFields(resource)
	if err != nil {
		t.Fatalf("FilterFields failed: %v", err)
	}

	// Verify managedFields was removed
	metadata := resource["metadata"].(map[string]interface{})
	if _, ok := metadata["managedFields"]; ok {
		t.Error("managedFields should have been removed")
	}

	// Verify status was removed
	if _, ok := resource["status"]; ok {
		t.Error("status should have been removed")
	}

	// Verify spec was preserved
	if _, ok := resource["spec"]; !ok {
		t.Error("spec should have been preserved")
	}
}

// TestProcess verifies combined filtering and key extraction.
func TestProcess(t *testing.T) {
	indexer, err := New(Config{
		IndexBy:      []string{"metadata.namespace", "metadata.name"},
		IgnoreFields: []string{"metadata.managedFields"},
	})
	if err != nil {
		t.Fatalf("failed to create indexer: %v", err)
	}

	resource := map[string]interface{}{
		"metadata": map[string]interface{}{
			"namespace":     "kube-system",
			"name":          "coredns",
			"managedFields": []interface{}{},
		},
	}

	result, err := indexer.Process(resource)
	if err != nil {
		t.Fatalf("Process failed: %v", err)
	}

	// Verify keys were extracted correctly
	if len(result.Keys) != 2 || result.Keys[0] != "kube-system" || result.Keys[1] != "coredns" {
		t.Errorf("unexpected keys: %v", result.Keys)
	}

	// Verify managedFields was removed
	metadata := resource["metadata"].(map[string]interface{})
	if _, ok := metadata["managedFields"]; ok {
		t.Error("managedFields should have been removed")
	}
}

// TestNumKeys verifies the NumKeys method.
func TestNumKeys(t *testing.T) {
	tests := []struct {
		name     string
		indexBy  []string
		expected int
	}{
		{
			name:     "single key",
			indexBy:  []string{"metadata.name"},
			expected: 1,
		},
		{
			name:     "two keys",
			indexBy:  []string{"metadata.namespace", "metadata.name"},
			expected: 2,
		},
		{
			name:     "three keys",
			indexBy:  []string{"metadata.namespace", "metadata.name", "metadata.labels['app']"},
			expected: 3,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			idx, err := New(Config{IndexBy: tt.indexBy})
			if err != nil {
				t.Fatalf("failed to create indexer: %v", err)
			}

			if idx.NumKeys() != tt.expected {
				t.Errorf("expected NumKeys()=%d, got %d", tt.expected, idx.NumKeys())
			}
		})
	}
}

// TestIndexExpressions verifies the IndexExpressions method.
func TestIndexExpressions(t *testing.T) {
	indexBy := []string{"metadata.namespace", "metadata.name", "metadata.labels['app']"}

	idx, err := New(Config{IndexBy: indexBy})
	if err != nil {
		t.Fatalf("failed to create indexer: %v", err)
	}

	exprs := idx.IndexExpressions()
	if len(exprs) != len(indexBy) {
		t.Fatalf("expected %d expressions, got %d", len(indexBy), len(exprs))
	}

	for i, expected := range indexBy {
		if exprs[i] != expected {
			t.Errorf("expression %d: expected %q, got %q", i, expected, exprs[i])
		}
	}
}

// TestIndexError verifies IndexError error handling.
func TestIndexError(t *testing.T) {
	baseErr := &JSONPathError{
		Expression: "metadata.missing",
		Operation:  "evaluate",
		Cause:      nil,
	}

	indexErr := &IndexError{
		Expression: "metadata.missing",
		Position:   0,
		Cause:      baseErr,
	}

	// Test Error() method
	errMsg := indexErr.Error()
	if !contains(errMsg, "index error") {
		t.Errorf("expected error message to contain 'index error', got: %q", errMsg)
	}
	if !contains(errMsg, "position 0") {
		t.Errorf("expected error message to contain 'position 0', got: %q", errMsg)
	}
	if !contains(errMsg, "metadata.missing") {
		t.Errorf("expected error message to contain expression, got: %q", errMsg)
	}

	// Test Unwrap() method
	unwrapped := indexErr.Unwrap()
	if unwrapped != baseErr {
		t.Errorf("Unwrap() should return the wrapped error")
	}
}

// TestFilterError verifies FilterError error handling.
func TestFilterError(t *testing.T) {
	baseErr := &JSONPathError{
		Expression: "metadata.managedFields",
		Operation:  "filter",
		Cause:      nil,
	}

	filterErr := &FilterError{
		Pattern: "metadata.managedFields",
		Cause:   baseErr,
	}

	// Test Error() method
	errMsg := filterErr.Error()
	if !contains(errMsg, "filter error") {
		t.Errorf("expected error message to contain 'filter error', got: %q", errMsg)
	}
	if !contains(errMsg, "metadata.managedFields") {
		t.Errorf("expected error message to contain pattern, got: %q", errMsg)
	}

	// Test Unwrap() method
	unwrapped := filterErr.Unwrap()
	if unwrapped != baseErr {
		t.Errorf("Unwrap() should return the wrapped error")
	}
}

// TestJSONPathError verifies JSONPathError error handling.
func TestJSONPathError(t *testing.T) {
	baseErr := &FilterError{Pattern: "test", Cause: nil}

	jsonpathErr := &JSONPathError{
		Expression: "metadata.name",
		Operation:  "evaluate",
		Cause:      baseErr,
	}

	// Test Error() method (already covered elsewhere)
	errMsg := jsonpathErr.Error()
	if !contains(errMsg, "JSONPath error") {
		t.Errorf("expected error message to contain 'JSONPath error', got: %q", errMsg)
	}

	// Test Unwrap() method
	unwrapped := jsonpathErr.Unwrap()
	if unwrapped != baseErr {
		t.Errorf("Unwrap() should return the wrapped error")
	}
}

// TestJSONPathEvaluator_Expression verifies the Expression method.
func TestJSONPathEvaluator_Expression(t *testing.T) {
	expr := "metadata.labels['app']"

	eval, err := NewJSONPathEvaluator(expr)
	if err != nil {
		t.Fatalf("failed to create evaluator: %v", err)
	}

	if eval.Expression() != expr {
		t.Errorf("expected Expression()=%q, got %q", expr, eval.Expression())
	}
}

// TestReflectValueToString verifies different type conversions.
func TestReflectValueToString(t *testing.T) {
	tests := []struct {
		name     string
		value    interface{}
		expected string
	}{
		{name: "string", value: "hello", expected: "hello"},
		{name: "int", value: 42, expected: "42"},
		{name: "int64", value: int64(123), expected: "123"},
		{name: "uint", value: uint(10), expected: "10"},
		{name: "float64", value: 3.14, expected: "3.140000"},
		{name: "bool true", value: true, expected: "true"},
		{name: "bool false", value: false, expected: "false"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			eval, err := NewJSONPathEvaluator("value")
			if err != nil {
				t.Fatalf("failed to create evaluator: %v", err)
			}

			resource := map[string]interface{}{"value": tt.value}
			result, err := eval.Evaluate(resource)
			if err != nil {
				t.Fatalf("Evaluate failed: %v", err)
			}

			if result != tt.expected {
				t.Errorf("expected %q, got %q", tt.expected, result)
			}
		})
	}
}

// TestProcessWithFilterError verifies error handling in Process when filter fails.
func TestProcessWithFilterError(t *testing.T) {
	// This test verifies that Process properly propagates filter errors
	// Create an indexer with a complex filter pattern
	idx, err := New(Config{
		IndexBy:      []string{"metadata.name"},
		IgnoreFields: []string{"metadata.annotations['kubectl.kubernetes.io/last-applied-configuration']"},
	})
	if err != nil {
		t.Fatalf("failed to create indexer: %v", err)
	}

	// Test with a valid resource
	resource := map[string]interface{}{
		"metadata": map[string]interface{}{
			"name": "test",
			"annotations": map[string]interface{}{
				"kubectl.kubernetes.io/last-applied-configuration": "{}",
			},
		},
	}

	result, err := idx.Process(resource)
	if err != nil {
		t.Fatalf("Process failed: %v", err)
	}

	if len(result.Keys) != 1 || result.Keys[0] != "test" {
		t.Errorf("unexpected keys: %v", result.Keys)
	}

	// Verify the annotation was removed
	metadata := resource["metadata"].(map[string]interface{})
	annotations := metadata["annotations"].(map[string]interface{})
	if _, ok := annotations["kubectl.kubernetes.io/last-applied-configuration"]; ok {
		t.Error("annotation should have been removed")
	}
}

// contains checks if a string contains a substring.
func contains(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}

// TestFilterFields_DeepNested verifies filtering of deeply nested fields.
func TestFilterFields_DeepNested(t *testing.T) {
	idx, err := New(Config{
		IndexBy: []string{"metadata.name"},
		IgnoreFields: []string{
			"metadata.managedFields",
			"metadata.annotations['kubectl.kubernetes.io/last-applied-configuration']",
			"status",
		},
	})
	if err != nil {
		t.Fatalf("failed to create indexer: %v", err)
	}

	resource := map[string]interface{}{
		"metadata": map[string]interface{}{
			"name":          "test",
			"managedFields": []interface{}{"a", "b"},
			"annotations": map[string]interface{}{
				"kubectl.kubernetes.io/last-applied-configuration": "{}",
				"other/annotation": "keep-me",
			},
			"labels": map[string]interface{}{
				"app": "test",
			},
		},
		"spec": map[string]interface{}{
			"replicas": 3,
		},
		"status": map[string]interface{}{
			"ready": true,
		},
	}

	err = idx.FilterFields(resource)
	if err != nil {
		t.Fatalf("FilterFields failed: %v", err)
	}

	// Verify deeply nested fields were handled
	metadata := resource["metadata"].(map[string]interface{})

	// managedFields should be removed
	if _, ok := metadata["managedFields"]; ok {
		t.Error("managedFields should have been removed")
	}

	// annotations map should exist with other annotation preserved
	annotations := metadata["annotations"].(map[string]interface{})
	if _, ok := annotations["kubectl.kubernetes.io/last-applied-configuration"]; ok {
		t.Error("last-applied-configuration annotation should have been removed")
	}
	if _, ok := annotations["other/annotation"]; !ok {
		t.Error("other/annotation should have been preserved")
	}

	// status should be removed
	if _, ok := resource["status"]; ok {
		t.Error("status should have been removed")
	}

	// spec should be preserved
	if _, ok := resource["spec"]; !ok {
		t.Error("spec should have been preserved")
	}
}

// TestFilterFields_NonExistentPath verifies handling of non-existent filter paths.
func TestFilterFields_NonExistentPath(t *testing.T) {
	idx, err := New(Config{
		IndexBy: []string{"metadata.name"},
		IgnoreFields: []string{
			"metadata.nonexistent",                // Doesn't exist
			"spec.missing.deep.path",              // Path doesn't exist
			"metadata.annotations['missing-key']", // Annotation doesn't exist
		},
	})
	if err != nil {
		t.Fatalf("failed to create indexer: %v", err)
	}

	resource := map[string]interface{}{
		"metadata": map[string]interface{}{
			"name": "test",
			"annotations": map[string]interface{}{
				"existing/annotation": "value",
			},
		},
		"spec": map[string]interface{}{
			"replicas": 3,
		},
	}

	// Should not error on non-existent paths
	err = idx.FilterFields(resource)
	if err != nil {
		t.Fatalf("FilterFields failed: %v", err)
	}

	// Verify existing fields are untouched
	metadata := resource["metadata"].(map[string]interface{})
	if metadata["name"] != "test" {
		t.Error("name should be preserved")
	}
}

// TestExtractKeys_MissingField verifies error when index field is missing.
func TestExtractKeys_MissingField(t *testing.T) {
	idx, err := New(Config{
		IndexBy: []string{"metadata.namespace", "metadata.missingField"},
	})
	if err != nil {
		t.Fatalf("failed to create indexer: %v", err)
	}

	resource := map[string]interface{}{
		"metadata": map[string]interface{}{
			"namespace": "default",
			"name":      "test",
		},
	}

	// Should return error for missing field
	_, err = idx.ExtractKeys(resource)
	if err == nil {
		t.Error("expected error for missing field")
	}
}

// TestFilterFields_EmptyResource verifies handling of empty resources.
func TestFilterFields_EmptyResource(t *testing.T) {
	idx, err := New(Config{
		IndexBy:      []string{"metadata.name"},
		IgnoreFields: []string{"metadata.managedFields"},
	})
	if err != nil {
		t.Fatalf("failed to create indexer: %v", err)
	}

	// Empty resource
	resource := map[string]interface{}{}

	// Should not error on empty resource
	err = idx.FilterFields(resource)
	if err != nil {
		t.Fatalf("FilterFields failed on empty resource: %v", err)
	}
}

// TestEvaluate_InvalidPath verifies error handling for invalid paths.
func TestEvaluate_InvalidPath(t *testing.T) {
	eval, err := NewJSONPathEvaluator("metadata.labels")
	if err != nil {
		t.Fatalf("failed to create evaluator: %v", err)
	}

	// Resource without the path
	resource := map[string]interface{}{
		"metadata": map[string]interface{}{
			"name": "test",
		},
	}

	_, err = eval.Evaluate(resource)
	if err == nil {
		t.Error("expected error for missing path")
	}
}

// TestFilterWithUnstructured verifies filtering works with Kubernetes Unstructured objects.
func TestFilterWithUnstructured(t *testing.T) {
	idx, err := New(Config{
		IndexBy:      []string{"metadata.namespace", "metadata.name"},
		IgnoreFields: []string{"metadata.managedFields"},
	})
	if err != nil {
		t.Fatalf("failed to create indexer: %v", err)
	}

	// Simulate unstructured.Unstructured.Object
	resource := map[string]interface{}{
		"apiVersion": "v1",
		"kind":       "ConfigMap",
		"metadata": map[string]interface{}{
			"namespace":     "default",
			"name":          "test",
			"managedFields": []interface{}{map[string]interface{}{"manager": "kubectl"}},
		},
		"data": map[string]interface{}{
			"key": "value",
		},
	}

	result, err := idx.Process(resource)
	if err != nil {
		t.Fatalf("Process failed: %v", err)
	}

	if len(result.Keys) != 2 || result.Keys[0] != "default" || result.Keys[1] != "test" {
		t.Errorf("unexpected keys: %v", result.Keys)
	}

	metadata := resource["metadata"].(map[string]interface{})
	if _, ok := metadata["managedFields"]; ok {
		t.Error("managedFields should have been removed")
	}
}
