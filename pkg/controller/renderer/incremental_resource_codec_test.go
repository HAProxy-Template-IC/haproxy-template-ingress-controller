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

package renderer

import (
	"bytes"
	"encoding/json"
	"math"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/require"
)

type resourceCodecStringer struct {
	calls *atomic.Int32
}

func (v resourceCodecStringer) String() string {
	v.calls.Add(1)
	return "native"
}

type resourceCodecJSONMarshaler struct {
	calls *atomic.Int32
}

func (v resourceCodecJSONMarshaler) MarshalJSON() ([]byte, error) {
	v.calls.Add(1)
	return []byte("\"native\""), nil
}

type resourceCodecTextMarshaler struct {
	calls *atomic.Int32
}

func (v resourceCodecTextMarshaler) MarshalText() ([]byte, error) {
	v.calls.Add(1)
	return []byte("native"), nil
}

type resourceCodecAccessor struct {
	calls *atomic.Int32
}

func (v resourceCodecAccessor) GetNamespace() string {
	v.calls.Add(1)
	return "default"
}

func (v resourceCodecAccessor) GetName() string {
	v.calls.Add(1)
	return "target"
}

type resourceCodecTypedMap map[string]any

func TestResourceCodecPreservesPlainDeterministicTree(t *testing.T) {
	value := map[string]any{
		"z": []any{nil, true, "text", int(-1), int8(-2), int16(-3), int32(-4), int64(-5)},
		"a": map[string]any{
			"unsigned": []any{uint(1), uint8(2), uint16(3), uint32(4), uint64(math.MaxUint64)},
			"floating": []any{float32(1.25), float64(2.5)},
		},
	}

	encoded, err := encodeResourceValue(value)
	require.NoError(t, err)
	require.Equal(t,
		"{\"a\":{\"floating\":[1.25,2.5],\"unsigned\":[1,2,3,4,18446744073709551615]},"+
			"\"z\":[null,true,\"text\",-1,-2,-3,-4,-5]}",
		string(encoded),
	)

	decoded, err := decodeResourceValue(encoded)
	require.NoError(t, err)
	decodedMap := decoded.(map[string]any)
	unsigned := decodedMap["a"].(map[string]any)["unsigned"].([]any)
	require.IsType(t, int64(0), unsigned[0])
	require.IsType(t, uint64(0), unsigned[4])
	floating := decodedMap["a"].(map[string]any)["floating"].([]any)
	require.IsType(t, float64(0), floating[0])
}

func TestResourceCodecRejectsNativeValuesWithoutCallingMethods(t *testing.T) {
	tests := map[string]func(*atomic.Int32) any{
		"stringer": func(calls *atomic.Int32) any {
			return resourceCodecStringer{calls: calls}
		},
		"JSON marshaler": func(calls *atomic.Int32) any {
			return resourceCodecJSONMarshaler{calls: calls}
		},
		"text marshaler": func(calls *atomic.Int32) any {
			return resourceCodecTextMarshaler{calls: calls}
		},
	}

	for name, value := range tests {
		t.Run(name, func(t *testing.T) {
			var calls atomic.Int32
			_, err := encodeResourceValue(map[string]any{"native": value(&calls)})
			require.Error(t, err)
			require.Zero(t, calls.Load())

			_, err = normalizeDecodedResourceNumbers(map[string]any{"native": value(&calls)}, 0)
			require.Error(t, err)
			require.Zero(t, calls.Load())
		})
	}
}

func TestResourceCodecRejectsNonPlainValues(t *testing.T) {
	scalar := 1
	tests := map[string]any{
		"pointer":       &scalar,
		"typed map":     resourceCodecTypedMap{"value": "x"},
		"typed slice":   []string{"x"},
		"raw JSON":      json.RawMessage("{\"value\":\"x\"}"),
		"JSON number":   json.Number("1"),
		"NaN":           math.NaN(),
		"positive Inf":  math.Inf(1),
		"negative Inf":  math.Inf(-1),
		"invalid UTF-8": string([]byte{0xff}),
	}

	for name, value := range tests {
		t.Run(name, func(t *testing.T) {
			_, err := encodeResourceValue(value)
			require.Error(t, err)
		})
	}

	_, err := encodeResourceValue(map[string]any{string([]byte{0xff}): "value"})
	require.ErrorContains(t, err, "invalid UTF-8 map key")
}

