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

package testrunner

import (
	"log/slog"
	"os"
	"testing"

	"github.com/stretchr/testify/assert"

	"gitlab.com/haproxy-haptic/haptic/pkg/core/config"
	"gitlab.com/haproxy-haptic/haptic/pkg/dataplane"
	"gitlab.com/haproxy-haptic/haptic/pkg/dataplane/auxiliaryfiles"
)

// createTestRunner creates a Runner for testing assertions.
func createTestRunner(t *testing.T) *Runner {
	t.Helper()
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))
	return &Runner{
		logger: logger,
	}
}

func TestRunner_AssertNotContains(t *testing.T) {
	runner := createTestRunner(t)

	tests := []struct {
		name       string
		content    string
		pattern    string
		wantPassed bool
		wantErr    string
	}{
		{
			name:       "pattern not in content - passes",
			content:    "hello world",
			pattern:    "foobar",
			wantPassed: true,
		},
		{
			name:       "pattern in content - fails",
			content:    "hello world",
			pattern:    "world",
			wantPassed: false,
			wantErr:    `pattern "world" unexpectedly found`,
		},
		{
			name:       "regex pattern not in content - passes",
			content:    "hello world",
			pattern:    `\d+`,
			wantPassed: true,
		},
		{
			name:       "regex pattern in content - fails",
			content:    "hello 123 world",
			pattern:    `\d+`,
			wantPassed: false,
			wantErr:    "unexpectedly found",
		},
		{
			name:       "invalid regex - fails",
			content:    "hello world",
			pattern:    "[invalid",
			wantPassed: false,
			wantErr:    "invalid regex pattern",
		},
		{
			name:       "empty pattern - passes (matches everything)",
			content:    "hello",
			pattern:    "",
			wantPassed: false, // Empty pattern matches at position 0
			wantErr:    "unexpectedly found",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assertion := &config.ValidationAssertion{
				Type:    "not_contains",
				Target:  "haproxy.cfg",
				Pattern: tt.pattern,
			}

			result := runner.assertNotContains(tt.content, nil, assertion, "")

			assert.Equal(t, tt.wantPassed, result.Passed)
			assert.Equal(t, "not_contains", result.Type)
			if tt.wantErr != "" {
				assert.Contains(t, result.Error, tt.wantErr)
			}
		})
	}
}

func TestRunner_AssertNotContains_WithDescription(t *testing.T) {
	runner := createTestRunner(t)

	assertion := &config.ValidationAssertion{
		Type:        "not_contains",
		Target:      "haproxy.cfg",
		Pattern:     "forbidden",
		Description: "Config must not contain forbidden pattern",
	}

	result := runner.assertNotContains("hello world", nil, assertion, "")

	assert.True(t, result.Passed)
	assert.Equal(t, "Config must not contain forbidden pattern", result.Description)
}

func TestRunner_AssertMatchCount(t *testing.T) {
	runner := createTestRunner(t)

	tests := []struct {
		name       string
		content    string
		pattern    string
		expected   string
		wantPassed bool
		wantErr    string
	}{
		{
			name:       "exact match count - passes",
			content:    "foo bar foo baz foo",
			pattern:    "foo",
			expected:   "3",
			wantPassed: true,
		},
		{
			name:       "match count too few - fails",
			content:    "foo bar foo",
			pattern:    "foo",
			expected:   "5",
			wantPassed: false,
			wantErr:    "expected 5 matches, got 2",
		},
		{
			name:       "match count too many - fails",
			content:    "foo foo foo foo",
			pattern:    "foo",
			expected:   "2",
			wantPassed: false,
			wantErr:    "expected 2 matches, got 4",
		},
		{
			name:       "zero matches expected - passes",
			content:    "hello world",
			pattern:    "foo",
			expected:   "0",
			wantPassed: true,
		},
		{
			name:       "regex pattern count - passes",
			content:    "abc123def456ghi789",
			pattern:    `\d+`,
			expected:   "3",
			wantPassed: true,
		},
		{
			name:       "invalid expected count - fails",
			content:    "hello",
			pattern:    "hello",
			expected:   "not-a-number",
			wantPassed: false,
			wantErr:    "invalid expected count",
		},
		{
			name:       "invalid regex pattern - fails",
			content:    "hello",
			pattern:    "[invalid",
			expected:   "1",
			wantPassed: false,
			wantErr:    "invalid regex pattern",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assertion := &config.ValidationAssertion{
				Type:     "match_count",
				Target:   "haproxy.cfg",
				Pattern:  tt.pattern,
				Expected: tt.expected,
			}

			result := runner.assertMatchCount(tt.content, nil, assertion, "")

			assert.Equal(t, tt.wantPassed, result.Passed)
			assert.Equal(t, "match_count", result.Type)
			if tt.wantErr != "" {
				assert.Contains(t, result.Error, tt.wantErr)
			}
		})
	}
}

