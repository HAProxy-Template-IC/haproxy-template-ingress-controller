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

package certloader

import (
	"context"
	"encoding/base64"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"

	"haproxy-template-ic/pkg/controller/events"
	"haproxy-template-ic/pkg/controller/testutil"
)

// createTestSecret creates an unstructured TLS Secret using testutil helper.
func createTestSecret(version string, tlsCrt, tlsKey []byte) *unstructured.Unstructured {
	return testutil.CreateTestSecretWithTLS("test-secret", "test-namespace", version, tlsCrt, tlsKey)
}

func TestNewCertLoaderComponent(t *testing.T) {
	bus, logger := testutil.NewTestBusAndLogger()
	component := NewCertLoaderComponent(bus, logger)

	require.NotNil(t, component)
	assert.Equal(t, bus, component.eventBus)
	assert.Equal(t, logger, component.logger)
	assert.NotNil(t, component.stopCh)
}

func TestCertLoaderComponent_StartAndStop(t *testing.T) {
	bus, logger := testutil.NewTestBusAndLogger()
	component := NewCertLoaderComponent(bus, logger)
	testutil.RunComponentStartStop(t, bus, component.Start, component.Stop)
}

func TestCertLoaderComponent_StartWithContextCancel(t *testing.T) {
	bus, logger := testutil.NewTestBusAndLogger()
	component := NewCertLoaderComponent(bus, logger)
	testutil.RunComponentContextCancel(t, bus, component.Start)
}

func TestCertLoaderComponent_ProcessValidCert(t *testing.T) {
	bus, logger := testutil.NewTestBusAndLogger()
	component := NewCertLoaderComponent(bus, logger)

	eventChan := bus.Subscribe(50)
	bus.Start()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go component.Start(ctx)
	time.Sleep(testutil.StartupDelay)

	// Create valid TLS secret
	testCert := []byte("-----BEGIN CERTIFICATE-----\ntest cert data\n-----END CERTIFICATE-----")
	testKey := []byte("-----BEGIN PRIVATE KEY-----\ntest key data\n-----END PRIVATE KEY-----")
	secret := createTestSecret("12345", testCert, testKey)

	bus.Publish(events.NewCertResourceChangedEvent(secret))

	parsed := testutil.WaitForEvent[*events.CertParsedEvent](t, eventChan, testutil.LongTimeout)
	assert.Equal(t, testCert, parsed.CertPEM)
	assert.Equal(t, testKey, parsed.KeyPEM)
	assert.Equal(t, "12345", parsed.Version)
}

func TestCertLoaderComponent_InvalidResourceType(t *testing.T) {
	bus, logger := testutil.NewTestBusAndLogger()
	component := NewCertLoaderComponent(bus, logger)

	eventChan := bus.Subscribe(50)
	bus.Start()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go component.Start(ctx)
	time.Sleep(testutil.StartupDelay)

	// Publish event with invalid resource type
	bus.Publish(events.NewCertResourceChangedEvent("not-an-unstructured"))

	testutil.AssertNoEvent[*events.CertParsedEvent](t, eventChan, testutil.NoEventTimeout)
}

func TestCertLoaderComponent_MissingDataField(t *testing.T) {
	bus, logger := testutil.NewTestBusAndLogger()
	component := NewCertLoaderComponent(bus, logger)

	eventChan := bus.Subscribe(50)
	bus.Start()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go component.Start(ctx)
	time.Sleep(testutil.StartupDelay)

	// Create secret without data field
	secret := &unstructured.Unstructured{}
	secret.SetKind("Secret")
	secret.SetResourceVersion("12345")

	bus.Publish(events.NewCertResourceChangedEvent(secret))

	testutil.AssertNoEvent[*events.CertParsedEvent](t, eventChan, testutil.NoEventTimeout)
}

func TestCertLoaderComponent_MissingTlsCrt(t *testing.T) {
	bus, logger := testutil.NewTestBusAndLogger()
	component := NewCertLoaderComponent(bus, logger)

	eventChan := bus.Subscribe(50)
	bus.Start()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go component.Start(ctx)
	time.Sleep(testutil.StartupDelay)

	// Create secret with only tls.key
	secret := &unstructured.Unstructured{}
	secret.SetKind("Secret")
	secret.SetResourceVersion("12345")
	err := unstructured.SetNestedField(secret.Object, map[string]interface{}{
		"tls.key": base64.StdEncoding.EncodeToString([]byte("key data")),
	}, "data")
	require.NoError(t, err)

	bus.Publish(events.NewCertResourceChangedEvent(secret))

	testutil.AssertNoEvent[*events.CertParsedEvent](t, eventChan, testutil.NoEventTimeout)
}

