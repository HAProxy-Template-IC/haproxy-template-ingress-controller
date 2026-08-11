// Copyright 2025 Philipp Hossner
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
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"gitlab.com/haproxy-haptic/haptic/pkg/controller/events"
	"gitlab.com/haproxy-haptic/haptic/pkg/controller/testutil"
	storepkg "gitlab.com/haproxy-haptic/haptic/pkg/httpstore"
)

func waitForLifecycleSignal(t *testing.T, signal <-chan struct{}, timeoutMessage string) {
	t.Helper()
	select {
	case <-signal:
	case <-time.After(testutil.LongTimeout):
		t.Fatal(timeoutMessage)
	}
}

func TestStopAllRefreshersJoinsRunningCallback(t *testing.T) {
	bus, logger := testutil.NewTestBusAndLogger()
	c := New(bus, logger, 0)
	eventCh := bus.SubscribeTypes("test", 1, events.EventTypeProposalValidationRequested)
	bus.Start()
	c.ctx, c.cancel = context.WithCancel(context.Background())
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("content"))
	}))
	defer server.Close()
	url := server.URL
	_, err := c.store.Fetch(context.Background(), url, storepkg.FetchOptions{Delay: time.Hour}, nil)
	require.NoError(t, err)

	started := make(chan struct{})
	release := make(chan struct{})
	c.refreshStoreURL = func(context.Context, string) (*storepkg.PendingVersion, error) {
		close(started)
		<-release
		return &storepkg.PendingVersion{}, nil
	}

	c.mu.Lock()
	c.refreshGeneration[url]++
	c.armRefresherLocked(url, time.Millisecond, c.refreshGeneration[url])
	c.mu.Unlock()

	select {
	case <-started:
	case <-time.After(testutil.LongTimeout):
		t.Fatal("refresh callback did not start")
	}

	stopped := make(chan struct{})
	go func() {
		c.stopAllRefreshers()
		close(stopped)
	}()
	require.Eventually(t, func() bool {
		c.mu.Lock()
		defer c.mu.Unlock()
		return c.stopped
	}, testutil.LongTimeout, time.Millisecond)
	select {
	case <-stopped:
		t.Fatal("stop returned while refresh callback was running")
	default:
	}

	close(release)
	select {
	case <-stopped:
	case <-time.After(testutil.LongTimeout):
		t.Fatal("stop did not join refresh callback")
	}

	c.RegisterURL(url)
	c.mu.Lock()
	_, exists := c.refreshers[url]
	c.mu.Unlock()
	if exists {
		t.Fatal("stopped component registered a new refresher")
	}
	testutil.AssertNoEvent[*events.ProposalValidationRequestedEvent](t, eventCh, testutil.NoEventTimeout)
}

func TestSupersededRefreshDiscardsItsCommitAndPromptsReplacement(t *testing.T) {
	bus, logger := testutil.NewTestBusAndLogger()
	c := New(bus, logger, 0)
	eventCh := bus.SubscribeTypes("test", 1, events.EventTypeProposalValidationRequested)
	bus.Start()
	c.ctx, c.cancel = context.WithCancel(context.Background())

	oldFetchStarted := make(chan struct{})
	releaseOldFetch := make(chan struct{})
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		switch requests.Add(1) {
		case 1:
			_, _ = w.Write([]byte("initial"))
		case 2:
			close(oldFetchStarted)
			<-releaseOldFetch
			_, _ = w.Write([]byte("stale"))
		default:
			_, _ = w.Write([]byte("fresh"))
		}
	}))
	defer server.Close()
	_, err := c.store.Fetch(context.Background(), server.URL, storepkg.FetchOptions{Delay: time.Hour}, nil)
	require.NoError(t, err)

	oldCommitted := make(chan struct{})
	releaseOldCallback := make(chan struct{})
	replacementAttempted := make(chan *storepkg.PendingVersion, 1)
	var refreshes atomic.Int32
	c.refreshStoreURL = func(ctx context.Context, url string) (*storepkg.PendingVersion, error) {
		call := refreshes.Add(1)
		version, refreshErr := c.store.RefreshURLVersion(ctx, url)
		switch call {
		case 1:
			close(oldCommitted)
			<-releaseOldCallback
		case 2:
			replacementAttempted <- version
		}
		return version, refreshErr
	}
	c.mu.Lock()
	c.refreshGeneration[server.URL]++
	c.armRefresherLocked(server.URL, time.Millisecond, c.refreshGeneration[server.URL])
	c.mu.Unlock()
	waitForLifecycleSignal(t, oldFetchStarted, "old refresh did not start")

	c.StopRefresher(server.URL)
	c.RegisterURL(server.URL)
	close(releaseOldFetch)
	waitForLifecycleSignal(t, oldCommitted, "old refresh did not commit")
	require.Equal(t, "stale", c.store.GetEntry(server.URL).PendingContent)

	c.mu.Lock()
	timerReset := c.refreshers[server.URL].Reset(0)
	c.mu.Unlock()
	require.True(t, timerReset)
	select {
	case version := <-replacementAttempted:
		require.Nil(t, version)
	case <-time.After(testutil.LongTimeout):
		t.Fatal("replacement refresh did not observe old pending content")
	}

	close(releaseOldCallback)
	request := testutil.WaitForEvent[*events.ProposalValidationRequestedEvent](t, eventCh, testutil.LongTimeout)
	content, ok := request.HTTPOverlay.GetContent(server.URL)
	require.True(t, ok)
	require.Equal(t, "fresh", content)
	require.Equal(t, int32(3), requests.Load())

	c.StopRefresher(server.URL)
	c.refreshCallbacks.Wait()
}

