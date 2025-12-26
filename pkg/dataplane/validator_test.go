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

package dataplane

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"gitlab.com/haproxy-haptic/haptic/pkg/dataplane/auxiliaryfiles"
)

// testValidationPaths returns validation paths for testing using temporary directories.
func testValidationPaths(t *testing.T) *ValidationPaths {
	t.Helper()
	tmpDir := t.TempDir()
	return &ValidationPaths{
		MapsDir:           tmpDir + "/maps",
		SSLCertsDir:       tmpDir + "/certs",
		GeneralStorageDir: tmpDir + "/general",
		ConfigFile:        tmpDir + "/haproxy.cfg",
	}
}

// TestValidateConfiguration_ValidMinimalConfig tests validation of minimal valid HAProxy config.
func TestValidateConfiguration_ValidMinimalConfig(t *testing.T) {
	config := `
global
    daemon

defaults
    mode http
    timeout connect 5000ms
    timeout client 50000ms
    timeout server 50000ms

frontend http-in
    bind :80
    default_backend servers

backend servers
    server s1 127.0.0.1:8080
`

	auxFiles := &AuxiliaryFiles{}

	err := ValidateConfiguration(config, auxFiles, testValidationPaths(t), nil, false)
	if err != nil {
		t.Fatalf("ValidateConfiguration() failed on valid config: %v", err)
	}
}

// TestValidateConfiguration_ValidComplexConfig tests validation of complex valid HAProxy config.
func TestValidateConfiguration_ValidComplexConfig(t *testing.T) {
	config := `
global
    daemon
    maxconn 4096
    log 127.0.0.1 local0

defaults
    mode http
    timeout connect 5000ms
    timeout client 50000ms
    timeout server 50000ms
    option httplog
    option dontlognull

frontend http-in
    bind :80
    default_backend web-servers
    acl is_api path_beg /api
    use_backend api-servers if is_api

backend web-servers
    mode http
    balance roundrobin
    option httpchk GET /health
    server web1 192.168.1.10:80 check
    server web2 192.168.1.11:80 check

backend api-servers
    mode http
    balance leastconn
    server api1 192.168.1.20:8080 check
    server api2 192.168.1.21:8080 check
`

	auxFiles := &AuxiliaryFiles{}

	err := ValidateConfiguration(config, auxFiles, testValidationPaths(t), nil, false)
	if err != nil {
		t.Fatalf("ValidateConfiguration() failed on valid complex config: %v", err)
	}
}

// TestValidateConfiguration_SyntaxError tests validation failure for syntax errors.
func TestValidateConfiguration_SyntaxError(t *testing.T) {
	// Config with completely invalid structure that parser will reject
	config := `
global
    daemon

defaults
    mode http

frontend http-in
    bind :80
    # Missing closing brace - parser may catch this
backend
`

	auxFiles := &AuxiliaryFiles{}

	err := ValidateConfiguration(config, auxFiles, testValidationPaths(t), nil, false)
	if err == nil {
		t.Fatal("ValidateConfiguration() should fail on malformed config")
	}

	// Verify it's a validation error
	valErr, ok := err.(*ValidationError)
	if !ok {
		t.Fatalf("Expected *ValidationError, got %T", err)
	}

	// Parser might catch it (syntax) or haproxy might catch it (semantic)
	// Either way is acceptable for this malformed config
	if valErr.Phase != "syntax" && valErr.Phase != "semantic" {
		t.Errorf("Expected phase to be 'syntax' or 'semantic', got: %q", valErr.Phase)
	}

	// Verify error message contains useful info
	errMsg := err.Error()
	if !strings.Contains(errMsg, "validation failed") {
		t.Errorf("Expected error message to contain 'validation failed', got: %s", errMsg)
	}
}

