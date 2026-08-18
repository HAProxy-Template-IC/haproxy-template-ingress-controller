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
// teardown signal is ctx.Done() in Start. The admission probe is a blocking
// HTTP call against a pod, so a hung agent must not outlive the iteration that
// started it.
func TestStartCancellationCancelsAdmissionProbe(t *testing.T) {
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
	c.discovery = &Discovery{dataplanePort: address.Port}
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
		t.Fatal("admission probe did not start")
	}
	cancel()

	select {
	case <-requestCanceled:
	case <-time.After(testutil.LongTimeout):
		release()
		t.Fatal("component cancellation did not cancel the admission probe request")
	}
	select {
	case err := <-done:
		require.NoError(t, err)
	case <-time.After(testutil.LongTimeout):
		t.Fatal("component did not return after its admission probe was canceled")
	}
}
