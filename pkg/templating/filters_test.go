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
	"encoding/base64"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSanitizeStorageName(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  string
	}{
		{
			name:  "simple domain with extension",
			input: "example.com.pem",
			want:  "example_com.pem",
		},
		{
			name:  "subdomain with extension",
			input: "sub.example.com.pem",
			want:  "sub_example_com.pem",
		},
		{
			name:  "namespace_secret pattern (no dots in name)",
			input: "keycloak_keycloak-tls.pem",
			want:  "keycloak_keycloak-tls.pem",
		},
		{
			name:  "namespace_secret.domain pattern",
			input: "keycloak_sso.example.com-tls.pem",
			want:  "keycloak_sso_example_com-tls.pem",
		},
		{
			name:  "multiple dots - last is treated as extension",
			input: "no.extension.here",
			want:  "no_extension.here", // .here is the extension, only basename dots replaced
		},
		{
			name:  "no dots - unchanged",
			input: "nodots.pem",
			want:  "nodots.pem",
		},
		{
			name:  "crt-list file with domain",
			input: "example.com.crtlist",
			want:  "example_com.crtlist",
		},
		{
			name:  "empty string",
			input: "",
			want:  "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := sanitizeStorageName(tt.input)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestPathResolver_GetPath(t *testing.T) {
	resolver := &PathResolver{
		MapsDir:    "/etc/haproxy/maps",
		SSLDir:     "/etc/haproxy/ssl",
		CRTListDir: "/etc/haproxy/general", // CRT-list files stored in general directory to avoid reload
		GeneralDir: "/etc/haproxy/general",
	}

	tests := []struct {
		name     string
		filename any
		args     []any
		want     string
		wantErr  bool
	}{
		{
			name:     "map file",
			filename: "host.map",
			args:     []any{"map"},
			want:     "/etc/haproxy/maps/host.map",
		},
		{
			name:     "general file",
			filename: "503.http",
			args:     []any{"file"},
			want:     "/etc/haproxy/general/503.http",
		},
		{
			name:     "ssl certificate",
			filename: "cert.pem",
			args:     []any{"cert"},
			want:     "/etc/haproxy/ssl/cert.pem",
		},
		{
			name:     "crt-list file",
			filename: "certificate-list.txt",
			args:     []any{"crt-list"},
			want:     "/etc/haproxy/general/certificate-list.txt",
		},
		// Sanitization tests for SSL certificates
		{
			name:     "ssl certificate with domain dots - sanitized",
			filename: "example.com.pem",
			args:     []any{"cert"},
			want:     "/etc/haproxy/ssl/example_com.pem",
		},
		{
			name:     "ssl certificate with subdomain - sanitized",
			filename: "sub.example.com.pem",
			args:     []any{"cert"},
			want:     "/etc/haproxy/ssl/sub_example_com.pem",
		},
		{
			name:     "ssl certificate production pattern - sanitized",
			filename: "keycloak_sso.example.com-tls.pem",
			args:     []any{"cert"},
			want:     "/etc/haproxy/ssl/keycloak_sso_example_com-tls.pem",
		},
		// Sanitization tests for CRT-list files
		{
			name:     "crt-list with domain dots - sanitized",
			filename: "example.com.crtlist",
			args:     []any{"crt-list"},
			want:     "/etc/haproxy/general/example_com.crtlist",
		},
		// Map files should NOT be sanitized
		{
			name:     "map file with dots - NOT sanitized",
			filename: "domain.map",
			args:     []any{"map"},
			want:     "/etc/haproxy/maps/domain.map",
		},
		{
			name:     "map file with multiple dots - NOT sanitized",
			filename: "sub.domain.com.map",
			args:     []any{"map"},
			want:     "/etc/haproxy/maps/sub.domain.com.map",
		},
		// General files should NOT be sanitized
		{
			name:     "general file with dots - NOT sanitized",
			filename: "error.page.http",
			args:     []any{"file"},
			want:     "/etc/haproxy/general/error.page.http",
		},
		{
			name:     "empty filename returns directory",
			filename: "",
			args:     []any{"map"},
			want:     "/etc/haproxy/maps",
		},
		{
			name:     "non-string filename",
			filename: 123,
			args:     []any{"map"},
			wantErr:  true,
		},
		{
			name:     "missing file type arg",
			filename: "test.map",
			args:     []any{},
			wantErr:  true,
		},
		{
			name:     "invalid file type",
			filename: "test.txt",
			args:     []any{"invalid"},
			wantErr:  true,
		},
		{
			name:     "non-string file type",
			filename: "test.map",
			args:     []any{123},
			wantErr:  true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// GetPath expects all arguments in a single variadic call
			args := []any{tt.filename}
			args = append(args, tt.args...)
			got, err := resolver.GetPath(args...)

			if tt.wantErr {
				require.Error(t, err)
				return
			}

			require.NoError(t, err)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestGlobMatch(t *testing.T) {
	tests := []struct {
		name    string
		input   any
		pattern string
		want    []any
		wantErr bool
	}{
		{
			name:    "simple wildcard match",
			input:   []any{"backend-annotation-auth", "backend-annotation-rate-limit", "frontend-config"},
			pattern: "backend-annotation-*",
			want:    []any{"backend-annotation-auth", "backend-annotation-rate-limit"},
		},
		{
			name:    "no matches",
			input:   []any{"frontend-config", "global-config"},
			pattern: "backend-*",
			want:    nil,
		},
		{
			name:    "question mark wildcard",
			input:   []any{"test1", "test2", "test10", "prod1"},
			pattern: "test?",
			want:    []any{"test1", "test2"},
		},
		{
			name:    "exact match",
			input:   []any{"exact", "exact-match", "not-exact"},
			pattern: "exact",
			want:    []any{"exact"},
		},
		{
			name:    "all match",
			input:   []any{"one", "two", "three"},
			pattern: "*",
			want:    []any{"one", "two", "three"},
		},
		{
			name:    "empty list",
			input:   []any{},
			pattern: "*",
			want:    nil,
		},
		{
			name:    "string slice input",
			input:   []string{"backend-annotation-auth", "backend-annotation-rate-limit"},
			pattern: "backend-*",
			want:    []any{"backend-annotation-auth", "backend-annotation-rate-limit"},
		},
		{
			name:    "mixed types in list - skips non-strings",
			input:   []any{"valid", 123, "another-valid", true},
			pattern: "*valid",
			want:    []any{"valid", "another-valid"},
		},
		{
			name:    "non-list input",
			input:   "not-a-list",
			pattern: "*",
			wantErr: true,
		},
		{
			name:    "missing pattern argument",
			input:   []any{"test"},
			pattern: "",
			wantErr: true,
		},
		{
			name:    "invalid glob pattern",
			input:   []any{"test"},
			pattern: "[invalid",
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var args []any
			if tt.pattern != "" {
				args = []any{tt.pattern}
			}

			got, err := GlobMatch(tt.input, args...)

			if tt.wantErr {
				require.Error(t, err)
				return
			}

			require.NoError(t, err)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestB64Decode(t *testing.T) {
	tests := []struct {
		name    string
		input   any
		want    string
		wantErr bool
	}{
		{
			name:  "simple string",
			input: base64.StdEncoding.EncodeToString([]byte("Hello, World!")),
			want:  "Hello, World!",
		},
		{
			name:  "empty string",
			input: base64.StdEncoding.EncodeToString([]byte("")),
			want:  "",
		},
		{
			name:  "special characters",
			input: base64.StdEncoding.EncodeToString([]byte("user:password!@#$%")),
			want:  "user:password!@#$%",
		},
		{
			name:  "multiline",
			input: base64.StdEncoding.EncodeToString([]byte("line1\nline2\nline3")),
			want:  "line1\nline2\nline3",
		},
		{
			name:  "encrypted password (HAProxy userlist format)",
			input: base64.StdEncoding.EncodeToString([]byte("$5$rounds=5000$salt$hashedpassword")),
			want:  "$5$rounds=5000$salt$hashedpassword",
		},
		{
			name:    "non-string input (converted to string, then decoded)",
			input:   123,
			wantErr: true, // "123" is not valid base64
		},
		{
			name:    "invalid base64",
			input:   "not-valid-base64!!!",
			wantErr: true,
		},
		{
			name:  "nil input (converts to empty string)",
			input: nil,
			want:  "", // nil → "" → b64decode("") → ""
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := B64Decode(tt.input)

			if tt.wantErr {
				require.Error(t, err)
				return
			}

			require.NoError(t, err)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestStrip(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  string
	}{
		{
			name:  "leading and trailing spaces",
			input: "  hello world  ",
			want:  "hello world",
		},
		{
			name:  "tabs and newlines",
			input: "\t\nhello\n\t",
			want:  "hello",
		},
		{
			name:  "no whitespace",
			input: "already-trimmed",
			want:  "already-trimmed",
		},
		{
			name:  "empty string",
			input: "",
			want:  "",
		},
		{
			name:  "only whitespace",
			input: "   \t\n   ",
			want:  "",
		},
		{
			name:  "internal whitespace preserved",
			input: "  hello   world  ",
			want:  "hello   world",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := Strip(tt.input)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestDebug(t *testing.T) {
	tests := []struct {
		name         string
		value        any
		label        string
		wantContains []string
	}{
		{
			name: "simple object without label",
			value: map[string]any{
				"key": "value",
			},
			label: "",
			wantContains: []string{
				"# DEBUG:",
				`#   "key": "value"`,
			},
		},
		{
			name: "simple object with label",
			value: map[string]any{
				"name": "test",
			},
			label: "my-label",
			wantContains: []string{
				"# DEBUG my-label:",
				`#   "name": "test"`,
			},
		},
		{
			name:  "array value",
			value: []string{"a", "b", "c"},
			label: "",
			wantContains: []string{
				"# DEBUG:",
				`#   "a"`,
				`#   "b"`,
				`#   "c"`,
			},
		},
		{
			name:  "nested structure",
			value: map[string]any{"outer": map[string]any{"inner": "value"}},
			label: "nested",
			wantContains: []string{
				"# DEBUG nested:",
				`#   "outer"`,
				`#     "inner": "value"`,
			},
		},
		{
			name:  "nil value",
			value: nil,
			label: "",
			wantContains: []string{
				"# DEBUG:",
				"# null",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := Debug(tt.value, tt.label)
			for _, want := range tt.wantContains {
				assert.Contains(t, got, want)
			}
		})
	}
}
