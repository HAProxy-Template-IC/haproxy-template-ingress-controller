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

package resourceapplier

import (
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"k8s.io/apimachinery/pkg/runtime"
	dynamicfake "k8s.io/client-go/dynamic/fake"
	k8stesting "k8s.io/client-go/testing"

	"gitlab.com/haproxy-haptic/haptic/pkg/controller/events"
	"gitlab.com/haproxy-haptic/haptic/pkg/controller/testutil"
	"gitlab.com/haproxy-haptic/haptic/pkg/templating"
)

func TestResourceProcessingCompletionDoesNotImplySuccess(t *testing.T) {
	for _, tc := range []struct {
		name   string
		leader bool
		held   bool
		fail   bool
	}{
		{name: "applied", leader: true},
		{name: "failed", leader: true, fail: true},
		{name: "held", leader: true, held: true},
		{name: "follower"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			comp, bus, _ := newTestComp(t, false)
			if tc.leader {
				setLeader(comp)
			}
			comp.gatePinned = tc.held
			if tc.fail {
				client := comp.dynamicClient.(*dynamicfake.FakeDynamicClient)
				client.PrependReactor("patch", "*", func(k8stesting.Action) (bool, runtime.Object, error) {
					return true, nil, errors.New("API failure")
				})
			}
			processed := bus.SubscribeTypes("processed-test", 4, events.EventTypeResourcesProcessed)
			applied := bus.SubscribeTypes("applied-test", 4, events.EventTypeResourcesApplied)
			bus.Start()
			event := reconciliationCompletedEvent(t, []templating.RenderedResource{sampleResource("haptic", "target", 80)})
			comp.handleReconciliationCompleted(t.Context(), event)
			receipt := testutil.WaitForEvent[*events.ResourcesProcessedEvent](t, processed, testutil.EventTimeout)
			requireSameOccurrence(t, event, receipt)
			assert.Equal(t, event.CorrelationID(), receipt.CorrelationID())
			if tc.leader && !tc.held && !tc.fail {
				_ = testutil.WaitForEvent[*events.ResourcesAppliedEvent](t, applied, testutil.EventTimeout)
			} else {
				testutil.AssertNoEvent[*events.ResourcesAppliedEvent](t, applied, testutil.NoEventTimeout)
			}
		})
	}
}

func TestResourceProcessingAcknowledgementWaitsForAPI(t *testing.T) {
	comp, bus, _ := newTestComp(t, false)
	setLeader(comp)
	started, release := make(chan struct{}), make(chan struct{})
	var releaseOnce sync.Once
	unblock := func() { releaseOnce.Do(func() { close(release) }) }
	client := comp.dynamicClient.(*dynamicfake.FakeDynamicClient)
	client.PrependReactor("patch", "*", func(k8stesting.Action) (bool, runtime.Object, error) {
		close(started)
		<-release
		return false, nil, nil
	})
	processed := bus.SubscribeTypes("processed-test", 4, events.EventTypeResourcesProcessed)
	bus.Start()
	event := reconciliationCompletedEvent(t, []templating.RenderedResource{sampleResource("haptic", "target", 80)})
	done := make(chan struct{})
	go func() {
		defer close(done)
		comp.handleReconciliationCompleted(t.Context(), event)
	}()
	t.Cleanup(func() {
		unblock()
		select {
		case <-done:
		case <-time.After(testutil.EventTimeout):
			t.Error("resource handler did not stop")
		}
	})
	select {
	case <-started:
	case <-time.After(testutil.EventTimeout):
		t.Fatal("API call did not start")
	}
	testutil.AssertNoEvent[*events.ResourcesProcessedEvent](t, processed, testutil.NoEventTimeout)
	unblock()
	receipt := testutil.WaitForEvent[*events.ResourcesProcessedEvent](t, processed, testutil.EventTimeout)
	requireSameOccurrence(t, event, receipt)
}
