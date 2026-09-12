// Copyright 2026 Philipp Hossner
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0

//go:build e2e

package e2e

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestAvailabilityMonitorStartupEvidence(t *testing.T) {
	for _, tc := range []struct {
		name                 string
		handshakeError       error
		exited               bool
		cancelAfterHandshake bool
		stderr               string
		wantError            string
	}{
		{name: "ready"},
		{name: "bad handshake", handshakeError: errors.New("invalid handshake"), wantError: "invalid handshake"},
		{name: "early exit", exited: true, wantError: "exited before startup"},
		{name: "request failed before handshake", handshakeError: errors.New("no handshake"), stderr: "HAPTIC_AVAILABILITY_FAILED status=503", wantError: "request failed before startup"},
		{name: "request failed before exit", exited: true, stderr: "HAPTIC_AVAILABILITY_FAILED status=503", wantError: "request failed before startup"},
		{name: "canceled during handshake", cancelAfterHandshake: true, wantError: "context canceled"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			ready := make(chan error, 1)
			done := make(chan struct{})
			if tc.exited {
				close(done)
			} else {
				ready <- tc.handshakeError
			}
			terminated := false
			cleanupErr := errors.New("cleanup failure")
			terminate := func() error {
				terminated = true
				return cleanupErr
			}
			recordHandshake := func() {
				if tc.cancelAfterHandshake {
					cancel()
				}
			}
			outcome := func() availabilityMonitorOutcome {
				require.True(t, terminated, "must join the command before reading its stderr")
				return availabilityMonitorOutcome{err: errors.New("command exited"), stderr: tc.stderr}
			}
			err := awaitAvailabilityMonitor(ctx, ready, done, terminate, recordHandshake, outcome)
			if tc.wantError == "" {
				require.NoError(t, err)
				require.False(t, terminated)
				return
			}
			require.ErrorContains(t, err, tc.wantError)
			require.True(t, terminated)
			require.ErrorContains(t, err, cleanupErr.Error())
		})
	}
}

func TestAvailabilityMonitorStopPreservesFailureAndRunsOnce(t *testing.T) {
	for _, tc := range []struct {
		name       string
		outcome    availabilityMonitorOutcome
		cleanupErr error
		wantError  string
	}{
		{name: "intentional stop"},
		{name: "cleanup failed", cleanupErr: errors.New("remote cleanup failed"), wantError: "remote cleanup failed"},
		{name: "unexpected clean exit", outcome: availabilityMonitorOutcome{unexpectedExit: true}, wantError: "exited early"},
		{name: "request failed", outcome: availabilityMonitorOutcome{err: errors.New("curl failed"), stderr: "HAPTIC_AVAILABILITY_FAILED status=000"}, wantError: "HAProxy request failed"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var cleanupCalls atomic.Int32
			var readBeforeCleanup atomic.Bool
			stop := availabilityMonitorStop(func() error {
				cleanupCalls.Add(1)
				return tc.cleanupErr
			}, func() availabilityMonitorOutcome {
				if cleanupCalls.Load() == 0 {
					readBeforeCleanup.Store(true)
				}
				return tc.outcome
			})
			for _, err := range concurrentMonitorStops(stop) {
				if tc.wantError == "" {
					require.NoError(t, err)
				} else {
					require.ErrorContains(t, err, tc.wantError)
				}
			}
			require.EqualValues(t, 1, cleanupCalls.Load())
			require.False(t, readBeforeCleanup.Load())
		})
	}
}

func concurrentMonitorStops(stop func() error) []error {
	results := make(chan error, 8)
	for range cap(results) {
		go func() { results <- stop() }()
	}
	verdicts := make([]error, 0, cap(results))
	for range cap(results) {
		verdicts = append(verdicts, <-results)
	}
	return verdicts
}
