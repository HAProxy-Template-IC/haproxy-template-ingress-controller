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
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"gitlab.com/haproxy-haptic/haptic/pkg/controller/testutil"
	purehttpstore "gitlab.com/haproxy-haptic/haptic/pkg/httpstore"
)

func TestWrapperRejectsConflictingDeclarationsForOneURL(t *testing.T) {
	requests := atomic.Int32{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		_, _ = w.Write([]byte(r.Header.Get("Authorization")))
	}))
	defer server.Close()

	bus, logger := testutil.NewTestBusAndLogger()
	component := New(bus, logger, 0)
	wrapper := NewHTTPStoreWrapper(t.Context(), component, logger, nil, SourceModeAuthoritative)

	content, err := wrapper.Fetch(server.URL, nil, map[string]any{
		"type": "bearer", "token": "first",
	})
	require.NoError(t, err)
	assert.Equal(t, "Bearer first", content)

	_, err = wrapper.Fetch(server.URL, nil, map[string]any{
		"type": "bearer", "token": "second",
	})
	require.ErrorContains(t, err, "conflicting authentication or options")
	assert.Equal(t, int32(1), requests.Load())
}

func TestAuthoritativeFetchMemoizesSourceWithinRender(t *testing.T) {
	requests := atomic.Int32{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = fmt.Fprintf(w, "candidate-%d", requests.Add(1))
	}))
	defer server.Close()

	bus, logger := testutil.NewTestBusAndLogger()
	component := New(bus, logger, 0)
	wrapper := NewHTTPStoreWrapper(t.Context(), component, logger, nil, SourceModeAuthoritative)

	type fetchResult struct {
		content any
		err     error
	}
	start := make(chan struct{})
	results := make(chan fetchResult, 2)
	for range 2 {
		go func() {
			<-start
			content, err := wrapper.Fetch(server.URL, map[string]any{"critical": true})
			results <- fetchResult{content: content, err: err}
		}()
	}
	close(start)

	for range 2 {
		result := <-results
		require.NoError(t, result.err)
		assert.Equal(t, "candidate-1", result.content)
	}
	assert.Equal(t, int32(1), requests.Load())
	commitInputTransaction(t, wrapper)
}

func TestAuthoritativeFetchRejectsSameIdentityFromNewGenerationWithinRender(t *testing.T) {
	requests := atomic.Int32{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = fmt.Fprintf(w, "candidate-%d", requests.Add(1))
	}))
	defer server.Close()

	bus, logger := testutil.NewTestBusAndLogger()
	component := New(bus, logger, 0)
	wrapper := NewHTTPStoreWrapper(t.Context(), component, logger, nil, SourceModeAuthoritative)

	content, err := wrapper.Fetch(server.URL, map[string]any{"critical": true})
	require.NoError(t, err)
	assert.Equal(t, "candidate-1", content)
	_, err = component.ReconcileSource(server.URL, purehttpstore.FetchOptions{Critical: true}, &purehttpstore.AuthConfig{
		Type:  purehttpstore.AuthTypeBearer,
		Token: "replacement",
	})
	require.NoError(t, err)
	_, err = component.ReconcileSource(server.URL, purehttpstore.FetchOptions{Critical: true}, nil)
	require.NoError(t, err)

	_, err = wrapper.Fetch(server.URL, map[string]any{"critical": true})
	require.ErrorContains(t, err, "changed within one render")
	wrapper.InputTransaction().Abort()
	_, accepted := component.store.Get(server.URL)
	assert.False(t, accepted)
	assert.Equal(t, int32(1), requests.Load())
}