func TestEvictionDoesNotRetireRefetchedURL(t *testing.T) {
	bus, logger := testutil.NewTestBusAndLogger()
	c := New(bus, logger, -time.Second)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("content"))
	}))
	defer server.Close()

	options := storepkg.FetchOptions{Delay: time.Hour}
	_, err := c.store.Fetch(t.Context(), server.URL, options, nil)
	require.NoError(t, err)
	c.RegisterURL(server.URL)
	c.mu.Lock()
	oldTimer := c.refreshers[server.URL]
	c.mu.Unlock()
	require.NotNil(t, oldTimer)

	realEvictUnused := c.evictStoreUnused
	evicted := make(chan struct{})
	releaseEviction := make(chan struct{})
	c.evictStoreUnused = func() []string {
		urls := realEvictUnused()
		close(evicted)
		<-releaseEviction
		return urls
	}

	evictionDone := make(chan []string, 1)
	go func() {
		evictionDone <- c.evictUnused()
	}()
	waitForLifecycleSignal(t, evicted, "eviction did not remove the old entry")
	require.Nil(t, c.store.GetEntry(server.URL))

	refetched := make(chan error, 1)
	registered := make(chan struct{})
	go func() {
		_, fetchErr := c.store.Fetch(t.Context(), server.URL, options, nil)
		refetched <- fetchErr
		c.RegisterURL(server.URL)
		close(registered)
	}()
	require.NoError(t, <-refetched)
	select {
	case <-registered:
		t.Fatal("registration crossed the eviction and timer-retirement boundary")
	case <-time.After(testutil.NoEventTimeout):
	}

	close(releaseEviction)
	select {
	case urls := <-evictionDone:
		require.Equal(t, []string{server.URL}, urls)
	case <-time.After(testutil.LongTimeout):
		t.Fatal("eviction did not finish")
	}
	waitForLifecycleSignal(t, registered, "refetched URL was not registered")

	c.mu.Lock()
	newTimer := c.refreshers[server.URL]
	c.mu.Unlock()
	require.NotNil(t, newTimer)
	require.NotSame(t, oldTimer, newTimer)
	c.StopRefresher(server.URL)
}

func TestRefreshBeforeStartRemainsScheduled(t *testing.T) {
	bus, logger := testutil.NewTestBusAndLogger()
	c := New(bus, logger, 0)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("content"))
	}))
	defer server.Close()
	_, err := c.store.Fetch(context.Background(), server.URL, storepkg.FetchOptions{Delay: time.Hour}, nil)
	require.NoError(t, err)

	refreshCalled := false
	c.refreshStoreURL = func(context.Context, string) (*storepkg.PendingVersion, error) {
		refreshCalled = true
		return nil, context.Canceled
	}
	timer := time.NewTimer(time.Hour)
	require.True(t, timer.Stop())
	c.mu.Lock()
	c.refreshGeneration[server.URL] = 1
	c.refreshManaged[server.URL] = true
	c.refreshPending[server.URL] = true
	c.refreshers[server.URL] = timer
	c.refreshCallbacks.Add(1)
	c.mu.Unlock()

	c.runRefresher(server.URL, 1)
	c.mu.Lock()
	pending := c.refreshPending[server.URL]
	c.mu.Unlock()
	require.True(t, pending)
	require.False(t, refreshCalled)
	c.StopRefresher(server.URL)
}