func TestRunner_AssertEquals(t *testing.T) {
	runner := createTestRunner(t)

	tests := []struct {
		name       string
		content    string
		expected   string
		wantPassed bool
		wantErr    string
	}{
		{
			name:       "exact match - passes",
			content:    "hello world",
			expected:   "hello world",
			wantPassed: true,
		},
		{
			name:       "different content - fails",
			content:    "hello world",
			expected:   "goodbye world",
			wantPassed: false,
			wantErr:    `expected "goodbye world", got "hello world"`,
		},
		{
			name:       "empty strings - passes",
			content:    "",
			expected:   "",
			wantPassed: true,
		},
		{
			name:       "whitespace difference - fails",
			content:    "hello world",
			expected:   "hello  world",
			wantPassed: false,
		},
		{
			name:       "case sensitive - fails",
			content:    "Hello World",
			expected:   "hello world",
			wantPassed: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assertion := &config.ValidationAssertion{
				Type:     "equals",
				Target:   "haproxy.cfg",
				Expected: tt.expected,
			}

			result := runner.assertEquals(tt.content, nil, assertion, "")

			assert.Equal(t, tt.wantPassed, result.Passed)
			assert.Equal(t, "equals", result.Type)
			if tt.wantErr != "" {
				assert.Contains(t, result.Error, tt.wantErr)
			}
		})
	}
}

func TestRunner_AssertEquals_TruncatesLongValues(t *testing.T) {
	runner := createTestRunner(t)

	// Create long strings that exceed 100 chars
	longContent := "A" + string(make([]byte, 150))
	longExpected := "B" + string(make([]byte, 150))

	assertion := &config.ValidationAssertion{
		Type:     "equals",
		Target:   "haproxy.cfg",
		Expected: longExpected,
	}

	result := runner.assertEquals(longContent, nil, assertion, "")

	assert.False(t, result.Passed)
	assert.Contains(t, result.Error, "Use --verbose for full preview")
}

