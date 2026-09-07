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

import "encoding/json"

// canonicalMapMemberFragment is canonicalMemberFragment for a map file. A map
// with thousands of entries changes a few per render, so a replaced entry
// splices the bytes of the entry it replaced around the entries that differ,
// instead of encoding every entry again. Every byte still comes from
// encoding/json: the entries are encoded one by one and the frame around them
// from the same struct without entries, which encoding/json composes exactly
// this way.
func canonicalMapMemberFragment(entry *snapshotEntry[Map]) ([]byte, error) {
	entry.canonical.once.Do(func() {
		predecessor := entry.predecessor
		entry.predecessor = nil
		if predecessor != nil {
			if spliced, ok, err := spliceCanonicalMapMember(entry, predecessor); err != nil {
				entry.canonical.err = err
				return
			} else if ok {
				entry.canonical.bytes, entry.canonical.spans = spliced.bytes, spliced.spans
				return
			}
		}
		fragment, err := encodeCanonicalMapMember(entry.key.name, entry.value.value)
		if err != nil {
			entry.canonical.err = err
			return
		}
		entry.canonical.bytes, entry.canonical.spans = fragment.bytes, fragment.spans
	})
	return entry.canonical.bytes, entry.canonical.err
}

// canonicalMapFragment is one map member's bytes with the offset of every
// entry; spans[len(entries)] is the offset of the closing bracket.
type canonicalMapFragment struct {
	bytes []byte
	spans []int
}

func encodeCanonicalMapMember(name string, value Map) (canonicalMapFragment, error) {
	if len(value.Entries) == 0 {
		bytes, err := canonicalMember(name, value)
		return canonicalMapFragment{bytes: bytes}, err
	}
	frame, err := canonicalMember(name, Map{Path: value.Path, Ordered: value.Ordered})
	if err != nil {
		return canonicalMapFragment{}, err
	}
	fragment := canonicalMapFragment{
		bytes: make([]byte, 0, len(frame)+len(value.Entries)*48),
		spans: make([]int, 0, len(value.Entries)+1),
	}
	fragment.bytes = append(fragment.bytes, frame[:len(frame)-1]...)
	fragment.bytes = append(fragment.bytes, `,"entries":[`...)
	for index := range value.Entries {
		if index > 0 {
			fragment.bytes = append(fragment.bytes, ',')
		}
		fragment.spans = append(fragment.spans, len(fragment.bytes))
		encoded, err := json.Marshal(value.Entries[index])
		if err != nil {
			return canonicalMapFragment{}, err
		}
		fragment.bytes = append(fragment.bytes, encoded...)
	}
	fragment.spans = append(fragment.spans, len(fragment.bytes))
	fragment.bytes = append(fragment.bytes, ']', '}')
	return fragment, nil
}

func spliceCanonicalMapMember(
	entry, predecessor *snapshotEntry[Map],
) (canonicalMapFragment, bool, error) {
	previous, err := canonicalMapMemberFragment(predecessor)
	if err != nil {
		return canonicalMapFragment{}, false, err
	}
	before, after := predecessor.value.value, entry.value.value
	spans := predecessor.canonical.spans
	if entry.key.name != predecessor.key.name || before.Path != after.Path ||
		before.Ordered != after.Ordered || len(before.Entries) == 0 || len(after.Entries) == 0 ||
		len(spans) != len(before.Entries)+1 {
		return canonicalMapFragment{}, false, nil
	}
	head, tail := commonEntryEdges(before.Entries, after.Entries)
	middle := after.Entries[head : len(after.Entries)-tail]
	tailStart := spans[len(before.Entries)-tail]
	fragment := canonicalMapFragment{
		bytes: make([]byte, 0, len(previous)+len(middle)*48),
		spans: make([]int, 0, len(after.Entries)+1),
	}
	// The kept head ends with the bracket that opens the list, with the comma
	// that followed its last entry, or with the last entry itself when every
	// previous entry is kept; the kept tail starts with an entry or with the
	// bracket that closes the list.
	trailingComma := head > 0 && head < len(before.Entries)
	fragment.bytes = append(fragment.bytes, previous[:spans[head]]...)
	fragment.spans = append(fragment.spans, spans[:head]...)
	if trailingComma && len(middle) == 0 && tail == 0 {
		fragment.bytes = fragment.bytes[:len(fragment.bytes)-1]
	}
	for index := range middle {
		if index > 0 || (head > 0 && !trailingComma) {
			fragment.bytes = append(fragment.bytes, ',')
		}
		fragment.spans = append(fragment.spans, len(fragment.bytes))
		encoded, err := json.Marshal(middle[index])
		if err != nil {
			return canonicalMapFragment{}, false, err
		}
		fragment.bytes = append(fragment.bytes, encoded...)
	}
	if tail > 0 && len(middle) > 0 {
		fragment.bytes = append(fragment.bytes, ',')
	}
	shift := len(fragment.bytes) - tailStart
	for _, span := range spans[len(before.Entries)-tail:] {
		fragment.spans = append(fragment.spans, span+shift)
	}
	fragment.bytes = append(fragment.bytes, previous[tailStart:]...)
	return fragment, true, nil
}

// commonEntryEdges counts the entries equal at the head and at the tail of
// both lists; the two never overlap.
func commonEntryEdges(before, after []Entry) (head, tail int) {
	limit := min(len(before), len(after))
	for head < limit && before[head] == after[head] {
		head++
	}
	for tail < limit-head && before[len(before)-1-tail] == after[len(after)-1-tail] {
		tail++
	}
	return head, tail
}
