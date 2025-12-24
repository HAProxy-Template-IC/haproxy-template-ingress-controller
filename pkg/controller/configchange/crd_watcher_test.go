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

package configchange

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/client-go/tools/cache"

	"gitlab.com/haproxy-template-ic/haproxy-template-ingress-controller/pkg/apis/haproxytemplate/v1alpha1"
	"gitlab.com/haproxy-template-ic/haproxy-template-ingress-controller/pkg/controller/events"
	"gitlab.com/haproxy-template-ic/haproxy-template-ingress-controller/pkg/controller/testutil"
	"gitlab.com/haproxy-template-ic/haproxy-template-ingress-controller/pkg/generated/clientset/versioned/fake"
)

func TestNewCRDWatcher(t *testing.T) {
	client := fake.NewSimpleClientset()
	bus, logger := testutil.NewTestBusAndLogger()

	watcher := NewCRDWatcher(client, bus, logger, "test-ns", "test-config")

	require.NotNil(t, watcher)
	assert.Equal(t, client, watcher.client)
	assert.Equal(t, bus, watcher.eventBus)
	assert.Equal(t, logger, watcher.logger)
	assert.Equal(t, "test-ns", watcher.namespace)
	assert.Equal(t, "test-config", watcher.name)
	assert.NotNil(t, watcher.stopCh)
	assert.NotNil(t, watcher.informerFactory)
}

func TestCRDWatcher_Stop(t *testing.T) {
	client := fake.NewSimpleClientset()
	bus, logger := testutil.NewTestBusAndLogger()

	watcher := NewCRDWatcher(client, bus, logger, "test-ns", "test-config")

	// Stop should close the channel without panic
	watcher.Stop()

	// Verify channel is closed
	select {
	case _, ok := <-watcher.stopCh:
		assert.False(t, ok, "stopCh should be closed")
	default:
		t.Fatal("stopCh should be closed")
	}
}

func TestCRDWatcher_OnAdd_MatchingResource(t *testing.T) {
	client := fake.NewSimpleClientset()
	bus, logger := testutil.NewTestBusAndLogger()

	watcher := NewCRDWatcher(client, bus, logger, "test-ns", "test-config")

	eventChan := bus.Subscribe(50)
	bus.Start()

	config := &v1alpha1.HAProxyTemplateConfig{
		ObjectMeta: metav1.ObjectMeta{
			Name:            "test-config",
			Namespace:       "test-ns",
			ResourceVersion: "12345",
		},
	}

	watcher.onAdd(config)

	// Should publish ConfigResourceChangedEvent
	changedEvent := testutil.WaitForEvent[*events.ConfigResourceChangedEvent](t, eventChan, testutil.LongTimeout)
	resource, ok := changedEvent.Resource.(*unstructured.Unstructured)
	require.True(t, ok, "expected resource to be *unstructured.Unstructured")
	assert.Equal(t, "test-config", resource.GetName())
	assert.Equal(t, "12345", resource.GetResourceVersion())
}

func TestCRDWatcher_OnAdd_DifferentResource(t *testing.T) {
	client := fake.NewSimpleClientset()
	bus, logger := testutil.NewTestBusAndLogger()

	watcher := NewCRDWatcher(client, bus, logger, "test-ns", "test-config")

	eventChan := bus.Subscribe(50)
	bus.Start()

	// Different name - should be ignored
	config := &v1alpha1.HAProxyTemplateConfig{
		ObjectMeta: metav1.ObjectMeta{
			Name:            "different-config",
			Namespace:       "test-ns",
			ResourceVersion: "12345",
		},
	}

	watcher.onAdd(config)

	// Should NOT publish any event
	testutil.AssertNoEvent[*events.ConfigResourceChangedEvent](t, eventChan, testutil.NoEventTimeout)
}

func TestCRDWatcher_OnAdd_InvalidType(t *testing.T) {
	client := fake.NewSimpleClientset()
	bus, logger := testutil.NewTestBusAndLogger()

	watcher := NewCRDWatcher(client, bus, logger, "test-ns", "test-config")

	eventChan := bus.Subscribe(50)
	bus.Start()

	// Invalid type - should be ignored
	watcher.onAdd("not-a-config")

	// Should NOT publish any event
	testutil.AssertNoEvent[*events.ConfigResourceChangedEvent](t, eventChan, testutil.NoEventTimeout)
}

