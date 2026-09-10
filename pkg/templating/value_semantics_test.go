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
	"testing"

	"github.com/stretchr/testify/require"
)

func TestEnginePreservesValueSemantics(t *testing.T) {
	for _, tc := range []struct{ name, source, want string }{
		{
			name:   "slice variable snapshot",
			source: `{%% holder := struct { Values []int }{[]int{7}}; saved := holder.Values; holder.Values = nil %%}{{ len(saved) }}|{{ saved[0] }}`,
			want:   "1|7\n",
		},
		{
			name:   "map variable snapshot",
			source: `{%% holder := struct { Values map[string]int }{map[string]int{"a": 7}}; saved := holder.Values; holder.Values = nil %%}{{ saved.a }}`,
			want:   "7\n",
		},
		{
			name:   "nested aggregate writes",
			source: `{%% var value struct { Items [1]struct { N int } }; value.Items[0].N = 7; value.Items[0].N += 2 %%}{{ value.Items[0].N }}`,
			want:   "9\n",
		},
		{
			name:   "interface equality",
			source: `{%% var value any = true %%}{{ value == 8 }}|{{ value == true }}`,
			want:   "false|true\n",
		},
		{
			name:   "explicit and implicit string conversion",
			source: `{%% n := 97; bytes := []byte{97, 98} %%}{{ string(n) }}|{{ string(bytes) }}|{{ "count: " + n }}`,
			want:   "a|ab|count: 97\n",
		},
		{
			name:   "interface compound assignment",
			source: `{%% values := map[string]any{"n": 4}; values.n += 3 %%}{{ values.n }}`,
			want:   "7\n",
		},
		{
			name:   "native packed variadic arguments",
			source: `{{ sprintf("%s-%d", []any{"route", 7}...) }}`,
			want:   "route-7\n",
		},
		{
			name: "native callback recovery",
			source: `{%% f := func() (value string) {
                defer func() { value = tostring(recover()) }()
                sort_by([]int{2, 1}, func(a, b int) int { panic("callback token") })
                return "unreachable"
            } %%}{{ f() }}`,
			want: "callback token\n",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			engine, err := New(map[string]string{"main": tc.source}, nil)
			require.NoError(t, err)
			for range 3 {
				var output string
				require.NotPanics(t, func() {
					output, err = engine.Render(t.Context(), "main", nil)
				})
				require.NoError(t, err)
				require.Equal(t, tc.want, output)
			}
		})
	}
}
