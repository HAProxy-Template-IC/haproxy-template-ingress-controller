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

// TestDiffSectionGuard covers rule 1: what a section may change without a
// reload, and what the record has to explain.
func TestDiffSectionGuard(t *testing.T) {
	withComment := dynBackend("be-a", srv("SRV_1", "10.0.0.1", 8080))
	withComment.CommentsDigest = renderplan.DigestString("# route default/api")

	withBody := dynBackend("be-a", srv("SRV_1", "10.0.0.1", 8080))
	withBody.BodyDigest = renderplan.DigestString("http-request set-var(txn.x) int(1)")

	unexplained := dynBackend("be-a", srv("SRV_1", "10.0.0.1", 8080))
	unexplained.TextDigest = renderplan.DigestString("text the record does not describe")

	tests := []struct {
		name    string
		next    *renderplan.Plan
		verdict deployplan.Verdict
		reason  string
	}{
		{
			name:    "comment change is written without ops",
			next:    basePlan(withBackend(withComment)),
			verdict: deployplan.VerdictFileOnly,
		},
		{
			name:    "body change reloads",
			next:    basePlan(withBackend(withBody)),
			verdict: deployplan.VerdictReload,
			reason:  "be-a: body changed",
		},
		{
			name:    "text the record does not explain reloads",
			next:    basePlan(withBackend(unexplained)),
			verdict: deployplan.VerdictReload,
			reason:  "be-a: unexplained text change",
		},
		{
			name:    "core section change reloads",
			next:    planWith(withCore("global", "global\n maxconn 100\n"), withProfile(testProfile, "body-1"), withBackend(dynBackend("be-a", srv("SRV_1", "10.0.0.1", 8080)))),
			verdict: deployplan.VerdictReload,
			reason:  "core section global changed",
		},
		{
			name:    "core section added reloads",
			next:    basePlan(withCore("frontend#1", "frontend f\n"), withBackend(dynBackend("be-a", srv("SRV_1", "10.0.0.1", 8080)))),
			verdict: deployplan.VerdictReload,
			reason:  "core section frontend#1 added",
		},
		{
			name:    "core section removed reloads",
			next:    planWith(withProfile(testProfile, "body-1"), withBackend(dynBackend("be-a", srv("SRV_1", "10.0.0.1", 8080)))),
			verdict: deployplan.VerdictReload,
			reason:  "core section global removed",
		},
		{
			name:    "profile added reloads",
			next:    basePlan(withProfile("haptic-be-2", "body-2"), withBackend(dynBackend("be-a", srv("SRV_1", "10.0.0.1", 8080)))),
			verdict: deployplan.VerdictReload,
			reason:  "profile haptic-be-2 added",
		},
		{
			name:    "profile body change reloads",
			next:    planWith(withCore("global", "global\n"), withProfile(testProfile, "body-2"), withBackend(dynBackend("be-a", srv("SRV_1", "10.0.0.1", 8080)))),
			verdict: deployplan.VerdictReload,
			reason:  "profile " + testProfile + " changed",
		},
	}

	prev := basePlan(withBackend(dynBackend("be-a", srv("SRV_1", "10.0.0.1", 8080))))
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := deployplan.Diff(tt.next, on34(prev))

			assert.Equal(t, tt.verdict, got.Verdict)
			assert.Empty(t, got.Ops)
			if tt.reason != "" {
				reasonsContain(t, got.Reasons, tt.reason)
			}
		})
	}
}

func TestDiffWithoutBaselineReloads(t *testing.T) {
	next := basePlan(withBackend(dynBackend("be-a", srv("SRV_1", "10.0.0.1", 8080))))

	got := deployplan.Diff(next, &deployplan.Baseline{Caps: deployplan.CapsFor("3.4.3", nil)})

	assert.Equal(t, deployplan.VerdictReload, got.Verdict)
	assert.Equal(t, api.ModeReload, got.Mode)
	assert.Empty(t, got.Ops)
	assert.Equal(t, []string{"no baseline"}, got.Reasons)
	assert.Len(t, got.Files, len(next.Files))
}

func TestDiffForeignSchemaVersionReloads(t *testing.T) {
	prev := basePlan()
	prev.SchemaVersion = renderplan.SchemaVersion + 1

	got := deployplan.Diff(basePlan(), on34(prev))

	assert.Equal(t, deployplan.VerdictReload, got.Verdict)
	reasonsContain(t, got.Reasons, "baseline plan schema")
}

