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

//go:build playground

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
	var (
		parsed *parser.StructuredConfig
		err    error
	)
	parsed, err = syntaxParser.ParseFromString(config)
	if err != nil {
		return nil, fmt.Errorf("syntax error: %w", err)
	}

	return parsed, nil
}

// getValidatorForVersion returns the immutable validator set for a HAProxy
// version. Unknown or pre-3.x versions fall back to the v3.0 validator;
// versions newer than v3.3 fall back to the v3.3 validator (since that is the
// newest schema currently bundled).
func getValidatorForVersion(version *Version) *validators.ValidatorSet {
	if version == nil {
		return validators.ForVersion(3, 0)
	}
	return validators.ForVersion(version.Major, version.Minor)
}
