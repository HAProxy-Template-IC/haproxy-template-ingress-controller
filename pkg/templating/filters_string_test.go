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

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestScriggoStringsSplit(t *testing.T) {
	tests := []struct {
		name string
		s    any
		sep  any
		want []string
	}{
		{name: "simple split", s: "a,b,c", sep: ",", want: []string{"a", "b", "c"}},
		{name: "single element when no separator", s: "abc", sep: ",", want: []string{"abc"}},
		{name: "split path", s: "/api/v1/users", sep: "/", want: []string{"", "api", "v1", "users"}},
		{name: "empty string yields one empty element", s: "", sep: ",", want: []string{""}},
		{name: "non-string input is converted", s: 123, sep: "2", want: []string{"1", "3"}},
		{name: "non-string sep is converted", s: "a1b1c", sep: 1, want: []string{"a", "b", "c"}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, scriggoStringsSplit(tt.s, tt.sep))
		})
	}
}

func TestScriggoStringsTrim_TrimSpace(t *testing.T) {
	// Both functions are aliases for strings.TrimSpace via scriggoToString.
	tests := []struct {
		name string
		in   any
		want string
	}{
		{name: "leading whitespace", in: "  hello", want: "hello"},
		{name: "trailing whitespace", in: "hello  ", want: "hello"},
		{name: "both sides", in: "  hello  ", want: "hello"},
		{name: "tabs and newlines", in: "\t\nhello\r\n", want: "hello"},
		{name: "no whitespace unchanged", in: "hello", want: "hello"},
		{name: "all-whitespace becomes empty", in: "   ", want: ""},
		{name: "non-string converted first", in: 42, want: "42"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got1 := scriggoStringsTrim(tt.in)
			got2 := scriggoTrimSpace(tt.in)
			assert.Equal(t, tt.want, got1, "scriggoStringsTrim")
			assert.Equal(t, tt.want, got2, "scriggoTrimSpace")
		})
	}
}

func TestScriggoStringsLower(t *testing.T) {
	tests := []struct {
		name string
		in   any
		want string
	}{
		{name: "uppercase to lower", in: "HELLO", want: "hello"},
		{name: "mixed case", in: "Hello World", want: "hello world"},
		{name: "already lower", in: "abc", want: "abc"},
		{name: "non-string converted", in: 42, want: "42"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, scriggoStringsLower(tt.in))
		})
	}
}

func TestScriggoStringsSplitN(t *testing.T) {
	tests := []struct {
		name string
		s    any
		sep  any
		n    int
		want []string
	}{
		{name: "split into 2", s: "a,b,c,d", sep: ",", n: 2, want: []string{"a", "b,c,d"}},
		{name: "split unlimited (n<0)", s: "a,b,c", sep: ",", n: -1, want: []string{"a", "b", "c"}},
		{name: "split into 0 returns nil", s: "a,b,c", sep: ",", n: 0, want: nil},
		{name: "n larger than count", s: "a,b", sep: ",", n: 10, want: []string{"a", "b"}},
		{name: "n=1 returns whole string", s: "a,b,c", sep: ",", n: 1, want: []string{"a,b,c"}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, scriggoStringsSplitN(tt.s, tt.sep, tt.n))
		})
	}
}

func TestScriggoTitle(t *testing.T) {
	tests := []struct {
		name string
		in   any
		want string
	}{
		{name: "lowercase to title", in: "hello world", want: "Hello World"},
		{name: "already title", in: "Hello World", want: "Hello World"},
		{name: "mixed case", in: "hELlO wORlD", want: "Hello World"},
		{name: "single word", in: "ingress", want: "Ingress"},
		{name: "empty string", in: "", want: ""},
		// Word-boundary semantics from x/text Unicode segmentation: an internal
		// apostrophe and a digit run stay inside the word (they are NOT word
		// boundaries), so the rune after them is not re-capitalised.
		{name: "internal apostrophe", in: "don't", want: "Don't"},
		{name: "internal digits", in: "abc2def", want: "Abc2def"},
		// Hyphen IS a word boundary.
		{name: "hyphenated", in: "config-maps", want: "Config-Maps"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, scriggoTitle(tt.in))
		})
	}
}