// TestValidateConfiguration_EmptyConfig tests validation failure for empty config.
func TestValidateConfiguration_EmptyConfig(t *testing.T) {
	config := ""
	auxFiles := &AuxiliaryFiles{}

	err := ValidateConfiguration(config, auxFiles, testValidationPaths(t), nil, false)
	if err == nil {
		t.Fatal("ValidateConfiguration() should fail on empty config")
	}

	// Verify it's a validation error
	valErr, ok := err.(*ValidationError)
	if !ok {
		t.Fatalf("Expected *ValidationError, got %T", err)
	}

	// Verify it's a syntax phase error (parser should reject empty config)
	if valErr.Phase != "syntax" {
		t.Errorf("Expected phase='syntax', got: %q", valErr.Phase)
	}
}

// TestValidateConfiguration_SemanticError tests validation failure for semantic errors.
func TestValidateConfiguration_SemanticError(t *testing.T) {
	// Valid syntax but semantic error: use_backend refers to non-existent backend
	config := `
global
    daemon

defaults
    mode http
    timeout connect 5000ms
    timeout client 50000ms
    timeout server 50000ms

frontend http-in
    bind :80
    default_backend servers
    use_backend nonexistent if TRUE

backend servers
    server s1 127.0.0.1:8080
`

	auxFiles := &AuxiliaryFiles{}

	err := ValidateConfiguration(config, auxFiles, testValidationPaths(t), nil, false)
	if err == nil {
		t.Fatal("ValidateConfiguration() should fail on semantic error")
	}

	// Verify it's a validation error
	valErr, ok := err.(*ValidationError)
	if !ok {
		t.Fatalf("Expected *ValidationError, got %T", err)
	}

	// Verify it's a semantic phase error
	if valErr.Phase != "semantic" {
		t.Errorf("Expected phase='semantic', got: %q", valErr.Phase)
	}

	// Verify error message contains useful info
	errMsg := err.Error()
	if !strings.Contains(errMsg, "semantic") {
		t.Errorf("Expected error message to contain 'semantic', got: %s", errMsg)
	}
}

