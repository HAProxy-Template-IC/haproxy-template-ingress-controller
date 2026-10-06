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

package renderer

import (
	"testing"

	"github.com/stretchr/testify/require"

	"gitlab.com/haproxy-haptic/haptic/pkg/templating"
)

func TestMembershipQueryKeyRegistersOnlyUngatedComponents(t *testing.T) {
	activation, err := templating.CompileExistenceJSONPath(`$.metadata.annotations["example.com/feature"]`)
	require.NoError(t, err)
	gated := incrementalComponent{name: "gated", activationPaths: []templating.ExistenceJSONPath{activation}}
	ungated := incrementalComponent{name: "ungated"}
	session := &incrementalRenderSession{
		state: &incrementalRenderState{components: map[string]incrementalComponent{
			gated.name: gated, ungated.name: ungated,
		}},
	}

	gatedKey := session.membershipQueryKey(&gated, "ingresses", "default", "web")
	require.Nil(t, session.componentQueries, "a gated member must not register before its activation evaluates it")
	require.Equal(t, componentQueryKey(&gated, "ingresses", "default", "web"), gatedKey)

	ungatedKey := session.membershipQueryKey(&ungated, "ingresses", "default", "web")
	_, registered := session.componentQueries.Lookup(session, ungatedKey)
	require.True(t, registered)
	_, registered = session.componentQueries.Lookup(session, gatedKey)
	require.False(t, registered)

	component, source, namespace, name, resolved := session.resolveComponentQuery(gatedKey)
	require.True(t, resolved)
	require.Equal(t, gated.name, component.name)
	require.Equal(t, []string{"ingresses", "default", "web"}, []string{source, namespace, name})
	_, registered = session.componentQueries.Lookup(session, gatedKey)
	require.True(t, registered, "resolving a gated query registers it")
}
