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

func TestNewDeployStack_WiresRuntimeBypassState(t *testing.T) {
	bus, logger := testutil.NewTestBusAndLogger()
	domainMetrics := metrics.NewMetrics(prometheus.NewRegistry())

	stack := NewDeployStack(bus, &coreconfig.Config{}, logger, domainMetrics)

	require.NotNil(t, stack.Deployer)
	require.NotNil(t, stack.Scheduler)
	require.NotNil(t, stack.DriftMonitor)
	require.NotNil(t, stack.Deployer.versionCache)

	assert.Same(t, stack.Deployer.versionCache, stack.Scheduler.runtimeBypass.configCache,
		"structural sync and runtime bypass must share one atomic endpoint observation")
	assert.NotNil(t, stack.Scheduler.runtimeBypass.recordFastPath,
		"the bypass must reach the metrics registry — without it every "+
			"haptic_runtime_fast_path_* counter stays flat, which reads as an idle "+
			"fast path rather than a broken one")
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