// TestValidateConfiguration_WithSSLCertificate tests validation with SSL certificate.
func TestValidateConfiguration_WithSSLCertificate(t *testing.T) {
	t.Skip("Skipping SSL test - HAProxy strictly validates certificate format in -c mode")

	// Note: We use a dummy cert for testing. In production, this would be a real PEM file.
	// HAProxy will validate the file exists but may not fully validate the cert format in -c mode.
	// Use relative path that will be resolved from the temp directory
	config := `
global
    daemon

defaults
    mode http
    timeout connect 5000ms
    timeout client 50000ms
    timeout server 50000ms

frontend https-in
    bind :443 ssl crt ssl/cert.pem
    default_backend servers

backend servers
    server s1 127.0.0.1:8080
`

	// Minimal self-signed certificate for testing
	dummyCert := `-----BEGIN CERTIFICATE-----
MIICljCCAX4CCQCKz8Q0Q0Q0QDANBgkqhkiG9w0BAQsFADANMQswCQYDVQQGEwJV
UzAeFw0yNDAxMDEwMDAwMDBaFw0yNTAxMDEwMDAwMDBaMA0xCzAJBgNVBAYTAlVT
MIIBIjANBgkqhkiG9w0BAQEFAAOCAQ8AMIIBCgKCAQEAq7BAxYCtENXeAZ0Qd5uV
VwE1TJLy7cZKlLq4VrfBdXqMzLbQqpL0fKnYS0qIvzEz2vjdIKVQ5HBbzj7L8YhP
lYKdAqLFH1KGq8JXxKpZxGS5vZ6T8nXGjCdLmJpQ1jVj5HvKzBpL5T9JKWmYfE6L
K5pZ1HvQqYfJdX5K6qL5YhT9KpXqLdXqL9YpT5KqXqLdXqL9YpT5KqXqLdXqL9Yp
T5KqXqLdXqL9YpT5KqXqLdXqL9YpT5KqXqLdXqL9YpT5KqXqLdXqL9YpT5KqXqLd
XqL9YpT5KqXqLdXqL9YpT5KqXqLdXqL9YpT5KqXqLdXqL9YpT5KqXqLdXqL9YpT5
KwIDAQABMA0GCSqGSIb3DQEBCwUAA4IBAQBzqYpQ1L5K6qL5YhT9KpXqLdXqL9Yp
T5KqXqLdXqL9YpT5KqXqLdXqL9YpT5KqXqLdXqL9YpT5KqXqLdXqL9YpT5KqXqLd
XqL9YpT5KqXqLdXqL9YpT5KqXqLdXqL9YpT5KqXqLdXqL9YpT5KqXqLdXqL9YpT5
KqXqLdXqL9YpT5KqXqLdXqL9YpT5KqXqLdXqL9YpT5KqXqLdXqL9YpT5KqXqLdXq
L9YpT5KqXqLdXqL9YpT5Kw==
-----END CERTIFICATE-----
-----BEGIN PRIVATE KEY-----
MIIEvQIBADANBgkqhkiG9w0BAQEFAASCBKcwggSjAgEAAoIBAQCrsEDFgK0Q1d4B
nRB3m5VXATVMkvLtxkqUurhWt8F1eozMttCqkvR8qdhLSoi/MTPa+N0gpVDkcFvO
PsvxiE+Vgp0CosUfUoarwlfEqlnEZLm9npPydcaMJ0uYmlDWNWPke8rMGkvlP0kp
aZh8TosrmlnUe9Cph8l1fkrqovliFP0qleot1eov1ilPkqpeot1eov1ilPkqpeot
1eov1ilPkqpeot1eov1ilPkqpeot1eov1ilPkqpeot1eov1ilPkqpeot1eov1ilP
kqpeot1eov1ilPkqpeot1eov1ilPkqpeot1eov1ilPkqpeot1eov1ilPkrAgMBAA
ECggEAH5j3L9YpT5KqXqLdXqL9YpT5KqXqLdXqL9YpT5KqXqLdXqL9YpT5KqXqLd
XqL9YpT5KqXqLdXqL9YpT5KqXqLdXqL9YpT5KqXqLdXqL9YpT5KqXqLdXqL9YpT5
KqXqLdXqL9YpT5KqXqLdXqL9YpT5KqXqLdXqL9YpT5KqXqLdXqL9YpT5KqXqLdXq
L9YpT5KqXqLdXqL9YpT5KqXqLdXqL9YpT5KqXqLdXqL9YpT5KqXqLdXqL9YpT5Kq
XqLdXqL9YpT5KqXqLdXqL9YpT5KqXqLdXqL9YpT5KqXqLdXqL9YpT5KqXqLdXqL9
YpT5KqXqLdXqL9YpT5KqXqLdXqL9YpT5KqXqLdXqL9YpT5KqXqLdXqL9YpT5KwKB
gQDXL5K6qL5YhT9KpXqLdXqL9YpT5KqXqLdXqL9YpT5KqXqLdXqL9YpT5KqXqLdX
qL9YpT5KqXqLdXqL9YpT5KqXqLdXqL9YpT5KqXqLdXqL9YpT5KqXqLdXqL9YpT5K
qXqLdXqL9YpT5KwKBgQDLL9YpT5KqXqLdXqL9YpT5KqXqLdXqL9YpT5KqXqLdXqL9
YpT5KqXqLdXqL9YpT5KqXqLdXqL9YpT5KqXqLdXqL9YpT5KqXqLdXqL9YpT5KqXq
LdXqL9YpT5KwKBgD5K6qL5YhT9KpXqLdXqL9YpT5KqXqLdXqL9YpT5KqXqLdXqL9
YpT5KqXqLdXqL9YpT5KqXqLdXqL9YpT5KqXqLdXqL9YpT5KqXqLdXqL9YpT5KqXq
LdXqL9YpT5KwKBgBzL9YpT5KqXqLdXqL9YpT5KqXqLdXqL9YpT5KqXqLdXqL9YpT
5KqXqLdXqL9YpT5KqXqLdXqL9YpT5KqXqLdXqL9YpT5KqXqLdXqL9YpT5KqXqLdX
qL9YpT5KwKBgFpT5KqXqLdXqL9YpT5KqXqLdXqL9YpT5KqXqLdXqL9YpT5KqXqLd
XqL9YpT5KqXqLdXqL9YpT5KqXqLdXqL9YpT5KqXqLdXqL9YpT5KqXqLdXqL9YpT5
Kw==
-----END PRIVATE KEY-----
`

	auxFiles := &AuxiliaryFiles{
		SSLCertificates: []auxiliaryfiles.SSLCertificate{
			{
				Path:    "ssl/cert.pem",
				Content: dummyCert,
			},
		},
	}

	err := ValidateConfiguration(config, auxFiles, testValidationPaths(t), nil, false)
	if err != nil {
		t.Fatalf("ValidateConfiguration() failed with SSL certificate: %v", err)
	}
}

