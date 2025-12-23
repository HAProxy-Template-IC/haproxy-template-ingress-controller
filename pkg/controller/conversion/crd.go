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

package conversion

import (
	"fmt"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"

	"haptic/pkg/apis/haproxytemplate/v1alpha1"
	"haptic/pkg/core/config"
)

// ParseCRD converts an unstructured HAProxyTemplateConfig CRD to typed structs.
//
// This function validates the resource type, converts the unstructured resource
// to a typed HAProxyTemplateConfig CRD, and then converts the CRD Spec to a
// config.Config for validation and rendering.
//
// Returns:
//   - *config.Config: Parsed configuration for validation and rendering
//   - *v1alpha1.HAProxyTemplateConfig: Original CRD for Kubernetes metadata (name, namespace, UID)
//   - error: Validation or conversion failure
func ParseCRD(resource *unstructured.Unstructured) (*config.Config, *v1alpha1.HAProxyTemplateConfig, error) {
	// Validate resource type
	apiVersion := resource.GetAPIVersion()
	kind := resource.GetKind()

	if kind != "HAProxyTemplateConfig" {
		return nil, nil, fmt.Errorf("expected HAProxyTemplateConfig, got %s", kind)
	}

	if apiVersion != "haproxy-template-ic.gitlab.io/v1alpha1" {
		return nil, nil, fmt.Errorf("expected apiVersion haproxy-template-ic.gitlab.io/v1alpha1, got %s", apiVersion)
	}

	// Convert unstructured to typed CRD
	crd := &v1alpha1.HAProxyTemplateConfig{}
	if err := runtime.DefaultUnstructuredConverter.FromUnstructured(resource.Object, crd); err != nil {
		return nil, nil, fmt.Errorf("failed to convert unstructured to HAProxyTemplateConfig: %w", err)
	}

	// Convert CRD Spec to config.Config
	cfg, err := ConvertSpec(&crd.Spec)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to convert CRD spec to config: %w", err)
	}

	return cfg, crd, nil
}
