// Copyright 2026 Philipp Hossner
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
// http://www.apache.org/licenses/LICENSE-2.0
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
	"sync"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"gitlab.com/haproxy-haptic/haptic/pkg/controller/testutil"
	purehttpstore "gitlab.com/haproxy-haptic/haptic/pkg/httpstore"
)

func TestReplaySnapshotIsIdempotentForExactAcceptedReads(t *testing.T) {
	component, snapshots := acceptedReplaySnapshots(t, t.Context(), 2)
	wrapper := NewHTTPStoreWrapper(t.Context(), component, component.logger, nil, SourceModeAuthoritative)
	enrolled := make([]purehttpstore.ContentSnapshot, len(snapshots))
	for index := range snapshots {
		current, replayed, err := wrapper.ReplaySnapshot(&snapshots[index])
		require.NoError(t, err)
		require.True(t, replayed)
		assert.True(t, sameObservedHTTPSnapshot(&snapshots[index], &current))
		enrolled[index] = current
	}

	watermarkOnly := enrolled[0]
	watermarkOnly.Watermark++
	current, replayed, err := wrapper.ReplaySnapshot(&watermarkOnly)
	require.NoError(t, err)
	require.True(t, replayed)
	assert.Equal(t, enrolled[0], current)

	stored := wrapper.InputTransaction().Snapshots()
	require.Len(t, stored, len(snapshots))
	assert.ElementsMatch(t, enrolled, stored)
}

func TestReplaySnapshotCanonicalizesUntrustedSnapshotFields(t *testing.T) {
	component, snapshots := acceptedReplaySnapshots(t, t.Context(), 1)
	wrapper := NewHTTPStoreWrapper(t.Context(), component, component.logger, nil, SourceModeAuthoritative)
	original := snapshots[0]
	_, replayed, err := wrapper.ReplaySnapshot(&original)
	require.NoError(t, err)
	require.True(t, replayed)

	tests := map[string]func(*purehttpstore.ContentSnapshot){
		"content": func(snapshot *purehttpstore.ContentSnapshot) {
			snapshot.Content = "poison"
		},
		"observation": func(snapshot *purehttpstore.ContentSnapshot) {
			snapshot.Observation++
		},
		"store source": func(snapshot *purehttpstore.ContentSnapshot) {
			snapshot.StoreSource++
		},
	}
	for name, mutate := range tests {
		t.Run(name, func(t *testing.T) {
			changed := original
			mutate(&changed)
			current, ok, replayErr := wrapper.ReplaySnapshot(&changed)
			require.NoError(t, replayErr)
			require.True(t, ok)
			assert.Equal(t, original, current)
			assert.Equal(t, []purehttpstore.ContentSnapshot{original}, wrapper.InputTransaction().Snapshots())
		})
	}
}

func TestReplaySnapshotRejectsInvalidAcceptedIdentityAfterEnrollment(t *testing.T) {
	component, snapshots := acceptedReplaySnapshots(t, t.Context(), 1)
	wrapper := NewHTTPStoreWrapper(t.Context(), component, component.logger, nil, SourceModeAuthoritative)
	original := snapshots[0]
	_, replayed, err := wrapper.ReplaySnapshot(&original)
	require.NoError(t, err)
	require.True(t, replayed)

	otherDescriptor, err := purehttpstore.DescribeSource(
		purehttpstore.FetchOptions{},
		&purehttpstore.AuthConfig{Type: purehttpstore.AuthTypeBearer, Token: "other"},
	)
	require.NoError(t, err)
	tests := map[string]func(*purehttpstore.ContentSnapshot){
		"missing": func(snapshot *purehttpstore.ContentSnapshot) {
			snapshot.Found = false
		},
		"non-cacheable": func(snapshot *purehttpstore.ContentSnapshot) {
			snapshot.Cacheable = false
		},
		"invalid token": func(snapshot *purehttpstore.ContentSnapshot) {
			snapshot.Token = purehttpstore.SnapshotToken{}
		},
		"different URL": func(snapshot *purehttpstore.ContentSnapshot) {
			snapshot.URL += "/other"
		},
		"different descriptor": func(snapshot *purehttpstore.ContentSnapshot) {
			snapshot.Descriptor = otherDescriptor
		},
	}
	for name, mutate := range tests {
		t.Run(name, func(t *testing.T) {
			changed := original
			mutate(&changed)
			_, ok, replayErr := wrapper.ReplaySnapshot(&changed)
			require.Error(t, replayErr)
			assert.False(t, ok)
			assert.Equal(t, []purehttpstore.ContentSnapshot{original}, wrapper.InputTransaction().Snapshots())
		})
	}
}

