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
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestHTTPStoreSemanticRevisionIgnoresNonObservableMutations(t *testing.T) {
	var body atomic.Value
	body.Store("accepted")
	var etag atomic.Value
	etag.Store("first")
	var notModified atomic.Bool
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		if notModified.Load() {
			w.WriteHeader(http.StatusNotModified)
			return
		}
		w.Header().Set("ETag", etag.Load().(string))
		_, _ = w.Write([]byte(body.Load().(string)))
	}))
	defer server.Close()

	store := newRevisionTestStore(0)
	options := FetchOptions{Critical: true}
	_, err := store.Fetch(t.Context(), server.URL, options, nil)
	require.NoError(t, err)
	descriptor, err := DescribeSource(options, nil)
	require.NoError(t, err)
	baseline := store.AcceptedSnapshot(server.URL, descriptor)
	require.True(t, baseline.Cacheable)

	_, _ = store.Get(server.URL)
	_, _ = store.GetSource(server.URL, descriptor)
	_, _ = store.GetForValidation(server.URL)
	notModified.Store(true)
	changed, err := store.RefreshURL(t.Context(), server.URL)
	require.NoError(t, err)
	assert.False(t, changed)
	notModified.Store(false)
	etag.Store("second")
	changed, err = store.RefreshURL(t.Context(), server.URL)
	require.NoError(t, err)
	assert.False(t, changed)

	after := store.AcceptedSnapshot(server.URL, descriptor)
	assert.Equal(t, baseline.Token, after.Token)
	assert.Equal(t, baseline.Observation, after.Observation)
	assert.Equal(t, baseline.Watermark, after.Watermark)
	current, changes, complete := store.ChangesSince(baseline.Watermark)
	assert.True(t, complete)
	assert.Equal(t, baseline.Watermark, current)
	assert.Empty(t, changes)
}

func TestHTTPStoreObservationsTrackOnlyTheExactSource(t *testing.T) {
	store := newRevisionTestStore(0)
	url := "https://example.test/source"
	first, err := DescribeSource(FetchOptions{}, nil)
	require.NoError(t, err)
	second, err := DescribeSource(FetchOptions{}, &AuthConfig{Type: AuthTypeBearer, Token: "second"})
	require.NoError(t, err)

	missingFirst := store.AcceptedSnapshot(url, first)
	missingSecond := store.AcceptedSnapshot(url, second)
	missingFirstToken := missingFirst.ObservationToken()
	missingSecondToken := missingSecond.ObservationToken()
	require.True(t, missingFirstToken.Valid())
	require.True(t, missingSecondToken.Valid())
	store.LoadFixture("https://example.test/unrelated", "content")
	assert.Equal(t, missingFirst.Observation, store.AcceptedSnapshot(url, first).Observation)
	assert.Equal(t, missingSecond.Observation, store.AcceptedSnapshot(url, second).Observation)
	assert.True(t, store.VerifyObservations([]ObservationToken{
		missingFirst.ObservationToken(),
		missingSecond.ObservationToken(),
	}))

	reconciled, err := store.ReconcileSource(url, FetchOptions{}, nil)
	require.NoError(t, err)
	firstSource := store.AcceptedSnapshot(url, first)
	assert.False(t, firstSource.Found)
	assert.Equal(t, missingFirst.Observation, firstSource.Observation)
	assert.Equal(t, missingSecond.Observation, store.AcceptedSnapshot(url, second).Observation)
	assert.False(t, store.VerifyObservations([]ObservationToken{missingFirst.ObservationToken()}))
	assert.True(t, store.VerifyObservations([]ObservationToken{missingSecond.ObservationToken()}))
	assert.True(t, store.VerifyObservations([]ObservationToken{firstSource.ObservationToken()}))

	reconciledAgain, err := store.ReconcileSource(url, FetchOptions{}, nil)
	require.NoError(t, err)
	assert.False(t, reconciledAgain.Changed)
	assert.Equal(t, reconciled.State, reconciledAgain.State)
	assert.Equal(t, firstSource.Observation, store.AcceptedSnapshot(url, first).Observation)

	replaced, err := store.ReconcileSource(
		url,
		FetchOptions{},
		&AuthConfig{Type: AuthTypeBearer, Token: "second"},
	)
	require.NoError(t, err)
	require.True(t, replaced.Changed)
	assert.Equal(t, firstSource.Observation, store.AcceptedSnapshot(url, first).Observation)
	assert.Equal(t, missingSecond.Observation, store.AcceptedSnapshot(url, second).Observation)
	assert.False(t, store.VerifyObservations([]ObservationToken{firstSource.ObservationToken()}))
	assert.False(t, store.VerifyObservations([]ObservationToken{missingSecond.ObservationToken()}))
}

