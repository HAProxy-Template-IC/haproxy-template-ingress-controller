package events

import (
	"context"
	"fmt"
	"sync"
	"time"
)

// Request is the interface for request events in the scatter-gather pattern.
//
// Requests are broadcast to all subscribers (scatter phase), and responses
// are correlated by RequestID (gather phase).
type Request interface {
	Event
	// RequestID returns a unique identifier for this request.
	// Responses must include this ID to be correlated correctly.
	RequestID() string
}

// Response is the interface for response events in the scatter-gather pattern.
//
// Each response includes the original RequestID and the name of the responder
// for tracking and debugging purposes.
type Response interface {
	Event
	// RequestID returns the ID of the request this response belongs to.
	RequestID() string
	// Responder returns the name of the component that sent this response.
	Responder() string
}

// RequestOptions configures the behavior of a scatter-gather request.
type RequestOptions struct {
	// Timeout is the maximum time to wait for responses.
	// If zero, defaults to 10 seconds.
	Timeout time.Duration

	// ExpectedResponders lists the names of components expected to respond.
	// If empty, the request will wait indefinitely for responses.
	ExpectedResponders []string

	// MinResponses is the minimum number of responses required.
	// If zero, all ExpectedResponders must respond.
	// Set to a lower value to implement graceful degradation.
	MinResponses int
}

// RequestResult contains the aggregated results from a scatter-gather request.
type RequestResult struct {
	// Responses contains all responses received before timeout or completion.
	Responses []Response

	// Errors contains error messages for responders that didn't respond
	// or timed out. Empty if all expected responders replied.
	Errors []string
}

// executeRequest implements the scatter-gather pattern.
//
// This is the core implementation separated from EventBus.Request() for testability.
func executeRequest(ctx context.Context, bus *EventBus, request Request, opts RequestOptions) (*RequestResult, error) {
	// Apply defaults
	if opts.Timeout == 0 {
		opts.Timeout = 10 * time.Second
	}

	minResponses := opts.MinResponses
	if minResponses == 0 {
		minResponses = len(opts.ExpectedResponders)
	}

	// Validate options
	if len(opts.ExpectedResponders) == 0 {
		return nil, fmt.Errorf("expected responders cannot be empty")
	}
	if minResponses > len(opts.ExpectedResponders) {
		return nil, fmt.Errorf("min responses (%d) cannot exceed expected responders (%d)", minResponses, len(opts.ExpectedResponders))
	}

	// Create response collector
	collector := &responseCollector{
		requestID:          request.RequestID(),
		expectedResponders: opts.ExpectedResponders,
		minResponses:       minResponses,
		responses:          make([]Response, 0, minResponses),
		responders:         make(map[string]bool, len(opts.ExpectedResponders)),
		done:               make(chan struct{}),
	}

	// Subscribe to responses for this request.
	// Suppress late subscription warning because scatter-gather subscriptions
	// are intentionally created after Start() during normal operation.
	responseChan := bus.subscribeInternal(100, true)

	// Start response listener in background
	listenerCtx, cancelListener := context.WithCancel(ctx)
	defer cancelListener()

	go collector.listen(listenerCtx, responseChan)

	// Publish request (scatter phase)
	bus.Publish(request)

	// Wait for responses (gather phase)
	timeoutCtx, cancelTimeout := context.WithTimeout(ctx, opts.Timeout)
	defer cancelTimeout()

	select {
	case <-collector.done:
		// Got minimum required responses
		return collector.result(), nil

	case <-timeoutCtx.Done():
		// Timeout or cancellation
		result := collector.result()
		if ctx.Err() != nil {
			// Context was cancelled by caller
			return result, ctx.Err()
		}
		// Timeout occurred
		return result, fmt.Errorf("request timeout after %v", opts.Timeout)
	}
}

// responseCollector handles the gather phase of scatter-gather.
//
// It listens for Response events matching the request ID and tracks
// which responders have replied. Once the minimum number of responses
// is received, it signals completion via the done channel.
type responseCollector struct {
	requestID          string
	expectedResponders []string
	minResponses       int

	mu         sync.Mutex
	responses  []Response
	responders map[string]bool // tracks which responders have replied
	done       chan struct{}
	completed  bool
}

// listen continuously processes events looking for matching responses.
func (c *responseCollector) listen(ctx context.Context, events <-chan Event) {
	for {
		select {
		case event := <-events:
			// Check if it's a response to our request
			if resp, ok := event.(Response); ok && resp.RequestID() == c.requestID {
				c.addResponse(resp)
			}

		case <-ctx.Done():
			return
		}
	}
}

// addResponse records a response and checks if we have enough to complete.
func (c *responseCollector) addResponse(resp Response) {
	c.mu.Lock()
	defer c.mu.Unlock()

	// Ignore if already completed
	if c.completed {
		return
	}

	// Record this responder (even if duplicate, we only count once)
	responder := resp.Responder()
	if !c.responders[responder] {
		c.responders[responder] = true
		c.responses = append(c.responses, resp)
	}

	// Check if we have enough responses
	if len(c.responses) >= c.minResponses {
		c.completed = true
		close(c.done)
	}
}

// result creates the final RequestResult with all responses and errors.
func (c *responseCollector) result() *RequestResult {
	c.mu.Lock()
	defer c.mu.Unlock()

	result := &RequestResult{
		Responses: c.responses,
		Errors:    []string{},
	}

	// Find missing responders
	for _, expected := range c.expectedResponders {
		if !c.responders[expected] {
			result.Errors = append(result.Errors,
				fmt.Sprintf("no response from %s", expected))
		}
	}

	return result
}
