# Event-Driven Architecture

Generic EventBus infrastructure providing pub/sub messaging, scatter-gather requests, typed subscriptions, and pre-start buffering for component coordination.

## ADDED Requirements

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

### Requirement: Subscribe with Buffer Size

`Subscribe(bufferSize)` SHALL return a channel of events with the specified buffer capacity. The subscriber SHALL receive all events published after subscription.

#### Scenario: Subscriber receives events after subscription

WHEN a subscriber calls Subscribe(100) and events are subsequently published
THEN the subscriber SHALL receive those events on the returned channel.

#### Scenario: Buffer size determines channel capacity

WHEN Subscribe is called with bufferSize 50
THEN the returned channel SHALL have a buffer capacity of 50.

### Requirement: Type-Filtered Subscriptions

`SubscribeTypes(bufferSize, typeNames...)` SHALL return a channel that only receives events whose type name matches one of the specified type names. Events of non-matching types SHALL NOT be delivered to the subscriber.

#### Scenario: Filtered subscriber receives only matching types

WHEN a subscriber calls SubscribeTypes(100, "ReconciliationTriggered") and events of various types are published
THEN the subscriber SHALL only receive events of type "ReconciliationTriggered".

#### Scenario: Non-matching events not delivered

WHEN a type-filtered subscriber is registered and events of non-matching types are published
THEN those events SHALL NOT appear on the subscriber's channel.

### Requirement: Generic Typed Subscribe

`Subscribe[T](ctx, bus, bufferSize)` SHALL return a channel of type T. The generic subscription SHALL filter events by Go type and deliver only events matching type T, already cast to the correct type.

#### Scenario: Generic subscription delivers typed events

WHEN Subscribe[ReconciliationTriggeredEvent] is called and a ReconciliationTriggeredEvent is published
THEN the typed channel SHALL receive the event as a ReconciliationTriggeredEvent (not as interface{}).

#### Scenario: Generic subscription ignores non-matching types

WHEN Subscribe[ReconciliationTriggeredEvent] is called and a DeploymentCompletedEvent is published
THEN the typed channel SHALL NOT receive the DeploymentCompletedEvent.

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
