// Copyright 2026 Philipp Hossner
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

package validator

import (
	"context"
	"fmt"
	"log/slog"
	"sort"
	"strings"
	"time"

	coreconfig "gitlab.com/haproxy-haptic/haptic/pkg/core/config"
)

// SyncFailures contains the complete config-validator verdict by validator.
type SyncFailures map[string][]string

// ValidateConfigSync applies the same four validators as the live
// scatter-gather before an iteration adopts a re-resolved config.
func ValidateConfigSync(
	ctx context.Context,
	cfg *coreconfig.Config,
	bootstrap TypeBootstrapper,
	runTimeout time.Duration,
	logger *slog.Logger,
) SyncFailures {
	failures := SyncFailures{}
	if errs := validateBasic(cfg); len(errs) > 0 {
		failures[ValidatorNameBasic] = errs
	}
	if errs := validateTemplates(ctx, cfg, bootstrap); len(errs) > 0 {
		failures[ValidatorNameTemplate] = errs
	}
	if errs := validateJSONPaths(cfg); len(errs) > 0 {
		failures[ValidatorNameJSONPath] = errs
	}

	result, err := RunValidationTestsSync(ctx, cfg, bootstrap, runTimeout, logger)
	switch {
	case err != nil:
		failures[ValidatorNameValidationTests] = []string{err.Error()}
	case result.Incomplete:
		failures[ValidatorNameValidationTests] = []string{"validationTests did not complete within the suite timeout"}
	case !result.Passed:
		failures[ValidatorNameValidationTests] = result.Failures
	}
	return failures
}

// Error returns a deterministic summary suitable for the startup error.
func (f SyncFailures) Error() string {
	if len(f) == 0 {
		return ""
	}
	names := make([]string, 0, len(f))
	for name := range f {
		names = append(names, name)
	}
	sort.Strings(names)
	parts := make([]string, 0, len(names))
	for _, name := range names {
		parts = append(parts, fmt.Sprintf("%s: %s", name, strings.Join(f[name], "; ")))
	}
	return strings.Join(parts, "; ")
}

// Flat returns the deterministic messages written to config status.
func (f SyncFailures) Flat() []string {
	if len(f) == 0 {
		return nil
	}
	names := make([]string, 0, len(f))
	for name := range f {
		names = append(names, name)
	}
	sort.Strings(names)
	flat := make([]string, 0, len(names))
	for _, name := range names {
		for _, failure := range f[name] {
			flat = append(flat, fmt.Sprintf("%s: %s", name, failure))
		}
	}
	return flat
}
