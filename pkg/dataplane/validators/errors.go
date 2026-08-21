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

// Package validators provides zero-allocation OpenAPI validation for HAProxy models.
//
// This package contains generated validators that work directly on client-native
// structs, avoiding the ~25GB allocation overhead of JSON marshal/unmarshal cycles
// that occurs when using the generic kin-openapi validator.
//
// The validators are generated from the pinned OpenAPI specs under
// cmd/gen-validators/spec/ and cover HAProxy versions 3.0 to 3.3.
// The generated code itself lives in pkg/generated/validators; this package
// wraps it with a version-dispatching ValidatorSet and a caching layer.
//
// Usage:
//
//	cache := validators.NewCache()
//	validatorSet := validators.ForVersion(3, 2)
//
//	// Validate with caching
//	err := cache.ValidateServer(server, validatorSet)
//
//	// Or validate without caching
//	err := validatorSet.ValidateServer(server)
package validators

import (
	genvalidators "gitlab.com/haproxy-haptic/haptic/pkg/generated/validators"
)

// FieldError represents an OpenAPI validation failure for a specific field.
//
// Aliased from pkg/generated/validators so the public API of this package stays
// unchanged after the generated code was split into its own subpackage. Callers
// that construct or type-assert *validators.FieldError continue to work.
type FieldError = genvalidators.FieldError
