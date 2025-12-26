// Package testutil provides shared test helpers for controller package tests.
// These helpers reduce code duplication across test files by providing:
// - Standard EventBus and Logger creation
// - Event waiting utilities with timeout
// - Component lifecycle testing helpers
// - Timing constants to replace magic numbers
package testutil

import (
	"context"
	"encoding/base64"
	"log/slog"
	"os"
	"testing"
	"time"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"

	busevents "gitlab.com/haproxy-haptic/haptic/pkg/events"
)

// Timing constants to replace magic numbers in tests.
// These provide consistent, documented values for test synchronization.
const (
	// StartupDelay is the time to wait for components to initialize after starting.
	StartupDelay = 50 * time.Millisecond

	// DebounceWait is the time to wait for debounce timers to fire.
	DebounceWait = 100 * time.Millisecond

	// EventTimeout is the default timeout for waiting on a single event.
	EventTimeout = 500 * time.Millisecond

	// LongTimeout is used for operations that may take longer (e.g., component stop).
	LongTimeout = 1 * time.Second

	// VeryLongTimeout is used for integration-style tests within unit test files.
	VeryLongTimeout = 2 * time.Second

	// NoEventTimeout is a shorter timeout for verifying no event is received.
	NoEventTimeout = 200 * time.Millisecond
)

// NewTestBus creates an EventBus with a standard buffer size for tests.
func NewTestBus() *busevents.EventBus {
	return busevents.NewEventBus(100)
}

// NewTestLogger creates a logger that writes to stderr with error level.
// This minimizes noise in test output while still capturing errors.
func NewTestLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{
		Level: slog.LevelError,
	}))
}

// NewTestBusAndLogger creates both an EventBus and Logger for tests.
// This is the most common setup pattern across controller tests.
func NewTestBusAndLogger() (*busevents.EventBus, *slog.Logger) {
	return NewTestBus(), NewTestLogger()
}

// WaitForEvent waits for an event of type T on the channel, failing the test on timeout.
// It skips over events of other types until finding a matching one.
//
// Example:
//
//	event := testutil.WaitForEvent[*events.ConfigParsedEvent](t, eventChan, testutil.EventTimeout)
//	assert.Equal(t, "v1", event.Version)
func WaitForEvent[T any](t *testing.T, eventChan <-chan busevents.Event, timeout time.Duration) T {
	t.Helper()
	timer := time.After(timeout)
	for {
		select {
		case event := <-eventChan:
			if e, ok := event.(T); ok {
				return e
			}
			// Skip non-matching events
		case <-timer:
			var zero T
			t.Fatalf("timeout waiting for event of type %T", zero)
			return zero
		}
	}
}

// WaitForEventWithPredicate waits for an event of type T that satisfies the predicate.
// Useful when you need to match on event fields, not just type.
//
// Example:
//
//	event := testutil.WaitForEventWithPredicate(t, eventChan, testutil.EventTimeout,
//	    func(e *events.ConfigParsedEvent) bool { return e.Version == "v2" })
func WaitForEventWithPredicate[T any](t *testing.T, eventChan <-chan busevents.Event, timeout time.Duration, predicate func(T) bool) T {
	t.Helper()
	timer := time.After(timeout)
	for {
		select {
		case event := <-eventChan:
			if e, ok := event.(T); ok && predicate(e) {
				return e
			}
			// Skip non-matching events
		case <-timer:
			var zero T
			t.Fatalf("timeout waiting for event of type %T matching predicate", zero)
			return zero
		}
	}
}

// AssertNoEvent verifies that no event of type T is received within the timeout.
// This is useful for testing that certain events are NOT published.
//
// Example:
//
//	testutil.AssertNoEvent[*events.ConfigParsedEvent](t, eventChan, testutil.NoEventTimeout)
func AssertNoEvent[T any](t *testing.T, eventChan <-chan busevents.Event, timeout time.Duration) {
	t.Helper()
	timer := time.After(timeout)
	for {
		select {
		case event := <-eventChan:
			if _, ok := event.(T); ok {
				var zero T
				t.Fatalf("unexpected event of type %T received", zero)
			}
			// Skip non-matching events
		case <-timer:
			return // Expected - no matching event received
		}
	}
}

