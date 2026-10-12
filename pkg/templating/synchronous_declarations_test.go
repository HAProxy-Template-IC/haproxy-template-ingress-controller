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
	"testing"

	"github.com/stretchr/testify/require"
	"gitlab.com/haproxy-haptic/scriggo"
	"gitlab.com/haproxy-haptic/scriggo/native"
)

func TestBuiltInDeclarationsPermitExecutionReuse(t *testing.T) {
	for _, source := range []string{
		`{{ dig(item, "value") }}`,
		`{{ pathResolver.GetPath("hosts.map", "map") }}`,
		`{% var sorted, _ = sort_by([]map[string]any{{"name": "b"}, {"name": "a"}}, []string{"$.name"}) %}{{ len(sorted) }}`,
	} {
		t.Run(source, func(t *testing.T) {
			engine, err := New(map[string]string{"main": source}, nil)
			require.NoError(t, err)
			require.True(t, engine.compiledTemplates["main"].BatchSafe())
		})
	}
}

func TestCustomDeclarationsDoNotInheritExecutionReuse(t *testing.T) {
	customFunction := func(...any) (any, error) { return "custom", nil }
	customFilter := func(any, ...any) (any, error) { return "custom", nil }
	for name, declarations := range map[string]native.Declarations{
		"function":    buildScriggoGlobals(nil, map[string]GlobalFunc{FuncDig: customFunction}, nil),
		"filter":      buildScriggoGlobals(map[string]FilterFunc{FuncDig: customFilter}, nil, nil),
		"declaration": buildScriggoGlobals(nil, nil, map[string]any{FuncDig: customFunction}),
	} {
		t.Run(name, func(t *testing.T) {
			compiled, err := scriggo.BuildTemplate(scriggo.Files{"main": []byte(`{{ dig(item, "value") }}`)}, "main", &scriggo.BuildOptions{Globals: declarations})
			require.NoError(t, err)
			require.False(t, compiled.BatchSafe())
		})
	}
}