func TestAuthoritativeFetchPinsAcceptedSourceGenerationWithinRender(t *testing.T) {
	for _, replacementAccepted := range []bool{false, true} {
		name := "accepted_to_candidate"
		if replacementAccepted {
			name = "accepted_to_accepted"
		}
		t.Run(name, func(t *testing.T) {
			requests := atomic.Int32{}
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				_, _ = fmt.Fprintf(w, "candidate-%d", requests.Add(1))
			}))
			defer server.Close()

			bus, logger := testutil.NewTestBusAndLogger()
			component := New(bus, logger, 0)
			options := purehttpstore.FetchOptions{Critical: true}
			_, err := component.store.Fetch(t.Context(), server.URL, options, nil)
			require.NoError(t, err)

			wrapper := NewHTTPStoreWrapper(t.Context(), component, logger, nil, SourceModeAuthoritative)
			content, err := wrapper.Fetch(server.URL, map[string]any{"critical": true})
			require.NoError(t, err)
			assert.Equal(t, "candidate-1", content)

			_, err = component.ReconcileSource(server.URL, options, &purehttpstore.AuthConfig{
				Type:  purehttpstore.AuthTypeBearer,
				Token: "replacement",
			})
			require.NoError(t, err)
			_, err = component.ReconcileSource(server.URL, options, nil)
			require.NoError(t, err)
			if replacementAccepted {
				replacement := NewHTTPStoreWrapper(t.Context(), component, logger, nil, SourceModeAuthoritative)
				content, err = replacement.Fetch(server.URL, map[string]any{"critical": true})
				require.NoError(t, err)
				assert.Equal(t, "candidate-2", content)
				commitInputTransaction(t, replacement)
			}

			_, err = wrapper.Fetch(server.URL, map[string]any{"critical": true})
			require.ErrorContains(t, err, "changed within one render")
			wrapper.InputTransaction().Abort()
			wantRequests := int32(1)
			if replacementAccepted {
				wantRequests = 2
			}
			assert.Equal(t, wantRequests, requests.Load())
		})
	}
}

func TestAuthoritativeSourceReplacementRearmsAndStopsRefreshTimer(t *testing.T) {
	requests := atomic.Int32{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		switch r.Header.Get("Authorization") {
		case "Bearer first":
			_, _ = w.Write([]byte("first-content"))
		case "Bearer second":
			_, _ = w.Write([]byte("second-content"))
		default:
			w.WriteHeader(http.StatusUnauthorized)
		}
	}))
	defer server.Close()

	bus, logger := testutil.NewTestBusAndLogger()
	component := New(bus, logger, 0)
	defer component.stopAllRefreshers()

	first := NewHTTPStoreWrapper(t.Context(), component, logger, nil, SourceModeAuthoritative)
	content, err := first.Fetch(
		server.URL,
		map[string]any{"interval": "1h", "critical": true},
		map[string]any{"type": "bearer", "token": "first"},
	)
	require.NoError(t, err)
	assert.Equal(t, "first-content", content)
	commitInputTransaction(t, first)
	firstState, exists := component.store.GetSourceState(server.URL)
	require.True(t, exists)
	firstTimer := currentRefresher(component, server.URL)
	require.NotNil(t, firstTimer)

	second := NewHTTPStoreWrapper(t.Context(), component, logger, nil, SourceModeAuthoritative)
	content, err = second.Fetch(
		server.URL,
		map[string]any{"interval": "2h", "critical": true},
		map[string]any{"type": "bearer", "token": "second"},
	)
	require.NoError(t, err)
	assert.Equal(t, "second-content", content)
	commitInputTransaction(t, second)
	secondState, exists := component.store.GetSourceState(server.URL)
	require.True(t, exists)
	secondTimer := currentRefresher(component, server.URL)
	require.NotNil(t, secondTimer)
	assert.NotSame(t, firstTimer, secondTimer)
	assert.False(t, firstTimer.Stop())
	assert.NotEqual(t, firstState.Identity, secondState.Identity)
	assert.Greater(t, secondState.Generation, firstState.Generation)
	assert.Equal(t, 2*time.Hour, component.store.GetDelay(server.URL))

	third := NewHTTPStoreWrapper(t.Context(), component, logger, nil, SourceModeAuthoritative)
	content, err = third.Fetch(
		server.URL,
		map[string]any{"critical": true},
		map[string]any{"type": "bearer", "token": "second"},
	)
	require.NoError(t, err)
	assert.Equal(t, "second-content", content)
	commitInputTransaction(t, third)
	thirdState, exists := component.store.GetSourceState(server.URL)
	require.True(t, exists)
	assert.Greater(t, thirdState.Generation, secondState.Generation)
	assert.Nil(t, currentRefresher(component, server.URL))
	assert.False(t, secondTimer.Stop())
	assert.Zero(t, component.store.GetDelay(server.URL))
	assert.Equal(t, int32(3), requests.Load())
}

