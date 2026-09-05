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

package events

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"gitlab.com/haproxy-haptic/haptic/pkg/dataplane/renderplan"
	"gitlab.com/haproxy-haptic/haptic/pkg/templating"
)

func TestStatusPatchSnapshotConstructorsPreserveExactIdentity(t *testing.T) {
	collector := templating.NewStatusPatchCollector()
	require.NoError(t, collector.Register(
		"default", "route", "example.test/v1", "Route",
		map[string]map[string]any{"rendered": {"owner": "stable"}},
	))
	snapshot, err := collector.Snapshot()
	require.NoError(t, err)
	plan := &renderplan.Plan{SchemaVersion: renderplan.SchemaVersion, ID: "plan"}

	rendered := NewTemplateRenderedEventWithStatusSnapshot(
		"cfg", nil, snapshot, nil, 0, 0, "test", "checksum", plan, plan.ID, true,
	)
	completed := NewReconciliationCompletedEventWithStatusSnapshot(0, plan.ID, nil, snapshot)
	resources := NewResourcesAppliedEventWithStatusSnapshot(snapshot)
	failed := NewReconciliationFailedEventWithStatusSnapshot("failure", "render", snapshot)
	scheduled := NewDeploymentScheduledEventWithStatusSnapshot(
		"cfg", nil, nil, "", "", "test", "checksum", plan, plan.ID,
		rendered.RenderProof, snapshot, true,
	)
	deployed := NewDeploymentCompletedEvent(&DeploymentResult{StatusPatchSnapshot: snapshot, Plan: plan})
	skipped := NewDeploymentSkippedEventWithStatusSnapshot(
		1, SkipReasonConfigUnchanged, "checksum", "pods", snapshot, rendered.RenderProof, plan,
	)

	assert.Same(t, snapshot, rendered.StatusPatchSnapshot)
	assert.Same(t, snapshot, completed.StatusPatchSnapshot)
	assert.Same(t, snapshot, resources.StatusPatchSnapshot)
	assert.Same(t, snapshot, failed.StatusPatchSnapshot)
	assert.Same(t, snapshot, scheduled.StatusPatchSnapshot)
	assert.Same(t, snapshot, deployed.StatusPatchSnapshot)
	assert.Same(t, snapshot, skipped.StatusPatchSnapshot)
	assert.Nil(t, rendered.StatusPatches)
	assert.Nil(t, completed.StatusPatches)
	assert.Nil(t, scheduled.StatusPatches)
}
