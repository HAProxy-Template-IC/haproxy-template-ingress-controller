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

// copySlice returns an independent copy of src. A nil or empty src yields nil,
// preserving the defensive-copy semantics established across event
// constructors (callers cannot distinguish "no data" from "empty data").
func copySlice[T any](src []T) []T {
	if len(src) == 0 {
		return nil
	}
	dst := make([]T, len(src))
	copy(dst, src)
	return dst
}

// copyStringSlicesMap returns an independent copy of src and its slice values.
// Empty slice values are preserved as nil to match the slice-copy semantics.
func copyStringSlicesMap(src map[string][]string) map[string][]string {
	if src == nil {
		return nil
	}
	dst := make(map[string][]string, len(src))
	for k, v := range src {
		dst[k] = copySlice(v)
	}
	return dst
}
