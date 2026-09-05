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
	"fmt"
	"log/slog"
	"slices"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestActiveLeasesTrackOnlyRelevantSemanticChanges(t *testing.T) {
	store := New(slog.Default(), 0)
	store.semanticJournalCapacity = 2
	store.LoadFixture("https://active.test/value", "a")
	set, token, err := store.NewActiveLeaseSet()
	require.NoError(t, err)
	token = commitActiveLeaseReplacement(t, store, set, token, []ActiveLeaseReference{{
		URL: "https://active.test/value", References: 1,
	}})

	for index := range 20 {
		store.LoadFixture(fmt.Sprintf("https://unrelated.test/%d", index), fmt.Sprint(index))
	}
	snapshot, err := set.BeginActiveLeases(token)
	require.NoError(t, err)
	assert.Empty(t, snapshot.Changes())

	store.LoadFixture("https://active.test/value", "b")
	store.LoadFixture("https://active.test/value", "a")
	snapshot, err = set.BeginActiveLeases(token)
	require.NoError(t, err)
	require.Len(t, snapshot.Changes(), 1)
	assert.Equal(t, "https://active.test/value", snapshot.Changes()[0].URL)
	assert.Equal(t, ActiveLeaseRevision(2), snapshot.Changes()[0].Revision)
}

func TestActiveLeaseSourceReplacementDirtiesPreviousDeclaration(t *testing.T) {
	store := New(slog.Default(), 0)
	url := "https://active.test/value"
	first, err := store.ReconcileSource(url, FetchOptions{}, nil)
	require.NoError(t, err)
	set, token, err := store.NewActiveLeaseSet()
	require.NoError(t, err)
	token = commitActiveLeaseReplacement(t, store, set, token, []ActiveLeaseReference{{
		URL: url, Descriptor: first.State.Descriptor, References: 1,
	}})

	_, err = store.ReconcileSource(url, FetchOptions{}, &AuthConfig{Type: AuthTypeBearer, Token: "next"})
	require.NoError(t, err)
	snapshot, err := set.BeginActiveLeases(token)
	require.NoError(t, err)
	require.Len(t, snapshot.Changes(), 1)
	assert.Equal(t, first.State.Descriptor, snapshot.Changes()[0].Descriptor)
}

func TestActiveLeaseChangeOrderUsesExactDescriptorBeyondDiagnosticDigest(t *testing.T) {
	first := SourceDescriptor{identity: "forced-collision", canonical: "first"}
	second := SourceDescriptor{identity: "forced-collision", canonical: "second"}
	changes := []ActiveLeaseChange{
		{URL: "https://active.test/value", Descriptor: second},
		{URL: "https://active.test/value", Descriptor: first},
	}

	slices.SortFunc(changes, compareActiveLeaseChanges)

	assert.Equal(t, first, changes[0].Descriptor)
	assert.Equal(t, second, changes[1].Descriptor)
}

func TestActiveLeaseTracksMissingPresentAndPresentMissing(t *testing.T) {
	store := New(slog.Default(), 0)
	url := "https://active.test/value"
	set, token, err := store.NewActiveLeaseSet()
	require.NoError(t, err)
	token = commitActiveLeaseReplacement(t, store, set, token, []ActiveLeaseReference{{
		URL: url, References: 1,
	}})

	store.LoadFixture(url, "present")
	snapshot, err := set.BeginActiveLeases(token)
	require.NoError(t, err)
	require.Len(t, snapshot.Changes(), 1)
	assert.Equal(t, ActiveLeaseRevision(1), snapshot.Changes()[0].Revision)
	assert.True(t, store.AcceptedSnapshot(url, SourceDescriptor{}).Found)
	token = publishActiveLeaseCommit(t, store, &ActiveLeaseCommit{Snapshot: snapshot})

	_, err = store.ReconcileSource(
		url,
		FetchOptions{},
		&AuthConfig{Type: AuthTypeBearer, Token: "replacement"},
	)
	require.NoError(t, err)
	snapshot, err = set.BeginActiveLeases(token)
	require.NoError(t, err)
	require.Len(t, snapshot.Changes(), 1)
	assert.Equal(t, ActiveLeaseRevision(2), snapshot.Changes()[0].Revision)
	assert.False(t, store.AcceptedSnapshot(url, SourceDescriptor{}).Found)
}

