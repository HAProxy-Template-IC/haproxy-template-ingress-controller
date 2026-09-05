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
	"log/slog"
	"net/http"
	"net/http/httptest"
	"slices"
	"testing"
	"time"

	iradix "github.com/hashicorp/go-immutable-radix/v2"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestPreparedInitialCandidateCommitHidesPublicationUntilRelease(t *testing.T) {
	store, server, candidate := preparedCandidateFixture(t, "candidate")
	defer server.Close()

	prepared, err := store.PrepareInitialCandidatesAndVerifyObservations(
		t.Context(),
		[]*InitialCandidate{candidate},
		nil,
	)
	require.NoError(t, err)

	read := make(chan ContentSnapshot, 1)
	started := make(chan struct{})
	go func() {
		close(started)
		read <- store.AcceptedSnapshot(candidate.URL(), candidate.sourceDescriptor)
	}()
	<-started
	assert.Never(t, func() bool { return len(read) > 0 }, 25*time.Millisecond, time.Millisecond)

	commits, watermark := prepared.Planned()
	prepared.Publish()
	require.NoError(t, prepared.ValidatePublishedPublication())
	require.NoError(t, prepared.CommitPublishedPublication())
	require.Len(t, commits, 1)
	assert.Equal(t, commits[0].Accepted.Revision(), watermark)
	assert.Never(t, func() bool { return len(read) > 0 }, 25*time.Millisecond, time.Millisecond)

	prepared.ReleaseCommittedPublication()
	select {
	case snapshot := <-read:
		assert.True(t, snapshot.Found)
		assert.Equal(t, "candidate", snapshot.Content)
		assert.Equal(t, commits[0].Accepted, snapshot.Token)
	case <-time.After(time.Second):
		t.Fatal("accepted HTTP reader remained blocked after release")
	}
}

func TestPreparedInitialCandidateCommitAbortRollsBackTentativePublication(t *testing.T) {
	store, server, candidate := preparedCandidateFixture(t, "candidate")
	defer server.Close()
	baselineSemantic := store.Watermark()
	baselineReplay := store.ReplayWatermark()
	prepared, err := store.PrepareInitialCandidatesAndVerifyObservations(
		t.Context(), []*InitialCandidate{candidate}, nil,
	)
	require.NoError(t, err)
	require.NoError(t, prepared.SealPublication())
	prepared.PublishSealed()
	require.NoError(t, prepared.ValidatePublishedPublication())
	require.NoError(t, prepared.CommitPublishedPublication())

	read := make(chan ContentSnapshot, 1)
	go func() {
		read <- store.AcceptedSnapshot(candidate.URL(), candidate.sourceDescriptor)
	}()
	assert.Never(t, func() bool { return len(read) != 0 }, 25*time.Millisecond, time.Millisecond)
	prepared.Abort()

	select {
	case snapshot := <-read:
		assert.False(t, snapshot.Found)
	case <-time.After(time.Second):
		t.Fatal("HTTP reader remained blocked after tentative publication rollback")
	}
	assert.Equal(t, baselineSemantic, store.Watermark())
	assert.Equal(t, baselineReplay, store.ReplayWatermark())

	retry, err := store.PrepareInitialCandidatesAndVerifyObservations(
		t.Context(), []*InitialCandidate{candidate}, nil,
	)
	require.NoError(t, err)
	retry.Publish()
	retry.Release()
	assert.True(t, store.AcceptedSnapshot(candidate.URL(), candidate.sourceDescriptor).Found)
}

func TestPreparedInitialCandidateCommitValidatesPublishedRoots(t *testing.T) {
	store, server, candidate := preparedCandidateFixture(t, "candidate")
	defer server.Close()
	prepared, err := store.PrepareInitialCandidatesAndVerifyObservations(
		t.Context(), []*InitialCandidate{candidate}, nil,
	)
	require.NoError(t, err)
	require.NoError(t, prepared.SealPublication())
	prepared.PublishSealed()
	require.NoError(t, prepared.ValidatePublishedPublication())
	entry := store.cache[candidate.URL()]
	entry.AcceptedContent = "poison"
	require.Error(t, prepared.ValidatePublishedPublication())
	entry.AcceptedContent = "candidate"
	require.NoError(t, prepared.CommitPublishedPublication())
	prepared.Abort()
	assert.False(t, store.AcceptedSnapshot(candidate.URL(), candidate.sourceDescriptor).Found)
}

func TestPreparedInitialCandidateCommitRollbackRestoresCorruptedReleaseAuthority(t *testing.T) {
	store, server, candidate := preparedCandidateFixture(t, "candidate")
	defer server.Close()
	prepared, err := store.PrepareInitialCandidatesAndVerifyObservations(
		t.Context(), []*InitialCandidate{candidate}, nil,
	)
	require.NoError(t, err)
	require.NoError(t, prepared.SealPublication())
	prepared.PublishSealed()
	store.prepareAuthority = nil

	assert.Panics(t, prepared.Abort)
	assert.False(t, store.AcceptedSnapshot(candidate.URL(), candidate.sourceDescriptor).Found)
	retry, err := store.PrepareInitialCandidatesAndVerifyObservations(
		t.Context(), []*InitialCandidate{candidate}, nil,
	)
	require.NoError(t, err)
	retry.Abort()
}

