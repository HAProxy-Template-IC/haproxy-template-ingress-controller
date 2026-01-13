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

package webhook

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"

	"gitlab.com/haproxy-haptic/haptic/pkg/controller/events"
	busevents "gitlab.com/haproxy-haptic/haptic/pkg/events"
)

func TestBasicValidatorComponent_New(t *testing.T) {
	eventBus := busevents.NewEventBus(10)
	component := NewBasicValidatorComponent(eventBus, testLogger())

	require.NotNil(t, component)
	assert.NotNil(t, component.eventBus)
	assert.NotNil(t, component.logger)
}

func TestBasicValidatorComponent_validateBasicStructure(t *testing.T) {
	eventBus := busevents.NewEventBus(10)
	component := NewBasicValidatorComponent(eventBus, testLogger())

	t.Run("valid object with name", func(t *testing.T) {
		obj := &unstructured.Unstructured{
			Object: map[string]interface{}{
				"apiVersion": "v1",
				"kind":       "ConfigMap",
				"metadata": map[string]interface{}{
					"name":      "test-config",
					"namespace": "default",
				},
			},
		}

		err := component.validateBasicStructure(obj)
		assert.NoError(t, err)
	})

	t.Run("valid object with generateName", func(t *testing.T) {
		obj := &unstructured.Unstructured{
			Object: map[string]interface{}{
				"apiVersion": "v1",
				"kind":       "ConfigMap",
				"metadata": map[string]interface{}{
					"generateName": "test-config-",
					"namespace":    "default",
				},
			},
		}

		err := component.validateBasicStructure(obj)
		assert.NoError(t, err)
	})

	t.Run("invalid object - missing name and generateName", func(t *testing.T) {
		obj := &unstructured.Unstructured{
			Object: map[string]interface{}{
				"apiVersion": "v1",
				"kind":       "ConfigMap",
				"metadata": map[string]interface{}{
					"namespace": "default",
				},
			},
		}

		err := component.validateBasicStructure(obj)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "metadata.name or metadata.generateName is required")
	})

	t.Run("valid cluster-scoped resource without namespace", func(t *testing.T) {
		obj := &unstructured.Unstructured{
			Object: map[string]interface{}{
				"apiVersion": "v1",
				"kind":       "Namespace",
				"metadata": map[string]interface{}{
					"name": "test-namespace",
				},
			},
		}

		err := component.validateBasicStructure(obj)
		assert.NoError(t, err)
	})
}

func TestBasicValidatorComponent_handleValidationRequest(t *testing.T) {
	t.Run("allows valid object", func(t *testing.T) {
		eventBus := busevents.NewEventBus(100)
		eventChan := eventBus.Subscribe("test-sub", 50)
		component := NewBasicValidatorComponent(eventBus, testLogger())
		eventBus.Start()

		obj := &unstructured.Unstructured{
			Object: map[string]interface{}{
				"apiVersion": "v1",
				"kind":       "ConfigMap",
				"metadata": map[string]interface{}{
					"name":      "test-config",
					"namespace": "default",
				},
			},
		}

		req := events.NewWebhookValidationRequest(
			"v1.ConfigMap",
			"default",
			"test-config",
			obj,
			"CREATE",
		)

		component.handleValidationRequest(req)

		// Wait for response
		timeout := time.After(1 * time.Second)
		select {
		case event := <-eventChan:
			resp, ok := event.(*events.WebhookValidationResponse)
			require.True(t, ok, "expected WebhookValidationResponse, got %T", event)
			assert.True(t, resp.Allowed)
			assert.Equal(t, BasicValidatorID, resp.ValidatorID)
			assert.Empty(t, resp.Reason)
		case <-timeout:
			t.Fatal("timeout waiting for validation response")
		}
	})

	t.Run("denies object without name", func(t *testing.T) {
		eventBus := busevents.NewEventBus(100)
		eventChan := eventBus.Subscribe("test-sub", 50)
		component := NewBasicValidatorComponent(eventBus, testLogger())
		eventBus.Start()

		obj := &unstructured.Unstructured{
			Object: map[string]interface{}{
				"apiVersion": "v1",
				"kind":       "ConfigMap",
				"metadata": map[string]interface{}{
					"namespace": "default",
				},
			},
		}

		req := events.NewWebhookValidationRequest(
			"v1.ConfigMap",
			"default",
			"",
			obj,
			"CREATE",
		)

		component.handleValidationRequest(req)

		// Wait for response
		timeout := time.After(1 * time.Second)
		select {
		case event := <-eventChan:
			resp, ok := event.(*events.WebhookValidationResponse)
			require.True(t, ok, "expected WebhookValidationResponse, got %T", event)
			assert.False(t, resp.Allowed)
			assert.Equal(t, BasicValidatorID, resp.ValidatorID)
			assert.Contains(t, resp.Reason, "metadata.name or metadata.generateName is required")
		case <-timeout:
			t.Fatal("timeout waiting for validation response")
		}
	})

	t.Run("denies invalid object type", func(t *testing.T) {
		eventBus := busevents.NewEventBus(100)
		eventChan := eventBus.Subscribe("test-sub", 50)
		component := NewBasicValidatorComponent(eventBus, testLogger())
		eventBus.Start()

		// Pass a non-unstructured object
		req := events.NewWebhookValidationRequest(
			"v1.ConfigMap",
			"default",
			"test",
			"invalid-object-type", // string instead of *unstructured.Unstructured
			"CREATE",
		)

		component.handleValidationRequest(req)

		// Wait for response
		timeout := time.After(1 * time.Second)
		select {
		case event := <-eventChan:
			resp, ok := event.(*events.WebhookValidationResponse)
			require.True(t, ok, "expected WebhookValidationResponse, got %T", event)
			assert.False(t, resp.Allowed)
			assert.Contains(t, resp.Reason, "invalid object type")
		case <-timeout:
			t.Fatal("timeout waiting for validation response")
		}
	})
}

