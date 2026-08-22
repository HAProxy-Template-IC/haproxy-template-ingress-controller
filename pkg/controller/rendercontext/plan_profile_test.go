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
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestPlanRegistryProfileContentAddressed(t *testing.T) {
	r := NewPlanRegistry(nil)

	// Same shape → same name, one registered section.
	a, err := r.Profile(map[string]any{"mode": "http", "balance": "roundrobin"})
	require.NoError(t, err)
	b, err := r.Profile(map[string]any{"mode": "http", "balance": "roundrobin"})
	require.NoError(t, err)
	assert.Equal(t, a, b, "identical shapes must share one profile name")
	assert.True(t, strings.HasPrefix(a, profileNamePrefix), "name is haptic-be-<hash>")

	// A different shape → a different name.
	c, err := r.Profile(map[string]any{"mode": "tcp"})
	require.NoError(t, err)
	assert.NotEqual(t, a, c)

	require.Len(t, r.sections, 2, "two distinct profiles registered, the repeat deduped")
}

func TestPlanRegistryProfileText(t *testing.T) {
	r := NewPlanRegistry(nil)
	name, err := r.Profile(map[string]any{
		"mode":          "http",
		"balance":       "roundrobin",
		"hashType":      "consistent",
		"defaultServer": []any{map[string]any{"name": "check"}, map[string]any{"name": "maxconn", "args": []any{"5"}}},
		"profile":       []any{"timeout connect 5s", "# a comment is dropped", "retries 3"},
	})
	require.NoError(t, err)

	text := r.sections[sectionKey{Kind: "profile", Name: name}]
	assert.Equal(t, "defaults "+name+" from "+baseProfileName+"\n"+
		"    mode http\n"+
		"    balance roundrobin\n"+
		"    hash-type consistent\n"+
		"    default-server check maxconn 5\n"+
		"    timeout connect 5s\n"+
		"    retries 3\n", text)
}

func TestPlanRegistryProfileRejectsUnknownKey(t *testing.T) {
	r := NewPlanRegistry(nil)
	_, err := r.Profile(map[string]any{"mode": "http", "balancee": "roundrobin"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "planRegistry.Profile")
}
