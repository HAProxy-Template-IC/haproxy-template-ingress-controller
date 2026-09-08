// Copyright 2026 Philipp Hossner
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

package deployplan

import (
	"fmt"
	"math/rand/v2"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"gitlab.com/haproxy-haptic/haptic/pkg/dataplane/renderplan"
)

const windowMap = "maps/route-backend.map"

func windowEntry(key, value string) renderplan.Entry {
	return renderplan.Entry{Key: key, Value: value}
}

// The windowed delta must equal the delta over the whole files for every
// edit, including duplicate keys inside and around the window: a del names
// every value of its key, so a key shared with the trimmed part forces the
// full compare.
func TestUnorderedMapOpsWindowMatchesTheFullCompare(t *testing.T) {
	rng := rand.New(rand.NewPCG(7, 11))
	keys := []string{"a.example", "b.example", "c.example", "d.example", "e.example", "f.example"}
	randomFile := func() []renderplan.Entry {
		n := rng.IntN(9)
		entries := make([]renderplan.Entry, 0, n)
		for range n {
			entries = append(entries, windowEntry(keys[rng.IntN(len(keys))], fmt.Sprintf("be-%d", rng.IntN(3))))
		}
		return entries
	}
	edit := func(base []renderplan.Entry) []renderplan.Entry {
		next := append([]renderplan.Entry(nil), base...)
		for range 1 + rng.IntN(3) {
			switch {
			case len(next) > 0 && rng.IntN(3) == 0:
				i := rng.IntN(len(next))
				next = append(next[:i], next[i+1:]...)
			case len(next) > 0 && rng.IntN(2) == 0:
				next[rng.IntN(len(next))].Value = fmt.Sprintf("be-%d", rng.IntN(3))
			default:
				i := rng.IntN(len(next) + 1)
				inserted := windowEntry(keys[rng.IntN(len(keys))], fmt.Sprintf("be-%d", rng.IntN(3)))
				next = append(next[:i], append([]renderplan.Entry{inserted}, next[i:]...)...)
			}
		}
		return next
	}
	for i := range 2000 {
		prev := randomFile()
		next := edit(prev)
		want := unorderedEntryOps(windowMap, prev, next)
		got := unorderedMapOps(windowMap, prev, next)
		require.Equal(t, want, got, "case %d: prev=%v next=%v", i, prev, next)
	}
}

func TestTrimCommonEntriesRefusesAKeySharedWithTheTrimmedPart(t *testing.T) {
	prev := []renderplan.Entry{windowEntry("k", "1"), windowEntry("k", "2"), windowEntry("z", "1")}
	next := []renderplan.Entry{windowEntry("k", "1"), windowEntry("z", "1")}
	_, _, ok := trimCommonEntries(prev, next)
	assert.False(t, ok, "k has a value in the common prefix, so a del would take it too")

	prev = []renderplan.Entry{windowEntry("a", "1"), windowEntry("b", "1"), windowEntry("z", "1")}
	next = []renderplan.Entry{windowEntry("a", "1"), windowEntry("b", "2"), windowEntry("z", "1")}
	prevWindow, nextWindow, ok := trimCommonEntries(prev, next)
	require.True(t, ok)
	assert.Equal(t, []renderplan.Entry{windowEntry("b", "1")}, prevWindow)
	assert.Equal(t, []renderplan.Entry{windowEntry("b", "2")}, nextWindow)
}
