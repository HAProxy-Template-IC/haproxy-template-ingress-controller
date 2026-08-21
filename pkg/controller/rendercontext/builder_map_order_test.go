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

package rendercontext

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"gitlab.com/haproxy-haptic/haptic/pkg/controller/testutil"
	"gitlab.com/haproxy-haptic/haptic/pkg/core/config"
	"gitlab.com/haproxy-haptic/haptic/pkg/dataplane"
	"gitlab.com/haproxy-haptic/haptic/pkg/dataplane/auxiliaryfiles"
	"gitlab.com/haproxy-haptic/haptic/pkg/templating"
)

// The declaration has to happen where the registry is created, because the
// validation-test runner builds its plan straight off the registry and never
// passes through the reconcile renderer. Declaring it at one call site made
// every static map read back as ordered through the other.
func TestBuilder_Build_DeclaresStaticMapOrder(t *testing.T) {
	ordered := true
	unordered := false

	cfg := &config.Config{
		Maps: map[string]config.MapFile{
			"host.map":       {Ordered: &unordered},
			"path-regex.map": {Ordered: &ordered},
			"undeclared.map": {},
		},
	}
	res := NewBuilder(t.Context(), cfg, &templating.PathResolver{MapsDir: "/etc/haproxy/maps"}, testutil.NewTestLogger()).Build()
	require.NotNil(t, res.PlanRegistry)

	plan, err := res.PlanRegistry.Plan("global\n", &dataplane.AuxiliaryFiles{
		MapFiles: []auxiliaryfiles.MapFile{
			{Path: "host.map", Content: "a b\n"},
			{Path: "path-regex.map", Content: "a b\n"},
			{Path: "undeclared.map", Content: "a b\n"},
		},
	})
	require.NoError(t, err)

	byName := map[string]bool{}
	for path, m := range plan.Maps {
		byName[path] = m.Ordered
	}

	assert.False(t, byName["/etc/haproxy/maps/host.map"], "ordered:false must reach the plan")
	assert.True(t, byName["/etc/haproxy/maps/path-regex.map"], "ordered:true must reach the plan")
	assert.True(t, byName["/etc/haproxy/maps/undeclared.map"], "an undeclared map is ordered, the safe default")
}
