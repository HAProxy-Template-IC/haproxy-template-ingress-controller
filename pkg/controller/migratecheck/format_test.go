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
	"encoding/json"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func sampleReport() *Report {
	return Classify(coverage(), []Ingress{
		{
			Namespace: "store", Name: "shop", Class: "acme",
			Annotations: map[string]string{
				"acme.io/ssl-redirect":  "true",
				"acme.io/canary":        "true",
				"acme.io/some-new-knob": "1",
			},
		},
		{
			Namespace: "store", Name: "bad", Class: "acme",
			Annotations: map[string]string{"acme.io/rewrite": "/"},
			RenderError: "template rejected: acme.io/rewrite",
		},
		{Namespace: "default", Name: "plain", Class: "nginx"},
	})
}

func TestFormatText_VerdictLineFirstAndBlockerContent(t *testing.T) {
	out, err := Format(sampleReport(), FormatText)
	require.NoError(t, err)

	lines := strings.SplitN(out, "\n", 3)
	require.GreaterOrEqual(t, len(lines), 2)
	// First line is the one-glance summary, second the verdict.
	assert.True(t, strings.HasPrefix(lines[0], "migrate-check:"), "summary line first: %q", lines[0])
	assert.Contains(t, lines[1], "Verdict:")
	assert.Contains(t, lines[1], "BLOCKERS FOUND")

	// A blocker report names the failing render and the source grouping.
	assert.Contains(t, out, "Source: acme")
	assert.Contains(t, out, "RENDER FAILED")
	assert.Contains(t, out, "acme.io/some-new-knob")
	assert.Contains(t, out, "[unknown]")
	// The unattributed Ingress is summarized, not classified.
	assert.Contains(t, out, "no annotations from a known source controller")
}

func TestFormatJSON_RoundTrips(t *testing.T) {
	out, err := Format(sampleReport(), FormatJSON)
	require.NoError(t, err)

	var decoded Report
	require.NoError(t, json.Unmarshal([]byte(out), &decoded))
	assert.Equal(t, 3, decoded.TotalIngresses)
	assert.Equal(t, 1, decoded.RenderFailures)
	require.Len(t, decoded.Sources, 1)
	assert.Equal(t, "acme", decoded.Sources[0].Source)
}

func TestFormatMarkdown_HasTableAndVerdict(t *testing.T) {
	out, err := Format(sampleReport(), FormatMarkdown)
	require.NoError(t, err)

	assert.Contains(t, out, "# HAPTIC migration check")
	assert.Contains(t, out, "**Verdict:")
	assert.Contains(t, out, "| Ingress | Annotation | Status | Note |")
	assert.Contains(t, out, "`acme.io/canary`")
	// Doc links render as Markdown links.
	assert.Contains(t, out, "([docs](d#s))")
}

func TestFormat_UnknownFormatErrors(t *testing.T) {
	_, err := Format(sampleReport(), "xml")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "unknown output format")
}

func TestFormatText_CleanReportReadsReady(t *testing.T) {
	report := Classify(coverage(), []Ingress{
		{
			Namespace: "n", Name: "ok", Class: "acme",
			Annotations: map[string]string{"acme.io/ssl-redirect": "true"},
		},
	})
	require.Equal(t, ExitClean, report.ExitCode())

	out, err := Format(report, FormatText)
	require.NoError(t, err)
	assert.Contains(t, out, "READY")
	assert.NotContains(t, out, "RENDER FAILED")
}

func TestAggregateRenderErrorIsBlocker(t *testing.T) {
	r := &Report{
		TotalIngresses: 2,
		Counts:         map[Status]int{StatusSupported: 2},
	}
	// Individually clean → would be ExitClean/READY.
	assert.Equal(t, ExitClean, r.ExitCode())

	// A cross-Ingress conflict flips it to a blocker with honest wording.
	r.AggregateRenderError = "backend echo already defined"
	assert.Equal(t, ExitBlockers, r.ExitCode())
	txt := formatText(r)
	assert.Contains(t, txt, "conflict when combined")
	assert.Contains(t, txt, "Cross-Ingress conflict")
	assert.Contains(t, txt, "backend echo already defined")
}
