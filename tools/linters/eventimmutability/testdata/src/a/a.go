package a

import (
	"time"

	"gitlab.com/haproxy-haptic/haptic/pkg/controller/events"
)

// Violation: Direct field mutation
func mutateEventField(event *events.TestEvent) {
	event.Reason = "modified" // want `event field mutation detected`
}

// Violation: Multiple field mutations
func mutateMultipleFields(event *events.TestEvent) {
	event.Reason = "modified"    // want `event field mutation detected`
	event.Count = 42             // want `event field mutation detected`
	event.Timestamp = time.Now() // want `event field mutation detected`
}

// OK: Read-only access
func readEventField(event *events.TestEvent) string {
	return event.Reason
}

// OK: Read and use in local variable
func useEventField(event *events.TestEvent) {
	reason := event.Reason
	count := event.Count
	_ = reason
	_ = count
}

// OK: Pass field to function
func passEventField(event *events.TestEvent) {
	processReason(event.Reason)
}

func processReason(reason string) {
	_ = reason
}

// Non-event struct (should not trigger)
type RegularStruct struct {
	Field string
}

// OK: Mutating non-event struct
func mutateRegularStruct(s *RegularStruct) {
	s.Field = "modified" // OK - not an event type
}

// OK: Local event creation and mutation
func createAndMutateLocalEvent() {
	event := &events.TestEvent{}
	event.Reason = "local" // OK - local variable, not passed as parameter
	event.Count = 1
}

// Violation: Mutation in conditional
func mutateInConditional(event *events.TestEvent, condition bool) {
	if condition {
		event.Reason = "conditional" // want `event field mutation detected`
	}
}

// Violation: Mutation in loop - event is loop variable, not local
// Note: This is harder to detect as the loop variable is technically local.
// For now, we'll accept this limitation and rely on code review for loop mutations.
func mutateInLoop(events []*events.TestEvent) {
	for _, event := range events {
		event.Count = event.Count + 1
	}
}
