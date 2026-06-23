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

package watcher

import (
	"errors"
	"testing"

	"github.com/stretchr/testify/require"
)

// TestSingleWatcher_LastWatchError verifies that a watch-connection error is
// recorded as a non-zero timestamp (kubernetes-resource-watching spec:
// "Watch error logged and timestamp recorded").
func TestSingleWatcher_LastWatchError(t *testing.T) {
	w := &SingleWatcher{}

	require.True(t, w.LastWatchError().IsZero(), "no watch error yet → zero time")

	w.handleWatchError(nil, errors.New("watch connection dropped"))

	require.False(t, w.LastWatchError().IsZero(), "after a watch error → non-zero timestamp")
}