func TestAcceptedHTTPOverlayFreezesMissingObservations(t *testing.T) {
	store := newRevisionTestStore(0)
	url := "https://example.test/source"
	first, err := DescribeSource(FetchOptions{}, nil)
	require.NoError(t, err)
	_, err = store.ReconcileSource(url, FetchOptions{}, nil)
	require.NoError(t, err)

	overlay := NewAcceptedHTTPOverlay(store)
	frozen := overlay.Snapshot(url, first)
	require.False(t, frozen.Found)
	frozenToken := frozen.ObservationToken()
	require.True(t, frozenToken.Valid())

	_, err = store.ReconcileSource(
		url,
		FetchOptions{},
		&AuthConfig{Type: AuthTypeBearer, Token: "replacement"},
	)
	require.NoError(t, err)
	assert.False(t, store.VerifyObservations([]ObservationToken{frozen.ObservationToken()}))
	assert.Equal(t, frozen, overlay.Snapshot(url, first))
}

func TestHTTPStoreRevisionSourceIsStableAndUnique(t *testing.T) {
	first := newRevisionTestStore(0)
	second := newRevisionTestStore(0)
	require.NotZero(t, first.RevisionSource())
	assert.Equal(t, first.RevisionSource(), first.RevisionSource())
	assert.NotEqual(t, first.RevisionSource(), second.RevisionSource())
}

func TestHTTPStoreSnapshotUsesExactDescriptorBeyondDiagnosticDigest(t *testing.T) {
	store := newRevisionTestStore(0)
	first := SourceDescriptor{identity: "forced-collision", canonical: "first"}
	second := SourceDescriptor{identity: "forced-collision", canonical: "second"}
	store.mu.Lock()
	entry := &CacheEntry{
		URL:              "http://source",
		AcceptedContent:  "first-content",
		AcceptedChecksum: checksum("first-content"),
		sourceIdentity:   first.Identity(),
		sourceDescriptor: first,
	}
	store.cache[entry.URL] = entry
	entry.acceptedRevision = store.recordSemanticChangeLocked(
		entry.URL,
		SourceDescriptor{},
		first,
		false,
	)
	store.mu.Unlock()

	assert.True(t, store.AcceptedSnapshot(entry.URL, first).Found)
	assert.False(t, store.AcceptedSnapshot(entry.URL, second).Found)
}

func TestSourceDescriptorCompareUsesExactCanonicalTieBreaker(t *testing.T) {
	first := SourceDescriptor{identity: "forced-collision", canonical: "first"}
	second := SourceDescriptor{identity: "forced-collision", canonical: "second"}

	firstAgain := SourceDescriptor{identity: "forced-collision", canonical: "first"}

	assert.Negative(t, first.Compare(second))
	assert.Positive(t, second.Compare(first))
	assert.Zero(t, first.Compare(firstAgain))
}

func TestSourceDescriptorDoesNotFormatCredentials(t *testing.T) {
	descriptor, err := DescribeSource(FetchOptions{}, &AuthConfig{
		Type:  AuthTypeBearer,
		Token: "secret-token",
	})
	require.NoError(t, err)
	assert.NotContains(t, fmt.Sprintf("%+v", descriptor), "secret-token")
}

func TestContentSnapshotRejectsImplicitJSONEncoding(t *testing.T) {
	descriptor, err := DescribeSource(FetchOptions{}, &AuthConfig{
		Type:  AuthTypeBearer,
		Token: "secret-token",
	})
	require.NoError(t, err)
	encoded, err := json.Marshal(ContentSnapshot{
		URL:        "https://example.com",
		Descriptor: descriptor,
		Content:    "content",
	})
	require.ErrorContains(t, err, "explicit dependency encoding")
	assert.Empty(t, encoded)
	assert.NotContains(t, err.Error(), "secret-token")
}

