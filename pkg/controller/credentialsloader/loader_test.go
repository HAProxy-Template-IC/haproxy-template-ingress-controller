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
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"

	"haproxy-template-ic/pkg/controller/events"
	"haproxy-template-ic/pkg/controller/testutil"
	"haproxy-template-ic/pkg/core/config"
)

// createCredentialsSecret creates an unstructured Secret using testutil helper.
func createCredentialsSecret(version string, data map[string]string) *unstructured.Unstructured {
	return testutil.CreateTestSecretWithStringData("credentials-secret", "test-namespace", version, data)
}

func TestNewCredentialsLoaderComponent(t *testing.T) {
	bus, logger := testutil.NewTestBusAndLogger()
	component := NewCredentialsLoaderComponent(bus, logger)

	require.NotNil(t, component)
	assert.Equal(t, bus, component.eventBus)
	assert.Equal(t, logger, component.logger)
	assert.NotNil(t, component.stopCh)
}

func TestCredentialsLoaderComponent_StartAndStop(t *testing.T) {
	bus, logger := testutil.NewTestBusAndLogger()
	component := NewCredentialsLoaderComponent(bus, logger)
	testutil.RunComponentStartStop(t, bus, component.Start, component.Stop)
}

func TestCredentialsLoaderComponent_StartWithContextCancel(t *testing.T) {
	bus, logger := testutil.NewTestBusAndLogger()
	component := NewCredentialsLoaderComponent(bus, logger)
	testutil.RunComponentContextCancel(t, bus, component.Start)
}

func TestCredentialsLoaderComponent_ProcessValidCredentials(t *testing.T) {
	bus, logger := testutil.NewTestBusAndLogger()
	component := NewCredentialsLoaderComponent(bus, logger)

	eventChan := bus.Subscribe(50)
	bus.Start()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go component.Start(ctx)
	time.Sleep(testutil.StartupDelay)

	secret := createCredentialsSecret("12345", map[string]string{
		"dataplane_username": "admin",
		"dataplane_password": "secret123",
	})

	bus.Publish(events.NewSecretResourceChangedEvent(secret))

	updated := testutil.WaitForEvent[*events.CredentialsUpdatedEvent](t, eventChan, testutil.LongTimeout)
	creds, ok := updated.Credentials.(*config.Credentials)
	require.True(t, ok, "expected *config.Credentials")
	assert.Equal(t, "admin", creds.DataplaneUsername)
	assert.Equal(t, "secret123", creds.DataplanePassword)
	assert.Equal(t, "12345", updated.SecretVersion)
}

func TestCredentialsLoaderComponent_InvalidResourceType(t *testing.T) {
	bus, logger := testutil.NewTestBusAndLogger()
	component := NewCredentialsLoaderComponent(bus, logger)

	eventChan := bus.Subscribe(50)
	bus.Start()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go component.Start(ctx)
	time.Sleep(testutil.StartupDelay)

	bus.Publish(events.NewSecretResourceChangedEvent("not-an-unstructured"))

	testutil.AssertNoEvent[*events.CredentialsUpdatedEvent](t, eventChan, testutil.NoEventTimeout)
}

func TestCredentialsLoaderComponent_MissingDataField(t *testing.T) {
	bus, logger := testutil.NewTestBusAndLogger()
	component := NewCredentialsLoaderComponent(bus, logger)

	eventChan := bus.Subscribe(50)
	bus.Start()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go component.Start(ctx)
	time.Sleep(testutil.StartupDelay)

	secret := &unstructured.Unstructured{}
	secret.SetKind("Secret")
	secret.SetResourceVersion("12345")

	bus.Publish(events.NewSecretResourceChangedEvent(secret))

	invalid := testutil.WaitForEvent[*events.CredentialsInvalidEvent](t, eventChan, testutil.LongTimeout)
	assert.Equal(t, "12345", invalid.SecretVersion)
	assert.Contains(t, invalid.Error, "no data field")
}

