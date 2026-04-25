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

package events

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func TestNewTimestamped_UsesCurrentTime(t *testing.T) {
	before := time.Now()
	ts := newTimestamped()
	after := time.Now()

	got := ts.Timestamp()
	assert.False(t, got.Before(before), "Timestamp must not be earlier than before-call")
	assert.False(t, got.After(after), "Timestamp must not be later than after-call")
}

func TestTimestamped_Timestamp_ReturnsStoredValue(t *testing.T) {
	want := time.Date(2025, 1, 2, 3, 4, 5, 0, time.UTC)
	ts := timestamped{ts: want}

	assert.Equal(t, want, ts.Timestamp())
}

func TestTimestamped_Timestamp_StableAcrossCalls(t *testing.T) {
	// Two calls must return the exact same instant — nothing in the type
	// should be re-stamping or freshening on each access.
	ts := newTimestamped()
	first := ts.Timestamp()
	time.Sleep(time.Millisecond)
	second := ts.Timestamp()

	assert.Equal(t, first, second)
}
