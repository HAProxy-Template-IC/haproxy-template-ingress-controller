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

// Package proposalvalidator validates hypothetical configuration changes.
package proposalvalidator

import (
	"fmt"
	"log/slog"
	"time"

	"gitlab.com/haproxy-haptic/haptic/pkg/controller/component"
	"gitlab.com/haproxy-haptic/haptic/pkg/controller/events"
	busevents "gitlab.com/haproxy-haptic/haptic/pkg/events"
)

const (
	ComponentName   = "proposalvalidator"
	EventBufferSize = busevents.StandardSubscriberBuffer
)

// Component adapts proposal-validation requests to the validation service.
type Component struct {
	*component.Base
	service *Service
}

// New subscribes to proposal-validation requests during construction.
func New(eventBus *busevents.EventBus, cfg *ServiceConfig) *Component {
	c := &Component{service: NewService(cfg)}
	c.Base = component.New(&component.Config{
		EventBus:   eventBus,
		Logger:     cfg.Logger,
		Name:       ComponentName,
		BufferSize: EventBufferSize,
		Handler:    c,
		EventTypes: []string{events.EventTypeProposalValidationRequested},
	})
	return c
}

// HandleEvent implements component.EventHandler: it processes incoming events.
func (c *Component) HandleEvent(event busevents.Event) {
	if e, ok := event.(*events.ProposalValidationRequestedEvent); ok {
		c.handleValidationRequest(e)
	}
}

// HandlePanic rejects the request so pending HTTP content receives a verdict.
func (c *Component) HandlePanic(recovered any, event busevents.Event) {
	req, ok := event.(*events.ProposalValidationRequestedEvent)
	if !ok {
		return
	}
	c.EventBus().Publish(events.NewProposalValidationFailedEvent(
		req.ID,
		"panic",
		fmt.Errorf("proposal validator panicked: %v", recovered),
		0,
	))
}

func (c *Component) handleValidationRequest(req *events.ProposalValidationRequestedEvent) {
	ctx := c.LifecycleContext()
	c.Logger().Debug("Processing proposal validation request",
		"request_id", req.ID,
		"source", req.Source,
		"context", req.SourceContext,
		"k8s_overlay_count", len(req.Overlays),
		"has_http_overlay", req.HTTPOverlay != nil && !req.HTTPOverlay.IsEmpty(),
	)

	start := time.Now()
	result, err := c.service.validateProposal(ctx, req.Overlays, req.HTTPOverlay)
	durationMs := time.Since(start).Milliseconds()
	if !result.Valid {
		level := slog.LevelInfo
		if err != nil {
			level = slog.LevelWarn
		}
		c.Logger().Log(ctx, level, "Proposal validation failed",
			"request_id", req.ID,
			"phase", result.Phase,
			"error", result.Error,
			"duration_ms", durationMs,
		)
		c.EventBus().Publish(events.NewProposalValidationFailedEvent(req.ID, result.Phase, result.Error, durationMs))
		return
	}

	c.Logger().Debug("Proposal validation succeeded",
		"request_id", req.ID,
		"source", req.Source,
		"duration_ms", durationMs,
	)
	c.EventBus().Publish(events.NewProposalValidationCompletedEvent(req.ID, durationMs))
}
