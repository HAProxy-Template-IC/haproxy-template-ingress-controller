package templating_test

import (
	"testing"

	"haptic/pkg/templating"
)

func TestWhitespaceComments(t *testing.T) {
	tests := []struct {
		name     string
		template string
		snippet  string
	}{
		{
			name: "render_glob with snippet starting with empty string output",
			template: `# Extension point
{{- render_glob "snippet-*" }}
# After extension`,
			snippet: `{{- "" }}
{# This is a comment #}
# Output from snippet-a`,
		},
		{
			name: "exact gateway template pattern",
			template: `# Libraries can inject ordered http-request statements here
{{- render_glob "frontend-matchers-advanced-*" }}
# Re-parse path_match`,
			snippet: `{{- "" }}
{# Generate http-request statements for routes with advanced matchers
   This is injected into base template #}
# gateway/advanced-matcher-gateway
{{ "" -}}
# Advanced route matching`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			templates := map[string]string{
				"main":                               tt.template,
				"snippet-a":                          tt.snippet,
				"frontend-matchers-advanced-gateway": tt.snippet,
			}
			engine, err := templating.NewScriggo(templates, []string{"main"}, nil, nil, nil)
			if err != nil {
				t.Fatalf("NewScriggo error: %v", err)
			}

			output, err := engine.Render("main", nil)
			if err != nil {
				t.Fatalf("Render error: %v", err)
			}

			t.Logf("Template:\n%q", tt.template)
			t.Logf("Snippet:\n%q", tt.snippet)
			t.Logf("Got:\n%q", output)
		})
	}
}