func TestPreparedInitialCandidateCommitRollbackUsesIndependentCheckpoint(t *testing.T) {
	poisons := map[string]func(*PreparedInitialCandidateCommit){
		"missing publication": func(prepared *PreparedInitialCandidateCommit) {
			prepared.publication = nil
		},
		"corrupted publication base": func(prepared *PreparedInitialCandidateCommit) {
			prepared.publication.base.cache = nil
		},
	}
	finalizers := map[string]func(*PreparedInitialCandidateCommit) error{
		"abort":   (*PreparedInitialCandidateCommit).AbortPublication,
		"release": (*PreparedInitialCandidateCommit).ReleasePublication,
	}
	for poisonName, poison := range poisons {
		for finalizerName, finalize := range finalizers {
			t.Run(poisonName+"/"+finalizerName, func(t *testing.T) {
				store, server, candidate := preparedCandidateFixture(t, "candidate")
				defer server.Close()
				prepared, err := store.PrepareInitialCandidatesAndVerifyObservations(
					t.Context(), []*InitialCandidate{candidate}, nil,
				)
				require.NoError(t, err)
				require.NoError(t, prepared.SealPublication())
				prepared.PublishSealed()
				poison(prepared)

				require.Error(t, finalize(prepared))
				assert.False(t, store.AcceptedSnapshot(candidate.URL(), candidate.sourceDescriptor).Found)

				retry, err := store.PrepareInitialCandidatesAndVerifyObservations(
					t.Context(), []*InitialCandidate{candidate}, nil,
				)
				require.NoError(t, err)
				retry.Publish()
				retry.Release()
				assert.True(t, store.AcceptedSnapshot(candidate.URL(), candidate.sourceDescriptor).Found)
			})
		}
	}
}

func TestPreparedInitialCandidateCommitQuarantinesFailedRollback(t *testing.T) {
	store, server, candidate := preparedCandidateFixture(t, "candidate")
	defer server.Close()
	prepared, err := store.PrepareInitialCandidatesAndVerifyObservations(
		t.Context(), []*InitialCandidate{candidate}, nil,
	)
	require.NoError(t, err)
	require.NoError(t, prepared.SealPublication())
	prepared.PublishSealed()
	prepared.rollback.roots.cache = nil

	require.Error(t, prepared.AbortPublication())
	assert.True(t, store.publicationPoisoned.Load())
	assert.False(t, store.AcceptedSnapshot(candidate.URL(), candidate.sourceDescriptor).Found)
	_, err = store.ReconcileSource(candidate.URL(), FetchOptions{Critical: true}, nil)
	require.ErrorIs(t, err, errHTTPStorePublicationPoisoned)
	_, err = store.Fetch(t.Context(), candidate.URL(), FetchOptions{Critical: true}, nil)
	require.ErrorIs(t, err, errHTTPStorePublicationPoisoned)
	_, err = store.StageSource(candidate.URL(), FetchOptions{Critical: true}, nil)
	require.ErrorIs(t, err, errHTTPStorePublicationPoisoned)
	_, err = store.PrepareInitialCandidatesAndVerifyObservations(t.Context(), nil, nil)
	require.ErrorIs(t, err, errHTTPStorePublicationPoisoned)
	_, _, err = store.NewActiveLeaseSet()
	require.ErrorIs(t, err, errHTTPStorePublicationPoisoned)
	assert.Nil(t, store.CaptureReplayEpoch())
	assert.Panics(t, func() { store.LoadFixture(candidate.URL(), "replacement") })
}

func TestPreparedInitialCandidateCommitCapturesAuthenticatedPublishedReplayState(t *testing.T) {
	store, server, candidate := preparedCandidateFixture(t, "candidate")
	defer server.Close()
	prepared, err := store.PrepareInitialCandidatesAndVerifyObservations(
		t.Context(), []*InitialCandidate{candidate}, nil,
	)
	require.NoError(t, err)
	commits, watermark := prepared.Planned()
	require.Len(t, commits, 1)
	snapshot := ContentSnapshot{
		URL: candidate.url, Descriptor: candidate.sourceDescriptor, Content: candidate.content,
		Found: true, Cacheable: true, Token: commits[0].Accepted, StoreSource: store.revisionSource,
		Observation: commits[0].Accepted.Revision(), Watermark: watermark,
	}

	state, err := prepared.PrepareAcceptedReplayState([]ContentSnapshot{snapshot})
	require.NoError(t, err)
	prepared.Publish()
	require.NoError(t, state.ValidateAuthentication())
	assert.Equal(t, []ContentSnapshot{snapshot}, state.Snapshots())
	require.Len(t, state.Proofs(), 1)
	assert.Same(t, state.Epoch(), state.Epoch())
	assert.Equal(t, watermark, state.Watermark())

	cloned := *state
	require.Error(t, cloned.ValidateAuthentication())
	originalRoot := state.root
	state.root = iradix.New[acceptedReplayStateEntry]().Root()
	require.Error(t, state.ValidateAuthentication())
	state.root = originalRoot
	require.NoError(t, state.ValidateAuthentication())

	mutated := make(chan struct{})
	go func() {
		store.LoadFixture(candidate.url, "new")
		close(mutated)
	}()
	assert.Never(t, func() bool { return len(mutated) > 0 }, 25*time.Millisecond, time.Millisecond)
	prepared.Release()
	select {
	case <-mutated:
	case <-time.After(time.Second):
		t.Fatal("HTTP mutation remained blocked after prepared authority release")
	}
	require.NoError(t, state.ValidateAuthentication())
	assert.False(t, store.VerifyReplayEpoch(state.Epoch()))
}

