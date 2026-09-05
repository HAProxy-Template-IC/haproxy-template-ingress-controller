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

func TestAcceptedReplayProofRejectsRefreshPendingAndABA(t *testing.T) {
	var body atomic.Value
	body.Store("A")
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(body.Load().(string)))
	}))
	defer server.Close()
	store := newRevisionTestStore(0)
	options := FetchOptions{Critical: true}
	_, err := store.Fetch(t.Context(), server.URL, options, nil)
	require.NoError(t, err)
	descriptor, err := DescribeSource(options, nil)
	require.NoError(t, err)
	snapshot := store.AcceptedSnapshot(server.URL, descriptor)
	proof, ok := store.CaptureAcceptedReplayProof(&snapshot)
	require.True(t, ok)

	current, source, ok := store.StageAcceptedReplayProof(proof)
	require.True(t, ok)
	require.Equal(t, snapshot.Token, current.Token)
	require.True(t, store.VerifyStagedSource(source))
	version, err := store.RefreshURLVersion(t.Context(), server.URL)
	require.NoError(t, err)
	require.Nil(t, version)
	_, refreshedSource, ok := store.StageAcceptedReplayProof(proof)
	require.True(t, ok)
	require.True(t, store.VerifyStagedSource(refreshedSource))

	body.Store("B")
	version, err = store.RefreshURLVersion(t.Context(), server.URL)
	require.NoError(t, err)
	require.NotNil(t, version)
	_, _, ok = store.StageAcceptedReplayProof(proof)
	require.False(t, ok)
	require.False(t, store.VerifyStagedSource(source))
	require.True(t, store.PromotePendingVersion(server.URL, version.Checksum, version.Revision))
	body.Store("A")
	promoteCurrentBody(t, store, server.URL)
	_, _, ok = store.StageAcceptedReplayProof(proof)
	require.False(t, ok)
}

func TestAcceptedReplayProofRejectsTamperCopyAndForeignStore(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("value"))
	}))
	defer server.Close()
	store := newRevisionTestStore(0)
	options := FetchOptions{Critical: true}
	_, err := store.Fetch(t.Context(), server.URL, options, nil)
	require.NoError(t, err)
	descriptor, err := DescribeSource(options, nil)
	require.NoError(t, err)
	snapshot := store.AcceptedSnapshot(server.URL, descriptor)
	proof, ok := store.CaptureAcceptedReplayProof(&snapshot)
	require.True(t, ok)

	copied := *proof
	_, _, ok = store.StageAcceptedReplayProof(&copied)
	require.False(t, ok)
	proof.replay++
	_, _, ok = store.StageAcceptedReplayProof(proof)
	require.False(t, ok)
	_, _, ok = newRevisionTestStore(0).StageAcceptedReplayProof(proof)
	require.False(t, ok)
}

func TestAcceptedReplayProofAndEpochRejectFixtureValidationStateCleanup(t *testing.T) {
	var body atomic.Value
	body.Store("A")
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(body.Load().(string)))
	}))
	defer server.Close()
	store := newRevisionTestStore(0)
	store.LoadFixture(server.URL, "A")
	body.Store("B")
	version, err := store.RefreshURLVersion(t.Context(), server.URL)
	require.NoError(t, err)
	require.NotNil(t, version)
	require.True(t, store.RejectPendingVersion(server.URL, version.Checksum, version.Revision))

	snapshot := store.AcceptedSnapshot(server.URL, SourceDescriptor{})
	proof, ok := store.CaptureAcceptedReplayProof(&snapshot)
	require.True(t, ok)
	epoch := store.CaptureReplayEpoch()
	require.True(t, store.VerifyReplayEpoch(epoch))

	store.LoadFixture(server.URL, "A")
	_, _, ok = store.StageAcceptedReplayProof(proof)
	require.False(t, ok)
	require.False(t, store.VerifyReplayEpoch(epoch))
}

func TestAcceptedReplayProofAndEpochIgnoreFixtureAccessMetadata(t *testing.T) {
	const url = "https://fixture.example.test/value"
	store := newRevisionTestStore(0)
	store.LoadFixture(url, "A")
	snapshot := store.AcceptedSnapshot(url, SourceDescriptor{})
	proof, ok := store.CaptureAcceptedReplayProof(&snapshot)
	require.True(t, ok)
	epoch := store.CaptureReplayEpoch()

	store.LoadFixture(url, "A")
	current, _, ok := store.StageAcceptedReplayProof(proof)
	require.True(t, ok)
	require.Equal(t, snapshot.Token, current.Token)
	require.True(t, store.VerifyReplayEpoch(epoch))
}
