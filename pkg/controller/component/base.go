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

// Package component provides a shared event-loop scaffold for controller
// components that subscribe on construction and dispatch one event at a
// time. The two domain-flavoured wrappers in the controller tree
// (pkg/controller/resourceloader.BaseLoader and
// pkg/controller/validator.BaseValidator) embed *Base for the actual
// subscribe/dispatch/panic-recovery loop and add their own domain-specific
// dispatch on top.
//
// Consumers embed *Base and implement EventHandler. Components that need a
// domain-specific response to a panic (e.g. scatter-gather responders)
// additionally implement PanicHandler.
package component

import (
	"context"
	"fmt"
	"log/slog"
	"sync"

	busevents "gitlab.com/haproxy-haptic/haptic/pkg/events"
)

// EventHandler dispatches a single event received on the subscription
// channel. Implementations should not block the goroutine for longer than
// the processing budget documented on the event.
type EventHandler interface {
	HandleEvent(event busevents.Event)
}

// PanicHandler is an optional interface that a component implements when it
// needs to publish a domain-specific response if event handling panics. The
// base always logs the panic and keeps the loop alive regardless of whether
// this interface is implemented.
type PanicHandler interface {
	HandlePanic(recovered any, event busevents.Event)
}

// CoalescingHandler is an optional interface implemented by handlers whose
// events of the declared types have latest-wins semantics. When it returns a
// non-empty list, Base runs in MAILBOX mode: a dedicated intake goroutine
// drains the subscription channel immediately into an internal unbounded
// queue, so the bus-side buffer can never fill and the bus never drops this
// subscriber's events — no matter how slow the handler is. Uninterrupted
// runs of coalescible events of a declared type (i.e.
// event.(busevents.CoalescibleEvent).Coalescible() == true) collapse to
// their latest element at the queue tail; any other event is appended,
// preserving arrival order across event types. The worker dispatches from
// the queue head at its own pace.
//
// This exists because slow handlers (e.g. status appliers doing SSA
// round-trips per event) otherwise stall the channel long enough under
// burst for the bus to overflow the subscriber buffer and drop events —
// including non-coalescible ones and the final event of a burst, whose loss
// leaves stale state until the next external trigger.
//
// Declaring a type is a per-component statement that ONLY the latest queued
// event of that type matters to THIS component. Never declare a type whose
// every instance carries per-event bookkeeping for the component (e.g. the
// deployer must see every deployment.completed to clear its in-flight flag,
// so it declares only deployment.scheduled).
//
// An empty list disables coalescing — handlers that conditionally need it
// can return nil to opt out at runtime (plain channel loop, no mailbox).
type CoalescingHandler interface {
	CoalescesOn() []string
}

// Base is a reusable event-loop implementation. It subscribes on
// construction (so components are guaranteed to receive events published
// after EventBus.Start()), wraps each dispatch in a recover, and supports
// graceful shutdown via either a cancelled context or an explicit Stop.
type Base struct {
	eventBus  *busevents.EventBus
	eventChan <-chan busevents.Event
	logger    *slog.Logger
	name      string
	handler   EventHandler
	stopCh    chan struct{}
	stopOnce  sync.Once

	// Mailbox state (only used when the handler is a CoalescingHandler
	// with a non-empty CoalescesOn; see startMailbox).
	mbMu     sync.Mutex
	mbQueue  []mailboxEntry
	mbNotify chan struct{}
}

// mailboxEntry is one queued event plus how many earlier coalescible events
// of the same run it superseded (for the coalesced-events debug log).
type mailboxEntry struct {
	event      busevents.Event
	superseded int
}

// Config wires up a new Base.
//
// If EventTypes is empty the component receives every event on the bus; if
// it is non-empty the component receives only the listed types (preferred
// for components that only react to a handful of events).
type Config struct {
	EventBus   *busevents.EventBus
	Logger     *slog.Logger
	Name       string
	BufferSize int
	Handler    EventHandler
	EventTypes []string
}

// New subscribes to the EventBus and returns a Base ready to Start. The
// logger is annotated with `component=<name>` before being stored. Config
// is taken by pointer because the struct is large enough that the linter
// flags by-value passing.
func New(cfg *Config) *Base {
	logger := cfg.Logger
	if logger == nil {
		logger = slog.Default()
	}

	var eventChan <-chan busevents.Event
	if len(cfg.EventTypes) == 0 {
		eventChan = cfg.EventBus.Subscribe(cfg.Name, cfg.BufferSize)
	} else {
		eventChan = cfg.EventBus.SubscribeTypes(cfg.Name, cfg.BufferSize, cfg.EventTypes...)
	}

	return &Base{
		eventBus:  cfg.EventBus,
		eventChan: eventChan,
		logger:    logger.With("component", cfg.Name),
		name:      cfg.Name,
		handler:   cfg.Handler,
		stopCh:    make(chan struct{}),
		// Allocated unconditionally (not in startMailbox): a component
		// restarted across leadership terms would otherwise race the new
		// term's channel assignment against the previous term's intake
		// goroutine still notifying on the old one.
		mbNotify: make(chan struct{}, 1),
	}
}

