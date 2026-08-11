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
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"hash"
	"log/slog"
	"sync"
	"time"

	"gitlab.com/haproxy-haptic/haptic/pkg/dataplane/auxiliaryfiles"
	"gitlab.com/haproxy-haptic/haptic/pkg/dataplane/parser"
)

// validationResultCache caches the result of the last successful validation.
// Since validation is expensive (parsing + schema validation + haproxy -c),
// we skip it when the same config is validated again.
type validationResultCache struct {
	mu              sync.RWMutex
	lastConfigHash  string
	lastAuxHash     string
	lastVersionHash string
}

var validationCache = &validationResultCache{}

// ValidationPaths holds the filesystem paths for HAProxy validation.
// These paths must match the HAProxy Dataplane API server's resource configuration.
type ValidationPaths struct {
	// TempDir is the root temp directory for validation files.
	// The validator is responsible for cleaning this up after validation completes.
	// This prevents race conditions where the renderer's cleanup runs before
	// the async validator can use the validation files.
	TempDir           string
	MapsDir           string
	SSLCertsDir       string
	CRTListDir        string // Directory for CRT-list files (may differ from SSLCertsDir on HAProxy < 3.2)
	GeneralStorageDir string
	ConfigFile        string
}

// ValidateSyntaxAndSchema performs syntax and schema validation on HAProxy configuration.
//
// This function runs Phase 1 (syntax validation) and Phase 1.5 (API schema validation)
// but NOT Phase 2 (semantic validation with haproxy binary).
//
// Use this when you need to parse the config without needing file I/O or the haproxy binary.
// The primary use case is when you need to parse the original config (before path modifications)
// for downstream reuse, while semantic validation is done separately with a modified config.
//
// Parameters:
//   - config: The HAProxy configuration content to validate
//   - version: HAProxy/DataPlane API version for schema selection (nil uses default v3.0)
//
// Returns:
//   - *parser.StructuredConfig: The parsed configuration
//   - error: ValidationError with phase information if validation fails
func ValidateSyntaxAndSchema(config string, version *Version) (*parser.StructuredConfig, error) {
	// Phase 1: Syntax validation with client-native parser
	parsedConfig, err := validateSyntax(config)
	if err != nil {
		return nil, phaseSyntax.wrap(err)
	}

	// Phase 1.5: API schema validation with OpenAPI spec
	if err := validateAPISchema(parsedConfig, version); err != nil {
		return nil, phaseSchema.wrap(err)
	}

	return parsedConfig, nil
}

// ValidateSemantics performs semantic validation using the haproxy binary (-c flag).
//
// This function runs only Phase 2 (semantic validation) and assumes syntax/schema validation
// has already been done. Use this after ValidateSyntaxAndSchema() when you need to validate
// a modified config (e.g., with temp paths) separately from parsing.
//
// Parameters:
//   - mainConfig: The HAProxy configuration content (may have modified paths for temp directory)
//   - auxFiles: All auxiliary files (maps, certificates, general files)
//   - paths: Filesystem paths for validation (must be isolated for parallel execution)
//   - skipDNSValidation: If true, adds -dr flag to skip DNS resolution failures
//
// Returns:
//   - error: ValidationError with phase "semantic" if validation fails
func ValidateSemantics(mainConfig string, auxFiles *AuxiliaryFiles, paths *ValidationPaths, skipDNSValidation bool) error {
	return ValidateSemanticsContext(context.Background(), mainConfig, auxFiles, paths, skipDNSValidation)
}

// ValidateSemanticsContext is ValidateSemantics with caller cancellation.
func ValidateSemanticsContext(ctx context.Context, mainConfig string, auxFiles *AuxiliaryFiles, paths *ValidationPaths, skipDNSValidation bool) error {
	if err := validateSemantics(ctx, mainConfig, auxFiles, paths, skipDNSValidation); err != nil {
		return phaseSemantic.wrap(err)
	}
	return nil
}

// ValidateConfiguration performs three-phase HAProxy configuration validation.
//
// Phase 1: Syntax validation using client-native parser
// Phase 1.5: API schema validation using OpenAPI spec (patterns, formats, required fields)
// Phase 2: Semantic validation using haproxy binary (-c flag)
//
// The validation writes files to the directories specified in paths. Callers must ensure
// that paths are isolated (e.g., per-worker temp directories) to allow parallel execution.
//
// Validation result caching: If the same config (main + aux files + version) has been
// successfully validated before, the cached result is returned immediately. This is safe
// because HAProxy config validation is deterministic - the same inputs always produce
// the same result.
//
// Parameters:
//   - mainConfig: The rendered HAProxy configuration (haproxy.cfg content)
//   - auxFiles: All auxiliary files (maps, certificates, general files)
//   - paths: Filesystem paths for validation (must be isolated for parallel execution)
//   - version: HAProxy/DataPlane API version for schema selection (nil uses default v3.0)
//   - skipDNSValidation: If true, adds -dr flag to skip DNS resolution failures. Use true for
//     runtime validation (permissive, prevents blocking when DNS fails) and false for webhook
//     validation (strict, catches DNS issues before resource admission).
//
// Returns:
//   - *parser.StructuredConfig: The pre-parsed configuration from syntax validation (nil on cache hit or error)
//   - error: nil if validation succeeds, ValidationError with phase information if validation fails
func ValidateConfiguration(mainConfig string, auxFiles *AuxiliaryFiles, paths *ValidationPaths, version *Version, skipDNSValidation bool) (*parser.StructuredConfig, error) {
	return ValidateConfigurationContext(context.Background(), mainConfig, auxFiles, paths, version, skipDNSValidation)
}