func TestActiveLeasePrepareRejectsLateRelevantChangeAndPermitsUnrelated(t *testing.T) {
	store := New(slog.Default(), 0)
	store.LoadFixture("https://active.test/value", "a")
	set, token, err := store.NewActiveLeaseSet()
	require.NoError(t, err)
	token = commitActiveLeaseReplacement(t, store, set, token, []ActiveLeaseReference{{
		URL: "https://active.test/value", References: 1,
	}})

	snapshot, err := set.BeginActiveLeases(token)
	require.NoError(t, err)
	store.LoadFixture("https://unrelated.test/value", "b")
	prepared, err := store.PrepareStagedSourcesAndVerifyObservationSetsWithActiveLeases(
		t.Context(), nil, nil, nil, nil, &ActiveLeaseCommit{Snapshot: snapshot},
	)
	require.NoError(t, err)
	prepared.Abort()

	snapshot, err = set.BeginActiveLeases(token)
	require.NoError(t, err)
	store.LoadFixture("https://active.test/value", "c")
	_, err = store.PrepareStagedSourcesAndVerifyObservationSetsWithActiveLeases(
		t.Context(), nil, nil, nil, nil, &ActiveLeaseCommit{Snapshot: snapshot},
	)
	assert.ErrorContains(t, err, "changed while the render was running")
}

func TestActiveLeaseAbortAndTokenAuthentication(t *testing.T) {
	store := New(slog.Default(), 0)
	set, token, err := store.NewActiveLeaseSet()
	require.NoError(t, err)
	snapshot, err := set.BeginActiveLeases(token)
	require.NoError(t, err)
	prepared, err := store.PrepareStagedSourcesAndVerifyObservationSetsWithActiveLeases(
		t.Context(), nil, nil, nil, nil, &ActiveLeaseCommit{
			Snapshot: snapshot,
			Updates:  []ActiveLeaseUpdate{{URL: "https://active.test/value", Added: 1}},
		},
	)
	require.NoError(t, err)
	planned, _, ok := prepared.PlannedActiveLeases()
	require.True(t, ok)
	prepared.Abort()
	_, err = set.BeginActiveLeases(planned)
	assert.ErrorContains(t, err, "current empty root")
	_, err = set.BeginActiveLeases(token)
	require.NoError(t, err)

	token = commitActiveLeaseUpdates(t, store, set, token, []ActiveLeaseUpdate{{
		URL: "https://active.test/value", Added: 1,
	}})
	_, err = set.BeginActiveLeases(token)
	require.NoError(t, err)
	_, err = set.BeginActiveLeases(snapshot.Token())
	assert.Error(t, err)
	other, otherToken, err := store.NewActiveLeaseSet()
	require.NoError(t, err)
	_, err = set.BeginActiveLeases(otherToken)
	assert.ErrorContains(t, err, "another authority")
	_, err = other.BeginActiveLeases(token)
	assert.ErrorContains(t, err, "another authority")
}

func TestActiveLeaseRejectsConflictingDescriptors(t *testing.T) {
	store := New(slog.Default(), 0)
	first, err := DescribeSource(FetchOptions{}, nil)
	require.NoError(t, err)
	second, err := DescribeSource(FetchOptions{}, &AuthConfig{Type: AuthTypeBearer, Token: "second"})
	require.NoError(t, err)
	set, token, err := store.NewActiveLeaseSet()
	require.NoError(t, err)
	snapshot, err := set.BeginActiveLeases(token)
	require.NoError(t, err)
	_, err = store.PrepareStagedSourcesAndVerifyObservationSetsWithActiveLeases(
		t.Context(), nil, nil, nil, nil, &ActiveLeaseCommit{
			Snapshot: snapshot,
			Updates: []ActiveLeaseUpdate{
				{URL: "https://active.test/value", Descriptor: first, Added: 1},
				{URL: "https://active.test/value", Descriptor: second, Added: 1},
			},
		},
	)
	assert.ErrorContains(t, err, "conflicting active declarations")
}

