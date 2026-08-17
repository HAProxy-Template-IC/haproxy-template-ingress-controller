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
