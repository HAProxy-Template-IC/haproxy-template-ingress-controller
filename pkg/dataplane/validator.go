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
	"hash"
	"log/slog"
	"sync"
	"time"

	"gitlab.com/haproxy-haptic/haptic/pkg/dataplane/auxiliaryfiles"
)

// validationResultCache caches the result of the last successful validation.
// Running `haproxy -c` costs a process and a config parse, so an unchanged
// config is not checked twice.
type validationResultCache struct {
	mu             sync.RWMutex
	lastConfigHash string
	lastAuxHash    string
}

var validationCache = &validationResultCache{}

// ValidationPaths holds the filesystem paths for HAProxy validation. They
// mirror the layout the agent writes on the pod, so a config that passes here
// resolves its files there.
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
	return ValidateSemanticsContext(context.Background(), mainConfig, auxFiles, paths, skipDNSValidation, nil)
}

// ValidateSemanticsContext is ValidateSemantics with caller cancellation and a
// caller-owned CheckGate; nil runs on the shared default gate.
func ValidateSemanticsContext(ctx context.Context, mainConfig string, auxFiles *AuxiliaryFiles, paths *ValidationPaths, skipDNSValidation bool, gate *CheckGate) error {
	if err := validateSemantics(ctx, mainConfig, auxFiles, paths, skipDNSValidation, gate); err != nil {
		return phaseSemantic.wrap(err)
	}
	return nil
}

// ValidateConfiguration asks HAProxy whether it can load this configuration.
//
// The validation writes files to the directories specified in paths. Callers must ensure
// that paths are isolated (e.g., per-worker temp directories) to allow parallel execution.
//
// Validation result caching: if the same config (main + aux files) has been
// successfully validated before, ErrValidationCacheHit is returned immediately.
// This is safe because the verdict is deterministic — the same bytes and the
// same binary always produce the same answer.
//
// skipDNSValidation adds -dr, which skips DNS resolution failures. Use true for
// runtime validation (permissive, prevents blocking when DNS fails) and false
// for webhook validation (strict, catches DNS issues before admission).
func ValidateConfiguration(mainConfig string, auxFiles *AuxiliaryFiles, paths *ValidationPaths, skipDNSValidation bool) error {
	return ValidateConfigurationContext(context.Background(), mainConfig, auxFiles, paths, skipDNSValidation, nil)
}

// ValidateConfigurationContext is ValidateConfiguration with caller cancellation
// and a caller-owned CheckGate (nil runs on the shared single-slot default gate;
// a batch caller passes a multi-slot gate so its checks run across cores).
func ValidateConfigurationContext(ctx context.Context, mainConfig string, auxFiles *AuxiliaryFiles, paths *ValidationPaths, skipDNSValidation bool, gate *CheckGate) error {
	if cause := context.Cause(ctx); cause != nil {
		return cause
	}

	// Check validation cache first - skip validation if same config already validated
	configHash := hashValidationInput(mainConfig)
	auxHash := hashAuxFiles(auxFiles)
	if isValidationCached(configHash, auxHash) {
		if cause := context.Cause(ctx); cause != nil {
			return cause
		}
		slog.Debug("Validation cache hit, skipping validation")
		return ErrValidationCacheHit
	}

	start := time.Now()
	if err := validateSemantics(ctx, mainConfig, auxFiles, paths, skipDNSValidation, gate); err != nil {
		return phaseSemantic.wrap(err)
	}
	slog.Debug("Validation completed", "semantic_ms", time.Since(start).Milliseconds())

	return cacheValidationResult(ctx, configHash, auxHash)
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

// isValidationCached checks if the given config combination was already validated successfully.
func isValidationCached(configHash, auxHash string) bool {
	validationCache.mu.RLock()
	defer validationCache.mu.RUnlock()

	return validationCache.lastConfigHash == configHash &&
		validationCache.lastAuxHash == auxHash &&
		validationCache.lastConfigHash != "" // Ensure cache is not empty
}

// cacheValidationResult stores the successful validation result for future checks.
func cacheValidationResult(ctx context.Context, configHash, auxHash string) error {
	validationCache.mu.Lock()
	defer validationCache.mu.Unlock()
	if cause := context.Cause(ctx); cause != nil {
		return cause
	}

	validationCache.lastConfigHash = configHash
	validationCache.lastAuxHash = auxHash
	return nil
}
