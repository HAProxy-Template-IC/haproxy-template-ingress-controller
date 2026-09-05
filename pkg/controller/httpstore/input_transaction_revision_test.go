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
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"gitlab.com/haproxy-haptic/haptic/pkg/controller/events"
	"gitlab.com/haproxy-haptic/haptic/pkg/controller/testutil"
	purehttpstore "gitlab.com/haproxy-haptic/haptic/pkg/httpstore"
)

func TestPreparedInputCommitRetainsAcceptedReadVisibility(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("candidate"))
	}))
	defer server.Close()

	bus, logger := testutil.NewTestBusAndLogger()
	component := New(bus, logger, 0)
	wrapper := NewHTTPStoreWrapper(t.Context(), component, logger, nil, SourceModeAuthoritative)
	_, err := wrapper.Fetch(server.URL, map[string]any{"critical": true})
	require.NoError(t, err)
	prepared, err := wrapper.InputTransaction().PrepareCommit(t.Context())
	require.NoError(t, err)

	read := make(chan purehttpstore.ContentSnapshot, 1)
	started := make(chan struct{})
	go func() {
		close(started)
		snapshot, _ := component.AcceptedSnapshot(
			server.URL,
			wrapper.InputTransaction().Snapshots()[0].Descriptor,
		)
		read <- snapshot
	}()
	<-started
	assert.Never(t, func() bool { return len(read) > 0 }, 25*time.Millisecond, time.Millisecond)

	prepared.Publish()
	require.NoError(t, prepared.ValidatePublishedPublication())
	require.NoError(t, prepared.CommitPublishedPublication())
	tentative := wrapper.InputTransaction().Snapshots()
	require.Len(t, tentative, 1)
	assert.Equal(t, purehttpstore.SnapshotInitialCandidate, tentative[0].Token.Kind())
	assert.Never(t, func() bool { return len(read) > 0 }, 25*time.Millisecond, time.Millisecond)
	prepared.ReleaseCommittedPublication()
	committed := wrapper.InputTransaction().Snapshots()
	require.Len(t, committed, 1)
	assert.Equal(t, purehttpstore.SnapshotAccepted, committed[0].Token.Kind())

	select {
	case snapshot := <-read:
		assert.True(t, snapshot.Found)
		assert.Equal(t, "candidate", snapshot.Content)
	case <-time.After(time.Second):
		t.Fatal("accepted HTTP reader remained blocked after release")
	}
}

func TestPreparedInputCommitRetainsSelectiveActiveReplayState(t *testing.T) {
	bus, logger := testutil.NewTestBusAndLogger()
	component := New(bus, logger, 0)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		_, _ = w.Write([]byte(strings.TrimPrefix(request.URL.Path, "/")))
	}))
	defer server.Close()
	selectedURL := server.URL + "/selected"
	graphURL := server.URL + "/graph"
	options := purehttpstore.FetchOptions{Critical: true}
	_, err := component.store.Fetch(t.Context(), selectedURL, options, nil)
	require.NoError(t, err)
	_, err = component.store.Fetch(t.Context(), graphURL, options, nil)
	require.NoError(t, err)
	wrapper := NewHTTPStoreWrapper(t.Context(), component, logger, nil, SourceModeAuthoritative)
	_, selected, err := wrapper.FetchSnapshot(selectedURL, map[string]any{"critical": true})
	require.NoError(t, err)
	_, _, err = wrapper.FetchSnapshot(graphURL, map[string]any{"critical": true})
	require.NoError(t, err)
	replay, ok := wrapper.CaptureAcceptedReplayState([]purehttpstore.ContentSnapshot{selected})
	require.True(t, ok)
	set, token, err := component.NewActiveLeaseSet()
	require.NoError(t, err)
	lease, err := component.BeginActiveLeases(set, token)
	require.NoError(t, err)

	prepared, err := wrapper.InputTransaction().PrepareCommitWithObservationsAndActiveLeases(
		t.Context(), nil, &purehttpstore.ActiveLeaseCommit{Snapshot: lease, Replay: replay},
	)
	require.NoError(t, err)
	token, _, ok = prepared.PlannedActiveLeases()
	require.True(t, ok)
	require.True(t, prepared.Publish())
	_, ok = wrapper.CommittedAcceptedReplayState()
	require.False(t, ok)
	prepared.Release()
	committed, ok := wrapper.CommittedAcceptedReplayState()
	require.True(t, ok)
	require.NoError(t, committed.ValidateAuthentication())
	require.Equal(t, []purehttpstore.ContentSnapshot{selected}, committed.Snapshots())
	require.NoError(t, component.RetireActiveLeases(set, token))
}

func TestPreparedInputCommitAbortDoesNotAcceptCandidate(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("candidate"))
	}))
	defer server.Close()

	bus, logger := testutil.NewTestBusAndLogger()
	component := New(bus, logger, 0)
	wrapper := NewHTTPStoreWrapper(t.Context(), component, logger, nil, SourceModeAuthoritative)
	_, snapshot, err := wrapper.FetchSnapshot(server.URL, map[string]any{"critical": true})
	require.NoError(t, err)
	prepared, err := wrapper.InputTransaction().PrepareCommit(t.Context())
	require.NoError(t, err)
	prepared.Abort()

	accepted, found := component.AcceptedSnapshot(server.URL, snapshot.Descriptor)
	assert.False(t, found)
	assert.False(t, accepted.Found)
	assert.False(t, wrapper.InputTransaction().Cacheable())
}

func TestPreparedInputCommitPublishSealedFailsClosedBeforeMutation(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("candidate"))
	}))
	defer server.Close()
	prepare := func(t *testing.T) (*Component, purehttpstore.ContentSnapshot, *PreparedInputCommit) {
		t.Helper()
		bus, logger := testutil.NewTestBusAndLogger()
		component := New(bus, logger, 0)
		wrapper := NewHTTPStoreWrapper(t.Context(), component, logger, nil, SourceModeAuthoritative)
		_, snapshot, err := wrapper.FetchSnapshot(server.URL, map[string]any{"critical": true})
		require.NoError(t, err)
		prepared, err := wrapper.InputTransaction().PrepareCommit(t.Context())
		require.NoError(t, err)
		return component, snapshot, prepared
	}

	t.Run("unsealed", func(t *testing.T) {
		component, snapshot, prepared := prepare(t)
		assert.PanicsWithValue(t, "prepared HTTP input publication is not sealed", prepared.PublishSealed)
		prepared.Abort()
		accepted, found := component.AcceptedSnapshot(server.URL, snapshot.Descriptor)
		assert.False(t, found)
		assert.False(t, accepted.Found)
	})

	t.Run("sealed", func(t *testing.T) {
		component, snapshot, prepared := prepare(t)
		require.NoError(t, prepared.SealPublication())
		assert.Equal(t, preparedInputSealed, prepared.state)
		assert.Equal(t, componentPreparedSealed, prepared.component.state)
		prepared.PublishSealed()
		prepared.Release()
		accepted, found := component.AcceptedSnapshot(server.URL, snapshot.Descriptor)
		assert.True(t, found)
		assert.Equal(t, "candidate", accepted.Content)
	})
}