func TestResourceCodecRejectsReferenceCycles(t *testing.T) {
	mapCycle := map[string]any{}
	mapCycle["self"] = mapCycle
	_, err := encodeResourceValue(mapCycle)
	require.ErrorContains(t, err, "reference cycle")

	sliceCycle := make([]any, 1)
	sliceCycle[0] = sliceCycle
	_, err = encodeResourceValue(sliceCycle)
	require.ErrorContains(t, err, "reference cycle")
}

func TestResourceCodecAllowsSharedAcyclicValues(t *testing.T) {
	shared := map[string]any{"value": []any{"x"}}
	encoded, err := encodeResourceValue(map[string]any{"first": shared, "second": shared})
	require.NoError(t, err)
	require.JSONEq(t,
		"{\"first\":{\"value\":[\"x\"]},\"second\":{\"value\":[\"x\"]}}",
		string(encoded),
	)
}

func TestResourceCodecRejectsExcessiveDepth(t *testing.T) {
	var value any = "leaf"
	for range resourceValueMaxDepth + 1 {
		value = []any{value}
	}

	_, err := encodeResourceValue(value)
	require.ErrorContains(t, err, "maximum depth")
}

func TestDecodeResourceValueNormalizesIntegersAndRejectsTrailingValues(t *testing.T) {
	decoded, err := decodeResourceValue([]byte(
		"{\"negative\":-1,\"positive\":1,\"large\":18446744073709551615,\"decimal\":1.5}",
	))
	require.NoError(t, err)
	resource := decoded.(map[string]any)
	require.Equal(t, int64(-1), resource["negative"])
	require.Equal(t, int64(1), resource["positive"])
	require.Equal(t, uint64(math.MaxUint64), resource["large"])
	require.Equal(t, float64(1.5), resource["decimal"])

	_, err = decodeResourceValue([]byte("{} {}"))
	require.ErrorContains(t, err, "multiple JSON values")

	_, err = decodeResourceValue([]byte("1e400"))
	require.Error(t, err)

	_, err = decodeResourceValue([]byte{'"', 0xff, '"'})
	require.ErrorContains(t, err, "not valid UTF-8")
}

func TestResourceIdentityReadsOnlyPlainMaps(t *testing.T) {
	resource := map[string]any{
		"metadata": map[string]any{
			"namespace": "default",
			"name":      "target",
		},
	}
	namespace, name, found := resourceIdentity(resource)
	require.True(t, found)
	require.Equal(t, "default", namespace)
	require.Equal(t, "target", name)

	var calls atomic.Int32
	namespace, name, found = resourceIdentity(resourceCodecAccessor{calls: &calls})
	require.False(t, found)
	require.Empty(t, namespace)
	require.Empty(t, name)
	require.Zero(t, calls.Load())
}

func FuzzResourceCodecCanonicalRoundTrip(f *testing.F) {
	for _, seed := range [][]byte{
		[]byte("null"),
		[]byte("{}"),
		[]byte("[]"),
		[]byte("{\"number\":18446744073709551615,\"list\":[true,1.5,\"text\"]}"),
		[]byte("{\"duplicate\":1,\"duplicate\":2}"),
	} {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, input []byte) {
		if !json.Valid(input) {
			return
		}
		value, err := decodeResourceValue(input)
		if err != nil {
			return
		}
		canonical, err := encodeResourceValue(value)
		require.NoError(t, err)
		roundTrip, err := decodeResourceValue(canonical)
		require.NoError(t, err)
		reencoded, err := encodeResourceValue(roundTrip)
		require.NoError(t, err)
		require.True(t, bytes.Equal(canonical, reencoded))
	})
}
