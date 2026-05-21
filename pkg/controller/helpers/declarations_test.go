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

package helpers

import (
	"reflect"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"gitlab.com/haproxy-haptic/haptic/pkg/controller/typebootstrap"
	"gitlab.com/haproxy-haptic/haptic/pkg/core/config"
)

// TestBuildAdditionalDeclarations_WithResult covers the happy path
// every production engine constructor takes: typebootstrap ran,
// each typed resource gets a *[]*<Generated> global, currentConfig
// is present unchanged.
func TestBuildAdditionalDeclarations_WithResult(t *testing.T) {
	gwType := reflect.StructOf([]reflect.StructField{
		{Name: "Metadata", Type: reflect.StructOf([]reflect.StructField{
			{Name: "Name", Type: reflect.TypeOf("")},
		})},
	})
	result := &typebootstrap.Result{
		Types:  map[string]reflect.Type{"gateways": gwType},
		Errors: map[string]error{},
	}

	decls := BuildAdditionalDeclarations(&config.Config{}, result)

	require.Contains(t, decls, "currentConfig",
		"static currentConfig declaration must be present in every consumer path")
	require.Contains(t, decls, "gateways",
		"typed-resource declaration must surface for every successful typebootstrap entry")

	// The declared shape MUST match what addTypedRenderContextEntries
	// produces at render time — a *[]*<gwType> — otherwise Scriggo's
	// runtime variable lookup mismatches the engine's compile-time
	// declaration.
	gwDecl := decls["gateways"]
	rv := reflect.ValueOf(gwDecl)
	require.Equal(t, reflect.Ptr, rv.Type().Kind(),
		"declared globals must be pointer-to-slice for Scriggo's typed-nil-pointer convention")
	require.Equal(t, reflect.Slice, rv.Type().Elem().Kind())
	assert.Equal(t, reflect.Ptr, rv.Type().Elem().Elem().Kind(),
		"slice element must be pointer-to-generated-type")
}

// TestBuildAdditionalDeclarations_NilResultPanics pins the
// contract that callers MUST provide a real typebootstrap.Result.
// The previous envelope-only fallback was removed because it
// false-positively rejected charts using typed Spec/Status
// access and silently bound them to a Metadata-only shape that
// would mismatch at render time. Callers that don't have a real
// Result yet (e.g. Stage-1 template validator) must obtain one
// via the injected TypeBootstrapper before calling this helper.
func TestBuildAdditionalDeclarations_NilResultPanics(t *testing.T) {
	assert.Panics(t, func() {
		BuildAdditionalDeclarations(&config.Config{}, nil)
	})
}