func TestPreparedInputCommitAbortRollsBackTentativePublication(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("candidate"))
	}))
	defer server.Close()
	bus, logger := testutil.NewTestBusAndLogger()
	component := New(bus, logger, 0)
	wrapper := NewHTTPStoreWrapper(t.Context(), component, logger, nil, SourceModeAuthoritative)
	_, snapshot, err := wrapper.FetchSnapshot(server.URL, map[string]any{
		"critical": true, "delay": "1h",
	})
	require.NoError(t, err)
	prepared, err := wrapper.InputTransaction().PrepareCommit(t.Context())
	require.NoError(t, err)
	require.NoError(t, prepared.SealPublication())
	prepared.PublishSealed()
	require.NoError(t, prepared.ValidatePublishedPublication())
	require.NoError(t, prepared.CommitPublishedPublication())
	prepared.Abort()

	accepted, found := component.AcceptedSnapshot(server.URL, snapshot.Descriptor)
	assert.False(t, found)
	assert.False(t, accepted.Found)
	assert.False(t, wrapper.InputTransaction().Cacheable())
	assert.Nil(t, currentRefresher(component, server.URL))

	retry := NewHTTPStoreWrapper(t.Context(), component, logger, nil, SourceModeAuthoritative)
	_, _, err = retry.FetchSnapshot(server.URL, map[string]any{
		"critical": true, "delay": "1h",
	})
	require.NoError(t, err)
	require.NoError(t, retry.InputTransaction().Commit(t.Context()))
	_, found = component.AcceptedSnapshot(server.URL, snapshot.Descriptor)
	assert.True(t, found)
	assert.NotNil(t, currentRefresher(component, server.URL))
}

func TestPreparedInputCommitValidatesPublishedPlans(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("candidate"))
	}))
	defer server.Close()
	bus, logger := testutil.NewTestBusAndLogger()
	component := New(bus, logger, 0)
	wrapper := NewHTTPStoreWrapper(t.Context(), component, logger, nil, SourceModeAuthoritative)
	_, snapshot, err := wrapper.FetchSnapshot(server.URL, map[string]any{"critical": true})
	require.NoError(t, err)
	prepared, err := wrapper.InputTransaction().PrepareCommit(t.Context())
	require.NoError(t, err)
	require.NoError(t, prepared.SealPublication())
	prepared.PublishSealed()
	require.NoError(t, prepared.ValidatePublishedPublication())
	original := prepared.transactionPlan.snapshots[0]
	prepared.transactionPlan.snapshots[0].Content = "poison"
	require.Error(t, prepared.ValidatePublishedPublication())
	prepared.transactionPlan.snapshots[0] = original
	require.NoError(t, prepared.CommitPublishedPublication())
	prepared.Abort()
	accepted, found := component.AcceptedSnapshot(server.URL, snapshot.Descriptor)
	assert.False(t, found)
	assert.False(t, accepted.Found)
	assert.False(t, wrapper.InputTransaction().Cacheable())
}

func TestPreparedInputCommitRejectsPostSealReleasePlanPoison(t *testing.T) {
	tests := map[string]func(*Component, *preparedComponentReleasePlan) func(){
		"component timer root": func(component *Component, _ *preparedComponentReleasePlan) func() {
			original := component.refreshers
			component.refreshers = nil
			return func() { component.refreshers = original }
		},
		"reconcile action": func(_ *Component, plan *preparedComponentReleasePlan) func() {
			original := plan.reconcile[0]
			plan.reconcile[0].url = ""
			return func() { plan.reconcile[0] = original }
		},
		"stopped state": func(_ *Component, plan *preparedComponentReleasePlan) func() {
			plan.stopped = !plan.stopped
			return func() { plan.stopped = !plan.stopped }
		},
		"component authority": func(component *Component, _ *preparedComponentReleasePlan) func() {
			original := component.prepareAuthority
			component.prepareAuthority = nil
			return func() { component.prepareAuthority = original }
		},
		"event bus": func(component *Component, _ *preparedComponentReleasePlan) func() {
			original := component.eventBus
			component.eventBus = nil
			return func() { component.eventBus = original }
		},
	}
	for name, poison := range tests {
		t.Run(name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				_, _ = w.Write([]byte("candidate"))
			}))
			defer server.Close()
			bus, logger := testutil.NewTestBusAndLogger()
			component := New(bus, logger, 0)
			wrapper := NewHTTPStoreWrapper(t.Context(), component, logger, nil, SourceModeAuthoritative)
			_, snapshot, err := wrapper.FetchSnapshot(server.URL, map[string]any{
				"critical": true, "delay": "1h",
			})
			require.NoError(t, err)
			prepared, err := wrapper.InputTransaction().PrepareCommit(t.Context())
			require.NoError(t, err)
			require.NoError(t, prepared.SealPublication())
			require.Len(t, prepared.component.releasePlan.reconcile, 1)
			restore := poison(component, prepared.component.releasePlan)

			assert.Panics(t, prepared.PublishSealed)
			assert.Equal(t, preparedInputSealed, prepared.state)
			assert.Equal(t, componentPreparedSealed, prepared.component.state)
			restore()
			require.NotPanics(t, prepared.PublishSealed)
			prepared.Release()

			accepted, found := component.AcceptedSnapshot(server.URL, snapshot.Descriptor)
			assert.True(t, found)
			assert.Equal(t, "candidate", accepted.Content)
			assert.NotNil(t, currentRefresher(component, server.URL))
		})
	}
}

func TestPreparedValidationPublicationPanicIsPostCommitOptional(t *testing.T) {
	component := &Component{}
	request := &events.ProposalValidationRequestedEvent{ID: "request"}
	assert.False(t, publishPreparedValidationRequest(component, request))
}

