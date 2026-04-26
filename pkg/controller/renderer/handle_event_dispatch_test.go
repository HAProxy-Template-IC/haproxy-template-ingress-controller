// Copyright 2025 Philipp Hossner
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0

package renderer

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"gitlab.com/haproxy-haptic/haptic/pkg/controller/events"
	"gitlab.com/haproxy-haptic/haptic/pkg/controller/testutil"
)

// renderer.handleEvent is a two-case dispatch table:
//   - ReconciliationTriggeredEvent → handleReconciliationTriggered
//   - LostLeadershipEvent → handleLostLeadership
//
// handleLostLeadership has its own pin (handle_lost_leadership_test.go)
// for the cache-clear contract, but those tests call it directly,
// NOT through handleEvent's dispatch. A regression that removed the
// LostLeadershipEvent case from the type switch would silently break
// the leadership-transition cache clear:
//
//   - The next leader's first render would short-circuit as
//     "checksum unchanged" because lastRenderedChecksum still
//     held the previous leader's value.
//   - No TemplateRenderedEvent would be published.
//   - Downstream leader-only components (validator, deployer
//     scheduler) would never wake up.
//   - The pipeline stays stuck until something else triggers a
//     fresh render.
//
// Pin the dispatch by going through handleEvent, observing the
// observable side effect (lastRenderedChecksum cleared).

func TestComponent_HandleEvent_RoutesLostLeadershipEvent(t *testing.T) {
	c := &Component{
		logger:               testutil.NewTestLogger(),
		lastRenderedChecksum: "previous-leader-checksum",
	}

	c.handleEvent(events.NewLostLeadershipEvent("test-pod", "test-reason"))

	assert.Empty(t, c.lastRenderedChecksum,
		"handleEvent MUST route *LostLeadershipEvent to handleLostLeadership "+
			"— a regression that removed the case from the type switch would "+
			"silently leave lastRenderedChecksum populated, causing the next "+
			"leader's first render to short-circuit as 'unchanged' and never "+
			"publish a TemplateRenderedEvent. Downstream leader-only "+
			"components (validator, deployer scheduler) would never wake up "+
			"and the pipeline would stay stuck after every leadership transition")
}
