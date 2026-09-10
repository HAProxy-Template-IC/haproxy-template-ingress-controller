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

package events

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"gitlab.com/haproxy-haptic/haptic/pkg/controller/rendercycle"
)

func TestResourcesProcessedOccurrenceAndFanout(t *testing.T) {
	fixture := deploymentEventCycleFixture(t)
	event, err := NewResourcesProcessedEvent(fixture.occurrence, WithCorrelation("cycle", "source"))
	require.NoError(t, err)
	clone := event.CloneForSubscriber().(*ResourcesProcessedEvent)
	assert.NotSame(t, event, clone)
	assert.Equal(t, EventTypeResourcesProcessed, clone.EventType())
	assert.Equal(t, "cycle", clone.CorrelationID())
	assert.Equal(t, "source", clone.CausationID())
	assert.Equal(t, event.Timestamp(), clone.Timestamp())
	clone.renderOccurrenceCarrier = renderOccurrenceCarrier{}
	_, err = clone.RenderOccurrence()
	require.Error(t, err)
	occurrence, err := event.RenderOccurrence()
	require.NoError(t, err)
	assert.Same(t, fixture.occurrence, occurrence)
}

func TestResourcesProcessedRejectsUnauthenticatedOccurrence(t *testing.T) {
	fixture := deploymentEventCycleFixture(t)
	copied := *fixture.occurrence
	for _, occurrence := range []*rendercycle.Occurrence{nil, {}, &copied} {
		_, err := NewResourcesProcessedEvent(occurrence)
		require.Error(t, err)
	}
}
