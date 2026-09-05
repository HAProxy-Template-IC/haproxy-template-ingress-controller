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

	"gitlab.com/haproxy-haptic/haptic/pkg/dataplane"
	"gitlab.com/haproxy-haptic/haptic/pkg/dataplane/renderartifact"
	"gitlab.com/haproxy-haptic/haptic/pkg/dataplane/renderplan"
)

func TestTemplateRenderedEventSnapshotConstructorPreservesExactIdentity(t *testing.T) {
	snapshot, err := dataplane.BuildAuxiliaryFileSnapshot(renderartifact.NewAuthority(), nil, nil)
	require.NoError(t, err)
	plan := &renderplan.Plan{SchemaVersion: renderplan.SchemaVersion, ID: "plan"}

	event, err := NewTemplateRenderedEventWithSnapshots(
		"cfg", snapshot, nil, nil, 0, 1, "test", "checksum", plan, plan.ID, true,
	)
	require.NoError(t, err)
	assert.Same(t, snapshot, event.AuxiliaryFileSnapshot)
	assert.Nil(t, event.AuxiliaryFiles)
}

func TestTemplateRenderedEventSnapshotConstructorRejectsInvalidRoots(t *testing.T) {
	for name, snapshot := range map[string]*renderartifact.Snapshot{
		"nil":  nil,
		"zero": {},
	} {
		t.Run(name, func(t *testing.T) {
			event, err := NewTemplateRenderedEventWithSnapshots(
				"cfg", snapshot, nil, nil, 0, 1, "test", "checksum", nil, "plan", true,
			)
			require.Error(t, err)
			assert.Nil(t, event)
		})
	}
}
