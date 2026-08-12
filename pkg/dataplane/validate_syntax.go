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
	validatorSlotV33 = &cachedValidatorSlot{major: 3, minor: 3}
)

// validateSyntax performs syntax validation using client-native parser.
// Returns the parsed configuration for use in Phase 1.5 (API schema validation).
//
// The parser is constructed per call and discarded. Reusing one kept
// client-native's internal maps and slices sized for the largest configuration
// ever parsed — measured at ~200 MB resident on a controller whose live
// configuration was 70 KiB — because neither shrinks. Construction is 89µs
// against a 44ms parse, so the retention bought 0.2%.
func validateSyntax(config string) (*parser.StructuredConfig, error) {
	syntaxParser, err := parser.New()
	if err != nil {
		return nil, fmt.Errorf("creating parser: %w", err)
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
// versions newer than v3.3 fall back to the v3.3 validator (since that is the
// newest schema currently bundled).
func getCachedValidatorForVersion(version *Version) *validators.CachedValidator {
	if version == nil || version.Major < 3 {
		return validatorSlotV30.get()
	}
	if version.Major > 3 {
		return validatorSlotV33.get()
	}
	switch {
	case version.Minor >= 3:
		return validatorSlotV33.get()
	case version.Minor >= 2:
		return validatorSlotV32.get()
	case version.Minor >= 1:
		return validatorSlotV31.get()
	default:
		return validatorSlotV30.get()
	}
}
