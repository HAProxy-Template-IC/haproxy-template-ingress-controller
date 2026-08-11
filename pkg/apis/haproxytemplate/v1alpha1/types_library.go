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
// +kubebuilder:subresource:status
// +kubebuilder:resource:shortName=htpllib,scope=Namespaced
// +kubebuilder:printcolumn:name="Revision",type=string,JSONPath=`.spec.revision`
// +kubebuilder:printcolumn:name="Age",type=date,JSONPath=`.metadata.creationTimestamp`

// HAProxyTemplateLibrary carries template library content that a
// HAProxyTemplateConfig pulls in through spec.libraryRefs.
//
// A library is a complete content contributor, not just a bag of snippets: it
// can define map files, general files, SSL certificates, Kubernetes resources
// and the main haproxyConfig template as well.
//
// It exists so the bulk of a configuration — templateSnippets and
// validationTests are ~94% of the rendered bytes — lives outside the object an
// operator reads and edits, and so a library too large for one object can be
// split across several without changing how it merges.
//
// It carries content only. A library cannot declare podSelector,
// watchedResources or dataplane settings, so it can never redefine the
// controller's operational identity.
type HAProxyTemplateLibrary struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   HAProxyTemplateLibrarySpec   `json:"spec,omitempty"`
	Status HAProxyTemplateLibraryStatus `json:"status,omitempty"`
}

// LibraryRef names one HAProxyTemplateLibrary object and the revision of it
// this configuration expects.
type LibraryRef struct {
	// Name of the HAProxyTemplateLibrary object, in this config's namespace.
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=253
	Name string `json:"name"`

	// Revision the referenced object must report in spec.revision.
	//
	// The controller compares the two strings and never recomputes either from
	// the content. A writer that applies both objects together stamps the same
	// value on each, so a half-applied set is visible as a mismatch — while an
	// in-place edit to a snippet's content leaves the revision alone and takes
	// effect immediately.
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=253
	Revision string `json:"revision"`
}

// HAProxyTemplateLibrarySpec is the content one snippets object contributes.
type HAProxyTemplateLibrarySpec struct {
	// Revision identifies this content to the configs that reference it. A
	// referencing spec.libraryRefs entry must carry the same string or the
	// controller holds the last-good configuration rather than rendering a
	// half-applied set.
	//
	// The writer chooses the value and the controller only ever compares it
	// against the reference — it never derives a revision from the content.
	// That is what lets `kubectl edit` change a snippet in place and take
	// effect: the content moves, the revision does not, so the reference still
	// matches. A digest of the content is the convenient source for a
	// generator, because it changes exactly when the content does.
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=253
	Revision string `json:"revision"`

	// TemplatingSettings lets a library ship defaults for the template context.
	//
	// Template configuration, not operational identity: the referencing config
	// merges last, so anything an operator sets wins over every library.
	// +optional
	TemplatingSettings TemplatingSettings `json:"templatingSettings,omitempty"`

	// TemplateSnippets maps snippet names to reusable template fragments.
	// +optional
	TemplateSnippets map[string]TemplateSnippet `json:"templateSnippets,omitempty"`

	// Maps maps map file names to their template definitions.
	// +optional
	Maps map[string]MapFile `json:"maps,omitempty"`

	// Files maps file names to their template definitions.
	// +optional
	Files map[string]GeneralFile `json:"files,omitempty"`

	// SSLCertificates maps certificate names to their template definitions.
	// +optional
	SSLCertificates map[string]SSLCertificate `json:"sslCertificates,omitempty"`

	// K8sResources maps resource template names to their template definitions.
	// +optional
	K8sResources map[string]K8sResource `json:"k8sResources,omitempty"`

	// HAProxyConfig contains the main HAProxy configuration template.
	//
	// Exactly one member of a merged set supplies it, or the referencing
	// config does.
	// +optional
	HAProxyConfig HAProxyConfig `json:"haproxyConfig,omitempty"`

	// ValidationTests contains embedded validation test definitions.
	//
	// Names must be unique across the merged set: the controller unions tests
	// per source and rejects a name two sources both define, because a silent
	// override would leave one author believing an assertion runs that does
	// not. The reserved `_global` entry accumulates instead.
	// +optional
	ValidationTests map[string]ValidationTest `json:"validationTests,omitempty"`
}

// HAProxyTemplateLibraryStatus reports what the controller observed.
type HAProxyTemplateLibraryStatus struct {
	// ObservedGeneration reflects the generation most recently observed.
	// +optional
	ObservedGeneration int64 `json:"observedGeneration,omitempty"`

	// Conditions represent the latest available observations.
	// +optional
	Conditions []metav1.Condition `json:"conditions,omitempty"`
}

// +kubebuilder:object:root=true

// HAProxyTemplateLibraryList contains a list of HAProxyTemplateLibrary.
type HAProxyTemplateLibraryList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []HAProxyTemplateLibrary `json:"items"`
}
