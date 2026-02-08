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
	"fmt"
	"sync"

	"gitlab.com/haproxy-haptic/haptic/pkg/dataplane/parser"
	"gitlab.com/haproxy-haptic/haptic/pkg/dataplane/validators"
)

// syntaxParser is a package-level singleton parser for syntax validation.
// Uses sync.Once to ensure it's only created once and reused across all calls
// to validateSyntax(). The parser is already protected by parserMutex in the
// parser package, so sharing is thread-safe.
var (
	syntaxParser     *parser.Parser
	syntaxParserOnce sync.Once
	syntaxParserErr  error
)

// cachedValidator provides zero-allocation validation with caching.
// It is initialized lazily on first use.
var (
	cachedValidatorV30 *validators.CachedValidator
	cachedValidatorV31 *validators.CachedValidator
	cachedValidatorV32 *validators.CachedValidator
	validatorOnceV30   sync.Once
	validatorOnceV31   sync.Once
	validatorOnceV32   sync.Once
)

// validateSyntax performs syntax validation using client-native parser.
// Returns the parsed configuration for use in Phase 1.5 (API schema validation).
// Uses a package-level singleton parser to avoid re-initializing parser internals
// on every call.
func validateSyntax(config string) (*parser.StructuredConfig, error) {
	// Get or create singleton parser
	syntaxParserOnce.Do(func() {
		syntaxParser, syntaxParserErr = parser.New()
	})
	if syntaxParserErr != nil {
		return nil, fmt.Errorf("failed to create parser: %w", syntaxParserErr)
	}

	// Parse configuration - this validates syntax
	parsed, err := syntaxParser.ParseFromString(config)
	if err != nil {
		return nil, fmt.Errorf("syntax error: %w", err)
	}

	return parsed, nil
}

// getCachedValidatorForVersion returns the cached validator for a HAProxy version.
func getCachedValidatorForVersion(version *Version) *validators.CachedValidator {
	if version == nil {
		// Default to v3.0
		validatorOnceV30.Do(func() {
			cachedValidatorV30 = validators.NewCachedValidator(3, 0)
		})
		return cachedValidatorV30
	}

	if version.Major == 3 {
		switch {
		case version.Minor >= 2:
			validatorOnceV32.Do(func() {
				cachedValidatorV32 = validators.NewCachedValidator(3, 2)
			})
			return cachedValidatorV32
		case version.Minor >= 1:
			validatorOnceV31.Do(func() {
				cachedValidatorV31 = validators.NewCachedValidator(3, 1)
			})
			return cachedValidatorV31
		default:
			validatorOnceV30.Do(func() {
				cachedValidatorV30 = validators.NewCachedValidator(3, 0)
			})
			return cachedValidatorV30
		}
	}

	if version.Major > 3 {
		validatorOnceV32.Do(func() {
			cachedValidatorV32 = validators.NewCachedValidator(3, 2)
		})
		return cachedValidatorV32
	}

	validatorOnceV30.Do(func() {
		cachedValidatorV30 = validators.NewCachedValidator(3, 0)
	})
	return cachedValidatorV30
}