func TestPreparedInitialCandidateCommitPublishedReplayLeaseAbortAndPublish(t *testing.T) {
	store, server, candidate := preparedCandidateFixture(t, "candidate")
	defer server.Close()
	set, token, err := store.NewActiveLeaseSet()
	require.NoError(t, err)

	prepare := func(t *testing.T, lease *ActiveLeaseSnapshot) *PreparedInitialCandidateCommit {
		t.Helper()
		prepared, prepareErr := store.PrepareInitialCandidatesAndVerifyObservations(
			t.Context(), []*InitialCandidate{candidate}, nil,
		)
		require.NoError(t, prepareErr)
		commits, watermark := prepared.Planned()
		require.Len(t, commits, 1)
		snapshot := ContentSnapshot{
			URL: candidate.url, Descriptor: candidate.sourceDescriptor, Content: candidate.content,
			Found: true, Cacheable: true, Token: commits[0].Accepted,
			StoreSource: store.revisionSource, Observation: commits[0].Accepted.Revision(),
			Watermark: watermark,
		}
		require.NoError(t, prepared.PreparePublishedReplayActiveLeases(
			&ActiveLeaseCommit{Snapshot: lease}, []ContentSnapshot{snapshot},
		))
		return prepared
	}

	lease, err := set.BeginActiveLeases(token)
	require.NoError(t, err)
	prepared := prepare(t, lease)
	abortedToken, _, ok := prepared.PlannedActiveLeases()
	require.True(t, ok)
	prepared.Abort()
	assert.False(t, store.AcceptedSnapshot(candidate.URL(), candidate.sourceDescriptor).Found)
	assert.False(t, store.HasActiveLease(candidate.URL()))
	_, err = set.BeginActiveLeases(abortedToken)
	assert.Error(t, err)
	_, err = set.BeginActiveLeases(token)
	require.NoError(t, err)

	lease, err = set.BeginActiveLeases(token)
	require.NoError(t, err)
	prepared = prepare(t, lease)
	publishedToken, _, ok := prepared.PlannedActiveLeases()
	require.True(t, ok)
	state, ok := prepared.PlannedActiveReplayState()
	require.True(t, ok)
	require.NoError(t, state.ValidateAuthentication())
	prepared.Publish()
	prepared.Release()
	assert.True(t, store.AcceptedSnapshot(candidate.URL(), candidate.sourceDescriptor).Found)
	assert.True(t, store.HasActiveLease(candidate.URL()))

	retired, err := set.RetireActiveLeases(publishedToken)
	require.NoError(t, err)
	assert.Equal(t, []string{candidate.URL()}, retired)
	assert.False(t, store.HasActiveLease(candidate.URL()))
}

func TestPreparedInitialCandidateCommitSealRejectsCorruptedFutureState(t *testing.T) {
	t.Run("unsealed terminal", func(t *testing.T) {
		store, server, candidate := preparedCandidateFixture(t, "candidate")
		defer server.Close()
		prepared, err := store.PrepareInitialCandidatesAndVerifyObservations(
			t.Context(), []*InitialCandidate{candidate}, nil,
		)
		require.NoError(t, err)

		assert.PanicsWithValue(t, "prepared HTTP store publication is not sealed", prepared.PublishSealed)
		prepared.Abort()
		assert.False(t, store.AcceptedSnapshot(candidate.URL(), candidate.sourceDescriptor).Found)
	})

	t.Run("published replay", func(t *testing.T) {
		store, server, candidate := preparedCandidateFixture(t, "candidate")
		defer server.Close()
		set, token, err := store.NewActiveLeaseSet()
		require.NoError(t, err)
		lease, err := set.BeginActiveLeases(token)
		require.NoError(t, err)
		prepared, err := store.PrepareInitialCandidatesAndVerifyObservations(
			t.Context(), []*InitialCandidate{candidate}, nil,
		)
		require.NoError(t, err)
		commits, watermark := prepared.Planned()
		require.Len(t, commits, 1)
		snapshot := ContentSnapshot{
			URL: candidate.url, Descriptor: candidate.sourceDescriptor, Content: candidate.content,
			Found: true, Cacheable: true, Token: commits[0].Accepted,
			StoreSource: store.revisionSource, Observation: commits[0].Accepted.Revision(),
			Watermark: watermark,
		}
		require.NoError(t, prepared.PreparePublishedReplayActiveLeases(
			&ActiveLeaseCommit{Snapshot: lease}, []ContentSnapshot{snapshot},
		))
		baselineSemantic := store.semanticRevision
		baselineReplay := store.replayRevision
		prepared.active.replay.root = iradix.New[acceptedReplayStateEntry]().Root()

		require.Error(t, prepared.SealPublication())
		prepared.Abort()
		assert.Equal(t, baselineSemantic, store.Watermark())
		assert.Equal(t, baselineReplay, store.ReplayWatermark())
		assert.False(t, store.AcceptedSnapshot(candidate.URL(), candidate.sourceDescriptor).Found)
		assert.False(t, store.HasActiveLease(candidate.URL()))
	})

	t.Run("accepted token", func(t *testing.T) {
		store, server, candidate := preparedCandidateFixture(t, "candidate")
		defer server.Close()
		baselineSemantic := store.Watermark()
		baselineReplay := store.ReplayWatermark()
		prepared, err := store.PrepareInitialCandidatesAndVerifyObservations(
			t.Context(), []*InitialCandidate{candidate}, nil,
		)
		require.NoError(t, err)
		prepared.commits[0].Accepted.revision++

		require.Error(t, prepared.SealPublication())
		prepared.Abort()
		assert.Equal(t, baselineSemantic, store.Watermark())
		assert.Equal(t, baselineReplay, store.ReplayWatermark())
		assert.False(t, store.AcceptedSnapshot(candidate.URL(), candidate.sourceDescriptor).Found)
	})

	t.Run("source entry", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			_, _ = w.Write([]byte("candidate"))
		}))
		defer server.Close()
		store := New(slog.Default(), 0)
		source, err := store.StageSource(server.URL, FetchOptions{Critical: true}, nil)
		require.NoError(t, err)
		_, candidate, err := store.PrepareStagedSnapshot(t.Context(), source)
		require.NoError(t, err)
		require.NotNil(t, candidate)
		prepared, err := store.PrepareStagedSourcesAndVerifyObservations(
			t.Context(), []*StagedSource{source}, []*InitialCandidate{candidate}, nil,
		)
		require.NoError(t, err)
		prepared.sources[0].publishedEntry.sourceGeneration++

		require.Error(t, prepared.SealPublication())
		prepared.Abort()
		assert.Zero(t, store.Watermark())
		assert.Zero(t, store.ReplayWatermark())
		_, exists := store.GetSourceState(server.URL)
		assert.False(t, exists)
	})
}

