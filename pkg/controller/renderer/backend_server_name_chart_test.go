// Copyright 2026 Philipp Hossner
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
// http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package renderer

import (
	"regexp"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"gitlab.com/haproxy-haptic/haptic/pkg/templating"
)

func TestBackendServerNamePreservesRegexpReplacement(t *testing.T) {
	inputs := []string{"", "safe.Name-1", "space slash/name", "bad;;;name", "grüße/雪"}
	snippet := loadBackendServersChartSnippets(t)["util-backend-servers-helpers"]
	engine, err := templating.New(map[string]string{
		"main": `{%- import "util-backend-servers-helpers" for ServerName -%}` +
			`{%- for _, name := range toStringSlice(extraContext["names"]) %}` +
			`{{ ServerName(name) }}|{%- end %}`,
		"util-backend-servers-helpers": snippet.Template,
	}, nil)
	require.NoError(t, err)

	output, err := engine.Render(t.Context(), "main", map[string]any{
		"extraContext": map[string]any{"names": inputs},
	})
	require.NoError(t, err)

	legacyRegexp := regexp.MustCompile(`[^A-Za-z0-9_.-]`)
	want := make([]string, 0, len(inputs))
	for _, input := range inputs {
		name := legacyRegexp.ReplaceAllString(input, "-")
		if name == "" {
			name = "srv"
		}
		if len(name) > 59 {
			name = name[:59]
		}
		want = append(want, name)
	}
	assert.Equal(t, strings.Join(want, "|")+"|\n", output)
}