func TestPreparedInputCommitRequiredValidationStateRemainsRollbackCapable(t *testing.T) {
	component, prepared, _ := prepareInputWithRetiredValidation(t)
	before := prepared.component.releasePlan.pendingBefore
	after := prepared.component.releasePlan.pendingAfter
	require.NotSame(t, before, after)

	require.NoError(t, prepared.CommitPublishedPublication())
	assert.Same(t, after, component.pendingValidation)
	prepared.Abort()
	component.mu.Lock()
	assert.Same(t, before, component.pendingValidation)
	component.mu.Unlock()
}

func TestPreparedInputCommitValidationPublishPanicDiscardsExactBatch(t *testing.T) {
	component, prepared, pendingURL := prepareInputWithRetiredValidation(t)
	require.NoError(t, prepared.CommitPublishedPublication())
	require.NotNil(t, component.pendingValidation)
	component.eventBus = nil

	prepared.ReleaseCommittedPublication()
	component.mu.Lock()
	assert.Nil(t, component.pendingValidation)
	component.mu.Unlock()
	assert.NotContains(t, component.store.GetPendingURLs(), pendingURL)
	assert.True(t, prepared.transaction.Cacheable())
}

func prepareInputWithRetiredValidation(
	t *testing.T,
) (*Component, *PreparedInputCommit, string) {
	t.Helper()
	var firstBody atomic.Value
	firstBody.Store("first-accepted")
	first := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(firstBody.Load().(string)))
	}))
	t.Cleanup(first.Close)
	var secondBody atomic.Value
	secondBody.Store("second-accepted")
	second := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(secondBody.Load().(string)))
	}))
	t.Cleanup(second.Close)
	bus, logger := testutil.NewTestBusAndLogger()
	component := New(bus, logger, 0)
	options := purehttpstore.FetchOptions{Critical: true}
	_, err := component.store.Fetch(t.Context(), first.URL, options, nil)
	require.NoError(t, err)
	_, err = component.store.Fetch(t.Context(), second.URL, options, nil)
	require.NoError(t, err)
	firstBody.Store("first-pending")
	secondBody.Store("second-pending")
	firstPending, err := component.store.RefreshURLVersion(t.Context(), first.URL)
	require.NoError(t, err)
	require.NotNil(t, firstPending)
	secondPending, err := component.store.RefreshURLVersion(t.Context(), second.URL)
	require.NoError(t, err)
	require.NotNil(t, secondPending)
	_, batch := prepareValidationRequest(purehttpstore.NewHTTPOverlay(component.store), first.URL)
	require.NotNil(t, batch)
	component.pendingValidation = batch

	wrapper := NewHTTPStoreWrapper(t.Context(), component, logger, nil, SourceModeAuthoritative)
	_, _, err = wrapper.FetchSnapshot(
		first.URL,
		map[string]any{"critical": true},
		map[string]any{"type": "bearer", "token": "replacement"},
	)
	require.NoError(t, err)
	prepared, err := wrapper.InputTransaction().PrepareCommit(t.Context())
	require.NoError(t, err)
	require.NoError(t, prepared.SealPublication())
	prepared.PublishSealed()
	require.NoError(t, prepared.ValidatePublishedPublication())
	return component, prepared, second.URL
}

func TestPreparedInputCommitSealRejectsForgedReplayHandle(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("candidate"))
	}))
	defer server.Close()

	bus, logger := testutil.NewTestBusAndLogger()
	component := New(bus, logger, 0)
	wrapper := NewHTTPStoreWrapper(t.Context(), component, logger, nil, SourceModeAuthoritative)
	_, snapshot, err := wrapper.FetchSnapshot(server.URL, map[string]any{"critical": true})
	require.NoError(t, err)
	prepared, err := wrapper.InputTransaction().PrepareCommit(t.Context())
	require.NoError(t, err)
	require.NotNil(t, prepared.committedReplay)
	forged := *prepared.committedReplay
	prepared.committedReplay = &forged

	require.Error(t, prepared.SealPublication())
	prepared.Abort()
	accepted, found := component.AcceptedSnapshot(server.URL, snapshot.Descriptor)
	assert.False(t, found)
	assert.False(t, accepted.Found)
}

func TestPreparedInputCommitRejectsReplayFromAnotherCommit(t *testing.T) {
	prepare := func(t *testing.T, content string) (*Component, *PreparedInputCommit) {
		t.Helper()
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			_, _ = w.Write([]byte(content))
		}))
		t.Cleanup(server.Close)
		bus, logger := testutil.NewTestBusAndLogger()
		component := New(bus, logger, 0)
		wrapper := NewHTTPStoreWrapper(t.Context(), component, logger, nil, SourceModeAuthoritative)
		_, _, err := wrapper.FetchSnapshot(server.URL, map[string]any{"critical": true})
		require.NoError(t, err)
		prepared, err := wrapper.InputTransaction().PrepareCommit(t.Context())
		require.NoError(t, err)
		return component, prepared
	}

	component, prepared := prepare(t, "first")
	otherComponent, other := prepare(t, "second")
	require.NoError(t, other.committedReplay.ValidateAuthentication())
	prepared.committedReplay = other.committedReplay

	require.ErrorContains(t, prepared.ValidatePublication(), "does not match")
	require.ErrorContains(t, prepared.SealPublication(), "does not match")
	prepared.Abort()
	other.Abort()
	assert.Zero(t, component.store.Watermark())
	assert.Zero(t, otherComponent.store.Watermark())
}

func TestPreparedInputCommitRejectsReplayFromEarlierSameStoreCommit(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("accepted"))
	}))
	defer server.Close()
	bus, logger := testutil.NewTestBusAndLogger()
	component := New(bus, logger, 0)

	first := NewHTTPStoreWrapper(t.Context(), component, logger, nil, SourceModeAuthoritative)
	_, _, err := first.FetchSnapshot(server.URL, map[string]any{"critical": true})
	require.NoError(t, err)
	require.NoError(t, first.InputTransaction().Commit(t.Context()))
	oldReplay, ok := first.CommittedAcceptedReplayState()
	require.True(t, ok)
	require.NoError(t, oldReplay.ValidateAuthentication())

	second := NewHTTPStoreWrapper(t.Context(), component, logger, nil, SourceModeAuthoritative)
	_, _, err = second.FetchSnapshot(server.URL, map[string]any{"critical": true})
	require.NoError(t, err)
	prepared, err := second.InputTransaction().PrepareCommit(t.Context())
	require.NoError(t, err)
	require.NotSame(t, oldReplay, prepared.committedReplay)
	prepared.committedReplay = oldReplay

	require.ErrorContains(t, prepared.ValidatePublication(), "does not match")
	require.ErrorContains(t, prepared.SealPublication(), "does not match")
	prepared.Abort()
	assert.False(t, second.InputTransaction().Cacheable())
}

