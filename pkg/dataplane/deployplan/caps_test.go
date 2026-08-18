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

package deployplan_test

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"gitlab.com/haproxy-haptic/haptic/pkg/dataplane/agent/api"
	"gitlab.com/haproxy-haptic/haptic/pkg/dataplane/deployplan"
)

func TestCapsFor(t *testing.T) {
	tests := []struct {
		name      string
		version   string
		servers   bool
		backends  bool
		initState bool
	}{
		{name: "3.0 has dynamic servers only", version: "3.0.26", servers: true},
		{name: "3.1 adds init-state", version: "3.1.17", servers: true, initState: true},
		{name: "3.3 still has no dynamic backends", version: "3.3.13", servers: true, initState: true},
		{name: "3.4 has everything", version: "3.4.3", servers: true, backends: true, initState: true},
		{name: "a v prefix and a suffix are read", version: "v3.4.3-1ppa", servers: true, backends: true, initState: true},
		{name: "a major only is read", version: "4", servers: true, backends: true, initState: true},
		{name: "an older release has none", version: "2.9.0", servers: false},
		{name: "an unreadable version has none", version: "unknown", servers: false},
		{name: "an empty version has none", version: "", servers: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			caps := deployplan.CapsFor(tt.version, nil)

			assert.Equal(t, tt.servers, caps.DynamicServers)
			assert.Equal(t, tt.backends, caps.DynamicBackends)
			assert.Equal(t, tt.initState, caps.ServerInitState)
			assert.Nil(t, caps.AgentOps)
		})
	}
}

func TestCapsForAgentOps(t *testing.T) {
	caps := deployplan.CapsFor("3.4.3", []string{api.OpMapSet, api.OpMapAdd})

	assert.Equal(t, map[string]bool{api.OpMapSet: true, api.OpMapAdd: true}, caps.AgentOps)
}