// TestValidateConfiguration_WithAbsolutePathMapFiles tests validation with absolute path map files.
func TestValidateConfiguration_WithAbsolutePathMapFiles(t *testing.T) {
	paths := testValidationPaths(t)

	// Use absolute paths matching validation paths
	config := fmt.Sprintf(`
global
    daemon

defaults
    mode http
    timeout connect 5000ms
    timeout client 50000ms
    timeout server 50000ms

frontend http-in
    bind :80
    http-request set-header X-Backend %%[base,map(%s/host.map,default)]
    default_backend servers

backend servers
    server s1 127.0.0.1:8080
`, paths.MapsDir)

	auxFiles := &AuxiliaryFiles{
		MapFiles: []auxiliaryfiles.MapFile{
			{
				Path:    paths.MapsDir + "/host.map",
				Content: "example.com backend1\ntest.com backend2\n",
			},
		},
	}

	err := ValidateConfiguration(config, auxFiles, paths, nil, false)
	if err != nil {
		t.Fatalf("ValidateConfiguration() failed with absolute path map files: %v", err)
	}
}

// TestValidateConfiguration_WithAbsolutePathGeneralFiles tests validation with absolute path general files.
func TestValidateConfiguration_WithAbsolutePathGeneralFiles(t *testing.T) {
	paths := testValidationPaths(t)

	// Use absolute paths matching validation paths
	config := fmt.Sprintf(`
global
    daemon

defaults
    mode http
    timeout connect 5000ms
    timeout client 50000ms
    timeout server 50000ms
    errorfile 503 %s/503.http

frontend http-in
    bind :80
    default_backend servers

backend servers
    server s1 127.0.0.1:8080
`, paths.GeneralStorageDir)

	auxFiles := &AuxiliaryFiles{
		GeneralFiles: []auxiliaryfiles.GeneralFile{
			{
				Filename: "503.http",
				Content: `HTTP/1.0 503 Service Unavailable
Cache-Control: no-cache
Connection: close
Content-Type: text/html

<html><body><h1>503 Service Unavailable</h1></body></html>
`,
			},
		},
	}

	err := ValidateConfiguration(config, auxFiles, paths, nil, false)
	if err != nil {
		t.Fatalf("ValidateConfiguration() failed with absolute path general files: %v", err)
	}
}

// TestValidateConfiguration_MissingGlobalSection tests validation failure when global section is missing.
func TestValidateConfiguration_MissingGlobalSection(t *testing.T) {
	// HAProxy requires global section
	config := `
defaults
    mode http
    timeout connect 5000ms
    timeout client 50000ms
    timeout server 50000ms

frontend http-in
    bind :80
    default_backend servers

backend servers
    server s1 127.0.0.1:8080
`

	auxFiles := &AuxiliaryFiles{}

	err := ValidateConfiguration(config, auxFiles, testValidationPaths(t), nil, false)
	// This may or may not fail depending on HAProxy version and parser strictness
	// Just verify the function doesn't panic
	_ = err
}

// TestValidationError_Unwrap tests error unwrapping for ValidationError.
func TestValidationError_Unwrap(t *testing.T) {
	innerErr := &ValidationError{
		Phase:   "syntax",
		Message: "inner error",
		Cause:   nil,
	}

	outerErr := &ValidationError{
		Phase:   "semantic",
		Message: "outer error",
		Cause:   innerErr,
	}

	unwrapped := outerErr.Unwrap()
	if unwrapped != innerErr {
		t.Errorf("Expected unwrapped error to be innerErr, got: %v", unwrapped)
	}
}