func TestReadOnlySourceChangeLeavesAuthoritativeSourceAndTimerUntouched(t *testing.T) {
	requests := atomic.Int32{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		switch r.Header.Get("Authorization") {
		case "Bearer accepted":
			_, _ = w.Write([]byte("accepted-content"))
		case "Bearer candidate":
			_, _ = w.Write([]byte("candidate-content"))
		default:
			w.WriteHeader(http.StatusUnauthorized)
		}
	}))
	defer server.Close()

	bus, logger := testutil.NewTestBusAndLogger()
	component := New(bus, logger, 0)
	defer component.stopAllRefreshers()

	authoritative := NewHTTPStoreWrapper(t.Context(), component, logger, nil, SourceModeAuthoritative)
	content, err := authoritative.Fetch(
		server.URL,
		map[string]any{"interval": "1h", "critical": true},
		map[string]any{"type": "bearer", "token": "accepted"},
	)
	require.NoError(t, err)
	assert.Equal(t, "accepted-content", content)
	commitInputTransaction(t, authoritative)

	stateBefore, exists := component.store.GetSourceState(server.URL)
	require.True(t, exists)
	entryBefore := component.store.GetEntry(server.URL)
	timerBefore, timerGenerationBefore, timerSourceGenerationBefore := refresherState(component, server.URL)
	require.NotNil(t, timerBefore)

	readOnly := NewHTTPStoreWrapper(t.Context(), component, logger, nil, SourceModeReadOnly)
	content, err = readOnly.Fetch(
		server.URL,
		map[string]any{"interval": "2h", "critical": true},
		map[string]any{"type": "bearer", "token": "candidate"},
	)
	require.NoError(t, err)
	assert.Equal(t, "candidate-content", content)

	stateAfter, exists := component.store.GetSourceState(server.URL)
	require.True(t, exists)
	entryAfter := component.store.GetEntry(server.URL)
	timerAfter, timerGenerationAfter, timerSourceGenerationAfter := refresherState(component, server.URL)
	assert.Equal(t, stateBefore, stateAfter)
	assert.Equal(t, entryBefore, entryAfter)
	assert.Same(t, timerBefore, timerAfter)
	assert.Equal(t, timerGenerationBefore, timerGenerationAfter)
	assert.Equal(t, timerSourceGenerationBefore, timerSourceGenerationAfter)
	assert.Equal(t, int32(2), requests.Load())
}

func TestReadOnlyColdFetchAcceptsEmptyBodyWithoutSharedState(t *testing.T) {
	requests := atomic.Int32{}
	server := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		requests.Add(1)
	}))
	defer server.Close()

	bus, logger := testutil.NewTestBusAndLogger()
	component := New(bus, logger, 0)
	readOnly := NewHTTPStoreWrapper(t.Context(), component, logger, nil, SourceModeReadOnly)

	content, err := readOnly.Fetch(server.URL, map[string]any{"interval": "1h", "critical": true})
	require.NoError(t, err)
	assert.Equal(t, "", content)
	assert.Nil(t, component.store.GetEntry(server.URL))
	assert.Nil(t, currentRefresher(component, server.URL))
	assert.Equal(t, int32(1), requests.Load())
}

func TestReadOnlyPendingFetchUsesOverlayWithoutChangingOwnedVersion(t *testing.T) {
	requests := atomic.Int32{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		if requests.Add(1) == 1 {
			_, _ = w.Write([]byte("accepted"))
			return
		}
		_, _ = w.Write([]byte("pending"))
	}))
	defer server.Close()

	bus, logger := testutil.NewTestBusAndLogger()
	component := New(bus, logger, 0)
	defer component.stopAllRefreshers()
	options := map[string]any{"interval": "1h", "critical": true}
	auth := map[string]any{"type": "bearer", "token": "owned"}

	authoritative := NewHTTPStoreWrapper(t.Context(), component, logger, nil, SourceModeAuthoritative)
	_, err := authoritative.Fetch(server.URL, options, auth)
	require.NoError(t, err)
	commitInputTransaction(t, authoritative)
	changed, err := component.store.RefreshURL(t.Context(), server.URL)
	require.NoError(t, err)
	require.True(t, changed)

	overlay := purehttpstore.NewHTTPOverlay(component.store)
	stateBefore, exists := component.store.GetSourceState(server.URL)
	require.True(t, exists)
	entryBefore := component.store.GetEntry(server.URL)
	timerBefore := currentRefresher(component, server.URL)

	readOnly := NewHTTPStoreWrapper(t.Context(), component, logger, overlay, SourceModeReadOnly)
	content, err := readOnly.Fetch(server.URL, options, auth)
	require.NoError(t, err)
	assert.Equal(t, "pending", content)

	stateAfter, exists := component.store.GetSourceState(server.URL)
	require.True(t, exists)
	assert.Equal(t, stateBefore, stateAfter)
	assert.Equal(t, entryBefore, component.store.GetEntry(server.URL))
	assert.Same(t, timerBefore, currentRefresher(component, server.URL))
	assert.Equal(t, int32(2), requests.Load())
}