func TestSourceDescriptorNormalizesEquivalentDeclarations(t *testing.T) {
	first, err := DescribeSource(FetchOptions{}, &AuthConfig{
		Type: AuthTypeHeader,
		Headers: map[string]string{
			"x-api-key": "value",
			"X-Second":  "second",
		},
	})
	require.NoError(t, err)
	second, err := DescribeSource(FetchOptions{
		Timeout:    DefaultTimeout,
		Retries:    DefaultRetries,
		RetryDelay: DefaultRetryDelay,
	}, &AuthConfig{
		Type: AuthTypeHeader,
		Headers: map[string]string{
			"X-API-Key": "value",
			"x-second":  "second",
		},
	})
	require.NoError(t, err)
	assert.Equal(t, first, second)
}

func TestHTTPStorePendingLifecycleChangesRevisionOnlyOnPromotion(t *testing.T) {
	var body atomic.Value
	body.Store("accepted")
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
	identity := descriptor.Identity()
	baseline := store.AcceptedSnapshot(server.URL, descriptor)

	body.Store("pending")
	version, err := store.RefreshURLVersion(t.Context(), server.URL)
	require.NoError(t, err)
	require.NotNil(t, version)
	assert.Equal(t, baseline.Token, store.AcceptedSnapshot(server.URL, descriptor).Token)
	assert.Equal(t, baseline.Watermark, store.Watermark())
	require.True(t, store.RejectPendingVersion(server.URL, version.Checksum, version.Revision))
	assert.Equal(t, baseline.Token, store.AcceptedSnapshot(server.URL, descriptor).Token)
	assert.Equal(t, baseline.Watermark, store.Watermark())

	version, err = store.RefreshURLVersion(t.Context(), server.URL)
	require.NoError(t, err)
	require.NotNil(t, version)
	require.True(t, store.PromotePendingVersion(server.URL, version.Checksum, version.Revision))
	promoted := store.AcceptedSnapshot(server.URL, descriptor)
	assert.Equal(t, "pending", promoted.Content)
	assert.NotEqual(t, baseline.Token, promoted.Token)
	assert.False(t, store.VerifySnapshots([]SnapshotToken{baseline.Token}))
	assert.True(t, store.VerifySnapshots([]SnapshotToken{promoted.Token}))

	current, changes, complete := store.ChangesSince(baseline.Watermark)
	require.True(t, complete)
	assert.Equal(t, promoted.Watermark, current)
	require.Len(t, changes, 1)
	assert.Equal(t, server.URL, changes[0].URL)
	assert.Equal(t, identity, changes[0].SourceIdentity)
	assert.False(t, changes[0].Removed)
}

func TestHTTPStoreSnapshotTokenIsABASafe(t *testing.T) {
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
	firstA := store.AcceptedSnapshot(server.URL, descriptor)

	body.Store("B")
	promoteCurrentBody(t, store, server.URL)
	body.Store("A")
	promoteCurrentBody(t, store, server.URL)
	secondA := store.AcceptedSnapshot(server.URL, descriptor)

	assert.Equal(t, firstA.Content, secondA.Content)
	assert.NotEqual(t, firstA.Token, secondA.Token)
	assert.False(t, store.VerifySnapshots([]SnapshotToken{firstA.Token}))
	assert.True(t, store.VerifySnapshots([]SnapshotToken{secondA.Token}))
}

func TestHTTPStoreConcurrentSnapshotsAndPromotions(t *testing.T) {
	var body atomic.Value
	body.Store("0")
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

	finished := make(chan struct{})
	readDone := make(chan struct{})
	go func() {
		defer close(readDone)
		for {
			select {
			case <-finished:
				return
			default:
				snapshot := store.AcceptedSnapshot(server.URL, descriptor)
				if snapshot.Cacheable {
					_ = store.VerifySnapshots([]SnapshotToken{snapshot.Token})
				}
			}
		}
	}()
	for value := 1; value <= 20; value++ {
		body.Store(fmt.Sprintf("%d", value))
		promoteCurrentBody(t, store, server.URL)
	}
	close(finished)
	<-readDone
	assert.Equal(t, "20", store.AcceptedSnapshot(server.URL, descriptor).Content)
}