func TestPreparedInputCommitRejectsMutatedSnapshotPlan(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("candidate"))
	}))
	defer server.Close()
	bus, logger := testutil.NewTestBusAndLogger()
	component := New(bus, logger, 0)
	wrapper := NewHTTPStoreWrapper(t.Context(), component, logger, nil, SourceModeAuthoritative)
	_, _, err := wrapper.FetchSnapshot(server.URL, map[string]any{"critical": true})
	require.NoError(t, err)
	prepared, err := wrapper.InputTransaction().PrepareCommit(t.Context())
	require.NoError(t, err)
	require.Len(t, prepared.committedSnapshots, 1)
	prepared.committedSnapshots[0].Content = "mutated"

	require.ErrorContains(t, prepared.ValidatePublication(), "does not match")
	require.ErrorContains(t, prepared.SealPublication(), "does not match")
	prepared.Abort()
	assert.Zero(t, component.store.Watermark())
}

func TestPreparedInputCommitRejectsFlippedCacheability(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("candidate"))
	}))
	defer server.Close()
	bus, logger := testutil.NewTestBusAndLogger()
	component := New(bus, logger, 0)
	wrapper := NewHTTPStoreWrapper(t.Context(), component, logger, nil, SourceModeAuthoritative)
	_, _, err := wrapper.FetchSnapshot(server.URL, map[string]any{"critical": true})
	require.NoError(t, err)
	prepared, err := wrapper.InputTransaction().PrepareCommit(t.Context())
	require.NoError(t, err)
	require.True(t, prepared.cacheable)
	prepared.cacheable = false

	require.ErrorContains(t, prepared.ValidatePublication(), "does not match")
	require.ErrorContains(t, prepared.SealPublication(), "does not match")
	prepared.Abort()
	assert.Zero(t, component.store.Watermark())
}

func TestPreparedInputCommitPublishSealedRejectsTransactionPlanPoison(t *testing.T) {
	tests := map[string]func(*preparedInputTransactionPlan) func(){
		"snapshots": func(plan *preparedInputTransactionPlan) func() {
			original := plan.snapshots[0]
			plan.snapshots[0].Content = "poison"
			return func() { plan.snapshots[0] = original }
		},
		"replay": func(plan *preparedInputTransactionPlan) func() {
			original := plan.replay
			forged := *plan.replay
			plan.replay = &forged
			return func() { plan.replay = original }
		},
		"cacheable": func(plan *preparedInputTransactionPlan) func() {
			plan.cacheable = !plan.cacheable
			return func() { plan.cacheable = !plan.cacheable }
		},
	}
	for name, poison := range tests {
		t.Run(name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				_, _ = w.Write([]byte("candidate"))
			}))
			defer server.Close()
			bus, logger := testutil.NewTestBusAndLogger()
			component := New(bus, logger, 0)
			wrapper := NewHTTPStoreWrapper(t.Context(), component, logger, nil, SourceModeAuthoritative)
			_, _, err := wrapper.FetchSnapshot(server.URL, map[string]any{"critical": true})
			require.NoError(t, err)
			prepared, err := wrapper.InputTransaction().PrepareCommit(t.Context())
			require.NoError(t, err)
			require.NoError(t, prepared.SealPublication())
			restore := poison(prepared.transactionPlan)

			assert.Panics(t, prepared.PublishSealed)
			assert.Equal(t, preparedInputSealed, prepared.state)

			restore()
			require.NotPanics(t, prepared.PublishSealed)
			prepared.Release()
			assert.True(t, wrapper.InputTransaction().Cacheable())
		})
	}
}

func TestPreparedInputCommitReleaseRollsBackPostPublishPoison(t *testing.T) {
	tests := map[string]func(*PreparedInputCommit) func(){
		"transaction plan": func(prepared *PreparedInputCommit) func() {
			original := prepared.transactionPlan.snapshots[0]
			prepared.transactionPlan.snapshots[0].Content = "poison"
			return func() { prepared.transactionPlan.snapshots[0] = original }
		},
		"component release plan": func(prepared *PreparedInputCommit) func() {
			original := prepared.component.releasePlan.reconcile[0]
			prepared.component.releasePlan.reconcile[0].url = ""
			return func() { prepared.component.releasePlan.reconcile[0] = original }
		},
	}
	for name, poison := range tests {
		t.Run(name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				_, _ = w.Write([]byte("candidate"))
			}))
			defer server.Close()
			bus, logger := testutil.NewTestBusAndLogger()
			component := New(bus, logger, 0)
			wrapper := NewHTTPStoreWrapper(t.Context(), component, logger, nil, SourceModeAuthoritative)
			_, snapshot, err := wrapper.FetchSnapshot(server.URL, map[string]any{
				"critical": true, "delay": "1h",
			})
			require.NoError(t, err)
			prepared, err := wrapper.InputTransaction().PrepareCommit(t.Context())
			require.NoError(t, err)
			require.NoError(t, prepared.SealPublication())
			prepared.PublishSealed()
			restore := poison(prepared)

			assert.Panics(t, prepared.Release)
			restore()
			accepted, found := component.AcceptedSnapshot(server.URL, snapshot.Descriptor)
			assert.False(t, found)
			assert.False(t, accepted.Found)
			assert.False(t, wrapper.InputTransaction().Cacheable())
			assert.Nil(t, currentRefresher(component, server.URL))

			retry := NewHTTPStoreWrapper(t.Context(), component, logger, nil, SourceModeAuthoritative)
			_, _, err = retry.FetchSnapshot(server.URL, map[string]any{
				"critical": true, "delay": "1h",
			})
			require.NoError(t, err)
			require.NoError(t, retry.InputTransaction().Commit(t.Context()))
			assert.True(t, retry.InputTransaction().Cacheable())
		})
	}
}

func TestInputTransactionCommitReportsConcurrentAbort(t *testing.T) {
	bus, logger := testutil.NewTestBusAndLogger()
	transaction := newInputTransaction(New(bus, logger, 0))
	ctx := &commitAbortPauseContext{
		paused: make(chan struct{}),
		resume: make(chan struct{}),
	}
	committed := make(chan error, 1)
	go func() {
		committed <- transaction.Commit(ctx)
	}()
	<-ctx.paused
	transaction.Abort()
	close(ctx.resume)

	require.ErrorIs(t, <-committed, errInputTransactionAborted)
	assert.False(t, transaction.Cacheable())
}

