// Copyright 2025 Philipp Hossner
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0

package templating

import (
	"errors"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
)

// FormatCompilationError is the public surface that operators see
// when their templates fail to compile at controller startup.
// FormatRenderError has comprehensive table tests; FormatCompilationError
// (its compilation-side sibling) had ZERO direct test coverage.
//
// The function has a few load-bearing branches that determine
// whether the operator gets a usable error or a wall of internal text:
//
//  1. nil error → empty string. A regression that returned a header
//     line for nil would print "Template Compilation Error: foo"
//     on every successful compile.
//
//  2. Scriggo "name:line:col: message" pattern → location AND
//     problem extracted. The Scriggo compiler emits this exact
//     shape for syntax errors; a regression in scriggoCompilePattern
//     would silently downgrade every compile error to "no location"
//     output, making operators triage by reading the entire raw
//     error blob.
//
//  3. Compilation-specific hints fire on "expected '}'" /
//     "expected '%}'" / "unexpected" / "undefined" patterns.
//     Generic templates emit these phrases in nearly every syntax
//     error; the hints are the actionable part operators rely on.
//
//  4. formatLocationLineOptionalColumn: when the parsed error
//     supplies Column=0 (legitimately, e.g. fallback parser found
//     the line but not the column), the location line MUST omit
//     the ", Column 0" suffix. A regression would print
//     "Line 5, Column 0" — visually noisy and misleading
//     (operators would chase a non-existent column).
//
//  5. Template-context snippet appears when both location and
//     templateContent are present. Without this branch the
//     formatter skips the "Template Context:" block entirely
//     (operators lose the line-number-anchored snippet).

func TestFormatCompilationError_NilErrorReturnsEmpty(t *testing.T) {
	got := FormatCompilationError(nil, "foo.tmpl", "anything")
	assert.Empty(t, got,
		"nil error MUST produce empty output — a regression that emitted "+
			"the 'Template Compilation Error:' header on nil would log spurious "+
			"errors on every successful compile")
}

func TestFormatCompilationError_TableDriven(t *testing.T) {
	const sampleTemplate = "line1\n{% if x %}\nline3\n{% end %}\n"

	tests := []struct {
		name            string
		err             error
		templateName    string
		templateContent string
		wantContains    []string
		wantNotContains []string
	}{
		{
			name:            "scriggo syntax error with line+column produces full location",
			err:             errors.New("validation:2:5: expected '}', found 'EOF'"),
			templateName:    "main.tmpl",
			templateContent: sampleTemplate,
			wantContains: []string{
				"Template Compilation Error: main.tmpl",
				// Format-locking: the Scriggo pattern set both line
				// AND column, so the formatter must emit both.
				"Line 2, Column 5",
				// Problem text extracted from the third capture
				// group (after "name:line:col:") — operators read
				// this to know what's wrong.
				"expected '}'",
				// Hint for "expected '}'" must fire — this is the
				// actionable guidance operators rely on.
				"missing or mismatched braces",
				"end %}",
				// The template-context snippet must appear because
				// both location and content are present.
				"Template Context:",
			},
		},
		{
			name:            "scriggo close-tag error fires the close-tag hint",
			err:             errors.New("validation:1:8: expected '%}', got 'identifier'"),
			templateName:    "tags.tmpl",
			templateContent: "{% for x ",
			wantContains: []string{
				"Template Compilation Error: tags.tmpl",
				"Line 1, Column 8",
				"expected '%}'",
				// Distinct hint for unclosed template tags — DO NOT
				// confuse with the brace hint above.
				"unclosed template tags",
			},
			wantNotContains: []string{
				// The brace hint MUST NOT also fire on this kind of
				// error — they're different actionable categories.
				"missing or mismatched braces",
			},
		},
		{
			name:            "fallback parser path with line only omits column suffix",
			err:             errors.New("compile failure: unexpected token 'foo' at line 7"),
			templateName:    "fallback.tmpl",
			templateContent: "x\ny\nz\n",
			wantContains: []string{
				"Template Compilation Error: fallback.tmpl",
				// Critical: column omitted when zero (fallback
				// parser found only the line). A regression that
				// always printed the column would output ", Column 0"
				// here and confuse operators.
				"Line 7\n",
				// Hint for "unexpected" must fire.
				"unexpected token at this location",
			},
			wantNotContains: []string{
				", Column 0",
			},
		},
		{
			name:            "undefined identifier produces the undefined hint",
			err:             errors.New("validation:3:1: undefined: missingVar"),
			templateName:    "undef.tmpl",
			templateContent: "a\nb\n{{ missingVar }}\n",
			wantContains: []string{
				"Template Compilation Error: undef.tmpl",
				"Line 3, Column 1",
				"undefined: missingVar",
				"variable or function is not defined",
			},
		},
		{
			name:         "unknown error pattern falls through to the generic hint",
			err:          errors.New("something exploded internally"),
			templateName: "weird.tmpl",
			// No template content provided — exercise the "no
			// template context" branch (snippet block must be
			// absent).
			templateContent: "",
			wantContains: []string{
				"Template Compilation Error: weird.tmpl",
				"something exploded internally",
				// Generic fallback hint when no specific pattern
				// matched — operators still get a starting point.
				"Check your template syntax",
			},
			wantNotContains: []string{
				"Template Context:",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := FormatCompilationError(tt.err, tt.templateName, tt.templateContent)

			for _, want := range tt.wantContains {
				assert.True(t, strings.Contains(got, want),
					"formatted compilation error MUST contain %q\n--- output ---\n%s",
					want, got)
			}
			for _, notWant := range tt.wantNotContains {
				assert.False(t, strings.Contains(got, notWant),
					"formatted compilation error MUST NOT contain %q\n--- output ---\n%s",
					notWant, got)
			}
		})
	}
}