func TestReplaySnapshotConcurrentIdempotenceRetainsOneReadPerURL(t *testing.T) {
	const count = 64
	component, snapshots := acceptedReplaySnapshots(t, t.Context(), count)
	wrapper := NewHTTPStoreWrapper(t.Context(), component, component.logger, nil, SourceModeAuthoritative)

	var wait sync.WaitGroup
	errs := make(chan error, count*4)
	for worker := range 4 {
		wait.Add(1)
		go func(offset int) {
			defer wait.Done()
			for index := range snapshots {
				snapshot := &snapshots[(index+offset)%len(snapshots)]
				current, replayed, err := wrapper.ReplaySnapshot(snapshot)
				if err != nil {
					errs <- err
					continue
				}
				if !replayed || !sameObservedHTTPSnapshot(&current, snapshot) {
					errs <- fmt.Errorf("replay for %s returned another snapshot", snapshot.URL)
				}
			}
		}(worker)
	}
	wait.Wait()
	close(errs)
	for err := range errs {
		require.NoError(t, err)
	}
	stored := wrapper.InputTransaction().Snapshots()
	require.Len(t, stored, len(snapshots))
	for index := range snapshots {
		assert.True(t, sameObservedHTTPSnapshot(&snapshots[index], &stored[index]))
	}
}

func TestReplaySnapshotObservedBeforeReplacementCannotPoisonCommit(t *testing.T) {
	var body atomic.Value
	body.Store("first")
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(body.Load().(string)))
	}))
	t.Cleanup(server.Close)

	bus, logger := testutil.NewTestBusAndLogger()
	component := New(bus, logger, 0)
	options := purehttpstore.FetchOptions{Critical: true}
	_, err := component.store.Fetch(t.Context(), server.URL, options, nil)
	require.NoError(t, err)
	descriptor, err := purehttpstore.DescribeSource(options, nil)
	require.NoError(t, err)
	first := component.store.AcceptedSnapshot(server.URL, descriptor)
	require.True(t, first.Found)

	wrapper := NewHTTPStoreWrapper(t.Context(), component, logger, nil, SourceModeAuthoritative)
	_, replayed, err := wrapper.ReplaySnapshot(&first)
	require.NoError(t, err)
	require.True(t, replayed)

	body.Store("second")
	pending, err := component.store.RefreshURLVersion(t.Context(), server.URL)
	require.NoError(t, err)
	require.NotNil(t, pending)
	require.True(t, component.store.PromotePendingVersion(server.URL, pending.Checksum, pending.Revision))
	second := component.store.AcceptedSnapshot(server.URL, descriptor)
	require.True(t, second.Found)
	require.NotEqual(t, first.Token, second.Token)

	_, replayed, err = wrapper.ReplaySnapshot(&second)
	require.ErrorContains(t, err, "changed within one render")
	assert.False(t, replayed)
	assert.Equal(t, []purehttpstore.ContentSnapshot{first}, wrapper.InputTransaction().Snapshots())
	require.ErrorContains(t, wrapper.InputTransaction().Commit(t.Context()), "changed while the render was running")
}

func BenchmarkReplaySnapshotManyDistinctURLs(b *testing.B) {
	component, snapshots := acceptedReplaySnapshots(b, b.Context(), 1000)
	for _, count := range []int{10, 100, 1000} {
		b.Run(fmt.Sprintf("urls=%d", count), func(b *testing.B) {
			wrapper := NewHTTPStoreWrapper(b.Context(), component, component.logger, nil, SourceModeAuthoritative)
			for index := range count {
				_, replayed, err := wrapper.ReplaySnapshot(&snapshots[index])
				require.NoError(b, err)
				require.True(b, replayed)
			}
			b.Cleanup(wrapper.InputTransaction().Abort)
			b.ReportAllocs()
			b.ResetTimer()
			for b.Loop() {
				for index := range count {
					_, replayed, err := wrapper.ReplaySnapshot(&snapshots[index])
					if err != nil || !replayed {
						b.Fatalf("replaying %q: replayed=%t error=%v", snapshots[index].URL, replayed, err)
					}
				}
			}
			b.ReportMetric(float64(count), "urls/op")
		})
	}
}

func acceptedReplaySnapshots(
	tb testing.TB,
	ctx context.Context,
	count int,
) (*Component, []purehttpstore.ContentSnapshot) {
	tb.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		_, _ = w.Write([]byte(request.URL.Path))
	}))
	tb.Cleanup(server.Close)
	bus, logger := testutil.NewTestBusAndLogger()
	component := New(bus, logger, 0)
	options := purehttpstore.FetchOptions{Critical: true}
	descriptor, err := purehttpstore.DescribeSource(options, nil)
	require.NoError(tb, err)
	snapshots := make([]purehttpstore.ContentSnapshot, count)
	for index := range count {
		url := fmt.Sprintf("%s/%06d", server.URL, index)
		_, err = component.store.Fetch(ctx, url, options, nil)
		require.NoError(tb, err)
		snapshots[index] = component.store.AcceptedSnapshot(url, descriptor)
		require.True(tb, snapshots[index].Found)
	}
	return component, snapshots
}
