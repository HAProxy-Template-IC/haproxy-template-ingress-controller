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

// ValidateSemanticsContext checks the configuration with HAProxy; a nil gate uses the shared default.
func ValidateSemanticsContext(ctx context.Context, mainConfig string, auxFiles *AuxiliaryFiles, paths *ValidationPaths, skipDNSValidation bool, gate *CheckGate) error {
	if err := validateSemantics(ctx, mainConfig, auxFiles, paths, skipDNSValidation, gate); err != nil {
		return phaseSemantic.wrap(err)
	}
	return nil
}

// ValidateConfigurationContext checks isolated validation files with HAProxy; a nil gate uses the shared default.
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