func TestPreparedInitialCandidateCommitSealRejectsCorruptedStoreRoots(t *testing.T) {
	tests := map[string]func(*HTTPStore) func(){
		"nil active lease sets": func(store *HTTPStore) func() {
			original := store.activeLeaseSets
			store.activeLeaseSets = nil
			return func() { store.activeLeaseSets = original }
		},
		"nil active lease URL index": func(store *HTTPStore) func() {
			original := store.activeLeaseURLs
			store.activeLeaseURLs = nil
			return func() { store.activeLeaseURLs = original }
		},
		"corrupt active lease URL index": func(store *HTTPStore) func() {
			store.activeLeaseURLs["https://poison.example.test"] = map[uint64]SourceDescriptor{
				99: {},
			}
			return func() { delete(store.activeLeaseURLs, "https://poison.example.test") }
		},
		"invalid replay journal start": func(store *HTTPStore) func() {
			original := store.replayJournalStart
			store.replayJournalStart = 1
			return func() { store.replayJournalStart = original }
		},
		"forged replay journal URL": func(store *HTTPStore) func() {
			original := store.replayJournal[0]
			store.replayJournal[0].URL = "https://poison.example.test"
			return func() { store.replayJournal[0] = original }
		},
		"invalid semantic journal start": func(store *HTTPStore) func() {
			original := store.semanticJournalStart
			store.semanticJournalStart = -1
			return func() { store.semanticJournalStart = original }
		},
		"forged semantic journal descriptor": func(store *HTTPStore) func() {
			original := store.semanticJournal[0]
			store.semanticJournal[0].Descriptor = SourceDescriptor{
				identity: "forged", canonical: "forged",
			}
			store.semanticJournal[0].SourceIdentity = "forged"
			return func() { store.semanticJournal[0] = original }
		},
	}
	for name, poison := range tests {
		t.Run(name, func(t *testing.T) {
			store, server, candidate := preparedCandidateFixture(t, "candidate")
			defer server.Close()
			baselineSemantic := store.semanticRevision
			baselineReplay := store.replayRevision
			baselineEntry := store.cache[candidate.URL()]
			prepared, err := store.PrepareInitialCandidatesAndVerifyObservations(
				t.Context(), []*InitialCandidate{candidate}, nil,
			)
			require.NoError(t, err)
			restore := poison(store)

			require.Error(t, prepared.SealPublication())
			assert.Nil(t, prepared.publication)
			assert.Equal(t, baselineSemantic, store.semanticRevision)
			assert.Equal(t, baselineReplay, store.replayRevision)
			assert.Same(t, baselineEntry, store.cache[candidate.URL()])

			restore()
			prepared.Abort()
			retry, err := store.PrepareInitialCandidatesAndVerifyObservations(
				t.Context(), []*InitialCandidate{candidate}, nil,
			)
			require.NoError(t, err)
			require.NoError(t, retry.SealPublication())
			retry.PublishSealed()
			retry.Release()
			assert.True(t, store.AcceptedSnapshot(candidate.URL(), candidate.sourceDescriptor).Found)
		})
	}
}

func TestPreparedInitialCandidateCommitRejectsRewoundMonotonicAuthorities(t *testing.T) {
	t.Run("candidate revision", func(t *testing.T) {
		store, server, candidate := preparedCandidateFixture(t, "candidate")
		defer server.Close()
		revision := store.nextCandidateRevision
		store.nextCandidateRevision = 0

		_, err := store.PrepareInitialCandidatesAndVerifyObservations(
			t.Context(), []*InitialCandidate{candidate}, nil,
		)
		require.Error(t, err)

		store.nextCandidateRevision = revision
		retry, err := store.PrepareInitialCandidatesAndVerifyObservations(
			t.Context(), []*InitialCandidate{candidate}, nil,
		)
		require.NoError(t, err)
		retry.Abort()
	})

	t.Run("active lease set", func(t *testing.T) {
		store := New(slog.Default(), 0)
		url := "https://active.example.test/input"
		store.LoadFixture(url, "accepted")
		set, token, err := store.NewActiveLeaseSet()
		require.NoError(t, err)
		token = commitActiveLeaseReplacement(t, store, set, token, []ActiveLeaseReference{{
			URL: url, References: 1,
		}})
		require.NotZero(t, token.generation)
		setID := store.nextActiveLeaseSet
		store.nextActiveLeaseSet = 0

		prepared, err := store.PrepareInitialCandidatesAndVerifyObservations(t.Context(), nil, nil)
		require.NoError(t, err)
		require.Error(t, prepared.SealPublication())

		store.nextActiveLeaseSet = setID
		prepared.Abort()
		retry, err := store.PrepareInitialCandidatesAndVerifyObservations(t.Context(), nil, nil)
		require.NoError(t, err)
		retry.Abort()

		state := store.activeLeaseSets[set.id]
		generation := state.token.generation
		state.token.generation = 0
		prepared, err = store.PrepareInitialCandidatesAndVerifyObservations(t.Context(), nil, nil)
		require.NoError(t, err)
		require.Error(t, prepared.SealPublication())
		state.token.generation = generation
		prepared.Abort()
	})
}

