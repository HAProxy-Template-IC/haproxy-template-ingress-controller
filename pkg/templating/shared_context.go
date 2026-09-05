package templating

import (
	"context"
	"errors"
	"sync"

	"golang.org/x/sync/singleflight"
)

// SharedContext provides thread-safe caching with compute-once semantics.
// It is used for sharing data between parallel template renders within a single
// reconciliation cycle.
//
// The API is intentionally minimal to prevent race conditions:
//   - ComputeIfAbsent stores values atomically (uses singleflight)
//   - Get provides read-only access to existing values
//   - No Set method exists - prevents racy check-then-act patterns
//   - The wasComputed return value enables deduplication (FirstSeen pattern)
type SharedContext struct {
	mu    sync.RWMutex
	data  map[string]any
	group singleflight.Group
}

// SharedContributionRecorder receives immutable owner-scoped state operations.
type SharedContributionRecorder interface {
	Unique(cell, key, value string)
}

type sharedValuePublisher interface {
	Publish(cell, key string, value any)
}

type sharedDetachedValuePublisher interface {
	PublishDetached(cell, key string, value *IncrementalDetachedValue)
}

type sharedRankedValuePublisher interface {
	PublishRanked(cell, key, rank string, value any)
}

type sharedRankedDetachedValuePublisher interface {
	PublishRankedDetached(cell, key, rank string, value *IncrementalDetachedValue)
}

// SharedValueSelector resolves one exact publication for an incremental component.
type SharedValueSelector interface {
	Select(group, cell, key string) (any, bool, error)
	SelectValues(group, cell string) ([]any, error)
	Count(group, cell string) (int, error)
}

// SharedContributionContext is the deterministic shared-state surface.
type SharedContributionContext interface {
	Unique(cell, key, value string) string
	Publish(cell, key string, value any) string
	PublishRanked(cell, key, rank string, value any) string
	Select(group, cell, key string) (any, bool)
	SelectValues(group, cell string) []any
	Count(group, cell string) int
	sharedContributionContext()
}

type sharedContributionContext struct {
	recorder         SharedContributionRecorder
	selector         SharedValueSelector
	executionContext context.Context
}

// NewSharedContext creates a new thread-safe shared context.
func NewSharedContext() *SharedContext {
	return &SharedContext{
		data: make(map[string]any),
	}
}

// NewSharedContributionContext creates an owner-scoped incremental contribution surface.
func NewSharedContributionContext(
	recorder SharedContributionRecorder,
	selectors ...SharedValueSelector,
) SharedContributionContext {
	return newSharedContributionContext(context.Background(), recorder, selectors...)
}

// NewLeasedSharedContributionContext rejects work after ctx's component lease ends.
func NewLeasedSharedContributionContext(
	ctx context.Context,
	recorder SharedContributionRecorder,
	selectors ...SharedValueSelector,
) SharedContributionContext {
	if ctx == nil {
		panic(errors.New("shared contribution execution context is nil"))
	}
	return newSharedContributionContext(ctx, recorder, selectors...)
}

func newSharedContributionContext(
	ctx context.Context,
	recorder SharedContributionRecorder,
	selectors ...SharedValueSelector,
) SharedContributionContext {
	if isNilValue(recorder) {
		panic(errors.New("shared contribution recorder is nil"))
	}
	if len(selectors) > 1 {
		panic(errors.New("shared contribution context has multiple selectors"))
	}
	shared := &sharedContributionContext{recorder: recorder, executionContext: ctx}
	if len(selectors) == 1 {
		if isNilValue(selectors[0]) {
			panic(errors.New("shared contribution selector is nil"))
		}
		shared.selector = selectors[0]
	}
	return shared
}

// computePanic carries a panic raised inside a ComputeIfAbsent compute function
// back out through singleflight WITHOUT singleflight's *panicError wrapper. That
// wrapper's Error() appends a debug.Stack() dump, which turns a clean template
// fail()/env.Stop() halt into a page of Go stack trace in the rendered error.
// Returning the panic as an ordinary error keeps singleflight from wrapping it;
// ComputeIfAbsent then re-raises the ORIGINAL panic value unchanged, so the
// abort still propagates to every caller (the computing goroutine and any
// singleflight-deduplicated waiters) with its original message.
type computePanic struct{ value any }

func (c *computePanic) Error() string { return "compute panic" }

// Get returns the value for key, or nil if not found.
// This is a read-only operation - use ComputeIfAbsent for initialization.
func (s *SharedContext) Get(key string) any {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.data[key]
}

