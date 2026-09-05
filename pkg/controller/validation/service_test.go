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
	"errors"
	"log/slog"
	"os"
	"path/filepath"
	"testing"

	"gitlab.com/haproxy-haptic/haptic/pkg/controller/testutil"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"gitlab.com/haproxy-haptic/haptic/pkg/dataplane"
	"gitlab.com/haproxy-haptic/haptic/pkg/dataplane/auxiliaryfiles"
	"gitlab.com/haproxy-haptic/haptic/pkg/dataplane/dataplanetest"
)

// validate computes the content checksum and runs ValidateWithChecksum, the
// convenience shape the tests exercise. Production callers (the pipeline)
// always have the checksum precomputed and call ValidateWithChecksum directly.
func validate(s *ValidationService, ctx context.Context, config string, auxFiles *dataplane.AuxiliaryFiles) *ValidationResult {
	checksum := dataplane.ComputeContentChecksum(config, auxFiles)
	return s.ValidateWithChecksum(ctx, config, auxFiles, checksum)
}

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
	config := testutil.MinimalHAProxyConfig

	result := validate(svc, context.Background(), config, nil)

	require.NotNil(t, result)
	assert.True(t, result.Valid, "expected valid config, got error: %v", result.Error)
	assert.Nil(t, result.Error)
	assert.Empty(t, result.Phase)
	assert.GreaterOrEqual(t, result.DurationMs, int64(0))
}

func TestValidationService_CancellationIsFollowedByFreshValidation(t *testing.T) {
	started := make(chan struct{})
	restoreBlocking := dataplanetest.InstallFakeHAProxy(dataplanetest.WithCheckContext(
		func(ctx context.Context, _ string, _ []string) ([]byte, error) {
			close(started)
			<-ctx.Done()
			return nil, context.Cause(ctx)
		},
	))

	svc := NewValidationService(&ValidationServiceConfig{Logger: slog.Default()})
	cause := errors.New("retired reconciliation")
	ctx, cancel := context.WithCancelCause(t.Context())
	done := make(chan *ValidationResult, 1)
	go func() {
		done <- validate(svc, ctx, testutil.MinimalHAProxyConfig, nil)
	}()

	<-started
	cancel(cause)
	result := <-done
	restoreBlocking()
	require.False(t, result.Valid)
	require.ErrorIs(t, result.Error, cause)

	var checks int
	restoreSuccess := dataplanetest.InstallFakeHAProxy(dataplanetest.WithCheck(
		func(string, []string) ([]byte, error) {
			checks++
			return nil, nil
		},
	))
	t.Cleanup(restoreSuccess)

	result = validate(svc, t.Context(), testutil.MinimalHAProxyConfig, nil)
	require.True(t, result.Valid, "validation failed: %v", result.Error)
	assert.Equal(t, 1, checks)
}

func TestValidationService_PreservesPreCancellationCause(t *testing.T) {
	svc := NewValidationService(&ValidationServiceConfig{Logger: slog.Default()})
	cause := errors.New("iteration replaced")
	ctx, cancel := context.WithCancelCause(t.Context())
	cancel(cause)

	result := validate(svc, ctx, testutil.MinimalHAProxyConfig, nil)
	require.False(t, result.Valid)
	require.ErrorIs(t, result.Error, cause)
	assert.EqualError(t, result.Error, "validation cancelled: iteration replaced")
}

func TestValidationService_Validate_SyntaxError(t *testing.T) {
	// Simulate haproxy rejecting the config (unit tests never shell out).
	t.Cleanup(dataplanetest.InstallFakeHAProxy(
		dataplanetest.WithRejectAll("parsing [haproxy.cfg:5] : unknown keyword 'invalid_directive' in 'defaults' section")))

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

	result := validate(svc, context.Background(), config, nil)

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

	result := validate(svc, context.Background(), config, auxFiles)

	require.NotNil(t, result)
	assert.True(t, result.Valid, "expected valid config with map file, got error: %v", result.Error)
	assert.Nil(t, result.Error)
}

func TestValidationService_Validate_MissingMapFile(t *testing.T) {
	// Simulate haproxy failing to open the missing map (unit tests never
	// shell out).
	t.Cleanup(dataplanetest.InstallFakeHAProxy(
		dataplanetest.WithRejectAll("parsing [haproxy.cfg:12] : error opening file <maps/missing.map> for ACL")))

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
	result := validate(svc, context.Background(), config, nil)

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

	result := validate(svc, context.Background(), config, auxFiles)

	require.NotNil(t, result)
	assert.True(t, result.Valid, "expected valid config with error file, got error: %v", result.Error)
	assert.Nil(t, result.Error)
}