// DrainChannel empties all pending events from the channel without blocking.
// Useful for clearing events between test phases.
func DrainChannel(eventChan <-chan busevents.Event) {
	for {
		select {
		case <-eventChan:
		default:
			return
		}
	}
}

// ComponentStopTest tests that a component stops cleanly when Stop() is called.
// This is a common pattern across controller components.
//
// The startFunc should start the component in blocking mode (e.g., component.Start(ctx)).
// The stopFunc should trigger the component to stop (e.g., component.Stop()).
//
// Example:
//
//	testutil.ComponentStopTest(t, bus,
//	    func(ctx context.Context) { component.Start(ctx) },
//	    func() { component.Stop() })
func ComponentStopTest(t *testing.T, bus *busevents.EventBus, startFunc, stopFunc func()) {
	t.Helper()
	bus.Start()

	done := make(chan struct{})
	go func() {
		startFunc()
		close(done)
	}()

	// Give component time to start
	time.Sleep(StartupDelay)

	// Trigger stop
	stopFunc()

	// Verify component stopped
	select {
	case <-done:
		// Success - component stopped
	case <-time.After(LongTimeout):
		t.Fatal("timeout waiting for component to stop")
	}
}

// RunComponentStartStop tests that a component starts and stops cleanly.
// This reduces boilerplate for the common test pattern in controller components.
//
// Example:
//
//	func TestCertLoaderComponent_StartAndStop(t *testing.T) {
//	    bus, logger := testutil.NewTestBusAndLogger()
//	    component := NewCertLoaderComponent(bus, logger)
//	    testutil.RunComponentStartStop(t, bus, component.Start, component.Stop)
//	}
func RunComponentStartStop(t *testing.T, bus *busevents.EventBus, startFunc func(context.Context) error, stopFunc func()) {
	t.Helper()
	bus.Start()

	errChan := make(chan error, 1)
	go func() {
		errChan <- startFunc(context.Background())
	}()

	time.Sleep(StartupDelay)
	stopFunc()

	select {
	case err := <-errChan:
		if err != nil {
			t.Fatalf("component Start returned unexpected error: %v", err)
		}
		// Success - component stopped cleanly
	case <-time.After(LongTimeout):
		t.Fatal("component did not stop in time")
	}
}

// RunComponentContextCancel tests that a component stops cleanly when context is cancelled.
// This reduces boilerplate for the common test pattern in controller components.
//
// Example:
//
//	func TestCertLoaderComponent_StartWithContextCancel(t *testing.T) {
//	    bus, logger := testutil.NewTestBusAndLogger()
//	    component := NewCertLoaderComponent(bus, logger)
//	    testutil.RunComponentContextCancel(t, bus, component.Start)
//	}
func RunComponentContextCancel(t *testing.T, bus *busevents.EventBus, startFunc func(context.Context) error) {
	t.Helper()
	bus.Start()

	ctx, cancel := context.WithCancel(context.Background())

	errChan := make(chan error, 1)
	go func() {
		errChan <- startFunc(ctx)
	}()

	time.Sleep(StartupDelay)
	cancel()

	select {
	case err := <-errChan:
		// nil and context.Canceled are both acceptable for graceful shutdown
		if err != nil && err != context.Canceled {
			t.Fatalf("component Start returned unexpected error: %v", err)
		}
		// Success - component stopped cleanly
	case <-time.After(LongTimeout):
		t.Fatal("component did not stop in time after context cancel")
	}
}

// CreateTestSecret creates an unstructured Secret for testing.
// This is a common fixture used by multiple loader components.
//
// Example:
//
//	secret := testutil.CreateTestSecret("test-secret", "test-ns", "12345", map[string]interface{}{
//	    "username": "admin",
//	    "password": "secret",
//	})
func CreateTestSecret(name, namespace, resourceVersion string, data map[string]interface{}) *unstructured.Unstructured {
	secret := &unstructured.Unstructured{}
	secret.SetKind("Secret")
	secret.SetAPIVersion("v1")
	secret.SetName(name)
	secret.SetNamespace(namespace)
	secret.SetResourceVersion(resourceVersion)

	if data != nil {
		if err := unstructured.SetNestedField(secret.Object, data, "data"); err != nil {
			panic(err)
		}
	}

	return secret
}