func TestCertLoaderComponent_MissingTlsKey(t *testing.T) {
	bus, logger := testutil.NewTestBusAndLogger()
	component := NewCertLoaderComponent(bus, logger)

	eventChan := bus.Subscribe(50)
	bus.Start()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go component.Start(ctx)
	time.Sleep(testutil.StartupDelay)

	// Create secret with only tls.crt
	secret := &unstructured.Unstructured{}
	secret.SetKind("Secret")
	secret.SetResourceVersion("12345")
	err := unstructured.SetNestedField(secret.Object, map[string]interface{}{
		"tls.crt": base64.StdEncoding.EncodeToString([]byte("cert data")),
	}, "data")
	require.NoError(t, err)

	bus.Publish(events.NewCertResourceChangedEvent(secret))

	testutil.AssertNoEvent[*events.CertParsedEvent](t, eventChan, testutil.NoEventTimeout)
}

func TestCertLoaderComponent_InvalidBase64Cert(t *testing.T) {
	bus, logger := testutil.NewTestBusAndLogger()
	component := NewCertLoaderComponent(bus, logger)

	eventChan := bus.Subscribe(50)
	bus.Start()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go component.Start(ctx)
	time.Sleep(testutil.StartupDelay)

	// Create secret with invalid base64 in tls.crt
	secret := &unstructured.Unstructured{}
	secret.SetKind("Secret")
	secret.SetResourceVersion("12345")
	err := unstructured.SetNestedField(secret.Object, map[string]interface{}{
		"tls.crt": "not-valid-base64!!!",
		"tls.key": base64.StdEncoding.EncodeToString([]byte("key data")),
	}, "data")
	require.NoError(t, err)

	bus.Publish(events.NewCertResourceChangedEvent(secret))

	testutil.AssertNoEvent[*events.CertParsedEvent](t, eventChan, testutil.NoEventTimeout)
}

func TestCertLoaderComponent_InvalidBase64Key(t *testing.T) {
	bus, logger := testutil.NewTestBusAndLogger()
	component := NewCertLoaderComponent(bus, logger)

	eventChan := bus.Subscribe(50)
	bus.Start()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go component.Start(ctx)
	time.Sleep(testutil.StartupDelay)

	// Create secret with invalid base64 in tls.key
	secret := &unstructured.Unstructured{}
	secret.SetKind("Secret")
	secret.SetResourceVersion("12345")
	err := unstructured.SetNestedField(secret.Object, map[string]interface{}{
		"tls.crt": base64.StdEncoding.EncodeToString([]byte("cert data")),
		"tls.key": "not-valid-base64!!!",
	}, "data")
	require.NoError(t, err)

	bus.Publish(events.NewCertResourceChangedEvent(secret))

	testutil.AssertNoEvent[*events.CertParsedEvent](t, eventChan, testutil.NoEventTimeout)
}

func TestDecodeBase64SecretValue(t *testing.T) {
	tests := []struct {
		name      string
		value     interface{}
		want      []byte
		wantError bool
	}{
		{
			name:      "valid base64 string",
			value:     base64.StdEncoding.EncodeToString([]byte("hello world")),
			want:      []byte("hello world"),
			wantError: false,
		},
		{
			name:      "byte slice already decoded",
			value:     []byte("already decoded"),
			want:      []byte("already decoded"),
			wantError: false,
		},
		{
			name:      "empty string",
			value:     "",
			want:      []byte{},
			wantError: false,
		},
		{
			name:      "invalid base64",
			value:     "not-valid-base64!!!",
			want:      nil,
			wantError: true,
		},
		{
			name:      "unexpected type (int)",
			value:     12345,
			want:      nil,
			wantError: true,
		},
		{
			name:      "unexpected type (nil)",
			value:     nil,
			want:      nil,
			wantError: true,
		},
		{
			name:      "unexpected type (map)",
			value:     map[string]string{"key": "value"},
			want:      nil,
			wantError: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := decodeBase64SecretValue(tt.value)

			if tt.wantError {
				require.Error(t, err)
				assert.Nil(t, got)
			} else {
				require.NoError(t, err)
				assert.Equal(t, tt.want, got)
			}
		})
	}
}

func TestCertLoaderComponent_IgnoresOtherEvents(t *testing.T) {
	bus, logger := testutil.NewTestBusAndLogger()
	component := NewCertLoaderComponent(bus, logger)

	eventChan := bus.Subscribe(50)
	bus.Start()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go component.Start(ctx)
	time.Sleep(testutil.StartupDelay)

	// Publish some other event type
	bus.Publish(events.NewConfigParsedEvent(nil, nil, "v1", ""))

	// Then publish a valid cert event
	testCert := []byte("cert data")
	testKey := []byte("key data")
	secret := createTestSecret("12345", testCert, testKey)
	bus.Publish(events.NewCertResourceChangedEvent(secret))

	// Should receive CertParsedEvent for the valid cert
	parsed := testutil.WaitForEvent[*events.CertParsedEvent](t, eventChan, testutil.LongTimeout)
	assert.Equal(t, testCert, parsed.CertPEM)
	assert.Equal(t, testKey, parsed.KeyPEM)
}
