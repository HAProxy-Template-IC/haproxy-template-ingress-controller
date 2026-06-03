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

package introspection

import (
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestFunc_Get(t *testing.T) {
	t.Run("returns value from function", func(t *testing.T) {
		f := Func(func() (any, error) {
			return "computed value", nil
		})

		value, err := f.Get()

		require.NoError(t, err)
		assert.Equal(t, "computed value", value)
	})

	t.Run("returns error from function", func(t *testing.T) {
		f := Func(func() (any, error) {
			return nil, errors.New("computation failed")
		})

		_, err := f.Get()

		require.Error(t, err)
		assert.Contains(t, err.Error(), "computation failed")
	})

	t.Run("computes value on each call", func(t *testing.T) {
		counter := 0
		f := Func(func() (any, error) {
			counter++
			return counter, nil
		})

		v1, _ := f.Get()
		v2, _ := f.Get()
		v3, _ := f.Get()

		assert.Equal(t, 1, v1)
		assert.Equal(t, 2, v2)
		assert.Equal(t, 3, v3)
	})
}
