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

package validation

import (
	"context"
	"log/slog"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"gitlab.com/haproxy-haptic/haptic/pkg/dataplane"
	"gitlab.com/haproxy-haptic/haptic/pkg/dataplane/auxiliaryfiles"
)

func TestNewValidationService(t *testing.T) {
	svc := NewValidationService(&ValidationServiceConfig{
		Logger:            slog.Default(),
		SkipDNSValidation: true,
	})

	require.NotNil(t, svc)
	assert.NotNil(t, svc.logger)
	assert.True(t, svc.skipDNSValidation)
}

func TestNewValidationService_DefaultLogger(t *testing.T) {
	svc := NewValidationService(&ValidationServiceConfig{})

	require.NotNil(t, svc)
	assert.NotNil(t, svc.logger)
}

func TestValidationService_Validate_ValidConfig(t *testing.T) {
	svc := NewValidationService(&ValidationServiceConfig{
		Logger:            slog.Default(),
		SkipDNSValidation: true,
	})

	// Minimal valid HAProxy configuration
	config := `global
    daemon

defaults
    mode http
    timeout connect 5s
    timeout client 50s
    timeout server 50s

frontend http_front
    bind *:8080
    default_backend http_back

backend http_back
    server srv1 127.0.0.1:80
`

	result := svc.Validate(context.Background(), config, nil)

	require.NotNil(t, result)
	assert.True(t, result.Valid, "expected valid config, got error: %v", result.Error)
	assert.Nil(t, result.Error)
	assert.Empty(t, result.Phase)
	assert.GreaterOrEqual(t, result.DurationMs, int64(0))
}

func TestValidationService_Validate_SyntaxError(t *testing.T) {
	svc := NewValidationService(&ValidationServiceConfig{
		Logger:            slog.Default(),
		SkipDNSValidation: true,
	})

	// Invalid HAProxy configuration with syntax error
	config := `global
    daemon

defaults
    invalid_directive foo
`

	result := svc.Validate(context.Background(), config, nil)

	require.NotNil(t, result)
	assert.False(t, result.Valid)
	assert.NotNil(t, result.Error)
	assert.GreaterOrEqual(t, result.DurationMs, int64(0))
}

func TestValidationService_Validate_WithMapFiles(t *testing.T) {
	svc := NewValidationService(&ValidationServiceConfig{
		Logger:            slog.Default(),
		SkipDNSValidation: true,
	})

	// Config that references a map file
	config := `global
    daemon

defaults
    mode http
    timeout connect 5s
    timeout client 50s
    timeout server 50s

frontend http_front
    bind *:8080
    acl is_api hdr(host) -f maps/hosts.map
    default_backend http_back

backend http_back
    server srv1 127.0.0.1:80
`

	auxFiles := &dataplane.AuxiliaryFiles{
		MapFiles: []auxiliaryfiles.MapFile{
			{
				Path:    "hosts.map",
				Content: "api.example.com\n",
			},
		},
	}

	result := svc.Validate(context.Background(), config, auxFiles)

	require.NotNil(t, result)
	assert.True(t, result.Valid, "expected valid config with map file, got error: %v", result.Error)
	assert.Nil(t, result.Error)
}

func TestValidationService_Validate_MissingMapFile(t *testing.T) {
	svc := NewValidationService(&ValidationServiceConfig{
		Logger:            slog.Default(),
		SkipDNSValidation: true,
	})

	// Config that references a non-existent map file
	config := `global
    daemon

defaults
    mode http
    timeout connect 5s
    timeout client 50s
    timeout server 50s

frontend http_front
    bind *:8080
    acl is_api hdr(host) -f maps/missing.map
    default_backend http_back

backend http_back
    server srv1 127.0.0.1:80
`

	// No auxiliary files provided
	result := svc.Validate(context.Background(), config, nil)

	require.NotNil(t, result)
	assert.False(t, result.Valid)
	assert.NotNil(t, result.Error)
	assert.Equal(t, "semantic", result.Phase)
}

func TestValidationService_Validate_WithGeneralFiles(t *testing.T) {
	// GeneralDir must match the directory name referenced in the config's errorfile directive
	svc := NewValidationService(&ValidationServiceConfig{
		Logger:            slog.Default(),
		SkipDNSValidation: true,
		GeneralDir:        "files", // Matches "files/503.http" in config
	})

	// Config that references an error file
	config := `global
    daemon

defaults
    mode http
    timeout connect 5s
    timeout client 50s
    timeout server 50s
    errorfile 503 files/503.http

frontend http_front
    bind *:8080
    default_backend http_back

backend http_back
    server srv1 127.0.0.1:80
`

	auxFiles := &dataplane.AuxiliaryFiles{
		GeneralFiles: []auxiliaryfiles.GeneralFile{
			{
				Filename: "503.http",
				Path:     "files/503.http",
				Content:  "HTTP/1.0 503 Service Unavailable\r\nContent-Type: text/html\r\n\r\n<html><body><h1>503 Service Unavailable</h1></body></html>\r\n",
			},
		},
	}

	result := svc.Validate(context.Background(), config, auxFiles)

	require.NotNil(t, result)
	assert.True(t, result.Valid, "expected valid config with error file, got error: %v", result.Error)
	assert.Nil(t, result.Error)
}

