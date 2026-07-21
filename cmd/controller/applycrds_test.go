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

package main

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	dynamicfake "k8s.io/client-go/dynamic/fake"
	clienttesting "k8s.io/client-go/testing"
)

func TestLoadCRDs(t *testing.T) {
	dir := t.TempDir()
	// Two CRD files, written out of alphabetical name order to prove sorting.
	writeFile(t, filepath.Join(dir, "b.yaml"), crdYAML("zebra.example.com"))
	writeFile(t, filepath.Join(dir, "a.yaml"), crdYAML("alpha.example.com"))
	// A non-YAML file that must be ignored.
	writeFile(t, filepath.Join(dir, "README.md"), "not yaml")

	crds, err := loadCRDs(dir)
	require.NoError(t, err)
	require.Len(t, crds, 2)
	// Sorted by metadata.name regardless of filename.
	assert.Equal(t, "alpha.example.com", crds[0].GetName())
	assert.Equal(t, "zebra.example.com", crds[1].GetName())
}

func TestLoadCRDs_MultiDocFile(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "crds.yaml"),
		crdYAML("one.example.com")+"\n---\n"+crdYAML("two.example.com")+"\n---\n")

	crds, err := loadCRDs(dir)
	require.NoError(t, err)
	require.Len(t, crds, 2)
	assert.Equal(t, "one.example.com", crds[0].GetName())
	assert.Equal(t, "two.example.com", crds[1].GetName())
}

func TestLoadCRDs_RejectsNonCRD(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "config.yaml"),
		"apiVersion: v1\nkind: ConfigMap\nmetadata:\n  name: oops\n")

	_, err := loadCRDs(dir)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "non-CRD document")
}

func TestApplyCRD_ServerSideApply(t *testing.T) {
	crd := &unstructured.Unstructured{}
	require.NoError(t, crd.UnmarshalJSON([]byte(crdJSONWithStatus(t, "test.example.com"))))

	fake := dynamicfake.NewSimpleDynamicClient(runtime.NewScheme())
	var captured clienttesting.PatchAction
	fake.PrependReactor("patch", "customresourcedefinitions",
		func(a clienttesting.Action) (bool, runtime.Object, error) {
			captured = a.(clienttesting.PatchAction)
			return true, &unstructured.Unstructured{}, nil
		})

	require.NoError(t, applyCRD(context.Background(), fake, crd))

	require.NotNil(t, captured)
	assert.Equal(t, types.ApplyPatchType, captured.GetPatchType(), "must use server-side apply")
	assert.Equal(t, "test.example.com", captured.GetName())

	// The payload must carry the spec but NOT status/creationTimestamp: the API
	// server owns CRD status (storedVersions); applying an empty one clobbers it.
	var payload map[string]any
	require.NoError(t, json.Unmarshal(captured.GetPatch(), &payload))
	assert.Contains(t, payload, "spec")
	assert.NotContains(t, payload, "status")
	meta := payload["metadata"].(map[string]any)
	assert.NotContains(t, meta, "creationTimestamp")
}

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	require.NoError(t, os.WriteFile(path, []byte(content), 0o600))
}

func crdYAML(name string) string {
	return "apiVersion: apiextensions.k8s.io/v1\n" +
		"kind: CustomResourceDefinition\n" +
		"metadata:\n" +
		"  name: " + name + "\n" +
		"spec:\n" +
		"  group: example.com\n"
}

func crdJSONWithStatus(t *testing.T, name string) string {
	t.Helper()
	obj := map[string]any{
		"apiVersion": "apiextensions.k8s.io/v1",
		"kind":       crdKind,
		"metadata": map[string]any{
			"name":              name,
			"creationTimestamp": nil,
		},
		"spec": map[string]any{"group": "example.com"},
		"status": map[string]any{
			"storedVersions": []any{"v1alpha1"},
			"acceptedNames":  map[string]any{"kind": "Thing"},
		},
	}
	b, err := json.Marshal(obj)
	require.NoError(t, err)
	return string(b)
}
