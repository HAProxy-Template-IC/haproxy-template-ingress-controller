// Copyright 2025 Philipp Hossner
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0

package enterprise

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// parseDirective is the line-tokenizer that every EE-section extractor
// (WAF global, WAF profile, BotMgmt profile, captcha, etc.) feeds raw
// config lines through. Pin every contract:
//   - empty / whitespace-only lines yield ("", nil)
//   - keyword-only lines yield (keyword, nil)
//   - keyword + values returns the values verbatim, in order
//   - inline comments after '#' are stripped
//   - '#' inside a quoted string is NOT treated as a comment
func TestParseDirective(t *testing.T) {
	tests := []struct {
		name       string
		line       string
		wantKey    string
		wantValues []string
	}{
		{name: "empty input", line: "", wantKey: "", wantValues: nil},
		{name: "whitespace only", line: "   \t  ", wantKey: "", wantValues: nil},
		{name: "keyword only", line: "block-by-default", wantKey: "block-by-default", wantValues: nil},
		{name: "keyword + single value", line: "log on", wantKey: "log", wantValues: []string{"on"}},
		{name: "keyword + multiple values", line: "track ip http_req_rate", wantKey: "track", wantValues: []string{"ip", "http_req_rate"}},
		{
			name:       "inline comment is stripped",
			line:       "log on # enable WAF logging",
			wantKey:    "log",
			wantValues: []string{"on"},
		},
		{
			name:       "hash inside quoted string is NOT treated as a comment",
			line:       `set-var "value-with-#hash"`,
			wantKey:    "set-var",
			wantValues: []string{"value-with-#hash"},
		},
		{
			name:       "leading whitespace is preserved by tokenizer",
			line:       "   indented value",
			wantKey:    "indented",
			wantValues: []string{"value"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gotKey, gotValues := parseDirective(tt.line)
			assert.Equal(t, tt.wantKey, gotKey)
			assert.Equal(t, tt.wantValues, gotValues)
		})
	}
}

// isInQuotedString tracks quote state up to a position, so callers can
// decide whether a given character (typically '#') is inside a quoted
// string and should NOT be interpreted as a comment marker.
//
// Pin every branch:
//   - position 0 is never inside quotes (trivial start)
//   - characters BEFORE an opening quote are not inside quotes
//   - characters BETWEEN matching quotes are inside
//   - characters AFTER a closing quote are no longer inside
//   - escaped quotes (\') do NOT close the string
//   - position out of bounds is silently capped (no panic)
//   - mismatched single/double quote types do not close each other
func TestIsInQuotedString(t *testing.T) {
	tests := []struct {
		name string
		s    string
		pos  int
		want bool
	}{
		{name: "position 0 is never inside", s: `"hello"`, pos: 0, want: false},
		{name: "before opening quote", s: `pre"hello"`, pos: 2, want: false},
		{name: "inside double-quoted string", s: `"hello"`, pos: 3, want: true},
		{name: "after closing quote", s: `"hello"world`, pos: 8, want: false},
		{name: "inside single-quoted string", s: `'hello'`, pos: 3, want: true},
		{name: "double quote inside single-quoted string is ignored", s: `'a"b'`, pos: 3, want: true},
		{name: "escaped quote does not close (still inside)", s: `"a\"b"`, pos: 4, want: true},
		{name: "position past end is capped to len(s)", s: `"hello"`, pos: 1000, want: false},
		{name: "unclosed quote — every position after is inside", s: `"unterminated`, pos: 5, want: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, isInQuotedString(tt.s, tt.pos))
		})
	}
}