func TestPreparedInitialCandidateCommitSealRejectsMalformedUnrelatedCacheEntry(t *testing.T) {
	tests := map[string]func(*HTTPStore, *CacheEntry) func(){
		"validation state": func(_ *HTTPStore, entry *CacheEntry) func() {
			original := entry.ValidationState
			entry.ValidationState = ValidationState(255)
			return func() { entry.ValidationState = original }
		},
		"source generation": func(store *HTTPStore, entry *CacheEntry) func() {
			original := entry.sourceGeneration
			entry.sourceGeneration = store.nextSourceGeneration + 1
			return func() { entry.sourceGeneration = original }
		},
		"accepted revision": func(store *HTTPStore, entry *CacheEntry) func() {
			original := entry.acceptedRevision
			entry.acceptedRevision = store.semanticRevision + 1
			return func() { entry.acceptedRevision = original }
		},
		"replay revision": func(store *HTTPStore, entry *CacheEntry) func() {
			original := entry.replayRevision
			entry.replayRevision = uint64(store.replayRevision) + 1
			return func() { entry.replayRevision = original }
		},
		"inactive pending version": func(_ *HTTPStore, entry *CacheEntry) func() {
			original := entry.PendingContent
			entry.PendingContent = "poison"
			return func() { entry.PendingContent = original }
		},
		"accepted checksum": func(_ *HTTPStore, entry *CacheEntry) func() {
			original := entry.AcceptedContent
			entry.AcceptedContent = "poison"
			return func() { entry.AcceptedContent = original }
		},
		"pending checksum": func(store *HTTPStore, entry *CacheEntry) func() {
			original := *entry
			originalPendingRevision := store.nextPendingRevision
			store.nextPendingRevision++
			entry.PendingContent = "pending"
			entry.PendingChecksum = checksum("different")
			entry.PendingRevision = store.nextPendingRevision
			entry.HasPending = true
			entry.ValidationState = StateValidating
			entry.ValidationStartedAt = time.Now()
			return func() {
				*entry = original
				store.nextPendingRevision = originalPendingRevision
			}
		},
		"source options": func(_ *HTTPStore, entry *CacheEntry) func() {
			original := entry.Options
			entry.Options.Critical = !entry.Options.Critical
			return func() { entry.Options = original }
		},
		"source authentication": func(_ *HTTPStore, entry *CacheEntry) func() {
			original := entry.Auth
			entry.Auth = &AuthConfig{Type: AuthTypeBearer, Token: "poison"}
			return func() { entry.Auth = original }
		},
		"stale validation timestamp": func(_ *HTTPStore, entry *CacheEntry) func() {
			original := entry.ValidationStartedAt
			entry.ValidationStartedAt = time.Now()
			return func() { entry.ValidationStartedAt = original }
		},
	}
	for name, poison := range tests {
		t.Run(name, func(t *testing.T) {
			store := New(slog.Default(), 0)
			url := "https://unrelated.example.test/input"
			store.LoadFixture(url, "accepted")
			reconciled, err := store.ReconcileSource(url, FetchOptions{Critical: true}, nil)
			require.NoError(t, err)
			entry := store.cache[url]
			restore := poison(store, entry)

			prepared, err := store.PrepareInitialCandidatesAndVerifyObservations(t.Context(), nil, nil)
			require.NoError(t, err)
			require.Error(t, prepared.SealPublication())
			assert.Same(t, entry, store.cache[url])

			restore()
			prepared.Abort()
			retry, err := store.PrepareInitialCandidatesAndVerifyObservations(t.Context(), nil, nil)
			require.NoError(t, err)
			require.NoError(t, retry.SealPublication())
			retry.PublishSealed()
			retry.Release()
			assert.Equal(t, "accepted", store.AcceptedSnapshot(url, reconciled.State.Descriptor).Content)
		})
	}
}