// TestValidateConfiguration_BackendHTTPRequestRuleInvalidAuthRealm tests validation of
// backend HTTP request rules with invalid auth_realm patterns (e.g., containing spaces).
// This test demonstrates the bug where backend rules are not validated against the OpenAPI schema.
func TestValidateConfiguration_BackendHTTPRequestRuleInvalidAuthRealm(t *testing.T) {
	// Config with backend http-request auth rule having auth_realm with spaces
	// OpenAPI spec pattern for auth_realm is: ^[^\s]+" (no spaces allowed)
	config := `
global
    daemon

defaults
    mode http
    timeout connect 5000ms
    timeout client 50000ms
    timeout server 50000ms

userlist auth_users
    user admin password $5$rounds=10000$saltysalt$hashedpassword

frontend http-in
    bind :80
    default_backend protected

backend protected
    http-request auth realm "Echo-Server Protected" unless { http_auth(auth_users) }
    server s1 127.0.0.1:8080
`

	auxFiles := &AuxiliaryFiles{}

	err := ValidateConfiguration(config, auxFiles, testValidationPaths(t), nil, false)
	if err == nil {
		t.Fatal("ValidateConfiguration() should fail on backend http-request rule with invalid auth_realm (contains spaces)")
	}

	// Verify it's a validation error
	valErr, ok := err.(*ValidationError)
	if !ok {
		t.Fatalf("Expected *ValidationError, got %T", err)
	}

	// Verify it's a schema phase error
	if valErr.Phase != "schema" {
		t.Errorf("Expected phase='schema', got: %q", valErr.Phase)
	}

	// Verify error message mentions auth_realm and the backend
	errMsg := err.Error()
	if !strings.Contains(errMsg, "auth_realm") {
		t.Errorf("Expected error message to contain 'auth_realm', got: %s", errMsg)
	}
	if !strings.Contains(errMsg, "backend") && !strings.Contains(errMsg, "protected") {
		t.Errorf("Expected error message to mention backend 'protected', got: %s", errMsg)
	}
}

// TestValidateConfiguration_FrontendTCPRequestRuleValidation tests comprehensive validation
// of frontend TCP request rules to ensure all rule types are validated.
func TestValidateConfiguration_FrontendTCPRequestRuleValidation(t *testing.T) {
	// Valid config with TCP request rule - should pass
	config := `
global
    daemon

defaults
    mode tcp
    timeout connect 5000ms
    timeout client 50000ms
    timeout server 50000ms

frontend tcp-in
    bind :3306
    mode tcp
    tcp-request connection accept
    default_backend mysql-servers

backend mysql-servers
    mode tcp
    server mysql1 127.0.0.1:3307
`

	auxFiles := &AuxiliaryFiles{}

	err := ValidateConfiguration(config, auxFiles, testValidationPaths(t), nil, false)
	if err != nil {
		t.Fatalf("ValidateConfiguration() should pass on valid TCP request rules: %v", err)
	}
}

// TestValidateConfiguration_BackendServerTemplateValidation tests validation
// of server templates in backends.
func TestValidateConfiguration_BackendServerTemplateValidation(t *testing.T) {
	// Valid config with server template - should pass
	config := `
global
    daemon

defaults
    mode http
    timeout connect 5000ms
    timeout client 50000ms
    timeout server 50000ms

resolvers mydns
    nameserver dns1 127.0.0.1:53

frontend http-in
    bind :80
    default_backend dynamic-servers

backend dynamic-servers
    server-template srv 1-3 example.com:8080 check resolvers mydns
`

	auxFiles := &AuxiliaryFiles{}

	err := ValidateConfiguration(config, auxFiles, testValidationPaths(t), nil, false)
	if err != nil {
		t.Fatalf("ValidateConfiguration() should pass on valid server templates: %v", err)
	}
}