// ComputeIfAbsent returns the value for key, computing it if not present.
// Uses singleflight to ensure only one goroutine computes for a given key.
// Other goroutines waiting for the same key will receive the computed result.
//
// Returns (value, wasComputed) where:
//   - value: the stored value (either existing or newly computed)
//   - wasComputed: true only for the goroutine that actually ran compute()
//
// Use wasComputed for deduplication (FirstSeen pattern):
//
//	_, wasFirst := shared.ComputeIfAbsent("seen:"+key, func() any { return true })
//	if wasFirst {
//	    // First occurrence
//	}
//
// Use with nil fallback for read-only access:
//
//	val, _ := shared.ComputeIfAbsent("key", func() any { return nil })
//
// IMPORTANT: The compute function is called WITHOUT holding the mutex, so it
// may safely call ComputeIfAbsent for other keys (nested/recursive calls).
func (s *SharedContext) ComputeIfAbsent(key string, compute func() any) (any, bool) {
	// Fast path: check if already computed
	s.mu.RLock()
	if val, ok := s.data[key]; ok {
		s.mu.RUnlock()
		return val, false // Already exists, didn't compute
	}
	s.mu.RUnlock()

	// Track if THIS goroutine computed the value. Each goroutine has its own
	// weComputed variable, and only the goroutine whose closure runs will set it.
	// Note: singleflight's `shared` return value is true for ALL callers when
	// there are duplicates (including the one that ran the function), so we
	// can't rely on it.
	var weComputed bool

	// Slow path: use singleflight to compute exactly once
	r, err, _ := s.group.Do(key, func() (val any, err error) {
		// Double-check under lock before computing
		s.mu.Lock()
		if v, ok := s.data[key]; ok {
			s.mu.Unlock()
			return v, nil // Found in double-check, weComputed stays false
		}
		s.mu.Unlock()

		// Mark that THIS goroutine is computing
		weComputed = true

		// Catch a compute() panic (typically a template fail()/env.Stop()
		// halt) and hand it back as an ordinary error. Without this,
		// singleflight recovers the panic into a *panicError that appends a
		// debug.Stack() dump, and that stack ends up in the rendered error
		// message. The original value is re-raised below, so the abort is
		// unchanged for every caller — only the spurious stack is gone.
		defer func() {
			if p := recover(); p != nil {
				err = &computePanic{value: p}
			}
		}()

		// Compute WITHOUT holding lock - allows nested ComputeIfAbsent calls
		v := compute()

		// Store result under lock
		s.mu.Lock()
		s.data[key] = v
		s.mu.Unlock()

		return v, nil
	})

	if cp, ok := err.(*computePanic); ok {
		panic(cp.value)
	}

	return r, weComputed
}

// Unique contributes one value per key; the lowest stable owner wins.
func (*SharedContext) Unique(_, _, _ string) string {
	panic(errors.New("shared.Unique is only available inside an incremental component"))
}

// Publish declares an immutable keyed value inside an incremental component.
func (*SharedContext) Publish(_, _ string, _ any) string {
	panic(errors.New("shared.Publish is only available inside an incremental component"))
}

func (*SharedContext) PublishRanked(_, _, _ string, _ any) string {
	panic(errors.New("shared.PublishRanked is only available inside an incremental component"))
}

func (*SharedContext) Select(_, _, _ string) (any, bool) {
	panic(errors.New("shared.Select is only available inside an incremental component"))
}

func (*SharedContext) SelectValues(_, _ string) []any {
	panic(errors.New("shared.SelectValues is only available inside an incremental component"))
}

func (*SharedContext) Count(_, _ string) int {
	panic(errors.New("shared.Count is only available inside an incremental component"))
}

func (s *sharedContributionContext) Unique(cell, key, value string) string {
	release := s.begin("shared.Unique")
	defer release()
	s.recorder.Unique(cell, key, value)
	return ""
}

func (s *sharedContributionContext) Publish(cell, key string, value any) string {
	release := s.begin("shared.Publish")
	defer release()
	detached, err := NewIncrementalDetachedValue(value)
	if err != nil {
		panic(errors.New("shared.Publish value is not JSON serializable: " + err.Error()))
	}
	if publisher, ok := s.recorder.(sharedDetachedValuePublisher); ok {
		publisher.PublishDetached(cell, key, detached)
		return ""
	}
	publisher, ok := s.recorder.(sharedValuePublisher)
	if !ok {
		panic(errors.New("shared.Publish recorder is unavailable"))
	}
	legacy, err := ConsumeIncrementalDetachedValue(detached)
	if err != nil {
		panic(err)
	}
	publisher.Publish(cell, key, legacy)
	return ""
}

func (s *sharedContributionContext) PublishRanked(cell, key, rank string, value any) string {
	release := s.begin("shared.PublishRanked")
	defer release()
	detached, err := NewIncrementalDetachedValue(value)
	if err != nil {
		panic(errors.New("shared.PublishRanked value is not JSON serializable: " + err.Error()))
	}
	if publisher, ok := s.recorder.(sharedRankedDetachedValuePublisher); ok {
		publisher.PublishRankedDetached(cell, key, rank, detached)
		return ""
	}
	publisher, ok := s.recorder.(sharedRankedValuePublisher)
	if !ok {
		panic(errors.New("shared.PublishRanked recorder is unavailable"))
	}
	legacy, err := ConsumeIncrementalDetachedValue(detached)
	if err != nil {
		panic(err)
	}
	publisher.PublishRanked(cell, key, rank, legacy)
	return ""
}

func (s *sharedContributionContext) Select(group, cell, key string) (any, bool) {
	release := s.begin("shared.Select")
	defer release()
	if s.selector == nil {
		panic(errors.New("shared.Select selector is unavailable"))
	}
	value, found, err := s.selector.Select(group, cell, key)
	if err != nil {
		panic(err)
	}
	return value, found
}

func (s *sharedContributionContext) SelectValues(group, cell string) []any {
	release := s.begin("shared.SelectValues")
	defer release()
	if s.selector == nil {
		panic(errors.New("shared.SelectValues selector is unavailable"))
	}
	values, err := s.selector.SelectValues(group, cell)
	if err != nil {
		panic(err)
	}
	return values
}

func (s *sharedContributionContext) Count(group, cell string) int {
	release := s.begin("shared.Count")
	defer release()
	if s.selector == nil {
		panic(errors.New("shared.Count selector is unavailable"))
	}
	count, err := s.selector.Count(group, cell)
	if err != nil {
		panic(err)
	}
	return count
}

func (s *sharedContributionContext) begin(operation string) func() {
	release, err := beginIncrementalExecution(s.executionContext, operation)
	if err != nil {
		panic(err)
	}
	return release
}

func (*sharedContributionContext) sharedContributionContext() {}
