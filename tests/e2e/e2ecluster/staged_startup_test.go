//go:build e2e

// Copyright 2026 Philipp Hossner
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

package e2ecluster

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"testing"

	"github.com/stretchr/testify/require"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/util/yaml"
)

func TestStageControllerStartupPreservesRequestedManifests(t *testing.T) {
	for _, replicas := range []int64{1, 2, 3} {
		t.Run(fmt.Sprint(replicas), func(t *testing.T) {
			manifest := fmt.Sprintf(`apiVersion: apps/v1
kind: Deployment
metadata:
  name: haptic-controller
  namespace: test
spec:
  replicas: %d
  template:
    metadata:
      labels:
        app.kubernetes.io/component: controller
    spec:
      containers:
        - name: controller
          image: haptic:test
---
apiVersion: apps/v1
kind: Deployment
metadata:
  name: other
spec:
  replicas: 7
---
apiVersion: v1
kind: Secret
metadata:
  name: credentials
data:
  password: dGVzdA==
`, replicas)
			staged, target, err := StageControllerStartup([]byte(manifest))
			require.NoError(t, err)
			require.Equal(t, ControllerScale{Name: "haptic-controller", Namespace: "test", Replicas: replicas}, target)
			before := decodeStartupManifests(t, []byte(manifest))
			after := decodeStartupManifests(t, staged)
			require.NoError(t, unstructured.SetNestedField(before[0], int64(1), "spec", "replicas"))
			require.Equal(t, before, after)
		})
	}
}

func TestStageControllerStartupRejectsAmbiguousOrInvalidInput(t *testing.T) {
	controller := `{"kind":"Deployment","metadata":{"name":"controller"},"spec":{"replicas":2,"template":{"metadata":{"labels":{"app.kubernetes.io/component":"controller"}}}}}`
	for name, manifest := range map[string]string{
		"deployment label alone": `{"kind":"Deployment","metadata":{"name":"controller","labels":{"app.kubernetes.io/component":"controller"}},"spec":{"replicas":2}}`,
		"empty":                  "",
		"invalid YAML":           "kind: [",
		"no controller":          "kind: Secret",
		"duplicate controller":   controller + "\n---\n" + controller,
		"zero replicas":          `{"kind":"Deployment","metadata":{"name":"controller"},"spec":{"replicas":0,"template":{"metadata":{"labels":{"app.kubernetes.io/component":"controller"}}}}}`,
		"invalid replicas":       `{"kind":"Deployment","metadata":{"name":"controller"},"spec":{"replicas":"two","template":{"metadata":{"labels":{"app.kubernetes.io/component":"controller"}}}}}`,
	} {
		t.Run(name, func(t *testing.T) {
			output, _, err := StageControllerStartup([]byte(manifest))
			require.Error(t, err)
			require.Empty(t, output)
		})
	}
}

func decodeStartupManifests(t *testing.T, encoded []byte) []map[string]any {
	t.Helper()
	decoder := yaml.NewYAMLOrJSONDecoder(bytes.NewReader(encoded), 4096)
	var objects []map[string]any
	for {
		object := &unstructured.Unstructured{}
		err := decoder.Decode(object)
		if errors.Is(err, io.EOF) {
			return objects
		}
		require.NoError(t, err)
		if len(object.Object) > 0 {
			objects = append(objects, object.Object)
		}
	}
}
