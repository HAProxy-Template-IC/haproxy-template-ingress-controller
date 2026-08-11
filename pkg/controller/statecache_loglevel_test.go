// Copyright 2025 Philipp Hossner
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0

package controller

import (
	"log/slog"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"gitlab.com/haproxy-haptic/haptic/pkg/controller/events"
	coreconfig "gitlab.com/haproxy-haptic/haptic/pkg/core/config"
	"gitlab.com/haproxy-haptic/haptic/pkg/core/logging"
	busevents "gitlab.com/haproxy-haptic/haptic/pkg/events"
)

// StateCache.handleConfigValidated has a side-effect that the existing
// happy-path test doesn't observe: when cfg.Logging.Level is set, the
// global log level is updated dynamically. This is the runtime control
// path for operators who want to flip log verbosity without restarting
// the controller. Two contracts are load-bearing and were uncovered:
//
//  1. Empty Logging.Level → global log level is NOT touched. The
//     comment in statecache.go explicitly says "Empty Level means
//     use LOG_LEVEL env var (don't change)". A regression that
//     called SetLevel("") unconditionally would silently reset the
//     log level on every config update — operators who set a
//     non-default level via env var would lose it the moment the
//     CRD reconciles.
//
//  2. Non-empty Logging.Level → global log level updates to that
//     value. Without this branch the runtime-tunable verbosity
//     control (one of the few operator-facing knobs that doesn't
//     require a restart) silently stops working.

func TestStateCache_HandleConfigValidated_EmptyLoggingLevelPreservesCurrentLevel(t *testing.T) {
	// Save and restore the global log level so this test doesn't
	// pollute other tests that share the package-level globalLevel.
	originalLevel := logging.GetLevel()
	t.Cleanup(func() { logging.SetLevel(originalLevel) })

	// Set a known starting level distinct from defaults so the
	// "preserved" assertion is meaningful.
	const baselineLevel = logging.LevelNameDebug
	logging.SetLevel(baselineLevel)
	require.Equal(t, baselineLevel, logging.GetLevel(),
		"baseline: SetLevel must take effect for this test to be meaningful")

	bus := busevents.NewEventBus(100)
	cache := NewStateCache(bus, nil, slog.Default())
	go cache.Start(t.Context())
	bus.Start()

	// Publish a config with EMPTY Logging.Level — the runtime level
	// must NOT be touched.
	cfg := &coreconfig.Config{
		// Logging zero-value: Level == ""
	}
	bus.Publish(events.NewConfigValidatedEvent(cfg, nil, "v-empty", ""))

	require.Eventually(t, func() bool {
		stored, _, err := cache.GetConfig()
		return err == nil && stored != nil
	}, 2*time.Second, 10*time.Millisecond,
		"sanity: the cache must observe the published config so the rest "+
			"of the test runs against actual state")

	assert.Equal(t, baselineLevel, logging.GetLevel(),
		"empty Logging.Level MUST leave the global log level untouched — "+
			"the explicit guard in statecache.go preserves the env-var-set "+
			"verbosity across config reloads; a regression that called "+
			"SetLevel(\"\") unconditionally would silently reset the level "+
			"on every reconcile")
}

func TestStateCache_HandleConfigValidated_NonEmptyLoggingLevelUpdatesLevel(t *testing.T) {
	originalLevel := logging.GetLevel()
	t.Cleanup(func() { logging.SetLevel(originalLevel) })

	// Start at a known level that differs from the level we'll set
	// via the config — otherwise the assertion can't tell whether
	// the update fired.
	logging.SetLevel(logging.LevelNameWarn)
	require.Equal(t, logging.LevelNameWarn, logging.GetLevel(),
		"baseline: starting level must be the one we expect to be replaced")

	bus := busevents.NewEventBus(100)
	cache := NewStateCache(bus, nil, slog.Default())
	go cache.Start(t.Context())
	bus.Start()

	const targetLevel = logging.LevelNameDebug
	cfg := &coreconfig.Config{
		Logging: coreconfig.LoggingConfig{Level: targetLevel},
	}
	bus.Publish(events.NewConfigValidatedEvent(cfg, nil, "v-debug", ""))

	// Wait for the level to actually change rather than racing via
	// time.Sleep — the event handler runs in a goroutine.
	require.Eventually(t, func() bool {
		return logging.GetLevel() == targetLevel
	}, 2*time.Second, 10*time.Millisecond,
		"non-empty Logging.Level MUST update the global log level — "+
			"this is the runtime-tunable verbosity knob (one of the few "+
			"operator-facing controls that doesn't require a restart); "+
			"a regression here silently breaks live debugging without "+
			"any visible error")
}

func TestStateCache_ActiveSnapshotRestoreRevertsCandidateState(t *testing.T) {
	originalLevel := logging.GetLevel()
	t.Cleanup(func() { logging.SetLevel(originalLevel) })
	logging.SetLevel(logging.LevelNameWarn)

	cache := NewStateCache(busevents.NewEventBus(100), nil, slog.Default())
	activeA := &coreconfig.Config{}
	cache.handleConfigValidated(events.NewConfigValidatedEvent(activeA, nil, "active-a", ""))

	candidateB := &coreconfig.Config{
		Logging: coreconfig.LoggingConfig{Level: logging.LevelNameDebug},
	}
	candidateEvent := events.NewConfigValidatedEvent(candidateB, nil, "candidate-b", "")
	candidateEvent.CandidateGeneration = 1
	cache.handleConfigValidated(candidateEvent)
	require.Equal(t, logging.LevelNameDebug, logging.GetLevel())

	restore := events.NewConfigValidatedEvent(activeA, nil, "active-a", "")
	restore.ActiveSnapshotRestore = true
	cache.handleConfigValidated(restore)

	stored, version, err := cache.GetConfig()
	require.NoError(t, err)
	assert.Same(t, activeA, stored)
	assert.Equal(t, "active-a", version)
	assert.Equal(t, logging.LevelNameWarn, logging.GetLevel())
}