func TestCRDWatcher_OnUpdate_MatchingResource(t *testing.T) {
	client := fake.NewSimpleClientset()
	bus, logger := testutil.NewTestBusAndLogger()

	watcher := NewCRDWatcher(client, bus, logger, "test-ns", "test-config")

	eventChan := bus.Subscribe(50)
	bus.Start()

	oldConfig := &v1alpha1.HAProxyTemplateConfig{
		ObjectMeta: metav1.ObjectMeta{
			Name:            "test-config",
			Namespace:       "test-ns",
			ResourceVersion: "12345",
		},
	}

	newConfig := &v1alpha1.HAProxyTemplateConfig{
		ObjectMeta: metav1.ObjectMeta{
			Name:            "test-config",
			Namespace:       "test-ns",
			ResourceVersion: "12346",
		},
	}

	watcher.onUpdate(oldConfig, newConfig)

	// Should publish ConfigResourceChangedEvent
	changedEvent := testutil.WaitForEvent[*events.ConfigResourceChangedEvent](t, eventChan, testutil.LongTimeout)
	resource, ok := changedEvent.Resource.(*unstructured.Unstructured)
	require.True(t, ok, "expected resource to be *unstructured.Unstructured")
	assert.Equal(t, "test-config", resource.GetName())
	assert.Equal(t, "12346", resource.GetResourceVersion())
}

func TestCRDWatcher_OnUpdate_SameResourceVersion(t *testing.T) {
	client := fake.NewSimpleClientset()
	bus, logger := testutil.NewTestBusAndLogger()

	watcher := NewCRDWatcher(client, bus, logger, "test-ns", "test-config")

	eventChan := bus.Subscribe(50)
	bus.Start()

	// Same resource version - should be ignored
	config := &v1alpha1.HAProxyTemplateConfig{
		ObjectMeta: metav1.ObjectMeta{
			Name:            "test-config",
			Namespace:       "test-ns",
			ResourceVersion: "12345",
		},
	}

	watcher.onUpdate(config, config)

	// Should NOT publish any event
	testutil.AssertNoEvent[*events.ConfigResourceChangedEvent](t, eventChan, testutil.NoEventTimeout)
}

func TestCRDWatcher_OnUpdate_DifferentResource(t *testing.T) {
	client := fake.NewSimpleClientset()
	bus, logger := testutil.NewTestBusAndLogger()

	watcher := NewCRDWatcher(client, bus, logger, "test-ns", "test-config")

	eventChan := bus.Subscribe(50)
	bus.Start()

	// Different name - should be ignored
	oldConfig := &v1alpha1.HAProxyTemplateConfig{
		ObjectMeta: metav1.ObjectMeta{
			Name:            "different-config",
			Namespace:       "test-ns",
			ResourceVersion: "12345",
		},
	}

	newConfig := &v1alpha1.HAProxyTemplateConfig{
		ObjectMeta: metav1.ObjectMeta{
			Name:            "different-config",
			Namespace:       "test-ns",
			ResourceVersion: "12346",
		},
	}

	watcher.onUpdate(oldConfig, newConfig)

	// Should NOT publish any event
	testutil.AssertNoEvent[*events.ConfigResourceChangedEvent](t, eventChan, testutil.NoEventTimeout)
}

func TestCRDWatcher_OnUpdate_InvalidType(t *testing.T) {
	client := fake.NewSimpleClientset()
	bus, logger := testutil.NewTestBusAndLogger()

	watcher := NewCRDWatcher(client, bus, logger, "test-ns", "test-config")

	eventChan := bus.Subscribe(50)
	bus.Start()

	// Invalid types - should be ignored
	watcher.onUpdate("not-a-config", "also-not-a-config")

	// Should NOT publish any event
	testutil.AssertNoEvent[*events.ConfigResourceChangedEvent](t, eventChan, testutil.NoEventTimeout)
}

func TestCRDWatcher_OnDelete_MatchingResource(t *testing.T) {
	client := fake.NewSimpleClientset()
	bus, logger := testutil.NewTestBusAndLogger()

	watcher := NewCRDWatcher(client, bus, logger, "test-ns", "test-config")

	eventChan := bus.Subscribe(50)
	bus.Start()

	config := &v1alpha1.HAProxyTemplateConfig{
		ObjectMeta: metav1.ObjectMeta{
			Name:            "test-config",
			Namespace:       "test-ns",
			ResourceVersion: "12345",
		},
	}

	watcher.onDelete(config)

	// Note: onDelete doesn't publish events currently, just logs
	// Should NOT publish any event
	testutil.AssertNoEvent[*events.ConfigResourceChangedEvent](t, eventChan, testutil.NoEventTimeout)
}

