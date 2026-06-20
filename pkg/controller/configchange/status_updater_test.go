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
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	clienttesting "k8s.io/client-go/testing"

	"gitlab.com/haproxy-haptic/haptic/pkg/apis/haproxytemplate/v1alpha1"
	"gitlab.com/haproxy-haptic/haptic/pkg/controller/events"
	"gitlab.com/haproxy-haptic/haptic/pkg/controller/testutil"
	crdclientfake "gitlab.com/haproxy-haptic/haptic/pkg/generated/clientset/versioned/fake"
)

const (
	testNamespace = "default"
	testName      = "test-htc"
)

func newHTC() *v1alpha1.HAProxyTemplateConfig {
	return &v1alpha1.HAProxyTemplateConfig{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: testNamespace,
			Name:      testName,
		},
	}
}

func newStatusUpdaterFixture(t *testing.T, existing ...runtime.Object) (*StatusUpdater, *crdclientfake.Clientset) {
	t.Helper()
	crdClient := crdclientfake.NewSimpleClientset(existing...)
	bus, logger := testutil.NewTestBusAndLogger()
	return NewStatusUpdater(crdClient, bus, logger), crdClient
}

func getStatus(t *testing.T, crdClient *crdclientfake.Clientset) v1alpha1.HAProxyTemplateConfigStatus {
	t.Helper()
	got, err := crdClient.HaproxyTemplateICV1alpha1().
		HAProxyTemplateConfigs(testNamespace).
		Get(context.Background(), testName, metav1.GetOptions{})
	require.NoError(t, err)
	return got.Status
}

func TestNewStatusUpdater(t *testing.T) {
	u, _ := newStatusUpdaterFixture(t)
	require.NotNil(t, u)
	assert.NotNil(t, u.Base)
	assert.Equal(t, StatusUpdaterComponentName, u.Name())
}

func TestStatusUpdater_HandleConfigValidated_Success(t *testing.T) {
	htc := newHTC()
	u, crd := newStatusUpdaterFixture(t, htc)

	u.handleConfigValidated(context.Background(), events.NewConfigValidatedEvent(nil, htc, "v1", ""))

	status := getStatus(t, crd)
	assert.Equal(t, "Valid", status.ValidationStatus)
	assert.Equal(t, "Configuration validated successfully", status.ValidationMessage)
	assert.Nil(t, status.ValidationErrors)
	assert.NotNil(t, status.LastValidated)

	// Cached config reference should be populated for subsequent HAProxy validation events.
	u.mu.RLock()
	assert.Equal(t, testNamespace, u.configNamespace)
	assert.Equal(t, testName, u.configName)
	u.mu.RUnlock()
}

func TestStatusUpdater_HandleConfigValidated_SkipsInitialVersion(t *testing.T) {
	htc := newHTC()
	u, crd := newStatusUpdaterFixture(t, htc)

	u.handleConfigValidated(context.Background(), events.NewConfigValidatedEvent(nil, htc, "initial", ""))

	// Nothing should have changed — ValidationStatus stays empty.
	assert.Empty(t, getStatus(t, crd).ValidationStatus)
}

func TestStatusUpdater_HandleConfigValidated_WrongTemplateType(t *testing.T) {
	u, crd := newStatusUpdaterFixture(t, newHTC())

	// TemplateConfig is not an *HAProxyTemplateConfig — handler should skip silently.
	u.handleConfigValidated(context.Background(), events.NewConfigValidatedEvent(nil, "not-a-crd", "v1", ""))

	assert.Empty(t, getStatus(t, crd).ValidationStatus)
}

func TestStatusUpdater_HandleConfigValidated_GetError(t *testing.T) {
	// CRD not seeded — Get() will return NotFound.
	u, crd := newStatusUpdaterFixture(t)
	htc := newHTC()

	// Must not panic or leave partial state.
	u.handleConfigValidated(context.Background(), events.NewConfigValidatedEvent(nil, htc, "v1", ""))

	_, err := crd.HaproxyTemplateICV1alpha1().
		HAProxyTemplateConfigs(testNamespace).
		Get(context.Background(), testName, metav1.GetOptions{})
	require.Error(t, err)

	// Cache was still populated before the Get() call — that's by design.
	u.mu.RLock()
	assert.Equal(t, testNamespace, u.configNamespace)
	u.mu.RUnlock()
}

func TestStatusUpdater_HandleConfigValidated_UpdateError(t *testing.T) {
	htc := newHTC()
	u, crd := newStatusUpdaterFixture(t, htc)

	// Make UpdateStatus fail to exercise the warning branch without affecting state.
	crd.PrependReactor("update", "haproxytemplateconfigs", func(a clienttesting.Action) (bool, runtime.Object, error) {
		if subAction, ok := a.(clienttesting.UpdateAction); ok && subAction.GetSubresource() == "status" {
			return true, nil, errors.New("forced update failure")
		}
		return false, nil, nil
	})

	// Must not panic.
	u.handleConfigValidated(context.Background(), events.NewConfigValidatedEvent(nil, htc, "v1", ""))
}

func TestStatusUpdater_HandleConfigInvalid_Success(t *testing.T) {
	htc := newHTC()
	u, crd := newStatusUpdaterFixture(t, htc)

	errs := map[string][]string{
		"basic":    {"template is empty"},
		"template": {"unknown identifier", "syntax error at line 3"},
	}

	u.handleConfigInvalid(context.Background(), events.NewConfigInvalidEvent("v2", htc, errs))

	status := getStatus(t, crd)
	assert.Equal(t, "Invalid", status.ValidationStatus)
	assert.Len(t, status.ValidationErrors, 3)
	assert.Contains(t, status.ValidationMessage, "3 validation error(s)")
	assert.NotNil(t, status.LastValidated)

	u.mu.RLock()
	assert.Equal(t, testName, u.configName)
	u.mu.RUnlock()
}