func TestActiveLeaseRejectsConflictingDescriptorsAcrossLeaseSets(t *testing.T) {
	store := New(slog.Default(), 0)
	firstDescriptor, err := DescribeSource(FetchOptions{}, nil)
	require.NoError(t, err)
	secondDescriptor, err := DescribeSource(
		FetchOptions{},
		&AuthConfig{Type: AuthTypeBearer, Token: "second"},
	)
	require.NoError(t, err)
	first, firstToken, err := store.NewActiveLeaseSet()
	require.NoError(t, err)
	_ = commitActiveLeaseUpdates(t, store, first, firstToken, []ActiveLeaseUpdate{{
		URL: "https://active.test/value", Descriptor: firstDescriptor, Added: 1,
	}})
	second, secondToken, err := store.NewActiveLeaseSet()
	require.NoError(t, err)
	snapshot, err := second.BeginActiveLeases(secondToken)
	require.NoError(t, err)
	_, err = store.PrepareStagedSourcesAndVerifyObservationSetsWithActiveLeases(
		t.Context(), nil, nil, nil, nil, &ActiveLeaseCommit{
			Snapshot: snapshot,
			Updates: []ActiveLeaseUpdate{{
				URL: "https://active.test/value", Descriptor: secondDescriptor, Added: 1,
			}},
		},
	)
	assert.ErrorContains(t, err, "conflicting active declarations")
	_, err = second.BeginActiveLeases(secondToken)
	require.NoError(t, err)
}

func TestActiveLeaseProtectsEvictionUntilLastReferenceRetires(t *testing.T) {
	store := New(slog.Default(), -time.Hour)
	url := "https://active.test/value"
	store.LoadFixture(url, "value")
	set, token, err := store.NewActiveLeaseSet()
	require.NoError(t, err)
	token = commitActiveLeaseUpdates(t, store, set, token, []ActiveLeaseUpdate{{URL: url, Added: 2}})
	assert.Empty(t, store.EvictUnused())
	token = commitActiveLeaseUpdates(t, store, set, token, []ActiveLeaseUpdate{{URL: url, Removed: 1}})
	assert.Empty(t, store.EvictUnused())
	token = commitActiveLeaseUpdates(t, store, set, token, []ActiveLeaseUpdate{{URL: url, Removed: 1}})
	assert.Equal(t, []string{url}, store.EvictUnused())
	_, err = set.BeginActiveLeases(token)
	require.NoError(t, err)
}

func TestActiveLeaseRetirementUnregistersEveryURL(t *testing.T) {
	store := New(slog.Default(), 0)
	set, token, err := store.NewActiveLeaseSet()
	require.NoError(t, err)
	references := make([]ActiveLeaseReference, 128)
	for index := range references {
		references[index] = ActiveLeaseReference{
			URL: fmt.Sprintf("https://active.test/%03d", index), References: 1,
		}
	}
	token = commitActiveLeaseReplacement(t, store, set, token, references)
	retired, err := set.RetireActiveLeases(token)
	require.NoError(t, err)
	assert.Len(t, retired, len(references))
	for _, reference := range references {
		assert.False(t, store.HasActiveLease(reference.URL))
	}
}

