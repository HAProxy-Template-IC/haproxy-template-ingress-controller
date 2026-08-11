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

// Package migratecheck classifies Ingress annotations against the
// migration-coverage data the template libraries declare for the browser
// playground's migration report.
//
// The package is a pure component: it never touches Kubernetes, Helm, or
// the template engine. Callers hand it the coverage data, the audited
// Ingresses (pre-extracted fields plus the per-Ingress real-render verdict),
// and get back a Report with per-source, per-Ingress findings.
//
// Source-controller detection remains data-driven from the coverage entries;
// the package is private to the Ingress migration tool that consumes it.
package migratecheck

import (
	"sort"
	"strings"
)

// Status classifies one annotation's migration coverage. The first four
// values mirror the library declaration contract; StatusUnknown is
// synthesized for annotation keys that match a source's declared prefixes
// but are absent from its coverage map.
type Status string

// Coverage statuses, ordered from best to worst.
const (
	StatusSupported Status = "supported"
	StatusDifferent Status = "different"
	StatusDropped   Status = "dropped"
	StatusFails     Status = "fails"
	StatusUnknown   Status = "unknown"
)

// Ingress is one audited Ingress resource, reduced to the fields the
// classification needs.
type Ingress struct {
	// Namespace and Name identify the resource.
	Namespace string `json:"namespace"`
	Name      string `json:"name"`

	// Class is the effective ingress class: spec.ingressClassName when
	// set, otherwise the legacy kubernetes.io/ingress.class annotation.
	Class string `json:"class,omitempty"`

	// Annotations is metadata.annotations.
	Annotations map[string]string `json:"-"`

	// RenderError is the (simplified) error from rendering this Ingress
	// through the real template pipeline; empty when the render passed
	// or was not performed.
	RenderError string `json:"renderError,omitempty"`
}

// Finding is one classified annotation on one Ingress.
type Finding struct {
	// Annotation is the full annotation key.
	Annotation string `json:"annotation"`
	// Value is the annotation's value on this Ingress.
	Value string `json:"value"`
	// Status is the coverage classification.
	Status Status `json:"status"`
	// Note is the plain-language explanation from the coverage data.
	Note string `json:"note,omitempty"`
	// Doc is the documentation link/anchor from the coverage data.
	Doc string `json:"doc,omitempty"`
}

// IngressReport carries the findings for one Ingress under one source.
type IngressReport struct {
	Namespace string `json:"namespace"`
	Name      string `json:"name"`
	Class     string `json:"class,omitempty"`
	// Findings lists the classified annotations, sorted by key. Fully
	// supported annotations are included (status "supported") so the
	// JSON output is complete; the text renderer summarizes them.
	Findings []Finding `json:"findings,omitempty"`
	// RenderError is the real-render verdict for this Ingress; empty
	// when the render passed.
	RenderError string `json:"renderError,omitempty"`
}

// SourceReport groups the audited Ingresses attributed to one source
// controller.
type SourceReport struct {
	// Source is the coverage entry's source name.
	Source string `json:"source"`
	// Ingresses lists the attributed Ingresses, sorted by namespace/name.
	Ingresses []IngressReport `json:"ingresses"`
	// Counts tallies findings by status across all Ingresses of this
	// source.
	Counts map[Status]int `json:"counts"`
}

// Report is the complete migrate-check result.
type Report struct {
	// Sources lists per-source findings in coverage declaration order.
	// Only sources with at least one attributed Ingress appear.
	Sources []SourceReport `json:"sources"`

	// Unattributed lists Ingresses that no coverage entry's detect rules
	// matched. They carry no findings, but their render verdict still
	// counts: a render failure is a blocker wherever it happens.
	Unattributed []IngressReport `json:"unattributed,omitempty"`

	// TotalIngresses is the number of audited Ingresses.
	TotalIngresses int `json:"totalIngresses"`

	// CheckedAnnotations is the number of annotation keys classified
	// across all sources.
	CheckedAnnotations int `json:"checkedAnnotations"`

	// Counts tallies findings by status across all sources.
	Counts map[Status]int `json:"counts"`

	// RenderFailures is the number of Ingresses whose real render failed.
	RenderFailures int `json:"renderFailures"`

	// AggregateRenderError is the (simplified) error from rendering ALL
	// audited Ingresses together, empty when the combined render succeeded.
	// Per-Ingress renders are isolated so a failure attributes to one
	// Ingress; this aggregate pass additionally catches conflicts that only
	// arise from the combination (duplicate backend/frontend names,
	// colliding hosts or paths, cross-resource map-key collisions) — which
	// isolated renders cannot see. A non-empty value is a blocker.
	AggregateRenderError string `json:"aggregateRenderError,omitempty"`
}

