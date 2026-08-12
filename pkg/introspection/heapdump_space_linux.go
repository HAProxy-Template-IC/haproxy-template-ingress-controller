// Copyright 2025 Philipp Hossner
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0

//go:build linux

package introspection

import (
	"math"

	"golang.org/x/sys/unix"
)

// availableBytes reports the free space a non-root process may use in dir.
// The second return is false when the filesystem cannot be queried or reports
// values that do not fit, in which case the caller skips its check rather than
// guessing.
func availableBytes(dir string) (uint64, bool) {
	var st unix.Statfs_t
	if err := unix.Statfs(dir, &st); err != nil {
		return 0, false
	}
	if st.Bsize <= 0 {
		return 0, false
	}
	blockSize := uint64(st.Bsize)
	if st.Bavail > math.MaxUint64/blockSize {
		return 0, false
	}
	return st.Bavail * blockSize, true
}
