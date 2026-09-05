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
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestReplayEpochRejectsPendingABAAndForgery(t *testing.T) {
	var body atomic.Value
	body.Store("A")
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(body.Load().(string)))
	}))
	defer server.Close()
	store := newRevisionTestStore(0)
	_, err := store.Fetch(t.Context(), server.URL, FetchOptions{Critical: true}, nil)
	require.NoError(t, err)
	epoch := store.CaptureReplayEpoch()
	require.True(t, store.VerifyReplayEpoch(epoch))

	copied := *epoch
	require.False(t, store.VerifyReplayEpoch(&copied))
	epoch.revision++
	require.False(t, store.VerifyReplayEpoch(epoch))
	epoch.revision--
	require.True(t, store.VerifyReplayEpoch(epoch))
	require.False(t, newRevisionTestStore(0).VerifyReplayEpoch(epoch))

	body.Store("B")
	version, err := store.RefreshURLVersion(t.Context(), server.URL)
	require.NoError(t, err)
	require.NotNil(t, version)
	require.False(t, store.VerifyReplayEpoch(epoch))
	require.True(t, store.PromotePendingVersion(server.URL, version.Checksum, version.Revision))
	body.Store("A")
	promoteCurrentBody(t, store, server.URL)
	require.False(t, store.VerifyReplayEpoch(epoch))
}

func TestReplayEpochIgnoresMetadataOnlyRefresh(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("stable"))
	}))
	defer server.Close()
	store := newRevisionTestStore(0)
	_, err := store.Fetch(t.Context(), server.URL, FetchOptions{Critical: true}, nil)
	require.NoError(t, err)
	epoch := store.CaptureReplayEpoch()
	version, err := store.RefreshURLVersion(t.Context(), server.URL)
	require.NoError(t, err)
	require.Nil(t, version)
	require.True(t, store.VerifyReplayEpoch(epoch))
}
