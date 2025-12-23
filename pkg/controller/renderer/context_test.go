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

package renderer

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"haptic/pkg/controller/rendercontext"
	"haptic/pkg/core/config"
)

func TestSortSnippetNames(t *testing.T) {
	tests := []struct {
		name     string
		snippets map[string]config.TemplateSnippet
		want     []string
	}{
		{
			name:     "empty snippets",
			snippets: map[string]config.TemplateSnippet{},
			want:     []string{},
		},
		{
			name: "single snippet",
			snippets: map[string]config.TemplateSnippet{
				"only": {Name: "only"},
			},
			want: []string{"only"},
		},
		{
			name: "alphabetical sorting",
			snippets: map[string]config.TemplateSnippet{
				"zebra":   {Name: "zebra"},
				"alpha":   {Name: "alpha"},
				"charlie": {Name: "charlie"},
			},
			want: []string{"alpha", "charlie", "zebra"},
		},
		{
			name: "ordering encoded in names with numbers",
			snippets: map[string]config.TemplateSnippet{
				"features-500-ssl":            {},
				"features-050-initialization": {},
				"features-150-crtlist":        {},
			},
			want: []string{
				"features-050-initialization",
				"features-150-crtlist",
				"features-500-ssl",
			},
		},
		{
			name: "real-world snippet ordering with priority-encoded names",
			snippets: map[string]config.TemplateSnippet{
				"backend-annotation-500-haproxytech-auth": {},
				"backend-annotation-200-rate-limit":       {},
				"backend-annotation-800-logging":          {},
				"backend-annotation-100-global-config":    {},
				"backend-annotation-500-cors":             {},
			},
			want: []string{
				"backend-annotation-100-global-config",
				"backend-annotation-200-rate-limit",
				"backend-annotation-500-cors",
				"backend-annotation-500-haproxytech-auth",
				"backend-annotation-800-logging",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := rendercontext.SortSnippetNames(tt.snippets)
			assert.Equal(t, tt.want, got)
		})
	}
}
