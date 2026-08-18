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

// fixedFence is a leadership term with a known epoch.
type fixedFence struct {
	epoch uint64
}

func (f fixedFence) Identity() string    { return "haptic-controller-0" }
func (f fixedFence) LeaderEpoch() uint64 { return f.epoch }

func TestNewDeployStack_WiresTheDeploySide(t *testing.T) {
	bus, logger := testutil.NewTestBusAndLogger()
	domainMetrics := metrics.NewMetrics(prometheus.NewRegistry())
	fence := fixedFence{epoch: 7}

	stack := NewDeployStack(bus, &coreconfig.Config{}, logger, domainMetrics, nil, fence)

	require.NotNil(t, stack.Deployer)
	require.NotNil(t, stack.Scheduler)
	require.NotNil(t, stack.DriftMonitor)
	require.NotNil(t, stack.Deployer.plans)
	require.NotNil(t, stack.Deployer.clients)
	assert.Equal(t, uint64(7), stack.Deployer.leaderEpoch(),
		"every apply is fenced by the term's epoch; without it a demoted leader could overwrite its successor")
	assert.Equal(t, "haptic-controller-0", stack.Deployer.identity())
}

// Without leader election there is one writer, so there is nothing to fence
// against: epoch zero and a name that says so.
func TestNewDeployStack_WithoutALeadershipFence(t *testing.T) {
	bus, logger := testutil.NewTestBusAndLogger()

	stack := NewDeployStack(bus, &coreconfig.Config{}, logger,
		metrics.NewMetrics(prometheus.NewRegistry()), nil, nil)

	assert.Equal(t, uint64(0), stack.Deployer.leaderEpoch())
	assert.Equal(t, standaloneIdentity, stack.Deployer.identity())
}

func TestNewDeployStack_AppliesConfiguredIntervals(t *testing.T) {
	bus, logger := testutil.NewTestBusAndLogger()

	cfg := &coreconfig.Config{}
	stack := NewDeployStack(bus, cfg, logger, metrics.NewMetrics(prometheus.NewRegistry()), nil, nil)

	// Taking the whole config rather than positional durations is deliberate: a
	// forgotten duration argument silently becomes 0, and a zero
	// deploymentTimeout would never retire a wedged deploy.
	assert.Equal(t, cfg.Dataplane.GetMinDeploymentInterval(), stack.Scheduler.minDeploymentInterval)
	assert.Equal(t, cfg.Dataplane.GetDeploymentTimeout(), stack.Scheduler.deploymentTimeout)
	assert.Positive(t, stack.Scheduler.deploymentTimeout,
		"an empty config must still yield the documented default, not zero")
}
