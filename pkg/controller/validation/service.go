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

// Package validation provides pure validation services for HAProxy configuration.
package validation

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"time"

	"gitlab.com/haproxy-haptic/haptic/pkg/dataplane"
)

// Default directory paths for HAProxy validation.
const (
	// DefaultBaseDir is the default base directory for HAProxy configuration.
	DefaultBaseDir = "/etc/haproxy"

	// DefaultMapsDir is the default relative directory name for map files.
	DefaultMapsDir = "maps"

	// DefaultSSLCertsDir is the default relative directory name for SSL certificates.
	DefaultSSLCertsDir = "ssl"

	// DefaultGeneralDir is the default relative directory name for general files.
	DefaultGeneralDir = "general"
)

// Timeout constants for validation operations.
const (
	// DefaultValidationTimeout is the default timeout for validation operations.
	// This is used by event handlers and webhook validators to prevent indefinite hangs.
	// 30 seconds allows sufficient time for render + validate while preventing stuck requests.
	DefaultValidationTimeout = 30 * time.Second
)

// ValidationResult contains the output of a validation operation.
type ValidationResult struct {
	// Valid is true if the configuration passed all validation phases.
	Valid bool

	// Error contains the validation error if Valid is false.
	Error error

	// Phase indicates which validation phase failed (syntax, schema, semantic, render, setup).
	// Empty if validation succeeded.
	Phase string

	// DurationMs is the total validation duration in milliseconds.
	DurationMs int64
}

// ErrorMessage returns a user-friendly error message.
// Returns empty string if validation succeeded.
func (r *ValidationResult) ErrorMessage() string {
	if r.Valid || r.Error == nil {
		return ""
	}
	return r.Error.Error()
}

// ValidationService is a pure service that validates HAProxy configuration.
//
// This service encapsulates temp directory lifecycle internally:
// - Creates an isolated temp directory for each validation
// - Writes config and auxiliary files
// - Runs haproxy -c for semantic validation
// - Cleans up temp directory after validation
//
// The service is stateless and can be called concurrently from multiple goroutines.
type ValidationService struct {
	logger *slog.Logger

	// version is the HAProxy version for schema selection.
	version *dataplane.Version

	// skipDNSValidation controls whether to skip DNS resolution failures.
	// Use true for runtime validation (permissive) and false for webhook validation (strict).
	skipDNSValidation bool

	// baseDir is the production base directory used in "default-path origin".
	// During validation, this is replaced with the temp directory path so
	// relative paths resolve correctly.
	baseDir string

	// Relative directory names for auxiliary files (must match RenderService output)
	mapsDir     string
	sslCertsDir string
	generalDir  string
}

// ValidationServiceConfig contains configuration for creating a ValidationService.
type ValidationServiceConfig struct {
	// Logger is the structured logger for logging.
	Logger *slog.Logger

	// Version is the HAProxy version for schema selection (nil uses default v3.0).
	Version *dataplane.Version

	// SkipDNSValidation controls whether to skip DNS resolution failures during validation.
	// When true, servers with unresolvable hostnames start in DOWN state instead of failing.
	// When false (strict mode), DNS resolution failures cause validation to fail.
	SkipDNSValidation bool

	// BaseDir is the production base directory used in "default-path origin" directive.
	// During local validation, this is replaced with the temp directory path so that
	// HAProxy resolves relative paths from the temp directory instead of production paths.
	// Example: "/etc/haproxy"
	BaseDir string

	// MapsDir is the relative directory name for map files (e.g., "maps").
	// Should match the basename of the dataplane MapsDir config.
	MapsDir string

	// SSLCertsDir is the relative directory name for SSL certificates (e.g., "ssl").
	// Should match the basename of the dataplane SSLCertsDir config.
	SSLCertsDir string

	// GeneralDir is the relative directory name for general files (e.g., "general").
	// Should match the basename of the dataplane GeneralStorageDir config.
	GeneralDir string
}

// NewValidationService creates a new ValidationService.
func NewValidationService(cfg *ValidationServiceConfig) *ValidationService {
	logger := cfg.Logger
	if logger == nil {
		logger = slog.Default()
	}

	// Use provided directory names or sensible defaults
	baseDir := cfg.BaseDir
	if baseDir == "" {
		baseDir = DefaultBaseDir
	}
	mapsDir := cfg.MapsDir
	if mapsDir == "" {
		mapsDir = DefaultMapsDir
	}
	sslCertsDir := cfg.SSLCertsDir
	if sslCertsDir == "" {
		sslCertsDir = DefaultSSLCertsDir
	}
	generalDir := cfg.GeneralDir
	if generalDir == "" {
		generalDir = DefaultGeneralDir
	}

	return &ValidationService{
		logger:            logger,
		version:           cfg.Version,
		skipDNSValidation: cfg.SkipDNSValidation,
		baseDir:           baseDir,
		mapsDir:           mapsDir,
		sslCertsDir:       sslCertsDir,
		generalDir:        generalDir,
	}
}