// CreateTestSecretWithTLS creates an unstructured TLS Secret for testing.
// The cert and key are base64-encoded as Kubernetes expects for Secret data.
//
// Example:
//
//	secret := testutil.CreateTestSecretWithTLS("my-tls", "default", "12345",
//	    []byte("-----BEGIN CERTIFICATE-----\n..."),
//	    []byte("-----BEGIN PRIVATE KEY-----\n..."))
func CreateTestSecretWithTLS(name, namespace, resourceVersion string, certPEM, keyPEM []byte) *unstructured.Unstructured {
	secret := &unstructured.Unstructured{}
	secret.SetKind("Secret")
	secret.SetAPIVersion("v1")
	secret.SetName(name)
	secret.SetNamespace(namespace)
	secret.SetResourceVersion(resourceVersion)

	data := map[string]interface{}{}
	if certPEM != nil {
		data["tls.crt"] = base64.StdEncoding.EncodeToString(certPEM)
	}
	if keyPEM != nil {
		data["tls.key"] = base64.StdEncoding.EncodeToString(keyPEM)
	}

	if len(data) > 0 {
		if err := unstructured.SetNestedField(secret.Object, data, "data"); err != nil {
			panic(err)
		}
	}

	return secret
}

// CreateTestSecretWithStringData creates an unstructured Secret with string data.
// This is useful for credentials secrets that don't need base64 encoding in tests.
//
// Example:
//
//	secret := testutil.CreateTestSecretWithStringData("creds", "default", "12345",
//	    map[string]string{"username": "admin", "password": "secret"})
func CreateTestSecretWithStringData(name, namespace, resourceVersion string, data map[string]string) *unstructured.Unstructured {
	secret := &unstructured.Unstructured{}
	secret.SetKind("Secret")
	secret.SetAPIVersion("v1")
	secret.SetName(name)
	secret.SetNamespace(namespace)
	secret.SetResourceVersion(resourceVersion)

	if data != nil {
		dataMap := make(map[string]interface{})
		for k, v := range data {
			// Base64-encode the values to match how Kubernetes stores Secret data
			// when accessed through the unstructured API
			dataMap[k] = base64.StdEncoding.EncodeToString([]byte(v))
		}
		if err := unstructured.SetNestedField(secret.Object, dataMap, "data"); err != nil {
			panic(err)
		}
	}

	return secret
}

// CreateTestConfigMap creates an unstructured ConfigMap for testing.
//
// Example:
//
//	cm := testutil.CreateTestConfigMap("my-config", "default", "12345",
//	    map[string]string{"key": "value"})
func CreateTestConfigMap(name, namespace, resourceVersion string, data map[string]string) *unstructured.Unstructured {
	cm := &unstructured.Unstructured{}
	cm.SetKind("ConfigMap")
	cm.SetAPIVersion("v1")
	cm.SetName(name)
	cm.SetNamespace(namespace)
	cm.SetResourceVersion(resourceVersion)

	if data != nil {
		dataMap := make(map[string]interface{})
		for k, v := range data {
			dataMap[k] = v
		}
		if err := unstructured.SetNestedField(cm.Object, dataMap, "data"); err != nil {
			panic(err)
		}
	}

	return cm
}

// ValidHAProxyConfigTemplate is a minimal valid HAProxy configuration template
// that passes HAProxy syntax validation. Use this in tests that need to render
// and validate HAProxy configurations.
const ValidHAProxyConfigTemplate = `global
    log stdout format raw local0

defaults
    mode http
    timeout connect 5s
    timeout client 50s
    timeout server 50s

frontend test_frontend
    bind *:8080
    default_backend test_backend

backend test_backend
    server test_server 127.0.0.1:8081
`
