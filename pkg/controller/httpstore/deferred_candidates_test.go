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
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"gitlab.com/haproxy-haptic/haptic/pkg/controller/testutil"
	purehttpstore "gitlab.com/haproxy-haptic/haptic/pkg/httpstore"
)

func TestDeferredCandidatesRefetchAfterSourceReplacement(t *testing.T) {
	var requests atomic.Int32
	entered := make(chan struct{})
	release := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if requests.Add(1) == 1 {
			close(entered)
			select {
			case <-release:
			case <-r.Context().Done():
				return
			}
		}
		_, _ = w.Write([]byte(r.Header.Get("Authorization")))
	}))
	t.Cleanup(server.Close)
	bus, logger := testutil.NewTestBusAndLogger()
	component := New(bus, logger, 0)
	ctx, candidates := WithDeferredCandidates(t.Context())
	t.Cleanup(candidates.Close)
	fetch := func(token string) (*HTTPStoreWrapper, any, error) {
		wrapper := NewHTTPStoreWrapper(ctx, component, logger, nil, SourceModeAuthoritative)
		value, err := wrapper.Fetch(server.URL, map[string]any{"critical": true}, map[string]any{"type": "bearer", "token": token})
		return wrapper, value, err
	}
	first, _, err := fetch("old")
	require.ErrorIs(t, err, ErrCandidatePending)
	first.InputTransaction().Abort()
	awaited := make(chan error, 1)
	go func() {
		_, fetchErr := candidates.Await(t.Context())
		awaited <- fetchErr
	}()
	<-entered
	_, err = component.GetStore().ReconcileSource(server.URL, purehttpstore.FetchOptions{Critical: true},
		&purehttpstore.AuthConfig{Type: "bearer", Token: "new"})
	require.NoError(t, err)
	close(release)
	require.NoError(t, <-awaited)
	second, _, err := fetch("new")
	require.ErrorIs(t, err, ErrCandidatePending)
	second.InputTransaction().Abort()
	retry, err := candidates.Await(t.Context())
	require.NoError(t, err)
	require.True(t, retry)
	third, content, err := fetch("new")
	require.NoError(t, err)
	assert.Equal(t, "Bearer new", content)
	commitInputTransaction(t, third)
	assert.Equal(t, int32(2), requests.Load())
	accepted, ok := component.GetStore().Get(server.URL)
	require.True(t, ok)
	assert.Equal(t, "Bearer new", accepted)
}

func TestDeferredCandidatesCancellationDrainsFetch(t *testing.T) {
	entered := make(chan struct{})
	canceled := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		close(entered)
		<-r.Context().Done()
		close(canceled)
	}))
	t.Cleanup(server.Close)
	bus, logger := testutil.NewTestBusAndLogger()
	component := New(bus, logger, 0)
	outer, cancel := context.WithCancel(t.Context())
	t.Cleanup(cancel)
	ctx, candidates := WithDeferredCandidates(outer)
	t.Cleanup(candidates.Close)
	wrapper := NewHTTPStoreWrapper(ctx, component, logger, nil, SourceModeAuthoritative)
	_, err := wrapper.Fetch(server.URL, map[string]any{"critical": true})
	require.ErrorIs(t, err, ErrCandidatePending)
	wrapper.InputTransaction().Abort()
	awaited := make(chan error, 1)
	go func() {
		_, fetchErr := candidates.Await(outer)
		awaited <- fetchErr
	}()
	<-entered
	cancel()
	require.ErrorIs(t, <-awaited, context.Canceled)
	candidates.Close()
	<-canceled
	_, exists := component.GetStore().GetSourceState(server.URL)
	assert.False(t, exists, "aborting discovery must not publish source authority")
}

func TestDeferredCandidatesCancellationBeforeFetch(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, _ *http.Request) {
		t.Error("a canceled candidate contacted its source")
	}))
	t.Cleanup(server.Close)
	bus, logger := testutil.NewTestBusAndLogger()
	component := New(bus, logger, 0)
	outer, cancel := context.WithCancel(t.Context())
	t.Cleanup(cancel)
	ctx, candidates := WithDeferredCandidates(outer)
	t.Cleanup(candidates.Close)
	wrapper := NewHTTPStoreWrapper(ctx, component, logger, nil, SourceModeAuthoritative)
	_, err := wrapper.Fetch(server.URL, map[string]any{"critical": true})
	require.ErrorIs(t, err, ErrCandidatePending)
	wrapper.InputTransaction().Abort()
	cancel()
	retry, err := candidates.Await(outer)
	require.ErrorIs(t, err, context.Canceled)
	assert.False(t, retry)
	candidates.Close()
	assert.False(t, candidates.pending[server.URL].started)
}

func TestDeferredCandidatesDoNotReplaySupersededContent(t *testing.T) {
	for _, revoke := range []bool{false, true} {
		t.Run(fmt.Sprintf("revoked=%t", revoke), func(t *testing.T) {
			var requests atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				_, _ = fmt.Fprintf(w, "version-%d", requests.Add(1))
			}))
			t.Cleanup(server.Close)
			bus, logger := testutil.NewTestBusAndLogger()
			component := New(bus, logger, 0)
			ctx, candidates := WithDeferredCandidates(t.Context())
			t.Cleanup(candidates.Close)
			discovery := NewHTTPStoreWrapper(ctx, component, logger, nil, SourceModeAuthoritative)
			_, err := discovery.Fetch(server.URL, map[string]any{"critical": true})
			require.ErrorIs(t, err, ErrCandidatePending)
			discovery.InputTransaction().Abort()
			retry, err := candidates.Await(t.Context())
			require.NoError(t, err)
			require.True(t, retry)

			concurrent := NewHTTPStoreWrapper(t.Context(), component, logger, nil, SourceModeAuthoritative)
			value, err := concurrent.Fetch(server.URL, map[string]any{"critical": true})
			require.NoError(t, err)
			require.Equal(t, "version-2", value)
			commitInputTransaction(t, concurrent)
			if revoke {
				require.Equal(t, 1, component.RevokeAcceptances(component.AcceptanceSequence()))
			}

			resumed := NewHTTPStoreWrapper(ctx, component, logger, nil, SourceModeAuthoritative)
			value, err = resumed.Fetch(server.URL, map[string]any{"critical": true})
			expected := "version-2"
			if revoke {
				require.ErrorIs(t, err, ErrCandidatePending)
				resumed.InputTransaction().Abort()
				retry, err = candidates.Await(t.Context())
				require.NoError(t, err)
				require.True(t, retry)
				resumed = NewHTTPStoreWrapper(ctx, component, logger, nil, SourceModeAuthoritative)
				value, err = resumed.Fetch(server.URL, map[string]any{"critical": true})
				expected = "version-3"
			}
			require.NoError(t, err)
			assert.Equal(t, expected, value)
			commitInputTransaction(t, resumed)
			accepted, found := component.GetStore().Get(server.URL)
			require.True(t, found)
			assert.Equal(t, expected, accepted)
		})
	}
}
