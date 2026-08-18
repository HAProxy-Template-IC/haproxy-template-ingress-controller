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

package deployplan_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"gitlab.com/haproxy-haptic/haptic/pkg/dataplane/agent/api"
	"gitlab.com/haproxy-haptic/haptic/pkg/dataplane/deployplan"
	"gitlab.com/haproxy-haptic/haptic/pkg/dataplane/renderplan"
)

// TestDiffGeneralFiles covers rule 7: a general file is written, and only its
// own reload-on-change flag makes it a reload.
func TestDiffGeneralFiles(t *testing.T) {
	tests := []struct {
		name           string
		reloadOnChange bool
		verdict        deployplan.Verdict
	}{
		{name: "written without a reload", verdict: deployplan.VerdictFileOnly},
		{name: "declared reload-on-change", reloadOnChange: true, verdict: deployplan.VerdictReload},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			file := renderplan.File{
				Path: "general/errors/503.http", Kind: renderplan.FileKindGeneral, ReloadOnChange: tt.reloadOnChange,
			}
			prev := basePlan(withFile(withDigest(file, "before")))
			next := basePlan(withFile(withDigest(file, "after")))

			got := deployplan.Diff(next, on34(prev))

			assert.Equal(t, tt.verdict, got.Verdict)
			assert.Empty(t, got.Ops)
		})
	}
}

// TestDiffRemovedFiles pins the other half of rule 7: the agent's ownership set
// makes absence a delete, so a dropped file is judged like a changed one.
func TestDiffRemovedFiles(t *testing.T) {
	tests := []struct {
		name           string
		reloadOnChange bool
		verdict        deployplan.Verdict
		reason         string
	}{
		{
			name:    "removed without a reload",
			verdict: deployplan.VerdictFileOnly,
			reason:  "was removed, which no runtime op undoes",
		},
		{
			name:           "removed while declared reload-on-change",
			reloadOnChange: true,
			verdict:        deployplan.VerdictReload,
			reason:         "was removed and is declared reload-on-change",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			prev := basePlan(withFile(renderplan.File{
				Path: "general/rules/extra.conf", Kind: renderplan.FileKindGeneral,
				Digest: "before", ReloadOnChange: tt.reloadOnChange,
			}))

			got := deployplan.Diff(basePlan(), on34(prev))

			assert.Equal(t, tt.verdict, got.Verdict)
			assert.Empty(t, got.Ops)
			reasonsContain(t, got.Reasons, tt.reason)
		})
	}
}

func TestFilesProjectsTheWholeSet(t *testing.T) {
	plan := basePlan(
		withFile(renderplan.File{
			Path: "general/errors/503.http", Kind: renderplan.FileKindGeneral,
			Digest: "d1", Size: 42, ReloadOnChange: true,
		}),
		withMap(renderplan.Map{Path: routeMap, Entries: []renderplan.Entry{entry("a", "1")}}),
	)

	files := deployplan.Files(plan)

	require.Len(t, files, len(plan.Files))
	assert.Equal(t, api.File{
		Path: "general/errors/503.http", Kind: renderplan.FileKindGeneral,
		Digest: "d1", Size: 42, ReloadOnChange: true,
	}, files[0])
	assert.Nil(t, deployplan.Files(nil))
}

func withDigest(f renderplan.File, digest string) renderplan.File {
	f.Digest = digest
	return f
}
