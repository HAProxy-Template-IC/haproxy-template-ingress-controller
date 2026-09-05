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
	"math"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type incrementalHTTPStringer struct {
	calls *int
}

func (s incrementalHTTPStringer) String() string {
	(*s.calls)++
	return "native"
}

type incrementalHTTPMarshalerMap map[string]any

func (m incrementalHTTPMarshalerMap) MarshalJSON() ([]byte, error) {
	stringer := m["stringer"].(incrementalHTTPStringer)
	(*stringer.calls)++
	return []byte(`{}`), nil
}

func TestCanonicalIncrementalHTTPArgsDetachesSupportedValues(t *testing.T) {
	options := map[string]any{
		"interval": "1m",
		"timeout":  "2s",
		"retries":  uint8(3),
		"critical": true,
	}
	headers := map[string]string{"X-Test": "value"}
	auth := map[string]any{
		"type":     "header",
		"username": "user",
		"password": "secret",
		"token":    "token",
		"headers":  headers,
	}

	canonical, err := CanonicalIncrementalHTTPArgs("https://example.test/data", options, auth)
	require.NoError(t, err)
	require.Len(t, canonical, 3)
	assert.Equal(t, "https://example.test/data", canonical[0])
	assert.Equal(t, map[string]any{
		"interval": "1m",
		"timeout":  "2s",
		"retries":  3,
		"critical": true,
	}, canonical[1])
	assert.Equal(t, map[string]any{
		"type":     "header",
		"username": "user",
		"password": "secret",
		"token":    "token",
		"headers":  map[string]any{"X-Test": "value"},
	}, canonical[2])

	options["timeout"] = "changed"
	headers["X-Test"] = "changed"
	assert.Equal(t, "2s", canonical[1].(map[string]any)["timeout"])
	assert.Equal(t, "value", canonical[2].(map[string]any)["headers"].(map[string]any)["X-Test"])
}

func TestCanonicalIncrementalHTTPArgsPreservesOptionalNil(t *testing.T) {
	canonical, err := CanonicalIncrementalHTTPArgs("https://example.test/data", nil, nil)
	require.NoError(t, err)
	assert.Equal(t, []any{"https://example.test/data", nil, nil}, canonical)
}

func TestCanonicalIncrementalHTTPArgsRejectsUnsafeValuesWithoutCallingMethods(t *testing.T) {
	calls := 0
	stringer := incrementalHTTPStringer{calls: &calls}
	marshaler := incrementalHTTPMarshalerMap{"stringer": stringer}
	tests := map[string]struct {
		args []any
		want string
	}{
		"missing URL": {
			want: "requires 1 to 3 arguments",
		},
		"extra argument": {
			args: []any{"https://example.test", nil, nil, "ignored"},
			want: "requires 1 to 3 arguments",
		},
		"Stringer URL": {
			args: []any{stringer},
			want: "plain string",
		},
		"non-string map keys": {
			args: []any{"https://example.test", map[any]any{stringer: "1m"}},
			want: "string-keyed map",
		},
		"custom marshaler map": {
			args: []any{"https://example.test", marshaler},
			want: "custom marshaler",
		},
		"unknown option": {
			args: []any{"https://example.test", map[string]any{"future": true}},
			want: "unknown options key",
		},
		"Stringer duration": {
			args: []any{"https://example.test", map[string]any{"timeout": stringer}},
			want: "fmt.Stringer",
		},
		"pointer duration": {
			args: []any{"https://example.test", map[string]any{"timeout": new(string)}},
			want: "plain duration string",
		},
		"non-finite retries": {
			args: []any{"https://example.test", map[string]any{"retries": math.NaN()}},
			want: "non-finite float",
		},
		"structured retries": {
			args: []any{"https://example.test", map[string]any{"retries": struct{}{}}},
			want: "no deterministic scalar representation",
		},
		"unknown auth field": {
			args: []any{"https://example.test", nil, map[string]any{"future": "value"}},
			want: "unknown auth key",
		},
		"Stringer auth field": {
			args: []any{"https://example.test", nil, map[string]any{"token": stringer}},
			want: "fmt.Stringer",
		},
		"non-string headers": {
			args: []any{"https://example.test", nil, map[string]any{"headers": map[string]any{"X-Test": 1}}},
			want: "plain string",
		},
		"non-string header keys": {
			args: []any{"https://example.test", nil, map[string]any{"headers": map[any]any{stringer: "value"}}},
			want: "string-keyed map",
		},
	}

	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			_, err := CanonicalIncrementalHTTPArgs(test.args...)
			require.ErrorContains(t, err, test.want)
		})
	}
	assert.Zero(t, calls)
}

func TestCanonicalIncrementalHTTPArgsRejectsIntervalAliasConflict(t *testing.T) {
	_, err := CanonicalIncrementalHTTPArgs("https://example.test", map[string]any{
		"interval": "1m",
		"delay":    "1m",
	})
	require.ErrorContains(t, err, "set either")
}
