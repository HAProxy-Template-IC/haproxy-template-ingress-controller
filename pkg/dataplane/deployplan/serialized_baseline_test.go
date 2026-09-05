// Copyright 2026 Philipp Hossner
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
// http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package deployplan_test

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"gitlab.com/haproxy-haptic/haptic/pkg/dataplane/deployplan"
	"gitlab.com/haproxy-haptic/haptic/pkg/dataplane/renderplan"
)

// throughAgentState is the baseline the controller actually diffs against: the
// plan a pod ACKed, reconstructed from the agent's /v1/state. Section.Text,
// Backend.Body/Comments and File.Content are json:"-", so they do not survive
// the trip and only the digests remain.
func throughAgentState(t *testing.T, plan *renderplan.Plan) *renderplan.Plan {
	t.Helper()
	encoded, err := json.Marshal(plan)
	require.NoError(t, err)
	var restored renderplan.Plan
	require.NoError(t, json.Unmarshal(encoded, &restored))

	for i := range restored.Sections {
		require.False(t, restored.Sections[i].TextKnown, "section text must not survive serialization")
	}
	for name := range restored.Backends {
		require.False(t, restored.Backends[name].ContentKnown, "backend content must not survive serialization")
	}
	for i := range restored.Files {
		require.False(t, restored.Files[i].ContentKnown, "file content must not survive serialization")
	}
	return &restored
}

// asRendered stamps the body and comment digests the render path always sets
// (plan_prepared.go digests the joined lines, so an empty body still hashes to
// a real value). The plain test fixtures leave them zero, which no produced
// plan ever is.
func asRendered(backend *renderplan.Backend) *renderplan.Backend {
	backend.BodyDigest = renderplan.DigestString("")
	backend.CommentsDigest = renderplan.DigestString("")
	return backend
}

// TestDiffAgainstSerializedBaselineDoesNotReload pins the digest fallback. The
// exact-content comparisons can only fire when both sides carry content, and a
// baseline reconstructed from the agent never does — so without the fallback
// every section, file and backend reads as changed and every reconcile reloads
// the fleet, including one that rendered byte-identical output.
func TestDiffAgainstSerializedBaselineDoesNotReload(t *testing.T) {
	rendered := basePlan(withBackend(asRendered(dynBackend("be-a", srv("SRV_1", "10.0.0.1", 8080)))))
	baseline := on34(throughAgentState(t, rendered))

	got := deployplan.Diff(rendered, baseline)

	assert.NotEqual(t, deployplan.VerdictReload, got.Verdict,
		"an unchanged render against an ACKed baseline must not reload; reasons: %v", got.Reasons)
	assert.Empty(t, got.Ops, "an unchanged render must compose no ops")
}

// TestDiffAgainstSerializedBaselineStillDetectsChange is the negative control:
// the fallback must not make everything compare equal.
func TestDiffAgainstSerializedBaselineStillDetectsChange(t *testing.T) {
	rendered := basePlan(withBackend(dynBackend("be-a", srv("SRV_1", "10.0.0.1", 8080))))
	baseline := on34(throughAgentState(t, rendered))

	changed := planWith(
		withCore("global", "global\n maxconn 100\n"),
		withProfile(testProfile, "body-1"),
		withBackend(dynBackend("be-a", srv("SRV_1", "10.0.0.1", 8080))),
	)

	got := deployplan.Diff(changed, baseline)

	assert.Equal(t, deployplan.VerdictReload, got.Verdict)
	reasonsContain(t, got.Reasons, "core section global changed")
}