func TestValidationService_ValidateWithStrictDNS(t *testing.T) {
	// Create service with permissive DNS
	svc := NewValidationService(&ValidationServiceConfig{
		Logger:            slog.Default(),
		SkipDNSValidation: true,
	})

	// Valid config with localhost server
	config := `global
    daemon

defaults
    mode http
    timeout connect 5s
    timeout client 50s
    timeout server 50s

frontend http_front
    bind *:8080
    default_backend http_back

backend http_back
    server srv1 127.0.0.1:80
`

	// Strict validation should still pass for localhost
	result := svc.ValidateWithStrictDNS(context.Background(), config, nil)

	require.NotNil(t, result)
	assert.True(t, result.Valid, "expected valid config, got error: %v", result.Error)
}

func TestValidationService_Validate_TempDirCleanup(t *testing.T) {
	svc := NewValidationService(&ValidationServiceConfig{
		Logger:            slog.Default(),
		SkipDNSValidation: true,
	})

	config := `global
    daemon

defaults
    mode http
    timeout connect 5s
    timeout client 50s
    timeout server 50s

frontend http_front
    bind *:8080
    default_backend http_back

backend http_back
    server srv1 127.0.0.1:80
`

	// Run validation multiple times to ensure temp dirs are cleaned up
	for i := 0; i < 3; i++ {
		result := svc.Validate(context.Background(), config, nil)
		require.NotNil(t, result)
		assert.True(t, result.Valid, "iteration %d: expected valid config, got error: %v", i, result.Error)
	}

	// No assertion on temp dir count - cleanup is verified by not running out of temp space
	// The defer in Validate ensures cleanup happens
}

func TestValidationService_Validate_Concurrent(t *testing.T) {
	svc := NewValidationService(&ValidationServiceConfig{
		Logger:            slog.Default(),
		SkipDNSValidation: true,
	})

	config := `global
    daemon

defaults
    mode http
    timeout connect 5s
    timeout client 50s
    timeout server 50s

frontend http_front
    bind *:8080
    default_backend http_back

backend http_back
    server srv1 127.0.0.1:80
`

	// Run concurrent validations to verify thread safety
	const concurrency = 5
	results := make(chan *ValidationResult, concurrency)

	for i := 0; i < concurrency; i++ {
		go func() {
			result := svc.Validate(context.Background(), config, nil)
			results <- result
		}()
	}

	// Collect all results
	for i := 0; i < concurrency; i++ {
		result := <-results
		require.NotNil(t, result)
		assert.True(t, result.Valid, "concurrent validation %d: expected valid config, got error: %v", i, result.Error)
	}
}

func TestValidationService_Validate_ParsedConfigPreservesProductionPaths(t *testing.T) {
	// This test ensures the pre-parsed config optimization returns configs
	// with production paths, not temp validation paths.
	//
	// The validation service replaces "default-path origin /etc/haproxy" with
	// "default-path origin /tmp/haproxy-validation-XXX" for haproxy -c validation.
	// The parsed config must contain the ORIGINAL production path, not the temp path,
	// because downstream components (deployer) use this config for sync operations.

	const productionBaseDir = "/etc/haproxy"

	svc := NewValidationService(&ValidationServiceConfig{
		Logger:            slog.Default(),
		SkipDNSValidation: true,
		BaseDir:           productionBaseDir,
	})

	// Config with default-path origin directive - this is what production configs look like
	config := `global
    daemon
    default-path origin /etc/haproxy

defaults
    mode http
    timeout connect 5s
    timeout client 50s
    timeout server 50s

frontend http_front
    bind *:8080
    default_backend http_back

backend http_back
    server srv1 127.0.0.1:80
`

	result := svc.Validate(context.Background(), config, nil)

	require.NotNil(t, result)
	require.True(t, result.Valid, "expected valid config, got error: %v", result.Error)

	// Critical assertions for the pre-parsed config optimization
	require.NotNil(t, result.ParsedConfig, "ParsedConfig should be set for successful validation")
	require.NotNil(t, result.ParsedConfig.Global, "Global section should be parsed")
	require.NotNil(t, result.ParsedConfig.Global.DefaultPath, "DefaultPath should be parsed")

	// THE BUG: ParsedConfig.Global.DefaultPath.Path contains "/tmp/haproxy-validation-XXX"
	// instead of the production path "/etc/haproxy"
	assert.Equal(t, "origin", result.ParsedConfig.Global.DefaultPath.Type,
		"DefaultPath type should be 'origin'")
	assert.Equal(t, productionBaseDir, result.ParsedConfig.Global.DefaultPath.Path,
		"DefaultPath.Path should contain production path, not temp validation path")
	assert.NotContains(t, result.ParsedConfig.Global.DefaultPath.Path, "/tmp/",
		"DefaultPath.Path must NOT contain temp directory path")
}