// ValidateConfigurationContext is ValidateConfiguration with caller cancellation.
func ValidateConfigurationContext(ctx context.Context, mainConfig string, auxFiles *AuxiliaryFiles, paths *ValidationPaths, version *Version, skipDNSValidation bool) (*parser.StructuredConfig, error) {
	return validateConfiguration(ctx, mainConfig, auxFiles, paths, version, skipDNSValidation)
}

func validateConfiguration(ctx context.Context, mainConfig string, auxFiles *AuxiliaryFiles, paths *ValidationPaths, version *Version, skipDNSValidation bool) (*parser.StructuredConfig, error) {
	if cause := context.Cause(ctx); cause != nil {
		return nil, cause
	}

	// Check validation cache first - skip validation if same config already validated
	configHash := hashValidationInput(mainConfig)
	auxHash := hashAuxFiles(auxFiles)
	versionHash := hashVersion(version)

	if isValidationCached(configHash, auxHash, versionHash) {
		if cause := context.Cause(ctx); cause != nil {
			return nil, cause
		}
		slog.Debug("Validation cache hit, skipping validation")
		return nil, ErrValidationCacheHit // Cache hit - caller should use parser cache if parsed config needed
	}

	// Timing variables for phase breakdown
	var syntaxMs, schemaMs, semanticMs int64
	startTime := time.Now()

	// Phase 1: Syntax validation with client-native parser
	// This also returns the parsed configuration for Phase 1.5
	syntaxStart := time.Now()
	parsedConfig, err := validateSyntax(mainConfig)
	syntaxMs = time.Since(syntaxStart).Milliseconds()
	if err != nil {
		return nil, phaseSyntax.wrap(err)
	}
	if cause := context.Cause(ctx); cause != nil {
		return nil, cause
	}

	// Phase 1.5: API schema validation with OpenAPI spec
	schemaStart := time.Now()
	if err := validateAPISchema(parsedConfig, version); err != nil {
		return nil, phaseSchema.wrap(err)
	}
	if cause := context.Cause(ctx); cause != nil {
		return nil, cause
	}
	schemaMs = time.Since(schemaStart).Milliseconds()

	// Phase 2: Semantic validation with haproxy binary
	semanticStart := time.Now()
	if err := validateSemantics(ctx, mainConfig, auxFiles, paths, skipDNSValidation); err != nil {
		return nil, phaseSemantic.wrap(err)
	}
	semanticMs = time.Since(semanticStart).Milliseconds()

	// Log timing breakdown for visibility when debugging
	slog.Debug("Validation phase timing breakdown",
		"total_ms", time.Since(startTime).Milliseconds(),
		"syntax_ms", syntaxMs,
		"schema_ms", schemaMs,
		"semantic_ms", semanticMs,
	)

	if err := cacheValidationResult(ctx, configHash, auxHash, versionHash); err != nil {
		return nil, err
	}

	return parsedConfig, nil
}

// hashValidationInput computes a SHA256 hash of the main config content.
func hashValidationInput(config string) string {
	h := sha256.Sum256([]byte(config))
	return hex.EncodeToString(h[:])
}

// hashAuxFiles computes a combined hash of all auxiliary files.
// The hash includes file paths and contents to detect any changes.
func hashAuxFiles(auxFiles *AuxiliaryFiles) string {
	if auxFiles == nil {
		return ""
	}

	h := sha256.New()
	hashAuxByPath(h, auxFiles.MapFiles, func(f auxiliaryfiles.MapFile) string { return f.Path })
	hashAuxByPath(h, auxFiles.GeneralFiles, func(f auxiliaryfiles.GeneralFile) string { return f.Path })
	hashAuxByPath(h, auxFiles.SSLCertificates, func(f auxiliaryfiles.SSLCertificate) string { return f.Path })
	hashAuxByPath(h, auxFiles.SSLCaFiles, func(f auxiliaryfiles.SSLCaFile) string { return f.Path })
	hashAuxByPath(h, auxFiles.CRTListFiles, func(f auxiliaryfiles.CRTListFile) string { return f.Path })
	return hex.EncodeToString(h.Sum(nil))
}

// hashAuxByPath writes (path, content) for each item to h. Used for the
// validation cache key, which keys on Path (the on-disk location HAProxy will
// see) rather than the API-side identifier returned by FileItem.GetIdentifier()
// (which differs from Path for GeneralFile, where it's the bare Filename).
func hashAuxByPath[T auxiliaryfiles.FileItem](h hash.Hash, items []T, getPath func(T) string) {
	for _, item := range items {
		h.Write([]byte(getPath(item)))
		h.Write([]byte(item.GetContent()))
	}
}

// hashVersion computes a hash string for the version to include in cache key.
func hashVersion(version *Version) string {
	if version == nil {
		return "nil"
	}
	return fmt.Sprintf("%d.%d", version.Major, version.Minor)
}

// isValidationCached checks if the given config combination was already validated successfully.
func isValidationCached(configHash, auxHash, versionHash string) bool {
	validationCache.mu.RLock()
	defer validationCache.mu.RUnlock()

	return validationCache.lastConfigHash == configHash &&
		validationCache.lastAuxHash == auxHash &&
		validationCache.lastVersionHash == versionHash &&
		validationCache.lastConfigHash != "" // Ensure cache is not empty
}

// cacheValidationResult stores the successful validation result for future checks.
func cacheValidationResult(ctx context.Context, configHash, auxHash, versionHash string) error {
	validationCache.mu.Lock()
	defer validationCache.mu.Unlock()
	if cause := context.Cause(ctx); cause != nil {
		return cause
	}

	validationCache.lastConfigHash = configHash
	validationCache.lastAuxHash = auxHash
	validationCache.lastVersionHash = versionHash
	return nil
}
