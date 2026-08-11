// Copyright 2025 Philipp Hossner
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0

package deployer

import (
	"testing"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"gitlab.com/haproxy-haptic/haptic/pkg/controller/metrics"
	"gitlab.com/haproxy-haptic/haptic/pkg/controller/testutil"
	coreconfig "gitlab.com/haproxy-haptic/haptic/pkg/core/config"
)

// Both recorder fields are nil-tolerant at the point of use, so a missing
// connection produces no compile error, no log, and no failing deploy — the
// activation half silently reintroduces the parked-config class (#112) and the
// fast-path counters silently read as an idle fast path. NewDeployStack exists
// so a caller cannot forget them; this is what stops the constructor itself
// from dropping one.

func TestNewDeployStack_WiresRuntimeBypassState(t *testing.T) {
	bus, logger := testutil.NewTestBusAndLogger()
	domainMetrics := metrics.NewMetrics(prometheus.NewRegistry())

	stack := NewDeployStack(bus, &coreconfig.Config{}, logger, domainMetrics)

	require.NotNil(t, stack.Deployer)
	require.NotNil(t, stack.Scheduler)
	require.NotNil(t, stack.DriftMonitor)

	assert.NotNil(t, stack.Scheduler.runtimeBypass.recordActivation,
		"the bypass must share the deployer's activation state — without it the "+
			"bypass applies silently and the structural sync keeps an activation "+
			"proof the bypass has since invalidated (#112)")
	assert.NotNil(t, stack.Scheduler.runtimeBypass.recordFastPath,
		"the bypass must reach the metrics registry — without it every "+
			"haptic_runtime_fast_path_* counter stays flat, which reads as an idle "+
			"fast path rather than a broken one")
	assert.NotNil(t, stack.Scheduler.runtimeBypass.retainAuthorities,
		"discovery must evict deployer observations for retired endpoint authorities")
}

func TestNewDeployStack_AppliesConfiguredIntervals(t *testing.T) {
	bus, logger := testutil.NewTestBusAndLogger()

	cfg := &coreconfig.Config{}
	stack := NewDeployStack(bus, cfg, logger, metrics.NewMetrics(prometheus.NewRegistry()))

	// Taking the whole config rather than positional durations is deliberate: a
	// forgotten duration argument silently becomes 0, and a zero
	// minDeploymentInterval disables reload throttling entirely.
	assert.Equal(t, cfg.Dataplane.GetMinDeploymentInterval(), stack.Scheduler.minDeploymentInterval)
	assert.Equal(t, cfg.Dataplane.GetDeploymentTimeout(), stack.Scheduler.deploymentTimeout)
	assert.Positive(t, stack.Scheduler.minDeploymentInterval,
		"an empty config must still yield the documented default, not zero")
}
