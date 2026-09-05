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
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gopkg.in/yaml.v3"

	"gitlab.com/haproxy-haptic/haptic/pkg/templating"
)

func TestNginxRewriteTargetCaptureReplacementPreservesRegexpBytes(t *testing.T) {
	inputs := []string{
		"",
		"/plain",
		"/$1/$2/$3/$4/$5/$6/$7/$8/$9",
		`/prefix\\$1/$9/suffix`,
		"/literal-dollar-$/double-$$/zero-$0/two-digits-$10",
	}
	snippet := loadNginxRewriteTargetSnippet(t)
	engine, err := templating.New(map[string]string{
		"main": `{%- import "util-nginx-ingress-rewrite-target" for RewriteTargetCaptures -%}` +
			`{%- for _, target := range toStringSlice(extraContext["targets"]) %}` +
			`{{ RewriteTargetCaptures(target) }}|{%- end %}`,
		"util-nginx-ingress-rewrite-target": snippet,
	}, nil)
	require.NoError(t, err)

	output, err := engine.Render(t.Context(), "main", map[string]any{
		"extraContext": map[string]any{"targets": inputs},
	})
	require.NoError(t, err)

	legacyRegexp := regexp.MustCompile(`\$([1-9])`)
	want := make([]string, 0, len(inputs))
	for _, input := range inputs {
		want = append(want, legacyRegexp.ReplaceAllString(input, `\$1`))
	}
	assert.Equal(t, strings.Join(want, "|")+"|\n", output)
}

func loadNginxRewriteTargetSnippet(t *testing.T) string {
	t.Helper()
	_, sourceFile, _, ok := runtime.Caller(0)
	require.True(t, ok)
	path := filepath.Join(filepath.Dir(sourceFile), "..", "..", "..", "charts", "haptic", "charts",
		"nginx-ingress", "10-backend-directives.yaml")
	content, err := os.ReadFile(path)
	require.NoError(t, err)
	var library backendServersChartLibrary
	require.NoError(t, yaml.Unmarshal(content, &library))
	snippet, found := library.TemplateSnippets["util-nginx-ingress-rewrite-target"]
	require.True(t, found)
	return snippet.Template
}