// Start drives the event loop until the context is cancelled or Stop is
// called. Returns nil on graceful shutdown. Handlers implementing
// CoalescingHandler (non-empty CoalescesOn) run in mailbox mode — see
// CoalescingHandler for the semantics and why.
func (b *Base) Start(ctx context.Context) error {
	b.logger.Debug(b.name + " starting")

	if ch, ok := b.handler.(CoalescingHandler); ok {
		if types := ch.CoalescesOn(); len(types) > 0 {
			return b.startMailbox(ctx, types)
		}
	}

	for {
		select {
		case <-ctx.Done():
			b.logger.Info(b.name+" shutting down", "reason", ctx.Err())
			return nil
		case <-b.stopCh:
			b.logger.Info(b.name + " shutting down")
			return nil
		case event := <-b.eventChan:
			b.dispatch(event)
		}
	}
}

// mailboxBacklogWarnFloor is the queue length from which power-of-two
// crossings emit a backlog warning (256, 512, 1024, …). The queue is
// unbounded by design — never dropping is the point — so backlog growth is
// surfaced instead of capped.
const mailboxBacklogWarnFloor = 256

// startMailbox runs the two-goroutine mailbox loop: the intake goroutine
// moves events off the subscription channel into the internal queue the
// instant they arrive (only µs-scale mutex work, so the bus-side buffer
// cannot fill and the bus never drops for this subscriber), while this
// goroutine dispatches from the queue head. Consecutive coalescible events
// of eventType collapse at the tail; everything else keeps arrival order.
func (b *Base) startMailbox(ctx context.Context, eventTypes []string) error {
	// A restarted component (leadership regained on the same instance) must
	// not resurrect the previous term's queue: those events describe state
	// from before the restart and FlushPending callers already expect a
	// clean slate (it clears this queue too; this covers non-flushing users).
	b.mbMu.Lock()
	if n := len(b.mbQueue); n > 0 {
		b.logger.Debug(b.name+" discarded stale mailbox events at start", "count", n)
		b.mbQueue = nil
	}
	b.mbMu.Unlock()
	coalesced := make(map[string]struct{}, len(eventTypes))
	for _, t := range eventTypes {
		coalesced[t] = struct{}{}
	}

	go func() {
		for {
			select {
			case event := <-b.eventChan:
				b.mailboxEnqueue(event, coalesced)
			case <-ctx.Done():
				return
			case <-b.stopCh:
				return
			}
		}
	}()

	for {
		select {
		case <-ctx.Done():
			b.logger.Info(b.name+" shutting down", "reason", ctx.Err())
			return nil
		case <-b.stopCh:
			b.logger.Info(b.name + " shutting down")
			return nil
		case <-b.mbNotify:
			if stopped := b.mailboxDrain(ctx); stopped {
				return nil
			}
		}
	}
}

// mailboxDrain dispatches queued entries until the queue is empty or
// shutdown is requested; returns true on shutdown. Checking for shutdown
// between dispatches is load-bearing: with a slow handler and a deep queue,
// draining to empty first would delay shutdown by the whole backlog.
// Undispatched entries stay queued and are discarded at the next Start
// (term boundary), matching FlushPending semantics.
func (b *Base) mailboxDrain(ctx context.Context) (stopped bool) {
	for {
		select {
		case <-ctx.Done():
			b.logger.Info(b.name+" shutting down", "reason", ctx.Err())
			return true
		case <-b.stopCh:
			b.logger.Info(b.name + " shutting down")
			return true
		default:
		}
		entry, ok := b.mailboxPop()
		if !ok {
			return false
		}
		if entry.superseded > 0 {
			b.logger.Debug(b.name+" coalesced events",
				"event_type", entry.event.EventType(),
				"superseded_count", entry.superseded)
		}
		b.dispatch(entry.event)
	}
}

