// Copyright 2025 Philipp Hossner
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0

package component

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	busevents "gitlab.com/haproxy-haptic/haptic/pkg/events"
)

// Base.Stop() uses sync.Once to make repeated calls safe — pin that
// the second Stop() doesn't panic from closing an already-closed
// channel and that calling Stop() before Start() also doesn't break.
func TestBase_Stop_SafeForMultipleCalls(t *testing.T) {
	bus := busevents.NewEventBus(16)
	base := New(&Config{
		EventBus: bus,
		Logger:   discardLogger(),
		Name:     "test",
		Handler:  &recordingHandler{},
	})

	// First Stop() closes the channel.
	assert.NotPanics(t, func() { base.Stop() })

	// Subsequent Stop()s must be no-ops, not panics — sync.Once guards
	// the close.
	assert.NotPanics(t, func() { base.Stop() }, "second Stop() must not re-close the channel")
	assert.NotPanics(t, func() { base.Stop() }, "third Stop() must remain a no-op")
}

// Stop() must wake a Start()-blocked goroutine without needing the
// context to be cancelled. Pin both the Stop-then-Start synchronisation
// (Start sees the closed stopCh on entry) and the more interesting
// Start-then-Stop case.
func TestBase_Stop_TerminatesStart(t *testing.T) {
	bus := busevents.NewEventBus(16)
	base := New(&Config{
		EventBus: bus,
		Logger:   discardLogger(),
		Name:     "test",
		Handler:  &recordingHandler{},
	})

	startErr := make(chan error, 1)
	go func() { startErr <- base.Start(context.Background()) }()

	// Give Start a moment to enter its select loop.
	time.Sleep(10 * time.Millisecond)
	base.Stop()

	select {
	case err := <-startErr:
		assert.NoError(t, err, "Stop() must terminate Start() with nil (graceful shutdown)")
	case <-time.After(time.Second):
		t.Fatal("Start() did not return after Stop()")
	}
}

// EventBus, Logger, and Name accessors expose constructor inputs so
// handlers can publish events / log / identify themselves. Pin that
// each returns the value supplied at construction (and that the
// logger is annotated with `component=<name>`, since handlers rely on
// that for log scrapers).
func TestBase_Accessors(t *testing.T) {
	bus := busevents.NewEventBus(16)
	logger := discardLogger()
	base := New(&Config{
		EventBus: bus,
		Logger:   logger,
		Name:     "renderer",
		Handler:  &recordingHandler{},
	})

	t.Run("EventBus returns the constructor's bus verbatim", func(t *testing.T) {
		assert.Same(t, bus, base.EventBus())
	})

	t.Run("Name returns the constructor's name", func(t *testing.T) {
		assert.Equal(t, "renderer", base.Name())
	})

	t.Run("Logger returns a non-nil logger annotated with component name", func(t *testing.T) {
		// The constructor wraps the supplied logger with `.With("component", name)`,
		// so the returned logger isn't the same pointer as the input.
		got := base.Logger()
		require.NotNil(t, got)
		assert.NotSame(t, logger, got, "Logger must be wrapped with component annotation, not the raw input")
	})
}

// New uses slog.Default() when Config.Logger is nil — pin that this
// fallback works (no nil-pointer deref on first log call) so callers
// don't need to remember to supply a logger.
func TestBase_New_NilLoggerFallsBackToSlogDefault(t *testing.T) {
	bus := busevents.NewEventBus(16)
	base := New(&Config{
		EventBus: bus,
		Logger:   nil, // explicitly nil
		Name:     "test",
		Handler:  &recordingHandler{},
	})

	require.NotNil(t, base.Logger(), "nil Config.Logger must fall back to a non-nil logger")
	// Sanity: the logger must be usable without panicking.
	assert.NotPanics(t, func() { base.Logger().Debug("smoke test") })
}
