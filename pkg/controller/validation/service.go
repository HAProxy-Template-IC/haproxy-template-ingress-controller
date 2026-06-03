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
	"sync"
	"time"

	"gitlab.com/haproxy-haptic/haptic/pkg/controller/names"
	"gitlab.com/haproxy-haptic/haptic/pkg/dataplane"
	"gitlab.com/haproxy-haptic/haptic/pkg/dataplane/parser"
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

	// ParsedConfig is the pre-parsed configuration from syntax validation.
	// May be nil if validation failed or validation cache was used.
	// When non-nil, can be passed to downstream sync operations to avoid re-parsing.
	ParsedConfig *parser.StructuredConfig
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
// The service caches the last successful validation result keyed by a content
// checksum of the config and auxiliary files. When called with unchanged content,
// it returns the cached ParsedConfig immediately, skipping all validation phases.
// Only successful validations are cached; failures always trigger a full retry.
//
// The service can be called concurrently from multiple goroutines.
type ValidationService struct {
	logger *slog.Logger

	// version is the HAProxy version for schema selection.
	version *dataplane.Version

	// skipDNSValidation controls whether to skip DNS resolution failures.
	// Use true for runtime validation (permissive) and false for webhook validation (strict).
	skipDNSValidation bool

	// skipSemanticValidation controls whether to skip the `haproxy -c`
	// semantic-validation step entirely. Use true for the deployment
	// path (the chart already validated the template, the syntax + schema
	// check still runs, and the dominant runtime risk is rolling-restart
	// reaction latency where `haproxy -c` adds ~60 ms per render) and
	// false for the webhook path (admission requests come from operator
	// input where strict validation is what the user expects).
	skipSemanticValidation bool

	// baseDir is the production base directory used in "default-path origin".
	// During validation, this is replaced with the temp directory path so
	// relative paths resolve correctly.
	baseDir string

	// Relative directory names for auxiliary files (must match RenderService output)
	mapsDir     string
	sslCertsDir string
	generalDir  string

	// Validation result cache - skips all validation phases when content unchanged.
	// Per-instance cache prevents webhook validation from evicting main pipeline cache.
	cacheMu            sync.RWMutex
	cachedChecksum     string
	cachedParsedConfig *parser.StructuredConfig
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

	// SkipSemanticValidation controls whether to skip the `haproxy -c`
	// semantic-validation step. Set true for the deployment pipeline
	// (~60 ms per render saved, syntax + schema still runs), false for
	// the webhook (admission requests must still be strictly validated).
	SkipSemanticValidation bool

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
		logger:                 logger,
		version:                cfg.Version,
		skipDNSValidation:      cfg.SkipDNSValidation,
		skipSemanticValidation: cfg.SkipSemanticValidation,
		baseDir:                baseDir,
		mapsDir:                mapsDir,
		sslCertsDir:            sslCertsDir,
		generalDir:             generalDir,
	}
}

// Validate validates HAProxy configuration.
//
// This method:
// 1. Parses and validates the ORIGINAL config (syntax + schema) - this produces the ParsedConfig
// 2. Creates an isolated temp directory
// 3. Replaces production baseDir with temp directory in config (for default-path origin)
// 4. Writes the config and auxiliary files
// 5. Runs semantic validation with haproxy -c using the MODIFIED config
// 6. Cleans up the temp directory
// 7. Returns the original ParsedConfig (with production paths)
//
// The key insight is that syntax/schema validation doesn't need actual files or temp paths -
// it just parses the config string. Only semantic validation (haproxy -c) needs temp paths
// for file I/O. By parsing the original config first, we ensure the returned ParsedConfig
// contains production paths that downstream components can use.
//
// Parameters:
//   - ctx: Context for cancellation
//   - config: The rendered HAProxy configuration content
//   - auxFiles: Auxiliary files (maps, certificates, general files)
//
// Returns:
//   - ValidationResult with success/failure status, timing, and ParsedConfig with production paths
func (s *ValidationService) Validate(ctx context.Context, config string, auxFiles *dataplane.AuxiliaryFiles) *ValidationResult {
	checksum := dataplane.ComputeContentChecksum(config, auxFiles)
	return s.ValidateWithChecksum(ctx, config, auxFiles, checksum)
}

