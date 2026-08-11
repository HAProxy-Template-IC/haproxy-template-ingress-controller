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

package migratecheck

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"strings"

	"sigs.k8s.io/yaml"
)

type CoverageSource struct {
	Source      string                        `json:"source"`
	Detect      Detect                        `json:"detect,omitempty"`
	Annotations map[string]AnnotationCoverage `json:"annotations,omitempty"`
}

type Detect struct {
	IngressClasses     []string `json:"ingressClasses,omitempty"`
	AnnotationPrefixes []string `json:"annotationPrefixes,omitempty"`
}

type AnnotationCoverage struct {
	Status Status `json:"status"`
	Note   string `json:"note,omitempty"`
	Doc    string `json:"doc,omitempty"`
}

func ParseCoverage(data []byte) ([]CoverageSource, error) {
	if strings.TrimSpace(string(data)) == "" {
		return nil, nil
	}

	var coverage []CoverageSource
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&coverage); err != nil {
		return nil, fmt.Errorf("parsing migration coverage: %w", err)
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		return nil, fmt.Errorf("parsing migration coverage: trailing JSON data")
	}
	return validateCoverage(coverage)
}

func ParseLegacyConfigCoverage(data []byte) ([]CoverageSource, bool) {
	type spec struct {
		MigrationCoverage json.RawMessage `json:"migrationCoverage"`
	}
	var document struct {
		Spec              spec            `json:"spec"`
		MigrationCoverage json.RawMessage `json:"migrationCoverage"`
		Items             []struct {
			Spec spec `json:"spec"`
		} `json:"items"`
	}
	if err := yaml.Unmarshal(data, &document); err != nil {
		return nil, false
	}

	coverage := document.MigrationCoverage
	if len(document.Spec.MigrationCoverage) > 0 {
		coverage = document.Spec.MigrationCoverage
	}
	if len(document.Items) > 0 && len(document.Items[0].Spec.MigrationCoverage) > 0 {
		coverage = document.Items[0].Spec.MigrationCoverage
	}
	if len(coverage) == 0 {
		return nil, false
	}

	var decoded []CoverageSource
	if err := json.Unmarshal(coverage, &decoded); err != nil {
		return nil, true
	}
	return usableLegacyCoverage(decoded), true
}

func usableLegacyCoverage(coverage []CoverageSource) []CoverageSource {
	out := make([]CoverageSource, 0, len(coverage))
	seen := make(map[string]struct{}, len(coverage))
	for _, source := range coverage {
		source.Source = strings.TrimSpace(source.Source)
		source.Detect.IngressClasses = nonemptyStrings(source.Detect.IngressClasses)
		source.Detect.AnnotationPrefixes = nonemptyStrings(source.Detect.AnnotationPrefixes)
		if source.Source == "" ||
			(len(source.Detect.IngressClasses) == 0 && len(source.Detect.AnnotationPrefixes) == 0) {
			continue
		}
		if _, exists := seen[source.Source]; exists {
			continue
		}
		seen[source.Source] = struct{}{}
		source.Annotations = usableLegacyAnnotations(source.Annotations)
		out = append(out, source)
	}
	return out
}

func usableLegacyAnnotations(annotations map[string]AnnotationCoverage) map[string]AnnotationCoverage {
	out := make(map[string]AnnotationCoverage, len(annotations))
	for annotation, entry := range annotations {
		if strings.TrimSpace(annotation) != "" && entry.Status.valid() {
			out[annotation] = entry
		}
	}
	return out
}

func nonemptyStrings(values []string) []string {
	out := make([]string, 0, len(values))
	for _, value := range values {
		if value = strings.TrimSpace(value); value != "" {
			out = append(out, value)
		}
	}
	return out
}

func validateCoverage(coverage []CoverageSource) ([]CoverageSource, error) {
	seen := make(map[string]struct{}, len(coverage))
	for i := range coverage {
		source := &coverage[i]
		if err := validateCoverageSource(source, i); err != nil {
			return nil, err
		}
		if _, exists := seen[source.Source]; exists {
			return nil, fmt.Errorf("migration coverage source %q is duplicated", source.Source)
		}
		seen[source.Source] = struct{}{}
	}
	return coverage, nil
}

func validateCoverageSource(source *CoverageSource, index int) error {
	if strings.TrimSpace(source.Source) == "" {
		return fmt.Errorf("migration coverage source %d has no name", index)
	}
	if len(source.Detect.IngressClasses) == 0 || len(source.Detect.AnnotationPrefixes) == 0 {
		return fmt.Errorf("migration coverage source %q has incomplete detection rules", source.Source)
	}
	if len(nonemptyStrings(source.Detect.IngressClasses)) != len(source.Detect.IngressClasses) ||
		len(nonemptyStrings(source.Detect.AnnotationPrefixes)) != len(source.Detect.AnnotationPrefixes) {
		return fmt.Errorf("migration coverage source %q has an empty detection rule", source.Source)
	}
	if len(source.Annotations) == 0 {
		return fmt.Errorf("migration coverage source %q has no annotations", source.Source)
	}
	for annotation, entry := range source.Annotations {
		if err := validateAnnotation(source.Source, annotation, entry); err != nil {
			return err
		}
	}
	return nil
}

func validateAnnotation(source, annotation string, entry AnnotationCoverage) error {
	if strings.TrimSpace(annotation) == "" {
		return fmt.Errorf("migration coverage source %q has an empty annotation key", source)
	}
	if !entry.Status.valid() {
		return fmt.Errorf("migration coverage source %q annotation %q has invalid status %q", source, annotation, entry.Status)
	}
	if strings.TrimSpace(entry.Note) == "" {
		return fmt.Errorf("migration coverage source %q annotation %q has no note", source, annotation)
	}
	return nil
}

func (s Status) valid() bool {
	switch s {
	case StatusSupported, StatusDifferent, StatusDropped, StatusFails:
		return true
	default:
		return false
	}
}
