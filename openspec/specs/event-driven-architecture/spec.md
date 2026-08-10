# Event-Driven Architecture

## Purpose

Generic EventBus infrastructure providing pub/sub messaging, scatter-gather requests, typed subscriptions, and pre-start buffering for component coordination.

## Requirements

### Requirement: Non-Blocking Publish

Publishing an event SHALL be non-blocking for the publisher. If a subscriber's channel buffer is full, the event SHALL be dropped for that subscriber rather than blocking the publisher.

#### Scenario: Slow subscriber does not block publisher

WHEN a subscriber's channel buffer is full and an event is published
THEN the publish call SHALL return immediately and the event SHALL be dropped for the slow subscriber.

#### Scenario: Event delivered to subscribers with available buffer

WHEN an event is published and subscribers have buffer capacity
THEN all subscribers with available buffer space SHALL receive the event.

### Requirement: Pre-Start Buffering

Events published before `Start()` is called SHALL be buffered internally. When `Start()` is called, all buffered events SHALL be replayed to subscribers in order before the bus begins processing new events.

#### Scenario: Events buffered before Start

WHEN events are published before Start() is called
THEN those events SHALL be stored internally and not delivered to subscribers yet.

#### Scenario: Buffered events replayed on Start

WHEN Start() is called after events have been buffered
THEN all buffered events SHALL be delivered to subscribers in the order they were published.

### Requirement: Pause and Resume Buffering

`Pause()` SHALL return a started bus to buffering mode: events published while paused SHALL be buffered instead of delivered, and `Publish` SHALL return 0 for them. A subsequent `Start()` SHALL replay the buffered events to subscribers in publish order. This pause/resume cycle exists so leadership transitions can buffer events while late-subscribing leader-only components register, without those components missing state. Both `Pause()` and `Start()` SHALL be idempotent and safe to call concurrently with `Publish` and `Subscribe`.

The buffer (both pre-start and paused) SHALL be capped at MaxPreStartBufferSize (1000 events); once full, further buffered publishes SHALL be dropped with a WARN log naming the event type.

#### Scenario: Events buffered while paused are replayed on Start

- **WHEN** the bus is paused, three events are published, a new component subscribes, and Start() is called
- **THEN** the new subscriber SHALL receive all three events in publish order.

#### Scenario: Publish returns zero while buffering

- **WHEN** an event is published on a paused (or not-yet-started) bus
- **THEN** Publish SHALL return 0.

#### Scenario: Buffer cap drops with warning

- **WHEN** the buffered-event count has reached 1000 and another event is published before Start()
- **THEN** that event SHALL be dropped and a WARN log SHALL record the capacity and event type.

#### Scenario: Pause is idempotent

- **WHEN** Pause() is called on a bus that is already paused (or never started)
- **THEN** the call SHALL have no additional effect and previously buffered events SHALL be retained.

### Requirement: Lossy Subscriptions and Drop Accounting

Subscriptions SHALL be either critical (the default `Subscribe` and `SubscribeTypes`) or lossy (`SubscribeLossy`, universal subscriptions only), and the bus SHALL account for buffer-full drops separately per class. A drop from a critical subscription SHALL increment the critical drop counter AND invoke the optional drop callback registered via `SetDropCallback` (receiving the event type, subscriber name, and buffer size). A drop from a lossy subscription SHALL increment the observability drop counter silently — no callback — because drops from observability consumers (commentator, debug taps) are expected under load and must not raise the same alerts as business-critical backpressure. Both counters SHALL be readable at runtime.

Subscribing after `Start()` on a non-suppressed path SHALL log a WARN including the caller's source file and line, since the subscriber may have missed buffered events; the leader-only typed subscription variant SHALL suppress this warning because late subscription is intentional there. Subscriber buffer sizes SHALL be drawn from named tier constants (10, 50, 100, 200, 1000) rather than ad-hoc per-component numbers.

#### Scenario: Critical drop fires callback and counter