// ValidateWithChecksum validates HAProxy configuration using a pre-computed content checksum.
// This avoids redundant hashing when the caller (e.g., Pipeline) has already computed the checksum.
func (s *ValidationService) ValidateWithChecksum(ctx context.Context, config string, auxFiles *dataplane.AuxiliaryFiles, checksum string) *ValidationResult {
	startTime := time.Now()

	// Check for context cancellation before starting
	if err := ctx.Err(); err != nil {
		return failedResult(fmt.Errorf("validation cancelled: %w", err), "setup", startTime)
	}

	// Check validation cache - skip all phases if content unchanged
	if cachedConfig := s.getCachedResult(checksum); cachedConfig != nil {
		return &ValidationResult{
			Valid:        true,
			DurationMs:   time.Since(startTime).Milliseconds(),
			ParsedConfig: cachedConfig,
		}
	}

	// Step 1: Parse and validate the ORIGINAL config (syntax + schema)
	// This runs syntax validation and API schema validation without needing temp files.
	// The returned parsedConfig contains production paths (e.g., /etc/haproxy) which
	// is what downstream components need.
	parsedConfig, err := dataplane.ValidateSyntaxAndSchema(config, s.version)
	if err != nil {
		return failedResult(err, validationPhase(err), startTime)
	}

	// Short-circuit: when the deployment pipeline opts out of semantic
	// validation, syntax + schema is enough. The chart's bundled
	// validationTests + the webhook's strict semantic check already
	// guard against structurally-broken configs; skipping the per-render
	// `haproxy -c` invocation here removes ~60 ms from every reconcile
	// (the dominant fixed cost on the rolling-restart hot path) without
	// affecting admission-time correctness.
	if s.skipSemanticValidation {
		s.cacheResult(checksum, parsedConfig)
		return &ValidationResult{
			Valid:        true,
			DurationMs:   time.Since(startTime).Milliseconds(),
			ParsedConfig: parsedConfig,
		}
	}

	// Step 2: Create isolated temp directory for semantic validation
	tempDir, err := os.MkdirTemp("", "haproxy-validation-*")
	if err != nil {
		return failedResult(fmt.Errorf("creating temp directory: %w", err), "setup", startTime)
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

	// Step 3: Create modified config with temp directory paths for semantic validation
	// The rendered config contains "default-path origin /etc/haproxy" (or similar).
	// For local validation with haproxy -c, we need HAProxy to resolve relative paths
	// from the temp directory, so we replace the production path with the temp directory path.
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
		ConfigFile:        filepath.Join(tempDir, names.MainTemplateName),
	}

	// Check for context cancellation before running semantic validation
	if err := ctx.Err(); err != nil {
		return failedResult(fmt.Errorf("validation cancelled: %w", err), "setup", startTime)
	}

	// Step 4: Run semantic validation with haproxy -c using the MODIFIED config
	// This validates that HAProxy can actually load the config with all file references resolved.
	err = dataplane.ValidateSemantics(validationConfig, auxFiles, paths, s.skipDNSValidation)
	if err != nil {
		return failedResult(err, validationPhase(err), startTime)
	}

	// Step 5: Cache successful result and return the ORIGINAL parsed config
	// (with production paths, not temp paths).
	s.cacheResult(checksum, parsedConfig)

	return &ValidationResult{
		Valid:        true,
		DurationMs:   time.Since(startTime).Milliseconds(),
		ParsedConfig: parsedConfig,
	}
}

// failedResult builds a Valid=false ValidationResult with the elapsed time
// since startTime. Used by every error exit path in ValidateWithChecksum so the
// timing math and zero-value fields live in one place.
func failedResult(err error, phase string, startTime time.Time) *ValidationResult {
	return &ValidationResult{
		Valid:      false,
		Error:      err,
		Phase:      phase,
		DurationMs: time.Since(startTime).Milliseconds(),
	}
}

// validationPhase returns the phase tag carried by a *dataplane.ValidationError,
// or "unknown" for any other error type. Lets callers tag failed results with a
// consistent phase string regardless of the underlying error shape.
func validationPhase(err error) string {
	if valErr, ok := err.(*dataplane.ValidationError); ok {
		return valErr.Phase
	}
	return "unknown"
}

func (s *ValidationService) getCachedResult(checksum string) *parser.StructuredConfig {
	s.cacheMu.RLock()
	defer s.cacheMu.RUnlock()
	if s.cachedChecksum != "" && s.cachedChecksum == checksum {
		return s.cachedParsedConfig
	}
	return nil
}

func (s *ValidationService) cacheResult(checksum string, parsedConfig *parser.StructuredConfig) {
	s.cacheMu.Lock()
	defer s.cacheMu.Unlock()
	s.cachedChecksum = checksum
	s.cachedParsedConfig = parsedConfig
}
