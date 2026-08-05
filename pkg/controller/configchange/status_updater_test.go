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
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	k8sfake "k8s.io/client-go/kubernetes/fake"
	clienttesting "k8s.io/client-go/testing"
	"k8s.io/client-go/tools/record"

	"gitlab.com/haproxy-haptic/haptic/pkg/apis/haproxytemplate/v1alpha1"
	"gitlab.com/haproxy-haptic/haptic/pkg/controller/events"
	"gitlab.com/haproxy-haptic/haptic/pkg/controller/testutil"
	crdclientfake "gitlab.com/haproxy-haptic/haptic/pkg/generated/clientset/versioned/fake"
)

const (
	testNamespace = "default"
	testName      = "test-htc"
	// testGeneration is the metadata.generation carried by the fixture config, so
	// status.observedGeneration and the Validated condition's observedGeneration
	// are assertable.
	testGeneration int64 = 3
)

func newHTC() *v1alpha1.HAProxyTemplateConfig {
	return &v1alpha1.HAProxyTemplateConfig{
		ObjectMeta: metav1.ObjectMeta{
			Namespace:  testNamespace,
			Name:       testName,
			Generation: testGeneration,
		},
	}
}

func newStatusUpdaterFixture(t *testing.T, existing ...runtime.Object) (*StatusUpdater, *crdclientfake.Clientset) {
	t.Helper()
	crdClient := crdclientfake.NewSimpleClientset(existing...)
	bus, logger := testutil.NewTestBusAndLogger()
	return NewStatusUpdater(crdClient, k8sfake.NewSimpleClientset(), bus, logger), crdClient
}

func TestStatusUpdater_EmitsEvents(t *testing.T) {
	u, _ := newStatusUpdaterFixture(t)
	rec := record.NewFakeRecorder(10)
	u.recorder = rec
	htc := &v1alpha1.HAProxyTemplateConfig{
		ObjectMeta: metav1.ObjectMeta{Namespace: testNamespace, Name: testName},
	}

	// Invalid -> a Warning Event carrying the first error (+ "more" hint).
	htc.Status = v1alpha1.HAProxyTemplateConfigStatus{
		ValidationStatus: statusInvalid,
		ValidationErrors: []string{"boom", "kaboom"},
	}
	u.emitStatusEvent(htc)
	assertNextEvent(t, rec, "Warning", eventReasonValidationFailed, "boom (+1 more)")

	// Invalid -> Valid is a recovery: a single Normal Event.
	htc.Status = v1alpha1.HAProxyTemplateConfigStatus{ValidationStatus: statusValid}
	u.emitStatusEvent(htc)
	assertNextEvent(t, rec, "Normal", eventReasonValidated, "valid again")

	// A subsequent routine Valid emits NOTHING (no spam on every success).
	u.emitStatusEvent(htc)
	select {
	case e := <-rec.Events:
		t.Fatalf("expected no event on routine success, got %q", e)
	default:
	}
}

func assertNextEvent(t *testing.T, rec *record.FakeRecorder, wantType, wantReason, wantMsgSubstr string) {
	t.Helper()
	select {
	case e := <-rec.Events:
		assert.Contains(t, e, wantType)
		assert.Contains(t, e, wantReason)
		if wantMsgSubstr != "" {
			assert.Contains(t, e, wantMsgSubstr)
		}
	default:
		t.Fatalf("expected an Event (%s/%s) but none was recorded", wantType, wantReason)
	}
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

	// observedGeneration records the generation the controller processed, and the
	// Validated condition reports it True with the same generation.
	assert.Equal(t, testGeneration, status.ObservedGeneration)
	cond := meta.FindStatusCondition(status.Conditions, conditionValidated)
	require.NotNil(t, cond)
	assert.Equal(t, metav1.ConditionTrue, cond.Status)
	assert.Equal(t, reasonValidationSucceeded, cond.Reason)
	assert.Equal(t, testGeneration, cond.ObservedGeneration)

	// Cached config references should be populated for subsequent HAProxy validation events.
	u.mu.RLock()
	require.Len(t, u.configRefs, 1)
	assert.Equal(t, testNamespace, u.configRefs[0].Namespace)
	assert.Equal(t, testName, u.configRefs[0].Name)
	assert.Equal(t, testGeneration, u.configRefs[0].Generation)
	u.mu.RUnlock()
}

