// Copyright 2026 Philipp Hossner
// SPDX-License-Identifier: Apache-2.0

package kindutil

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	k8syaml "k8s.io/apimachinery/pkg/util/yaml"
)

// SetDeploymentFixtureImage replaces exactly one named container across a fixture's Deployments.
func SetDeploymentFixtureImage(manifest []byte, containerName, image string) ([]byte, error) {
	if containerName == "" || image == "" {
		return nil, errors.New("fixture container name and image must not be empty")
	}
	decoder := k8syaml.NewYAMLOrJSONDecoder(bytes.NewReader(manifest), 4096)
	var output bytes.Buffer
	matches := 0
	for {
		var object map[string]any
		if err := decoder.Decode(&object); errors.Is(err, io.EOF) {
			break
		} else if err != nil {
			return nil, fmt.Errorf("decoding image fixture: %w", err)
		}
		if len(object) == 0 {
			continue
		}
		if object["kind"] == "Deployment" {
			count, err := setFixtureContainerImage(object, containerName, image)
			if err != nil {
				return nil, err
			}
			matches += count
		}
		output.WriteString("---\n")
		if err := json.NewEncoder(&output).Encode(object); err != nil {
			return nil, fmt.Errorf("encoding image fixture: %w", err)
		}
	}
	if matches != 1 {
		return nil, fmt.Errorf("fixture container %q matched %d times, want exactly one", containerName, matches)
	}
	return output.Bytes(), nil
}

func setFixtureContainerImage(object map[string]any, containerName, image string) (int, error) {
	path := []string{"spec", "template", "spec", "containers"}
	containers, _, err := unstructured.NestedSlice(object, path...)
	if err != nil {
		return 0, fmt.Errorf("reading fixture containers: %w", err)
	}
	matches := 0
	for _, value := range containers {
		container, ok := value.(map[string]any)
		if !ok {
			return 0, fmt.Errorf("fixture container has invalid type %T", value)
		}
		if container["name"] == containerName {
			container["image"] = image
			matches++
		}
	}
	if err := unstructured.SetNestedSlice(object, containers, path...); err != nil {
		return 0, fmt.Errorf("setting fixture containers: %w", err)
	}
	return matches, nil
}
