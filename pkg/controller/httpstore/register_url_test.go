// Copyright 2025 Philipp Hossner
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0

package httpstore

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"gitlab.com/haproxy-haptic/haptic/pkg/controller/testutil"
	"gitlab.com/haproxy-haptic/haptic/pkg/httpstore"
)

// RegisterURL has three branches:
//
//  1. delay == 0          → no-op  (covered by TestComponent_RegisterURL_NoDelay)
//  2. already registered  → no-op  (covered by TestComponent_RegisterURL_AlreadyRegistered)
//  3. delay > 0, new URL  → time.AfterFunc(delay, ...) is created and stored
//
// Branch (3) is the only one currently NOT exercised. The existing
// TestComponent_RegisterURL_WithDelay despite its name actually tests
// the no-delay case. Without coverage, a regression that flipped the
// "already registered" guard around the wrong way (early-returning
// EVERY call instead of just duplicates) would silently disable
// periodic refresh entirely — templates would see stale content
// forever and `delay`-configured URLs would behave the same as
// one-shot fetches. That kind of bug is invisible in a unit-test
// suite and impossible to catch without standing up a real refresh
// flow in production.
//
// Pin the happy-path branch by:
//   - Standing up a real HTTP server so the store has a populated
//     entry with Options.Delay > 0;
//   - Calling RegisterURL once;
//   - Asserting the refreshers map gained EXACTLY one entry under
//     the URL key.
func TestComponent_RegisterURL_HappyPath_AddsTimerForURLWithDelay(t *testing.T) {
	bus, logger := testutil.NewTestBusAndLogger()
	c := New(bus, logger, time.Minute)

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("hello"))
	}))
	defer server.Close()

	// Long delay so the timer doesn't fire during the test.
	_, err := c.store.Fetch(context.Background(), server.URL,
		httpstore.FetchOptions{Delay: time.Hour}, nil)
	require.NoError(t, err)

	c.RegisterURL(server.URL)

	c.mu.Lock()
	timer, exists := c.refreshers[server.URL]
	got := len(c.refreshers)
	c.mu.Unlock()

	assert.True(t, exists,
		"URL with delay > 0 must be registered under its own key — "+
			"a regression that returned early before the AfterFunc call "+
			"would silently disable periodic refresh and templates would "+
			"see stale content indefinitely")
	assert.Equal(t, 1, got, "exactly one refresh timer must be added")

	if timer != nil {
		timer.Stop() // prevent leak past the test
	}
}
