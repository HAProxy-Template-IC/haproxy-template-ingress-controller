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

package v1alpha1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// +genclient
// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object
// +kubebuilder:object:root=true
// +kubebuilder:resource:shortName=htpltests,scope=Namespaced
// +kubebuilder:printcolumn:name="Tests",type=integer,JSONPath=`.status.testCount`
// +kubebuilder:printcolumn:name="Age",type=date,JSONPath=`.metadata.creationTimestamp`

// HAProxyValidationTests carries validation tests for an HAProxyTemplateConfig
// that selects it.
//
// Tests live in their own object because they dominate the configuration's size
// — measured at 36% of the merged spec, and the difference between a profile
// that fits etcd's per-object limit and one that does not — while being needed
// only when the configuration is validated, never when it is rendered.
//
// This is not a chart-private mechanism. An operator who disables every bundled
// template library and writes their own configuration authors these objects the
// same way the chart does, and may equally keep tests inline on the config's
// `spec.validationTests`. Both sources are unioned; neither is privileged.
type HAProxyValidationTests struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   HAProxyValidationTestsSpec   `json:"spec,omitempty"`
	Status HAProxyValidationTestsStatus `json:"status,omitempty"`
}

// HAProxyValidationTestsSpec defines a set of validation tests.
type HAProxyValidationTestsSpec struct {
	// ValidationTests maps test name to its definition, with exactly the shape
	// and semantics of HAProxyTemplateConfigSpec.ValidationTests — the same
	// suite runner executes both, so a test can be moved between the two
	// without edits.
	//
	// A name may appear in only one source. Two sources defining the same test
	// is an error rather than a silent last-writer-wins, because the losing
	// definition would be an assertion its author believes is running.
	//
	// The reserved `_global` entry is the exception: several sources
	// legitimately contribute part of it, and its fixtures are unioned.
	// +optional
	ValidationTests map[string]ValidationTest `json:"validationTests,omitempty"`
}

// HAProxyValidationTestsStatus reports what the controller made of this object.
type HAProxyValidationTestsStatus struct {
	// TestCount is the number of tests this object contributed to the suite the
	// controller last ran. It is not len(spec.validationTests): a test whose
	// name collides with another source contributes nothing, and this is how an
	// operator sees that without reading logs.
	// +optional
	TestCount int `json:"testCount,omitempty"`

	// ObservedGeneration is the spec generation this status describes.
	// +optional
	ObservedGeneration int64 `json:"observedGeneration,omitempty"`

	// Conditions follows the standard Kubernetes condition contract. `Accepted`
	// is False when this object was discovered but could not be used — a name
	// collision, or a config that selects it refusing to load.
	// +optional
	// +listType=map
	// +listMapKey=type
	Conditions []metav1.Condition `json:"conditions,omitempty"`
}

// +kubebuilder:object:root=true

// HAProxyValidationTestsList is a list of HAProxyValidationTests.
type HAProxyValidationTestsList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`

	Items []HAProxyValidationTests `json:"items"`
}