func TestCommitInitialCandidatesConveniencePathAbortsAfterPublishPanic(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("candidate"))
	}))
	defer server.Close()
	bus, logger := testutil.NewTestBusAndLogger()
	component := New(bus, logger, 0)
	reconciled, err := component.store.ReconcileSource(
		server.URL, purehttpstore.FetchOptions{Critical: true}, nil,
	)
	require.NoError(t, err)
	_, candidate, err := component.store.PrepareInitialSnapshot(
		t.Context(), server.URL, reconciled.State,
	)
	require.NoError(t, err)
	require.NotNil(t, candidate)
	ctx := &componentPostPreparePoisonContext{
		Context: t.Context(),
		poison:  func() { component.prepareAuthority = nil },
	}

	assert.Panics(t, func() {
		_, _, _ = component.CommitInitialCandidatesAndVerifyObservations(
			ctx, []*purehttpstore.InitialCandidate{candidate}, nil,
		)
	})

	_, _, err = component.CommitInitialCandidatesAndVerifyObservations(
		t.Context(), []*purehttpstore.InitialCandidate{candidate}, nil,
	)
	require.NoError(t, err)
}

type componentPostPreparePoisonContext struct {
	context.Context
	calls  int
	poison func()
}

func (c *componentPostPreparePoisonContext) Err() error {
	c.calls++
	if c.calls == 2 {
		c.poison()
	}
	return nil
}

type commitAbortPauseContext struct {
	calls  atomic.Int32
	paused chan struct{}
	resume chan struct{}
}

func (*commitAbortPauseContext) Deadline() (time.Time, bool) { return time.Time{}, false }

func (*commitAbortPauseContext) Done() <-chan struct{} { return nil }

func (c *commitAbortPauseContext) Err() error {
	if c.calls.Add(1) == 2 {
		close(c.paused)
		<-c.resume
	}
	return nil
}

func (*commitAbortPauseContext) Value(any) any { return nil }

func TestPrepareCommitWithObservationsCoversGraphOnlyProofs(t *testing.T) {
	bus, logger := testutil.NewTestBusAndLogger()
	component := New(bus, logger, 0)
	component.store.LoadFixture("https://example.test/input", "old")
	old := component.store.AcceptedSnapshot("https://example.test/input", purehttpstore.SourceDescriptor{})
	require.True(t, old.Found)
	component.store.LoadFixture("https://example.test/input", "new")

	transaction := newInputTransaction(component)
	_, err := transaction.PrepareCommitWithObservations(
		t.Context(),
		[]purehttpstore.ObservationToken{old.ObservationToken()},
	)
	require.ErrorContains(t, err, "changed while the render was running")
}

func TestPrepareCommitWithObservationsRejectsOwnCandidatePublication(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("candidate"))
	}))
	defer server.Close()
	bus, logger := testutil.NewTestBusAndLogger()
	component := New(bus, logger, 0)
	options := purehttpstore.FetchOptions{Critical: true}
	descriptor, err := purehttpstore.DescribeSource(options, nil)
	require.NoError(t, err)
	negative := component.store.AcceptedSnapshot(server.URL, descriptor)
	wrapper := NewHTTPStoreWrapper(t.Context(), component, logger, nil, SourceModeAuthoritative)
	_, err = wrapper.Fetch(server.URL, map[string]any{"critical": true})
	require.NoError(t, err)

	_, err = wrapper.InputTransaction().PrepareCommitWithObservations(
		t.Context(),
		[]purehttpstore.ObservationToken{negative.ObservationToken()},
	)
	require.ErrorContains(t, err, "invalidates a render observation")
	wrapper.InputTransaction().Abort()
	assert.False(t, component.store.AcceptedSnapshot(server.URL, descriptor).Found)
	_, exists := component.store.GetSourceState(server.URL)
	assert.False(t, exists)
}

func TestPrepareCommitWithObservationsAllowsUnrelatedCandidatePublication(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("candidate"))
	}))
	defer server.Close()
	bus, logger := testutil.NewTestBusAndLogger()
	component := New(bus, logger, 0)
	negative := component.store.AcceptedSnapshot(
		"https://unrelated.example.test/input",
		purehttpstore.SourceDescriptor{},
	)
	wrapper := NewHTTPStoreWrapper(t.Context(), component, logger, nil, SourceModeAuthoritative)
	_, err := wrapper.Fetch(server.URL, map[string]any{"critical": true})
	require.NoError(t, err)

	prepared, err := wrapper.InputTransaction().PrepareCommitWithObservations(
		t.Context(),
		[]purehttpstore.ObservationToken{negative.ObservationToken()},
	)
	require.NoError(t, err)
	prepared.Publish()
	prepared.Release()
	assert.True(t, component.store.VerifyObservations(
		[]purehttpstore.ObservationToken{negative.ObservationToken()},
	))
}

func TestInputTransactionRebasesVerificationOnlyNegativeObservation(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	defer server.Close()
	bus, logger := testutil.NewTestBusAndLogger()
	component := New(bus, logger, 0)
	wrapper := NewHTTPStoreWrapper(t.Context(), component, logger, nil, SourceModeAuthoritative)
	_, before, err := wrapper.FetchSnapshot(server.URL)
	require.NoError(t, err)
	require.False(t, before.Cacheable)

	require.NoError(t, wrapper.InputTransaction().Commit(t.Context()))
	committed := wrapper.InputTransaction().Snapshots()
	require.Len(t, committed, 1)
	assert.Greater(t, committed[0].Watermark, before.Watermark)
	assert.True(t, component.VerifyObservations(
		[]purehttpstore.ObservationToken{committed[0].ObservationToken()},
	))
}

func TestPreparedInputCommitAcquireHonorsContext(t *testing.T) {
	bus, logger := testutil.NewTestBusAndLogger()
	component := New(bus, logger, 0)
	component.store.LoadFixture("https://example.test/input", "accepted")
	snapshot := component.store.AcceptedSnapshot("https://example.test/input", purehttpstore.SourceDescriptor{})

	first := newInputTransaction(component)
	prepared, err := first.PrepareCommitWithObservations(
		t.Context(),
		[]purehttpstore.ObservationToken{snapshot.ObservationToken()},
	)
	require.NoError(t, err)
	defer prepared.Abort()

	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	second := newInputTransaction(component)
	_, err = second.PrepareCommitWithObservations(
		ctx,
		[]purehttpstore.ObservationToken{snapshot.ObservationToken()},
	)
	require.ErrorIs(t, err, context.Canceled)
}

