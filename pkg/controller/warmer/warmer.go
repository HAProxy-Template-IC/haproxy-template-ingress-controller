// Copyright 2026 Philipp Hossner
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
// http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

// Package warmer keeps a follower's incremental render graph warm.
//
// Only the leader's Coordinator renders to deploy, so without this a follower's
// graph sits at generation zero and every leadership change starts with a cold
// render. The warmer runs the same reconcile render on every trigger a follower
// receives and commits it, which is what publishes the graph, then discards the
// output: nothing it renders reaches the fleet, the API server, or any other
// component.
package warmer

import (
	"context"
	"log/slog"

	"gitlab.com/haproxy-haptic/haptic/pkg/controller/component"
	"gitlab.com/haproxy-haptic/haptic/pkg/controller/events"
	"gitlab.com/haproxy-haptic/haptic/pkg/controller/metrics"
	"gitlab.com/haproxy-haptic/haptic/pkg/controller/pipeline"
	"gitlab.com/haproxy-haptic/haptic/pkg/controller/rendercontext"
	busevents "gitlab.com/haproxy-haptic/haptic/pkg/events"
	"gitlab.com/haproxy-haptic/haptic/pkg/stores"
)

const (
	// ComponentName is the unique identifier for this component.
	ComponentName = "render-warmer"

	// EventBufferSize matches the Coordinator's: the trigger stream is one
	// event per resource change, and the mailbox collapses it to one render.
	EventBufferSize = busevents.ResourceChurnSubscriberBuffer
)

// PipelineExecutor renders and commits; the warmer never reads the output.
type PipelineExecutor interface {
	Execute(ctx context.Context, provider stores.StoreProvider, mode rendercontext.RenderMode, extraOpts ...rendercontext.Option) (*pipeline.PipelineResult, error)
}

// Config wires a Component.
type Config struct {
	EventBus      *busevents.EventBus
	Pipeline      PipelineExecutor
	StoreProvider stores.StoreProvider
	// CurrentFiles returns the auxiliary files the fleet runs, the same set a
	// new leader's first render reads.
	CurrentFiles func() (map[string]string, error)
	Metrics      *metrics.Metrics
	Logger       *slog.Logger
}

// Component renders on a follower and publishes nothing.
//
// BecameLeaderEvent is the first event replayed after the bus pauses for a
// leadership change, ahead of the trigger the Reconciler derives from it, so
// the warmer stands down before the Coordinator's first render. A render
// already in flight at that point holds the lower output generation and its
// commit is discarded as superseded.
type Component struct {
	*component.Base

	pipeline      PipelineExecutor
	storeProvider stores.StoreProvider
	currentFiles  func() (map[string]string, error)
	metrics       *metrics.Metrics
	leader        bool
}

// New subscribes the component; call before the bus starts.
func New(cfg *Config) *Component {
	c := &Component{
		pipeline:      cfg.Pipeline,
		storeProvider: cfg.StoreProvider,
		currentFiles:  cfg.CurrentFiles,
		metrics:       cfg.Metrics,
	}
	c.Base = component.New(&component.Config{
		EventBus:   cfg.EventBus,
		Logger:     cfg.Logger,
		Name:       ComponentName,
		BufferSize: EventBufferSize,
		Handler:    c,
		EventTypes: []string{
			events.EventTypeReconciliationTriggered,
			events.EventTypeBecameLeader,
			events.EventTypeLostLeadership,
		},
	})
	return c
}

// CoalescesOn collapses a burst of triggers to the latest one: every render
// re-reads the stores, so the newest trigger is all a follower needs.
func (c *Component) CoalescesOn() []string {
	return []string{events.EventTypeReconciliationTriggered}
}

// HandleEvent implements component.EventHandler.
func (c *Component) HandleEvent(event busevents.Event) {
	switch event.(type) {
	case *events.BecameLeaderEvent:
		c.leader = true
	case *events.LostLeadershipEvent:
		c.leader = false
	case *events.ReconciliationTriggeredEvent:
		if !c.leader {
			c.render()
		}
	}
}

func (c *Component) render() {
	ctx := c.LifecycleContext()
	files, err := c.currentFiles()
	if err != nil {
		c.Logger().Debug("Follower render skipped", "reason", err)
		return
	}
	result, err := c.pipeline.Execute(
		ctx, c.storeProvider, rendercontext.RenderModeReconcile, rendercontext.WithCurrentAuxFiles(files),
	)
	if err != nil {
		if context.Cause(ctx) == nil {
			c.Logger().Debug("Follower render failed", "error", err)
		}
		return
	}
	if c.metrics != nil {
		c.metrics.RecordRender(result.CacheState)
	}
	c.Logger().Debug("Follower render completed",
		"render_ms", result.RenderDurationMs,
		"cache_state", result.CacheState,
		"cache_build_ms", result.CacheBuildMs)
}
