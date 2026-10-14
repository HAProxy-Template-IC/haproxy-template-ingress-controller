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

package rendercontext

import (
	"fmt"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func BenchmarkDerivedResourceProjection(b *testing.B) {
	for _, fields := range []int{16, 128} {
		b.Run(fmt.Sprintf("fields-%d", fields), func(b *testing.B) {
			spec := make(map[string]any, fields)
			for index := range fields {
				spec[fmt.Sprintf("field-%d", index)] = strings.Repeat("value", 16)
			}
			source := map[string]any{
				"metadata": map[string]any{"namespace": "default", "name": "object"},
				"spec":     spec,
			}
			encoded, err := encodeDerivedResource(source)
			require.NoError(b, err)
			identity, err := derivedResourceIdentity("objects", source)
			require.NoError(b, err)
			entry := DerivedResource{Identity: identity, Source: encoded, Value: encoded}
			view := NewDerivedResourceViewWithResolver(DerivedResourceResolverFunc(func(
				DerivedResourceIdentity,
			) (DerivedResource, bool, error) {
				return entry, true, nil
			}))
			view.Freeze()
			items := []any{source}
			for b.Loop() {
				if _, err := view.Project("objects", items); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}