func TestInitialCandidateCommitReturnsMatchingAcceptedToken(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	defer server.Close()

	store := newRevisionTestStore(0)
	options := FetchOptions{Critical: true}
	reconciled, err := store.ReconcileSource(server.URL, options, nil)
	require.NoError(t, err)
	snapshot, candidate, err := store.PrepareInitialSnapshot(t.Context(), server.URL, reconciled.State)
	require.NoError(t, err)
	require.NotNil(t, candidate)
	assert.True(t, snapshot.Found)
	assert.True(t, snapshot.Cacheable)
	assert.Empty(t, snapshot.Content)
	assert.Equal(t, SnapshotInitialCandidate, snapshot.Token.Kind())

	commits, watermark, err := store.CommitInitialCandidatesAndVerify(t.Context(), []*InitialCandidate{candidate}, nil)
	require.NoError(t, err)
	require.Len(t, commits, 1)
	accepted := store.AcceptedSnapshot(server.URL, reconciled.State.Descriptor)
	assert.True(t, accepted.Found)
	assert.True(t, accepted.Cacheable)
	assert.Empty(t, accepted.Content)
	assert.Equal(t, snapshot.Token, commits[0].Candidate)
	assert.Equal(t, accepted.Token, commits[0].Accepted)
	assert.Equal(t, accepted.Watermark, watermark)
	pinned, state, ok := store.PinAcceptedSnapshot(accepted.Token)
	require.True(t, ok)
	assert.Equal(t, accepted, pinned)
	assert.Equal(t, reconciled.State.Descriptor, state.Descriptor)
}

func TestInitialCandidateCommitDoesNotPartiallyAcceptAfterPinnedInputChanges(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("candidate"))
	}))
	defer server.Close()

	store := newRevisionTestStore(0)
	store.LoadFixture("http://pinned", "before")
	pinned := store.AcceptedSnapshot("http://pinned", SourceDescriptor{})
	reconciled, err := store.ReconcileSource(server.URL, FetchOptions{Critical: true}, nil)
	require.NoError(t, err)
	_, candidate, err := store.PrepareInitialSnapshot(t.Context(), server.URL, reconciled.State)
	require.NoError(t, err)
	require.NotNil(t, candidate)
	store.LoadFixture("http://pinned", "after")

	_, _, err = store.CommitInitialCandidatesAndVerify(
		t.Context(),
		[]*InitialCandidate{candidate},
		[]SnapshotToken{pinned.Token},
	)
	require.ErrorContains(t, err, "changed while the render was running")
	_, accepted := store.GetSource(server.URL, reconciled.State.Descriptor)
	assert.False(t, accepted)
}

func TestNonCriticalFailedEmptyResponseIsNotCacheable(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	defer server.Close()

	store := newRevisionTestStore(0)
	options := FetchOptions{Retries: 1, RetryDelay: time.Nanosecond}
	reconciled, err := store.ReconcileSource(server.URL, options, nil)
	require.NoError(t, err)
	snapshot, candidate, err := store.PrepareInitialSnapshot(t.Context(), server.URL, reconciled.State)
	require.NoError(t, err)
	assert.Nil(t, candidate)
	assert.Empty(t, snapshot.Content)
	assert.False(t, snapshot.Found)
	assert.False(t, snapshot.Cacheable)
	assert.False(t, snapshot.Token.Valid())
}

func TestNonCriticalFailureCannotCrossConcurrentSourceReplacement(t *testing.T) {
	requestStarted := make(chan struct{})
	releaseResponse := make(chan struct{})
	var firstRequest atomic.Bool
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		if firstRequest.CompareAndSwap(false, true) {
			close(requestStarted)
		}
		<-releaseResponse
		w.WriteHeader(http.StatusNotFound)
	}))
	defer server.Close()

	store := newRevisionTestStore(0)
	options := FetchOptions{Retries: 1, RetryDelay: time.Nanosecond}
	reconciled, err := store.ReconcileSource(server.URL, options, nil)
	require.NoError(t, err)
	type prepareResult struct {
		snapshot ContentSnapshot
		err      error
	}
	result := make(chan prepareResult, 1)
	go func() {
		snapshot, _, prepareErr := store.PrepareInitialSnapshot(t.Context(), server.URL, reconciled.State)
		result <- prepareResult{snapshot: snapshot, err: prepareErr}
	}()
	<-requestStarted
	_, err = store.ReconcileSource(server.URL, options, &AuthConfig{Type: AuthTypeBearer, Token: "new"})
	require.NoError(t, err)
	close(releaseResponse)

	prepared := <-result
	require.ErrorContains(t, prepared.err, "changed while it was being fetched")
	assert.False(t, prepared.snapshot.Found)
	assert.False(t, prepared.snapshot.Cacheable)
}

