// Copyright 2026 Philipp Hossner
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

package leaderelection

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	coordinationv1 "k8s.io/api/coordination/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/kubernetes/fake"
	k8stesting "k8s.io/client-go/testing"

	"gitlab.com/haproxy-haptic/haptic/pkg/controller/events"
	"gitlab.com/haproxy-haptic/haptic/pkg/controller/testutil"
	k8sleaderelection "gitlab.com/haproxy-haptic/haptic/pkg/k8s/leaderelection"
)

// failingLeases makes Lease updates fail the way a briefly unavailable
// apiserver does — a non-conflict error, which client-go's conflict retry does
// not cover. A negative count never recovers.
func failingLeases(clientset *fake.Clientset, failures int) *int {
	attempts := 0
	clientset.PrependReactor("update", "leases", func(k8stesting.Action) (bool, runtime.Object, error) {
		attempts++
		if failures < 0 || attempts <= failures {
			return true, nil, apierrors.NewInternalError(errors.New("apiserver is having a moment"))
		}
		return false, nil, nil
	})
	return &attempts
}

func claimTestComponent(t *testing.T, failures int) (*Component, *int, *[]string, <-chan any) {
	t.Helper()
	bus, logger := testutil.NewTestBusAndLogger()
	clientset := fake.NewClientset(&coordinationv1.Lease{
		ObjectMeta: metav1.ObjectMeta{Name: "test-lease", Namespace: "test-ns"},
	})
	attempts := failingLeases(clientset, failures)
	stoodDown := &[]string{}
	events := bus.Subscribe("test-sub", 10)
	bus.Start()

	component := &Component{
		eventBus:  bus,
		logger:    logger,
		identity:  "test-pod",
		leaseName: "test-lease",
		epoch: NewTerm(
			NewLeaseEpoch(clientset, "test-ns", "test-lease", "test-pod", logger),
			func(reason string) { *stoodDown = append(*stoodDown, reason) },
		),
	}
	forwarded := make(chan any, 10)
	go func() {
		for event := range events {
			forwarded <- event
		}
	}()
	return component, attempts, stoodDown, forwarded
}

// The apiserver blip that costs the previous leader its lease is exactly what
// can cost the new one its epoch claim. A transient failure is retried, and the
// term starts on the epoch it claimed.
func TestOnStartedLeadingRetriesATransientEpochClaimFailure(t *testing.T) {
	component, attempts, stoodDown, _ := claimTestComponent(t, 2)
	started := false
	callbacks := component.wrapCallbacks("test-pod",
		k8sleaderelection.Callbacks{OnStartedLeading: func(context.Context) { started = true }})

	callbacks.OnStartedLeading(context.Background())

	assert.Equal(t, 3, *attempts, "two failures, then the claim that lands")
	assert.Equal(t, uint64(1), component.epoch.LeaderEpoch())
	assert.True(t, started, "the term claimed its epoch, so leader-only components must run")
	assert.Empty(t, *stoodDown)
}

// A claim that keeps failing must not start the term: every apply would carry
// an epoch the fleet outranks, while this replica keeps renewing the lease that
// stops anyone else from taking over. It hands the lease back instead.
func TestOnStartedLeadingGivesUpWhenTheEpochClaimKeepsFailing(t *testing.T) {
	component, _, stoodDown, published := claimTestComponent(t, -1)
	started := false
	callbacks := component.wrapCallbacks("test-pod",
		k8sleaderelection.Callbacks{OnStartedLeading: func(context.Context) { started = true }})

	callbacks.OnStartedLeading(context.Background())

	assert.Equal(t, []string{"epoch_claim_failed"}, *stoodDown)
	assert.False(t, started, "an unclaimed epoch must start nothing")
	assert.Equal(t, uint64(0), component.epoch.LeaderEpoch())

	// The bus was never paused, so it still delivers — and what it delivers is
	// not a leadership this replica never took.
	component.eventBus.Publish(events.NewLeaderElectionStartedEvent("test-pod", "test-lease", "test-ns"))
	delivered := <-published
	_, wrongEvent := delivered.(*events.BecameLeaderEvent)
	require.False(t, wrongEvent, "an unclaimed term must not announce leadership")
	assert.IsType(t, &events.LeaderElectionStartedEvent{}, delivered)
}