func TestConcurrentReadOnlyFetchCannotRetireAuthoritativeRefresh(t *testing.T) {
	refreshStarted := make(chan struct{})
	releaseRefresh := make(chan struct{})
	var releaseOnce sync.Once
	defer releaseOnce.Do(func() { close(releaseRefresh) })
	acceptedRequests := atomic.Int32{}
	candidateRequests := atomic.Int32{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Header.Get("Authorization") {
		case "Bearer accepted":
			if acceptedRequests.Add(1) == 1 {
				_, _ = w.Write([]byte("accepted-content"))
				return
			}
			close(refreshStarted)
			<-releaseRefresh
			_, _ = w.Write([]byte("refreshed-content"))
		case "Bearer candidate":
			candidateRequests.Add(1)
			_, _ = w.Write([]byte("candidate-content"))
		default:
			w.WriteHeader(http.StatusUnauthorized)
		}
	}))
	defer server.Close()

	bus, logger := testutil.NewTestBusAndLogger()
	component := New(bus, logger, 0)
	defer component.stopAllRefreshers()
	options := map[string]any{"interval": "1h", "critical": true}
	acceptedAuth := map[string]any{"type": "bearer", "token": "accepted"}

	authoritative := NewHTTPStoreWrapper(t.Context(), component, logger, nil, SourceModeAuthoritative)
	fetchAndCommitInputTransaction(t, authoritative, server.URL, options, acceptedAuth)
	stateBefore, exists := component.store.GetSourceState(server.URL)
	require.True(t, exists)
	timerBefore := currentRefresher(component, server.URL)

	type refreshResult struct {
		version *purehttpstore.PendingVersion
		err     error
	}
	result := make(chan refreshResult, 1)
	go func() {
		version, refreshErr := component.store.RefreshURLVersionForGeneration(
			t.Context(),
			server.URL,
			stateBefore.Generation,
		)
		result <- refreshResult{version: version, err: refreshErr}
	}()
	select {
	case <-refreshStarted:
	case <-time.After(testutil.LongTimeout):
		t.Fatal("authoritative refresh did not start")
	}

	readOnly := NewHTTPStoreWrapper(t.Context(), component, logger, nil, SourceModeReadOnly)
	content, err := readOnly.Fetch(
		server.URL,
		map[string]any{"interval": "2h", "critical": true},
		map[string]any{"type": "bearer", "token": "candidate"},
	)
	require.NoError(t, err)
	assert.Equal(t, "candidate-content", content)
	stateDuring, exists := component.store.GetSourceState(server.URL)
	require.True(t, exists)
	assert.Equal(t, stateBefore, stateDuring)
	assert.Equal(t, "accepted-content", component.store.GetEntry(server.URL).AcceptedContent)

	releaseOnce.Do(func() { close(releaseRefresh) })
	select {
	case refresh := <-result:
		require.NoError(t, refresh.err)
		require.NotNil(t, refresh.version)
	case <-time.After(testutil.LongTimeout):
		t.Fatal("authoritative refresh did not finish")
	}

	stateAfter, exists := component.store.GetSourceState(server.URL)
	require.True(t, exists)
	entryAfter := component.store.GetEntry(server.URL)
	assert.Equal(t, stateBefore, stateAfter)
	assert.Equal(t, "accepted", entryAfter.Auth.Token)
	assert.Equal(t, "accepted-content", entryAfter.AcceptedContent)
	assert.Equal(t, "refreshed-content", entryAfter.PendingContent)
	assert.True(t, entryAfter.HasPending)
	assert.Same(t, timerBefore, currentRefresher(component, server.URL))
	assert.Equal(t, int32(2), acceptedRequests.Load())
	assert.Equal(t, int32(1), candidateRequests.Load())
}

