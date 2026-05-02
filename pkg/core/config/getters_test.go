// Copyright 2025 Philipp Hossner
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0

package config

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

// TemplatingSettings.GetRenderTimeout is the only "Get<Duration>" accessor in
// the codebase that does NOT funnel through the shared parseDurationOr helper —
// it inlines its own empty/parse-error/valid fallback. That makes its
// behaviour independently regression-prone: a mistake in the inline
// implementation (e.g. dropping the err == nil check, returning the zero
// value on parse error, or skipping the fallback path entirely) would make
// every render time out instantly. Pin all three branches.
//
// LeaderElectionConfig.GetLeaseDuration / GetRenewDeadline / GetRetryPeriod
// share the parseDurationOr code path with the DataplaneConfig getters
// already exercised in defaults_duration_test.go, but each one targets a
// distinct field and a distinct default constant. A regression that wired the
// wrong default into one (e.g. RetryPeriod returning the lease duration on
// fallback) would break leader election in subtle ways — election would
// start, but the retry cadence would be wrong. Pin the field-to-default
// mapping with a table that names every binding explicitly.

func TestTemplatingSettings_GetRenderTimeout(t *testing.T) {
	tests := []struct {
		name string
		raw  string
		want time.Duration
	}{
		{
			name: "empty string falls back to the documented default",
			raw:  "",
			want: DefaultRenderTimeout,
		},
		{
			name: "valid duration is parsed and returned verbatim",
			// Pick a value distinct from DefaultRenderTimeout so a regression
			// that always returned the default would fail this case.
			raw:  "12s",
			want: 12 * time.Second,
		},
		{
			name: "valid sub-second duration is honored",
			raw:  "750ms",
			want: 750 * time.Millisecond,
		},
		{
			name: "invalid duration falls back silently to the default",
			// This is the load-bearing branch: a regression that returned
			// the zero duration on parse error would make every render time
			// out immediately. The silent fallback is documented behaviour.
			raw:  "not-a-duration",
			want: DefaultRenderTimeout,
		},
		{
			name: "missing unit is rejected and falls back",
			raw:  "30",
			want: DefaultRenderTimeout,
		},
		{
			name: "trailing garbage is rejected and falls back",
			raw:  "30sX",
			want: DefaultRenderTimeout,
		},
		{
			name: "explicit zero duration is honored, NOT treated as missing",
			// "0s" is a valid time.Duration string that parses to 0. The
			// fallback only fires for empty string OR parse error; an
			// explicit "0s" must be preserved. A regression that treated
			// any zero result as "use default" would silently override an
			// operator's deliberate "no timeout" intent.
			raw:  "0s",
			want: 0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ts := &TemplatingSettings{RenderTimeout: tt.raw}
			assert.Equal(t, tt.want, ts.GetRenderTimeout(),
				"render timeout fallback contract: empty/invalid → default, "+
					"valid → parsed, explicit 0s → 0")
		})
	}
}

// TestLeaderElectionConfig_DurationGetters_Defaults pins that each of the
// three leader-election duration getters falls back to its OWN named
// default constant. The structure is identical for all three (delegating
// to parseDurationOr) but the field-to-default wiring is per-method, so a
// regression like `return parseDurationOr(le.RetryPeriod, DefaultLeaderElectionLeaseDuration)`
// would compile cleanly and only show up at runtime as broken election timing.
func TestLeaderElectionConfig_DurationGetters_Defaults(t *testing.T) {
	cfg := &LeaderElectionConfig{}

	tests := []struct {
		name string
		got  func() time.Duration
		want time.Duration
	}{
		{
			name: "GetLeaseDuration → DefaultLeaderElectionLeaseDuration on empty",
			got:  cfg.GetLeaseDuration,
			want: DefaultLeaderElectionLeaseDuration,
		},
		{
			name: "GetRenewDeadline → DefaultLeaderElectionRenewDeadline on empty",
			got:  cfg.GetRenewDeadline,
			want: DefaultLeaderElectionRenewDeadline,
		},
		{
			name: "GetRetryPeriod → DefaultLeaderElectionRetryPeriod on empty",
			got:  cfg.GetRetryPeriod,
			want: DefaultLeaderElectionRetryPeriod,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, tt.got(),
				"each leader-election getter must wire to its OWN default; "+
					"a copy-paste regression that returned a sibling default "+
					"would compile cleanly and only manifest as broken election timing")
		})
	}
}

