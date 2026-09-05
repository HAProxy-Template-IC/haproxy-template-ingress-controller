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
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"fmt"
	"math/big"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"gitlab.com/haproxy-haptic/haptic/pkg/dataplane/auxiliaryfiles"
)

// generateSelfSignedSSLPEM returns a PEM bundle (cert + private key) that
// passes HAProxy's strict format check in `-c` mode. We generate a fresh
// self-signed certificate per test rather than committing a fixture, both to
// avoid expiry surprises and to keep the test fully self-contained.
func generateSelfSignedSSLPEM(t *testing.T) string {
	t.Helper()

	priv, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("generating RSA key: %v", err)
	}

	template := &x509.Certificate{
		SerialNumber:          big.NewInt(1),
		Subject:               pkix.Name{CommonName: "haptic-test"},
		NotBefore:             time.Now().Add(-1 * time.Hour),
		NotAfter:              time.Now().Add(24 * time.Hour),
		KeyUsage:              x509.KeyUsageKeyEncipherment | x509.KeyUsageDigitalSignature,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		BasicConstraintsValid: true,
		DNSNames:              []string{"localhost"},
	}
	certDER, err := x509.CreateCertificate(rand.Reader, template, template, &priv.PublicKey, priv)
	if err != nil {
		t.Fatalf("creating certificate: %v", err)
	}

	certPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: certDER})
	keyPEM := pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(priv)})
	return string(certPEM) + string(keyPEM)
}

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

	err := ValidateConfiguration(config, auxFiles, testValidationPaths(t), false)
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

	err := ValidateConfiguration(config, auxFiles, testValidationPaths(t), false)
	if err != nil {
		t.Fatalf("ValidateConfiguration() failed on valid complex config: %v", err)
	}
}

func TestValidateConfiguration_SyntaxError(t *testing.T) {
	// Simulate haproxy rejecting the malformed config (unit tests never
	// shell out; the real binary's verdict is integration-test territory).
	installRejectingHAProxy(t, "parsing [haproxy.cfg:9] : 'backend' section requires a name")

	config := `
global
    daemon

defaults
    mode http

frontend http-in
    bind :80
backend
`

	auxFiles := &AuxiliaryFiles{}

	err := ValidateConfiguration(config, auxFiles, testValidationPaths(t), false)
	if err == nil {
		t.Fatal("ValidateConfiguration() should fail on malformed config")
	}

	// Verify it's a validation error
	valErr, ok := err.(*ValidationError)
	if !ok {
		t.Fatalf("Expected *ValidationError, got %T", err)
	}

	if valErr.Phase != phaseNameSemantic {
		t.Errorf("Expected phase to be %q, got: %q", phaseNameSemantic, valErr.Phase)
	}

	// Verify error message contains useful info
	errMsg := err.Error()
	if !strings.Contains(errMsg, "validation failed") {
		t.Errorf("Expected error message to contain 'validation failed', got: %s", errMsg)
	}
}

func TestValidateConfiguration_EmptyConfig(t *testing.T) {
	// An empty config is HAProxy's verdict to give, not the controller's:
	// nothing short-circuits it before the binary sees it. Simulated here
	// (unit tests never shell out) with HAProxy's own message.
	installRejectingHAProxy(t, "no <listen|frontend|backend> line. Nothing to do !")

	err := ValidateConfiguration("", &AuxiliaryFiles{}, testValidationPaths(t), false)
	if err == nil {
		t.Fatal("ValidateConfiguration() should surface HAProxy's refusal of an empty config")
	}

	valErr, ok := err.(*ValidationError)
	if !ok {
		t.Fatalf("Expected *ValidationError, got %T", err)
	}
	if valErr.Phase != "semantic" {
		t.Errorf("Expected phase='semantic', got: %q", valErr.Phase)
	}
}

func TestValidateConfiguration_SemanticError(t *testing.T) {
	// Simulate haproxy rejecting the dangling use_backend reference (unit
	// tests never shell out; the real verdict is integration-test territory).
	installRejectingHAProxy(t, "unable to find required backend: 'nonexistent'")

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

	err := ValidateConfiguration(config, auxFiles, testValidationPaths(t), false)
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
	// HAProxy's -c mode actually loads the cert referenced by `bind ssl crt`,
	// so the bundled PEM has to be a syntactically valid cert + key — a
	// hand-crafted dummy gets rejected. We generate a fresh self-signed
	// cert per run via crypto/x509 to avoid bundling a fixture that would
	// silently expire.
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

	auxFiles := &AuxiliaryFiles{
		SSLCertificates: []auxiliaryfiles.SSLCertificate{
			{
				Path:    "ssl/cert.pem",
				Content: generateSelfSignedSSLPEM(t),
			},
		},
	}

	if err := ValidateConfiguration(config, auxFiles, testValidationPaths(t), false); err != nil {
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

	err := ValidateConfiguration(config, auxFiles, paths, false)
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

	err := ValidateConfiguration(config, auxFiles, paths, false)
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

	err := ValidateConfiguration(config, auxFiles, testValidationPaths(t), false)
	// This may or may not fail depending on the HAProxy version.
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

	err := ValidateConfiguration(config, auxFiles, testValidationPaths(t), false)
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

	err := ValidateConfiguration(config, auxFiles, testValidationPaths(t), false)
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

func TestValidateConfiguration_RevalidatesIdenticalBytesAfterExecutorSwap(t *testing.T) {
	const config = "global\n    daemon\n"
	paths := testValidationPaths(t)
	auxFiles := &AuxiliaryFiles{}

	acceptChecks := 0
	restoreAccepting := SetHAProxyExecutor(contextExecutor{check: func(context.Context, string, ...string) ([]byte, error) {
		acceptChecks++
		return nil, nil
	}})
	err := ValidateConfiguration(config, auxFiles, paths, false)
	restoreAccepting()
	require.NoError(t, err)
	require.Equal(t, 1, acceptChecks)

	rejectChecks := 0
	restoreRejecting := SetHAProxyExecutor(contextExecutor{check: func(context.Context, string, ...string) ([]byte, error) {
		rejectChecks++
		return []byte("[ALERT] config : runtime policy now refuses this config\n"), fmt.Errorf("exit status 1")
	}})
	t.Cleanup(restoreRejecting)

	err = ValidateConfiguration(config, auxFiles, paths, false)
	require.Error(t, err)
	assert.Equal(t, 1, rejectChecks, "identical bytes must reach the replacement executor")
	var validationErr *ValidationError
	require.ErrorAs(t, err, &validationErr)
	assert.Equal(t, phaseNameSemantic, validationErr.Phase)
	assert.ErrorContains(t, err, "runtime policy now refuses this config")
}