func TestReadOnlyFetchUsesRenderContext(t *testing.T) {
	requestStarted := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		close(requestStarted)
		<-r.Context().Done()
	}))
	defer server.Close()

	bus, logger := testutil.NewTestBusAndLogger()
	component := New(bus, logger, 0)
	ctx, cancel := context.WithCancel(t.Context())
	wrapper := NewHTTPStoreWrapper(ctx, component, logger, nil, SourceModeReadOnly)
	result := make(chan error, 1)
	go func() {
		_, err := wrapper.Fetch(server.URL, map[string]any{"critical": true})
		result <- err
	}()
	select {
	case <-requestStarted:
	case <-time.After(testutil.LongTimeout):
		t.Fatal("transient fetch did not start")
	}
	cancel()
	select {
	case err := <-result:
		require.Error(t, err)
		assert.True(t, errors.Is(err, context.Canceled))
	case <-time.After(testutil.LongTimeout):
		t.Fatal("transient fetch ignored render cancellation")
	}
	assert.Nil(t, component.store.GetEntry(server.URL))
}

func TestRetiredTimerCannotPublishRefreshFromOldCredentials(t *testing.T) {
	refreshStarted := make(chan struct{})
	releaseRefresh := make(chan struct{})
	oldRequests := atomic.Int32{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Header.Get("Authorization") {
		case "Bearer old-token":
			if oldRequests.Add(1) == 1 {
				_, _ = w.Write([]byte("old-content"))
				return
			}
			close(refreshStarted)
			<-releaseRefresh
			_, _ = w.Write([]byte("stale-content"))
		case "Bearer new-token":
			_, _ = w.Write([]byte("new-content"))
		default:
			w.WriteHeader(http.StatusUnauthorized)
		}
	}))
	defer server.Close()

	bus, logger := testutil.NewTestBusAndLogger()
	component := New(bus, logger, 0)
	component.mu.Lock()
	component.ctx = context.Background()
	component.mu.Unlock()

	oldWrapper := NewHTTPStoreWrapper(t.Context(), component, logger, nil, SourceModeAuthoritative)
	_, err := oldWrapper.Fetch(
		server.URL,
		map[string]any{"interval": "1ms", "critical": true},
		map[string]any{"type": "bearer", "token": "old-token"},
	)
	require.NoError(t, err)
	commitInputTransaction(t, oldWrapper)
	select {
	case <-refreshStarted:
	case <-time.After(testutil.LongTimeout):
		t.Fatal("old-credential refresh did not start")
	}

	newWrapper := NewHTTPStoreWrapper(t.Context(), component, logger, nil, SourceModeAuthoritative)
	content, err := newWrapper.Fetch(
		server.URL,
		map[string]any{"critical": true},
		map[string]any{"type": "bearer", "token": "new-token"},
	)
	require.NoError(t, err)
	assert.Equal(t, "new-content", content)
	commitInputTransaction(t, newWrapper)
	close(releaseRefresh)
	component.stopAllRefreshers()

	accepted, ok := component.store.Get(server.URL)
	require.True(t, ok)
	assert.Equal(t, "new-content", accepted)
	assert.Empty(t, component.store.GetPendingURLs())
	component.mu.Lock()
	assert.Empty(t, component.refreshers)
	component.mu.Unlock()
}

func TestRetiredTimerCannotRefreshReplacementSource(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("content"))
	}))
	defer server.Close()

	bus, logger := testutil.NewTestBusAndLogger()
	component := New(bus, logger, 0)
	component.mu.Lock()
	component.ctx = context.Background()
	component.mu.Unlock()
	oldAuth := &purehttpstore.AuthConfig{Type: purehttpstore.AuthTypeBearer, Token: "old"}
	options := purehttpstore.FetchOptions{Delay: time.Hour, Critical: true}
	_, err := component.store.Fetch(t.Context(), server.URL, options, oldAuth)
	require.NoError(t, err)
	component.RegisterURL(server.URL)
	component.mu.Lock()
	timerGeneration := component.refreshGeneration[server.URL]
	component.mu.Unlock()

	newAuth := &purehttpstore.AuthConfig{Type: purehttpstore.AuthTypeBearer, Token: "new"}
	_, err = component.store.ReconcileSource(server.URL, options, newAuth)
	require.NoError(t, err)
	refreshCalls := atomic.Int32{}
	component.refreshStoreURL = func(context.Context, string, uint64) (*purehttpstore.PendingVersion, error) {
		refreshCalls.Add(1)
		return &purehttpstore.PendingVersion{}, nil
	}

	component.refreshURLForGeneration(server.URL, timerGeneration)
	assert.Zero(t, refreshCalls.Load())
	component.ReconcileURL(server.URL)
	component.stopAllRefreshers()
}

