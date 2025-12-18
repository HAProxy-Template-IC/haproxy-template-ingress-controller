package templating

import (
	"errors"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestFormatRenderError(t *testing.T) {
	tests := []struct {
		name            string
		err             error
		templateName    string
		templateContent string
		wantContains    []string
		wantNotContains []string
	}{
		{
			name:         "unknown method error with location",
			err:          errors.New("failed to render template 'host.map': unable to execute template: Unable to execute controlStructure at line 1: ForControlStructure(Line=1 Col=63): unable to evaluate 'call(['ingresses' {], map[])': invalid call to method 'get' of {'endpoints': [], 'ingresses': [], 'secrets': [], 'services': []}: unknown method 'get' for '{'endpoints': [], 'ingresses': [], 'secrets': [], 'services': []}'"),
			templateName: "host.map",
			templateContent: `{% for ingress in resources.ingresses.get() %}
{{ ingress.name }}
{% endfor %}`,
			wantContains: []string{
				"Template Rendering Error: host.map",
				"Line 1, Column 63",
				"Unknown method 'get'",
				"{% for ingress in resources.ingresses.get() %}",
				"Hint:",
				"dot notation",
				"bracket syntax",
			},
		},
		{
			name:         "undefined variable",
			err:          errors.New("unable to execute template: undefined variable 'foo' at line 2"),
			templateName: "test.cfg",
			templateContent: `line 1
{{ foo }}
line 3`,
			wantContains: []string{
				"Template Rendering Error: test.cfg",
				"Line 2",
				"Undefined variable 'foo'",
				"{{ foo }}",
				"Check that the variable is defined",
			},
		},
		{
			name:            "type mismatch",
			err:             errors.New("type error: expected string, got int"),
			templateName:    "config.txt",
			templateContent: `{{ value }}`,
			wantContains: []string{
				"Template Rendering Error: config.txt",
				"Type mismatch: expected string, got int",
				"different data type",
			},
		},
		{
			name:            "nil error",
			err:             nil,
			templateName:    "test",
			templateContent: "",
			wantContains:    []string{},
			wantNotContains: []string{"Template Rendering Error"},
		},
		{
			name:            "simple error without special patterns",
			err:             errors.New("something went wrong"),
			templateName:    "simple.txt",
			templateContent: `hello world`,
			wantContains: []string{
				"Template Rendering Error: simple.txt",
				"something went wrong",
				"Hint:",
				"Check your template syntax",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			formatted := FormatRenderError(tt.err, tt.templateName, tt.templateContent)

			if tt.err == nil {
				assert.Empty(t, formatted)
				return
			}

			// Check all expected strings are present
			for _, want := range tt.wantContains {
				assert.Contains(t, formatted, want,
					"formatted error should contain %q", want)
			}

			// Check unwanted strings are not present
			for _, notWant := range tt.wantNotContains {
				assert.NotContains(t, formatted, notWant,
					"formatted error should not contain %q", notWant)
			}

			// Verify it's multi-line
			if tt.err != nil {
				lines := strings.Split(formatted, "\n")
				assert.Greater(t, len(lines), 1,
					"formatted error should be multi-line")
			}
		})
	}
}

func TestExtractLocation(t *testing.T) {
	tests := []struct {
		name     string
		errorStr string
		want     *errorLocation
	}{
		{
			name:     "line and column present",
			errorStr: "error at Line=5 Col=10",
			want:     &errorLocation{Line: 5, Column: 10},
		},
		{
			name:     "only line present",
			errorStr: "error at line 3",
			want:     &errorLocation{Line: 3, Column: 0},
		},
		{
			name:     "no location",
			errorStr: "generic error",
			want:     nil,
		},
		{
			name:     "multiple line mentions (uses Line=X Col=Y pattern)",
			errorStr: "error at line 1: something at Line=2 Col=5",
			want:     &errorLocation{Line: 2, Column: 5},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := extractLocation(tt.errorStr)
			if tt.want == nil {
				assert.Nil(t, got)
			} else {
				require.NotNil(t, got)
				assert.Equal(t, tt.want.Line, got.Line)
				assert.Equal(t, tt.want.Column, got.Column)
			}
		})
	}
}

