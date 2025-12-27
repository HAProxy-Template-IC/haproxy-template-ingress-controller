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

// TestSanitizeStorageName tests the filename sanitization for Dataplane API storage.
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
		CRTListDir: "/etc/haproxy/ssl", // CRT-list files stored in SSL directory
		GeneralDir: "/etc/haproxy/general",
	}

	tests := []struct {
		name     string
		filename interface{}
		args     []interface{}
		want     string
		wantErr  bool
	}{
		{
			name:     "map file",
			filename: "host.map",
			args:     []interface{}{"map"},
			want:     "/etc/haproxy/maps/host.map",
		},
		{
			name:     "general file",
			filename: "503.http",
			args:     []interface{}{"file"},
			want:     "/etc/haproxy/general/503.http",
		},
		{
			name:     "ssl certificate",
			filename: "cert.pem",
			args:     []interface{}{"cert"},
			want:     "/etc/haproxy/ssl/cert.pem",
		},
		{
			name:     "crt-list file",
			filename: "certificate-list.txt",
			args:     []interface{}{"crt-list"},
			want:     "/etc/haproxy/ssl/certificate-list.txt",
		},
		// Sanitization tests for SSL certificates
		{
			name:     "ssl certificate with domain dots - sanitized",
			filename: "example.com.pem",
			args:     []interface{}{"cert"},
			want:     "/etc/haproxy/ssl/example_com.pem",
		},
		{
			name:     "ssl certificate with subdomain - sanitized",
			filename: "sub.example.com.pem",
			args:     []interface{}{"cert"},
			want:     "/etc/haproxy/ssl/sub_example_com.pem",
		},
		{
			name:     "ssl certificate production pattern - sanitized",
			filename: "keycloak_sso.example.com-tls.pem",
			args:     []interface{}{"cert"},
			want:     "/etc/haproxy/ssl/keycloak_sso_example_com-tls.pem",
		},
		// Sanitization tests for CRT-list files
		{
			name:     "crt-list with domain dots - sanitized",
			filename: "example.com.crtlist",
			args:     []interface{}{"crt-list"},
			want:     "/etc/haproxy/ssl/example_com.crtlist",
		},
		// Map files should NOT be sanitized
		{
			name:     "map file with dots - NOT sanitized",
			filename: "domain.map",
			args:     []interface{}{"map"},
			want:     "/etc/haproxy/maps/domain.map",
		},
		{
			name:     "map file with multiple dots - NOT sanitized",
			filename: "sub.domain.com.map",
			args:     []interface{}{"map"},
			want:     "/etc/haproxy/maps/sub.domain.com.map",
		},
		// General files should NOT be sanitized
		{
			name:     "general file with dots - NOT sanitized",
			filename: "error.page.http",
			args:     []interface{}{"file"},
			want:     "/etc/haproxy/general/error.page.http",
		},
		{
			name:     "empty filename returns directory",
			filename: "",
			args:     []interface{}{"map"},
			want:     "/etc/haproxy/maps",
		},
		{
			name:     "non-string filename",
			filename: 123,
			args:     []interface{}{"map"},
			wantErr:  true,
		},
		{
			name:     "missing file type arg",
			filename: "test.map",
			args:     []interface{}{},
			wantErr:  true,
		},
		{
			name:     "invalid file type",
			filename: "test.txt",
			args:     []interface{}{"invalid"},
			wantErr:  true,
		},
		{
			name:     "non-string file type",
			filename: "test.map",
			args:     []interface{}{123},
			wantErr:  true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// GetPath expects all arguments in a single variadic call
			args := []interface{}{tt.filename}
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
		input   interface{}
		pattern string
		want    []interface{}
		wantErr bool
	}{
		{
			name:    "simple wildcard match",
			input:   []interface{}{"backend-annotation-auth", "backend-annotation-rate-limit", "frontend-config"},
			pattern: "backend-annotation-*",
			want:    []interface{}{"backend-annotation-auth", "backend-annotation-rate-limit"},
		},
		{
			name:    "no matches",
			input:   []interface{}{"frontend-config", "global-config"},
			pattern: "backend-*",
			want:    nil,
		},
		{
			name:    "question mark wildcard",
			input:   []interface{}{"test1", "test2", "test10", "prod1"},
			pattern: "test?",
			want:    []interface{}{"test1", "test2"},
		},
		{
			name:    "exact match",
			input:   []interface{}{"exact", "exact-match", "not-exact"},
			pattern: "exact",
			want:    []interface{}{"exact"},
		},
		{
			name:    "all match",
			input:   []interface{}{"one", "two", "three"},
			pattern: "*",
			want:    []interface{}{"one", "two", "three"},
		},
		{
			name:    "empty list",
			input:   []interface{}{},
			pattern: "*",
			want:    nil,
		},
		{
			name:    "string slice input",
			input:   []string{"backend-annotation-auth", "backend-annotation-rate-limit"},
			pattern: "backend-*",
			want:    []interface{}{"backend-annotation-auth", "backend-annotation-rate-limit"},
		},
		{
			name:    "mixed types in list - skips non-strings",
			input:   []interface{}{"valid", 123, "another-valid", true},
			pattern: "*valid",
			want:    []interface{}{"valid", "another-valid"},
		},
		{
			name:    "non-list input",
			input:   "not-a-list",
			pattern: "*",
			wantErr: true,
		},
		{
			name:    "missing pattern argument",
			input:   []interface{}{"test"},
			pattern: "",
			wantErr: true,
		},
		{
			name:    "invalid glob pattern",
			input:   []interface{}{"test"},
			pattern: "[invalid",
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var args []interface{}
			if tt.pattern != "" {
				args = []interface{}{tt.pattern}
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
		input   interface{}
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

// TestStrip tests the shared Strip function that removes leading/trailing whitespace.
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

// TestDebug tests the shared Debug function that formats values as JSON comments.
func TestDebug(t *testing.T) {
	tests := []struct {
		name         string
		value        interface{}
		label        string
		wantContains []string
	}{
		{
			name: "simple object without label",
			value: map[string]interface{}{
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
			value: map[string]interface{}{
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
			value: map[string]interface{}{"outer": map[string]interface{}{"inner": "value"}},
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