func TestActiveLeaseAcceptedReplayAbortRetirementAndReacquisition(t *testing.T) {
	store := New(slog.Default(), 0)
	const url = "https://active.test/replay"
	store.LoadFixture(url, "A")
	replay := captureFixtureReplayState(t, store, url)
	set, token, err := store.NewActiveLeaseSet()
	require.NoError(t, err)

	snapshot, err := set.BeginActiveLeases(token)
	require.NoError(t, err)
	prepared, err := store.PrepareStagedSourcesAndVerifyObservationSetsWithActiveLeases(
		t.Context(), nil, nil, nil, nil, &ActiveLeaseCommit{Snapshot: snapshot, Replay: replay},
	)
	require.NoError(t, err)
	planned, _, ok := prepared.PlannedActiveLeases()
	require.True(t, ok)
	prepared.Abort()
	assert.False(t, store.HasActiveLease(url))
	_, err = set.BeginActiveLeases(planned)
	assert.Error(t, err)
	_, err = set.BeginActiveLeases(token)
	require.NoError(t, err)

	snapshot, err = set.BeginActiveLeases(token)
	require.NoError(t, err)
	token = publishActiveLeaseCommit(t, store, &ActiveLeaseCommit{Snapshot: snapshot, Replay: replay})
	assert.True(t, store.HasActiveLease(url))

	store.LoadFixture("https://unrelated.test/replay", "A")
	store.LoadFixture("https://unrelated.test/replay", "B")
	store.LoadFixture("https://unrelated.test/replay", "A")
	advanced, ok := store.AdvanceAcceptedReplayState(replay)
	require.True(t, ok)
	snapshot, err = set.BeginActiveLeases(token)
	require.NoError(t, err)
	assert.Empty(t, snapshot.Changes())
	token = publishActiveLeaseCommit(t, store, &ActiveLeaseCommit{Snapshot: snapshot, Replay: advanced})
	assert.True(t, store.HasActiveLease(url))

	store.LoadFixture(url, "B")
	snapshot, err = set.BeginActiveLeases(token)
	require.NoError(t, err)
	require.Len(t, snapshot.Changes(), 1)
	_, err = store.PrepareStagedSourcesAndVerifyObservationSetsWithActiveLeases(
		t.Context(), nil, nil, nil, nil, &ActiveLeaseCommit{Snapshot: snapshot, Replay: advanced},
	)
	assert.ErrorContains(t, err, "accepted HTTP replay lease changed")

	replay = captureFixtureReplayState(t, store, url)
	token = publishActiveLeaseCommit(t, store, &ActiveLeaseCommit{Snapshot: snapshot, Replay: replay})
	assert.True(t, store.HasActiveLease(url))
	retired, err := set.RetireActiveLeases(token)
	require.NoError(t, err)
	assert.Equal(t, []string{url}, retired)
	assert.False(t, store.HasActiveLease(url))

	assertActiveLeaseReacquisition(t, store, url, replay)
}

func assertActiveLeaseReacquisition(
	t *testing.T,
	store *HTTPStore,
	url string,
	replay *AcceptedReplayState,
) {
	t.Helper()
	reacquiredSet, reacquiredToken, err := store.NewActiveLeaseSet()
	require.NoError(t, err)
	reacquiredSnapshot, err := reacquiredSet.BeginActiveLeases(reacquiredToken)
	require.NoError(t, err)
	reacquiredToken = publishActiveLeaseCommit(t, store, &ActiveLeaseCommit{
		Snapshot: reacquiredSnapshot,
		Replay:   replay,
	})
	assert.True(t, store.HasActiveLease(url))
	_, err = reacquiredSet.RetireActiveLeases(reacquiredToken)
	require.NoError(t, err)
	assert.False(t, store.HasActiveLease(url))
}

func TestActiveLeaseConcurrentReplayRebaseUsesAuthenticatedCurrentCursor(t *testing.T) {
	store := New(slog.Default(), 0)
	store.replayJournalCapacity = 2
	const target = "https://active.test/replay"
	store.LoadFixture(target, "target")
	replay := captureFixtureReplayState(t, store, target)
	set, token, err := store.NewActiveLeaseSet()
	require.NoError(t, err)
	snapshot, err := set.BeginActiveLeases(token)
	require.NoError(t, err)
	token = publishActiveLeaseCommit(t, store, &ActiveLeaseCommit{Snapshot: snapshot, Replay: replay})

	stale, err := set.BeginActiveLeases(token)
	require.NoError(t, err)
	first, err := set.BeginActiveLeases(token)
	require.NoError(t, err)
	store.LoadFixture("https://unrelated.test/first", "A")
	store.LoadFixture("https://unrelated.test/second", "A")
	advanced, ok := store.AdvanceAcceptedReplayState(replay)
	require.True(t, ok)
	firstToken := publishActiveLeaseCommit(t, store, &ActiveLeaseCommit{
		Snapshot: first,
		Replay:   advanced,
	})
	assert.Equal(t, token, firstToken)

	store.LoadFixture("https://unrelated.test/third", "A")
	store.LoadFixture("https://unrelated.test/fourth", "A")
	_, ok = store.AdvanceAcceptedReplayState(replay)
	require.False(t, ok)
	rebasedToken := publishActiveLeaseCommit(t, store, &ActiveLeaseCommit{
		Snapshot: stale,
		Replay:   replay,
	})
	assert.Equal(t, token, rebasedToken)
	assert.True(t, store.HasActiveLease(target))
	current, err := set.BeginActiveLeases(rebasedToken)
	require.NoError(t, err)
	assert.Empty(t, current.Changes())
}