// TestReportConfigLoadFailure covers the synchronous startup path: when the
// fatal load gate rejects the config, the iteration writes an Invalid status
// (observedGeneration + Validated=False/LoadGateFailed + the failing tests)
// before crash-looping, so operators can see WHY via kubectl.
func TestReportConfigLoadFailure(t *testing.T) {
	htc := newHTC()
	crdClient := crdclientfake.NewSimpleClientset(htc)
	_, logger := testutil.NewTestBusAndLogger()

	failures := []string{"test-ssl-x failed: boom", "test-ssl-y failed: kaboom"}
	ReportConfigLoadFailure(context.Background(), crdClient, events.ConfigSourceRef{
		Namespace: htc.Namespace, Name: htc.Name, Generation: htc.Generation,
	}, failures, logger)

	status := getStatus(t, crdClient)
	assert.Equal(t, "Invalid", status.ValidationStatus)
	assert.Equal(t, testGeneration, status.ObservedGeneration)
	assert.ElementsMatch(t, failures, status.ValidationErrors)
	assert.Contains(t, status.ValidationMessage, "startup load gate")

	cond := meta.FindStatusCondition(status.Conditions, conditionValidated)
	require.NotNil(t, cond)
	assert.Equal(t, metav1.ConditionFalse, cond.Status)
	assert.Equal(t, reasonLoadGateFailed, cond.Reason)
	assert.Equal(t, testGeneration, cond.ObservedGeneration)
	assert.Contains(t, cond.Message, "boom") // first failure surfaces in the condition
}

func TestReportConfigLoadFailure_GetError(t *testing.T) {
	// CRD not seeded — Get() returns NotFound; must not panic, just log + return.
	crdClient := crdclientfake.NewSimpleClientset()
	_, logger := testutil.NewTestBusAndLogger()
	htc := newHTC()
	ReportConfigLoadFailure(context.Background(), crdClient, events.ConfigSourceRef{
		Namespace: htc.Namespace, Name: htc.Name, Generation: htc.Generation,
	}, []string{"boom"}, logger)
}

func TestStatusUpdater_HandleConfigInvalid(t *testing.T) {
	htc := newHTC()
	u, crd := newStatusUpdaterFixture(t, htc)

	u.handleConfigInvalid(context.Background(), events.NewConfigInvalidEvent("v1", htc, map[string][]string{
		"template": {"boom", "kaboom"},
	}))

	status := getStatus(t, crd)
	assert.Equal(t, "Invalid", status.ValidationStatus)
	assert.Equal(t, testGeneration, status.ObservedGeneration)
	assert.ElementsMatch(t, []string{"boom", "kaboom"}, status.ValidationErrors)

	cond := meta.FindStatusCondition(status.Conditions, conditionValidated)
	require.NotNil(t, cond)
	assert.Equal(t, metav1.ConditionFalse, cond.Status)
	assert.Equal(t, reasonConfigInvalid, cond.Reason)
	assert.Equal(t, testGeneration, cond.ObservedGeneration)
	// The condition message surfaces the first error so `kubectl describe` is useful.
	assert.Contains(t, cond.Message, "boom")
}