func TestExtractProblem(t *testing.T) {
	tests := []struct {
		name     string
		errorStr string
		want     string
	}{
		{
			name:     "unknown method",
			errorStr: "invalid call to method 'get': unknown method 'get' for type map",
			want:     "Unknown method 'get' - cannot call methods on this type",
		},
		{
			name:     "undefined variable",
			errorStr: "undefined variable 'foo'",
			want:     "Undefined variable 'foo'",
		},
		{
			name:     "type mismatch",
			errorStr: "type error: expected string, got int",
			want:     "Type mismatch: expected string, got int",
		},
		{
			name:     "unable to evaluate",
			errorStr: "unable to evaluate 'some.expression': syntax error",
			want:     "Unable to evaluate expression: 'some.expression'",
		},
		{
			name:     "no recognized pattern",
			errorStr: "generic template error",
			want:     "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := extractProblem(tt.errorStr)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestGenerateHints(t *testing.T) {
	tests := []struct {
		name         string
		errorStr     string
		wantContains []string
	}{
		{
			name:     "unknown method get",
			errorStr: "unknown method 'get'",
			wantContains: []string{
				"dot notation",
				"bracket syntax",
			},
		},
		{
			name:     "undefined variable",
			errorStr: "undefined variable 'foo'",
			wantContains: []string{
				"defined in the rendering context",
				"Verify spelling",
			},
		},
		{
			name:     "type mismatch",
			errorStr: "expected string, got int",
			wantContains: []string{
				"different data type",
				"Verify the types",
			},
		},
		{
			name:     "control structure error",
			errorStr: "ForControlStructure error",
			wantContains: []string{
				"loop or conditional",
				"iterating over a list",
			},
		},
		{
			name:     "generic error",
			errorStr: "something broke",
			wantContains: []string{
				"Check your template syntax",
				"Scriggo",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			hints := generateHints(tt.errorStr)
			require.NotEmpty(t, hints, "should always generate at least one hint")

			// Join hints into single string for easier checking
			allHints := strings.Join(hints, " ")

			for _, want := range tt.wantContains {
				assert.Contains(t, allHints, want,
					"hints should contain %q", want)
			}
		})
	}
}

func TestExtractTemplateContext(t *testing.T) {
	tests := []struct {
		name            string
		templateContent string
		line            int
		column          int
		wantContains    []string
	}{
		{
			name: "single line with column pointer",
			templateContent: `{% for item in items %}
{{ item.name }}
{% endfor %}`,
			line:   2,
			column: 5,
			wantContains: []string{
				"2 | {{ item.name }}",
				"    ^", // Caret should point to column 5
			},
		},
		{
			name:            "line out of range",
			templateContent: "line 1\nline 2",
			line:            10,
			column:          1,
			wantContains:    []string{}, // Should return empty string
		},
		{
			name:            "column zero (no caret)",
			templateContent: "hello world",
			line:            1,
			column:          0,
			wantContains: []string{
				"1 | hello world",
			},
		},
		{
			name:            "column too large (no caret)",
			templateContent: "short",
			line:            1,
			column:          100,
			wantContains: []string{
				"1 | short",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := extractTemplateContext(tt.templateContent, tt.line, tt.column)

			if len(tt.wantContains) == 0 {
				assert.Empty(t, got)
				return
			}

			for _, want := range tt.wantContains {
				assert.Contains(t, got, want)
			}
		})
	}
}

func TestFormatRenderErrorShort(t *testing.T) {
	tests := []struct {
		name         string
		err          error
		templateName string
		wantContains []string
		maxLength    int
	}{
		{
			name:         "error with location",
			err:          errors.New("unable to execute at Line=5 Col=10: unknown method 'get'"),
			templateName: "host.map",
			wantContains: []string{
				"Template: host.map",
				"Line 5 Col 10",
				"Unknown method 'get'",
			},
			maxLength: 200,
		},
		{
			name:         "nil error",
			err:          nil,
			templateName: "test",
			wantContains: []string{},
			maxLength:    0,
		},
		{
			name:         "long error gets truncated",
			err:          errors.New(strings.Repeat("a", 100)),
			templateName: "test",
			wantContains: []string{
				"Template: test",
				"...", // Should be truncated
			},
			maxLength: 150,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := FormatRenderErrorShort(tt.err, tt.templateName)

			if tt.err == nil {
				assert.Empty(t, got)
				return
			}

			for _, want := range tt.wantContains {
				assert.Contains(t, got, want)
			}

			if tt.maxLength > 0 {
				assert.LessOrEqual(t, len(got), tt.maxLength,
					"short format should not exceed reasonable length")
			}

			// Should be single line
			assert.NotContains(t, got, "\n",
				"short format should be single line")
		})
	}
}

// Benchmark formatting to ensure it's fast enough for production use.
func BenchmarkFormatRenderError(b *testing.B) {
	err := errors.New("failed to render template 'host.map': unable to execute template: Unable to execute controlStructure at line 1: ForControlStructure(Line=1 Col=63): unable to evaluate 'call(['ingresses' {], map[])': invalid call to method 'get' of {'endpoints': [], 'ingresses': [], 'secrets': [], 'services': []}: unknown method 'get' for '{'endpoints': [], 'ingresses': [], 'secrets': [], 'services': []}'")
	templateContent := `{% for ingress in resources.ingresses.get() %}
{{ ingress.name }}
{% endfor %}`

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		FormatRenderError(err, "host.map", templateContent)
	}
}

func BenchmarkFormatRenderErrorShort(b *testing.B) {
	err := errors.New("failed to render template 'host.map': unable to execute at Line=1 Col=63: unknown method 'get'")

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		FormatRenderErrorShort(err, "host.map")
	}
}
