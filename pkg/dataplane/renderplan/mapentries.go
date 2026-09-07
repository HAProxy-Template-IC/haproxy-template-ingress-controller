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

package renderplan

import "strings"

// ParseMapEntries splits rendered map-file content into entries: the first
// field is the key, the rest of the line the value. Blank and comment lines are
// dropped; duplicate keys are kept in order, because HAProxy map lookups are a
// multiset and a delta over them must see every occurrence.
//
// This reads HAPTIC's own output format, not HAProxy configuration.
func ParseMapEntries(content string) []Entry {
	if content == "" {
		return nil
	}
	lines := strings.Split(content, "\n")
	entries := make([]Entry, 0, len(lines))
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" || strings.HasPrefix(trimmed, "#") {
			continue
		}
		key, value := trimmed, ""
		if i := strings.IndexAny(trimmed, " \t"); i >= 0 {
			key = trimmed[:i]
			value = strings.TrimSpace(trimmed[i+1:])
		}
		entries = append(entries, Entry{Key: key, Value: value})
	}
	if len(entries) == 0 {
		return nil
	}
	return entries
}

// ParsedMapEntries is a map file's entries with the text they came from, so
// the next version of the text parses only the lines that changed.
type ParsedMapEntries struct {
	Content string
	Entries []Entry
	// starts[i] is the offset in Content of the line Entries[i] came from.
	starts []int
}

// ParseMapEntriesIndexed is ParseMapEntries keeping what Reparse needs.
func ParseMapEntriesIndexed(content string) ParsedMapEntries {
	entries, starts := parseMapLines(content, 0, nil, nil)
	return ParsedMapEntries{Content: content, Entries: entries, starts: starts}
}

// Reparse parses content, reusing the entries of every line that is unchanged
// at the head and at the tail of the previous text. An entry depends only on
// its own line, so only the lines between the two are parsed again.
func (p ParsedMapEntries) Reparse(content string) ParsedMapEntries {
	if content == p.Content {
		return p
	}
	if len(p.Entries) == 0 {
		return ParseMapEntriesIndexed(content)
	}
	previous := p.Content
	prefix := commonPrefixLen(previous, content)
	prefix = strings.LastIndexByte(previous[:prefix], '\n') + 1
	suffix := commonSuffixLen(previous[prefix:], content[prefix:])
	oldTail := len(previous) - suffix
	if newline := strings.IndexByte(previous[oldTail:], '\n'); newline < 0 {
		oldTail = len(previous)
	} else {
		oldTail += newline + 1
	}
	newTail := len(content) - (len(previous) - oldTail)

	head := 0
	for head < len(p.Entries) && p.starts[head] < prefix {
		head++
	}
	tail := len(p.Entries)
	for tail > head && p.starts[tail-1] >= oldTail {
		tail--
	}
	entries := make([]Entry, 0, len(p.Entries)+8)
	starts := make([]int, 0, len(p.Entries)+8)
	entries = append(entries, p.Entries[:head]...)
	starts = append(starts, p.starts[:head]...)
	entries, starts = parseMapLines(content[prefix:newTail], prefix, entries, starts)
	entries = append(entries, p.Entries[tail:]...)
	for _, start := range p.starts[tail:] {
		starts = append(starts, start-oldTail+newTail)
	}
	if len(entries) == 0 {
		entries, starts = nil, nil
	}
	return ParsedMapEntries{Content: content, Entries: entries, starts: starts}
}

func parseMapLines(
	content string,
	base int,
	entries []Entry,
	starts []int,
) (parsed []Entry, lineStarts []int) {
	offset := 0
	for offset <= len(content) {
		end := strings.IndexByte(content[offset:], '\n')
		line := content[offset:]
		if end >= 0 {
			line = content[offset : offset+end]
		}
		trimmed := strings.TrimSpace(line)
		if trimmed != "" && !strings.HasPrefix(trimmed, "#") {
			key, value := trimmed, ""
			if i := strings.IndexAny(trimmed, " \t"); i >= 0 {
				key = trimmed[:i]
				value = strings.TrimSpace(trimmed[i+1:])
			}
			entries = append(entries, Entry{Key: key, Value: value})
			starts = append(starts, base+offset)
		}
		if end < 0 {
			break
		}
		offset += end + 1
	}
	return entries, starts
}

func commonPrefixLen(left, right string) int {
	limit := min(len(left), len(right))
	index := 0
	for index < limit && left[index] == right[index] {
		index++
	}
	return index
}

func commonSuffixLen(left, right string) int {
	limit := min(len(left), len(right))
	index := 0
	for index < limit && left[len(left)-1-index] == right[len(right)-1-index] {
		index++
	}
	return index
}

// MapEntriesMatch reports whether ParseMapEntries(content) would equal
// entries, without building the entry list: it walks the lines and compares
// each parsed entry in place.
func MapEntriesMatch(content string, entries []Entry) bool {
	if content == "" {
		return entries == nil
	}
	next := 0
	offset := 0
	for offset <= len(content) {
		end := strings.IndexByte(content[offset:], '\n')
		line := content[offset:]
		if end >= 0 {
			line = content[offset : offset+end]
		}
		trimmed := strings.TrimSpace(line)
		if trimmed != "" && !strings.HasPrefix(trimmed, "#") {
			key, value := trimmed, ""
			if i := strings.IndexAny(trimmed, " \t"); i >= 0 {
				key = trimmed[:i]
				value = strings.TrimSpace(trimmed[i+1:])
			}
			if next >= len(entries) || entries[next].Key != key || entries[next].Value != value {
				return false
			}
			next++
		}
		if end < 0 {
			break
		}
		offset += end + 1
	}
	if next != len(entries) {
		return false
	}
	return next > 0 || entries == nil
}
