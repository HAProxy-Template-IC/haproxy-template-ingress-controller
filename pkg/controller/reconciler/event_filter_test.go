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

package reconciler

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"gitlab.com/haproxy-haptic/haptic/pkg/controller/events"
	"gitlab.com/haproxy-haptic/haptic/pkg/controller/names"
	busevents "gitlab.com/haproxy-haptic/haptic/pkg/events"
	"gitlab.com/haproxy-haptic/haptic/pkg/k8s/types"
)

// The fleet is a deployment target, not a configuration source, so its index
// updates are the one kind handleResourceChange discards. Letting them into a
// non-lossy buffer means a rolling restart can fill it with traffic nobody
// reads, and one drop restarts the controller iteration.
func TestTheFleetsOwnPodChurnNeverReachesTheReconciler(t *testing.T) {
	fleet := &events.ResourceIndexUpdatedEvent{
		ResourceTypeName: names.HAProxyPodsResourceType,
		ChangeStats:      types.ChangeStats{},
	}

	assert.False(t, reconcilerWantsEvent(fleet))
}

// Everything the reconciler reconciles has to get through, or a resource change
// stops triggering a render.
func TestEveryConfigurationSourceStillReachesTheReconciler(t *testing.T) {
	for _, resourceType := range []string{"ingresses", "httproutes", "services", "endpoints", "secrets"} {
		assert.True(t, reconcilerWantsEvent(&events.ResourceIndexUpdatedEvent{
			ResourceTypeName: resourceType,
			ChangeStats:      types.ChangeStats{},
		}), "%s drives reconciliation and must not be filtered out", resourceType)
	}

	for _, event := range []busevents.Event{
		&events.IndexSynchronizedEvent{},
		&events.HTTPResourceUpdatedEvent{},
		&events.HTTPResourceAcceptedEvent{},
		&events.DriftPreventionTriggeredEvent{},
		&events.BecameLeaderEvent{},
	} {
		assert.True(t, reconcilerWantsEvent(event),
			"%s is subscribed and must not be filtered out", event.EventType())
	}
}
