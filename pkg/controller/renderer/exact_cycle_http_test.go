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

package renderer

import (
	"log/slog"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/require"

	controllerhttpstore "gitlab.com/haproxy-haptic/haptic/pkg/controller/httpstore"
	"gitlab.com/haproxy-haptic/haptic/pkg/controller/testutil"
	"gitlab.com/haproxy-haptic/haptic/pkg/httpstore"
)

func TestExactCycleHTTPObservationReplaysOnlyExactAcceptedState(t *testing.T) {
	var body atomic.Value
	body.Store("A")
	requests := atomic.Uint64{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		requests.Add(1)
		if request.URL.Path == "/unrelated" {
			_, _ = w.Write([]byte("unrelated"))
			return
		}
		_, _ = w.Write([]byte(body.Load().(string)))
	}))
	defer server.Close()
	bus, logger := testutil.NewTestBusAndLogger()
	component := controllerhttpstore.New(bus, logger, 0)
	first := controllerhttpstore.NewHTTPStoreWrapper(
		t.Context(), component, logger, nil, controllerhttpstore.SourceModeAuthoritative,
	)
	_, err := first.Fetch(server.URL, map[string]any{"critical": true})
	require.NoError(t, err)
	require.NoError(t, first.InputTransaction().Commit(t.Context()))
	observations, err := captureExactCycleHTTPObservations(first, component)
	require.NoError(t, err)
	require.NotNil(t, observations)
	require.Len(t, observations.state.Snapshots(), 1)

	replay := controllerhttpstore.NewHTTPStoreWrapper(
		t.Context(), component, logger, nil, controllerhttpstore.SourceModeAuthoritative,
	)
	matched, err := observations.matches(t.Context(), replay, component, false)
	require.NoError(t, err)
	require.True(t, matched)
	require.NoError(t, replay.InputTransaction().Commit(t.Context()))
	require.Equal(t, uint64(1), requests.Load())

	unrelated := controllerhttpstore.NewHTTPStoreWrapper(
		t.Context(), component, logger, nil, controllerhttpstore.SourceModeAuthoritative,
	)
	_, err = unrelated.Fetch(server.URL+"/unrelated", map[string]any{"critical": true})
	require.NoError(t, err)
	require.NoError(t, unrelated.InputTransaction().Commit(t.Context()))
	requireExactCycleHTTPReplayMatch(t, observations, component, logger, false, true)
	requireExactCycleHTTPReplayMatch(t, observations, component, logger, true, true)

	body.Store("B")
	version, err := component.GetStore().RefreshURLVersion(t.Context(), server.URL)
	require.NoError(t, err)
	require.NotNil(t, version)
	requireExactCycleHTTPReplayMatch(t, observations, component, logger, false, false)
	require.True(t, component.GetStore().PromotePendingVersion(server.URL, version.Checksum, version.Revision))
	body.Store("A")
	version, err = component.GetStore().RefreshURLVersion(t.Context(), server.URL)
	require.NoError(t, err)
	require.NotNil(t, version)
	require.True(t, component.GetStore().PromotePendingVersion(server.URL, version.Checksum, version.Revision))
	requireExactCycleHTTPReplayMatch(t, observations, component, logger, false, false)
}

func requireExactCycleHTTPReplayMatch(
	t *testing.T,
	observations *exactCycleHTTPObservations,
	component *controllerhttpstore.Component,
	logger *slog.Logger,
	requiresRoots bool,
	want bool,
) {
	t.Helper()
	replay := controllerhttpstore.NewHTTPStoreWrapper(
		t.Context(), component, logger, nil, controllerhttpstore.SourceModeAuthoritative,
	)
	defer replay.InputTransaction().Abort()
	matched, err := observations.matches(t.Context(), replay, component, requiresRoots)
	require.NoError(t, err)
	require.Equal(t, want, matched)
}