// Validate validates HAProxy configuration.
//
// This method:
// 1. Creates an isolated temp directory
// 2. Replaces production baseDir with temp directory in config (for default-path origin)
// 3. Writes the config and auxiliary files
// 4. Runs three-phase validation (syntax, schema, semantic)
// 5. Cleans up the temp directory
//
// Parameters:
//   - ctx: Context for cancellation
//   - config: The rendered HAProxy configuration content
//   - auxFiles: Auxiliary files (maps, certificates, general files)
//
// Returns:
//   - ValidationResult with success/failure status and timing
func (s *ValidationService) Validate(ctx context.Context, config string, auxFiles *dataplane.AuxiliaryFiles) *ValidationResult {
	startTime := time.Now()

	// Check for context cancellation before starting
	if err := ctx.Err(); err != nil {
		return &ValidationResult{
			Valid:      false,
			Error:      fmt.Errorf("validation cancelled: %w", err),
			Phase:      "setup",
			DurationMs: time.Since(startTime).Milliseconds(),
		}
	}

	// Create isolated temp directory for this validation
	tempDir, err := os.MkdirTemp("", "haproxy-validation-*")
	if err != nil {
		return &ValidationResult{
			Valid:      false,
			Error:      fmt.Errorf("failed to create temp directory: %w", err),
			Phase:      "setup",
			DurationMs: time.Since(startTime).Milliseconds(),
		}
	}

	// Ensure cleanup happens regardless of validation outcome
	defer func() {
		if err := os.RemoveAll(tempDir); err != nil {
			s.logger.Warn("failed to clean up validation temp directory",
				"temp_dir", tempDir,
				"error", err,
			)
		}
	}()

	// Replace production baseDir with temp directory in config.
	// The rendered config contains "default-path origin /etc/haproxy" (or similar).
	// For local validation, we need HAProxy to resolve relative paths from the temp
	// directory, so we replace the production path with the temp directory path.
	validationConfig := strings.Replace(config, "default-path origin "+s.baseDir, "default-path origin "+tempDir, 1)

	// Build validation paths using relative subdirectories
	// These must match the relative paths used by RenderService.
	// CRTListDir uses generalDir because CRT-list files are always stored in general
	// file storage to avoid triggering HAProxy reloads (see pkg/dataplane/auxiliaryfiles/crtlist.go).
	paths := &dataplane.ValidationPaths{
		TempDir:           tempDir,
		MapsDir:           filepath.Join(tempDir, s.mapsDir),
		SSLCertsDir:       filepath.Join(tempDir, s.sslCertsDir),
		CRTListDir:        filepath.Join(tempDir, s.generalDir),
		GeneralStorageDir: filepath.Join(tempDir, s.generalDir),
		ConfigFile:        filepath.Join(tempDir, "haproxy.cfg"),
	}

	// Check for context cancellation before running validation
	if err := ctx.Err(); err != nil {
		return &ValidationResult{
			Valid:      false,
			Error:      fmt.Errorf("validation cancelled: %w", err),
			Phase:      "setup",
			DurationMs: time.Since(startTime).Milliseconds(),
		}
	}

	// Run three-phase validation
	err = dataplane.ValidateConfiguration(validationConfig, auxFiles, paths, s.version, s.skipDNSValidation)
	if err != nil {
		// Extract phase from ValidationError if available
		phase := "unknown"
		if valErr, ok := err.(*dataplane.ValidationError); ok {
			phase = valErr.Phase
		}

		return &ValidationResult{
			Valid:      false,
			Error:      err,
			Phase:      phase,
			DurationMs: time.Since(startTime).Milliseconds(),
		}
	}

	return &ValidationResult{
		Valid:      true,
		DurationMs: time.Since(startTime).Milliseconds(),
	}
}

// ValidateWithStrictDNS validates configuration with strict DNS checking.
// This is a convenience method that temporarily overrides SkipDNSValidation.
// Use this for webhook validation where DNS failures should be caught early.
func (s *ValidationService) ValidateWithStrictDNS(ctx context.Context, config string, auxFiles *dataplane.AuxiliaryFiles) *ValidationResult {
	// Create a copy with strict DNS
	strictService := &ValidationService{
		logger:            s.logger,
		version:           s.version,
		skipDNSValidation: false,
		baseDir:           s.baseDir,
		mapsDir:           s.mapsDir,
		sslCertsDir:       s.sslCertsDir,
		generalDir:        s.generalDir,
	}
	return strictService.Validate(ctx, config, auxFiles)
}