// splitQuotedFields tokenizes a string into fields the way HAProxy
// configuration directives are split: whitespace separates tokens
// EXCEPT inside matched quotes. Pin the contracts:
//   - empty input yields no fields
//   - whitespace-only input yields no fields
//   - a single token yields a one-element slice
//   - tabs and spaces both separate tokens
//   - quoted tokens preserve internal whitespace
//   - escaped quotes inside a quoted token don't close it
//   - quote characters themselves are stripped from the output
func TestSplitQuotedFields(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want []string
	}{
		{name: "empty input", in: "", want: nil},
		{name: "whitespace only", in: "   \t  ", want: nil},
		{name: "single token", in: "abc", want: []string{"abc"}},
		{name: "space-separated tokens", in: "a b c", want: []string{"a", "b", "c"}},
		{name: "tab-separated tokens", in: "a\tb\tc", want: []string{"a", "b", "c"}},
		{name: "mixed spaces and tabs", in: "a\t b \tc", want: []string{"a", "b", "c"}},
		{
			name: "quoted token preserves internal whitespace",
			in:   `a "with spaces" c`,
			want: []string{"a", "with spaces", "c"},
		},
		{
			name: "single-quoted token preserves internal whitespace",
			in:   `a 'with spaces' c`,
			want: []string{"a", "with spaces", "c"},
		},
		{
			// The escape-sequence handling is "don't treat \" as closing
			// quote", NOT "consume the escape". The function preserves
			// both the backslash AND the quote in the output verbatim,
			// because nothing in this tokenizer interprets escape
			// sequences. Pin that observable behaviour rather than what
			// a HAProxy-config-savvy reader might expect.
			name: "escaped double quote inside double-quoted token does not close it",
			in:   `a "esc\"quote" b`,
			want: []string{"a", `esc\"quote`, "b"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := splitQuotedFields(tt.in)
			assert.Equal(t, tt.want, got)
		})
	}
}

// parseInt returns *int on success, nil on parse error. Pin both
// branches because every EE field setter funnels through this and a
// future refactor that switched to swallowing errors as 0 would
// silently mask malformed config.
func TestParseInt(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want *int
	}{
		{name: "positive integer", in: "42", want: intPtr(42)},
		{name: "zero", in: "0", want: intPtr(0)},
		{name: "negative integer", in: "-7", want: intPtr(-7)},
		{name: "leading whitespace is rejected (Atoi semantics)", in: " 7", want: nil},
		{name: "trailing whitespace is rejected", in: "7 ", want: nil},
		{name: "non-numeric input yields nil", in: "abc", want: nil},
		{name: "empty input yields nil", in: "", want: nil},
		{name: "float input yields nil", in: "1.5", want: nil},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := parseInt(tt.in)
			if tt.want == nil {
				assert.Nil(t, got)
				return
			}
			require.NotNil(t, got)
			assert.Equal(t, *tt.want, *got)
		})
	}
}

// parseBool accepts a curated set of HAProxy-config truthy/falsy
// strings (case-insensitive) and returns nil for anything else. Pin
// every accepted alias and the unknown-input rejection.
func TestParseBool(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want *bool
	}{
		// truthy
		{name: "true", in: "true", want: boolPtr(true)},
		{name: "enabled", in: "enabled", want: boolPtr(true)},
		{name: "on", in: "on", want: boolPtr(true)},
		{name: "1", in: "1", want: boolPtr(true)},
		{name: "TRUE (case insensitive)", in: "TRUE", want: boolPtr(true)},
		{name: "On (case insensitive)", in: "On", want: boolPtr(true)},
		// falsy
		{name: "false", in: "false", want: boolPtr(false)},
		{name: "disabled", in: "disabled", want: boolPtr(false)},
		{name: "off", in: "off", want: boolPtr(false)},
		{name: "0", in: "0", want: boolPtr(false)},
		{name: "FALSE (case insensitive)", in: "FALSE", want: boolPtr(false)},
		// unknown
		{name: "yes is NOT recognised", in: "yes", want: nil},
		{name: "no is NOT recognised", in: "no", want: nil},
		{name: "empty input", in: "", want: nil},
		{name: "garbage", in: "maybe", want: nil},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := parseBool(tt.in)
			if tt.want == nil {
				assert.Nil(t, got)
				return
			}
			require.NotNil(t, got)
			assert.Equal(t, *tt.want, *got)
		})
	}
}

func intPtr(n int) *int    { return &n }
func boolPtr(b bool) *bool { return &b }
