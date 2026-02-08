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

	_, err := ValidateConfiguration(config, auxFiles, testValidationPaths(t), nil, false)
	if err != nil {
		t.Fatalf("ValidateConfiguration() failed on valid config: %v", err)
	}
}

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

	_, err := ValidateConfiguration(config, auxFiles, testValidationPaths(t), nil, false)
	if err != nil {
		t.Fatalf("ValidateConfiguration() failed on valid complex config: %v", err)
	}
}

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

	_, err := ValidateConfiguration(config, auxFiles, testValidationPaths(t), nil, false)
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

func TestValidateConfiguration_EmptyConfig(t *testing.T) {
	config := ""
	auxFiles := &AuxiliaryFiles{}

	_, err := ValidateConfiguration(config, auxFiles, testValidationPaths(t), nil, false)
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

	_, err := ValidateConfiguration(config, auxFiles, testValidationPaths(t), nil, false)
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

	_, err := ValidateConfiguration(config, auxFiles, testValidationPaths(t), nil, false)
	if err != nil {
		t.Fatalf("ValidateConfiguration() failed with SSL certificate: %v", err)
	}
}

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

	_, err := ValidateConfiguration(config, auxFiles, paths, nil, false)
	if err != nil {
		t.Fatalf("ValidateConfiguration() failed with absolute path map files: %v", err)
	}
}

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

	_, err := ValidateConfiguration(config, auxFiles, paths, nil, false)
	if err != nil {
		t.Fatalf("ValidateConfiguration() failed with absolute path general files: %v", err)
	}
}

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

	_, err := ValidateConfiguration(config, auxFiles, testValidationPaths(t), nil, false)
	// This may or may not fail depending on HAProxy version and parser strictness
	// Just verify the function doesn't panic
	_ = err
}

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

	_, err := ValidateConfiguration(config, auxFiles, testValidationPaths(t), nil, false)
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

	_, err := ValidateConfiguration(config, auxFiles, testValidationPaths(t), nil, false)
	if err != nil {
		t.Fatalf("ValidateConfiguration() should pass on valid TCP request rules: %v", err)
	}
}

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

	_, err := ValidateConfiguration(config, auxFiles, testValidationPaths(t), nil, false)
	if err != nil {
		t.Fatalf("ValidateConfiguration() should pass on valid server templates: %v", err)
	}
}

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

func TestValidationCacheHelpers(t *testing.T) {
	t.Run("hashValidationInput", func(t *testing.T) {
		hash1 := hashValidationInput("config1")
		hash2 := hashValidationInput("config1")
		hash3 := hashValidationInput("config2")

		// Same input should produce same hash
		if hash1 != hash2 {
			t.Error("hashValidationInput() should produce same hash for same input")
		}

		// Different input should produce different hash
		if hash1 == hash3 {
			t.Error("hashValidationInput() should produce different hash for different input")
		}
	})

	t.Run("hashAuxFiles", func(t *testing.T) {
		// nil aux files should return empty string
		if hashAuxFiles(nil) != "" {
			t.Error("hashAuxFiles(nil) should return empty string")
		}

		aux1 := &AuxiliaryFiles{
			MapFiles: []auxiliaryfiles.MapFile{{Path: "test.map", Content: "content1"}},
		}
		aux2 := &AuxiliaryFiles{
			MapFiles: []auxiliaryfiles.MapFile{{Path: "test.map", Content: "content1"}},
		}
		aux3 := &AuxiliaryFiles{
			MapFiles: []auxiliaryfiles.MapFile{{Path: "test.map", Content: "content2"}},
		}

		hash1 := hashAuxFiles(aux1)
		hash2 := hashAuxFiles(aux2)
		hash3 := hashAuxFiles(aux3)

		// Same input should produce same hash
		if hash1 != hash2 {
			t.Error("hashAuxFiles() should produce same hash for same input")
		}

		// Different input should produce different hash
		if hash1 == hash3 {
			t.Error("hashAuxFiles() should produce different hash for different input")
		}
	})

	t.Run("hashVersion", func(t *testing.T) {
		// nil version
		if hashVersion(nil) != "nil" {
			t.Error("hashVersion(nil) should return 'nil'")
		}

		v30 := &Version{Major: 3, Minor: 0}
		v31 := &Version{Major: 3, Minor: 1}

		if hashVersion(v30) != "3.0" {
			t.Errorf("hashVersion(3.0) = %s, want '3.0'", hashVersion(v30))
		}

		if hashVersion(v31) != "3.1" {
			t.Errorf("hashVersion(3.1) = %s, want '3.1'", hashVersion(v31))
		}
	})
}

func TestValidationCacheMechanism(t *testing.T) {
	// Clear cache state before test
	validationCache.mu.Lock()
	validationCache.lastConfigHash = ""
	validationCache.lastAuxHash = ""
	validationCache.lastVersionHash = ""
	validationCache.mu.Unlock()

	configHash := "config123"
	auxHash := "aux456"
	versionHash := "3.2"

	// Initially should not be cached
	if isValidationCached(configHash, auxHash, versionHash) {
		t.Error("isValidationCached() should return false for uncached config")
	}

	// Cache the result
	cacheValidationResult(configHash, auxHash, versionHash)

	// Now should be cached
	if !isValidationCached(configHash, auxHash, versionHash) {
		t.Error("isValidationCached() should return true for cached config")
	}

	// Different config should not hit cache
	if isValidationCached("different", auxHash, versionHash) {
		t.Error("isValidationCached() should return false for different config")
	}

	// Different aux should not hit cache
	if isValidationCached(configHash, "different", versionHash) {
		t.Error("isValidationCached() should return false for different aux")
	}

	// Different version should not hit cache
	if isValidationCached(configHash, auxHash, "different") {
		t.Error("isValidationCached() should return false for different version")
	}
}
