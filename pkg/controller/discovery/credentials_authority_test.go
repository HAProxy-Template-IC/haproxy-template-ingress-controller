// Copyright 2025 Philipp Hossner
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0

package discovery

import (
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"strconv"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"gitlab.com/haproxy-haptic/haptic/pkg/controller/events"
	"gitlab.com/haproxy-haptic/haptic/pkg/controller/testutil"
	coreconfig "gitlab.com/haproxy-haptic/haptic/pkg/core/config"
	"gitlab.com/haproxy-haptic/haptic/pkg/dataplane"
	busevents "gitlab.com/haproxy-haptic/haptic/pkg/events"
	"gitlab.com/haproxy-haptic/haptic/pkg/k8s/types"
)

func newBlockedVersionServer(t *testing.T) (
	server *httptest.Server,
	probeStarted <-chan struct{},
	release func(),
) {
	t.Helper()
	started := make(chan struct{})
	releaseProbe := make(chan struct{})
	var releaseOnce sync.Once
	server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		switch request.URL.Path {
		case "/v3/info":
			select {
			case <-started:
			default:
				close(started)
			}
			<-releaseProbe
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"api":{"version":"v3.3.5"}}`))
		case "/v3/services/haproxy/runtime/info":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"info":{"version":"3.3.1"}}`))
		default:
			http.NotFound(w, request)
		}
	}))
	release = func() {
		releaseOnce.Do(func() { close(releaseProbe) })
	}
	t.Cleanup(func() {
		release()
		server.Close()
	})
	return server, started, release
}

func TestReplacementAuthorityRetiresBeforeVersionProbeCompletes(t *testing.T) {
	server, probeStarted, releaseProbe := newBlockedVersionServer(t)
	host, portText, err := net.SplitHostPort(server.Listener.Addr().String())
	require.NoError(t, err)
	port, err := strconv.Atoi(portText)
	require.NoError(t, err)

	bus, _ := testutil.NewTestBusAndLogger()
	component := createTestComponent(t, bus)
	podStore := createTestPodStore(t, nil)
	addPodToStoreWithPort(t, podStore, "haproxy-0", "default", host, int64(port))
	component.SetPodStore(podStore)
	oldEndpoint := dataplane.Endpoint{
		URL: server.URL + "/v3", Username: "admin", Password: "password",
		PodName: "haproxy-0", PodNamespace: "default", PodUID: "uid-old",
		DetectedMajorVersion: 3, DetectedMinorVersion: 3, DetectedFullVersion: "v3.3.4",
	}
	component.mu.Lock()
	component.dataplanePort = port
	component.hasDataplanePort = true
	component.credentials = &coreconfig.Credentials{
		DataplaneUsername: "admin",
		DataplanePassword: "password",
	}
	component.hasCredentials = true
	component.initialDiscoveryDone = true
	component.localVersion = &dataplane.Version{Major: 3, Minor: 3, Full: "v3.3.0"}
	component.discovery = &Discovery{dataplanePort: port, localVersion: component.localVersion}
	component.lastEndpoints[podIdentity{podNamespace: "default", podName: "haproxy-0"}] = endpointAuthorityOf(&oldEndpoint)
	component.admissionProofs[endpointIdentityOf(&oldEndpoint)] = versionAdmissionProof{
		dataPlaneAPI: dataplane.Version{Major: 3, Minor: 3, Full: "v3.3.4"},
		haproxy:      dataplane.Version{Major: 3, Minor: 3, Full: "3.3.1"},
	}
	component.mu.Unlock()
	component.discoveredReplayer.Cache(events.NewHAProxyPodsDiscoveredEvent([]dataplane.Endpoint{oldEndpoint}, 1))

	authorityCh := bus.Subscribe("pre-probe-authority", 10)
	bus.Start()
	done := make(chan struct{})
	go func() {
		component.triggerDiscovery("replacement")
		close(done)
	}()

	select {
	case <-probeStarted:
	case <-time.After(testutil.EventTimeout):
		t.Fatal("replacement version probe did not start")
	}
	first := testutil.WaitForEvent[busevents.Event](t, authorityCh, testutil.EventTimeout)
	terminated, ok := first.(*events.HAProxyPodTerminatedEvent)
	require.True(t, ok, "first authority event is %T", first)
	assert.Equal(t, "uid-old", terminated.PodUID)
	second := testutil.WaitForEvent[busevents.Event](t, authorityCh, testutil.EventTimeout)
	interim, ok := second.(*events.HAProxyPodsDiscoveredEvent)
	require.True(t, ok, "second authority event is %T", second)
	assert.Empty(t, interim.Endpoints)
	replayed, ok := component.discoveredReplayer.Get()
	require.True(t, ok)
	assert.Empty(t, replayed.Endpoints)

	releaseProbe()
	select {
	case <-done:
	case <-time.After(testutil.EventTimeout):
		t.Fatal("replacement discovery did not finish")
	}
	admitted := testutil.WaitForEvent[*events.HAProxyPodsDiscoveredEvent](t, authorityCh, testutil.EventTimeout)
	require.Len(t, admitted.Endpoints, 1)
	assert.Equal(t, "haproxy-0-uid", admitted.Endpoints[0].PodUID)
}