func postSealPoisonCases() map[string]func(*PreparedInitialCandidateCommit) func() {
	return map[string]func(*PreparedInitialCandidateCommit) func(){
		"cache root": func(prepared *PreparedInitialCandidateCommit) func() {
			original := prepared.publication.cache.entries
			prepared.publication.cache.entries = nil
			return func() { prepared.publication.cache.entries = original }
		},
		"cache entry": func(prepared *PreparedInitialCandidateCommit) func() {
			entry := prepared.publication.cache.entries[prepared.candidates[0].url]
			original := entry.AcceptedContent
			entry.AcceptedContent = "poison"
			return func() { entry.AcceptedContent = original }
		},
		"cache access time": func(prepared *PreparedInitialCandidateCommit) func() {
			entry := prepared.publication.cache.entries[prepared.candidates[0].url]
			original := entry.LastAccessTime
			entry.LastAccessTime = original.Add(time.Second)
			return func() { entry.LastAccessTime = original }
		},
		"cache authentication": func(prepared *PreparedInitialCandidateCommit) func() {
			entry := prepared.publication.cache.entries[prepared.candidates[0].url]
			original := entry.Auth
			entry.Auth = &AuthConfig{Type: AuthTypeBearer, Token: "poison"}
			return func() { entry.Auth = original }
		},
		"replay journal start": func(prepared *PreparedInitialCandidateCommit) func() {
			original := prepared.publication.replayJournal.start
			prepared.publication.replayJournal.start = -1
			return func() { prepared.publication.replayJournal.start = original }
		},
		"replay journal contents": func(prepared *PreparedInitialCandidateCommit) func() {
			original := prepared.publication.replayJournal.entries[0]
			prepared.publication.replayJournal.entries[0].URL = "https://poison.example.test"
			return func() { prepared.publication.replayJournal.entries[0] = original }
		},
		"semantic journal start": func(prepared *PreparedInitialCandidateCommit) func() {
			original := prepared.publication.semanticJournal.start
			prepared.publication.semanticJournal.start = -1
			return func() { prepared.publication.semanticJournal.start = original }
		},
		"semantic journal contents": func(prepared *PreparedInitialCandidateCommit) func() {
			original := prepared.publication.semanticJournal.entries[0]
			prepared.publication.semanticJournal.entries[0].URL = "https://poison.example.test"
			return func() { prepared.publication.semanticJournal.entries[0] = original }
		},
		"active set root": func(prepared *PreparedInitialCandidateCommit) func() {
			original := prepared.publication.active.sets
			prepared.publication.active.sets = nil
			return func() { prepared.publication.active.sets = original }
		},
		"active URL index": func(prepared *PreparedInitialCandidateCommit) func() {
			original := prepared.publication.active.urls
			prepared.publication.active.urls = nil
			return func() { prepared.publication.active.urls = original }
		},
		"forged active URL": func(prepared *PreparedInitialCandidateCommit) func() {
			prepared.publication.active.urls["https://poison.example.test"] =
				map[uint64]SourceDescriptor{99: {}}
			return func() { delete(prepared.publication.active.urls, "https://poison.example.test") }
		},
		"live cache root": func(prepared *PreparedInitialCandidateCommit) func() {
			original := prepared.store.cache
			prepared.store.cache = nil
			return func() { prepared.store.cache = original }
		},
		"publication store": func(prepared *PreparedInitialCandidateCommit) func() {
			original := prepared.publication.store
			prepared.publication.store = New(slog.Default(), 0)
			return func() { prepared.publication.store = original }
		},
		"prepare authority": func(prepared *PreparedInitialCandidateCommit) func() {
			original := prepared.store.prepareAuthority
			prepared.store.prepareAuthority = nil
			return func() { prepared.store.prepareAuthority = original }
		},
		"pending revision scalar": func(prepared *PreparedInitialCandidateCommit) func() {
			original := prepared.store.nextPendingRevision
			prepared.store.nextPendingRevision++
			return func() { prepared.store.nextPendingRevision = original }
		},
		"candidate revision scalar": func(prepared *PreparedInitialCandidateCommit) func() {
			original := prepared.store.nextCandidateRevision
			prepared.store.nextCandidateRevision--
			return func() { prepared.store.nextCandidateRevision = original }
		},
		"active lease set scalar": func(prepared *PreparedInitialCandidateCommit) func() {
			original := prepared.store.nextActiveLeaseSet
			prepared.store.nextActiveLeaseSet++
			return func() { prepared.store.nextActiveLeaseSet = original }
		},
	}
}

func TestPreparedInitialCandidateCommitPublishSealedRejectsPostSealPoison(t *testing.T) {
	for name, poison := range postSealPoisonCases() {
		t.Run(name, func(t *testing.T) {
			store, server, candidate := preparedCandidateFixture(t, "candidate")
			defer server.Close()
			prepared, err := store.PrepareInitialCandidatesAndVerifyObservations(
				t.Context(), []*InitialCandidate{candidate}, nil,
			)
			require.NoError(t, err)
			require.NoError(t, prepared.SealPublication())
			baselineCache := store.cache
			baselineEntry := store.cache[candidate.URL()]
			baselineSemantic := store.semanticRevision
			baselineReplay := store.replayRevision
			baselineReplayJournal := slices.Clone(store.replayJournal)
			baselineSemanticJournal := slices.Clone(store.semanticJournal)
			baselineActiveSets := store.activeLeaseSets
			baselineActiveURLs := store.activeLeaseURLs
			restore := poison(prepared)

			assert.Panics(t, prepared.PublishSealed)
			assert.Equal(t, preparedCommitSealed, prepared.state)
			if store.cache != nil {
				assert.Equal(t, baselineCache, store.cache)
				assert.Same(t, baselineEntry, store.cache[candidate.URL()])
			}
			assert.Equal(t, baselineSemantic, store.semanticRevision)
			assert.Equal(t, baselineReplay, store.replayRevision)
			assert.Equal(t, baselineReplayJournal, store.replayJournal)
			assert.Equal(t, baselineSemanticJournal, store.semanticJournal)
			assert.Equal(t, baselineActiveSets, store.activeLeaseSets)
			assert.Equal(t, baselineActiveURLs, store.activeLeaseURLs)

			restore()
			require.NotPanics(t, prepared.PublishSealed)
			prepared.Release()
			accepted := store.AcceptedSnapshot(candidate.URL(), candidate.sourceDescriptor)
			assert.True(t, accepted.Found)
			assert.Equal(t, "candidate", accepted.Content)
		})
	}
}