func TestValidationOverlayRejectsPendingContentFromAnotherAuthority(t *testing.T) {
	calls := atomic.Int32{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		if calls.Add(1) == 1 {
			_, _ = w.Write([]byte("accepted"))
			return
		}
		_, _ = w.Write([]byte("pending"))
	}))
	defer server.Close()

	bus, logger := testutil.NewTestBusAndLogger()
	component := New(bus, logger, 0)
	oldAuth := &purehttpstore.AuthConfig{Type: purehttpstore.AuthTypeBearer, Token: "old"}
	_, err := component.store.Fetch(t.Context(), server.URL, purehttpstore.FetchOptions{}, oldAuth)
	require.NoError(t, err)
	changed, err := component.store.RefreshURL(t.Context(), server.URL)
	require.NoError(t, err)
	require.True(t, changed)
	overlay := purehttpstore.NewHTTPOverlay(component.store)

	wrapper := NewHTTPStoreWrapper(t.Context(), component, logger, overlay, SourceModeReadOnly)
	_, err = wrapper.Fetch(server.URL, nil, map[string]any{
		"type": "bearer", "token": "new",
	})
	require.ErrorContains(t, err, "pending content from different authentication or options")
	entry := component.store.GetEntry(server.URL)
	require.NotNil(t, entry)
	assert.Equal(t, "old", entry.Auth.Token)
	assert.Equal(t, "accepted", entry.AcceptedContent)
	assert.Equal(t, "pending", entry.PendingContent)
	assert.True(t, entry.HasPending)
}

func TestAuthoritativeSourceReplacementAbortLeavesSharedStateUnchanged(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(r.Header.Get("Authorization")))
	}))
	defer server.Close()

	bus, logger := testutil.NewTestBusAndLogger()
	component := New(bus, logger, 0)
	defer component.stopAllRefreshers()
	accepted := NewHTTPStoreWrapper(t.Context(), component, logger, nil, SourceModeAuthoritative)
	_, err := accepted.Fetch(
		server.URL,
		map[string]any{"interval": "1h", "critical": true},
		map[string]any{"type": "bearer", "token": "accepted"},
	)
	require.NoError(t, err)
	require.NoError(t, accepted.InputTransaction().Commit(t.Context()))

	stateBefore, exists := component.store.GetSourceState(server.URL)
	require.True(t, exists)
	entryBefore := component.store.GetEntry(server.URL)
	timerBefore, timerGenerationBefore, timerSourceGenerationBefore := refresherState(component, server.URL)
	watermarkBefore := component.store.Watermark()

	replacement := NewHTTPStoreWrapper(t.Context(), component, logger, nil, SourceModeAuthoritative)
	content, err := replacement.Fetch(
		server.URL,
		map[string]any{"interval": "2h", "critical": true},
		map[string]any{"type": "bearer", "token": "replacement"},
	)
	require.NoError(t, err)
	assert.Equal(t, "Bearer replacement", content)
	assert.Equal(t, stateBefore, mustSourceState(t, component, server.URL))
	assert.Equal(t, entryBefore, component.store.GetEntry(server.URL))
	assert.Equal(t, watermarkBefore, component.store.Watermark())
	timerDuring, timerGenerationDuring, timerSourceGenerationDuring := refresherState(component, server.URL)
	assert.Same(t, timerBefore, timerDuring)
	assert.Equal(t, timerGenerationBefore, timerGenerationDuring)
	assert.Equal(t, timerSourceGenerationBefore, timerSourceGenerationDuring)

	replacement.InputTransaction().Abort()
	assert.Equal(t, stateBefore, mustSourceState(t, component, server.URL))
	assert.Equal(t, entryBefore, component.store.GetEntry(server.URL))
	assert.Equal(t, watermarkBefore, component.store.Watermark())
	timerAfter, timerGenerationAfter, timerSourceGenerationAfter := refresherState(component, server.URL)
	assert.Same(t, timerBefore, timerAfter)
	assert.Equal(t, timerGenerationBefore, timerGenerationAfter)
	assert.Equal(t, timerSourceGenerationBefore, timerSourceGenerationAfter)
}