func TestPreparedInputCommitBlocksSourceAndTimerMutation(t *testing.T) {
	bus, logger := testutil.NewTestBusAndLogger()
	component := New(bus, logger, 0)
	defer component.stopAllRefreshers()
	url := "https://example.test/input"
	component.store.LoadFixture(url, "accepted")
	snapshot := component.store.AcceptedSnapshot(url, purehttpstore.SourceDescriptor{})

	transaction := newInputTransaction(component)
	prepared, err := transaction.PrepareCommitWithObservations(
		t.Context(),
		[]purehttpstore.ObservationToken{snapshot.ObservationToken()},
	)
	require.NoError(t, err)
	defer prepared.Release()

	mutated := make(chan error, 1)
	go func() {
		_, reconcileErr := component.ReconcileSource(
			url,
			purehttpstore.FetchOptions{Delay: time.Hour},
			nil,
		)
		mutated <- reconcileErr
	}()
	assert.Never(t, func() bool { return len(mutated) > 0 }, 25*time.Millisecond, time.Millisecond)

	prepared.Publish()
	assert.Never(t, func() bool { return len(mutated) > 0 }, 25*time.Millisecond, time.Millisecond)
	prepared.Release()
	select {
	case reconcileErr := <-mutated:
		require.NoError(t, reconcileErr)
	case <-time.After(time.Second):
		t.Fatal("HTTP source reconciliation remained blocked after release")
	}
	assert.NotNil(t, currentRefresher(component, url))
}

func TestInputRetrySeedReusesExactCandidateWithoutPublishingOnAbort(t *testing.T) {
	requests := atomic.Int32{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		requests.Add(1)
		_, _ = w.Write([]byte("candidate"))
	}))
	defer server.Close()

	bus, logger := testutil.NewTestBusAndLogger()
	component := New(bus, logger, 0)
	first := NewHTTPStoreWrapper(t.Context(), component, logger, nil, SourceModeAuthoritative)
	_, snapshot, err := first.FetchSnapshot(server.URL, map[string]any{"critical": true})
	require.NoError(t, err)
	seed := first.InputTransaction().RetrySeed()
	require.NotNil(t, seed)
	first.InputTransaction().Abort()
	_, found := component.AcceptedSnapshot(server.URL, snapshot.Descriptor)
	assert.False(t, found)

	retry := NewHTTPStoreWrapperWithRetrySeed(
		t.Context(), component, logger, nil, SourceModeAuthoritative, seed,
	)
	content, retried, err := retry.FetchSnapshot(server.URL, map[string]any{"critical": true})
	require.NoError(t, err)
	assert.Equal(t, "candidate", content)
	assert.Equal(t, snapshot.Token, retried.Token)
	assert.Equal(t, int32(1), requests.Load())
	_, found = component.AcceptedSnapshot(server.URL, snapshot.Descriptor)
	assert.False(t, found)
	require.NoError(t, retry.InputTransaction().Commit(t.Context()))
	accepted, found := component.AcceptedSnapshot(server.URL, snapshot.Descriptor)
	require.True(t, found)
	assert.Equal(t, "candidate", accepted.Content)
}

func TestInputRetrySeedPublishesOnlyInputsUsedByFinalAttempt(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(r.URL.Path))
	}))
	defer server.Close()

	bus, logger := testutil.NewTestBusAndLogger()
	component := New(bus, logger, 0)
	unusedURL := server.URL + "/unused"
	usedURL := server.URL + "/used"
	first := NewHTTPStoreWrapper(t.Context(), component, logger, nil, SourceModeAuthoritative)
	_, unused, err := first.FetchSnapshot(unusedURL, map[string]any{"critical": true})
	require.NoError(t, err)
	seed := first.InputTransaction().RetrySeed()
	require.NotNil(t, seed)
	first.InputTransaction().Abort()

	retry := NewHTTPStoreWrapperWithRetrySeed(
		t.Context(), component, logger, nil, SourceModeAuthoritative, seed,
	)
	_, used, err := retry.FetchSnapshot(usedURL, map[string]any{"critical": true})
	require.NoError(t, err)
	require.NoError(t, retry.InputTransaction().Commit(t.Context()))
	_, found := component.AcceptedSnapshot(unusedURL, unused.Descriptor)
	assert.False(t, found)
	accepted, found := component.AcceptedSnapshot(usedURL, used.Descriptor)
	require.True(t, found)
	assert.Equal(t, "/used", accepted.Content)
}

func TestInputRetrySeedRequiresExactSourceDescriptor(t *testing.T) {
	requests := atomic.Int32{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		_, _ = w.Write([]byte(r.Header.Get("Authorization")))
	}))
	defer server.Close()

	bus, logger := testutil.NewTestBusAndLogger()
	component := New(bus, logger, 0)
	first := NewHTTPStoreWrapper(t.Context(), component, logger, nil, SourceModeAuthoritative)
	_, err := first.Fetch(
		server.URL,
		map[string]any{"critical": true},
		map[string]any{"type": "bearer", "token": "first"},
	)
	require.NoError(t, err)
	seed := first.InputTransaction().RetrySeed()
	require.NotNil(t, seed)
	first.InputTransaction().Abort()

	retry := NewHTTPStoreWrapperWithRetrySeed(
		t.Context(), component, logger, nil, SourceModeAuthoritative, seed,
	)
	content, err := retry.Fetch(
		server.URL,
		map[string]any{"critical": true},
		map[string]any{"type": "bearer", "token": "second"},
	)
	require.NoError(t, err)
	assert.Equal(t, "Bearer second", content)
	assert.Equal(t, int32(2), requests.Load())
	require.NoError(t, retry.InputTransaction().Commit(t.Context()))
	entry := component.store.GetEntry(server.URL)
	require.NotNil(t, entry)
	assert.Equal(t, "second", entry.Auth.Token)
}

