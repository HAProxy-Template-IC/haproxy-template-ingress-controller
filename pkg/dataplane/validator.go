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
	"log/slog"
	"time"
)

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

// ValidateSemantics asks the HAProxy binary whether it can load the configuration.
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

	start := time.Now()
	if err := validateSemantics(ctx, mainConfig, auxFiles, paths, skipDNSValidation, gate); err != nil {
		return phaseSemantic.wrap(err)
	}
	slog.Debug("Validation completed", "semantic_ms", time.Since(start).Milliseconds())

	if cause := context.Cause(ctx); cause != nil {
		return cause
	}
	return nil
}
