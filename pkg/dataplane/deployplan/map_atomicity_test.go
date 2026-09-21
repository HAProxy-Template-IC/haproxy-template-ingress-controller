// Copyright 2026 Philipp Hossner
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
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"gitlab.com/haproxy-haptic/haptic/pkg/dataplane/agent/api"
	"gitlab.com/haproxy-haptic/haptic/pkg/dataplane/deployplan"
	"gitlab.com/haproxy-haptic/haptic/pkg/dataplane/renderplan"
)

func TestMapValueReplacementNeverRemovesAuthenticationBetweenCommands(t *testing.T) {
	before := basePlan(withMap(renderplan.Map{Path: routeMap, Entries: []renderplan.Entry{entry("protected", "require old-key")}}))
	after := basePlan(withMap(renderplan.Map{Path: routeMap, Entries: []renderplan.Entry{entry("protected", "require new-key")}}))
	decision := deployplan.Diff(after, withMapsLoaded(on34(before), routeMap))
	require.Equal(t, deployplan.VerdictRuntime, decision.Verdict)
	assert.Equal(t, []string{api.OpMapReplace}, kinds(decision.Ops))
}

func TestInPlaceMapBatchIsNotTruncated(t *testing.T) {
	beforeEntries := make([]renderplan.Entry, api.MaxOpsPerApply+1)
	afterEntries := make([]renderplan.Entry, len(beforeEntries))
	for i := range beforeEntries {
		key := fmt.Sprintf("key-%04d", i)
		beforeEntries[i] = entry(key, "before")
		afterEntries[i] = entry(key, "after")
	}
	before := basePlan(withMap(renderplan.Map{Path: routeMap, Entries: beforeEntries}))
	after := basePlan(withMap(renderplan.Map{Path: routeMap, Entries: afterEntries}))
	baseline := withMapsLoaded(on34(before), routeMap)
	baseline.WorkerOps, baseline.ReloadPending = before, true
	decision := deployplan.Diff(after, baseline)
	assert.Empty(t, decision.InPlace)
	assert.Nil(t, decision.WorkerPlan)
}

func TestInPlaceMapsKeepDenialUntilAllEnforcementCanApply(t *testing.T) {
	for _, structural := range []bool{false, true} {
		name := "new map dependency"
		if structural {
			name = "new frontend enforcement"
		}
		t.Run(name, func(t *testing.T) {
			before := basePlan(
				withMap(renderplan.Map{Path: "maps/denied.map", Entries: []renderplan.Entry{entry("protected", "unavailable")}}),
				withMap(renderplan.Map{Path: "maps/authentication.map"}),
			)
			after := basePlan(
				withMap(renderplan.Map{Path: "maps/denied.map"}),
				withMap(renderplan.Map{Path: "maps/authentication.map", Entries: []renderplan.Entry{entry("protected", "require credentials")}}),
			)
			if structural {
				after.Sections = append(after.Sections, renderplan.Section{Kind: renderplan.SectionKindCore, Name: "authentication", TextDigest: "verify-credentials"})
			}
			baseline := withMapsLoaded(on34(before), "maps/denied.map", "maps/authentication.map")
			baseline.WorkerOps = before
			baseline.ReloadPending = true
			decision := deployplan.Diff(after, baseline)
			assert.Empty(t, decision.InPlace)
			assert.Nil(t, decision.WorkerPlan)
		})
	}
}
