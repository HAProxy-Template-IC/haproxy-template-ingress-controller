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

// Package v1alpha1 contains API Schema definitions for the haproxytemplate v1alpha1 API group.
//
// # Overview
//
// This package defines the HAProxyTemplateConfig custom resource, which provides
// configuration for the HAProxy Template Ingress Controller. It replaces the previous
// ConfigMap-based configuration approach with a Kubernetes-native CRD.
//
// # Key Features
//
//   - Type-safe configuration with OpenAPI validation
//   - Embedded validation tests for templates
//   - Support for admission webhook validation
//   - Credential reference management via Secrets
//
// # API Group
//
// Group: haproxy-haptic.org
// Version: v1alpha1
//
// # Resources
//
// HAProxyTemplateConfig: Controller configuration with embedded validation tests
//
// # Examples
//
// See docs/development/crd-validation-design.md for comprehensive examples
// and usage patterns.
//
// +k8s:deepcopy-gen=package
// +groupName=haproxy-haptic.org
package v1alpha1
