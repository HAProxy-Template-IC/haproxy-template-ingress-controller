package crdwatch

import "k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"

func projectDefinition(obj any) (any, error) {
	definition, ok := obj.(*unstructured.Unstructured)
	if !ok || definition == nil {
		return obj, nil
	}
	group, _, _ := unstructured.NestedString(definition.Object, "spec", "group")
	// Reload decisions use generation; schema resolution fetches its own current definitions.
	return &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": definition.GetAPIVersion(),
		"kind":       definition.GetKind(),
		"metadata": map[string]any{
			"name":            definition.GetName(),
			"namespace":       definition.GetNamespace(),
			"uid":             string(definition.GetUID()),
			"resourceVersion": definition.GetResourceVersion(),
			"generation":      definition.GetGeneration(),
		},
		"spec": map[string]any{"group": group},
	}}, nil
}
