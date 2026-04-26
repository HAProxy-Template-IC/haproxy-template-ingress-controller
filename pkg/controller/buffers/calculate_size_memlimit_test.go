// Copyright 2025 Philipp Hossner
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0

package buffers

import (
	"runtime/debug"
	"testing"

	"github.com/stretchr/testify/assert"
)

// calculateSize has two branches:
//
//  1. GOMEMLIMIT unset / effectively unlimited (>1PB) → use
//     BaseSize * multiplier (then clamp). This is the common
//     no-memory-limit path that the existing Test_calculateSize_*
//     tests already cover when run without GOMEMLIMIT.
//
//  2. GOMEMLIMIT set to a real value → scale by
//     memLimit/bytesPerSlot * multiplier (then clamp). This is the
//     production path inside containers where Go's GC respects the
//     cgroup memory limit, and it is the branch that currently has
//     ZERO coverage.
//
// The scaling branch matters because it's how production buffer
// sizes are actually picked. A regression that swapped numerator
// and denominator (e.g. multiplier/memLimit instead of memLimit *
// multiplier), or that dropped the multiplier altogether, would
// silently downsize buffers in production while passing every
// "no GOMEMLIMIT" unit test. The scaled buffers would then drop
// events under load.
//
// Pin three properties of the scaling branch:
//
//   - Setting GOMEMLIMIT to a moderate value (10 GiB) takes the
//     scaling branch and yields MaxSize (because 10240 slots is way
//     more than MaxSize=10000, so clamping kicks in). This proves
//     the scaling math runs without panicking and the result still
//     respects the upper bound.
//   - Setting GOMEMLIMIT to a tiny value (1 byte) yields BaseSize
//     (the lower clamp bound). This proves clamp() still applies
//     after the scaling formula and the function never returns
//     below-base sizes even with absurd inputs.
//   - The multiplier is honoured: at the same GOMEMLIMIT, a higher
//     multiplier yields >= the lower one. This catches a regression
//     that dropped or inverted the multiplier inside the scaling
//     formula (the existing Test_calculateSize_MultiplierScales is
//     the same shape but only exercises the unlimited branch).
func TestCalculateSize_HonoursGOMEMLIMITScalingPath(t *testing.T) {
	// Snapshot and restore — debug.SetMemoryLimit affects the entire
	// process, so any test that mutates it must put it back even on
	// failure. SetMemoryLimit(-1) is the documented "leave alone"
	// query form; saving the returned value gives us the original.
	original := debug.SetMemoryLimit(-1)
	t.Cleanup(func() {
		debug.SetMemoryLimit(original)
	})

	t.Run("scaling branch reached and upper-clamped at MaxSize for a 10GiB limit", func(t *testing.T) {
		debug.SetMemoryLimit(10 * 1024 * 1024 * 1024) // 10 GiB
		// scaled = (10GiB / 1MiB) * 1.0 = 10240, which is > MaxSize=10000.
		// Clamp pulls it back to MaxSize.
		got := calculateSize(1.0)
		assert.Equal(t, MaxSize, got,
			"with GOMEMLIMIT=10GiB the scaling formula yields 10240 slots, "+
				"which the upper clamp must pull back to MaxSize. A "+
				"regression that bypassed clamp on the scaled path would "+
				"return >MaxSize here and could surface as runaway buffer "+
				"allocation in production")
	})

	t.Run("scaling branch lower-clamped at BaseSize for a tiny limit", func(t *testing.T) {
		debug.SetMemoryLimit(1) // 1 byte
		// scaled = (1 / 1MiB) * 1.0 → integer division → 0. Clamp
		// pulls 0 up to BaseSize.
		got := calculateSize(1.0)
		assert.Equal(t, BaseSize, got,
			"with a tiny GOMEMLIMIT the scaling formula yields 0 slots — "+
				"the lower clamp must pull this up to BaseSize. A "+
				"regression that skipped the clamp here would give 0-size "+
				"buffers in low-memory environments and immediately "+
				"deadlock event publishing")
	})

	t.Run("multiplier scales the result inside the GOMEMLIMIT branch", func(t *testing.T) {
		// 5 GiB so neither bound clamps:
		// scaled1 = 5120 * 1.0 = 5120 (in [BaseSize, MaxSize])
		// scaled2 = 5120 * 2.0 = 10240 → clamps to MaxSize=10000
		// 10000 > 5120, so the multiplier is observably honoured.
		debug.SetMemoryLimit(5 * 1024 * 1024 * 1024)
		size1 := calculateSize(1.0)
		size2 := calculateSize(2.0)
		assert.Greater(t, size2, size1,
			"a higher multiplier MUST yield a strictly larger result "+
				"inside the scaling branch — a regression that dropped "+
				"or inverted the multiplier on the scaled path would "+
				"silently downsize observability buffers (which use 2x) "+
				"down to critical-buffer size, dropping events under load")
	})
}
