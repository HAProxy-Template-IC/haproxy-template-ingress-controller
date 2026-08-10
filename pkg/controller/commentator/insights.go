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

package commentator

import (
	"fmt"
	"time"

	busevents "gitlab.com/haproxy-haptic/haptic/pkg/events"
)

// Lookback window durations for event correlation in the ring buffer.
const (
	validationLookbackWindow     = 30 * time.Second
	reconciliationLookbackWindow = 5 * time.Minute
	startEventLookbackWindow     = 1 * time.Minute
	discoveryLookbackWindow      = 30 * time.Second
)

const (
	attrKeyEventType = "event_type"
	statusFailed     = "FAILED"
)

// generateInsight creates a contextual message and structured attributes for the event.
//
// This applies domain knowledge and uses the ring buffer for event correlation.
// Each per-domain handler already switches on the concrete event types it owns
// and returns an empty insight for everything else, so there is no need for an
// outer type-group switch to pre-route events — that just duplicated the type
// list. We hand the event to each handler in turn; the first one that claims it
// (returns a non-empty insight) wins. Per-domain handlers live in
// insights_config.go (config + validation), insights_pipeline.go (resource,
// reconciliation, template, deployment, pod) and insights_platform.go (leader,
// status).
func (ec *EventCommentator) generateInsight(event busevents.Event) (insight string, args []any) {
	eventType := event.EventType()
	attrs := []any{
		attrKeyEventType, eventType,
		"timestamp", event.Timestamp(),
	}

	handlers := []func(busevents.Event, []any) (string, []any){
		ec.configInsight,
		ec.resourceInsight,
		ec.reconciliationInsight,
		ec.templateInsight,
		ec.validationInsight,
		ec.deploymentInsight,
		ec.podInsight,
		ec.leaderInsight,
		ec.statusInsight,
	}
	for _, handler := range handlers {
		if insight, args = handler(event, attrs); insight != "" {
			return insight, args
		}
	}

	// Types with no insight case: the publisher already logs their payload, so
	// this line only records that the event happened.
	return fmt.Sprintf("Event: %s", eventType), attrs
}