func TestCredentialsLoaderComponent_NonStringDataValue(t *testing.T) {
	bus, logger := testutil.NewTestBusAndLogger()
	component := NewCredentialsLoaderComponent(bus, logger)

	eventChan := bus.Subscribe(50)
	bus.Start()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go component.Start(ctx)
	time.Sleep(testutil.StartupDelay)

	// Create secret with non-string value by directly setting the object map.
	// We use float64 here because Go's JSON unmarshaling converts numbers to float64,
	// which is deep-copyable (unlike int which causes a panic in NestedMap).
	secret := &unstructured.Unstructured{
		Object: map[string]interface{}{
			"apiVersion": "v1",
			"kind":       "Secret",
			"metadata": map[string]interface{}{
				"resourceVersion": "12345",
			},
			"data": map[string]interface{}{
				"dataplane_username": float64(12345), // Invalid - should be string
				"dataplane_password": "secret",
			},
		},
	}

	bus.Publish(events.NewSecretResourceChangedEvent(secret))

	invalid := testutil.WaitForEvent[*events.CredentialsInvalidEvent](t, eventChan, testutil.LongTimeout)
	assert.Equal(t, "12345", invalid.SecretVersion)
	assert.Contains(t, invalid.Error, "invalid type")
}

func TestCredentialsLoaderComponent_MissingRequiredCredentials(t *testing.T) {
	tests := []struct {
		name          string
		data          map[string]string
		expectedError string
	}{
		{
			name:          "missing dataplane_username",
			data:          map[string]string{"dataplane_password": "secret"},
			expectedError: "dataplane_username",
		},
		{
			name:          "missing dataplane_password",
			data:          map[string]string{"dataplane_username": "admin"},
			expectedError: "dataplane_password",
		},
		{
			name:          "empty dataplane_username",
			data:          map[string]string{"dataplane_username": "", "dataplane_password": "secret"},
			expectedError: "dataplane_username",
		},
		{
			name:          "empty dataplane_password",
			data:          map[string]string{"dataplane_username": "admin", "dataplane_password": ""},
			expectedError: "dataplane_password",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			bus, logger := testutil.NewTestBusAndLogger()
			component := NewCredentialsLoaderComponent(bus, logger)

			eventChan := bus.Subscribe(50)
			bus.Start()

			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()

			go component.Start(ctx)
			time.Sleep(testutil.StartupDelay)

			secret := createCredentialsSecret("12345", tt.data)
			bus.Publish(events.NewSecretResourceChangedEvent(secret))

			invalid := testutil.WaitForEvent[*events.CredentialsInvalidEvent](t, eventChan, testutil.LongTimeout)
			assert.Equal(t, "12345", invalid.SecretVersion)
			assert.Contains(t, invalid.Error, tt.expectedError)
		})
	}
}

func TestCredentialsLoaderComponent_IgnoresOtherEvents(t *testing.T) {
	bus, logger := testutil.NewTestBusAndLogger()
	component := NewCredentialsLoaderComponent(bus, logger)

	eventChan := bus.Subscribe(50)
	bus.Start()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go component.Start(ctx)
	time.Sleep(testutil.StartupDelay)

	// Publish some other event type (ConfigParsedEvent)
	bus.Publish(events.NewConfigParsedEvent(nil, nil, "v1", ""))

	// Then publish a valid secret event
	secret := createCredentialsSecret("12345", map[string]string{
		"dataplane_username": "admin",
		"dataplane_password": "secret123",
	})
	bus.Publish(events.NewSecretResourceChangedEvent(secret))

	// Should receive CredentialsUpdatedEvent for the valid secret
	updated := testutil.WaitForEvent[*events.CredentialsUpdatedEvent](t, eventChan, testutil.LongTimeout)
	creds, ok := updated.Credentials.(*config.Credentials)
	require.True(t, ok, "expected *config.Credentials")
	assert.Equal(t, "admin", creds.DataplaneUsername)
}
