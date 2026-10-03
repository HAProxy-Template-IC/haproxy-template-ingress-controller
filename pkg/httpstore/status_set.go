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

package httpstore

import (
	"encoding/json"
	"fmt"
	"net/http"
	"slices"
	"strconv"
	"strings"
)

// StatusSet is a set of extra HTTP statuses whose response body is content.
// It holds its sorted codes as one string so FetchOptions stays small and
// comparable, and equal sets compare equal however they were built.
type StatusSet struct {
	// sorted is ",404,503," for {404, 503}; "" for the empty set.
	sorted string
}

// NewStatusSet validates codes and returns their set. 200 is always content,
// so listing it is a no-op.
func NewStatusSet(codes ...int) (StatusSet, error) {
	kept := make([]int, 0, len(codes))
	for _, code := range codes {
		if code < 100 || code > 599 {
			return StatusSet{}, fmt.Errorf("acceptStatus %d is not an HTTP status; use 100-599", code)
		}
		// 304 answers the conditional request the store sends on refresh;
		// treating its empty body as content would erase the cached copy.
		if code == http.StatusNotModified {
			return StatusSet{}, fmt.Errorf("acceptStatus %d is reserved for conditional refresh; remove it", code)
		}
		if code != http.StatusOK {
			kept = append(kept, code)
		}
	}
	if len(kept) == 0 {
		return StatusSet{}, nil
	}
	slices.Sort(kept)
	kept = slices.Compact(kept)
	var b strings.Builder
	for _, code := range kept {
		b.WriteString(",")
		b.WriteString(strconv.Itoa(code))
	}
	b.WriteString(",")
	return StatusSet{sorted: b.String()}, nil
}

// Contains reports whether code is in the set.
func (s StatusSet) Contains(code int) bool {
	return s.sorted != "" && strings.Contains(s.sorted, ","+strconv.Itoa(code)+",")
}

// Codes returns the statuses in ascending order.
func (s StatusSet) Codes() []int {
	codes := []int{}
	for _, field := range strings.Split(strings.Trim(s.sorted, ","), ",") {
		if code, err := strconv.Atoi(field); err == nil {
			codes = append(codes, code)
		}
	}
	return codes
}

// MarshalJSON encodes the set as its sorted status list.
func (s StatusSet) MarshalJSON() ([]byte, error) {
	return json.Marshal(s.Codes())
}
