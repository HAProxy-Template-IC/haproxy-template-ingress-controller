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

package events

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"gitlab.com/haproxy-haptic/haptic/pkg/dataplane/renderplan"
)

// Legacy constructors retain an independently owned plan copy.

func TestTemplateRenderedEvent_CarriesThePlan(t *testing.T) {
	plan := &renderplan.Plan{ID: "plan-abc"}

	event := NewTemplateRenderedEvent("cfg", nil, nil, nil, 0, 0, "", "checksum", plan, plan.ID, true)

	assert.Equal(t, plan, event.Plan)
	assert.NotSame(t, plan, event.Plan)
	assert.Equal(t, "plan-abc", event.PlanID)
}

func TestDeploymentScheduledEvent_CarriesThePlan(t *testing.T) {
	plan := &renderplan.Plan{ID: "plan-abc"}

	event := NewDeploymentScheduledEvent("cfg", nil, nil, "n", "ns", "r", "checksum",
		plan, plan.ID, nil, true)

	assert.Equal(t, plan, event.Plan)
	assert.NotSame(t, plan, event.Plan)
	assert.Equal(t, "plan-abc", event.PlanID)
}

func TestPlanlessRendersStayNil(t *testing.T) {
	rendered := NewTemplateRenderedEvent("cfg", nil, nil, nil, 0, 0, "", "", nil, "", true)
	scheduled := NewDeploymentScheduledEvent("cfg", nil, nil, "n", "ns", "r", "", nil, "", nil, true)

	// Admission and proposal renders carry no plan; nothing may dereference it.
	assert.Nil(t, rendered.Plan)
	assert.Empty(t, rendered.PlanID)
	assert.Nil(t, scheduled.Plan)
	assert.Empty(t, scheduled.PlanID)
}