func TestPreparedInitialCandidateCommitAuthenticatesPostSealActiveEntries(t *testing.T) {
	tests := map[string]func(*preparedHTTPStorePublication, uint64, string) func(){
		"set entry": func(publication *preparedHTTPStorePublication, setID uint64, _ string) func() {
			state := publication.active.sets[setID]
			original := state.changeRevision
			state.changeRevision++
			return func() { state.changeRevision = original }
		},
		"pending entry": func(publication *preparedHTTPStorePublication, setID uint64, url string) func() {
			state := publication.active.sets[setID]
			state.pending[url] = ActiveLeaseChange{URL: url, Revision: 1}
			return func() { delete(state.pending, url) }
		},
		"URL index entry": func(publication *preparedHTTPStorePublication, setID uint64, url string) func() {
			original := publication.active.urls[url][setID]
			publication.active.urls[url][setID] = SourceDescriptor{identity: "poison"}
			return func() { publication.active.urls[url][setID] = original }
		},
	}
	for name, poison := range tests {
		t.Run(name, func(t *testing.T) {
			store, server, candidate := preparedCandidateFixture(t, "candidate")
			defer server.Close()
			activeURL := "https://active.example.test/input"
			store.LoadFixture(activeURL, "active")
			set, token, err := store.NewActiveLeaseSet()
			require.NoError(t, err)
			token = commitActiveLeaseReplacement(t, store, set, token, []ActiveLeaseReference{{
				URL: activeURL, References: 1,
			}})
			snapshot, err := set.BeginActiveLeases(token)
			require.NoError(t, err)
			prepared, err := store.PrepareStagedSourcesAndVerifyObservationSetsWithActiveLeasesAndReplayEpoch(
				t.Context(), nil, []*InitialCandidate{candidate}, nil, nil,
				&ActiveLeaseCommit{Snapshot: snapshot}, nil,
			)
			require.NoError(t, err)
			require.NoError(t, prepared.SealPublication())
			restore := poison(prepared.publication, set.id, activeURL)

			assert.Panics(t, prepared.PublishSealed)
			restore()
			require.NotPanics(t, prepared.PublishSealed)
			prepared.Release()
			assert.True(t, store.HasActiveLease(activeURL))
			assert.True(t, store.AcceptedSnapshot(candidate.URL(), candidate.sourceDescriptor).Found)
		})
	}
}

func TestPreparedInitialCandidateCommitBlocksSemanticMutation(t *testing.T) {
	store := New(slog.Default(), 0)
	store.LoadFixture("https://example.test/input", "old")
	snapshot := store.AcceptedSnapshot("https://example.test/input", SourceDescriptor{})
	prepared, err := store.PrepareInitialCandidatesAndVerifyObservations(
		t.Context(),
		nil,
		[]ObservationToken{snapshot.ObservationToken()},
	)
	require.NoError(t, err)
	defer prepared.Release()

	mutated := make(chan struct{})
	go func() {
		store.LoadFixture("https://example.test/input", "new")
		close(mutated)
	}()
	assert.Never(t, func() bool { return len(mutated) > 0 }, 25*time.Millisecond, time.Millisecond)

	prepared.Publish()
	assert.Never(t, func() bool { return len(mutated) > 0 }, 25*time.Millisecond, time.Millisecond)
	prepared.Release()
	select {
	case <-mutated:
	case <-time.After(time.Second):
		t.Fatal("HTTP mutation remained blocked after release")
	}
	accepted := store.AcceptedSnapshot("https://example.test/input", SourceDescriptor{})
	assert.Equal(t, "new", accepted.Content)
}

func TestPreparedInitialCandidateCommitAbortMutatesNothing(t *testing.T) {
	store, server, candidate := preparedCandidateFixture(t, "candidate")
	defer server.Close()

	prepared, err := store.PrepareInitialCandidatesAndVerifyObservations(
		t.Context(),
		[]*InitialCandidate{candidate},
		nil,
	)
	require.NoError(t, err)
	prepared.Abort()

	snapshot := store.AcceptedSnapshot(candidate.URL(), candidate.sourceDescriptor)
	assert.False(t, snapshot.Found)
}

func TestPreparedInitialCandidateCommitAcquireHonorsContext(t *testing.T) {
	store := New(slog.Default(), 0)
	first, err := store.PrepareInitialCandidatesAndVerifyObservations(t.Context(), nil, nil)
	require.NoError(t, err)
	defer first.Abort()

	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	_, err = store.PrepareInitialCandidatesAndVerifyObservations(ctx, nil, nil)
	require.ErrorIs(t, err, context.Canceled)
}

func TestCommitInitialCandidatesConveniencePathAbortsAfterPublishPanic(t *testing.T) {
	store, server, candidate := preparedCandidateFixture(t, "candidate")
	defer server.Close()
	originalRevision := store.nextCandidateRevision
	ctx := &postPreparePoisonContext{
		Context: t.Context(),
		poison:  func() { store.nextCandidateRevision = 0 },
	}

	assert.Panics(t, func() {
		_, _, _ = store.CommitInitialCandidatesAndVerifyObservations(
			ctx, []*InitialCandidate{candidate}, nil,
		)
	})
	store.nextCandidateRevision = originalRevision

	retry, err := store.PrepareInitialCandidatesAndVerifyObservations(
		t.Context(), []*InitialCandidate{candidate}, nil,
	)
	require.NoError(t, err)
	retry.Abort()
}

type postPreparePoisonContext struct {
	context.Context
	calls  int
	poison func()
}

func (c *postPreparePoisonContext) Err() error {
	c.calls++
	if c.calls == 2 {
		c.poison()
	}
	return nil
}

func TestPreparedCommitRejectsOwnNegativeToPresentTransition(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("candidate"))
	}))
	defer server.Close()
	store := New(slog.Default(), 0)
	options := FetchOptions{Critical: true}
	descriptor, err := DescribeSource(options, nil)
	require.NoError(t, err)
	negative := store.AcceptedSnapshot(server.URL, descriptor)
	require.False(t, negative.Found)
	source, err := store.StageSource(server.URL, options, nil)
	require.NoError(t, err)
	_, candidate, err := store.PrepareStagedSnapshot(t.Context(), source)
	require.NoError(t, err)
	require.NotNil(t, candidate)

	_, err = store.PrepareStagedSourcesAndVerifyObservations(
		t.Context(),
		[]*StagedSource{source},
		[]*InitialCandidate{candidate},
		[]ObservationToken{negative.ObservationToken()},
	)
	require.ErrorContains(t, err, "invalidates a render observation")
	assert.False(t, store.AcceptedSnapshot(server.URL, descriptor).Found)
	_, exists := store.GetSourceState(server.URL)
	assert.False(t, exists)
}

