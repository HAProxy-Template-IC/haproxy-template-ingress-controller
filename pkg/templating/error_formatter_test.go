package templating

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

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
