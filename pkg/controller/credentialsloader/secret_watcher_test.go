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

package credentialsloader

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/client-go/kubernetes/fake"
	"k8s.io/client-go/tools/cache"

	"gitlab.com/haproxy-haptic/haptic/pkg/controller/events"
	"gitlab.com/haproxy-haptic/haptic/pkg/controller/testutil"
)

func TestNewSecretWatcher(t *testing.T) {
	client := fake.NewClientset()
	bus, logger := testutil.NewTestBusAndLogger()

	watcher := NewSecretWatcher(client, bus, logger, "test-ns", "test-secret")

	require.NotNil(t, watcher)
	assert.Equal(t, client, watcher.client)
	assert.Equal(t, bus, watcher.eventBus)
	assert.Equal(t, logger, watcher.logger)
	assert.Equal(t, "test-ns", watcher.namespace)
	assert.Equal(t, "test-secret", watcher.name)
	assert.NotNil(t, watcher.informerFactory)
	assert.NotNil(t, watcher.stopCh)
}

func TestSecretWatcher_StartAndStop(t *testing.T) {
	client := fake.NewClientset()
	bus, logger := testutil.NewTestBusAndLogger()

	watcher := NewSecretWatcher(client, bus, logger, "test-ns", "test-secret")
	bus.Start()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	done := make(chan error)
	go func() {
		done <- watcher.Start(ctx)
	}()

	time.Sleep(testutil.DebounceWait)
	watcher.Stop()

	select {
	case err := <-done:
		assert.NoError(t, err)
	case <-time.After(testutil.VeryLongTimeout):
		t.Fatal("watcher did not stop in time")
	}
}

func TestSecretWatcher_OnAdd_MatchingSecret(t *testing.T) {
	client := fake.NewClientset()
	bus, logger := testutil.NewTestBusAndLogger()

	watcher := NewSecretWatcher(client, bus, logger, "test-ns", "test-secret")

	eventChan := bus.Subscribe(50)
	bus.Start()

	secret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:            "test-secret",
			Namespace:       "test-ns",
			ResourceVersion: "12345",
		},
		Data: map[string][]byte{
			"dataplane_username": []byte("admin"),
			"dataplane_password": []byte("secret"),
		},
	}

	watcher.onAdd(secret)

	changed := testutil.WaitForEvent[*events.SecretResourceChangedEvent](t, eventChan, testutil.LongTimeout)
	resource, ok := changed.Resource.(*unstructured.Unstructured)
	require.True(t, ok)
	assert.Equal(t, "test-secret", resource.GetName())
	assert.Equal(t, "test-ns", resource.GetNamespace())
	assert.Equal(t, "12345", resource.GetResourceVersion())
}

func TestSecretWatcher_OnAdd_DifferentSecret(t *testing.T) {
	client := fake.NewClientset()
	bus, logger := testutil.NewTestBusAndLogger()

	watcher := NewSecretWatcher(client, bus, logger, "test-ns", "test-secret")

	eventChan := bus.Subscribe(50)
	bus.Start()

	secret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:            "other-secret",
			Namespace:       "test-ns",
			ResourceVersion: "12345",
		},
	}

	watcher.onAdd(secret)

	testutil.AssertNoEvent[*events.SecretResourceChangedEvent](t, eventChan, testutil.NoEventTimeout)
}

func TestSecretWatcher_OnAdd_InvalidType(t *testing.T) {
	client := fake.NewClientset()
	bus, logger := testutil.NewTestBusAndLogger()

	watcher := NewSecretWatcher(client, bus, logger, "test-ns", "test-secret")

	eventChan := bus.Subscribe(50)
	bus.Start()

	watcher.onAdd("not-a-secret")

	testutil.AssertNoEvent[*events.SecretResourceChangedEvent](t, eventChan, testutil.NoEventTimeout)
}

