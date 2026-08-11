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

package configpublisher

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	k8sfake "k8s.io/client-go/kubernetes/fake"
	k8stesting "k8s.io/client-go/testing"

	"gitlab.com/haproxy-haptic/haptic/pkg/apis/haproxytemplate/v1alpha1"
	"gitlab.com/haproxy-haptic/haptic/pkg/controller/testutil"
	busevents "gitlab.com/haproxy-haptic/haptic/pkg/events"
	crdclientfake "gitlab.com/haproxy-haptic/haptic/pkg/generated/clientset/versioned/fake"
	"gitlab.com/haproxy-haptic/haptic/pkg/k8s/configpublisher"
)

func TestComponentStartWaitsForWorkers(t *testing.T) {
	crdClient := crdclientfake.NewSimpleClientset()
	entered := make(chan struct{})
	release := make(chan struct{})
	released := false
	defer func() {
		if !released {
			close(release)
		}
	}()
	crdClient.PrependReactor("*", "*", func(k8stesting.Action) (bool, runtime.Object, error) {
		select {
		case <-entered:
		default:
			close(entered)
		}
		<-release
		return false, nil, nil
	})

	bus := busevents.NewEventBus(8)
	publisher := configpublisher.NewWithListers(
		k8sfake.NewClientset(), crdClient, nil, testutil.NewTestLogger())
	c := New(publisher, bus, testutil.NewTestLogger())
	ctx, cancel := context.WithCancel(t.Context())
	startDone := make(chan error, 1)
	go func() { startDone <- c.Start(ctx) }()

	select {
	case <-c.SubscriptionReady():
	case <-time.After(time.Second):
		t.Fatal("config publisher did not become ready")
	}
	c.publishWork <- &publishWorkItem{
		correlationID: "blocked-publish",
		templateConfig: &v1alpha1.HAProxyTemplateConfig{ObjectMeta: metav1.ObjectMeta{
			Name: "test", Namespace: "default", UID: types.UID("test-uid"),
		}},
		entry: &renderedConfigEntry{config: "global\n", contentChecksum: "checksum"},
	}
	select {
	case <-entered:
	case <-time.After(time.Second):
		t.Fatal("publish worker did not enter the Kubernetes client")
	}

	cancel()
	select {
	case <-startDone:
		t.Fatal("Component.Start returned before its publish worker exited")
	case <-time.After(50 * time.Millisecond):
	}

	close(release)
	released = true
	select {
	case err := <-startDone:
		assert.ErrorIs(t, err, context.Canceled)
	case <-time.After(time.Second):
		t.Fatal("Component.Start did not return after its publish worker exited")
	}
}

func TestComponentShutdownDoesNotFlushPendingWrites(t *testing.T) {
	crdClient := crdclientfake.NewSimpleClientset()
	kubeClient := k8sfake.NewClientset()
	bus := busevents.NewEventBus(8)
	publisher := configpublisher.NewWithListers(kubeClient, crdClient, nil, testutil.NewTestLogger())
	c := New(publisher, bus, testutil.NewTestLogger(), WithPublishInterval(time.Hour))
	c.publishThrottle.MarkFired()
	c.statusThrottle.MarkFired()
	c.pendingPublish = &publishWorkItem{
		correlationID: "pending-at-shutdown",
		templateConfig: &v1alpha1.HAProxyTemplateConfig{ObjectMeta: metav1.ObjectMeta{
			Name: "test", Namespace: "default", UID: types.UID("test-uid"),
		}},
		entry: &renderedConfigEntry{config: "global\n", contentChecksum: "checksum"},
	}

	ctx, cancel := context.WithCancel(t.Context())
	startDone := make(chan error, 1)
	go func() { startDone <- c.Start(ctx) }()
	select {
	case <-c.SubscriptionReady():
	case <-time.After(time.Second):
		t.Fatal("config publisher did not become ready")
	}
	cancel()
	select {
	case err := <-startDone:
		assert.ErrorIs(t, err, context.Canceled)
	case <-time.After(time.Second):
		t.Fatal("config publisher did not stop")
	}

	require.Empty(t, crdClient.Actions(), "shutdown must not detach and publish pending CRD work")
	require.Empty(t, kubeClient.Actions(), "shutdown must not detach and publish pending Secret work")
}