func TestScriggoSanitizeRegex(t *testing.T) {
	tests := []struct {
		name string
		in   any
		want string
	}{
		{name: "no special chars", in: "abc", want: "abc"},
		{name: "special chars escaped", in: "a.b*c", want: `a\.b\*c`},
		{name: "path with slashes", in: "/api/v1", want: "/api/v1"},
		{name: "all metacharacters", in: `.+*?()[]{}|^$\`, want: `\.\+\*\?\(\)\[\]\{\}\|\^\$\\`},
		{name: "empty string", in: "", want: ""},
		{name: "non-string converted", in: 42, want: "42"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, scriggoSanitizeRegex(tt.in))
		})
	}
}

func TestScriggoIsDigit(t *testing.T) {
	tests := []struct {
		name string
		in   any
		want bool
	}{
		{name: "all digits", in: "12345", want: true},
		{name: "single digit", in: "5", want: true},
		{name: "empty string", in: "", want: false},
		{name: "contains letter", in: "12a45", want: false},
		{name: "contains symbol", in: "1-2", want: false},
		{name: "leading whitespace", in: " 123", want: false},
		{name: "negative number is not all digits", in: "-123", want: false},
		{name: "decimal", in: "1.23", want: false},
		{name: "non-string converted to number string is digits", in: 42, want: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, scriggoIsDigit(tt.in))
		})
	}
}

func TestScriggoBasename(t *testing.T) {
	tests := []struct {
		name string
		path string
		want string
	}{
		{name: "absolute path", path: "/etc/haproxy/ssl/cert.pem", want: "cert.pem"},
		{name: "relative path", path: "ssl/cert.pem", want: "cert.pem"},
		{name: "trailing slash", path: "/etc/haproxy/", want: "haproxy"},
		{name: "filename only", path: "cert.pem", want: "cert.pem"},
		{name: "just slash", path: "/", want: "/"},
		{name: "empty string", path: "", want: "."},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, scriggoBasename(tt.path))
		})
	}
}

func TestParseIndentWidth(t *testing.T) {
	tests := []struct {
		name    string
		args    []any
		want    string
		wantErr bool
	}{
		{name: "no args defaults to 4 spaces", args: nil, want: "    "},
		{name: "explicit nil first arg defaults", args: []any{nil}, want: "    "},
		{name: "int width", args: []any{2}, want: "  "},
		{name: "zero width is no indent", args: []any{0}, want: ""},
		{name: "negative width is error", args: []any{-1}, wantErr: true},
		{name: "string prefix", args: []any{">>"}, want: ">>"},
		{name: "empty string prefix", args: []any{""}, want: ""},
		{name: "invalid type is error", args: []any{3.14}, wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := parseIndentWidth(tt.args)
			if tt.wantErr {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestParseIndentBool(t *testing.T) {
	tests := []struct {
		name    string
		args    []any
		index   int
		want    bool
		wantErr bool
	}{
		{name: "missing arg defaults false", args: []any{4}, index: 1, want: false},
		{name: "explicit nil defaults false", args: []any{4, nil}, index: 1, want: false},
		{name: "true value", args: []any{4, true}, index: 1, want: true},
		{name: "false value", args: []any{4, false}, index: 1, want: false},
		{name: "wrong type is error", args: []any{4, "yes"}, index: 1, wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := parseIndentBool(tt.args, tt.index, "first")
			if tt.wantErr {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestApplyIndentation(t *testing.T) {
	tests := []struct {
		name        string
		input       string
		indent      string
		indentFirst bool
		indentBlank bool
		want        string
	}{
		{name: "empty input unchanged", input: "", indent: "  ", want: ""},
		{name: "single line, default skips first", input: "line", indent: "  ", indentFirst: false, want: "line"},
		{name: "single line with first=true", input: "line", indent: "  ", indentFirst: true, want: "  line"},
		{name: "two lines skip first", input: "a\nb", indent: "  ", indentFirst: false, want: "a\n  b"},
		{name: "two lines indent first", input: "a\nb", indent: "  ", indentFirst: true, want: "  a\n  b"},
		{name: "blank line skipped", input: "a\n\nb", indent: "  ", indentFirst: true, want: "  a\n\n  b"},
		{name: "blank line indented", input: "a\n\nb", indent: "  ", indentFirst: true, indentBlank: true, want: "  a\n  \n  b"},
		{name: "trailing newline", input: "a\n", indent: "  ", indentFirst: true, want: "  a\n"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := applyIndentation(tt.input, tt.indent, tt.indentFirst, tt.indentBlank)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestShouldIndentLine(t *testing.T) {
	tests := []struct {
		name        string
		lineIndex   int
		line        string
		indentFirst bool
		indentBlank bool
		want        bool
	}{
		{name: "first line, indentFirst=false", lineIndex: 0, line: "x", want: false},
		{name: "first line, indentFirst=true", lineIndex: 0, line: "x", indentFirst: true, want: true},
		{name: "blank line, indentBlank=false", lineIndex: 1, line: "  ", want: false},
		{name: "blank line, indentBlank=true", lineIndex: 1, line: "  ", indentBlank: true, want: true},
		{name: "non-first non-blank always indents", lineIndex: 1, line: "x", want: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := shouldIndentLine(tt.lineIndex, tt.line, tt.indentFirst, tt.indentBlank)
			assert.Equal(t, tt.want, got)
		})
	}
}
