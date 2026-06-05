package watcher

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"

	"gitlab.com/haproxy-haptic/haptic/pkg/k8s/indexer"
)

func TestProjectionRoots(t *testing.T) {
	tests := []struct {
		name          string
		indexBy       []string
		fieldSelector string
		wantContains  []string
		wantAbsent    []string
	}{
		{
			name:         "secrets by namespace/name",
			indexBy:      []string{"metadata.namespace", "metadata.name"},
			wantContains: []string{"apiVersion", "kind", "metadata"},
			wantAbsent:   []string{"data", "spec", "status"},
		},
		{
			name:          "field selector adds its root",
			indexBy:       []string{"metadata.namespace", "metadata.name"},
			fieldSelector: "spec.ingressClassName=haproxy",
			wantContains:  []string{"apiVersion", "kind", "metadata", "spec"},
			wantAbsent:    []string{"status", "data"},
		},
		{
			name:         "spec-indexed CRD retains spec",
			indexBy:      []string{"spec.foo", "metadata.name"},
			wantContains: []string{"apiVersion", "kind", "metadata", "spec"},
			wantAbsent:   []string{"status"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			roots := projectionRoots(tt.indexBy, tt.fieldSelector)
			for _, k := range tt.wantContains {
				if !roots[k] {
					t.Errorf("projectionRoots missing expected root %q (got %v)", k, roots)
				}
			}
			for _, k := range tt.wantAbsent {
				if roots[k] {
					t.Errorf("projectionRoots unexpectedly retains %q (got %v)", k, roots)
				}
			}
		})
	}
}

func TestProjectionTransform_StripsHeavyFields(t *testing.T) {
	roots := projectionRoots([]string{"metadata.namespace", "metadata.name"}, "")
	transform := newProjectionTransform(roots, nil)

	full := &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": "v1",
		"kind":       "Secret",
		"metadata": map[string]any{
			"namespace":       "default",
			"name":            "tls-cert",
			"resourceVersion": "123",
			"labels":          map[string]any{"app": "web"},
		},
		"type": "kubernetes.io/tls",
		"data": map[string]any{
			"tls.crt": "BIGBASE64CERT",
			"tls.key": "BIGBASE64KEY",
		},
	}}

	out, err := transform(full)
	if err != nil {
		t.Fatalf("transform returned error: %v", err)
	}
	projected, ok := out.(*unstructured.Unstructured)
	if !ok {
		t.Fatalf("transform returned %T, want *unstructured.Unstructured", out)
	}

	// Identity / index fields survive.
	if projected.GetName() != "tls-cert" {
		t.Errorf("GetName() = %q, want tls-cert", projected.GetName())
	}
	if projected.GetNamespace() != "default" {
		t.Errorf("GetNamespace() = %q, want default", projected.GetNamespace())
	}
	if projected.GetResourceVersion() != "123" {
		t.Errorf("GetResourceVersion() = %q, want 123", projected.GetResourceVersion())
	}
	if _, ok := projected.Object["apiVersion"]; !ok {
		t.Error("projected object dropped apiVersion")
	}
	if _, ok := projected.Object["kind"]; !ok {
		t.Error("projected object dropped kind")
	}

	// The heavy, non-indexed fields are gone.
	if _, present := projected.Object["data"]; present {
		t.Error("projected object still carries data (the heavy field that should be stripped)")
	}
	if _, present := projected.Object["type"]; present {
		t.Error("projected object still carries type (non-retained field)")
	}
}

// Retaining the whole `metadata` block would keep its heaviest payloads —
// metadata.managedFields and the last-applied-configuration annotation (which
// duplicates the entire applied object, including the data the projection is
// meant to drop). The transform must apply the watcher's IgnoreFields to the
// husk so those are stripped too.
func TestProjectionTransform_StripsIgnoredMetadataFields(t *testing.T) {
	idx, err := indexer.New(indexer.Config{
		IndexBy: []string{"metadata.namespace", "metadata.name"},
		IgnoreFields: []string{
			"metadata.managedFields",
			"metadata.annotations['kubectl.kubernetes.io/last-applied-configuration']",
		},
	})
	require.NoError(t, err)

	transform := newProjectionTransform(projectionRoots([]string{"metadata.namespace", "metadata.name"}, ""), idx)

	full := &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": "v1",
		"kind":       "Secret",
		"metadata": map[string]any{
			"namespace":     "default",
			"name":          "tls-cert",
			"managedFields": []any{map[string]any{"manager": "kubectl", "operation": "Apply"}},
			"annotations": map[string]any{
				"kubectl.kubernetes.io/last-applied-configuration": `{"data":{"tls.crt":"HUGEBASE64DUPLICATEDBODY"}}`,
				"keep-me": "yes",
			},
		},
		"data": map[string]any{"tls.crt": "HUGEBASE64"},
	}}

	out, err := transform(full)
	require.NoError(t, err)
	h, ok := out.(*unstructured.Unstructured)
	require.True(t, ok)

	// Top-level heavy field dropped by projection.
	if _, has, _ := unstructured.NestedMap(h.Object, "data"); has {
		t.Error("husk should drop top-level data")
	}
	// managedFields stripped from the retained metadata.
	if _, has, _ := unstructured.NestedSlice(h.Object, "metadata", "managedFields"); has {
		t.Error("husk should strip metadata.managedFields")
	}
	// last-applied annotation stripped, but unrelated annotations retained.
	ann, _, _ := unstructured.NestedStringMap(h.Object, "metadata", "annotations")
	if _, has := ann["kubectl.kubernetes.io/last-applied-configuration"]; has {
		t.Error("husk should strip the last-applied-configuration annotation (it duplicates the body)")
	}
	assert.Equal(t, "yes", ann["keep-me"], "unrelated annotations must be retained")
	// Identity survives.
	assert.Equal(t, "tls-cert", h.GetName())
	assert.Equal(t, "default", h.GetNamespace())
}

func TestProjectionTransform_PassesThroughNonUnstructured(t *testing.T) {
	transform := newProjectionTransform(projectionRoots([]string{"metadata.name"}, ""), nil)

	in := "not an unstructured object"
	out, err := transform(in)
	if err != nil {
		t.Fatalf("transform returned error: %v", err)
	}
	if out != in {
		t.Errorf("transform mutated non-unstructured input: got %v", out)
	}
}
