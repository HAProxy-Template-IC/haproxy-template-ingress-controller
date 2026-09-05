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

package renderer

import (
	"testing"

	iradix "github.com/hashicorp/go-immutable-radix/v2"
	"github.com/stretchr/testify/require"

	"gitlab.com/haproxy-haptic/haptic/pkg/incremental"
	"gitlab.com/haproxy-haptic/haptic/pkg/templating"
)

func TestIncrementalStateSnapshotAuthenticationRejectsRootPoison(t *testing.T) {
	tests := map[string]func(*incrementalStateSnapshot){
		"cursor": func(snapshot *incrementalStateSnapshot) {
			snapshot.cursors["routes"] = incrementalStoreCursor{sequence: 1}
		},
		"bindings": func(snapshot *incrementalStateSnapshot) {
			snapshot.bindings = iradix.New[string]()
		},
		"members": func(snapshot *incrementalStateSnapshot) {
			snapshot.members = iradix.New[struct{}]()
		},
		"active groups": func(snapshot *incrementalStateSnapshot) {
			snapshot.activeGroups = newIncrementalActiveGroupIndex()
		},
		"retired": func(snapshot *incrementalStateSnapshot) {
			snapshot.retired = iradix.New[struct{}]()
		},
		"results": func(snapshot *incrementalStateSnapshot) {
			snapshot.results = iradix.New[incremental.ExactValueRoot]()
		},
		"group index": func(snapshot *incrementalStateSnapshot) {
			snapshot.groupIndexes["routes"] = newIncrementalGroupIndex()
		},
		"group ready": func(snapshot *incrementalStateSnapshot) {
			snapshot.groupReady["routes"] = true
		},
		"status plan": func(snapshot *incrementalStateSnapshot) {
			snapshot.statusPlan = templating.NewStatusPatchProjectionPlan()
		},
	}
	for name, poison := range tests {
		t.Run(name, func(t *testing.T) {
			snapshot := newIncrementalStateSnapshot()
			require.NoError(t, validateIncrementalStateSnapshotAuthentication(snapshot))
			poison(snapshot)
			require.Error(t, validateIncrementalStateSnapshotAuthentication(snapshot))
		})
	}
}

func TestPreparedIncrementalStateCommitRejectsSnapshotPoison(t *testing.T) {
	snapshot := newIncrementalStateSnapshot()
	state := &incrementalRenderState{snapshot: snapshot}
	prepared := &preparedIncrementalStateCommit{
		runtime: &incrementalRenderSession{state: state}, snapshot: snapshot, detached: true,
	}
	require.NoError(t, prepared.validateDetachedPublication())

	snapshot.results = iradix.New[incremental.ExactValueRoot]()
	require.Error(t, prepared.validateDetachedPublication())
}

func TestIncrementalStateSnapshotAuthenticationRejectsCopiedAuthority(t *testing.T) {
	snapshot := newIncrementalStateSnapshot()
	copied := *snapshot
	require.Error(t, validateIncrementalStateSnapshotAuthentication(&copied))

	forged := *snapshot.auth
	forged.snapshot = &copied
	copied.auth = &forged
	require.Error(t, validateIncrementalStateSnapshotAuthentication(&copied))
}
