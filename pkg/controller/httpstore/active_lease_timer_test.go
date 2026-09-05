// Copyright 2026 Philipp Hossner
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

package httpstore

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"gitlab.com/haproxy-haptic/haptic/pkg/controller/testutil"
	purehttpstore "gitlab.com/haproxy-haptic/haptic/pkg/httpstore"
)

type activeLeaseTimerState struct {
	timer            *time.Timer
	registered       bool
	managed          bool
	pending          bool
	immediate        bool
	generation       uint64
	sourceGeneration uint64
}

func TestActiveLeaseTimerChangesOnlyAtZeroReferenceTransitions(t *testing.T) {
	component, url, descriptor := activeLeaseTimerFixture(t)
	set, token, err := component.NewActiveLeaseSet()
	require.NoError(t, err)

	empty := activeLeaseTimerSnapshot(component, url)
	token = publishComponentActiveLease(t, component, set, token, []purehttpstore.ActiveLeaseUpdate{{
		URL: url, Descriptor: descriptor, Added: 1,
	}})
	active := activeLeaseTimerSnapshot(component, url)
	require.True(t, active.registered)
	require.NotEqual(t, empty, active)

	token = publishComponentActiveLease(t, component, set, token, []purehttpstore.ActiveLeaseUpdate{{
		URL: url, Descriptor: descriptor, Added: 1,
	}})
	assert.Equal(t, active, activeLeaseTimerSnapshot(component, url))
	token = publishComponentActiveLease(t, component, set, token, []purehttpstore.ActiveLeaseUpdate{{
		URL: url, Descriptor: descriptor, Removed: 1,
	}})
	assert.Equal(t, active, activeLeaseTimerSnapshot(component, url))
	_ = publishComponentActiveLease(t, component, set, token, []purehttpstore.ActiveLeaseUpdate{{
		URL: url, Descriptor: descriptor, Removed: 1,
	}})
	assert.Equal(t, activeLeaseTimerState{generation: active.generation + 1}, activeLeaseTimerSnapshot(component, url))
}

func TestRetiringActiveLeaseStopsOnlyTheLastOwnerTimer(t *testing.T) {
	component, url, descriptor := activeLeaseTimerFixture(t)
	first, firstToken, err := component.NewActiveLeaseSet()
	require.NoError(t, err)
	second, secondToken, err := component.NewActiveLeaseSet()
	require.NoError(t, err)
	firstToken = publishComponentActiveLease(t, component, first, firstToken, []purehttpstore.ActiveLeaseUpdate{{
		URL: url, Descriptor: descriptor, Added: 1,
	}})
	secondToken = publishComponentActiveLease(t, component, second, secondToken, []purehttpstore.ActiveLeaseUpdate{{
		URL: url, Descriptor: descriptor, Added: 1,
	}})
	active := activeLeaseTimerSnapshot(component, url)
	require.True(t, active.registered)

	require.NoError(t, component.RetireActiveLeases(first, firstToken))
	assert.True(t, component.store.HasActiveLease(url))
	assert.Equal(t, active, activeLeaseTimerSnapshot(component, url))
	require.NoError(t, component.RetireActiveLeases(second, secondToken))
	assert.False(t, component.store.HasActiveLease(url))
	assert.Equal(t, activeLeaseTimerState{generation: active.generation + 1}, activeLeaseTimerSnapshot(component, url))
}

func TestSourceReplacementPreservesTimerAndDirtiesOldLease(t *testing.T) {
	component, url, descriptor := activeLeaseTimerFixture(t)
	set, token, err := component.NewActiveLeaseSet()
	require.NoError(t, err)
	token = publishComponentActiveLease(t, component, set, token, []purehttpstore.ActiveLeaseUpdate{{
		URL: url, Descriptor: descriptor, Added: 1,
	}})
	before := activeLeaseTimerSnapshot(component, url)

	replacement, err := component.store.StageSource(
		url,
		purehttpstore.FetchOptions{Delay: time.Hour},
		&purehttpstore.AuthConfig{Type: purehttpstore.AuthTypeBearer, Token: "replacement"},
	)
	require.NoError(t, err)
	require.True(t, replacement.Changed())
	transaction := newInputTransaction(component)
	_, err = transaction.enrollSource(replacement)
	require.NoError(t, err)
	prepared, err := transaction.PrepareCommitPreservingRefreshers(t.Context(), nil)
	require.NoError(t, err)
	require.True(t, prepared.Publish())
	prepared.Release()

	assert.Equal(t, before, activeLeaseTimerSnapshot(component, url))
	snapshot, err := component.BeginActiveLeases(set, token)
	require.NoError(t, err)
	require.Len(t, snapshot.Changes(), 1)
	assert.Equal(t, descriptor, snapshot.Changes()[0].Descriptor)
	assert.True(t, snapshot.Contains(url, descriptor))
}

func activeLeaseTimerFixture(
	t *testing.T,
) (*Component, string, purehttpstore.SourceDescriptor) {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("content"))
	}))
	t.Cleanup(server.Close)
	bus, logger := testutil.NewTestBusAndLogger()
	component := New(bus, logger, 0)
	options := purehttpstore.FetchOptions{Delay: time.Hour}
	_, err := component.store.Fetch(t.Context(), server.URL, options, nil)
	require.NoError(t, err)
	descriptor, err := purehttpstore.DescribeSource(options, nil)
	require.NoError(t, err)
	t.Cleanup(func() { component.StopRefresher(server.URL) })
	return component, server.URL, descriptor
}

func publishComponentActiveLease(
	t *testing.T,
	component *Component,
	set *purehttpstore.ActiveLeaseSet,
	token purehttpstore.ActiveLeaseToken,
	updates []purehttpstore.ActiveLeaseUpdate,
) purehttpstore.ActiveLeaseToken {
	t.Helper()
	snapshot, err := component.BeginActiveLeases(set, token)
	require.NoError(t, err)
	prepared, err := component.PrepareObservationCommitWithActiveLeases(
		t.Context(), nil, &purehttpstore.ActiveLeaseCommit{Snapshot: snapshot, Updates: updates},
	)
	require.NoError(t, err)
	next, _, planned := prepared.PlannedActiveLeases()
	require.True(t, planned)
	require.True(t, prepared.Publish())
	prepared.Release()
	return next
}

func activeLeaseTimerSnapshot(component *Component, url string) activeLeaseTimerState {
	component.mu.Lock()
	defer component.mu.Unlock()
	timer, registered := component.refreshers[url]
	return activeLeaseTimerState{
		timer:            timer,
		registered:       registered,
		managed:          component.refreshManaged[url],
		pending:          component.refreshPending[url],
		immediate:        component.refreshImmediate[url],
		generation:       component.refreshGeneration[url],
		sourceGeneration: component.refreshSourceGeneration[url],
	}
}
