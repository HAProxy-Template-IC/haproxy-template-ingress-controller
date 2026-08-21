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
	"gitlab.com/haproxy-haptic/haptic/pkg/dataplane/parser"
)

// The phases only this check can fail in.
var (
	phaseSyntax = validationPhase{name: phaseNameSyntax, message: "configuration has syntax errors"}
	phaseSchema = validationPhase{name: "schema", message: "configuration violates API schema constraints"}
)

// ValidateSyntaxAndSchema parses a configuration and checks the parsed models
// against the pinned OpenAPI schema. It exists for the browser playground,
// which has no haproxy binary: `haproxy -c` is a strict superset of both checks
// and is what every other caller runs (ADR-0022). The `playground` build tag is
// what keeps a config parser out of every production binary.
func ValidateSyntaxAndSchema(config string, version *Version) (*parser.StructuredConfig, error) {
	parsedConfig, err := validateSyntax(config)
	if err != nil {
		return nil, phaseSyntax.wrap(err)
	}

	if err := validateAPISchema(parsedConfig, version); err != nil {
		return nil, phaseSchema.wrap(err)
	}

	return parsedConfig, nil
}
