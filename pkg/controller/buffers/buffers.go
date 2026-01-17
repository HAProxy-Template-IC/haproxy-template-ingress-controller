// Copyright 2025 Philipp Hossner
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

// Package buffers provides memory-adaptive buffer size calculation for event subscriptions.
//
// Buffer sizes are scaled based on GOMEMLIMIT (set by automemlimit from cgroup memory).
// Larger containers automatically get larger buffers, reducing event drops.
package buffers

import (
	"runtime/debug"
)

const (
	// BaseSize is the minimum buffer size when memory limit is unknown or very low.
	BaseSize = 100

	// MaxSize caps buffer sizes to prevent excessive memory usage.
	MaxSize = 10000

	// bytesPerSlot is the estimated memory per buffer slot for scaling calculation.
	// 1MB per slot provides reasonable scaling across container sizes.
	bytesPerSlot = 1024 * 1024
)

// calculateSize returns a buffer size scaled by available memory.
//
// The multiplier adjusts the base calculation - use higher values for
// components that can tolerate larger buffers (like observability).
//
// When GOMEMLIMIT is not set or very low, returns BaseSize * multiplier.
func calculateSize(multiplier float64) int {
	// Query current GOMEMLIMIT without changing it
	// Returns math.MaxInt64 if not set
	memLimit := debug.SetMemoryLimit(-1)

	// If no limit set (MaxInt64) or negative, use base size
	if memLimit < 0 || memLimit > 1<<50 { // > 1 PB means effectively unlimited
		return clamp(int(float64(BaseSize) * multiplier))
	}

	// Scale buffer size based on available memory
	scaled := int(float64(memLimit/bytesPerSlot) * multiplier)

	return clamp(scaled)
}

// clamp ensures the value is within [BaseSize, MaxSize] bounds.
func clamp(size int) int {
	if size < BaseSize {
		return BaseSize
	}
	if size > MaxSize {
		return MaxSize
	}
	return size
}

// Observability returns buffer size for lossy observability components.
//
// Uses a 2x multiplier since these components can tolerate larger buffers
// and occasional drops are acceptable.
func Observability() int {
	return calculateSize(2.0)
}

// Critical returns buffer size for business-critical components.
//
// Uses a 1x multiplier - these components need reliable delivery
// but don't need oversized buffers.
func Critical() int {
	return calculateSize(1.0)
}