// TestLeaderElectionConfig_DurationGetters_Overrides pins that valid
// duration strings are honored on each getter, and that invalid strings
// fall back through parseDurationOr to the per-method default rather than
// crashing or returning zero.
func TestLeaderElectionConfig_DurationGetters_Overrides(t *testing.T) {
	t.Run("valid durations are parsed verbatim", func(t *testing.T) {
		// Use distinct values per field so a regression that swapped the
		// field reads inside the getters would fail at least one assertion.
		cfg := &LeaderElectionConfig{
			LeaseDuration: "21s",
			RenewDeadline: "22s",
			RetryPeriod:   "1500ms",
		}

		assert.Equal(t, 21*time.Second, cfg.GetLeaseDuration())
		assert.Equal(t, 22*time.Second, cfg.GetRenewDeadline())
		assert.Equal(t, 1500*time.Millisecond, cfg.GetRetryPeriod())
	})

	t.Run("invalid durations fall back to the per-method default", func(t *testing.T) {
		cfg := &LeaderElectionConfig{
			LeaseDuration: "garbage",
			RenewDeadline: "garbage",
			RetryPeriod:   "garbage",
		}

		assert.Equal(t, DefaultLeaderElectionLeaseDuration, cfg.GetLeaseDuration(),
			"invalid LeaseDuration → DefaultLeaderElectionLeaseDuration")
		assert.Equal(t, DefaultLeaderElectionRenewDeadline, cfg.GetRenewDeadline(),
			"invalid RenewDeadline → DefaultLeaderElectionRenewDeadline")
		assert.Equal(t, DefaultLeaderElectionRetryPeriod, cfg.GetRetryPeriod(),
			"invalid RetryPeriod → DefaultLeaderElectionRetryPeriod")
	})
}

// TestWatchedResource_GetDebounceInterval pins the per-resource debounce
// override semantics: GetDebounceInterval returns 0 for the empty / invalid
// cases (so the watcher's WatcherConfig.SetDefaults takes over with the
// pkg/k8s/types.DefaultDebounceInterval = 5s value), and returns the parsed
// duration verbatim for valid Go duration strings. The contract is
// load-bearing for the resourcewatcher hand-off (resourcewatcher/watcher.go
// passes this value straight into WatcherConfig.DebounceInterval).
//
// This getter intentionally falls back to ZERO (not to a default duration)
// because the watcher layer owns the default. The other Get* getters in
// this file all fall back to a named default constant — this one is the
// outlier and the comment exists to make that asymmetry explicit.
func TestWatchedResource_GetDebounceInterval(t *testing.T) {
	tests := []struct {
		name string
		raw  string
		want time.Duration
	}{
		{
			name: "empty returns zero so watcher uses its own default",
			raw:  "",
			want: 0,
		},
		{
			name: "valid sub-second duration is parsed",
			raw:  "500ms",
			want: 500 * time.Millisecond,
		},
		{
			name: "valid second duration is parsed",
			raw:  "10s",
			want: 10 * time.Second,
		},
		{
			name: "valid compound duration is parsed",
			raw:  "1m30s",
			want: 90 * time.Second,
		},
		{
			name: "invalid string returns zero (silent fallback to watcher default)",
			raw:  "not-a-duration",
			want: 0,
		},
		{
			name: "missing unit returns zero (time.ParseDuration rejects bare numbers)",
			raw:  "30",
			want: 0,
		},
		{
			name: "explicit zero is preserved (caller asked for the watcher's default)",
			raw:  "0s",
			want: 0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := &WatchedResource{DebounceInterval: tt.raw}
			assert.Equal(t, tt.want, r.GetDebounceInterval())
		})
	}
}
