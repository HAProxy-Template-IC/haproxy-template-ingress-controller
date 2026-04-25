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

package templating

import (
	"math"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestScriggoToString(t *testing.T) {
	tests := []struct {
		name string
		in   any
		want string
	}{
		{name: "nil", in: nil, want: ""},
		{name: "empty string", in: "", want: ""},
		{name: "string", in: "hello", want: "hello"},
		{name: "int positive", in: 42, want: "42"},
		{name: "int zero", in: 0, want: "0"},
		{name: "int negative", in: -7, want: "-7"},
		{name: "int64", in: int64(9999999999), want: "9999999999"},
		{name: "float64 integral", in: 3.0, want: "3"},
		{name: "float64 fractional", in: 3.14, want: "3.14"},
		{name: "bool true", in: true, want: "true"},
		{name: "bool false", in: false, want: "false"},
		{name: "fallback to fmt.Sprint", in: []int{1, 2}, want: "[1 2]"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, scriggoToString(tt.in))
		})
	}
}

func TestScriggoToInt(t *testing.T) {
	tests := []struct {
		name string
		in   any
		want int
	}{
		{name: "nil", in: nil, want: 0},
		{name: "int", in: 42, want: 42},
		{name: "int negative", in: -5, want: -5},
		{name: "int64", in: int64(123), want: 123},
		{name: "float64 truncates", in: 3.9, want: 3},
		{name: "float64 negative truncates toward zero", in: -2.7, want: -2},
		{name: "valid string", in: "42", want: 42},
		{name: "valid negative string", in: "-7", want: -7},
		{name: "invalid string", in: "abc", want: 0},
		{name: "empty string", in: "", want: 0},
		{name: "unsupported type returns 0", in: true, want: 0},
		{name: "slice returns 0", in: []int{1}, want: 0},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, scriggoToInt(tt.in))
		})
	}
}

func TestScriggoToFloat(t *testing.T) {
	tests := []struct {
		name    string
		in      any
		want    float64
		wantErr bool
	}{
		{name: "nil", in: nil, want: 0},
		{name: "float64", in: 3.14, want: 3.14},
		{name: "int", in: 42, want: 42},
		{name: "int64", in: int64(123), want: 123},
		{name: "valid string", in: "2.5", want: 2.5},
		{name: "scientific notation string", in: "1e3", want: 1000},
		{name: "invalid string", in: "abc", wantErr: true},
		{name: "unsupported type", in: true, wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := scriggoToFloat(tt.in)
			if tt.wantErr {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)
			assert.InDelta(t, tt.want, got, 0.0001)
		})
	}
}

func TestScriggoToStringSlice(t *testing.T) {
	tests := []struct {
		name string
		in   any
		want []string
	}{
		{name: "nil", in: nil, want: []string{}},
		{name: "[]string passes through", in: []string{"a", "b"}, want: []string{"a", "b"}},
		{name: "[]any with strings", in: []any{"a", "b"}, want: []string{"a", "b"}},
		{name: "[]any with mixed types", in: []any{"a", 42, true}, want: []string{"a", "42", "true"}},
		{name: "[]any empty", in: []any{}, want: []string{}},
		{name: "unsupported type returns empty", in: 42, want: []string{}},
		{name: "string is unsupported", in: "abc", want: []string{}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, scriggoToStringSlice(tt.in))
		})
	}
}

func TestScriggoToSlice(t *testing.T) {
	tests := []struct {
		name string
		in   any
		want []any
	}{
		{name: "nil yields empty", in: nil, want: []any{}},
		{name: "[]any passes through", in: []any{1, 2, 3}, want: []any{1, 2, 3}},
		{name: "[]string converted via reflection", in: []string{"a", "b"}, want: []any{"a", "b"}},
		{name: "[]int converted via reflection", in: []int{1, 2, 3}, want: []any{1, 2, 3}},
		{name: "non-slice returns nil", in: 42, want: nil},
		{name: "string returns nil (not a slice)", in: "abc", want: nil},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, scriggoToSlice(tt.in))
		})
	}
}

func TestToSlice(t *testing.T) {
	tests := []struct {
		name   string
		in     any
		want   []any
		wantOK bool
	}{
		{name: "nil", in: nil, wantOK: false},
		{name: "[]any passes through", in: []any{1, 2}, want: []any{1, 2}, wantOK: true},
		{name: "[]string via reflection", in: []string{"x", "y"}, want: []any{"x", "y"}, wantOK: true},
		{name: "non-slice returns false", in: 42, wantOK: false},
		{name: "map returns false", in: map[string]int{"a": 1}, wantOK: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := toSlice(tt.in)
			assert.Equal(t, tt.wantOK, ok)
			if tt.wantOK {
				assert.Equal(t, tt.want, got)
			}
		})
	}
}

func TestScriggoCeil(t *testing.T) {
	tests := []struct {
		name string
		in   float64
		want float64
	}{
		{name: "integral", in: 3.0, want: 3},
		{name: "rounds up", in: 3.1, want: 4},
		{name: "rounds up barely", in: 3.99, want: 4},
		{name: "negative rounds toward zero", in: -3.5, want: -3},
		{name: "zero", in: 0, want: 0},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := scriggoCeil(tt.in)
			assert.True(t, math.Abs(got-tt.want) < 0.0001, "got %v, want %v", got, tt.want)
		})
	}
}

func TestScriggoSeq(t *testing.T) {
	tests := []struct {
		name string
		n    int
		want []int
	}{
		{name: "zero", n: 0, want: []int{}},
		{name: "negative", n: -3, want: []int{}},
		{name: "one", n: 1, want: []int{0}},
		{name: "five", n: 5, want: []int{0, 1, 2, 3, 4}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, scriggoSeq(tt.n))
		})
	}
}

func TestIsNilValue(t *testing.T) {
	type myStruct struct{ X int }
	var nilPtr *myStruct
	var nilMap map[string]int
	var nilSlice []int
	var nilFunc func()
	var nilChan chan int
	concrete := myStruct{}

	tests := []struct {
		name string
		in   any
		want bool
	}{
		{name: "untyped nil", in: nil, want: true},
		{name: "typed nil pointer", in: nilPtr, want: true},
		{name: "non-nil pointer", in: &concrete, want: false},
		{name: "nil map", in: nilMap, want: true},
		{name: "non-nil map", in: map[string]int{}, want: false},
		{name: "nil slice", in: nilSlice, want: true},
		{name: "non-nil slice", in: []int{}, want: false},
		{name: "nil func", in: nilFunc, want: true},
		{name: "nil chan", in: nilChan, want: true},
		{name: "int is not nil", in: 0, want: false},
		{name: "string is not nil", in: "", want: false},
		{name: "struct is not nil", in: concrete, want: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, isNilValue(tt.in))
			assert.Equal(t, tt.want, scriggoIsNil(tt.in))
		})
	}
}