func TestHTTPStoreSourceReplacementAndEvictionInvalidateExactTokens(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(r.Header.Get("Authorization")))
	}))
	defer server.Close()

	store := newRevisionTestStore(time.Second)
	options := FetchOptions{Critical: true}
	oldAuth := &AuthConfig{Type: AuthTypeBearer, Token: "old"}
	newAuth := &AuthConfig{Type: AuthTypeBearer, Token: "new"}
	_, err := store.Fetch(t.Context(), server.URL, options, oldAuth)
	require.NoError(t, err)
	oldDescriptor, err := DescribeSource(options, oldAuth)
	require.NoError(t, err)
	oldIdentity := oldDescriptor.Identity()
	old := store.AcceptedSnapshot(server.URL, oldDescriptor)

	reconciled, err := store.ReconcileSource(server.URL, options, newAuth)
	require.NoError(t, err)
	assert.True(t, reconciled.Changed)
	assert.False(t, store.VerifySnapshots([]SnapshotToken{old.Token}))
	current, changes, complete := store.ChangesSince(old.Watermark)
	require.True(t, complete)
	require.NotEmpty(t, changes)
	assert.Equal(t, oldIdentity, changes[len(changes)-1].PreviousSourceIdentity)
	assert.Equal(t, reconciled.State.Identity, changes[len(changes)-1].SourceIdentity)

	prepared, candidate, err := store.PrepareInitialSnapshot(t.Context(), server.URL, reconciled.State)
	require.NoError(t, err)
	require.True(t, prepared.Cacheable)
	_, _, err = store.CommitInitialCandidatesAndVerify(t.Context(), []*InitialCandidate{candidate}, nil)
	require.NoError(t, err)
	accepted := store.AcceptedSnapshot(server.URL, reconciled.State.Descriptor)
	store.mu.Lock()
	store.cache[server.URL].LastAccessTime = time.Now().Add(-2 * time.Second)
	store.mu.Unlock()
	require.Equal(t, []string{server.URL}, store.EvictUnused())
	assert.False(t, store.VerifySnapshots([]SnapshotToken{accepted.Token}))
	_, evictionChanges, complete := store.ChangesSince(current)
	assert.True(t, complete)
	require.Len(t, evictionChanges, 2)
	assert.True(t, evictionChanges[1].Removed)
}

func TestHTTPStoreChangeJournalFailsClosedAfterOverflow(t *testing.T) {
	store := newRevisionTestStore(0)
	store.semanticJournalCapacity = 2
	store.LoadFixture("http://a", "a")
	first := store.Watermark()
	store.LoadFixture("http://b", "b")
	store.LoadFixture("http://c", "c")

	current, changes, complete := store.ChangesSince(0)
	assert.False(t, complete)
	assert.Empty(t, changes)
	assert.Equal(t, store.Watermark(), current)
	_, changes, complete = store.ChangesSince(first)
	assert.True(t, complete)
	require.Len(t, changes, 2)
	assert.Equal(t, "http://b", changes[0].URL)
	assert.Equal(t, "http://c", changes[1].URL)
}

func BenchmarkHTTPStoreOneChangeAfterHistory(b *testing.B) {
	for _, history := range []int{1, 1000, 4000} {
		b.Run(fmt.Sprintf("history=%d", history), func(b *testing.B) {
			store := newRevisionTestStore(0)
			for index := range history {
				store.LoadFixture(fmt.Sprintf("http://history-%04d", index), "body")
			}
			baseline := store.Watermark()
			store.LoadFixture("http://changed", "body")
			_, changes, complete := store.ChangesSince(baseline)
			require.True(b, complete)
			require.Len(b, changes, 1)

			b.ReportAllocs()
			b.ResetTimer()
			for range b.N {
				_, changes, complete = store.ChangesSince(baseline)
				if !complete || len(changes) != 1 {
					b.Fatalf("one-change suffix is unavailable: complete=%t changes=%d", complete, len(changes))
				}
			}
		})
	}
}

func TestHTTPStoreNegativeObservationDetectsPresentEvictedABA(t *testing.T) {
	store := newRevisionTestStore(time.Second)
	url := "http://exact-negative"
	missing := store.AcceptedSnapshot(url, SourceDescriptor{})
	missingToken := missing.ObservationToken()
	require.True(t, missingToken.Valid())

	store.LoadFixture(url, "present")
	store.mu.Lock()
	store.cache[url].LastAccessTime = time.Now().Add(-2 * time.Second)
	store.mu.Unlock()
	require.Equal(t, []string{url}, store.EvictUnused())

	assert.False(t, store.VerifyObservations([]ObservationToken{missing.ObservationToken()}))
	current := store.AcceptedSnapshot(url, SourceDescriptor{})
	assert.False(t, current.Found)
	assert.True(t, store.VerifyObservations([]ObservationToken{current.ObservationToken()}))
}

