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

package renderer

import (
	"fmt"
	"math"
	"testing"
)

// The observation key is a cache key, so the one-buffer builder must produce
// exactly what the previous path did: three Sprintf'd fixed-width decimals fed
// through incrementalOrderedTuple with the remaining parts. A single byte of
// disagreement makes every key miss silently.
func TestObservationKeyMatchesTupleOfFormattedParts(t *testing.T) {
	cases := []struct {
		kind                              exactCycleIncrementalKind
		scope                             string
		ordinal, occurrence               uint64
		group, component, cell, delimiter string
	}{
		{0, "", 0, 0, "", "", "", ""},
		{7, "scope", 1, 2, "g", "c", "cell", "|"},
		{255, "with\x00nul", 1 << 40, math.MaxUint64, "grp\x00", "comp", "c\x00ell", "\x00"},
		{12, "unicode-ä", 999999, 1234567890, "group", "component", "cell", "--"},
	}
	for _, tc := range cases {
		want := string(incrementalOrderedTuple(
			fmt.Sprintf("%020d", tc.occurrence),
			tc.scope,
			fmt.Sprintf("%020d", tc.ordinal),
			fmt.Sprintf("%03d", tc.kind),
			tc.group, tc.component, tc.cell, tc.delimiter,
		))
		got := exactCycleIncrementalObservationKey(
			tc.kind, tc.scope, tc.ordinal, tc.occurrence,
			tc.group, tc.component, tc.cell, tc.delimiter,
		)
		if got != want {
			t.Fatalf("key mismatch for %+v:\n got %q\nwant %q", tc, got, want)
		}
	}
}