func TestInputTransactionRejectsAcceptedContentChangedDuringRender(t *testing.T) {
	var body atomic.Value
	body.Store("accepted")
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(body.Load().(string)))
	}))
	defer server.Close()

	bus, logger := testutil.NewTestBusAndLogger()
	component := New(bus, logger, 0)
	first := NewHTTPStoreWrapper(t.Context(), component, logger, nil, SourceModeAuthoritative)
	content, err := first.Fetch(server.URL, map[string]any{"critical": true})
	require.NoError(t, err)
	assert.Equal(t, "accepted", content)
	require.NoError(t, first.InputTransaction().Commit(t.Context()))

	render := NewHTTPStoreWrapper(t.Context(), component, logger, nil, SourceModeAuthoritative)
	content, err = render.Fetch(server.URL, map[string]any{"critical": true})
	require.NoError(t, err)
	assert.Equal(t, "accepted", content)
	assert.True(t, render.InputTransaction().Cacheable())

	body.Store("replacement")
	version, err := component.store.RefreshURLVersion(t.Context(), server.URL)
	require.NoError(t, err)
	require.NotNil(t, version)
	require.True(t, component.store.PromotePendingVersion(server.URL, version.Checksum, version.Revision))

	err = render.InputTransaction().Commit(t.Context())
	require.ErrorContains(t, err, "changed while the render was running")
	accepted, ok := component.store.Get(server.URL)
	require.True(t, ok)
	assert.Equal(t, "replacement", accepted)
}

func TestInputTransactionDistinguishesSuccessfulAndFailedEmptyBodies(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/failed" {
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer server.Close()

	bus, logger := testutil.NewTestBusAndLogger()
	component := New(bus, logger, 0)

	successful := NewHTTPStoreWrapper(t.Context(), component, logger, nil, SourceModeAuthoritative)
	content, successfulSnapshot, err := successful.FetchSnapshot(
		server.URL+"/empty",
		map[string]any{"critical": true},
	)
	require.NoError(t, err)
	assert.Empty(t, content)
	assert.Equal(t, server.URL+"/empty", successfulSnapshot.URL)
	assert.Equal(t, purehttpstore.SnapshotInitialCandidate, successfulSnapshot.Token.Kind())
	assert.True(t, successfulSnapshot.Found)
	assert.True(t, successfulSnapshot.Cacheable)
	assert.True(t, successful.InputTransaction().Cacheable())
	require.NoError(t, successful.InputTransaction().Commit(t.Context()))
	snapshots := successful.InputTransaction().Snapshots()
	require.Len(t, snapshots, 1)
	assert.Equal(t, purehttpstore.SnapshotAccepted, snapshots[0].Token.Kind())
	assert.True(t, component.VerifySnapshots([]purehttpstore.SnapshotToken{snapshots[0].Token}))

	failed := NewHTTPStoreWrapper(t.Context(), component, logger, nil, SourceModeAuthoritative)
	content, failedSnapshot, err := failed.FetchSnapshot(server.URL+"/failed", map[string]any{
		"retries": 1,
	})
	require.NoError(t, err)
	assert.Empty(t, content)
	assert.Equal(t, server.URL+"/failed", failedSnapshot.URL)
	assert.NotEqual(t, purehttpstore.SourceDescriptor{}, failedSnapshot.Descriptor)
	assert.False(t, failedSnapshot.Found)
	assert.False(t, failedSnapshot.Cacheable)
	assert.False(t, failedSnapshot.Token.Valid())
	assert.False(t, failed.InputTransaction().Cacheable())
}

func TestInputTransactionRejectsNegativeObservationThatBecamePresent(t *testing.T) {
	available := atomic.Bool{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		if !available.Load() {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		_, _ = w.Write([]byte("present"))
	}))
	defer server.Close()

	bus, logger := testutil.NewTestBusAndLogger()
	component := New(bus, logger, 0)
	options := map[string]any{"retries": 1}
	negative := NewHTTPStoreWrapper(t.Context(), component, logger, nil, SourceModeAuthoritative)
	content, snapshot, err := negative.FetchSnapshot(server.URL, options)
	require.NoError(t, err)
	assert.Empty(t, content)
	assert.False(t, snapshot.Found)
	observation := snapshot.ObservationToken()
	require.True(t, observation.Valid())

	available.Store(true)
	present := NewHTTPStoreWrapper(t.Context(), component, logger, nil, SourceModeAuthoritative)
	content, err = present.Fetch(server.URL, options)
	require.NoError(t, err)
	assert.Equal(t, "present", content)
	require.NoError(t, present.InputTransaction().Commit(t.Context()))

	err = negative.InputTransaction().Commit(t.Context())
	require.ErrorContains(t, err, "changed while the render was running")
}

func TestAbortedInputTransactionIsNotCacheable(t *testing.T) {
	bus, logger := testutil.NewTestBusAndLogger()
	transaction := newInputTransaction(New(bus, logger, 0))
	transaction.Abort()
	assert.False(t, transaction.Cacheable())
}

func TestFetchSnapshotReturnsExactFrozenAcceptedVersion(t *testing.T) {
	requests := atomic.Int32{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		requests.Add(1)
		_, _ = w.Write([]byte("accepted"))
	}))
	defer server.Close()

	bus, logger := testutil.NewTestBusAndLogger()
	component := New(bus, logger, 0)
	authoritative := NewHTTPStoreWrapper(t.Context(), component, logger, nil, SourceModeAuthoritative)
	content, err := authoritative.Fetch(server.URL, map[string]any{"critical": true})
	require.NoError(t, err)
	assert.Equal(t, "accepted", content)
	require.NoError(t, authoritative.InputTransaction().Commit(t.Context()))

	readOnly := NewHTTPStoreWrapper(t.Context(), component, logger, nil, SourceModeReadOnly)
	content, snapshot, err := readOnly.FetchSnapshot(server.URL, map[string]any{"critical": true})
	require.NoError(t, err)
	assert.Equal(t, "accepted", content)
	assert.Equal(t, server.URL, snapshot.URL)
	assert.True(t, snapshot.Found)
	assert.True(t, snapshot.Cacheable)
	assert.Equal(t, purehttpstore.SnapshotAccepted, snapshot.Token.Kind())
	assert.True(t, component.VerifySnapshots([]purehttpstore.SnapshotToken{snapshot.Token}))
	assert.Equal(t, component.RevisionSource(), snapshot.Token.Source())
	current, changes, complete := component.ChangesSince(snapshot.Watermark)
	assert.True(t, complete)
	assert.Equal(t, component.Watermark(), current)
	assert.Empty(t, changes)
	assert.Equal(t, int32(1), requests.Load())
}

