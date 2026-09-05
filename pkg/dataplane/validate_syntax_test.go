// Copyright 2025 Philipp Hossner
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0

//go:build playground

package dataplane

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"gitlab.com/haproxy-haptic/haptic/pkg/dataplane/validators"
)

func TestGetValidatorForVersion(t *testing.T) {
	tests := []struct {
		name         string
		version      *Version
		major, minor int
	}{
		{name: "nil", major: 3, minor: 0},
		{name: "zero", version: &Version{}, major: 3, minor: 0},
		{name: "legacy", version: &Version{Major: 2, Minor: 8}, major: 3, minor: 0},
		{name: "v3.0", version: &Version{Major: 3}, major: 3, minor: 0},
		{name: "v3.1", version: &Version{Major: 3, Minor: 1}, major: 3, minor: 1},
		{name: "v3.2", version: &Version{Major: 3, Minor: 2}, major: 3, minor: 2},
		{name: "v3.3", version: &Version{Major: 3, Minor: 3}, major: 3, minor: 3},
		{name: "newer minor", version: &Version{Major: 3, Minor: 99}, major: 3, minor: 3},
		{name: "newer major", version: &Version{Major: 5, Minor: 7}, major: 3, minor: 3},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Same(t, validators.ForVersion(tt.major, tt.minor), getValidatorForVersion(tt.version))
		})
	}
}

func TestGetValidatorForVersionReusesImmutableSets(t *testing.T) {
	first := getValidatorForVersion(&Version{Major: 3, Minor: 2})
	second := getValidatorForVersion(&Version{Major: 3, Minor: 2})
	other := getValidatorForVersion(&Version{Major: 3, Minor: 3})

	assert.Same(t, first, second)
	assert.NotSame(t, first, other)
}
