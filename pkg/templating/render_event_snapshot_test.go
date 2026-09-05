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

package templating

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRenderedEventSnapshotReusesOnlyExactPreviousState(t *testing.T) {
	newSnapshot := func(t *testing.T, message string, previous ...*RenderedEventSnapshot) *RenderedEventSnapshot {
		t.Helper()
		collector := NewEventCollector()
		require.NoError(t, collector.Register(
			"default", "route", "example.test/v1", "Route", EventTypeWarning, "Conflict", message,
		))
		snapshot, err := collector.Snapshot(previous...)
		require.NoError(t, err)
		return snapshot
	}

	first := newSnapshot(t, "stable")
	reused := newSnapshot(t, "stable", first)
	assert.Same(t, first, reused)

	foreignEqual := newSnapshot(t, "stable")
	equal, err := first.ExactEqual(foreignEqual)
	require.NoError(t, err)
	assert.True(t, equal)
	sameRoot, err := first.SameRoot(foreignEqual)
	require.NoError(t, err)
	assert.False(t, sameRoot)

	changed := newSnapshot(t, "changed", first)
	equal, err = first.ExactEqual(changed)
	require.NoError(t, err)
	assert.False(t, equal)
	sameRoot, err = first.SameRoot(changed)
	require.NoError(t, err)
	assert.False(t, sameRoot)
}

func TestRenderedEventSnapshotSealsCollector(t *testing.T) {
	collector := NewEventCollector()
	require.NoError(t, collector.Register(
		"default", "route", "example.test/v1", "Route", EventTypeWarning, "Conflict", "stable",
	))
	first, err := collector.Snapshot()
	require.NoError(t, err)
	second, err := collector.Snapshot()
	require.NoError(t, err)
	assert.Same(t, first, second)
	require.ErrorContains(t, collector.Register(
		"default", "later", "example.test/v1", "Route", EventTypeNormal, "Ready", "later",
	), "sealed")
}

func TestRenderedEventSnapshotRejectsCopiedAndSubstitutedState(t *testing.T) {
	newSnapshot := func(t *testing.T, name string) *RenderedEventSnapshot {
		t.Helper()
		collector := NewEventCollector()
		require.NoError(t, collector.Register(
			"default", name, "example.test/v1", "Route", EventTypeWarning, "Conflict", "stable",
		))
		snapshot, err := collector.Snapshot()
		require.NoError(t, err)
		return snapshot
	}
	first := newSnapshot(t, "first")
	second := newSnapshot(t, "second")

	copied := *first
	require.ErrorContains(t, copied.ValidateAuthentication(), "invalid provenance")

	substituted := *first
	substituted.seal = &substituted
	substituted.storage = second.storage
	require.ErrorContains(t, substituted.ValidateAuthentication(), "invalid provenance")

	first.auth.count++
	require.ErrorContains(t, first.ValidateAuthentication(), "invalid provenance")
}

func TestRenderedEventSnapshotCompatibilityViewIsDetached(t *testing.T) {
	collector := NewEventCollector()
	require.NoError(t, collector.Register(
		"default", "route", "example.test/v1", "Route", EventTypeWarning, "Conflict", "stable",
	))
	snapshot, err := collector.Snapshot()
	require.NoError(t, err)

	first, err := snapshot.Events()
	require.NoError(t, err)
	first[0].Message = "poison"
	second, err := snapshot.Events()
	require.NoError(t, err)
	assert.Equal(t, "stable", second[0].Message)
	collectorView := collector.Events()
	assert.Equal(t, "stable", collectorView[0].Message)
}

func TestRenderedEventSnapshotAddedSinceReturnsOnlyExactAdditions(t *testing.T) {
	newSnapshot := func(t *testing.T, names ...string) *RenderedEventSnapshot {
		t.Helper()
		collector := NewEventCollector()
		for _, name := range names {
			require.NoError(t, collector.Register(
				"default", name, "example.test/v1", "Route", EventTypeWarning, "Conflict", "message-"+name,
			))
		}
		snapshot, err := collector.Snapshot()
		require.NoError(t, err)
		return snapshot
	}

	previous := newSnapshot(t, "removed", "stable")
	current := newSnapshot(t, "added", "stable")
	added, err := current.AddedSince(previous)
	require.NoError(t, err)
	require.Len(t, added, 1)
	assert.Equal(t, "added", added[0].Name)

	added[0].Message = "poison"
	currentEvents, err := current.Events()
	require.NoError(t, err)
	assert.Equal(t, "message-added", currentEvents[0].Message)

	all, err := current.AddedSince(nil)
	require.NoError(t, err)
	assert.Equal(t, []string{"added", "stable"}, []string{all[0].Name, all[1].Name})
}

func TestRenderedEventSnapshotAddedSinceSameRootDoesNotMaterialize(t *testing.T) {
	collector := NewEventCollector()
	require.NoError(t, collector.Register(
		"default", "route", "example.test/v1", "Route", EventTypeWarning, "Conflict", "stable",
	))
	snapshot, err := collector.Snapshot()
	require.NoError(t, err)

	added, err := snapshot.AddedSince(snapshot)
	require.NoError(t, err)
	assert.Nil(t, added)
	assert.Zero(t, testing.AllocsPerRun(100, func() {
		unchanged, deltaErr := snapshot.AddedSince(snapshot)
		if deltaErr != nil || unchanged != nil {
			panic("same event root materialized")
		}
	}))
}

func TestRenderedEventSnapshotAddedSinceRejectsInvalidProvenance(t *testing.T) {
	collector := NewEventCollector()
	snapshot, err := collector.Snapshot()
	require.NoError(t, err)

	invalid := &RenderedEventSnapshot{}
	_, err = invalid.AddedSince(snapshot)
	require.ErrorContains(t, err, "invalid provenance")
	_, err = snapshot.AddedSince(invalid)
	require.ErrorContains(t, err, "invalid provenance")
}

func TestRenderedEventSnapshotSortsRegistrationOrder(t *testing.T) {
	collector := NewEventCollector()
	require.NoError(t, collector.Register(
		"default", "z", "example.test/v1", "Route", EventTypeWarning, "Conflict", "z",
	))
	require.NoError(t, collector.Register(
		"default", "a", "example.test/v1", "Route", EventTypeWarning, "Conflict", "a",
	))
	snapshot, err := collector.Snapshot()
	require.NoError(t, err)
	events, err := snapshot.Events()
	require.NoError(t, err)
	require.Len(t, events, 2)
	assert.Equal(t, []string{"a", "z"}, []string{events[0].Name, events[1].Name})
}
