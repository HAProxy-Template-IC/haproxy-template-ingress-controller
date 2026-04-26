// Copyright 2025 Philipp Hossner
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0

package configchange

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"gitlab.com/haproxy-haptic/haptic/pkg/controller/testutil"
	coreconfig "gitlab.com/haproxy-haptic/haptic/pkg/core/config"
)

// sendPendingConfig is the debounce-timer callback that signals the
// controller to reinitialize. It has THREE branches; only the
// happy path is tested via the existing
// TestConfigChangeHandler_HandleConfigValidated_SignalController.
// The two early-exit / failure-mode branches were uncovered:
//
//  1. nil pendingConfig → silent no-op. Pendingset is cleared
//     when the timer wasn't actually armed (e.g. drift trigger
//     stops it). Without this guard, the function would panic on
//     the channel send below (h.configChangeCh <- nil would
//     succeed but propagate a nil config to the controller, which
//     would either panic in iteration setup or silently use stale
//     state).
//
//  2. Channel full (non-blocking select default) → log a warning
//     AND clear pendingConfig anyway. The non-blocking select is
//     load-bearing: the controller's reinit channel is a bounded
//     ring; if it's full, a blocking send would deadlock the
//     debounce-timer goroutine, locking out future config changes.
//     The cleared pendingConfig MUST also be observable so a
//     regression that leaked pending state across timer cycles
//     surfaces immediately.

// pendingConfigHandler builds a minimal handler with the channel
// pre-wired. We bypass NewConfigChangeHandler / Start to keep the
// test focused on sendPendingConfig's logic.
func pendingConfigHandler(t *testing.T, channelBuffer int) (handler *ConfigChangeHandler, configCh chan *coreconfig.Config) {
	t.Helper()
	_, logger := testutil.NewTestBusAndLogger()
	ch := make(chan *coreconfig.Config, channelBuffer)
	h := &ConfigChangeHandler{
		logger:         logger,
		configChangeCh: ch,
	}
	return h, ch
}

func TestSendPendingConfig_NilPendingIsNoOp(t *testing.T) {
	// pendingConfig defaults to nil. The function MUST early-return
	// without sending anything (and crucially without sending nil
	// to the channel — which would type-check as a valid channel
	// op but propagate nil through the controller's reinit path).
	h, ch := pendingConfigHandler(t, 1)
	require.Nil(t, h.pendingConfig,
		"baseline: pendingConfig must start nil for the assertion to be meaningful")

	require.NotPanics(t, func() { h.sendPendingConfig() },
		"nil pendingConfig MUST be a silent no-op — the early return "+
			"protects against the case where the timer fires AFTER another "+
			"path cleared the pending config (e.g. drift trigger pre-empts "+
			"the debounce). Without it, the function would propagate nil "+
			"through the channel to the controller's iteration setup, "+
			"which either panics or silently uses stale config")

	// Channel MUST stay empty.
	assert.Empty(t, ch,
		"nil-pending guard MUST NOT push anything onto the reinit channel")
}

func TestSendPendingConfig_ChannelFullLogsAndClearsPending(t *testing.T) {
	// Unbuffered channel + no reader → next send blocks. The
	// non-blocking select MUST take the default branch (warn-log
	// path) instead of deadlocking the debounce-timer goroutine.
	h, ch := pendingConfigHandler(t, 0) // unbuffered: any send blocks

	cfg := &coreconfig.Config{}
	h.pendingConfig = cfg

	require.NotPanics(t, func() { h.sendPendingConfig() },
		"channel-full path MUST take the non-blocking-select default "+
			"branch — a blocking send here would deadlock the debounce-"+
			"timer goroutine, locking out future config changes")

	// pendingConfig MUST be cleared even on the channel-full path —
	// see the unconditional assignment at the top of sendPendingConfig.
	// A regression that left it set would either fire the same config
	// repeatedly on every timer tick (eventually succeeding when a
	// reader appeared) or accumulate stale state across cycles.
	assert.Nil(t, h.pendingConfig,
		"pendingConfig MUST be cleared regardless of send outcome — "+
			"the unconditional clear at the top of sendPendingConfig is "+
			"the contract that prevents the same pending value from being "+
			"re-sent on every timer cycle")

	// Sanity: the channel was full so nothing landed on it.
	select {
	case got := <-ch:
		t.Fatalf("channel-full path MUST NOT have queued anything; got %v", got)
	default:
		// expected
	}
}
