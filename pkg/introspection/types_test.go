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
	"sync"
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

func TestIntVar(t *testing.T) {
	t.Run("NewInt creates with initial value", func(t *testing.T) {
		v := NewInt(42)

		assert.Equal(t, int64(42), v.Value())
	})

	t.Run("Get returns current value", func(t *testing.T) {
		v := NewInt(100)

		value, err := v.Get()

		require.NoError(t, err)
		assert.Equal(t, int64(100), value)
	})

	t.Run("Set updates value", func(t *testing.T) {
		v := NewInt(0)

		v.Set(50)

		assert.Equal(t, int64(50), v.Value())
	})

	t.Run("Add increments value", func(t *testing.T) {
		v := NewInt(10)

		v.Add(5)
		assert.Equal(t, int64(15), v.Value())

		v.Add(-3)
		assert.Equal(t, int64(12), v.Value())
	})

	t.Run("concurrent access is safe", func(t *testing.T) {
		v := NewInt(0)
		var wg sync.WaitGroup

		for range 100 {
			wg.Go(func() {
				v.Add(1)
			})
		}

		wg.Wait()
		assert.Equal(t, int64(100), v.Value())
	})
}

func TestStringVar(t *testing.T) {
	t.Run("NewString creates with initial value", func(t *testing.T) {
		v := NewString("initial")

		assert.Equal(t, "initial", v.Value())
	})

	t.Run("Get returns current value", func(t *testing.T) {
		v := NewString("test")

		value, err := v.Get()

		require.NoError(t, err)
		assert.Equal(t, "test", value)
	})

	t.Run("Set updates value", func(t *testing.T) {
		v := NewString("old")

		v.Set("new")

		assert.Equal(t, "new", v.Value())
	})

	t.Run("concurrent access is safe", func(t *testing.T) {
		v := NewString("start")
		var wg sync.WaitGroup

		for range 100 {
			wg.Add(2)
			go func() {
				defer wg.Done()
				v.Set("write")
			}()
			go func() {
				defer wg.Done()
				_ = v.Value()
			}()
		}

		wg.Wait()
		// No race conditions should occur
	})
}

func TestFloatVar(t *testing.T) {
	t.Run("NewFloat creates with initial value", func(t *testing.T) {
		v := NewFloat(3.14)

		assert.Equal(t, 3.14, v.Value())
	})

	t.Run("Get returns current value", func(t *testing.T) {
		v := NewFloat(2.71)

		value, err := v.Get()

		require.NoError(t, err)
		assert.Equal(t, 2.71, value)
	})

	t.Run("Set updates value", func(t *testing.T) {
		v := NewFloat(0.0)

		v.Set(1.5)

		assert.Equal(t, 1.5, v.Value())
	})

	t.Run("Add increments value", func(t *testing.T) {
		v := NewFloat(10.0)

		v.Add(2.5)
		assert.Equal(t, 12.5, v.Value())

		v.Add(-3.0)
		assert.Equal(t, 9.5, v.Value())
	})

	t.Run("concurrent access is safe", func(t *testing.T) {
		v := NewFloat(0.0)
		var wg sync.WaitGroup

		for range 100 {
			wg.Add(2)
			go func() {
				defer wg.Done()
				v.Add(1.0)
			}()
			go func() {
				defer wg.Done()
				_ = v.Value()
			}()
		}

		wg.Wait()
		// Value should be 100.0 (100 additions of 1.0)
		assert.Equal(t, 100.0, v.Value())
	})
}

func TestMapVar(t *testing.T) {
	t.Run("NewMap creates empty map", func(t *testing.T) {
		v := NewMap()

		assert.Equal(t, 0, v.Len())
	})

	t.Run("Set and Get", func(t *testing.T) {
		v := NewMap()

		v.Set("key1", "value1")
		v.Set("key2", 42)

		data, err := v.Get()
		require.NoError(t, err)

		m := data.(map[string]any)
		assert.Equal(t, "value1", m["key1"])
		assert.Equal(t, 42, m["key2"])
	})

	t.Run("Get returns copy", func(t *testing.T) {
		v := NewMap()
		v.Set("key", "value")

		data1, _ := v.Get()
		data2, _ := v.Get()

		// Modify first copy
		m1 := data1.(map[string]any)
		m1["key"] = "modified"

		// Second copy should be unaffected
		m2 := data2.(map[string]any)
		assert.Equal(t, "value", m2["key"])
	})

	t.Run("Delete removes key", func(t *testing.T) {
		v := NewMap()
		v.Set("key1", "value1")
		v.Set("key2", "value2")

		v.Delete("key1")

		assert.Equal(t, 1, v.Len())
		data, _ := v.Get()
		m := data.(map[string]any)
		_, exists := m["key1"]
		assert.False(t, exists)
	})

	t.Run("Len returns correct count", func(t *testing.T) {
		v := NewMap()
		assert.Equal(t, 0, v.Len())

		v.Set("a", 1)
		assert.Equal(t, 1, v.Len())

		v.Set("b", 2)
		assert.Equal(t, 2, v.Len())

		v.Delete("a")
		assert.Equal(t, 1, v.Len())
	})

	t.Run("concurrent access is safe", func(t *testing.T) {
		v := NewMap()
		var wg sync.WaitGroup

		for i := range 100 {
			wg.Add(3)
			idx := i
			go func() {
				defer wg.Done()
				v.Set("key", idx)
			}()
			go func() {
				defer wg.Done()
				_, _ = v.Get()
			}()
			go func() {
				defer wg.Done()
				_ = v.Len()
			}()
		}

		wg.Wait()
		// No race conditions should occur
	})
}
