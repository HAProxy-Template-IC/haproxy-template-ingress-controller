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

// cachedValidatorSlot lazily constructs a CachedValidator for one
// (major, minor) HAProxy version on first use and reuses it thereafter.
type cachedValidatorSlot struct {
	once  sync.Once
	cache *validators.CachedValidator
	major int
	minor int
}

// get returns the slot's CachedValidator, constructing it on first call.
func (s *cachedValidatorSlot) get() *validators.CachedValidator {
	s.once.Do(func() {
		s.cache = validators.NewCachedValidator(s.major, s.minor)
	})
	return s.cache
}

// Per-version validator slots. Allocation is deferred until first use, so
// instances that only ever see one HAProxy version pay the cost for that
// version only.
var (
	validatorSlotV30 = &cachedValidatorSlot{major: 3, minor: 0}
	validatorSlotV31 = &cachedValidatorSlot{major: 3, minor: 1}
	validatorSlotV32 = &cachedValidatorSlot{major: 3, minor: 2}
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
		return nil, fmt.Errorf("creating parser: %w", syntaxParserErr)
	}

	// Parse configuration - this validates syntax
	parsed, err := syntaxParser.ParseFromString(config)
	if err != nil {
		return nil, fmt.Errorf("syntax error: %w", err)
	}

	return parsed, nil
}

// getCachedValidatorForVersion returns the cached validator for a HAProxy
// version. Unknown or pre-3.x versions fall back to the v3.0 validator;
// versions newer than v3.2 fall back to the v3.2 validator (since that is the
// newest schema currently bundled).
func getCachedValidatorForVersion(version *Version) *validators.CachedValidator {
	if version == nil || version.Major < 3 {
		return validatorSlotV30.get()
	}
	if version.Major > 3 {
		return validatorSlotV32.get()
	}
	switch {
	case version.Minor >= 2:
		return validatorSlotV32.get()
	case version.Minor >= 1:
		return validatorSlotV31.get()
	default:
		return validatorSlotV30.get()
	}
}
