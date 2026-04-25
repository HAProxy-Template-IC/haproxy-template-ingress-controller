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

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestCopySlice(t *testing.T) {
	t.Run("nil yields nil", func(t *testing.T) {
		assert.Nil(t, copySlice[string](nil))
	})

	t.Run("empty yields nil (not empty)", func(t *testing.T) {
		// Documented: "A nil or empty src yields nil"
		assert.Nil(t, copySlice([]string{}))
	})

	t.Run("strings copied", func(t *testing.T) {
		src := []string{"a", "b", "c"}
		dst := copySlice(src)
		assert.Equal(t, src, dst)
	})

	t.Run("ints copied", func(t *testing.T) {
		src := []int{1, 2, 3}
		dst := copySlice(src)
		assert.Equal(t, src, dst)
	})

	t.Run("returned slice is independent of source", func(t *testing.T) {
		src := []string{"a", "b", "c"}
		dst := copySlice(src)
		require.Equal(t, src, dst)

		// Mutate source — must not affect destination.
		src[0] = "mutated"
		assert.Equal(t, "a", dst[0])
	})

	t.Run("returned slice does not share backing array", func(t *testing.T) {
		src := make([]string, 3, 10) // length 3, cap 10
		src[0], src[1], src[2] = "a", "b", "c"
		dst := copySlice(src)

		// If they shared the backing array, append-without-realloc would mutate dst.
		_ = append(src, "d")
		assert.Equal(t, []string{"a", "b", "c"}, dst)
		assert.Len(t, dst, 3)
	})
}

func TestCopyStringSlicesMap(t *testing.T) {
	t.Run("nil yields nil", func(t *testing.T) {
		assert.Nil(t, copyStringSlicesMap(nil))
	})

	t.Run("empty map yields empty (non-nil) map", func(t *testing.T) {
		got := copyStringSlicesMap(map[string][]string{})
		require.NotNil(t, got)
		assert.Empty(t, got)
	})

	t.Run("values are copied independently", func(t *testing.T) {
		src := map[string][]string{
			"a": {"1", "2"},
			"b": {"3", "4"},
		}
		dst := copyStringSlicesMap(src)
		assert.Equal(t, src, dst)

		// Mutating the source slice value must not affect destination.
		src["a"][0] = "mutated"
		assert.Equal(t, "1", dst["a"][0])

		// Adding a new key to source must not affect destination.
		src["c"] = []string{"5"}
		assert.NotContains(t, dst, "c")
	})

	t.Run("empty slice values become nil", func(t *testing.T) {
		src := map[string][]string{
			"empty": {},
			"value": {"x"},
		}
		dst := copyStringSlicesMap(src)
		assert.Nil(t, dst["empty"])
		assert.Equal(t, []string{"x"}, dst["value"])
	})

	t.Run("nil slice values become nil", func(t *testing.T) {
		src := map[string][]string{"nilslice": nil}
		dst := copyStringSlicesMap(src)
		assert.Nil(t, dst["nilslice"])
	})
}
