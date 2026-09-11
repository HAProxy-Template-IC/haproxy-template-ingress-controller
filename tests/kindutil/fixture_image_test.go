// Copyright 2026 Philipp Hossner
// SPDX-License-Identifier: Apache-2.0

package kindutil

import (
	"bytes"
	"errors"
	"io"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	k8syaml "k8s.io/apimachinery/pkg/util/yaml"

	devassets "gitlab.com/haproxy-haptic/haptic/scripts/dev-env-assets"
)

func TestSetDeploymentFixtureImagePreservesOtherFields(t *testing.T) {
	for name, manifest := range map[string][]byte{
		"demo": devassets.HAProxyDemoBackendYAML,
		"test": devassets.HAProxyTestBackendYAML,
		"sidecar": []byte(`kind: Deployment
spec:
  template:
    spec:
      containers:
        - {name: haproxy, image: previous:version}
        - {name: observer, image: previous:version}
---
kind: ConfigMap
data: {config: "image: previous:version"}
`),
	} {
		t.Run(name, func(t *testing.T) {
			updated, err := SetDeploymentFixtureImage(manifest, "haproxy", "example.test/haproxy:chart")
			require.NoError(t, err)
			before := fixtureObjects(t, manifest)
			matches := 0
			for _, original := range before {
				if original["kind"] != "Deployment" {
					continue
				}
				containers := original["spec"].(map[string]any)["template"].(map[string]any)["spec"].(map[string]any)["containers"].([]any)
				for _, value := range containers {
					container := value.(map[string]any)
					if container["name"] == "haproxy" {
						container["image"] = "example.test/haproxy:chart"
						matches++
					}
				}
			}
			assert.Equal(t, 1, matches)
			assert.Equal(t, before, fixtureObjects(t, updated))
		})
	}
}

func fixtureObjects(t *testing.T, manifest []byte) []map[string]any {
	t.Helper()
	decoder := k8syaml.NewYAMLOrJSONDecoder(bytes.NewReader(manifest), 4096)
	var objects []map[string]any
	for {
		var object map[string]any
		err := decoder.Decode(&object)
		if errors.Is(err, io.EOF) {
			return objects
		}
		require.NoError(t, err)
		if len(object) > 0 {
			objects = append(objects, object)
		}
	}
}

func TestSetDeploymentFixtureImageRejectsInvalidSelection(t *testing.T) {
	for name, manifest := range map[string][]byte{
		"absent":     []byte("kind: Service\n"),
		"ambiguous":  bytes.Join([][]byte{devassets.HAProxyDemoBackendYAML, devassets.HAProxyTestBackendYAML}, []byte("\n---\n")),
		"later YAML": append(bytes.Clone(devassets.HAProxyDemoBackendYAML), []byte("\n---\ninvalid: [\n")...),
		"containers": []byte("kind: Deployment\nspec: {template: {spec: {containers: invalid}}}"),
		"container":  []byte("kind: Deployment\nspec: {template: {spec: {containers: [invalid]}}}"),
	} {
		t.Run(name, func(t *testing.T) {
			_, err := SetDeploymentFixtureImage(manifest, "haproxy", "example.test/haproxy:chart")
			require.Error(t, err)
		})
	}
	_, err := SetDeploymentFixtureImage(devassets.HAProxyDemoBackendYAML, "haproxy", "")
	require.Error(t, err)
	_, err = SetDeploymentFixtureImage(devassets.HAProxyDemoBackendYAML, "", "image")
	require.Error(t, err)
}