// Classify groups the audited Ingresses by source controller (via each
// coverage entry's detect rules) and classifies every annotation whose key
// matches one of the source's declared prefixes against the source's
// coverage map. Keys matching a prefix but absent from the coverage map
// are reported honestly as StatusUnknown.
//
// An Ingress is attributed to a source when its class matches one of the
// source's detect.ingressClasses OR any of its annotation keys carries one
// of the source's detect.annotationPrefixes. One Ingress can be attributed
// to several sources (each classifies only its own prefixes).
func Classify(coverage []CoverageSource, ingresses []Ingress) *Report {
	report := &Report{
		Sources: []SourceReport{},
		Counts:  map[Status]int{},
	}

	sorted := make([]Ingress, len(ingresses))
	copy(sorted, ingresses)
	sort.Slice(sorted, func(i, j int) bool {
		if sorted[i].Namespace != sorted[j].Namespace {
			return sorted[i].Namespace < sorted[j].Namespace
		}
		return sorted[i].Name < sorted[j].Name
	})

	report.TotalIngresses = len(sorted)
	attributed := make([]bool, len(sorted))

	for ci := range coverage {
		src := &coverage[ci]
		srcReport := SourceReport{
			Source: src.Source,
			Counts: map[Status]int{},
		}
		for ii := range sorted {
			ing := &sorted[ii]
			if !matchesSource(src, ing) {
				continue
			}
			attributed[ii] = true
			ir := classifyIngress(src, ing)
			for _, f := range ir.Findings {
				srcReport.Counts[f.Status]++
				report.Counts[f.Status]++
				report.CheckedAnnotations++
			}
			srcReport.Ingresses = append(srcReport.Ingresses, ir)
		}
		if len(srcReport.Ingresses) > 0 {
			report.Sources = append(report.Sources, srcReport)
		}
	}

	for ii := range sorted {
		ing := &sorted[ii]
		if ing.RenderError != "" {
			report.RenderFailures++
		}
		if !attributed[ii] {
			report.Unattributed = append(report.Unattributed, IngressReport{
				Namespace:   ing.Namespace,
				Name:        ing.Name,
				Class:       ing.Class,
				RenderError: ing.RenderError,
			})
		}
	}

	return report
}

// matchesSource reports whether the detect rules of a coverage entry
// attribute the Ingress to that source.
func matchesSource(src *CoverageSource, ing *Ingress) bool {
	for _, class := range src.Detect.IngressClasses {
		if ing.Class == class {
			return true
		}
	}
	for key := range ing.Annotations {
		if matchesPrefix(src.Detect.AnnotationPrefixes, key) {
			return true
		}
	}
	return false
}

// matchesPrefix reports whether the annotation key carries any of the
// declared prefixes.
func matchesPrefix(prefixes []string, key string) bool {
	for _, prefix := range prefixes {
		if strings.HasPrefix(key, prefix) {
			return true
		}
	}
	return false
}

// classifyIngress builds the per-Ingress report for one source: every
// annotation key matching the source's prefixes becomes a Finding, sorted
// by key for deterministic output.
func classifyIngress(src *CoverageSource, ing *Ingress) IngressReport {
	ir := IngressReport{
		Namespace:   ing.Namespace,
		Name:        ing.Name,
		Class:       ing.Class,
		RenderError: ing.RenderError,
	}

	keys := make([]string, 0, len(ing.Annotations))
	for key := range ing.Annotations {
		if matchesPrefix(src.Detect.AnnotationPrefixes, key) {
			keys = append(keys, key)
		}
	}
	sort.Strings(keys)

	for _, key := range keys {
		finding := Finding{
			Annotation: key,
			Value:      ing.Annotations[key],
			Status:     StatusUnknown,
			Note:       "This annotation is not in HAPTIC's coverage data for this source; verify its behaviour manually after migrating.",
		}
		if ann, ok := src.Annotations[key]; ok {
			finding.Status = ann.Status
			finding.Note = ann.Note
			finding.Doc = ann.Doc
		}
		ir.Findings = append(ir.Findings, finding)
	}

	return ir
}
