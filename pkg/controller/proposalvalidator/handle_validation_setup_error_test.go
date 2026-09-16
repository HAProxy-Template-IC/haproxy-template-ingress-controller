// Copyright 2025 Philipp Hossner
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0

package proposalvalidator

import (
	"context"
	"log/slog"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"gitlab.com/haproxy-haptic/haptic/pkg/controller/events"
	busevents "gitlab.com/haproxy-haptic/haptic/pkg/events"
	"gitlab.com/haproxy-haptic/haptic/pkg/stores"
)

func TestHandleValidationRequest_SetupErrorPublishesFailedWithRequestID(t *testing.T) {
	// Minimal pipeline — never reached on the setup-error branch
	// because overlayProvider.Validate() fails first.
	template := `global
    daemon

defaults
    mode http
`
	bus := busevents.NewEventBus(100)
	pipelineInstance := createTestPipeline(t, template)

	// Empty base store — any overlay referencing a store name will
	// fail Validate().
	baseStore := stores.NewRealStoreProvider(map[string]stores.Store{})

	component := New(bus, &ServiceConfig{
		Pipeline:          pipelineInstance,
		BaseStoreProvider: baseStore,
		Logger:            slog.Default(),
	})

	resultChan := bus.Subscribe("test-failed-event-watcher", 10)
	bus.Start()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	go func() { _ = component.Start(ctx) }()
	time.Sleep(50 * time.Millisecond)

	// Build a request whose overlay references a non-existent
	// store. NewProposalValidationRequestedEvent generates a
	// fresh RequestID we capture for the propagation assertion.
	req := events.NewProposalValidationRequestedEvent(
		map[string]*stores.StoreOverlay{
			"nonexistent-store": stores.NewStoreOverlay(),
		},
		nil, // no HTTP overlay
		"test-source",
		"test-source-context",
	)
	require.NotEmpty(t, req.ID,
		"baseline: ProposalValidationRequestedEvent constructor must "+
			"generate a non-empty ID for the propagation assertion to "+
			"be meaningful")

	bus.Publish(req)

	deadline := time.After(3 * time.Second)
	for {
		select {
		case event := <-resultChan:
			completed, ok := event.(*events.ProposalValidationCompletedEvent)
			if !ok {
				continue // skip the request event we already published
			}

			assert.Equal(t, req.ID, completed.RequestID,
				"RequestID MUST be propagated unchanged from the request to "+
					"the failed completion event — httpstore correlates "+
					"pending validations by RequestID; without correlation "+
					"the validator publishes anonymous failures and the "+
					"requester never learns its specific request was "+
					"rejected, leaving HTTP content stuck pending forever")
			assert.False(t, completed.Valid,
				"the completion event MUST have Valid=false — without this "+
					"signal httpstore would treat a malformed-overlay "+
					"request as success and promote unvalidated content")
			assert.Equal(t, "setup", completed.Phase,
				"Phase MUST be 'setup' to distinguish from render/validate "+
					"failures — operators triage by phase to know whether "+
					"to look at the requester (setup), the renderer (render), "+
					"or HAProxy (validate)")
			assert.NotEmpty(t, completed.Error,
				"the failed completion MUST carry the underlying error "+
					"message — without it operators would see 'phase=setup' "+
					"with no signal of what specifically broke")
			return
		case <-deadline:
			t.Fatal("timeout waiting for ProposalValidationCompletedEvent " +
				"with Valid=false — the setup-error branch must always " +
				"publish a completion event so the requester gets a response")
		}
	}
}
