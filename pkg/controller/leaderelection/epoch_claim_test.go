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
	busevents "gitlab.com/haproxy-haptic/haptic/pkg/events"
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

// claimTest is one wrapped component whose lease updates fail, and what its
// term did: how many claims it tried, whether it gave leadership back, and what
// it published.
type claimTest struct {
	component *Component
	attempts  *int
	stoodDown *[]string
	published <-chan busevents.Event
}

func newClaimTest(t *testing.T, failures int) *claimTest {
	t.Helper()
	bus, logger := testutil.NewTestBusAndLogger()
	clientset := fake.NewClientset(&coordinationv1.Lease{
		ObjectMeta: metav1.ObjectMeta{Name: "test-lease", Namespace: "test-ns"},
	})
	test := &claimTest{
		attempts:  failingLeases(clientset, failures),
		stoodDown: &[]string{},
		published: bus.Subscribe("test-sub", 10),
	}
	bus.Start()

	test.component = &Component{
		eventBus:  bus,
		logger:    logger,
		identity:  "test-pod",
		leaseName: "test-lease",
		epoch: NewTerm(
			NewLeaseEpoch(clientset, "test-ns", "test-lease", "test-pod", logger),
			func(reason string) { *test.stoodDown = append(*test.stoodDown, reason) },
		),
	}
	return test
}

// The apiserver blip that costs the previous leader its lease is exactly what
// can cost the new one its epoch claim. A transient failure is retried, and the
// term starts on the epoch it claimed.
func TestOnStartedLeadingRetriesATransientEpochClaimFailure(t *testing.T) {
	test := newClaimTest(t, 2)
	started := false
	callbacks := test.component.wrapCallbacks("test-pod",
		k8sleaderelection.Callbacks{OnStartedLeading: func(context.Context) { started = true }})

	callbacks.OnStartedLeading(context.Background())

	assert.Equal(t, 3, *test.attempts, "two failures, then the claim that lands")
	assert.Equal(t, uint64(1), test.component.epoch.LeaderEpoch())
	assert.True(t, started, "the term claimed its epoch, so leader-only components must run")
	assert.Empty(t, *test.stoodDown)
}

// A claim that keeps failing must not start the term: every apply would carry
// an epoch the fleet outranks, while this replica keeps renewing the lease that
// stops anyone else from taking over. It hands the lease back instead.
func TestOnStartedLeadingGivesUpWhenTheEpochClaimKeepsFailing(t *testing.T) {
	test := newClaimTest(t, -1)
	started := false
	callbacks := test.component.wrapCallbacks("test-pod",
		k8sleaderelection.Callbacks{OnStartedLeading: func(context.Context) { started = true }})

	callbacks.OnStartedLeading(context.Background())

	assert.Equal(t, []string{"epoch_claim_failed"}, *test.stoodDown)
	assert.False(t, started, "an unclaimed epoch must start nothing")
	assert.Equal(t, uint64(0), test.component.epoch.LeaderEpoch())

	// The bus was never paused, so it still delivers — and what it delivers is
	// not a leadership this replica never took.
	test.component.eventBus.Publish(events.NewLeaderElectionStartedEvent("test-pod", "test-lease", "test-ns"))
	delivered := <-test.published
	_, wrongEvent := delivered.(*events.BecameLeaderEvent)
	require.False(t, wrongEvent, "an unclaimed term must not announce leadership")
	assert.IsType(t, &events.LeaderElectionStartedEvent{}, delivered)
}