func TestSecretWatcher_OnUpdate_MatchingSecret(t *testing.T) {
	client := fake.NewClientset()
	bus, logger := testutil.NewTestBusAndLogger()

	watcher := NewSecretWatcher(client, bus, logger, "test-ns", "test-secret")

	eventChan := bus.Subscribe(50)
	bus.Start()

	oldSecret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:            "test-secret",
			Namespace:       "test-ns",
			ResourceVersion: "12345",
		},
	}

	newSecret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:            "test-secret",
			Namespace:       "test-ns",
			ResourceVersion: "12346", // Changed
		},
		Data: map[string][]byte{
			"dataplane_username": []byte("admin"),
			"dataplane_password": []byte("newsecret"),
		},
	}

	watcher.onUpdate(oldSecret, newSecret)

	changed := testutil.WaitForEvent[*events.SecretResourceChangedEvent](t, eventChan, testutil.LongTimeout)
	resource, ok := changed.Resource.(*unstructured.Unstructured)
	require.True(t, ok)
	assert.Equal(t, "12346", resource.GetResourceVersion())
}

func TestSecretWatcher_OnUpdate_SameResourceVersion(t *testing.T) {
	client := fake.NewClientset()
	bus, logger := testutil.NewTestBusAndLogger()

	watcher := NewSecretWatcher(client, bus, logger, "test-ns", "test-secret")

	eventChan := bus.Subscribe(50)
	bus.Start()

	oldSecret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:            "test-secret",
			Namespace:       "test-ns",
			ResourceVersion: "12345",
		},
	}

	newSecret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:            "test-secret",
			Namespace:       "test-ns",
			ResourceVersion: "12345", // Same version
		},
	}

	watcher.onUpdate(oldSecret, newSecret)

	testutil.AssertNoEvent[*events.SecretResourceChangedEvent](t, eventChan, testutil.NoEventTimeout)
}

func TestSecretWatcher_OnUpdate_DifferentSecret(t *testing.T) {
	client := fake.NewClientset()
	bus, logger := testutil.NewTestBusAndLogger()

	watcher := NewSecretWatcher(client, bus, logger, "test-ns", "test-secret")

	eventChan := bus.Subscribe(50)
	bus.Start()

	oldSecret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:            "other-secret",
			Namespace:       "test-ns",
			ResourceVersion: "12345",
		},
	}

	newSecret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:            "other-secret",
			Namespace:       "test-ns",
			ResourceVersion: "12346",
		},
	}

	watcher.onUpdate(oldSecret, newSecret)

	testutil.AssertNoEvent[*events.SecretResourceChangedEvent](t, eventChan, testutil.NoEventTimeout)
}

func TestSecretWatcher_OnUpdate_InvalidTypes(t *testing.T) {
	client := fake.NewClientset()
	bus, logger := testutil.NewTestBusAndLogger()

	watcher := NewSecretWatcher(client, bus, logger, "test-ns", "test-secret")

	eventChan := bus.Subscribe(50)
	bus.Start()

	watcher.onUpdate("not-a-secret", &corev1.Secret{})
	watcher.onUpdate(&corev1.Secret{}, "not-a-secret")

	testutil.AssertNoEvent[*events.SecretResourceChangedEvent](t, eventChan, testutil.NoEventTimeout)
}

func TestSecretWatcher_OnDelete_MatchingSecret(t *testing.T) {
	client := fake.NewClientset()
	bus, logger := testutil.NewTestBusAndLogger()

	watcher := NewSecretWatcher(client, bus, logger, "test-ns", "test-secret")

	eventChan := bus.Subscribe(50)
	bus.Start()

	secret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:            "test-secret",
			Namespace:       "test-ns",
			ResourceVersion: "12345",
		},
	}

	watcher.onDelete(secret)

	testutil.AssertNoEvent[*events.SecretResourceChangedEvent](t, eventChan, testutil.NoEventTimeout)
}

