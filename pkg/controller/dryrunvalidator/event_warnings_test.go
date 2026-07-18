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

package dryrunvalidator

import (
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"

	"gitlab.com/haproxy-haptic/haptic/pkg/templating"
)

func TestFormatRenderedEventWarnings(t *testing.T) {
	t.Run("warnings formatted, normals skipped", func(t *testing.T) {
		out := formatRenderedEventWarnings([]templating.RenderedEvent{
			{Type: templating.EventTypeWarning, Reason: "WafPermissionDenied", Kind: "Ingress", Namespace: "default", Name: "app", Message: "allowCustomRules=false"},
			{Type: "Normal", Reason: "Synced", Kind: "Ingress", Namespace: "default", Name: "app", Message: "ok"},
		})
		assert.Equal(t, []string{"WafPermissionDenied on Ingress default/app: allowCustomRules=false"}, out)
	})

	t.Run("cluster-scoped subject has no namespace prefix", func(t *testing.T) {
		out := formatRenderedEventWarnings([]templating.RenderedEvent{
			{Type: templating.EventTypeWarning, Reason: "R", Kind: "GatewayClass", Name: "haptic", Message: "m"},
		})
		assert.Equal(t, []string{"R on GatewayClass haptic: m"}, out)
	})

	t.Run("capped with suppression summary", func(t *testing.T) {
		var events []templating.RenderedEvent
		for i := 0; i < maxEventWarnings+3; i++ {
			events = append(events, templating.RenderedEvent{Type: templating.EventTypeWarning, Reason: "R", Kind: "Ingress", Namespace: "ns", Name: fmt.Sprintf("app-%d", i), Message: "m"})
		}
		out := formatRenderedEventWarnings(events)
		assert.Len(t, out, maxEventWarnings+1)
		assert.Contains(t, out[maxEventWarnings], "3 more warnings")
	})
}
