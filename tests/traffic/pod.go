// Copyright 2026 Philipp Hossner
// SPDX-License-Identifier: Apache-2.0

// Package traffic checks routes through owned pod tunnels.
package traffic

import (
	"context"
	"errors"
	"fmt"
	"time"

	"gitlab.com/haproxy-haptic/haptic/tests/kubeexec"
	"gitlab.com/haproxy-haptic/haptic/tests/testutil"
	"gitlab.com/haproxy-haptic/haptic/tests/tunnel"
)

type PodProbe struct {
	Client         kubeexec.Client
	Target         string
	Ports          []tunnel.Port
	StartupTimeout time.Duration
	RouteTimeout   time.Duration
	Interval       time.Duration
	Check          func(context.Context, []int) error
}

func Wait(ctx context.Context, probe *PodProbe) (result error) {
	if probe == nil || probe.Check == nil || probe.StartupTimeout <= 0 || probe.RouteTimeout <= 0 || probe.Interval <= 0 {
		return errors.New("pod probe needs a check and positive startup, route, and polling budgets")
	}
	forward, err := tunnel.Start(ctx, ctx, probe.Client, probe.Target, probe.Ports, probe.StartupTimeout)
	if err != nil {
		return err
	}
	defer func() {
		if forward != nil {
			result = errors.Join(result, forward.Process.Stop())
		}
	}()
	return testutil.Poll(ctx, testutil.WaitConfig{Timeout: probe.RouteTimeout, InitialInterval: probe.Interval, MaxInterval: probe.Interval, Multiplier: 1}, "routes through "+probe.Target, func(attempt context.Context) (testutil.PollResult, error) {
		select {
		case <-forward.Process.Done():
			if err := forward.Process.Stop(); err != nil {
				return testutil.PollFailed, err
			}
			forward = nil
			if err := attempt.Err(); err != nil {
				return testutil.PollFailed, err
			}
			deadline, _ := attempt.Deadline()
			next, startErr := tunnel.Start(ctx, ctx, probe.Client, probe.Target, probe.Ports, min(probe.StartupTimeout, time.Until(deadline)))
			if startErr != nil {
				return testutil.PollFailed, fmt.Errorf("restore pod tunnel: %w", startErr)
			}
			forward = next
		default:
		}
		if err := probe.Check(attempt, forward.Locals); err != nil {
			return testutil.PollPending, err
		}
		return testutil.PollSucceeded, nil
	})
}