func TestExactCycleHTTPObservationRejectsUncacheableNegativeRead(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	defer server.Close()
	bus, logger := testutil.NewTestBusAndLogger()
	component := controllerhttpstore.New(bus, logger, 0)
	wrapper := controllerhttpstore.NewHTTPStoreWrapper(
		t.Context(), component, logger, nil, controllerhttpstore.SourceModeAuthoritative,
	)
	_, _, err := wrapper.FetchSnapshot(server.URL, map[string]any{"retries": 1})
	require.NoError(t, err)
	require.NoError(t, wrapper.InputTransaction().Commit(t.Context()))
	observations, err := captureExactCycleHTTPObservations(wrapper, component)
	require.NoError(t, err)
	require.Nil(t, observations)

	descriptor, err := httpstore.DescribeSource(httpstore.FetchOptions{Retries: 1}, nil)
	require.NoError(t, err)
	_, found := component.AcceptedSnapshot(server.URL, descriptor)
	require.False(t, found)
}

func TestExactCycleHTTPObservationIgnoresUnobservedPendingState(t *testing.T) {
	var mainBody atomic.Value
	mainBody.Store("main")
	var unrelatedBody atomic.Value
	unrelatedBody.Store("A")
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		if request.URL.Path == "/unrelated" {
			_, _ = w.Write([]byte(unrelatedBody.Load().(string)))
			return
		}
		_, _ = w.Write([]byte(mainBody.Load().(string)))
	}))
	defer server.Close()
	bus, logger := testutil.NewTestBusAndLogger()
	component := controllerhttpstore.New(bus, logger, 0)

	unrelated := controllerhttpstore.NewHTTPStoreWrapper(
		t.Context(), component, logger, nil, controllerhttpstore.SourceModeAuthoritative,
	)
	_, err := unrelated.Fetch(server.URL+"/unrelated", map[string]any{"critical": true})
	require.NoError(t, err)
	require.NoError(t, unrelated.InputTransaction().Commit(t.Context()))

	first := controllerhttpstore.NewHTTPStoreWrapper(
		t.Context(), component, logger, nil, controllerhttpstore.SourceModeAuthoritative,
	)
	_, err = first.Fetch(server.URL, map[string]any{"critical": true})
	require.NoError(t, err)
	require.NoError(t, first.InputTransaction().Commit(t.Context()))
	observations, err := captureExactCycleHTTPObservations(first, component)
	require.NoError(t, err)
	require.NotNil(t, observations)

	unrelatedBody.Store("B")
	version, err := component.GetStore().RefreshURLVersion(t.Context(), server.URL+"/unrelated")
	require.NoError(t, err)
	require.NotNil(t, version)

	selective := controllerhttpstore.NewHTTPStoreWrapper(
		t.Context(), component, logger, nil, controllerhttpstore.SourceModeAuthoritative,
	)
	matched, err := observations.matches(t.Context(), selective, component, false)
	require.NoError(t, err)
	require.True(t, matched)
	selective.InputTransaction().Abort()

	legacyShared := controllerhttpstore.NewHTTPStoreWrapper(
		t.Context(), component, logger, nil, controllerhttpstore.SourceModeAuthoritative,
	)
	matched, err = observations.matches(t.Context(), legacyShared, component, true)
	require.NoError(t, err)
	require.True(t, matched)
	legacyShared.InputTransaction().Abort()
}

func TestExactCycleHTTPObservationCommitFenceRejectsMutationAfterMatch(t *testing.T) {
	var body atomic.Value
	body.Store("A")
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(body.Load().(string)))
	}))
	defer server.Close()
	bus, logger := testutil.NewTestBusAndLogger()
	component := controllerhttpstore.New(bus, logger, 0)
	first := controllerhttpstore.NewHTTPStoreWrapper(
		t.Context(), component, logger, nil, controllerhttpstore.SourceModeAuthoritative,
	)
	_, err := first.Fetch(server.URL, map[string]any{"critical": true})
	require.NoError(t, err)
	require.NoError(t, first.InputTransaction().Commit(t.Context()))
	observations, err := captureExactCycleHTTPObservations(first, component)
	require.NoError(t, err)
	require.NotNil(t, observations)

	replay := controllerhttpstore.NewHTTPStoreWrapper(
		t.Context(), component, logger, nil, controllerhttpstore.SourceModeAuthoritative,
	)
	matched, err := observations.matches(t.Context(), replay, component, true)
	require.NoError(t, err)
	require.True(t, matched)

	body.Store("B")
	version, err := component.GetStore().RefreshURLVersion(t.Context(), server.URL)
	require.NoError(t, err)
	require.NotNil(t, version)
	require.Error(t, replay.InputTransaction().Commit(t.Context()))
}