func TestBasicValidatorComponent_handleEvent(t *testing.T) {
	t.Run("processes WebhookValidationRequest", func(t *testing.T) {
		eventBus := busevents.NewEventBus(100)
		eventChan := eventBus.Subscribe("test-sub", 50)
		component := NewBasicValidatorComponent(eventBus, testLogger())
		eventBus.Start()

		obj := &unstructured.Unstructured{
			Object: map[string]interface{}{
				"apiVersion": "v1",
				"kind":       "ConfigMap",
				"metadata": map[string]interface{}{
					"name":      "test-config",
					"namespace": "default",
				},
			},
		}

		req := events.NewWebhookValidationRequest(
			"v1.ConfigMap",
			"default",
			"test-config",
			obj,
			"CREATE",
		)

		component.handleEvent(req)

		// Wait for response
		timeout := time.After(1 * time.Second)
		select {
		case event := <-eventChan:
			resp, ok := event.(*events.WebhookValidationResponse)
			require.True(t, ok, "expected WebhookValidationResponse")
			assert.True(t, resp.Allowed)
		case <-timeout:
			t.Fatal("timeout waiting for validation response")
		}
	})

	t.Run("ignores other event types", func(t *testing.T) {
		eventBus := busevents.NewEventBus(100)
		eventChan := eventBus.Subscribe("test-sub", 50)
		component := NewBasicValidatorComponent(eventBus, testLogger())
		eventBus.Start()

		// Create a different event type
		otherEvent := events.NewValidationStartedEvent()
		component.handleEvent(otherEvent)

		// Should not produce any response
		select {
		case <-eventChan:
			t.Fatal("should not receive response for non-validation event")
		case <-time.After(100 * time.Millisecond):
			// Expected - no response
		}
	})
}

func TestBasicValidatorComponent_Start(t *testing.T) {
	t.Run("processes events until context cancelled", func(t *testing.T) {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()

		eventBus := busevents.NewEventBus(100)
		eventChan := eventBus.Subscribe("test-sub", 50)
		component := NewBasicValidatorComponent(eventBus, testLogger())
		eventBus.Start()

		// Start component in goroutine
		done := make(chan error, 1)
		go func() {
			done <- component.Start(ctx)
		}()

		// Give component time to start
		time.Sleep(50 * time.Millisecond)

		// Publish a validation request
		obj := &unstructured.Unstructured{
			Object: map[string]interface{}{
				"apiVersion": "v1",
				"kind":       "ConfigMap",
				"metadata": map[string]interface{}{
					"name":      "test-config",
					"namespace": "default",
				},
			},
		}

		req := events.NewWebhookValidationRequest(
			"v1.ConfigMap",
			"default",
			"test-config",
			obj,
			"CREATE",
		)
		eventBus.Publish(req)

		// Wait for response (skip other events like the request we just published)
		timeout := time.After(1 * time.Second)
	waitLoop:
		for {
			select {
			case event := <-eventChan:
				if resp, ok := event.(*events.WebhookValidationResponse); ok {
					assert.True(t, resp.Allowed)
					break waitLoop
				}
				// Skip other event types (like the request we just published)
			case <-timeout:
				t.Fatal("timeout waiting for validation response")
			}
		}

		// Cancel context and verify clean shutdown
		cancel()

		select {
		case err := <-done:
			assert.NoError(t, err)
		case <-time.After(1 * time.Second):
			t.Fatal("component did not shut down in time")
		}
	})
}