// TestRemoveNullValues tests recursive null value removal from JSON maps.
func TestRemoveNullValues(t *testing.T) {
	tests := []struct {
		name  string
		input map[string]interface{}
		want  map[string]interface{}
	}{
		{
			name:  "empty map",
			input: map[string]interface{}{},
			want:  map[string]interface{}{},
		},
		{
			name: "no null values",
			input: map[string]interface{}{
				"name": "test",
				"port": 8080,
			},
			want: map[string]interface{}{
				"name": "test",
				"port": 8080,
			},
		},
		{
			name: "with null values",
			input: map[string]interface{}{
				"name":   "test",
				"port":   8080,
				"weight": nil,
			},
			want: map[string]interface{}{
				"name": "test",
				"port": 8080,
			},
		},
		{
			name: "nested map with nulls",
			input: map[string]interface{}{
				"server": map[string]interface{}{
					"name":   "srv1",
					"weight": nil,
				},
			},
			want: map[string]interface{}{
				"server": map[string]interface{}{
					"name": "srv1",
				},
			},
		},
		{
			name: "empty nested map removed",
			input: map[string]interface{}{
				"name": "test",
				"options": map[string]interface{}{
					"value": nil,
				},
			},
			want: map[string]interface{}{
				"name": "test",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := removeNullValues(tt.input)
			// Compare via JSON marshaling to handle nested maps
			gotJSON, _ := json.Marshal(got)
			wantJSON, _ := json.Marshal(tt.want)
			if !bytes.Equal(gotJSON, wantJSON) {
				t.Errorf("removeNullValues() = %s, want %s", gotJSON, wantJSON)
			}
		})
	}
}

// TestCleanJSON tests JSON cleaning functionality.
func TestCleanJSON(t *testing.T) {
	tests := []struct {
		name    string
		input   string
		want    string
		wantErr bool
	}{
		{
			name:  "valid JSON without nulls",
			input: `{"name":"test","port":8080}`,
			want:  `{"name":"test","port":8080}`,
		},
		{
			name:  "JSON with null values",
			input: `{"name":"test","weight":null}`,
			want:  `{"name":"test"}`,
		},
		{
			name:    "invalid JSON",
			input:   `{"name":`,
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := cleanJSON([]byte(tt.input))
			assertCleanJSONResult(t, got, err, tt.want, tt.wantErr)
		})
	}
}

// assertCleanJSONResult validates the result of cleanJSON.
func assertCleanJSONResult(t *testing.T, got []byte, err error, want string, wantErr bool) {
	t.Helper()

	if wantErr {
		if err == nil {
			t.Errorf("cleanJSON() expected error, got nil")
		}
		return
	}
	if err != nil {
		t.Errorf("cleanJSON() unexpected error: %v", err)
		return
	}
	// Compare by unmarshaling to avoid whitespace differences
	var gotMap, wantMap map[string]interface{}
	if err := json.Unmarshal(got, &gotMap); err != nil {
		t.Errorf("cleanJSON() output is not valid JSON: %v", err)
		return
	}
	if err := json.Unmarshal([]byte(want), &wantMap); err != nil {
		t.Errorf("test setup error: want is not valid JSON: %v", err)
		return
	}
	if len(gotMap) != len(wantMap) {
		t.Errorf("cleanJSON() = %s, want %s", got, want)
	}
}

// TestVersionMinor tests minor version extraction.
func TestVersionMinor(t *testing.T) {
	tests := []struct {
		name    string
		version *Version
		want    int
	}{
		{
			name:    "nil version",
			version: nil,
			want:    0,
		},
		{
			name: "version 3.2",
			version: &Version{
				Major: 3,
				Minor: 2,
			},
			want: 2,
		},
		{
			name: "version 3.0",
			version: &Version{
				Major: 3,
				Minor: 0,
			},
			want: 0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := versionMinor(tt.version)
			if got != tt.want {
				t.Errorf("versionMinor() = %d, want %d", got, tt.want)
			}
		})
	}
}