func TestStatusUpdater_HandleConfigInvalid_WrongTemplateType(t *testing.T) {
	u, crd := newStatusUpdaterFixture(t, newHTC())

	u.handleConfigInvalid(context.Background(), events.NewConfigInvalidEvent("v2", "not-a-crd", nil))
	assert.Empty(t, getStatus(t, crd).ValidationStatus)
}

func TestStatusUpdater_HandleConfigInvalid_GetError(t *testing.T) {
	u, _ := newStatusUpdaterFixture(t)
	u.handleConfigInvalid(context.Background(), events.NewConfigInvalidEvent("v2", newHTC(), nil))
}

func TestStatusUpdater_HandleHAProxyValidationFailed_WithoutCachedRef(t *testing.T) {
	u, crd := newStatusUpdaterFixture(t, newHTC())

	// No prior handleConfigValidated/Invalid call — cache is empty, handler should no-op.
	u.handleHAProxyValidationFailed(context.Background(), events.NewValidationFailedEvent([]string{"err"}, 42, "test"))

	assert.Empty(t, getStatus(t, crd).ValidationStatus)
}

func TestStatusUpdater_HandleHAProxyValidationFailed_Success(t *testing.T) {
	htc := newHTC()
	u, crd := newStatusUpdaterFixture(t, htc)

	// Prime cache via a previous ConfigValidated handler call.
	u.handleConfigValidated(context.Background(), events.NewConfigValidatedEvent(nil, htc, "v1", ""))

	errs := []string{"haproxy: line 42: unknown keyword"}
	u.handleHAProxyValidationFailed(context.Background(), events.NewValidationFailedEvent(errs, 100, "reconcile"))

	status := getStatus(t, crd)
	assert.Equal(t, "Invalid", status.ValidationStatus)
	assert.Equal(t, "HAProxy configuration validation failed", status.ValidationMessage)
	assert.Equal(t, errs, status.ValidationErrors)
}

func TestStatusUpdater_HandleHAProxyValidationFailed_GetError(t *testing.T) {
	// Seed an HTC so cache can be populated, then delete it so the subsequent Get fails.
	htc := newHTC()
	u, crd := newStatusUpdaterFixture(t, htc)
	u.handleConfigValidated(context.Background(), events.NewConfigValidatedEvent(nil, htc, "v1", ""))
	require.NoError(t, crd.HaproxyTemplateICV1alpha1().HAProxyTemplateConfigs(testNamespace).Delete(context.Background(), testName, metav1.DeleteOptions{}))

	u.handleHAProxyValidationFailed(context.Background(), events.NewValidationFailedEvent([]string{"x"}, 1, ""))
}

// TestStatusUpdater_Integration exercises the Start/Stop loop end-to-end by publishing
// one event of each handled type through the real event bus and asserting the CRD
// reflects the final state.
func TestStatusUpdater_Integration(t *testing.T) {
	htc := newHTC()
	u, crd := newStatusUpdaterFixture(t, htc)
	u.EventBus().Start()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	done := make(chan struct{})
	go func() {
		_ = u.Start(ctx)
		close(done)
	}()

	u.EventBus().Publish(events.NewConfigValidatedEvent(nil, htc, "v1", ""))

	require.Eventually(t, func() bool {
		return getStatus(t, crd).ValidationStatus == "Valid"
	}, 2*time.Second, 10*time.Millisecond)

	u.EventBus().Publish(events.NewConfigInvalidEvent("v2", htc, map[string][]string{"basic": {"err"}}))

	require.Eventually(t, func() bool {
		s := getStatus(t, crd)
		return s.ValidationStatus == "Invalid" && len(s.ValidationErrors) == 1
	}, 2*time.Second, 10*time.Millisecond)

	u.EventBus().Publish(events.NewValidationFailedEvent([]string{"haproxy: bad"}, 5, ""))

	require.Eventually(t, func() bool {
		s := getStatus(t, crd)
		return s.ValidationMessage == "HAProxy configuration validation failed"
	}, 2*time.Second, 10*time.Millisecond)

	u.Stop()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("StatusUpdater did not stop in time")
	}
}

// TestStatusUpdater_DoubleStop verifies Stop is idempotent (sync.Once via
// component.Base): a second call must not panic on an already-closed channel.
func TestStatusUpdater_DoubleStop(t *testing.T) {
	u, _ := newStatusUpdaterFixture(t, newHTC())
	u.Stop()
	assert.NotPanics(t, u.Stop)
}

// TestStatusUpdater_StopViaContext verifies context cancellation shuts down Start().
func TestStatusUpdater_StopViaContext(t *testing.T) {
	u, _ := newStatusUpdaterFixture(t, newHTC())
	u.EventBus().Start()

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		_ = u.Start(ctx)
		close(done)
	}()

	cancel()

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("Start did not return after context cancel")
	}
}

// Sanity check: HTC error status text is correctly formatted as N validation error(s).
func TestStatusUpdater_ErrorCountMessage(t *testing.T) {
	htc := newHTC()
	u, crd := newStatusUpdaterFixture(t, htc)
	errs := map[string][]string{"a": {"1", "2"}, "b": {"3"}}
	u.handleConfigInvalid(context.Background(), events.NewConfigInvalidEvent("v3", htc, errs))
	assert.Equal(t, fmt.Sprintf("%d validation error(s)", 3), getStatus(t, crd).ValidationMessage)
}