func TestPreparedCommitRejectsOwnSourceReplacement(t *testing.T) {
	t.Run("accepted observation", func(t *testing.T) {
		store, server, candidate := preparedCandidateFixture(t, "accepted")
		defer server.Close()
		prepared, err := store.PrepareInitialCandidatesAndVerifyObservations(
			t.Context(), []*InitialCandidate{candidate}, nil,
		)
		require.NoError(t, err)
		prepared.Publish()
		prepared.Release()
		accepted := store.AcceptedSnapshot(candidate.URL(), candidate.sourceDescriptor)
		require.True(t, accepted.Found)
		replacement, err := store.StageSource(
			candidate.URL(), FetchOptions{Critical: true, Timeout: time.Second}, nil,
		)
		require.NoError(t, err)

		_, err = store.PrepareStagedSourcesAndVerifyObservations(
			t.Context(),
			[]*StagedSource{replacement},
			nil,
			[]ObservationToken{accepted.ObservationToken()},
		)
		require.ErrorContains(t, err, "invalidates a render observation")
		assert.Equal(t, "accepted", store.AcceptedSnapshot(candidate.URL(), candidate.sourceDescriptor).Content)
	})

	t.Run("negative observation", func(t *testing.T) {
		store := New(slog.Default(), 0)
		url := "https://example.test/input"
		originalOptions := FetchOptions{Critical: true}
		original, err := store.ReconcileSource(url, originalOptions, nil)
		require.NoError(t, err)
		negative := store.AcceptedSnapshot(url, original.State.Descriptor)
		require.False(t, negative.Found)
		replacement, err := store.StageSource(
			url, FetchOptions{Critical: true, Timeout: time.Second}, nil,
		)
		require.NoError(t, err)

		_, err = store.PrepareStagedSourcesAndVerifyObservations(
			t.Context(),
			[]*StagedSource{replacement},
			nil,
			[]ObservationToken{negative.ObservationToken()},
		)
		require.ErrorContains(t, err, "invalidates a render observation")
		assert.Equal(t, original.State, mustSourceState(t, store, url))
	})
}

func TestPreparedCommitRejectsOwnObservationJournalEviction(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("candidate"))
	}))
	defer server.Close()
	store := New(slog.Default(), 0)
	store.semanticJournalCapacity = 1
	negative := store.AcceptedSnapshot("https://unrelated.example.test/input", SourceDescriptor{})
	source, err := store.StageSource(server.URL, FetchOptions{Critical: true}, nil)
	require.NoError(t, err)
	_, candidate, err := store.PrepareStagedSnapshot(t.Context(), source)
	require.NoError(t, err)
	require.NotNil(t, candidate)

	_, err = store.PrepareStagedSourcesAndVerifyObservations(
		t.Context(),
		[]*StagedSource{source},
		[]*InitialCandidate{candidate},
		[]ObservationToken{negative.ObservationToken()},
	)
	require.ErrorContains(t, err, "invalidates a render observation")
}

func TestPreparedCommitPreservesUnrelatedNegativeObservation(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("candidate"))
	}))
	defer server.Close()
	store := New(slog.Default(), 0)
	negative := store.AcceptedSnapshot("https://unrelated.example.test/input", SourceDescriptor{})
	source, err := store.StageSource(server.URL, FetchOptions{Critical: true}, nil)
	require.NoError(t, err)
	_, candidate, err := store.PrepareStagedSnapshot(t.Context(), source)
	require.NoError(t, err)
	require.NotNil(t, candidate)

	prepared, err := store.PrepareStagedSourcesAndVerifyObservations(
		t.Context(),
		[]*StagedSource{source},
		[]*InitialCandidate{candidate},
		[]ObservationToken{negative.ObservationToken()},
	)
	require.NoError(t, err)
	prepared.Publish()
	prepared.Release()
	assert.True(t, store.VerifyObservations([]ObservationToken{negative.ObservationToken()}))
}

func TestPreparedCommitRebasesVerificationOnlyNegativeObservation(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	defer server.Close()
	store := New(slog.Default(), 0)
	source, err := store.StageSource(server.URL, FetchOptions{}, nil)
	require.NoError(t, err)
	snapshot, candidate, err := store.PrepareStagedSnapshot(t.Context(), source)
	require.NoError(t, err)
	require.Nil(t, candidate)
	require.False(t, snapshot.Cacheable)
	observation := snapshot.ObservationToken()
	require.True(t, observation.Valid())

	prepared, err := store.PrepareStagedSourcesAndVerifyObservationSets(
		t.Context(),
		[]*StagedSource{source},
		nil,
		[]ObservationToken{observation},
		nil,
	)
	require.NoError(t, err)
	prepared.Publish()
	prepared.Release()
	assert.False(t, store.VerifyObservations([]ObservationToken{observation}))
	snapshot.Watermark = store.Watermark()
	assert.True(t, store.VerifyObservations([]ObservationToken{snapshot.ObservationToken()}))
}

func preparedCandidateFixture(t *testing.T, body string) (*HTTPStore, *httptest.Server, *InitialCandidate) {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(body))
	}))
	store := New(slog.Default(), 0)
	reconciled, err := store.ReconcileSource(server.URL, FetchOptions{Critical: true}, nil)
	require.NoError(t, err)
	_, candidate, err := store.PrepareInitialSnapshot(t.Context(), server.URL, reconciled.State)
	require.NoError(t, err)
	require.NotNil(t, candidate)
	return store, server, candidate
}