func TestConcurrentStagedSourceReplacementsCommitExactlyOneAuthority(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(r.Header.Get("Authorization")))
	}))
	defer server.Close()

	bus, logger := testutil.NewTestBusAndLogger()
	component := New(bus, logger, 0)
	defer component.stopAllRefreshers()
	base := NewHTTPStoreWrapper(t.Context(), component, logger, nil, SourceModeAuthoritative)
	_, err := base.Fetch(
		server.URL,
		map[string]any{"interval": "1h", "critical": true},
		map[string]any{"type": "bearer", "token": "base"},
	)
	require.NoError(t, err)
	require.NoError(t, base.InputTransaction().Commit(t.Context()))
	baseTimer := currentRefresher(component, server.URL)
	require.NotNil(t, baseTimer)

	type contender struct {
		token   string
		delay   time.Duration
		wrapper *HTTPStoreWrapper
	}
	contenders := []contender{
		{token: "first", delay: 2 * time.Hour},
		{token: "second", delay: 3 * time.Hour},
	}
	for index := range contenders {
		contenders[index].wrapper = NewHTTPStoreWrapper(
			t.Context(), component, logger, nil, SourceModeAuthoritative,
		)
		content, fetchErr := contenders[index].wrapper.Fetch(
			server.URL,
			map[string]any{"interval": contenders[index].delay.String(), "critical": true},
			map[string]any{"type": "bearer", "token": contenders[index].token},
		)
		require.NoError(t, fetchErr)
		assert.Equal(t, "Bearer "+contenders[index].token, content)
	}
	assert.Same(t, baseTimer, currentRefresher(component, server.URL))
	assert.Equal(t, "base", component.store.GetEntry(server.URL).Auth.Token)

	type commitResult struct {
		index int
		err   error
	}
	start := make(chan struct{})
	results := make(chan commitResult, len(contenders))
	for index := range contenders {
		go func(index int) {
			<-start
			results <- commitResult{
				index: index,
				err:   contenders[index].wrapper.InputTransaction().Commit(t.Context()),
			}
		}(index)
	}
	close(start)
	commits := make([]commitResult, 0, len(contenders))
	for range contenders {
		commits = append(commits, <-results)
	}
	winner := -1
	for _, result := range commits {
		if result.err == nil {
			require.Equal(t, -1, winner)
			winner = result.index
			continue
		}
		require.ErrorContains(t, result.err, "changed while the render was running")
		contenders[result.index].wrapper.InputTransaction().Abort()
	}
	require.NotEqual(t, -1, winner)
	entry := component.store.GetEntry(server.URL)
	require.NotNil(t, entry)
	assert.Equal(t, contenders[winner].token, entry.Auth.Token)
	assert.Equal(t, "Bearer "+contenders[winner].token, entry.AcceptedContent)
	assert.Equal(t, contenders[winner].delay, entry.Options.Delay)
	assert.NotSame(t, baseTimer, currentRefresher(component, server.URL))
}

func mustSourceState(t *testing.T, component *Component, url string) purehttpstore.SourceState {
	t.Helper()
	state, exists := component.store.GetSourceState(url)
	require.True(t, exists)
	return state
}

func currentRefresher(component *Component, url string) *time.Timer {
	component.mu.Lock()
	defer component.mu.Unlock()
	return component.refreshers[url]
}

func commitInputTransaction(t *testing.T, wrapper *HTTPStoreWrapper) {
	t.Helper()
	require.NoError(t, wrapper.InputTransaction().Commit(t.Context()))
}

func fetchAndCommitInputTransaction(t *testing.T, wrapper *HTTPStoreWrapper, args ...any) any {
	t.Helper()
	content, err := wrapper.Fetch(args...)
	require.NoError(t, err)
	commitInputTransaction(t, wrapper)
	return content
}

func refresherState(component *Component, url string) (
	timer *time.Timer,
	timerGeneration uint64,
	sourceGeneration uint64,
) {
	component.mu.Lock()
	defer component.mu.Unlock()
	return component.refreshers[url], component.refreshGeneration[url], component.refreshSourceGeneration[url]
}