func TestHTTPStoreNegativeObservationFailsClosedAcrossJournalGap(t *testing.T) {
	store := newRevisionTestStore(0)
	store.semanticJournalCapacity = 2
	missing := store.AcceptedSnapshot("http://missing", SourceDescriptor{})
	store.LoadFixture("http://unrelated-a", "a")
	store.LoadFixture("http://unrelated-b", "b")
	store.LoadFixture("http://unrelated-c", "c")

	assert.False(t, store.VerifyObservations([]ObservationToken{missing.ObservationToken()}))
}

func TestHTTPStorePresentObservationDoesNotDependOnUnrelatedJournalHistory(t *testing.T) {
	store := newRevisionTestStore(0)
	store.semanticJournalCapacity = 2
	store.LoadFixture("http://present", "value")
	present := store.AcceptedSnapshot("http://present", SourceDescriptor{})
	store.LoadFixture("http://unrelated-a", "a")
	store.LoadFixture("http://unrelated-b", "b")
	store.LoadFixture("http://unrelated-c", "c")

	assert.True(t, store.VerifyObservations([]ObservationToken{present.ObservationToken()}))
	other := newRevisionTestStore(0)
	other.LoadFixture("http://present", "value")
	assert.False(t, other.VerifyObservations([]ObservationToken{present.ObservationToken()}))
}

func TestHTTPStoreDescriptorRetentionIsBoundedByLiveCacheAndJournal(t *testing.T) {
	store := newRevisionTestStore(0)
	store.semanticJournalCapacity = 3
	url := "https://example.test/credentials"
	for index := range 100 {
		_, err := store.ReconcileSource(url, FetchOptions{}, &AuthConfig{
			Type:  AuthTypeBearer,
			Token: fmt.Sprintf("credential-%03d", index),
		})
		require.NoError(t, err)
	}

	store.mu.RLock()
	defer store.mu.RUnlock()
	assert.Len(t, store.cache, 1)
	assert.Len(t, store.semanticJournal, store.semanticJournalCapacity)
	retained := map[SourceDescriptor]struct{}{store.cache[url].sourceDescriptor: {}}
	for _, change := range store.semanticJournal {
		retained[change.PreviousDescriptor] = struct{}{}
		retained[change.Descriptor] = struct{}{}
	}
	assert.LessOrEqual(t, len(retained), 1+2*store.semanticJournalCapacity)
}

func TestHTTPOverlayFreezesPendingAndAcceptedFallbacks(t *testing.T) {
	store := newRevisionTestStore(0)
	store.LoadFixture("http://accepted", "old")
	store.LoadFixture("http://pending", "accepted")
	store.mu.Lock()
	entry := store.cache["http://pending"]
	store.nextPendingRevision++
	entry.PendingContent = "pending"
	entry.PendingChecksum = checksum("pending")
	entry.PendingRevision = store.nextPendingRevision
	entry.HasPending = true
	store.mu.Unlock()

	overlay := NewHTTPOverlay(store)
	acceptedBefore := overlay.Snapshot("http://accepted", SourceDescriptor{})
	pendingBefore := overlay.Snapshot("http://pending", SourceDescriptor{})
	require.True(t, acceptedBefore.Cacheable)
	require.True(t, pendingBefore.Cacheable)
	store.LoadFixture("http://accepted", "new")
	require.True(t, store.RejectPending("http://pending"))
	store.LoadFixture("http://late", "late")

	assert.Equal(t, acceptedBefore, overlay.Snapshot("http://accepted", SourceDescriptor{}))
	assert.Equal(t, pendingBefore, overlay.Snapshot("http://pending", SourceDescriptor{}))
	_, found := overlay.GetContent("http://late")
	assert.False(t, found)
}

func newRevisionTestStore(maxAge time.Duration) *HTTPStore {
	return New(slog.New(slog.NewTextHandler(io.Discard, nil)), maxAge)
}

func promoteCurrentBody(t *testing.T, store *HTTPStore, url string) {
	t.Helper()
	version, err := store.RefreshURLVersion(t.Context(), url)
	require.NoError(t, err)
	require.NotNil(t, version)
	require.True(t, store.PromotePendingVersion(url, version.Checksum, version.Revision))
}