// mailboxEnqueue appends event to the mailbox queue, collapsing it into the
// tail entry when both are coalescible events of the same declared type
// (latest wins, superseded count carried for logging).
func (b *Base) mailboxEnqueue(event busevents.Event, coalescedTypes map[string]struct{}) {
	coalescible := false
	if _, declared := coalescedTypes[event.EventType()]; declared {
		if c, ok := event.(busevents.CoalescibleEvent); ok && c.Coalescible() {
			coalescible = true
		}
	}

	b.mbMu.Lock()
	if coalescible && len(b.mbQueue) > 0 {
		tail := &b.mbQueue[len(b.mbQueue)-1]
		if tail.event.EventType() == event.EventType() {
			if tc, ok := tail.event.(busevents.CoalescibleEvent); ok && tc.Coalescible() {
				tail.event = event
				tail.superseded++
				b.mbMu.Unlock()
				b.mailboxNotify()
				return
			}
		}
	}
	b.mbQueue = append(b.mbQueue, mailboxEntry{event: event})
	n := len(b.mbQueue)
	b.mbMu.Unlock()

	if n >= mailboxBacklogWarnFloor && n&(n-1) == 0 {
		b.logger.Warn(b.name+" mailbox backlog growing — handler slower than event arrival",
			"queue_len", n)
	}
	b.mailboxNotify()
}

// mailboxPop removes and returns the queue head.
func (b *Base) mailboxPop() (mailboxEntry, bool) {
	b.mbMu.Lock()
	defer b.mbMu.Unlock()
	if len(b.mbQueue) == 0 {
		return mailboxEntry{}, false
	}
	entry := b.mbQueue[0]
	b.mbQueue[0] = mailboxEntry{} // release the event for GC
	b.mbQueue = b.mbQueue[1:]
	if len(b.mbQueue) == 0 {
		b.mbQueue = nil // reset backing array so it can't grow unboundedly
	}
	return entry, true
}

// mailboxNotify wakes the worker; the 1-buffered channel coalesces wakeups.
func (b *Base) mailboxNotify() {
	select {
	case b.mbNotify <- struct{}{}:
	default:
	}
}

// dispatch forwards event to the handler, recovering panics so a single bad
// event cannot tear down the goroutine. Components that implement
// PanicHandler receive a callback after the panic is logged.
func (b *Base) dispatch(event busevents.Event) {
	defer func() {
		r := recover()
		if r == nil {
			return
		}
		b.logger.Error(b.name+" panicked during event handling",
			"panic", r,
			"event_type", fmt.Sprintf("%T", event))

		ph, ok := b.handler.(PanicHandler)
		if !ok {
			return
		}
		// Guard the recovery hook so a panicking hook cannot re-panic the
		// goroutine — the whole point of the outer recover is that the loop
		// must stay alive.
		func() {
			defer func() {
				if rr := recover(); rr != nil {
					b.logger.Error(b.name+" panic handler itself panicked",
						"panic", rr)
				}
			}()
			ph.HandlePanic(r, event)
		}()
	}()
	b.handler.HandleEvent(event)
}

// SafeDispatch invokes handle, recovering and logging any panic so a single
// bad event cannot tear down a component's event loop. Components that embed
// Base get this protection automatically via (*Base).dispatch; components that
// cannot embed Base — because they use a lossy subscription or add extra
// ticker/timer arms to their select — call this directly around their per-event
// handling instead, getting the same recover-and-keep-alive guarantee.
func SafeDispatch(logger *slog.Logger, name string, event busevents.Event, handle func()) {
	if logger == nil {
		logger = slog.Default()
	}
	defer func() {
		if r := recover(); r != nil {
			logger.Error(name+" panicked during event handling",
				"panic", r,
				"event_type", fmt.Sprintf("%T", event))
		}
	}()
	handle()
}

// FlushPending discards every event currently buffered on the subscription
// channel without dispatching it. Leader-only components that embed Base —
// and are therefore subscribed for the whole process lifetime, not per
// leadership term — call this at Start entry so events buffered during a
// previous leadership term (or while not leader) are not replayed into the
// new term. Events that arrive after the flush are dispatched normally.
func (b *Base) FlushPending() {
	flushed := 0
	// The mailbox queue holds events the intake goroutine already moved off
	// the channel; they are exactly as stale as buffered channel events, so
	// a flush must clear both. Without this, a leader-only mailbox component
	// restarted on leadership re-acquisition would replay the PREVIOUS
	// term's queued events ahead of the fresh term's (the channel flush
	// below can't see them).
	b.mbMu.Lock()
	flushed += len(b.mbQueue)
	b.mbQueue = nil
	b.mbMu.Unlock()
	for {
		select {
		case <-b.eventChan:
			flushed++
		default:
			if flushed > 0 {
				b.logger.Debug(b.name+" discarded stale buffered events at start",
					"count", flushed)
			}
			return
		}
	}
}

// Stop signals the event loop to exit. Safe to call multiple times.
func (b *Base) Stop() {
	b.stopOnce.Do(func() {
		close(b.stopCh)
	})
}

// EventBus returns the bus the component subscribed to, for use in handler
// implementations that need to publish response events.
func (b *Base) EventBus() *busevents.EventBus { return b.eventBus }

// Logger returns the component-annotated logger.
func (b *Base) Logger() *slog.Logger { return b.logger }

// Name returns the component name supplied at construction.
func (b *Base) Name() string { return b.name }
