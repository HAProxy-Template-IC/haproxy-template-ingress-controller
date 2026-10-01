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

package main

import (
	"testing"

	"github.com/stretchr/testify/require"
	"golang.org/x/sys/unix"
)

func TestDisableTransparentHugePages(t *testing.T) {
	previous, err := unix.PrctlRetInt(unix.PR_GET_THP_DISABLE, 0, 0, 0, 0)
	require.NoError(t, err)
	t.Cleanup(func() {
		require.NoError(t, unix.Prctl(unix.PR_SET_THP_DISABLE, uintptr(previous&1), uintptr(previous>>1), 0, 0))
	})
	require.NoError(t, unix.Prctl(unix.PR_SET_THP_DISABLE, 0, 0, 0, 0))

	require.NoError(t, disableTransparentHugePages())

	disabled, err := unix.PrctlRetInt(unix.PR_GET_THP_DISABLE, 0, 0, 0, 0)
	require.NoError(t, err)
	require.Equal(t, 1, disabled)
}