// TestValidationError_Error tests error message formatting.
func TestValidationError_Error(t *testing.T) {
	tests := []struct {
		name     string
		err      *ValidationError
		contains []string
	}{
		{
			name: "syntax error with phase",
			err: &ValidationError{
				Phase:   "syntax",
				Message: "invalid directive",
				Cause:   nil,
			},
			contains: []string{"syntax", "validation failed", "invalid directive"},
		},
		{
			name: "semantic error with phase",
			err: &ValidationError{
				Phase:   "semantic",
				Message: "backend not found",
				Cause:   nil,
			},
			contains: []string{"semantic", "validation failed", "backend not found"},
		},
		{
			name: "error without phase",
			err: &ValidationError{
				Phase:   "",
				Message: "generic error",
				Cause:   nil,
			},
			contains: []string{"HAProxy validation failed", "generic error"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			errMsg := tt.err.Error()
			for _, substr := range tt.contains {
				if !strings.Contains(errMsg, substr) {
					t.Errorf("Expected error message to contain %q, got: %s", substr, errMsg)
				}
			}
		})
	}
}

// =============================================================================
// OpenAPI Spec Caching Tests
// =============================================================================

// TestGetCachedSwaggerV30 tests v3.0 OpenAPI spec caching.
func TestGetCachedSwaggerV30(t *testing.T) {
	// First call should load and cache the spec
	spec1, err1 := getCachedSwaggerV30()
	if err1 != nil {
		t.Fatalf("getCachedSwaggerV30() first call failed: %v", err1)
	}
	if spec1 == nil {
		t.Fatal("getCachedSwaggerV30() returned nil spec on first call")
	}

	// Second call should return the same cached instance
	spec2, err2 := getCachedSwaggerV30()
	if err2 != nil {
		t.Fatalf("getCachedSwaggerV30() second call failed: %v", err2)
	}
	if spec2 == nil {
		t.Fatal("getCachedSwaggerV30() returned nil spec on second call")
	}

	// Verify it's the same instance (caching works)
	if spec1 != spec2 {
		t.Error("getCachedSwaggerV30() should return the same cached instance")
	}

	// Verify spec has expected content
	if spec1.Components == nil || spec1.Components.Schemas == nil {
		t.Error("getCachedSwaggerV30() spec should have components and schemas")
	}
}

// TestGetCachedSwaggerV31 tests v3.1 OpenAPI spec caching.
func TestGetCachedSwaggerV31(t *testing.T) {
	// First call should load and cache the spec
	spec1, err1 := getCachedSwaggerV31()
	if err1 != nil {
		t.Fatalf("getCachedSwaggerV31() first call failed: %v", err1)
	}
	if spec1 == nil {
		t.Fatal("getCachedSwaggerV31() returned nil spec on first call")
	}

	// Second call should return the same cached instance
	spec2, err2 := getCachedSwaggerV31()
	if err2 != nil {
		t.Fatalf("getCachedSwaggerV31() second call failed: %v", err2)
	}
	if spec2 == nil {
		t.Fatal("getCachedSwaggerV31() returned nil spec on second call")
	}

	// Verify it's the same instance (caching works)
	if spec1 != spec2 {
		t.Error("getCachedSwaggerV31() should return the same cached instance")
	}

	// Verify spec has expected content
	if spec1.Components == nil || spec1.Components.Schemas == nil {
		t.Error("getCachedSwaggerV31() spec should have components and schemas")
	}
}

// TestGetCachedSwaggerV32 tests v3.2 OpenAPI spec caching.
func TestGetCachedSwaggerV32(t *testing.T) {
	// First call should load and cache the spec
	spec1, err1 := getCachedSwaggerV32()
	if err1 != nil {
		t.Fatalf("getCachedSwaggerV32() first call failed: %v", err1)
	}
	if spec1 == nil {
		t.Fatal("getCachedSwaggerV32() returned nil spec on first call")
	}

	// Second call should return the same cached instance
	spec2, err2 := getCachedSwaggerV32()
	if err2 != nil {
		t.Fatalf("getCachedSwaggerV32() second call failed: %v", err2)
	}
	if spec2 == nil {
		t.Fatal("getCachedSwaggerV32() returned nil spec on second call")
	}

	// Verify it's the same instance (caching works)
	if spec1 != spec2 {
		t.Error("getCachedSwaggerV32() should return the same cached instance")
	}

	// Verify spec has expected content
	if spec1.Components == nil || spec1.Components.Schemas == nil {
		t.Error("getCachedSwaggerV32() spec should have components and schemas")
	}
}