func TestDiffBackendSectionWithoutRecordReloads(t *testing.T) {
	prev := basePlan()
	next := basePlan()
	next.Sections = append(next.Sections, renderplan.Section{
		Kind: renderplan.SectionKindBackend, Name: "ghost", TextDigest: "abc",
	})

	got := deployplan.Diff(next, on34(prev))

	assert.Equal(t, deployplan.VerdictReload, got.Verdict)
	reasonsContain(t, got.Reasons, "ghost: section without a record")
}

func TestDiffConfigChangeWithoutSectionChangeReloads(t *testing.T) {
	prev := basePlan(withBackend(dynBackend("be-a", srv("SRV_1", "10.0.0.1", 8080))))
	next := basePlan(withBackend(dynBackend("be-a", srv("SRV_1", "10.0.0.1", 8080))))
	setFileDigest(t, next, "haproxy.cfg", "0123456789abcdef")

	got := deployplan.Diff(next, on34(prev))

	assert.Equal(t, deployplan.VerdictReload, got.Verdict)
	reasonsContain(t, got.Reasons, "haproxy.cfg changed with no section explaining it")
}

// doomedProfile is the profile the garbage-collection cases remove.
const doomedProfile = "gone"

// TestDiffProfileGarbageCollection covers the one profile removal that stays
// off the reload path: nothing in the render uses it and every backend that
// did is deleted by an op in the same diff.
func TestDiffProfileGarbageCollection(t *testing.T) {
	tests := []struct {
		name    string
		prev    *renderplan.Plan
		next    *renderplan.Plan
		base    func(*renderplan.Plan) *deployplan.Baseline
		verdict deployplan.Verdict
		reason  string
	}{
		{
			name:    "last backend of the profile is deleted at runtime",
			prev:    basePlan(withProfile(doomedProfile, "body-gone"), withBackend(onDoomedProfile(dynBackend("be-a", srv("SRV_1", "10.0.0.1", 8080))))),
			next:    basePlan(),
			base:    on34,
			verdict: deployplan.VerdictRuntime,
		},
		{
			name:    "a backend still references the profile",
			prev:    basePlan(withProfile(doomedProfile, "body-gone"), withBackend(onDoomedProfile(dynBackend("be-a", srv("SRV_1", "10.0.0.1", 8080))))),
			next:    basePlan(withBackend(onDoomedProfile(dynBackend("be-a", srv("SRV_1", "10.0.0.1", 8080))))),
			base:    on34,
			verdict: deployplan.VerdictReload,
			reason:  "profile gone removed but backend be-a still uses it",
		},
		{
			name:    "the referencing backend is removed by a reload, not by ops",
			prev:    basePlan(withProfile(doomedProfile, "body-gone"), withBackend(onDoomedProfile(structuralBackend("be-a", srv("SRV_1", "10.0.0.1", 8080))))),
			next:    basePlan(),
			base:    on34,
			verdict: deployplan.VerdictReload,
			reason:  "profile gone removed but backend be-a is not deleted at runtime",
		},
		{
			name:    "the pod cannot delete backends at all",
			prev:    basePlan(withProfile(doomedProfile, "body-gone"), withBackend(onDoomedProfile(dynBackend("be-a", srv("SRV_1", "10.0.0.1", 8080))))),
			next:    basePlan(),
			base:    on33,
			verdict: deployplan.VerdictReload,
			reason:  "profile gone removed but backend be-a is not deleted at runtime",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := deployplan.Diff(tt.next, tt.base(tt.prev))

			require.Equal(t, tt.verdict, got.Verdict, got.Reasons)
			if tt.reason != "" {
				reasonsContain(t, got.Reasons, tt.reason)
			}
			if tt.verdict == deployplan.VerdictRuntime {
				assert.Contains(t, kinds(got.Ops), api.OpBackendDel)
			}
		})
	}
}

// onDoomedProfile puts a backend on the profile these cases remove.
func onDoomedProfile(be *renderplan.Backend) *renderplan.Backend {
	be.Profile = doomedProfile
	return be
}

func setFileDigest(t *testing.T, p *renderplan.Plan, path, digest string) {
	t.Helper()
	for i := range p.Files {
		if p.Files[i].Path == path {
			p.Files[i].Digest = digest
			p.Files[i].Content = digest
			p.Files[i].ContentKnown = true
			return
		}
	}
	t.Fatalf("plan has no file %q", path)
}
