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

package discovery

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"gitlab.com/haproxy-haptic/haptic/pkg/controller/events"
	"gitlab.com/haproxy-haptic/haptic/pkg/controller/names"
	busevents "gitlab.com/haproxy-haptic/haptic/pkg/events"
	"gitlab.com/haproxy-haptic/haptic/pkg/k8s/types"
)

func indexUpdate(resourceType string) *events.ResourceIndexUpdatedEvent {
	return &events.ResourceIndexUpdatedEvent{
		ResourceTypeName: resourceType,
		ChangeStats:      types.ChangeStats{},
	}
}

// This component acts on one watched kind. Every other kind's index updates
// used to reach its buffer only to be discarded on the handler's first line,
// and under churn that filled the buffer: one dropped event on a non-lossy
// subscriber restarts the whole controller iteration and the fleet loses its
// routing until it returns.
func TestOnlyTheSelfWatchedPodIndexReachesDiscovery(t *testing.T) {
	assert.True(t, discoveryWantsEvent(indexUpdate(names.HAProxyPodsResourceType)),
		"the haproxy-pods index is the one this component exists to follow")

	for _, other := range []string{"ingresses", "httproutes", "services", "endpoints", "secrets"} {
		assert.False(t, discoveryWantsEvent(indexUpdate(other)),
			"%s index updates must not occupy the buffer", other)
	}
}

// The filter must only judge the event type it understands. Anything else this
// component subscribed to has to pass, or the filter silently unsubscribes it.
func TestEveryOtherSubscribedEventStillReachesDiscovery(t *testing.T) {
	for _, event := range []busevents.Event{
		&events.ConfigValidatedEvent{},
		&events.CredentialsUpdatedEvent{},
		&events.ResourceSyncCompleteEvent{},
		&events.BecameLeaderEvent{},
		&events.DriftPreventionTriggeredEvent{},
	} {
		assert.True(t, discoveryWantsEvent(event),
			"%s is subscribed and must not be filtered out", event.EventType())
	}
}
