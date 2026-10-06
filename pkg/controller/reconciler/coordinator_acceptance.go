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
	"context"
	"errors"
	"runtime"
	"sync"
	"sync/atomic"
	"weak"

	"gitlab.com/haproxy-haptic/haptic/pkg/controller/events"
	"gitlab.com/haproxy-haptic/haptic/pkg/controller/pipeline"
	"gitlab.com/haproxy-haptic/haptic/pkg/controller/rendercontext"
	"gitlab.com/haproxy-haptic/haptic/pkg/controller/rendercycle"
	"gitlab.com/haproxy-haptic/haptic/pkg/httpstore"
)

// HTTPContentAcceptance is the HTTP store's reversible acceptance (ADR-0030).
type HTTPContentAcceptance interface {
	AcceptanceSequence() uint64
	ConfirmAcceptances(observations []httpstore.ObservationToken)
	RevokeAcceptances(sequence uint64) int
}

const maxRefusedOutputs = 32

// acceptanceWindow keeps exact confirmation tokens and a conservative revocation ceiling.
type acceptanceWindow struct {
	reached      uint64
	observations []httpstore.ObservationToken
}

type acceptanceLedger struct {
	mu           sync.Mutex
	windows      map[weak.Pointer[rendercycle.Occurrence]]acceptanceWindow
	refused      map[string]struct{}
	refusedOrder []string
	attempting   bool
	again        bool
	attempts     sync.WaitGroup
	attempted    atomic.Uint64
	lastRefusal  string
}

func newAcceptanceLedger() *acceptanceLedger {
	return &acceptanceLedger{
		windows: make(map[weak.Pointer[rendercycle.Occurrence]]acceptanceWindow),
		refused: make(map[string]struct{}),
	}
}

func (l *acceptanceLedger) record(occurrence *rendercycle.Occurrence, window acceptanceWindow) {
	l.mu.Lock()
	defer l.mu.Unlock()
	reference := weak.Make(occurrence)
	if _, exists := l.windows[reference]; !exists {
		// Delayed verdicts keep their window without the ledger retaining rendered snapshots.
		runtime.AddCleanup(occurrence, cleanAcceptanceWindow, acceptanceWindowCleanup{ledger: l, occurrence: reference})
	}
	l.windows[reference] = window
	runtime.KeepAlive(occurrence)
}

func (l *acceptanceLedger) window(occurrence *rendercycle.Occurrence) (acceptanceWindow, bool) {
	l.mu.Lock()
	defer l.mu.Unlock()
	window, exists := l.windows[weak.Make(occurrence)]
	runtime.KeepAlive(occurrence)
	return window, exists
}

type acceptanceWindowCleanup struct {
	ledger     *acceptanceLedger
	occurrence weak.Pointer[rendercycle.Occurrence]
}

func cleanAcceptanceWindow(cleanup acceptanceWindowCleanup) {
	cleanup.ledger.mu.Lock()
	defer cleanup.ledger.mu.Unlock()
	delete(cleanup.ledger.windows, cleanup.occurrence)
}

func (l *acceptanceLedger) markRefused(checksum string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if _, exists := l.refused[checksum]; exists {
		return
	}
	l.refused[checksum] = struct{}{}
	l.refusedOrder = append(l.refusedOrder, checksum)
	if len(l.refusedOrder) > maxRefusedOutputs {
		delete(l.refused, l.refusedOrder[0])
		l.refusedOrder = l.refusedOrder[1:]
	}
}

func (l *acceptanceLedger) isRefused(checksum string) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	_, refused := l.refused[checksum]
	return refused
}

// acceptanceSequence is 0 when the coordinator runs without an HTTP store.
func (c *Coordinator) acceptanceSequence() uint64 {
	if c.acceptance == nil {
		return 0
	}
	return c.acceptance.AcceptanceSequence()
}

func (c *Coordinator) settleAcceptances(event *events.RenderGateCompletedEvent) {
	if c.acceptance == nil || (!event.OK && !event.Refused) {
		return
	}
	occurrence, err := event.RenderOccurrence()
	if err != nil {
		return
	}
	cycle, err := occurrence.Snapshot()
	if err != nil {
		return
	}
	checksum, err := cycle.ContentChecksum()
	if err != nil {
		return
	}
	window, dispatched := c.ledger.window(occurrence)
	if event.Refused {
		c.ledger.markRefused(checksum)
	}
	if !dispatched {
		return
	}
	if event.OK {
		c.acceptance.ConfirmAcceptances(window.observations)
		return
	}
	if revoked := c.acceptance.RevokeAcceptances(window.reached); revoked > 0 {
		c.logger.Warn("HAProxy refused a render containing newly accepted HTTP content; "+
			"took the content back and rendering without it", "revoked", revoked)
	}
}

// A request during acceptance schedules one more attempt for sources that arrived meanwhile.
func (c *Coordinator) requestAcceptanceAttempt(ctx context.Context, generation uint64) {
	c.ledger.mu.Lock()
	defer c.ledger.mu.Unlock()
	if c.ledger.attempting {
		c.ledger.again = true
		return
	}
	c.ledger.attempting = true
	c.ledger.attempts.Add(1)
	go c.runAcceptanceAttempts(ctx, generation)
}

func (c *Coordinator) runAcceptanceAttempts(ctx context.Context, generation uint64) {
	defer c.ledger.attempts.Done()
	for {
		c.attemptAcceptance(ctx, generation)
		c.ledger.attempted.Add(1)
		c.ledger.mu.Lock()
		if !c.ledger.again || ctx.Err() != nil {
			c.ledger.attempting = false
			c.ledger.again = false
			c.ledger.mu.Unlock()
			return
		}
		c.ledger.again = false
		c.ledger.mu.Unlock()
	}
}

// Acceptance output is discarded; a new deploying render reads the accepted content.
func (c *Coordinator) attemptAcceptance(ctx context.Context, generation uint64) {
	opts, err := c.renderOptions(generation)
	if err != nil {
		c.logger.Debug("HTTP content acceptance attempt skipped", "error", err)
		return
	}
	before := c.acceptance.AcceptanceSequence()
	attemptCtx := pipeline.WithRefusedOutputs(ctx, c.ledger.isRefused)
	if _, err := c.pipeline.Execute(attemptCtx, c.storeProvider, rendercontext.RenderModeReconcile, opts...); err != nil {
		if ctx.Err() == nil {
			c.reportPendingContent(err)
		}
		return
	}
	c.ledger.mu.Lock()
	c.ledger.lastRefusal = ""
	c.ledger.mu.Unlock()
	if c.acceptance.AcceptanceSequence() != before {
		c.eventBus.Publish(events.NewReconciliationTriggeredEvent("http_content_accepted", true, events.WithNewCorrelation()))
	}
}

// Repeated refusals under watch churn should not bury the original diagnostic.
func (c *Coordinator) reportPendingContent(err error) {
	if pipeline.InputsMoved(err) || errors.Is(err, pipeline.ErrOutputRefusedByGate) {
		c.logger.Debug("HTTP content stays pending", "error", err)
		return
	}
	reason := err.Error()
	c.ledger.mu.Lock()
	repeated := c.ledger.lastRefusal == reason
	c.ledger.lastRefusal = reason
	c.ledger.mu.Unlock()
	if repeated {
		return
	}
	c.logger.Warn("New http.Fetch content stays pending. Non-critical sources deploy without it; "+
		"a critical source holds every deploy. Fix the source or the template the error names.",
		"error", err)
}
