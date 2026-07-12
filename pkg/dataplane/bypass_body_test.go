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

package dataplane

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"gitlab.com/haproxy-haptic/haptic/pkg/dataplane/parser"
)

const bypassBodyBaseline = `global

defaults
  mode http
  timeout connect 5s
  timeout client 30s
  timeout server 30s

backend api
  default-server check
  server SRV_1 10.0.0.1:8080 enabled
  server SRV_2 10.0.0.9:8080 enabled

backend web
  default-server check
  server SRV_1 10.1.0.1:8080 enabled
`

// computeBypassUpdates parses baseline and desired and returns the render diff
// exactly as the deployer computes it (ComputeRuntimeServerUpdates).
func computeBypassUpdates(t *testing.T, baseline, desired string) *RuntimeServerUpdates {
	t.Helper()
	p, err := parser.New()
	require.NoError(t, err)
	prev, err := p.ParseFromString(baseline)
	require.NoError(t, err)
	cur, err := p.ParseFromString(desired)
	require.NoError(t, err)
	updates, err := ComputeRuntimeServerUpdates(prev, cur)
	require.NoError(t, err)
	return updates
}

// TestBuildRuntimeBypassBody pins the issue #84 bypass-body invariant: the
// body a runtime-bypass push carries is the last-ACTIVATED baseline patched
// with ONLY the runtime-eligible server lines of the pending render — never
// the pending render itself. Structural content of the pending render (a new
// backend, a new frontend) must NOT appear; runtime-updated server lines must
// carry the pending render's values.
func TestBuildRuntimeBypassBody(t *testing.T) {
	tests := []struct {
		name        string
		desired     string
		wantContain []string
		wantAbsent  []string
		// wantBaselineVerbatim asserts the body IS the baseline, unmodified.
		wantBaselineVerbatim bool
	}{
		{
			name: "address change is patched onto the baseline",
			desired: strings.Replace(bypassBodyBaseline,
				"server SRV_1 10.0.0.1:8080 enabled",
				"server SRV_1 10.0.0.2:8080 enabled", 1),
			wantContain: []string{"server SRV_1 10.0.0.2:8080 enabled"},
			wantAbsent:  []string{"10.0.0.1:8080"},
		},
		{
			name: "pending render's NEW backend must NOT appear in the bypass body",
			desired: strings.Replace(bypassBodyBaseline,
				"server SRV_1 10.0.0.1:8080 enabled",
				"server SRV_1 10.0.0.2:8080 enabled", 1) +
				"\nbackend api2\n  default-server check\n  server SRV_1 10.9.9.9:8080 enabled\n",
			wantContain: []string{"server SRV_1 10.0.0.2:8080 enabled"},
			wantAbsent:  []string{"api2", "10.9.9.9"},
		},
		{
			name: "same-named server in an untouched backend keeps its baseline line",
			desired: strings.Replace(bypassBodyBaseline,
				"server SRV_1 10.0.0.1:8080 enabled",
				"server SRV_1 10.0.0.2:8080 enabled", 1),
			// backend web's SRV_1 shares the slot name but did not change —
			// its line must stay at the baseline address.
			wantContain: []string{"server SRV_1 10.1.0.1:8080 enabled"},
		},
		{
			name: "admin-state flip is patched",
			desired: strings.Replace(bypassBodyBaseline,
				"server SRV_2 10.0.0.9:8080 enabled",
				"server SRV_2 10.0.0.9:8080 disabled", 1),
			wantContain: []string{"server SRV_2 10.0.0.9:8080 disabled"},
			wantAbsent:  []string{"server SRV_2 10.0.0.9:8080 enabled"},
		},
		{
			name:                 "identical render leaves the baseline verbatim",
			desired:              bypassBodyBaseline,
			wantBaselineVerbatim: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			updates := computeBypassUpdates(t, bypassBodyBaseline, tt.desired)

			body := updates.BuildRuntimeBypassBody(bypassBodyBaseline, tt.desired)

			if tt.wantBaselineVerbatim {
				assert.Equal(t, bypassBodyBaseline, body)
				return
			}
			for _, want := range tt.wantContain {
				assert.Contains(t, body, want)
			}
			for _, absent := range tt.wantAbsent {
				assert.NotContains(t, body, absent)
			}
			// The patch never changes the baseline's structure: same number
			// of lines, and every non-server line is byte-identical.
			baseLines := strings.Split(bypassBodyBaseline, "\n")
			bodyLines := strings.Split(body, "\n")
			require.Equal(t, len(baseLines), len(bodyLines), "the patch must be line-for-line")
			for i := range baseLines {
				if !strings.HasPrefix(strings.TrimSpace(baseLines[i]), "server ") {
					assert.Equal(t, baseLines[i], bodyLines[i], "non-server line %d must stay untouched", i)
				}
			}
		})
	}
}

// TestBuildRuntimeBypassBody_NilAndEmpty guards the degenerate inputs: a nil
// receiver, an empty diff, and a diff whose targets don't appear in the
// baseline all return the baseline unchanged (the safe direction — disk stays
// at activated content).
func TestBuildRuntimeBypassBody_NilAndEmpty(t *testing.T) {
	var nilUpdates *RuntimeServerUpdates
	assert.Equal(t, bypassBodyBaseline, nilUpdates.BuildRuntimeBypassBody(bypassBodyBaseline, "whatever"))

	empty := computeBypassUpdates(t, bypassBodyBaseline, bypassBodyBaseline)
	assert.Equal(t, bypassBodyBaseline, empty.BuildRuntimeBypassBody(bypassBodyBaseline, bypassBodyBaseline))

	// Targets that exist in the diff but not in this (different) baseline
	// text: nothing is replaced.
	desired := strings.Replace(bypassBodyBaseline,
		"server SRV_1 10.0.0.1:8080 enabled",
		"server SRV_1 10.0.0.2:8080 enabled", 1)
	updates := computeBypassUpdates(t, bypassBodyBaseline, desired)
	other := "global\n\nbackend other\n  server X 1.2.3.4:80 enabled\n"
	assert.Equal(t, other, updates.BuildRuntimeBypassBody(other, desired))
}
