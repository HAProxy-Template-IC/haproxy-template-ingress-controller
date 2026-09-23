// Copyright 2026 Philipp Hossner
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
// http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package templating

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
)

func encodeStatusListOwnership(variants map[string]map[string]any, options []map[string]any) (string, error) {
	if len(options) == 0 {
		return "", nil
	}
	if len(options) != 1 {
		return "", errors.New("statusPatch: provide at most one list ownership map")
	}
	encoded, err := json.Marshal(options[0])
	if err != nil {
		return "", fmt.Errorf("statusPatch: list ownership: %w", err)
	}
	return statusListOwnershipArgument([]string{string(encoded)}, variants)
}

func statusListOwnershipArgument(options []string, variants map[string]map[string]any) (string, error) {
	if len(options) == 0 || (len(options) == 1 && options[0] == "") {
		return "", nil
	}
	if len(options) != 1 {
		return "", errors.New("statusPatch: provide at most one list ownership map")
	}
	rules, err := parseStatusListOwnership(options[0])
	if err != nil {
		return "", err
	}
	for path, selector := range rules {
		found := false
		for phase, payload := range variants {
			entries, present, err := statusListAt(payload, path)
			if err != nil {
				return "", fmt.Errorf("statusPatch: %s: %w", phase, err)
			}
			found = found || present
			if err := validateStatusListEntries(entries, path, selector); err != nil {
				return "", fmt.Errorf("statusPatch: %s: %w", phase, err)
			}
		}
		if !found {
			return "", fmt.Errorf("statusPatch: owned list %s is absent from every variant", path)
		}
	}
	if len(rules) == 0 {
		return "", nil
	}
	encoded, err := json.Marshal(rules)
	return string(encoded), err
}

func parseStatusListOwnership(encoded string) (map[string]map[string]any, error) {
	var rules map[string]map[string]any
	if err := json.Unmarshal([]byte(encoded), &rules); err != nil {
		return nil, fmt.Errorf("statusPatch: list ownership must map JSON pointers to object selectors: %w", err)
	}
	for path, selector := range rules {
		if _, err := statusListPath(path); err != nil {
			return nil, err
		}
		if len(selector) == 0 {
			return nil, fmt.Errorf("statusPatch: list %s needs a nonempty ownership selector", path)
		}
		for other := range rules {
			if strings.HasPrefix(other, path+"/") {
				return nil, fmt.Errorf("statusPatch: owned lists %s and %s overlap", path, other)
			}
		}
	}
	return rules, nil
}

func statusListPath(path string) ([]string, error) {
	if !strings.HasPrefix(path, "/") {
		return nil, fmt.Errorf("statusPatch: list path %q must be a JSON pointer relative to status", path)
	}
	parts := strings.Split(path[1:], "/")
	for index, part := range parts {
		for position := 0; position < len(part); position++ {
			if part[position] == '~' {
				position++
				if position == len(part) || (part[position] != '0' && part[position] != '1') {
					return nil, fmt.Errorf("statusPatch: invalid escape in list path %q", path)
				}
			}
		}
		parts[index] = strings.ReplaceAll(strings.ReplaceAll(part, "~1", "/"), "~0", "~")
	}
	return parts, nil
}

func statusListParent(status map[string]any, path string) (parent map[string]any, field string, err error) {
	parts, err := statusListPath(path)
	if err != nil {
		return nil, "", err
	}
	for _, part := range parts[:len(parts)-1] {
		value, present := status[part]
		if !present {
			return nil, parts[len(parts)-1], nil
		}
		status, present = value.(map[string]any)
		if !present {
			return nil, "", fmt.Errorf("statusPatch: list path %s crosses a non-object field", path)
		}
	}
	return status, parts[len(parts)-1], nil
}

func statusListAt(status map[string]any, path string) (entries []any, present bool, err error) {
	parent, field, err := statusListParent(status, path)
	if err != nil {
		return nil, false, err
	}
	value, present := parent[field]
	if !present {
		return nil, false, nil
	}
	list, ok := value.([]any)
	if !ok {
		return nil, true, fmt.Errorf("statusPatch: %s must contain an array", path)
	}
	return list, true, nil
}

func statusEntryMatches(entry any, selector map[string]any) bool {
	object, ok := entry.(map[string]any)
	if !ok {
		return false
	}
	for key, expected := range selector {
		actual, present := object[key]
		if !present {
			return false
		}
		if nested, ok := expected.(map[string]any); ok {
			if !statusEntryMatches(actual, nested) {
				return false
			}
			continue
		}
		left, leftErr := json.Marshal(actual)
		right, rightErr := json.Marshal(expected)
		if leftErr != nil || rightErr != nil || !bytes.Equal(left, right) {
			return false
		}
	}
	return true
}

// MergeStatusLists replaces selected entries while retaining other writers' current entries.
func MergeStatusLists(payload, current map[string]any, ownership string) (map[string]any, error) {
	rules, err := parseStatusListOwnership(ownership)
	if err != nil {
		return nil, err
	}
	merged, err := cloneStatusPatchVariant(payload)
	if err != nil {
		return nil, err
	}
	for path, selector := range rules {
		desired, present, err := statusListAt(merged, path)
		if err != nil {
			return nil, err
		}
		if !present {
			continue
		}
		entries, _, err := statusListAt(current, path)
		if err != nil {
			return nil, err
		}
		if err := validateStatusListEntries(desired, path, selector); err != nil {
			return nil, err
		}
		result := append([]any{}, desired...)
		for _, entry := range entries {
			if !statusEntryMatches(entry, selector) {
				result = append(result, entry)
			}
		}
		parent, field, err := statusListParent(merged, path)
		if err != nil {
			return nil, err
		}
		parent[field] = result
	}
	return merged, nil
}

func validateStatusListEntries(entries []any, path string, selector map[string]any) error {
	for _, entry := range entries {
		if !statusEntryMatches(entry, selector) {
			return fmt.Errorf("statusPatch: %s contains an entry outside its ownership selector", path)
		}
	}
	return nil
}