func commitActiveLeaseReplacement(
	t *testing.T,
	store *HTTPStore,
	set *ActiveLeaseSet,
	token ActiveLeaseToken,
	replacement []ActiveLeaseReference,
) ActiveLeaseToken {
	t.Helper()
	snapshot, err := set.BeginActiveLeases(token)
	require.NoError(t, err)
	return publishActiveLeaseCommit(t, store, &ActiveLeaseCommit{
		Snapshot: snapshot, Replacement: replacement, Replace: true,
	})
}

func commitActiveLeaseUpdates(
	t *testing.T,
	store *HTTPStore,
	set *ActiveLeaseSet,
	token ActiveLeaseToken,
	updates []ActiveLeaseUpdate,
) ActiveLeaseToken {
	t.Helper()
	snapshot, err := set.BeginActiveLeases(token)
	require.NoError(t, err)
	return publishActiveLeaseCommit(t, store, &ActiveLeaseCommit{Snapshot: snapshot, Updates: updates})
}

func publishActiveLeaseCommit(
	t *testing.T,
	store *HTTPStore,
	active *ActiveLeaseCommit,
) ActiveLeaseToken {
	t.Helper()
	prepared, err := store.PrepareStagedSourcesAndVerifyObservationSetsWithActiveLeases(
		t.Context(), nil, nil, nil, nil, active,
	)
	require.NoError(t, err)
	token, _, ok := prepared.PlannedActiveLeases()
	require.True(t, ok)
	prepared.Publish()
	prepared.Release()
	return token
}

func BenchmarkActiveLeaseBeginNoChange(b *testing.B) {
	for _, count := range []int{1, 128, 8192} {
		b.Run(fmt.Sprint(count), func(b *testing.B) {
			store := New(slog.Default(), 0)
			set, token, err := store.NewActiveLeaseSet()
			require.NoError(b, err)
			references := make([]ActiveLeaseReference, count)
			for index := range references {
				references[index] = ActiveLeaseReference{
					URL: fmt.Sprintf("https://active.test/%05d", index), References: 1,
				}
			}
			token = benchmarkCommitActiveLeases(b, store, set, token, references)
			b.ReportAllocs()
			b.ResetTimer()
			for range b.N {
				snapshot, beginErr := set.BeginActiveLeases(token)
				if beginErr != nil || len(snapshot.Changes()) != 0 {
					b.Fatalf("begin active leases: %v", beginErr)
				}
			}
		})
	}
}

func BenchmarkActiveLeasePrepareNoChange(b *testing.B) {
	for _, count := range []int{1, 128, 8192} {
		b.Run(fmt.Sprint(count), func(b *testing.B) {
			store, set, token := benchmarkActiveLeaseSet(b, count)
			b.ReportAllocs()
			b.ResetTimer()
			for range b.N {
				snapshot, err := set.BeginActiveLeases(token)
				if err != nil {
					b.Fatal(err)
				}
				prepared, err := store.PrepareStagedSourcesAndVerifyObservationSetsWithActiveLeases(
					b.Context(), nil, nil, nil, nil, &ActiveLeaseCommit{Snapshot: snapshot},
				)
				if err != nil {
					b.Fatal(err)
				}
				prepared.Abort()
			}
		})
	}
}

func BenchmarkActiveLeaseOneChange(b *testing.B) {
	for _, count := range []int{1, 128, 8192} {
		b.Run(fmt.Sprint(count), func(b *testing.B) {
			store, set, token := benchmarkActiveLeaseSet(b, count)
			url := "https://active.test/00000"
			b.ReportAllocs()
			b.ResetTimer()
			for index := range b.N {
				store.LoadFixture(url, fmt.Sprint(index&1))
				snapshot, err := set.BeginActiveLeases(token)
				if err != nil || len(snapshot.Changes()) != 1 {
					b.Fatalf("begin active lease change: %v", err)
				}
				prepared, err := store.PrepareStagedSourcesAndVerifyObservationSetsWithActiveLeases(
					b.Context(), nil, nil, nil, nil, &ActiveLeaseCommit{Snapshot: snapshot},
				)
				if err != nil {
					b.Fatal(err)
				}
				var planned bool
				token, _, planned = prepared.PlannedActiveLeases()
				if !planned {
					b.Fatal("active lease change had no publication token")
				}
				prepared.Publish()
				prepared.Release()
			}
		})
	}
}

