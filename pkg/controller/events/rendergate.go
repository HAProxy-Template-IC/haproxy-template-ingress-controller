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

// RenderGateCompletedEvent carries the render gate's verdict on one plan:
// what the controller's own `haproxy -c` said about the render the fleet was
// (or is about to be) given.
//
// It replaces ValidationCompletedEvent as the reconcile path's validation
// signal. Deployment is armed by TemplateRenderedEvent now, so this event is
// what moves the gate's latch rather than what starts a deploy — except while
// Pinned, where the pass named by PlanID releases the render the scheduler is
// holding.
//
// Deliberately NOT coalescible: the latch is a state machine, and skipping a
// verdict would leave the scheduler pinned on a plan that has since passed.
type RenderGateCompletedEvent struct {
	// PlanID is the render this verdict describes.
	PlanID string

	// OK is true when the check passed.
	OK bool

	// Refused distinguishes HAProxy's own verdict from a gate that could not
	// run at all (no binary, unwritable temp tree). Only a refusal is evidence
	// about the config, so only a refusal reverts the fleet.
	Refused bool

	// Newest reports that this verdict describes the render the fleet is
	// converging on, rather than a superseded plan some pod still runs. The
	// gate checks both; only the newest one may move the latch, the pinned
	// gauge, the conditions or the validated plan, because every consumer of
	// those has already moved past a straggler. A straggler's verdict still
	// travels: it is what scopes the revert to the pods carrying it.
	Newest bool

	// Message is HAProxy's own words on a refusal, or the reason the gate
	// could not run. Empty on a pass.
	Message string

	// Pinned reports that renders are held until one passes: the gate is in
	// its pessimistic state and the fleet stays on the last config it accepted.
	Pinned bool

	// DurationMs is how long the check took.
	DurationMs int64

	timestamped

	// Correlation embeds correlation tracking for event tracing.
	Correlation
}

// NewRenderGateCompletedEvent creates a new RenderGateCompletedEvent.
//
// Use PropagateCorrelation() to propagate correlation from the render this
// verdict describes:
//
//	event := events.NewRenderGateCompletedEvent(planID, ok, refused, newest, message, pinned, durationMs,
//	    events.PropagateCorrelation(renderedEvent))
func NewRenderGateCompletedEvent(
	planID string,
	ok, refused, newest bool,
	message string,
	pinned bool,
	durationMs int64,
	opts ...CorrelationOption,
) *RenderGateCompletedEvent {
	return &RenderGateCompletedEvent{
		PlanID:      planID,
		OK:          ok,
		Refused:     refused,
		Newest:      newest,
		Message:     message,
		Pinned:      pinned,
		DurationMs:  durationMs,
		timestamped: newTimestamped(),
		Correlation: newCorrelation(opts...),
	}
}

func (e *RenderGateCompletedEvent) EventType() string { return EventTypeRenderGateCompleted }
