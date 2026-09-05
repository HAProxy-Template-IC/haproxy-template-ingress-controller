// Copyright 2026 Philipp Hossner
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
// http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package main

import (
	"testing"

	"github.com/stretchr/testify/require"

	"gitlab.com/haproxy-haptic/haptic/pkg/templating"
)

func TestBundledChartIncrementalVectorCarrierEligibility(t *testing.T) {
	_, setup, _, cleanup := bundledChartSetup(t)
	defer cleanup()

	renderer, ok := setup.Engine.(templating.IncrementalComponentVectorCarrierWavesRenderer)
	require.True(t, ok, "bundled engine does not expose the vector carrier protocol")
	diagnostics, ok := setup.Engine.(interface {
		IncrementalComponentVectorCarrierDiagnostic() error
	})
	require.True(t, ok, "bundled engine does not expose vector carrier diagnostics")
	eligibility, available := renderer.IncrementalComponentVectorCarrierEligibility()
	require.Truef(
		t,
		available,
		"bundled vector carrier rejected every entrypoint: %v",
		diagnostics.IncrementalComponentVectorCarrierDiagnostic(),
	)
	require.NotEmpty(t, eligibility.TemplateNames)
	require.NotEmpty(t, eligibility.BindingNames)
}
