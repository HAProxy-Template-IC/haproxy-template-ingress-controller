// Copyright 2025 Philipp Hossner
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0

package templating

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// generateCompilationHints classifies a Scriggo compilation error string into a
// short list of actionable hints. The bucket boundaries (brace mismatch vs tag
// mismatch vs generic "expected"; "undefined" / "not declared"; the catch-all)
// are part of the user-facing CLI experience — pin them.
func TestGenerateCompilationHints(t *testing.T) {
	tests := []struct {
		name     string
		errorStr string
		want     []string // exact hint slice
	}{
		{
			name:     "expected '}' triggers brace-mismatch hints",
			errorStr: "validation:3:5: expected '}'",
			want: []string{
				"Check for missing or mismatched braces in your template.",
				"Ensure {% %} blocks are properly closed with {% end %}.",
			},
		},
		{
			name:     "expected '{' triggers brace-mismatch hints",
			errorStr: "validation:3:5: expected '{'",
			want: []string{
				"Check for missing or mismatched braces in your template.",
				"Ensure {% %} blocks are properly closed with {% end %}.",
			},
		},
		{
			name:     "expected '%}' triggers tag-close hints",
			errorStr: "validation:1:1: expected '%}'",
			want: []string{
				"Check for unclosed template tags.",
				"Ensure {{ }} and {% %} are properly closed.",
			},
		},
		{
			name:     "expected '}}' triggers tag-close hints",
			errorStr: "validation:1:1: expected '}}'",
			want: []string{
				"Check for unclosed template tags.",
				"Ensure {{ }} and {% %} are properly closed.",
			},
		},
		{
			name:     "generic 'expected' falls through to malformed hints",
			errorStr: "validation:1:1: expected operand",
			want: []string{
				"The template syntax is incomplete or malformed.",
				"Check for missing operators, parentheses, or keywords.",
			},
		},
		{
			// "unexpected" contains the substring "expected" so the generic-
			// "expected" branch ALSO fires. Pin this so a future refactor
			// (e.g. switching to a switch on prefix) doesn't silently drop
			// either set of hints.
			name:     "unexpected token also triggers the generic-expected branch",
			errorStr: "validation:1:1: unexpected token EOF",
			want: []string{
				"The template syntax is incomplete or malformed.",
				"Check for missing operators, parentheses, or keywords.",
				"The template contains an unexpected token at this location.",
				"Check for typos or misplaced syntax elements.",
			},
		},
		{
			name:     "undefined identifier hints",
			errorStr: "undefined: foo",
			want: []string{
				"The variable or function is not defined.",
				"Check spelling and ensure it's declared in the template context.",
			},
		},
		{
			name:     "not declared identifier hints",
			errorStr: "foo not declared",
			want: []string{
				"The variable or function is not defined.",
				"Check spelling and ensure it's declared in the template context.",
			},
		},
		{
			name:     "unknown error pattern falls back to generic hint",
			errorStr: "some completely opaque error string",
			want: []string{
				"Check your template syntax for errors.",
				"See Scriggo template documentation for syntax help.",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := generateCompilationHints(tt.errorStr)
			assert.Equal(t, tt.want, got)
		})
	}
}

// parseCompilationError must prefer the structured "name:line:col: msg" pattern
// when present, fall back to the runtime location/problem extractors otherwise,
// and always populate Hints.
func TestParseCompilationError(t *testing.T) {
	t.Run("scriggo compile pattern populates location and problem", func(t *testing.T) {
		got := parseCompilationError("validation:7:23: expected '}'")

		require.NotNil(t, got.Location)
		assert.Equal(t, 7, got.Location.Line)
		assert.Equal(t, 23, got.Location.Column)
		assert.Equal(t, "expected '}'", got.Problem)
		assert.NotEmpty(t, got.Hints, "hints must always be populated")
	})

	t.Run("non-scriggo error falls back to runtime parsers", func(t *testing.T) {
		// extractLocation matches "at line N", extractProblem matches "unknown method 'X'"
		got := parseCompilationError("at line 42: unknown method 'foo'")

		require.NotNil(t, got.Location, "fallback path must still extract location")
		assert.Equal(t, 42, got.Location.Line)
		assert.NotEmpty(t, got.Problem, "fallback path must still extract a problem string")
		assert.NotEmpty(t, got.Hints)
	})

	t.Run("opaque error has no location but still gets generic hints", func(t *testing.T) {
		got := parseCompilationError("totally opaque failure")

		// extractLocation returns nil when no pattern matches
		assert.Nil(t, got.Location)
		// generateCompilationHints always returns at least the generic fallback
		assert.NotEmpty(t, got.Hints)
	})
}

func TestFormatLocationLineOptionalColumn(t *testing.T) {
	tests := []struct {
		name string
		loc  *errorLocation
		want string
	}{
		{
			name: "non-zero column is included",
			loc:  &errorLocation{Line: 12, Column: 4},
			want: "Location: Line 12, Column 4\n",
		},
		{
			name: "zero column is omitted entirely",
			loc:  &errorLocation{Line: 12, Column: 0},
			want: "Location: Line 12\n",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := formatLocationLineOptionalColumn(tt.loc)
			assert.Equal(t, tt.want, got)
		})
	}
}