func TestCRDWatcher_OnDelete_DifferentResource(t *testing.T) {
	client := fake.NewSimpleClientset()
	bus, logger := testutil.NewTestBusAndLogger()

	watcher := NewCRDWatcher(client, bus, logger, "test-ns", "test-config")

	eventChan := bus.Subscribe(50)
	bus.Start()

	// Different name - should be ignored
	config := &v1alpha1.HAProxyTemplateConfig{
		ObjectMeta: metav1.ObjectMeta{
			Name:            "different-config",
			Namespace:       "test-ns",
			ResourceVersion: "12345",
		},
	}

	watcher.onDelete(config)

	// Should NOT publish any event
	testutil.AssertNoEvent[*events.ConfigResourceChangedEvent](t, eventChan, testutil.NoEventTimeout)
}

func TestCRDWatcher_OnDelete_Tombstone(t *testing.T) {
	client := fake.NewSimpleClientset()
	bus, logger := testutil.NewTestBusAndLogger()

	watcher := NewCRDWatcher(client, bus, logger, "test-ns", "test-config")

	eventChan := bus.Subscribe(50)
	bus.Start()

	config := &v1alpha1.HAProxyTemplateConfig{
		ObjectMeta: metav1.ObjectMeta{
			Name:            "test-config",
			Namespace:       "test-ns",
			ResourceVersion: "12345",
		},
	}

	tombstone := cache.DeletedFinalStateUnknown{
		Key: "test-ns/test-config",
		Obj: config,
	}

	watcher.onDelete(tombstone)

	// Note: onDelete doesn't publish events currently, just logs
	// Should NOT publish any event
	testutil.AssertNoEvent[*events.ConfigResourceChangedEvent](t, eventChan, testutil.NoEventTimeout)
}

func TestCRDWatcher_OnDelete_InvalidTombstone(t *testing.T) {
	client := fake.NewSimpleClientset()
	bus, logger := testutil.NewTestBusAndLogger()

	watcher := NewCRDWatcher(client, bus, logger, "test-ns", "test-config")

	eventChan := bus.Subscribe(50)
	bus.Start()

	// Tombstone with invalid object
	tombstone := cache.DeletedFinalStateUnknown{
		Key: "test-ns/test-config",
		Obj: "not-a-config",
	}

	watcher.onDelete(tombstone)

	// Should NOT publish any event
	testutil.AssertNoEvent[*events.ConfigResourceChangedEvent](t, eventChan, testutil.NoEventTimeout)
}

func TestCRDWatcher_OnDelete_InvalidType(t *testing.T) {
	client := fake.NewSimpleClientset()
	bus, logger := testutil.NewTestBusAndLogger()

	watcher := NewCRDWatcher(client, bus, logger, "test-ns", "test-config")

	eventChan := bus.Subscribe(50)
	bus.Start()

	// Invalid type - should be ignored
	watcher.onDelete("not-a-config")

	// Should NOT publish any event
	testutil.AssertNoEvent[*events.ConfigResourceChangedEvent](t, eventChan, testutil.NoEventTimeout)
}

func TestCRDWatcher_ToUnstructured(t *testing.T) {
	client := fake.NewSimpleClientset()
	bus, logger := testutil.NewTestBusAndLogger()

	watcher := NewCRDWatcher(client, bus, logger, "test-ns", "test-config")

	config := &v1alpha1.HAProxyTemplateConfig{
		ObjectMeta: metav1.ObjectMeta{
			Name:            "test-config",
			Namespace:       "test-ns",
			ResourceVersion: "12345",
		},
	}

	unstructuredConfig, err := watcher.toUnstructured(config)

	require.NoError(t, err)
	require.NotNil(t, unstructuredConfig)
	assert.Equal(t, "test-config", unstructuredConfig.GetName())
	assert.Equal(t, "test-ns", unstructuredConfig.GetNamespace())
	assert.Equal(t, "12345", unstructuredConfig.GetResourceVersion())
}
