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
	"encoding/json"
	"errors"
	"fmt"
	"io"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/util/yaml"
)

// ControllerScale is the requested controller size before staged installation.
type ControllerScale struct {
	Name      string `json:"name"`
	Namespace string `json:"namespace"`
	Replicas  int64  `json:"replicas"`
}

// StageControllerStartup changes only the controller Deployment's initial size.
func StageControllerStartup(manifests []byte) ([]byte, ControllerScale, error) {
	decoder := yaml.NewYAMLOrJSONDecoder(bytes.NewReader(manifests), 4096)
	var output bytes.Buffer
	var target ControllerScale
	for {
		object := &unstructured.Unstructured{}
		if err := decoder.Decode(object); err != nil {
			if errors.Is(err, io.EOF) {
				break
			}
			return nil, ControllerScale{}, fmt.Errorf("decode Helm manifest: %w", err)
		}
		if len(object.Object) == 0 {
			continue
		}
		if err := stageControllerObject(object, &target); err != nil {
			return nil, ControllerScale{}, err
		}
		encoded, err := json.Marshal(object.Object)
		if err != nil {
			return nil, ControllerScale{}, fmt.Errorf("encode Helm manifest: %w", err)
		}
		output.Write(encoded)
		output.WriteString("\n---\n")
	}
	if target.Name == "" {
		return nil, ControllerScale{}, errors.New("helm manifest has no controller Deployment")
	}
	return output.Bytes(), target, nil
}

func stageControllerObject(object *unstructured.Unstructured, target *ControllerScale) error {
	if object.GetKind() != "Deployment" || object.GetLabels()["app.kubernetes.io/component"] != "controller" {
		return nil
	}
	if target.Name != "" {
		return errors.New("staged startup requires exactly one controller Deployment")
	}
	replicas, found, err := unstructured.NestedInt64(object.Object, "spec", "replicas")
	if err != nil {
		return fmt.Errorf("read controller replicas: %w", err)
	}
	if !found {
		replicas = 1
	}
	if object.GetName() == "" || replicas < 1 {
		return errors.New("controller Deployment needs a name and at least one replica")
	}
	*target = ControllerScale{Name: object.GetName(), Namespace: object.GetNamespace(), Replicas: replicas}
	return unstructured.SetNestedField(object.Object, int64(1), "spec", "replicas")
}
