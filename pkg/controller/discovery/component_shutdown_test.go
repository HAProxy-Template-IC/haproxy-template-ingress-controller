// Copyright 2025 Philipp Hossner
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0

package discovery

import (
	"context"
	"net"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"gitlab.com/haproxy-haptic/haptic/pkg/controller/events"
	"gitlab.com/haproxy-haptic/haptic/pkg/controller/testutil"
	coreconfig "gitlab.com/haproxy-haptic/haptic/pkg/core/config"
	"gitlab.com/haproxy-haptic/haptic/pkg/k8s/store"
)

// Discovery is an all-replica component recreated every controller iteration; a
// config/credential change cancels its context and rebuilds the tree. Its only
// teardown signal is ctx.Done() in Start (it never sees LostLeadershipEvent).
// A version-probe retry timer (time.AfterFunc, backoff up to maxRetryInterval)
// armed before shutdown must be stopped when Start returns — otherwise it fires
// against the torn-down iteration's EventBus and keeps the dead Component
// reachable for up to a minute. Sibling timer-driven components (drift monitor,
// scheduler, metrics) all stop their timers on shutdown; this pins that Start
// does too.
func TestStart_StopsRetryTimerOnShutdown(t *testing.T) {
	c := newTestComponentWithoutHAProxy(t)

	// Arm a retry timer whose delay is long enough that it can only fire if
	// Start fails to stop it on shutdown.
	fired := make(chan struct{}, 1)
	c.retryTimerMu.Lock()
	c.retryTimer = time.AfterFunc(50*time.Millisecond, func() { fired <- struct{}{} })
	c.retryTimerMu.Unlock()

	// ctx already cancelled → Start takes the ctx.Done() path immediately
	// (component.Base returns nil on graceful shutdown), and its defer must
	// stop the armed timer well before the 50ms delay elapses.
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	require.NoError(t, c.Start(ctx))

	select {
	case <-fired:
		t.Fatal("retry timer fired after Start returned — timer leaked past shutdown")
	case <-time.After(150 * time.Millisecond):
		// Timer was stopped by Start's defer; no post-teardown fire.
	}
}

func TestStopRetryTimerJoinsRunningCallback(t *testing.T) {
	c := newTestComponentWithoutHAProxy(t)

	c.mu.Lock()
	locked := true
	defer func() {
		if locked {
			c.mu.Unlock()
		}
	}()
	c.retryTimerMu.Lock()
	c.armRetryTimerLocked(time.Millisecond)
	c.retryTimerMu.Unlock()

	require.Eventually(t, func() bool {
		c.retryTimerMu.Lock()
		defer c.retryTimerMu.Unlock()
		return c.retryTimer == nil
	}, testutil.LongTimeout, time.Millisecond)

	stopped := make(chan struct{})
	go func() {
		c.stopRetryTimer()
		close(stopped)
	}()
	require.Eventually(t, func() bool {
		c.retryTimerMu.Lock()
		defer c.retryTimerMu.Unlock()
		return c.retryTimerStopped
	}, testutil.LongTimeout, time.Millisecond)
	select {
	case <-stopped:
		t.Fatal("stop returned while retry callback was running")
	default:
	}

	c.mu.Unlock()
	locked = false
	select {
	case <-stopped:
	case <-time.After(testutil.LongTimeout):
		t.Fatal("stop did not join retry callback")
	}
}

func TestStartCancellationCancelsVersionProbe(t *testing.T) {
	requestStarted := make(chan struct{})
	requestCanceled := make(chan struct{})
	releaseRequest := make(chan struct{})
	var releaseOnce sync.Once
	release := func() {
		releaseOnce.Do(func() { close(releaseRequest) })
	}

	server := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, request *http.Request) {
		close(requestStarted)
		select {
		case <-request.Context().Done():
			close(requestCanceled)
		case <-releaseRequest:
		}
	}))
	t.Cleanup(server.Close)
	t.Cleanup(release)
	address := server.Listener.Addr().(*net.TCPAddr)

	bus, _ := testutil.NewTestBusAndLogger()
	c := createTestComponent(t, bus)
	podStore := store.NewMemoryStore(2)
	addPodToStoreWithPort(t, podStore, "haproxy-0", "default", address.IP.String(), int64(address.Port))
	c.SetPodStore(podStore)
	c.mu.Lock()
	c.credentials = &coreconfig.Credentials{}
	c.hasCredentials = true
	c.hasDataplanePort = true
	c.initialDiscoveryDone = true
	c.discovery = &Discovery{dataplanePort: address.Port, localVersion: c.localVersion}
	c.mu.Unlock()

	bus.Start()
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan error, 1)
	go func() {
		done <- c.Start(ctx)
	}()
	bus.Publish(events.NewDriftPreventionTriggeredEvent(time.Minute))

	select {
	case <-requestStarted:
	case <-time.After(testutil.LongTimeout):
		t.Fatal("version probe did not start")
	}
	cancel()

	select {
	case <-requestCanceled:
	case <-time.After(testutil.LongTimeout):
		release()
		t.Fatal("component cancellation did not cancel the version probe request")
	}
	select {
	case err := <-done:
		require.NoError(t, err)
	case <-time.After(testutil.LongTimeout):
		t.Fatal("component did not return after its version probe was canceled")
	}
}