- **WHEN** a critical subscriber's buffer is full and an event is published
- **THEN** the critical drop counter SHALL increment and the drop callback (if set) SHALL be invoked with the drop details.

#### Scenario: Lossy drop is silent

- **WHEN** a lossy subscriber's buffer is full and an event is published
- **THEN** the observability drop counter SHALL increment and the drop callback SHALL NOT be invoked.

#### Scenario: Late subscription warns with caller location

- **WHEN** a component calls Subscribe after Start() has already run
- **THEN** a WARN log SHALL be emitted identifying the calling source file and line.

#### Scenario: Leader-only late subscription does not warn

- **WHEN** a leader-only component subscribes via the leader-only typed variant after Start()
- **THEN** no late-subscription warning SHALL be logged.

### Requirement: Subscribe with Buffer Size

`Subscribe(name, bufferSize)` SHALL return a channel of events with the specified buffer capacity. The `name` parameter identifies the subscriber for drop accounting and debug logging. The subscriber SHALL receive all events published after subscription.

#### Scenario: Subscriber receives events after subscription

WHEN a subscriber calls Subscribe("mycomponent", 100) and events are subsequently published
THEN the subscriber SHALL receive those events on the returned channel.

#### Scenario: Buffer size determines channel capacity

WHEN Subscribe is called with name "mycomponent" and bufferSize 50
THEN the returned channel SHALL have a buffer capacity of 50.

### Requirement: Type-Filtered Subscriptions

`SubscribeTypes(name, bufferSize, typeNames...)` SHALL return a channel that only receives events whose type name matches one of the specified type names. The `name` parameter identifies the subscriber for drop accounting and debug logging. Events of non-matching types SHALL NOT be delivered to the subscriber.

#### Scenario: Filtered subscriber receives only matching types

WHEN a subscriber calls SubscribeTypes("mycomponent", 100, "ReconciliationTriggered") and events of various types are published
THEN the subscriber SHALL only receive events of type "ReconciliationTriggered".

#### Scenario: Non-matching events not delivered

WHEN a type-filtered subscriber is registered and events of non-matching types are published
THEN those events SHALL NOT appear on the subscriber's channel.

### Requirement: Scatter-Gather Requests

`Request(ctx, req, opts)` SHALL publish a request event and collect responses from responders within a configurable timeout. Responses SHALL be correlated to requests via a request ID. The method SHALL return all collected responses when the timeout expires or all expected responses are received.

#### Scenario: Request collects responses within timeout

WHEN a Request is made with a 5-second timeout and two responders reply within 2 seconds
THEN the Request SHALL return both responses.

#### Scenario: Request times out with partial responses

WHEN a Request is made with a 1-second timeout and only one of two expected responders replies
THEN the Request SHALL return the one received response after the timeout expires.

#### Scenario: Response correlation via request ID

WHEN multiple concurrent Requests are in-flight
THEN each Request SHALL only receive responses matching its own request ID.

### Requirement: Constructor-Time Subscription

Components MUST subscribe to the EventBus in their constructors (during `New()` calls), before `EventBus.Start()` is called. This ensures all subscribers are registered before any events are delivered, preventing race conditions.

#### Scenario: Subscriber registered before Start receives all events

WHEN a component subscribes in its constructor and Start() is called afterward
THEN the component SHALL receive all events including those buffered during pre-start.

#### Scenario: Late subscription misses buffered events

WHEN a component subscribes after Start() has already been called and buffered events replayed
THEN the component SHALL NOT receive the previously buffered events.

### Requirement: Concurrent Publish Safety

Multiple goroutines SHALL be able to publish events concurrently without data races. The EventBus SHALL use an RWMutex for the subscriber list, allowing concurrent read access during publishes while serializing subscriber list modifications.

#### Scenario: Concurrent publishes from multiple goroutines

WHEN multiple goroutines publish events simultaneously
THEN all events SHALL be delivered without data races or panics.

#### Scenario: Subscriber addition during publish

WHEN a new subscriber is added while publishes are in progress
THEN the subscriber list modification SHALL be serialized via write lock without causing data races with concurrent publishes.
