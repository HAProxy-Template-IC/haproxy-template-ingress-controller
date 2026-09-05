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

package templating

import (
	"reflect"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestIncrementalDetachedValueTransfersOwnedValueOnce(t *testing.T) {
	shared := map[string]any{"name": "before"}
	source := map[string]any{
		"first":  shared,
		"second": shared,
		"items":  []any{map[string]any{"enabled": true}},
	}

	detached, err := NewIncrementalDetachedValue(source)
	require.NoError(t, err)
	shared["name"] = "after"
	source["items"].([]any)[0].(map[string]any)["enabled"] = false

	value, err := ConsumeIncrementalDetachedValue(detached)
	require.NoError(t, err)
	owned := value.(map[string]any)
	assert.Equal(t, "before", owned["first"].(map[string]any)["name"])
	assert.Equal(t, "before", owned["second"].(map[string]any)["name"])
	assert.Equal(t, true, owned["items"].([]any)[0].(map[string]any)["enabled"])

	owned["first"].(map[string]any)["name"] = "first-only"
	assert.Equal(t, "before", owned["second"].(map[string]any)["name"])
	_, err = ConsumeIncrementalDetachedValue(detached)
	require.ErrorContains(t, err, "transfer provenance")
}

func TestIncrementalDetachedValueConcurrentConsumption(t *testing.T) {
	detached, err := NewIncrementalDetachedValue(map[string]any{"name": "route"})
	require.NoError(t, err)

	var successes atomic.Int32
	var workers sync.WaitGroup
	for range 32 {
		workers.Add(1)
		go func() {
			defer workers.Done()
			if _, consumeErr := ConsumeIncrementalDetachedValue(detached); consumeErr == nil {
				successes.Add(1)
			}
		}()
	}
	workers.Wait()
	assert.Equal(t, int32(1), successes.Load())
}

func TestIncrementalDetachedValueRejectsPoisonedTransfer(t *testing.T) {
	tests := map[string]func(*IncrementalDetachedValue){
		"consumed": func(value *IncrementalDetachedValue) { value.consumed.Store(true) },
		"seal":     func(value *IncrementalDetachedValue) { value.seal = nil },
	}
	for name, poison := range tests {
		t.Run(name, func(t *testing.T) {
			detached, err := NewIncrementalDetachedValue(map[string]any{"name": "route"})
			require.NoError(t, err)
			poison(detached)
			_, err = ConsumeIncrementalDetachedValue(detached)
			require.Error(t, err)
		})
	}
}

func TestClonePlainIncrementalSerializationMatchesReflectiveClone(t *testing.T) {
	source := map[string]any{
		"bool":    true,
		"float32": float32(1.25),
		"float64": 2.5,
		"int":     int(-3),
		"list":    []any{"value", nil, map[string]any{"nested": uint64(4)}},
		"nilList": []any(nil),
		"nilMap":  map[string]any(nil),
		"uint":    uint(5),
	}

	plain, supported, err := clonePlainIncrementalSerialization(
		source,
		make(map[incrementalSerializationVisit]struct{}),
		0,
	)
	require.NoError(t, err)
	require.True(t, supported)
	require.NoError(t, validateIncrementalSerialization(source))
	reflective, err := cloneIncrementalSerializationValue(reflect.ValueOf(source), 0)
	require.NoError(t, err)
	assert.Equal(t, reflective.Interface(), plain)
}

func TestClonePlainIncrementalSerializationRejectsCycle(t *testing.T) {
	cyclic := map[string]any{}
	cyclic["self"] = cyclic
	_, err := cloneIncrementalSerialization(cyclic)
	require.ErrorContains(t, err, "reference cycle")
}

func TestCloneIncrementalExportedSerializationDoesNotRetainHiddenState(t *testing.T) {
	type value struct {
		Visible int
		hidden  *int
	}
	hidden := 7
	source := value{Visible: 3, hidden: &hidden}

	_, err := cloneIncrementalSerialization(source)
	require.ErrorContains(t, err, "field hidden is unexported")

	cloned, err := cloneIncrementalExportedSerialization(source)
	require.NoError(t, err)
	assert.Equal(t, 3, cloned.(value).Visible)
	assert.Nil(t, cloned.(value).hidden)
}

func BenchmarkIncrementalSerializationClone(b *testing.B) {
	value := map[string]any{
		"metadata": map[string]any{
			"name":      "route",
			"namespace": "default",
			"labels": map[string]any{
				"app": "echo",
			},
		},
		"rules": []any{
			map[string]any{"host": "example.test", "port": int64(8080)},
			map[string]any{"host": "other.test", "port": int64(8081)},
		},
	}
	b.Run("plain-one-pass", func(b *testing.B) {
		b.ReportAllocs()
		for range b.N {
			cloned, supported, err := clonePlainIncrementalSerialization(
				value,
				make(map[incrementalSerializationVisit]struct{}),
				0,
			)
			if err != nil || !supported || cloned == nil {
				b.Fatal(err)
			}
		}
	})
	b.Run("reflective-two-pass", func(b *testing.B) {
		b.ReportAllocs()
		for range b.N {
			if err := validateIncrementalSerialization(value); err != nil {
				b.Fatal(err)
			}
			cloned, err := cloneIncrementalSerializationValue(reflect.ValueOf(value), 0)
			if err != nil || !cloned.IsValid() {
				b.Fatal(err)
			}
		}
	})
}
