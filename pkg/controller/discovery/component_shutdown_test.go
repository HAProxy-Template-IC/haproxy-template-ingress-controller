// Copyright 2025 Philipp Hossner
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0

package discovery

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// Discovery is an all-replica component recreated every controller iteration; a
// config/credential change cancels its context and rebuilds the tree. Its only
// teardown signal is ctx.Done() in Start (it never sees LostLeadershipEvent).
// A version-probe retry timer (time.AfterFunc, backoff up to maxRetryInterval)
// armed before shutdown must be stopped when Start returns — otherwise it fires
// against the torn-down iteration's EventBus and keeps the dead Component
// reachable for up to a minute. Sibling timer-driven components (drift monitor,
// scheduler, metrics) all stop their timers on shutdown; this pins that Start
// does too.
func TestStart_StopsRetryTimerOnShutdown(t *testing.T) {
	c := newTestComponentWithoutHAProxy(t)

	// Arm a retry timer whose delay is long enough that it can only fire if
	// Start fails to stop it on shutdown.
	fired := make(chan struct{}, 1)
	c.retryTimerMu.Lock()
	c.retryTimer = time.AfterFunc(50*time.Millisecond, func() { fired <- struct{}{} })
	c.retryTimerMu.Unlock()

	// ctx already cancelled → Start takes the ctx.Done() path immediately
	// (component.Base returns nil on graceful shutdown), and its defer must
	// stop the armed timer well before the 50ms delay elapses.
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	require.NoError(t, c.Start(ctx))

	select {
	case <-fired:
		t.Fatal("retry timer fired after Start returned — timer leaked past shutdown")
	case <-time.After(150 * time.Millisecond):
		// Timer was stopped by Start's defer; no post-teardown fire.
	}
}
