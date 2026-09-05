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

package deployer

import (
	"testing"

	"github.com/stretchr/testify/require"

	"gitlab.com/haproxy-haptic/haptic/pkg/dataplane/agent/api"
	"gitlab.com/haproxy-haptic/haptic/pkg/dataplane/deployplan"
)

// An agent reports the plan it holds, which is not always the one the apply
// sent: a revert lands the last known good set, and a baseline invalidated
// mid-apply clears the applied plan outright. Both answer OK with a different
// -- or empty -- id. Reading that as a proof fault failed the apply as
// retryable and burned the rollback window: the corrupt-certificate e2e test
// gives the fleet 60s to come back and the retries outlasted it.
func TestBindApplyResultIgnoresAPlanTheAgentDidNotApply(t *testing.T) {
	component := &Component{plans: newPlanCache()}
	attempt := &podApply{req: &deployRequest{planID: "wanted"}}
	decision := &deployplan.Decision{}

	cases := []struct {
		name    string
		applied string
	}{
		{name: "reverted to another plan", applied: "last-known-good"},
		{name: "baseline invalidated", applied: ""},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			require.NoError(t, component.bindApplyResult(attempt, "pod-a", decision, &api.ApplyResult{
				OK:               true,
				AppliedPlanID:    test.applied,
				AppliedPlanProof: "a:1",
			}))
		})
	}
}