func TestRunner_AssertJSONPath(t *testing.T) {
	runner := createTestRunner(t)

	tests := []struct {
		name       string
		context    map[string]interface{}
		jsonpath   string
		expected   string
		wantPassed bool
		wantErr    string
	}{
		{
			name: "simple field access - passes",
			context: map[string]interface{}{
				"name": "test-service",
			},
			jsonpath:   "{.name}",
			expected:   "test-service",
			wantPassed: true,
		},
		{
			name: "nested field access - passes",
			context: map[string]interface{}{
				"metadata": map[string]interface{}{
					"name":      "my-pod",
					"namespace": "default",
				},
			},
			jsonpath:   "{.metadata.name}",
			expected:   "my-pod",
			wantPassed: true,
		},
		{
			name: "value mismatch - fails",
			context: map[string]interface{}{
				"status": "running",
			},
			jsonpath:   "{.status}",
			expected:   "pending",
			wantPassed: false,
			wantErr:    `expected "pending", got "running"`,
		},
		{
			name: "field not found - fails",
			context: map[string]interface{}{
				"name": "test",
			},
			jsonpath:   "{.missing}",
			expected:   "value",
			wantPassed: false,
			wantErr:    "is not found",
		},
		{
			name:       "invalid jsonpath syntax - fails",
			context:    map[string]interface{}{},
			jsonpath:   "{invalid",
			expected:   "",
			wantPassed: false,
			wantErr:    "invalid JSONPath expression",
		},
		{
			name: "array index access - passes",
			context: map[string]interface{}{
				"items": []interface{}{"first", "second", "third"},
			},
			jsonpath:   "{.items[1]}",
			expected:   "second",
			wantPassed: true,
		},
		{
			name: "no expected value - passes if path exists",
			context: map[string]interface{}{
				"exists": "yes",
			},
			jsonpath:   "{.exists}",
			expected:   "", // Empty expected means just check if path exists
			wantPassed: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assertion := &config.ValidationAssertion{
				Type:     "jsonpath",
				JSONPath: tt.jsonpath,
				Expected: tt.expected,
			}

			result := runner.assertJSONPath(tt.context, assertion)

			assert.Equal(t, tt.wantPassed, result.Passed, "result.Passed mismatch")
			assert.Equal(t, "jsonpath", result.Type)
			if tt.wantErr != "" {
				assert.Contains(t, result.Error, tt.wantErr)
			}
		})
	}
}

func TestRunner_AssertMatchOrder(t *testing.T) {
	runner := createTestRunner(t)

	tests := []struct {
		name       string
		content    string
		patterns   []string
		wantPassed bool
		wantErr    string
	}{
		{
			name:       "patterns in order - passes",
			content:    "first second third",
			patterns:   []string{"first", "second", "third"},
			wantPassed: true,
		},
		{
			name:       "patterns out of order - fails",
			content:    "first third second",
			patterns:   []string{"first", "second", "third"},
			wantPassed: false,
			wantErr:    "patterns out of order",
		},
		{
			name:       "pattern not found - fails",
			content:    "first second",
			patterns:   []string{"first", "missing", "second"},
			wantPassed: false,
			wantErr:    "not found",
		},
		{
			name:       "empty patterns - fails",
			content:    "any content",
			patterns:   []string{},
			wantPassed: false,
			wantErr:    "no patterns specified",
		},
		{
			name:       "regex patterns in order - passes",
			content:    "backend_foo backend_bar backend_baz",
			patterns:   []string{`backend_foo`, `backend_bar`, `backend_baz`},
			wantPassed: true,
		},
		{
			name:       "invalid regex pattern - fails",
			content:    "content",
			patterns:   []string{"valid", "[invalid"},
			wantPassed: false,
			wantErr:    "invalid regex pattern",
		},
		{
			name:       "single pattern - passes",
			content:    "hello world",
			patterns:   []string{"hello"},
			wantPassed: true,
		},
		{
			name:       "same pattern twice in order - passes",
			content:    "foo bar foo",
			patterns:   []string{"foo", "bar"},
			wantPassed: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assertion := &config.ValidationAssertion{
				Type:     "match_order",
				Target:   "haproxy.cfg",
				Patterns: tt.patterns,
			}

			result := runner.assertMatchOrder(tt.content, nil, assertion, "")

			assert.Equal(t, tt.wantPassed, result.Passed)
			assert.Equal(t, "match_order", result.Type)
			if tt.wantErr != "" {
				assert.Contains(t, result.Error, tt.wantErr)
			}
		})
	}
}