type blockingFirstListStore struct {
	types.Store
	firstStarted chan struct{}
	releaseFirst chan struct{}
	calls        atomic.Int32
}

func (s *blockingFirstListStore) List() ([]any, error) {
	resources, err := s.Store.List()
	if err != nil {
		return nil, err
	}
	if s.calls.Add(1) == 1 {
		close(s.firstStarted)
		<-s.releaseFirst
	}
	return resources, nil
}

func TestCredentialsUpdatedPublishesFreshEndpointCredentials(t *testing.T) {
	bus, _ := testutil.NewTestBusAndLogger()
	component := createTestComponent(t, bus)
	podStore := createTestPodStore(t, []string{"127.0.0.1"})
	component.SetPodStore(podStore)

	identity := endpointIdentity{
		podNamespace: "default",
		podName:      "haproxy-0",
		podUID:       "haproxy-0-uid",
		url:          "http://127.0.0.1:5555/v3",
	}
	component.mu.Lock()
	component.dataplanePort = 5555
	component.hasDataplanePort = true
	component.initialDiscoveryDone = true
	component.discovery = &Discovery{dataplanePort: 5555, localVersion: component.localVersion}
	component.admissionProofs[identity] = versionAdmissionProof{
		dataPlaneAPI: dataplane.Version{Major: 3, Minor: 3, Full: "v3.3.5 cached"},
		haproxy:      dataplane.Version{Major: 3, Minor: 2, Full: "3.2.0"},
	}
	component.mu.Unlock()

	eventChannel := bus.Subscribe("credential-authority-test", 10)
	bus.Start()
	component.handleCredentialsUpdated(events.NewCredentialsUpdatedEvent(&coreconfig.Credentials{
		DataplaneUsername: "rotated-user",
		DataplanePassword: "rotated-password",
	}, "secret-v2"))

	discovered := testutil.WaitForEvent[*events.HAProxyPodsDiscoveredEvent](t, eventChannel, testutil.EventTimeout)
	require.Len(t, discovered.Endpoints, 1)
	assert.Equal(t, "rotated-user", discovered.Endpoints[0].Username)
	assert.Equal(t, "rotated-password", discovered.Endpoints[0].Password)
	assert.Equal(t, "v3.3.5 cached", discovered.Endpoints[0].DetectedFullVersion)
	assert.Equal(t, dataplane.Endpoint{
		URL:                  identity.url,
		Username:             "rotated-user",
		Password:             "rotated-password",
		PodName:              identity.podName,
		PodNamespace:         identity.podNamespace,
		PodUID:               identity.podUID,
		DetectedMajorVersion: 3,
		DetectedMinorVersion: 3,
		DetectedFullVersion:  "v3.3.5 cached",
	}, discovered.Endpoints[0])
}

func TestCredentialsUpdateCannotBeOverwrittenByOlderRetryDiscovery(t *testing.T) {
	bus, _ := testutil.NewTestBusAndLogger()
	component := createTestComponent(t, bus)
	podStore := &blockingFirstListStore{
		Store:        createTestPodStore(t, []string{"127.0.0.1"}),
		firstStarted: make(chan struct{}),
		releaseFirst: make(chan struct{}),
	}
	component.SetPodStore(podStore)

	identity := endpointIdentity{
		podNamespace: "default",
		podName:      "haproxy-0",
		podUID:       "haproxy-0-uid",
		url:          "http://127.0.0.1:5555/v3",
	}
	component.mu.Lock()
	component.dataplanePort = 5555
	component.hasDataplanePort = true
	component.initialDiscoveryDone = true
	component.credentials = &coreconfig.Credentials{
		DataplaneUsername: "old-user",
		DataplanePassword: "old-password",
	}
	component.hasCredentials = true
	component.discovery = &Discovery{dataplanePort: 5555, localVersion: component.localVersion}
	component.admissionProofs[identity] = versionAdmissionProof{
		dataPlaneAPI: dataplane.Version{Major: 3, Minor: 3, Full: "v3.3.5 cached"},
		haproxy:      dataplane.Version{Major: 3, Minor: 2, Full: "3.2.0"},
	}
	component.mu.Unlock()

	eventChannel := bus.Subscribe("ordered-credential-authority-test", 10)
	bus.Start()
	oldDone := make(chan struct{})
	go func() {
		component.triggerDiscovery("retry_timer")
		close(oldDone)
	}()
	select {
	case <-podStore.firstStarted:
	case <-time.After(testutil.LongTimeout):
		t.Fatal("older discovery did not start")
	}

	newDone := make(chan struct{})
	go func() {
		component.handleCredentialsUpdated(events.NewCredentialsUpdatedEvent(&coreconfig.Credentials{
			DataplaneUsername: "new-user",
			DataplanePassword: "new-password",
		}, "secret-v2"))
		close(newDone)
	}()
	close(podStore.releaseFirst)

	for _, done := range []<-chan struct{}{oldDone, newDone} {
		select {
		case <-done:
		case <-time.After(testutil.LongTimeout):
			t.Fatal("discovery did not complete")
		}
	}

	first := testutil.WaitForEvent[*events.HAProxyPodsDiscoveredEvent](t, eventChannel, testutil.EventTimeout)
	interim := testutil.WaitForEvent[*events.HAProxyPodsDiscoveredEvent](t, eventChannel, testutil.EventTimeout)
	second := testutil.WaitForEvent[*events.HAProxyPodsDiscoveredEvent](t, eventChannel, testutil.EventTimeout)
	require.Len(t, first.Endpoints, 1)
	require.Empty(t, interim.Endpoints)
	require.Len(t, second.Endpoints, 1)
	assert.Equal(t, "old-user", first.Endpoints[0].Username)
	assert.Equal(t, "new-user", second.Endpoints[0].Username)
	assert.Equal(t, "new-password", second.Endpoints[0].Password)
}

