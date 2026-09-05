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

package renderartifact

import (
	"sync"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestReconcileSnapshotTransitionsExactDesiredState(t *testing.T) {
	authority := NewAuthority()
	base := buildReconcileSnapshot(t, authority, nil, []reconcileArtifactFixture{
		{descriptor: Descriptor{Family: Map, Path: "maps/a.map"}, content: "a"},
		{descriptor: Descriptor{Family: Map, Path: "maps/delete.map"}, content: "delete"},
		{descriptor: Descriptor{Family: General, Name: "keep", Path: "general/keep"}, content: "keep"},
	})
	desired := buildReconcileSnapshot(t, authority, base, []reconcileArtifactFixture{
		{descriptor: Descriptor{Family: Map, Path: "maps/a.map"}, content: "changed"},
		{descriptor: Descriptor{Family: Certificate, Path: "ssl/insert.pem"}, content: "insert"},
		{descriptor: Descriptor{Family: General, Name: "keep", Path: "general/keep"}, content: "keep"},
	})

	next, delta, err := ReconcileSnapshot(authority, base, desired)
	require.NoError(t, err)
	require.NotSame(t, base, next)
	require.NoError(t, delta.ValidateAuthentication())
	equal, err := next.ExactEqual(desired)
	require.NoError(t, err)
	require.True(t, equal)
	changes, err := delta.Changes()
	require.NoError(t, err)
	require.Len(t, changes, 3)
	structural, err := delta.RequiresFullValidation()
	require.NoError(t, err)
	require.True(t, structural)
}

func TestReconcileSnapshotNoOpReusesExactBase(t *testing.T) {
	authority := NewAuthority()
	base := buildReconcileSnapshot(t, authority, nil, []reconcileArtifactFixture{
		{descriptor: Descriptor{Family: Map, Path: "maps/a.map"}, content: "a"},
	})

	next, delta, err := ReconcileSnapshot(authority, base, base)
	require.NoError(t, err)
	require.Same(t, base, next)
	same, err := delta.SameRoot()
	require.NoError(t, err)
	require.True(t, same)
}

func TestReconcileSnapshotFailsClosedOnForeignRoots(t *testing.T) {
	authority := NewAuthority()
	base := buildReconcileSnapshot(t, authority, nil, nil)
	foreignAuthority := NewAuthority()
	foreign := buildReconcileSnapshot(t, foreignAuthority, nil, nil)

	_, _, err := ReconcileSnapshot(authority, foreign, base)
	require.Error(t, err)
	_, _, err = ReconcileSnapshot(authority, base, foreign)
	require.Error(t, err)
	_, _, err = ReconcileSnapshot(nil, base, base)
	require.Error(t, err)
}

func TestReconcileSnapshotConcurrentNoOpIsRaceSafe(t *testing.T) {
	authority := NewAuthority()
	base := buildReconcileSnapshot(t, authority, nil, []reconcileArtifactFixture{
		{descriptor: Descriptor{Family: Map, Path: "maps/a.map"}, content: "a"},
	})

	var wait sync.WaitGroup
	for range 16 {
		wait.Add(1)
		go func() {
			defer wait.Done()
			for range 50 {
				next, delta, err := ReconcileSnapshot(authority, base, base)
				require.NoError(t, err)
				require.Same(t, base, next)
				require.NoError(t, delta.ValidateAuthentication())
			}
		}()
	}
	wait.Wait()
}

type reconcileArtifactFixture struct {
	descriptor Descriptor
	content    string
}

func buildReconcileSnapshot(
	tb testing.TB,
	authority *Authority,
	previous *Snapshot,
	artifacts []reconcileArtifactFixture,
) *Snapshot {
	tb.Helper()
	builder, err := NewBuilder(authority, previous)
	require.NoError(tb, err)
	for _, artifact := range artifacts {
		require.NoError(tb, builder.Add(
			artifact.descriptor,
			NewLiteralContent(artifact.content),
		))
	}
	snapshot, err := builder.Build()
	require.NoError(tb, err)
	return snapshot
}
