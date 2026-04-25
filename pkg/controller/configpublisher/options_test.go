// Copyright 2025 Philipp Hossner
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0

package configpublisher

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

// WithPublishInterval is the only Option exposed by the configpublisher
// component today. It throttles CRD publish frequency to relieve etcd
// write pressure during endpoint churn. Pin both ends of the
// documented contract:
//   - a non-zero duration enables throttling
//   - a zero duration disables throttling (every config publishes
//     immediately)
//
// The constructor uses functional options, so the helper just stores
// the value into the component's publishInterval field. Apply the
// option directly to a zero-value Component to keep the test focused
// on the option's contract rather than the constructor's wiring.
func TestWithPublishInterval(t *testing.T) {
	tests := []struct {
		name string
		d    time.Duration
		want time.Duration
	}{
		{name: "5s enables throttling", d: 5 * time.Second, want: 5 * time.Second},
		{name: "1ms enables throttling at sub-second", d: time.Millisecond, want: time.Millisecond},
		{name: "0 disables throttling", d: 0, want: 0},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := &Component{}
			WithPublishInterval(tt.d)(c)
			assert.Equal(t, tt.want, c.publishInterval)
		})
	}
}

// Apply WithPublishInterval multiple times — last write wins (the
// option is a setter, not an accumulator). Pin so a future refactor
// can't accidentally make it accumulate or reject second calls.
func TestWithPublishInterval_LastWriteWins(t *testing.T) {
	c := &Component{}
	WithPublishInterval(5 * time.Second)(c)
	WithPublishInterval(2 * time.Second)(c)
	WithPublishInterval(10 * time.Second)(c)

	assert.Equal(t, 10*time.Second, c.publishInterval,
		"last call wins; option is a setter, not an accumulator")
}

// Component.Name is the lifecycle.Component identifier — pin the
// constant rather than re-deriving it.
func TestComponent_Name_ConstantValue(t *testing.T) {
	c := &Component{}
	assert.Equal(t, ComponentName, c.Name(),
		"Component.Name must always return the package-level ComponentName constant")
}