// TestStatusUpdater_ObservedGenerationTracksValidatedGeneration pins the drift
// semantics: observedGeneration reflects the generation that was actually
// validated, not the (possibly newer) live spec — so a reader can tell the
// controller is behind without any controller-version field on the spec.
func TestStatusUpdater_ObservedGenerationTracksValidatedGeneration(t *testing.T) {
	stored := newHTC()
	stored.Generation = 5 // live spec has moved on
	u, crd := newStatusUpdaterFixture(t, stored)

	validated := newHTC()
	validated.Generation = 4 // but we validated the previous generation
	u.handleConfigValidated(context.Background(), events.NewConfigValidatedEvent(nil, validated, "v1", ""))

	status := getStatus(t, crd)
	assert.Equal(t, int64(4), status.ObservedGeneration)
	cond := meta.FindStatusCondition(status.Conditions, conditionValidated)
	require.NotNil(t, cond)
	assert.Equal(t, int64(4), cond.ObservedGeneration)
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
	require.Len(t, u.configRefs, 1)
	assert.Equal(t, testNamespace, u.configRefs[0].Namespace)
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
	require.Len(t, u.configRefs, 1)
	assert.Equal(t, testName, u.configRefs[0].Name)
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

// shard builds one member of a merged set.
func shard(name string, generation int64) *v1alpha1.HAProxyTemplateConfig {
	return &v1alpha1.HAProxyTemplateConfig{
		ObjectMeta: metav1.ObjectMeta{
			Namespace:  testNamespace,
			Name:       name,
			Generation: generation,
		},
	}
}

func shardStatus(t *testing.T, crdClient *crdclientfake.Clientset, name string) v1alpha1.HAProxyTemplateConfigStatus {
	t.Helper()
	got, err := crdClient.HaproxyTemplateICV1alpha1().
		HAProxyTemplateConfigs(testNamespace).
		Get(context.Background(), name, metav1.GetOptions{})
	require.NoError(t, err)
	return got.Status
}

// The verdict is a property of the merged set, so every source is stamped —
// with ITS OWN observedGeneration, because that field is only meaningful
// against the same object's metadata.generation. A designated primary could
// not represent a shard edit at all (ADR-0016).
func TestStatusUpdater_StampsEverySourceOfTheMergedSet(t *testing.T) {
	a, b, c := shard("haptic-config-base", 4), shard("haptic-config-ssl", 7), shard("haptic-config", 2)
	u, crdClient := newStatusUpdaterFixture(t, a, b, c)

	event := events.NewConfigValidatedEvent(nil, c, "v9", "")
	event.Sources = []events.ConfigSourceRef{
		{Namespace: testNamespace, Name: a.Name, Generation: a.Generation},
		{Namespace: testNamespace, Name: b.Name, Generation: b.Generation},
		{Namespace: testNamespace, Name: c.Name, Generation: c.Generation},
	}
	u.handleConfigValidated(context.Background(), event)

	for _, tc := range []struct {
		name       string
		generation int64
	}{
		{a.Name, a.Generation}, {b.Name, b.Generation}, {c.Name, c.Generation},
	} {
		status := shardStatus(t, crdClient, tc.name)
		assert.Equal(t, "Valid", status.ValidationStatus, tc.name)
		assert.Equal(t, tc.generation, status.ObservedGeneration,
			"%s must carry ITS OWN generation, not the merged identity's", tc.name)
		cond := meta.FindStatusCondition(status.Conditions, conditionValidated)
		require.NotNil(t, cond, tc.name)
		assert.Equal(t, tc.generation, cond.ObservedGeneration, tc.name)
	}
}

// A repeat verdict must not write: with N sources per set, N-1 statuses are
// usually unchanged, and rewriting them would churn resourceVersions for a
// timestamp refresh nobody reads.
func TestStatusUpdater_SkipsNoOpStatusWrites(t *testing.T) {
	htc := newHTC()
	u, crdClient := newStatusUpdaterFixture(t, htc)

	event := events.NewConfigValidatedEvent(nil, htc, "v1", "")
	u.handleConfigValidated(context.Background(), event)

	var writes int
	crdClient.PrependReactor("update", "haproxytemplateconfigs", func(a clienttesting.Action) (bool, runtime.Object, error) {
		if subAction, ok := a.(clienttesting.UpdateAction); ok && subAction.GetSubresource() == "status" {
			writes++
		}
		return false, nil, nil
	})

	// Same verdict again — everything but LastValidated would be identical.
	u.handleConfigValidated(context.Background(), events.NewConfigValidatedEvent(nil, htc, "v2", ""))
	assert.Zero(t, writes, "an unchanged status must not be rewritten")

	// A real change still writes.
	u.handleConfigInvalid(context.Background(), events.NewConfigInvalidEvent("v3", htc, map[string][]string{
		"basic": {"boom"},
	}))
	assert.Equal(t, 1, writes, "a changed status must still be written")
}