func TestSecretWatcher_OnDelete_DifferentSecret(t *testing.T) {
	client := fake.NewClientset()
	bus, logger := testutil.NewTestBusAndLogger()

	watcher := NewSecretWatcher(client, bus, logger, "test-ns", "test-secret")

	eventChan := bus.Subscribe(50)
	bus.Start()

	secret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:            "other-secret",
			Namespace:       "test-ns",
			ResourceVersion: "12345",
		},
	}

	watcher.onDelete(secret)

	testutil.AssertNoEvent[*events.SecretResourceChangedEvent](t, eventChan, testutil.NoEventTimeout)
}

func TestSecretWatcher_OnDelete_Tombstone(t *testing.T) {
	client := fake.NewClientset()
	bus, logger := testutil.NewTestBusAndLogger()

	watcher := NewSecretWatcher(client, bus, logger, "test-ns", "test-secret")

	eventChan := bus.Subscribe(50)
	bus.Start()

	secret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:            "test-secret",
			Namespace:       "test-ns",
			ResourceVersion: "12345",
		},
	}

	tombstone := cache.DeletedFinalStateUnknown{
		Key: "test-ns/test-secret",
		Obj: secret,
	}

	watcher.onDelete(tombstone)

	testutil.AssertNoEvent[*events.SecretResourceChangedEvent](t, eventChan, testutil.NoEventTimeout)
}

func TestSecretWatcher_OnDelete_InvalidType(t *testing.T) {
	client := fake.NewClientset()
	bus, logger := testutil.NewTestBusAndLogger()

	watcher := NewSecretWatcher(client, bus, logger, "test-ns", "test-secret")

	eventChan := bus.Subscribe(50)
	bus.Start()

	watcher.onDelete("not-a-secret")

	testutil.AssertNoEvent[*events.SecretResourceChangedEvent](t, eventChan, testutil.NoEventTimeout)
}

func TestSecretWatcher_OnDelete_TombstoneWithInvalidObject(t *testing.T) {
	client := fake.NewClientset()
	bus, logger := testutil.NewTestBusAndLogger()

	watcher := NewSecretWatcher(client, bus, logger, "test-ns", "test-secret")

	eventChan := bus.Subscribe(50)
	bus.Start()

	tombstone := cache.DeletedFinalStateUnknown{
		Key: "test-ns/test-secret",
		Obj: "not-a-secret",
	}

	watcher.onDelete(tombstone)

	testutil.AssertNoEvent[*events.SecretResourceChangedEvent](t, eventChan, testutil.NoEventTimeout)
}

func TestSecretWatcher_ToUnstructured(t *testing.T) {
	client := fake.NewClientset()
	bus, logger := testutil.NewTestBusAndLogger()

	watcher := NewSecretWatcher(client, bus, logger, "test-ns", "test-secret")

	secret := &corev1.Secret{
		TypeMeta: metav1.TypeMeta{
			Kind:       "Secret",
			APIVersion: "v1",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:            "test-secret",
			Namespace:       "test-ns",
			ResourceVersion: "12345",
		},
		Data: map[string][]byte{
			"username": []byte("admin"),
			"password": []byte("secret"),
		},
	}

	unstructuredSecret, err := watcher.toUnstructured(secret)

	require.NoError(t, err)
	require.NotNil(t, unstructuredSecret)
	assert.Equal(t, "test-secret", unstructuredSecret.GetName())
	assert.Equal(t, "test-ns", unstructuredSecret.GetNamespace())
	assert.Equal(t, "12345", unstructuredSecret.GetResourceVersion())
}

func TestSecretWatcher_StartWithContextCancel(t *testing.T) {
	client := fake.NewClientset()
	bus, logger := testutil.NewTestBusAndLogger()

	watcher := NewSecretWatcher(client, bus, logger, "test-ns", "test-secret")
	bus.Start()

	ctx, cancel := context.WithCancel(context.Background())

	done := make(chan error)
	go func() {
		done <- watcher.Start(ctx)
	}()

	time.Sleep(testutil.DebounceWait)
	cancel()

	select {
	case err := <-done:
		assert.ErrorIs(t, err, context.Canceled)
	case <-time.After(testutil.VeryLongTimeout):
		t.Fatal("watcher did not stop in time after context cancel")
	}
}