func TestDataplanePortUpdateCannotBeOverwrittenByOlderRetryDiscovery(t *testing.T) {
	server := infoServer(t, "v3.3.5 cached", "3.2.0")
	newPort := server.Listener.Addr().(*net.TCPAddr).Port
	bus, _ := testutil.NewTestBusAndLogger()
	component := createTestComponent(t, bus)
	podStore := &blockingFirstListStore{
		Store:        createTestPodStore(t, []string{"127.0.0.1"}),
		firstStarted: make(chan struct{}),
		releaseFirst: make(chan struct{}),
	}
	component.SetPodStore(podStore)

	oldIdentity := endpointIdentity{
		podNamespace: "default",
		podName:      "haproxy-0",
		podUID:       "haproxy-0-uid",
		url:          "http://127.0.0.1:5555/v3",
	}
	component.mu.Lock()
	component.dataplanePort = 5555
	component.hasDataplanePort = true
	component.initialDiscoveryDone = true
	component.credentials = &coreconfig.Credentials{
		DataplaneUsername: "admin",
		DataplanePassword: "password",
	}
	component.hasCredentials = true
	component.discovery = &Discovery{dataplanePort: 5555, localVersion: component.localVersion}
	component.admissionProofs[oldIdentity] = versionAdmissionProof{
		dataPlaneAPI: dataplane.Version{Major: 3, Minor: 3, Full: "v3.3.5 cached"},
		haproxy:      dataplane.Version{Major: 3, Minor: 2, Full: "3.2.0"},
	}
	component.mu.Unlock()

	eventChannel := bus.Subscribe("ordered-port-authority-test", 10)
	bus.Start()
	oldDone := make(chan struct{})
	go func() {
		component.triggerDiscovery("retry_timer")
		close(oldDone)
	}()
	select {
	case <-podStore.firstStarted:
	case <-time.After(testutil.LongTimeout):
		t.Fatal("older discovery did not start")
	}
	require.NoError(t, podStore.Clear())
	addPodToStoreWithPort(t, podStore, "haproxy-0", "default", "127.0.0.1", int64(newPort))

	newDone := make(chan struct{})
	go func() {
		component.handleConfigValidated(events.NewConfigValidatedEvent(&coreconfig.Config{
			Dataplane: coreconfig.DataplaneConfig{Port: newPort},
		}, nil, "v2", "secret-v1"))
		close(newDone)
	}()
	close(podStore.releaseFirst)

	for _, done := range []<-chan struct{}{oldDone, newDone} {
		select {
		case <-done:
		case <-time.After(testutil.LongTimeout):
			t.Fatal("discovery did not complete")
		}
	}

	first := testutil.WaitForEvent[*events.HAProxyPodsDiscoveredEvent](t, eventChannel, testutil.EventTimeout)
	interim := testutil.WaitForEvent[*events.HAProxyPodsDiscoveredEvent](t, eventChannel, testutil.EventTimeout)
	second := testutil.WaitForEvent[*events.HAProxyPodsDiscoveredEvent](t, eventChannel, testutil.EventTimeout)
	require.Len(t, first.Endpoints, 1)
	require.Empty(t, interim.Endpoints)
	require.Len(t, second.Endpoints, 1)
	assert.Equal(t, oldIdentity.url, first.Endpoints[0].URL)
	assert.Equal(t, fmt.Sprintf("http://127.0.0.1:%d/v3", newPort), second.Endpoints[0].URL)
}

func TestCredentialsUpdateResetsPendingProbeBackoff(t *testing.T) {
	component := newTestComponentWithoutHAProxy(t)
	identity := testEndpointIdentity("haproxy-0")
	component.credentials = &coreconfig.Credentials{
		DataplaneUsername: "old-user",
		DataplanePassword: "old-password",
	}
	component.pendingRetries[identity] = &retryState{retryCount: 4, lastAttempt: time.Now()}

	component.handleCredentialsUpdated(events.NewCredentialsUpdatedEvent(&coreconfig.Credentials{
		DataplaneUsername: "new-user",
		DataplanePassword: "new-password",
	}, "secret-v2"))

	assert.Empty(t, component.pendingRetries)
}