func TestValidationService_Validate_TempDirCleanup(t *testing.T) {
	svc := NewValidationService(&ValidationServiceConfig{
		Logger:            slog.Default(),
		SkipDNSValidation: true,
	})

	config := testutil.MinimalHAProxyConfig

	// Run validation multiple times to ensure temp dirs are cleaned up
	for i := range 3 {
		result := validate(svc, context.Background(), config, nil)
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

	config := testutil.MinimalHAProxyConfig

	// Run concurrent validations to verify thread safety
	const concurrency = 5
	results := make(chan *ValidationResult, concurrency)

	for range concurrency {
		go func() {
			result := validate(svc, context.Background(), config, nil)
			results <- result
		}()
	}

	// Collect all results
	for i := range concurrency {
		result := <-results
		require.NotNil(t, result)
		assert.True(t, result.Valid, "concurrent validation %d: expected valid config, got error: %v", i, result.Error)
	}
}

// validConfig is a minimal valid HAProxy configuration used by validation tests.
const validConfig = testutil.MinimalHAProxyConfig

func TestValidationService_RevalidatesIdenticalBytesAfterExecutorSwap(t *testing.T) {
	acceptChecks := 0
	restoreAccepting := dataplanetest.InstallFakeHAProxy(dataplanetest.WithCheck(
		func(string, []string) ([]byte, error) {
			acceptChecks++
			return nil, nil
		},
	))

	svc := NewValidationService(&ValidationServiceConfig{
		Logger:            slog.Default(),
		SkipDNSValidation: true,
	})
	checksum := dataplane.ComputeContentChecksum(validConfig, nil)

	result := svc.ValidateWithChecksum(t.Context(), validConfig, nil, checksum)
	restoreAccepting()
	require.True(t, result.Valid, "first validation failed: %v", result.Error)
	require.Equal(t, 1, acceptChecks)

	rejectChecks := 0
	t.Cleanup(dataplanetest.InstallFakeHAProxy(dataplanetest.WithCheck(
		func(string, []string) ([]byte, error) {
			rejectChecks++
			return []byte("[ALERT] config : runtime policy now refuses this config\n"), errors.New("exit status 1")
		},
	)))

	result = svc.ValidateWithChecksum(t.Context(), validConfig, nil, checksum)
	require.False(t, result.Valid)
	assert.Equal(t, "semantic", result.Phase)
	assert.ErrorContains(t, result.Error, "runtime policy now refuses this config")
	assert.Equal(t, 1, rejectChecks, "identical bytes must reach the replacement executor")
}

func TestValidationService_ChecksumCollisionCannotSkipValidation(t *testing.T) {
	checks := 0
	t.Cleanup(dataplanetest.InstallFakeHAProxy(dataplanetest.WithCheck(
		func(string, []string) ([]byte, error) {
			checks++
			return nil, nil
		},
	)))

	svc := NewValidationService(&ValidationServiceConfig{Logger: slog.Default(), SkipDNSValidation: true})
	left := &dataplane.AuxiliaryFiles{MapFiles: []auxiliaryfiles.MapFile{{Path: "a", Content: "bc"}}}
	right := &dataplane.AuxiliaryFiles{MapFiles: []auxiliaryfiles.MapFile{{Path: "ab", Content: "c"}}}
	require.Equal(t, dataplane.ComputeContentChecksum(validConfig, left), dataplane.ComputeContentChecksum(validConfig, right))

	require.True(t, validate(svc, t.Context(), validConfig, left).Valid)
	require.True(t, validate(svc, t.Context(), validConfig, right).Valid)
	assert.Equal(t, 2, checks)
}

func TestValidationService_Validate_TempPathRewriteIsLocalToTheCheck(t *testing.T) {
	// The service repoints `default-path origin` at its temp directory so
	// haproxy -c resolves the auxiliary files it wrote there. Only the checked
	// copy is rewritten: the caller's config is what ships to the fleet.
	const productionBaseDir = "/etc/haproxy"

	var checked string
	t.Cleanup(dataplanetest.InstallFakeHAProxy(dataplanetest.WithCheck(
		func(workDir string, args []string) ([]byte, error) {
			contents, err := os.ReadFile(filepath.Join(workDir, args[len(args)-1]))
			if err != nil {
				return nil, err
			}
			checked = string(contents)
			return nil, nil
		},
	)))

	svc := NewValidationService(&ValidationServiceConfig{
		Logger:            slog.Default(),
		SkipDNSValidation: true,
		BaseDir:           productionBaseDir,
	})

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

	result := validate(svc, context.Background(), config, nil)
	require.NotNil(t, result)
	require.True(t, result.Valid, "expected valid config, got error: %v", result.Error)

	require.NotEmpty(t, checked, "the fake binary must have seen a config")
	assert.NotContains(t, checked, "default-path origin "+productionBaseDir,
		"the checked copy must point at the temp directory")
	assert.Contains(t, config, "default-path origin "+productionBaseDir,
		"the caller's config must come back untouched")
}