func TestRunner_FindGeneralFile(t *testing.T) {
	runner := createTestRunner(t)

	tests := []struct {
		name     string
		fileName string
		files    *dataplane.AuxiliaryFiles
		want     string
	}{
		{
			name:     "file found",
			fileName: "error.http",
			files: &dataplane.AuxiliaryFiles{
				GeneralFiles: []auxiliaryfiles.GeneralFile{
					{Filename: "error.http", Content: "HTTP/1.0 503 Service Unavailable"},
				},
			},
			want: "HTTP/1.0 503 Service Unavailable",
		},
		{
			name:     "file not found",
			fileName: "missing.http",
			files: &dataplane.AuxiliaryFiles{
				GeneralFiles: []auxiliaryfiles.GeneralFile{
					{Filename: "error.http", Content: "content"},
				},
			},
			want: "",
		},
		{
			name:     "nil auxiliary files",
			fileName: "any.http",
			files:    nil,
			want:     "",
		},
		{
			name:     "empty general files",
			fileName: "error.http",
			files: &dataplane.AuxiliaryFiles{
				GeneralFiles: []auxiliaryfiles.GeneralFile{},
			},
			want: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := runner.findGeneralFile(tt.fileName, tt.files)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestRunner_FindCertificate(t *testing.T) {
	runner := createTestRunner(t)

	tests := []struct {
		name     string
		certName string
		files    *dataplane.AuxiliaryFiles
		want     string
	}{
		{
			name:     "certificate found by basename",
			certName: "server.pem",
			files: &dataplane.AuxiliaryFiles{
				SSLCertificates: []auxiliaryfiles.SSLCertificate{
					{Path: "/etc/haproxy/ssl/server.pem", Content: "-----BEGIN CERTIFICATE-----"},
				},
			},
			want: "-----BEGIN CERTIFICATE-----",
		},
		{
			name:     "certificate not found",
			certName: "missing.pem",
			files: &dataplane.AuxiliaryFiles{
				SSLCertificates: []auxiliaryfiles.SSLCertificate{
					{Path: "/etc/haproxy/ssl/server.pem", Content: "content"},
				},
			},
			want: "",
		},
		{
			name:     "nil auxiliary files",
			certName: "any.pem",
			files:    nil,
			want:     "",
		},
		{
			name:     "matches basename only",
			certName: "cert.pem",
			files: &dataplane.AuxiliaryFiles{
				SSLCertificates: []auxiliaryfiles.SSLCertificate{
					{Path: "/deep/nested/path/cert.pem", Content: "found"},
				},
			},
			want: "found",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := runner.findCertificate(tt.certName, tt.files)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestRunner_FindCRTListFile(t *testing.T) {
	runner := createTestRunner(t)

	tests := []struct {
		name        string
		crtListName string
		files       *dataplane.AuxiliaryFiles
		want        string
	}{
		{
			name:        "crt-list found by basename",
			crtListName: "certificates.txt",
			files: &dataplane.AuxiliaryFiles{
				CRTListFiles: []auxiliaryfiles.CRTListFile{
					{Path: "/etc/haproxy/ssl/certificates.txt", Content: "/etc/haproxy/ssl/cert1.pem"},
				},
			},
			want: "/etc/haproxy/ssl/cert1.pem",
		},
		{
			name:        "crt-list not found",
			crtListName: "missing.txt",
			files: &dataplane.AuxiliaryFiles{
				CRTListFiles: []auxiliaryfiles.CRTListFile{
					{Path: "/etc/haproxy/ssl/certs.txt", Content: "content"},
				},
			},
			want: "",
		},
		{
			name:        "nil auxiliary files",
			crtListName: "any.txt",
			files:       nil,
			want:        "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := runner.findCRTListFile(tt.crtListName, tt.files)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestRunner_ResolveTarget(t *testing.T) {
	runner := createTestRunner(t)

	haproxyConfig := "global\n  maxconn 1000"
	renderError := "template error occurred"
	auxFiles := &dataplane.AuxiliaryFiles{
		MapFiles: []auxiliaryfiles.MapFile{
			{Path: "backends.map", Content: "example.com backend1"},
		},
		GeneralFiles: []auxiliaryfiles.GeneralFile{
			{Filename: "error.http", Content: "HTTP/1.0 503"},
		},
		SSLCertificates: []auxiliaryfiles.SSLCertificate{
			{Path: "/ssl/cert.pem", Content: "CERT"},
		},
		CRTListFiles: []auxiliaryfiles.CRTListFile{
			{Path: "/ssl/list.txt", Content: "LIST"},
		},
	}

	tests := []struct {
		name   string
		target string
		want   string
	}{
		{
			name:   "haproxy.cfg target",
			target: "haproxy.cfg",
			want:   haproxyConfig,
		},
		{
			name:   "empty target defaults to haproxy.cfg",
			target: "",
			want:   haproxyConfig,
		},
		{
			name:   "rendering_error target",
			target: "rendering_error",
			want:   renderError,
		},
		{
			name:   "map target",
			target: "map:backends.map",
			want:   "example.com backend1",
		},
		{
			name:   "file target",
			target: "file:error.http",
			want:   "HTTP/1.0 503",
		},
		{
			name:   "cert target",
			target: "cert:cert.pem",
			want:   "CERT",
		},
		{
			name:   "crt-list target",
			target: "crt-list:list.txt",
			want:   "LIST",
		},
		{
			name:   "unknown target defaults to haproxy.cfg",
			target: "unknown:something",
			want:   haproxyConfig,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := runner.resolveTarget(tt.target, haproxyConfig, auxFiles, renderError)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestRunner_ResolveAuxiliaryFile_NilFiles(t *testing.T) {
	runner := createTestRunner(t)

	// Should return empty string for all target types when auxiliaryFiles is nil
	targets := []string{"map:test.map", "file:test.http", "cert:test.pem", "crt-list:test.txt"}

	for _, target := range targets {
		t.Run(target, func(t *testing.T) {
			got := runner.resolveAuxiliaryFile(target, nil)
			assert.Equal(t, "", got)
		})
	}
}

func TestTruncateString(t *testing.T) {
	tests := []struct {
		name   string
		input  string
		maxLen int
		want   string
	}{
		{
			name:   "string shorter than max - unchanged",
			input:  "hello",
			maxLen: 10,
			want:   "hello",
		},
		{
			name:   "string at max length - unchanged",
			input:  "hello",
			maxLen: 5,
			want:   "hello",
		},
		{
			name:   "string longer than max - truncated",
			input:  "hello world",
			maxLen: 5,
			want:   "hello...",
		},
		{
			name:   "empty string - unchanged",
			input:  "",
			maxLen: 10,
			want:   "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := truncateString(tt.input, tt.maxLen)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestRunner_PopulateTargetMetadata(t *testing.T) {
	runner := createTestRunner(t)

	tests := []struct {
		name          string
		target        string
		targetName    string
		hasFailed     bool
		wantPreview   bool
		wantTargetSet bool
	}{
		{
			name:          "failed assertion - has preview",
			target:        "hello world content",
			targetName:    "haproxy.cfg",
			hasFailed:     true,
			wantPreview:   true,
			wantTargetSet: true,
		},
		{
			name:          "passed assertion - no preview",
			target:        "hello world content",
			targetName:    "haproxy.cfg",
			hasFailed:     false,
			wantPreview:   false,
			wantTargetSet: true,
		},
		{
			name:          "empty target - no preview even if failed",
			target:        "",
			targetName:    "map:test.map",
			hasFailed:     true,
			wantPreview:   false,
			wantTargetSet: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := &AssertionResult{}
			runner.populateTargetMetadata(result, tt.target, tt.targetName, tt.hasFailed)

			assert.Equal(t, tt.targetName, result.Target)
			assert.Equal(t, len(tt.target), result.TargetSize)

			if tt.wantPreview {
				assert.NotEmpty(t, result.TargetPreview)
			} else {
				assert.Empty(t, result.TargetPreview)
			}
		})
	}
}