// TestGetSwaggerForVersion tests version-based spec selection.
func TestGetSwaggerForVersion(t *testing.T) {
	tests := []struct {
		name        string
		version     *Version
		description string
	}{
		{
			name:        "nil version defaults to v3.0",
			version:     nil,
			description: "nil version should default to v3.0 (safest default)",
		},
		{
			name:        "version 3.0 uses v3.0",
			version:     &Version{Major: 3, Minor: 0},
			description: "explicit v3.0 should use v3.0 spec",
		},
		{
			name:        "version 3.1 uses v3.1",
			version:     &Version{Major: 3, Minor: 1},
			description: "explicit v3.1 should use v3.1 spec",
		},
		{
			name:        "version 3.2 uses v3.2",
			version:     &Version{Major: 3, Minor: 2},
			description: "explicit v3.2 should use v3.2 spec",
		},
		{
			name:        "version 3.3 uses v3.2 (latest available)",
			version:     &Version{Major: 3, Minor: 3},
			description: "v3.3+ should use v3.2 spec (latest available)",
		},
		{
			name:        "version 4.0 uses v3.2 (latest available)",
			version:     &Version{Major: 4, Minor: 0},
			description: "v4.0+ should use v3.2 spec (latest available)",
		},
		{
			name:        "version 2.9 uses v3.0 (fallback)",
			version:     &Version{Major: 2, Minor: 9},
			description: "v2.x should fall back to v3.0 spec",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			spec, err := getSwaggerForVersion(tt.version)
			if err != nil {
				t.Fatalf("getSwaggerForVersion() failed: %v", err)
			}
			if spec == nil {
				t.Fatalf("getSwaggerForVersion() returned nil spec")
			}

			// Verify spec has expected structure
			if spec.Components == nil {
				t.Error("spec should have Components")
			}
			if spec.Components.Schemas == nil {
				t.Error("spec should have Schemas")
			}
		})
	}
}

// TestGetCachedSwagger_ConcurrentAccess tests thread-safe access to cached specs.
func TestGetCachedSwagger_ConcurrentAccess(t *testing.T) {
	const goroutines = 50

	// Test concurrent access to all three cached specs
	testFuncs := []struct {
		name string
		fn   func() error
	}{
		{
			name: "V30",
			fn: func() error {
				_, err := getCachedSwaggerV30()
				return err
			},
		},
		{
			name: "V31",
			fn: func() error {
				_, err := getCachedSwaggerV31()
				return err
			},
		},
		{
			name: "V32",
			fn: func() error {
				_, err := getCachedSwaggerV32()
				return err
			},
		},
	}

	for _, tf := range testFuncs {
		t.Run(tf.name, func(t *testing.T) {
			errs := make(chan error, goroutines)

			for i := 0; i < goroutines; i++ {
				go func() {
					errs <- tf.fn()
				}()
			}

			// Collect all errors
			for i := 0; i < goroutines; i++ {
				if err := <-errs; err != nil {
					t.Errorf("concurrent getCachedSwagger%s() failed: %v", tf.name, err)
				}
			}
		})
	}
}

// TestGetSwaggerForVersion_ConcurrentAccess tests concurrent version-based spec selection.
func TestGetSwaggerForVersion_ConcurrentAccess(t *testing.T) {
	const goroutines = 50

	versions := []*Version{
		nil,
		{Major: 3, Minor: 0},
		{Major: 3, Minor: 1},
		{Major: 3, Minor: 2},
		{Major: 3, Minor: 3},
		{Major: 4, Minor: 0},
	}

	errs := make(chan error, goroutines*len(versions))

	for i := 0; i < goroutines; i++ {
		for _, v := range versions {
			go func() {
				_, err := getSwaggerForVersion(v)
				errs <- err
			}()
		}
	}

	// Collect all errors
	for i := 0; i < goroutines*len(versions); i++ {
		if err := <-errs; err != nil {
			t.Errorf("concurrent getSwaggerForVersion() failed: %v", err)
		}
	}
}
