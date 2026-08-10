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

package migratecheck

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"gitlab.com/haproxy-haptic/haptic/pkg/apis/haproxytemplate/v1alpha1"
)

// coverage builds a single-source coverage fixture: source "acme", detected
// by class "acme" or the "acme.io/" annotation prefix, with one annotation
// per status. No source or annotation name here appears anywhere in Go
// production code — the point of the whole design.
func coverage() []v1alpha1.MigrationCoverageSource {
	return []v1alpha1.MigrationCoverageSource{
		{
			Source: "acme",
			Detect: v1alpha1.MigrationDetect{
				IngressClasses:     []string{"acme"},
				AnnotationPrefixes: []string{"acme.io/"},
			},
			Annotations: map[string]v1alpha1.AnnotationCoverage{
				"acme.io/ssl-redirect": {Status: "supported", Note: "same", Doc: "d#s"},
				"acme.io/rate-limit":   {Status: "different", Note: "differs", Doc: "d#r"},
				"acme.io/canary":       {Status: "dropped", Note: "ignored"},
				"acme.io/rewrite":      {Status: "fails", Note: "rejected"},
			},
		},
	}
}

func TestClassify_GroupsBySourceAndClassifiesStatuses(t *testing.T) {
	ingresses := []Ingress{
		{
			Namespace: "store", Name: "shop", Class: "acme",
			Annotations: map[string]string{
				"acme.io/ssl-redirect": "true",
				"acme.io/canary":       "true",
			},
		},
	}

	report := Classify(coverage(), ingresses)

	require.Len(t, report.Sources, 1)
	src := report.Sources[0]
	assert.Equal(t, "acme", src.Source)
	require.Len(t, src.Ingresses, 1)

	ing := src.Ingresses[0]
	require.Len(t, ing.Findings, 2)
	// Findings are sorted by annotation key: canary before ssl-redirect.
	assert.Equal(t, "acme.io/canary", ing.Findings[0].Annotation)
	assert.Equal(t, StatusDropped, ing.Findings[0].Status)
	assert.Equal(t, "acme.io/ssl-redirect", ing.Findings[1].Annotation)
	assert.Equal(t, StatusSupported, ing.Findings[1].Status)

	assert.Equal(t, 2, report.CheckedAnnotations)
	assert.Equal(t, 1, report.Counts[StatusSupported])
	assert.Equal(t, 1, report.Counts[StatusDropped])
}

func TestClassify_UnknownPrefixAnnotationIsHonestlyUnknown(t *testing.T) {
	ingresses := []Ingress{
		{
			Namespace: "store", Name: "x", Class: "acme",
			Annotations: map[string]string{
				// Matches the acme.io/ prefix but isn't in the coverage map.
				"acme.io/some-new-knob": "1",
			},
		},
	}

	report := Classify(coverage(), ingresses)

	require.Len(t, report.Sources, 1)
	require.Len(t, report.Sources[0].Ingresses, 1)
	findings := report.Sources[0].Ingresses[0].Findings
	require.Len(t, findings, 1)
	assert.Equal(t, StatusUnknown, findings[0].Status)
	assert.NotEmpty(t, findings[0].Note, "unknown findings still carry a plain-language note")
	assert.Equal(t, 1, report.Counts[StatusUnknown])
}

func TestClassify_DetectsByAnnotationPrefixWithoutClass(t *testing.T) {
	// No matching class, but an acme.io/ annotation attributes it.
	ingresses := []Ingress{
		{
			Namespace: "n", Name: "a", Class: "something-else",
			Annotations: map[string]string{"acme.io/rate-limit": "5"},
		},
	}

	report := Classify(coverage(), ingresses)

	require.Len(t, report.Sources, 1)
	assert.Empty(t, report.Unattributed)
	assert.Equal(t, StatusDifferent, report.Sources[0].Ingresses[0].Findings[0].Status)
}

func TestClassify_UnattributedIngressIsReportedButNotClassified(t *testing.T) {
	ingresses := []Ingress{
		{Namespace: "n", Name: "plain", Class: "nginx", Annotations: map[string]string{"foo": "bar"}},
	}

	report := Classify(coverage(), ingresses)

	assert.Empty(t, report.Sources)
	require.Len(t, report.Unattributed, 1)
	assert.Equal(t, "plain", report.Unattributed[0].Name)
	assert.Equal(t, 0, report.CheckedAnnotations)
}

func TestClassify_OneIngressAttributedToMultipleSources(t *testing.T) {
	cov := []v1alpha1.MigrationCoverageSource{
		{
			Source: "acme",
			Detect: v1alpha1.MigrationDetect{AnnotationPrefixes: []string{"acme.io/"}},
			Annotations: map[string]v1alpha1.AnnotationCoverage{
				"acme.io/a": {Status: "supported"},
			},
		},
		{
			Source: "beta",
			Detect: v1alpha1.MigrationDetect{AnnotationPrefixes: []string{"beta.io/"}},
			Annotations: map[string]v1alpha1.AnnotationCoverage{
				"beta.io/b": {Status: "dropped"},
			},
		},
	}
	ingresses := []Ingress{
		{
			Namespace: "n", Name: "both",
			Annotations: map[string]string{"acme.io/a": "1", "beta.io/b": "2"},
		},
	}

	report := Classify(cov, ingresses)

	require.Len(t, report.Sources, 2)
	// Each source classifies only its own prefix.
	assert.Len(t, report.Sources[0].Ingresses[0].Findings, 1)
	assert.Equal(t, "acme.io/a", report.Sources[0].Ingresses[0].Findings[0].Annotation)
	assert.Len(t, report.Sources[1].Ingresses[0].Findings, 1)
	assert.Equal(t, "beta.io/b", report.Sources[1].Ingresses[0].Findings[0].Annotation)
	assert.Empty(t, report.Unattributed)
}

func TestClassify_RenderFailureCounted(t *testing.T) {
	ingresses := []Ingress{
		{
			Namespace: "store", Name: "bad", Class: "acme",
			Annotations: map[string]string{"acme.io/rewrite": "/"},
			RenderError: "template rejected: acme.io/rewrite",
		},
	}

	report := Classify(coverage(), ingresses)

	assert.Equal(t, 1, report.RenderFailures)
	require.Len(t, report.Sources, 1)
	assert.Equal(t, "template rejected: acme.io/rewrite", report.Sources[0].Ingresses[0].RenderError)
}

func TestClassify_DeterministicIngressOrder(t *testing.T) {
	ingresses := []Ingress{
		{Namespace: "b", Name: "z", Class: "acme", Annotations: map[string]string{"acme.io/canary": "1"}},
		{Namespace: "a", Name: "y", Class: "acme", Annotations: map[string]string{"acme.io/canary": "1"}},
		{Namespace: "a", Name: "x", Class: "acme", Annotations: map[string]string{"acme.io/canary": "1"}},
	}

	report := Classify(coverage(), ingresses)

	require.Len(t, report.Sources, 1)
	got := report.Sources[0].Ingresses
	require.Len(t, got, 3)
	assert.Equal(t, []string{"a/x", "a/y", "b/z"}, []string{
		got[0].Namespace + "/" + got[0].Name,
		got[1].Namespace + "/" + got[1].Name,
		got[2].Namespace + "/" + got[2].Name,
	})
}