func TestReadOnlyFailedFetchIsFrozenForTheRender(t *testing.T) {
	requests := atomic.Int32{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		if requests.Add(1) <= 2 {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		_, _ = w.Write([]byte("later"))
	}))
	defer server.Close()

	bus, logger := testutil.NewTestBusAndLogger()
	component := New(bus, logger, 0)
	readOnly := NewHTTPStoreWrapper(t.Context(), component, logger, nil, SourceModeReadOnly)
	options := map[string]any{"retries": 1}
	firstContent, first, err := readOnly.FetchSnapshot(server.URL, options)
	require.NoError(t, err)
	secondContent, second, err := readOnly.FetchSnapshot(server.URL, options)
	require.NoError(t, err)

	assert.Empty(t, firstContent)
	assert.Empty(t, secondContent)
	assert.Equal(t, first, second)
	assert.Equal(t, server.URL, first.URL)
	assert.False(t, first.Found)
	assert.False(t, first.Cacheable)
	assert.Equal(t, int32(2), requests.Load())
	snapshots, cacheable := readOnly.ContentSnapshots()
	require.Len(t, snapshots, 1)
	assert.Equal(t, first, snapshots[0])
	assert.False(t, cacheable)
}

func TestReplaySnapshotDefersTimerUntilCommitWithoutFetching(t *testing.T) {
	requests := atomic.Int32{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		requests.Add(1)
		_, _ = w.Write([]byte("accepted"))
	}))
	defer server.Close()

	bus, logger := testutil.NewTestBusAndLogger()
	component := New(bus, logger, 0)
	defer component.stopAllRefreshers()
	options := map[string]any{"interval": "1h", "critical": true}
	first := NewHTTPStoreWrapper(t.Context(), component, logger, nil, SourceModeAuthoritative)
	_, err := first.Fetch(server.URL, options)
	require.NoError(t, err)
	require.NoError(t, first.InputTransaction().Commit(t.Context()))
	stored := first.InputTransaction().Snapshots()
	require.Len(t, stored, 1)
	component.StopRefresher(server.URL)
	assert.Nil(t, currentRefresher(component, server.URL))

	replay := NewHTTPStoreWrapper(t.Context(), component, logger, nil, SourceModeAuthoritative)
	current, ok, err := replay.ReplaySnapshot(&stored[0])
	require.NoError(t, err)
	require.True(t, ok)
	assert.Equal(t, stored[0].Token, current.Token)
	assert.Equal(t, int32(1), requests.Load())
	assert.Nil(t, currentRefresher(component, server.URL))
	assert.True(t, replay.InputTransaction().Cacheable())
	require.NoError(t, replay.InputTransaction().Commit(t.Context()))
	assert.NotNil(t, currentRefresher(component, server.URL))
	committed := replay.InputTransaction().Snapshots()
	require.Len(t, committed, 1)
	assert.Equal(t, current.Token, committed[0].Token)
}

func TestReplaySnapshotEnrollsAcceptedTokenInEndCAS(t *testing.T) {
	var body atomic.Value
	body.Store("accepted")
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(body.Load().(string)))
	}))
	defer server.Close()

	bus, logger := testutil.NewTestBusAndLogger()
	component := New(bus, logger, 0)
	first := NewHTTPStoreWrapper(t.Context(), component, logger, nil, SourceModeAuthoritative)
	_, err := first.Fetch(server.URL, map[string]any{"critical": true})
	require.NoError(t, err)
	require.NoError(t, first.InputTransaction().Commit(t.Context()))
	stored := first.InputTransaction().Snapshots()
	require.Len(t, stored, 1)

	replay := NewHTTPStoreWrapper(t.Context(), component, logger, nil, SourceModeAuthoritative)
	_, ok, err := replay.ReplaySnapshot(&stored[0])
	require.NoError(t, err)
	require.True(t, ok)
	body.Store("replacement")
	version, err := component.store.RefreshURLVersion(t.Context(), server.URL)
	require.NoError(t, err)
	require.NotNil(t, version)
	require.True(t, component.store.PromotePendingVersion(server.URL, version.Checksum, version.Revision))

	err = replay.InputTransaction().Commit(t.Context())
	require.ErrorContains(t, err, "changed while the render was running")
}

func TestReplaySnapshotUsesFrozenReadOnlyOverlay(t *testing.T) {
	var body atomic.Value
	body.Store("accepted")
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(body.Load().(string)))
	}))
	defer server.Close()

	bus, logger := testutil.NewTestBusAndLogger()
	component := New(bus, logger, 0)
	first := NewHTTPStoreWrapper(t.Context(), component, logger, nil, SourceModeAuthoritative)
	_, err := first.Fetch(server.URL, map[string]any{"critical": true})
	require.NoError(t, err)
	require.NoError(t, first.InputTransaction().Commit(t.Context()))
	stored := first.InputTransaction().Snapshots()
	require.Len(t, stored, 1)

	readOnly := NewHTTPStoreWrapper(t.Context(), component, logger, nil, SourceModeReadOnly)
	body.Store("replacement")
	version, err := component.store.RefreshURLVersion(t.Context(), server.URL)
	require.NoError(t, err)
	require.NotNil(t, version)
	require.True(t, component.store.PromotePendingVersion(server.URL, version.Checksum, version.Revision))

	current, replayed, err := readOnly.ReplaySnapshot(&stored[0])
	require.NoError(t, err)
	require.True(t, replayed)
	assert.Equal(t, stored[0], current)
	live, found := component.AcceptedSnapshot(stored[0].URL, stored[0].Descriptor)
	require.True(t, found)
	assert.Equal(t, "replacement", live.Content)
}

func TestComponentAcceptedSnapshotReadsExactCurrentInputWithoutAuthorityChange(t *testing.T) {
	requests := atomic.Int32{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		requests.Add(1)
		_, _ = w.Write([]byte("accepted"))
	}))
	defer server.Close()

	bus, logger := testutil.NewTestBusAndLogger()
	component := New(bus, logger, 0)
	first := NewHTTPStoreWrapper(t.Context(), component, logger, nil, SourceModeAuthoritative)
	_, err := first.Fetch(server.URL, map[string]any{"critical": true})
	require.NoError(t, err)
	require.NoError(t, first.InputTransaction().Commit(t.Context()))
	stored := first.InputTransaction().Snapshots()
	require.Len(t, stored, 1)
	before, exists := component.store.GetSourceState(server.URL)
	require.True(t, exists)

	current, found := component.AcceptedSnapshot(stored[0].URL, stored[0].Descriptor)
	require.True(t, found)
	assert.Equal(t, stored[0].Token, current.Token)
	assert.Equal(t, "accepted", current.Content)
	other, err := purehttpstore.DescribeSource(purehttpstore.FetchOptions{}, nil)
	require.NoError(t, err)
	_, found = component.AcceptedSnapshot(server.URL, other)
	assert.False(t, found)
	after, exists := component.store.GetSourceState(server.URL)
	require.True(t, exists)
	assert.Equal(t, before, after)
	assert.Equal(t, int32(1), requests.Load())
}
