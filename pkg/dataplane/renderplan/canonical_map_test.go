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

package renderplan

import (
	"fmt"
	"math/rand/v2"
	"testing"

	"github.com/stretchr/testify/require"
)

// A spliced map member must be the bytes encoding/json produces for the whole
// member, across chains of edits at the head, the middle and the tail, with
// the characters encoding/json escapes.
func TestSplicedCanonicalMapMemberEqualsFullEncoding(t *testing.T) {
	random := rand.New(rand.NewPCG(3, 5))
	values := []string{"be1", "<b>&amp;</b>", "ünïcödé", "tab\tsep", " line", "quote\"q", ""}
	randomEntry := func(i int) Entry {
		return Entry{Key: fmt.Sprintf("host-%d.example.com", i), Value: values[random.IntN(len(values))]}
	}
	entries := make([]Entry, 0, 64)
	for i := range 30 {
		entries = append(entries, randomEntry(i))
	}
	authority := NewAuthority()
	key := snapshotKey{index: -1, name: "maps/host.map"}
	current := sealSnapshotEntry(authority, mapSnapshotCollection, key,
		ownMap(Map{Path: "host.map", Ordered: true, Entries: entries}))

	for step := range 300 {
		switch random.IntN(6) {
		case 0:
			if len(entries) > 0 {
				entries[random.IntN(len(entries))] = randomEntry(1000 + step)
			}
		case 1:
			at := random.IntN(len(entries) + 1)
			entries = append(entries[:at], append([]Entry{randomEntry(2000 + step)}, entries[at:]...)...)
		case 2:
			if len(entries) > 0 {
				at := random.IntN(len(entries))
				entries = append(entries[:at], entries[at+1:]...)
			}
		case 3:
			entries = entries[:len(entries)/2]
		case 4:
			for range random.IntN(20) {
				entries = append(entries, randomEntry(3000+step))
			}
		default:
			if len(entries) > 1 {
				entries = entries[1:]
			}
		}
		value := Map{Path: "host.map", Ordered: true, Entries: append([]Entry(nil), entries...)}
		next := sealSnapshotEntry(authority, mapSnapshotCollection, key, ownMap(value))
		next.predecessor = current
		spliced, err := canonicalMapMemberFragment(next)
		require.NoError(t, err)
		full, err := canonicalMember(key.name, value)
		require.NoError(t, err)
		require.Equal(t, string(full), string(spliced), "step %d", step)
		require.Nil(t, next.predecessor, "the predecessor is dropped once used")
		if len(value.Entries) > 0 {
			require.Len(t, next.canonical.spans, len(value.Entries)+1, "step %d", step)
			require.Equal(t, byte(']'), spliced[next.canonical.spans[len(value.Entries)]])
		}
		current = next
	}
}

func TestSplicedCanonicalMapMemberFallsBackWhenTheFrameChanges(t *testing.T) {
	authority := NewAuthority()
	key := snapshotKey{index: -1, name: "maps/host.map"}
	before := sealSnapshotEntry(authority, mapSnapshotCollection, key,
		Map{Path: "host.map", Ordered: true, Entries: []Entry{{Key: "a", Value: "1"}}})
	after := sealSnapshotEntry(authority, mapSnapshotCollection, key,
		Map{Path: "host.map", Ordered: false, Entries: []Entry{{Key: "a", Value: "1"}, {Key: "b", Value: "2"}}})
	after.predecessor = before
	spliced, err := canonicalMapMemberFragment(after)
	require.NoError(t, err)
	full, err := canonicalMember(key.name, after.value.value)
	require.NoError(t, err)
	require.Equal(t, string(full), string(spliced))
}