func benchmarkActiveLeaseSet(
	b *testing.B,
	count int,
) (*HTTPStore, *ActiveLeaseSet, ActiveLeaseToken) {
	b.Helper()
	store := New(slog.Default(), 0)
	set, token, err := store.NewActiveLeaseSet()
	require.NoError(b, err)
	references := make([]ActiveLeaseReference, count)
	for index := range references {
		references[index] = ActiveLeaseReference{
			URL: fmt.Sprintf("https://active.test/%05d", index), References: 1,
		}
	}
	token = benchmarkCommitActiveLeases(b, store, set, token, references)
	return store, set, token
}

func benchmarkCommitActiveLeases(
	b *testing.B,
	store *HTTPStore,
	set *ActiveLeaseSet,
	token ActiveLeaseToken,
	references []ActiveLeaseReference,
) ActiveLeaseToken {
	b.Helper()
	snapshot, err := set.BeginActiveLeases(token)
	require.NoError(b, err)
	prepared, err := store.PrepareStagedSourcesAndVerifyObservationSetsWithActiveLeases(
		b.Context(), nil, nil, nil, nil, &ActiveLeaseCommit{
			Snapshot: snapshot, Replacement: references, Replace: true,
		},
	)
	require.NoError(b, err)
	token, _, ok := prepared.PlannedActiveLeases()
	require.True(b, ok)
	prepared.Publish()
	prepared.Release()
	return token
}

// A cold render replaces the whole reference set, and its replacement carries
// only that render's own references -- never the accepted replay's, which the
// same tree also counts. Dropping the replay in the same commit then removed a
// reference the replacement never held: "active reference count is
// inconsistent", which failed every render until the next cold one. On a live
// cluster this fired 30 times in one e2e run, right after the renders that
// forced the cold start.
func TestActiveLeaseReplacementDropsAReplayItNeverHeld(t *testing.T) {
	store := New(slog.Default(), 0)
	const target = "https://active.test/replaced-replay"
	store.LoadFixture(target, "target")
	replay := captureFixtureReplayState(t, store, target)

	set, token, err := store.NewActiveLeaseSet()
	require.NoError(t, err)
	snapshot, err := set.BeginActiveLeases(token)
	require.NoError(t, err)
	token = publishActiveLeaseCommit(t, store, &ActiveLeaseCommit{Snapshot: snapshot, Replay: replay})
	require.True(t, store.HasActiveLease(target))

	token = commitActiveLeaseReplacement(t, store, set, token, nil)
	assert.False(t, store.HasActiveLease(target), "the replay's reference is gone with the replay")

	_, err = set.RetireActiveLeases(token)
	require.NoError(t, err)
}

// The replacement must keep a replay the commit carries forward, since the
// replay's references live in the tree the replacement rebuilt.
func TestActiveLeaseReplacementKeepsTheReplayItCarriesForward(t *testing.T) {
	store := New(slog.Default(), 0)
	const target = "https://active.test/kept-replay"
	store.LoadFixture(target, "target")
	replay := captureFixtureReplayState(t, store, target)

	set, token, err := store.NewActiveLeaseSet()
	require.NoError(t, err)
	snapshot, err := set.BeginActiveLeases(token)
	require.NoError(t, err)
	token = publishActiveLeaseCommit(t, store, &ActiveLeaseCommit{Snapshot: snapshot, Replay: replay})
	require.True(t, store.HasActiveLease(target))

	replaced, err := set.BeginActiveLeases(token)
	require.NoError(t, err)
	token = publishActiveLeaseCommit(t, store, &ActiveLeaseCommit{
		Snapshot: replaced, Replacement: nil, Replace: true, Replay: replay,
	})
	assert.True(t, store.HasActiveLease(target), "the replay still holds its source")

	_, err = set.RetireActiveLeases(token)
	require.NoError(t, err)
	assert.False(t, store.HasActiveLease(target))
}
