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
	"errors"
	"fmt"

	"gitlab.com/haproxy-haptic/haptic/pkg/controller/events"
	"gitlab.com/haproxy-haptic/haptic/pkg/controller/rendercycle"
	"gitlab.com/haproxy-haptic/haptic/pkg/dataplane/renderoutput"
	"gitlab.com/haproxy-haptic/haptic/pkg/dataplane/renderplan"
	"gitlab.com/haproxy-haptic/haptic/pkg/templating"
)

type renderOccurrenceIdentity struct {
	occurrence    *rendercycle.Occurrence
	cycle         *rendercycle.Snapshot
	output        *renderoutput.Snapshot
	planSnapshot  *renderplan.Snapshot
	plan          *renderplan.Plan
	statusPatches *templating.StatusPatchSnapshot
	proof         string
	config        string
	planID        string
	checksum      string
}

func inspectOccurrence(occurrence *rendercycle.Occurrence) (renderOccurrenceIdentity, error) {
	if occurrence == nil {
		return renderOccurrenceIdentity{}, errors.New("render occurrence is nil")
	}
	if err := occurrence.ValidateAuthentication(); err != nil {
		return renderOccurrenceIdentity{}, fmt.Errorf("render occurrence: %w", err)
	}
	cycle, err := occurrence.Snapshot()
	if err != nil {
		return renderOccurrenceIdentity{}, fmt.Errorf("render occurrence cycle: %w", err)
	}
	proof, err := occurrence.Proof()
	if err != nil {
		return renderOccurrenceIdentity{}, fmt.Errorf("render occurrence proof: %w", err)
	}
	output, err := cycle.OutputSnapshot()
	if err != nil {
		return renderOccurrenceIdentity{}, fmt.Errorf("render occurrence output: %w", err)
	}
	planSnapshot, err := output.PlanSnapshot()
	if err != nil {
		return renderOccurrenceIdentity{}, fmt.Errorf("render occurrence plan: %w", err)
	}
	statusPatches, err := cycle.StatusPatchSnapshot()
	if err != nil {
		return renderOccurrenceIdentity{}, fmt.Errorf("render occurrence status patches: %w", err)
	}
	config, err := output.Config()
	if err != nil {
		return renderOccurrenceIdentity{}, fmt.Errorf("render occurrence config: %w", err)
	}
	planID, err := output.PlanID()
	if err != nil {
		return renderOccurrenceIdentity{}, fmt.Errorf("render occurrence plan ID: %w", err)
	}
	checksum, err := cycle.ContentChecksum()
	if err != nil {
		return renderOccurrenceIdentity{}, fmt.Errorf("render occurrence checksum: %w", err)
	}
	snapshotPlanID, err := planSnapshot.ID()
	if err != nil || snapshotPlanID != planID {
		return renderOccurrenceIdentity{}, errors.New("render occurrence plan is inconsistent")
	}
	return renderOccurrenceIdentity{
		occurrence: occurrence, cycle: cycle, output: output, planSnapshot: planSnapshot,
		statusPatches: statusPatches, proof: proof, config: config,
		planID: planID, checksum: checksum,
	}, nil
}

func materializeOccurrence(occurrence *rendercycle.Occurrence) (renderOccurrenceIdentity, error) {
	identity, err := inspectOccurrence(occurrence)
	if err != nil {
		return renderOccurrenceIdentity{}, err
	}
	plan, err := identity.planSnapshot.LegacyCopy()
	if err != nil {
		return renderOccurrenceIdentity{}, fmt.Errorf("render occurrence plan: %w", err)
	}
	if plan == nil || plan.ID != identity.planID || !exactPlan(plan, plan) {
		return renderOccurrenceIdentity{}, errors.New("render occurrence plan is inconsistent")
	}
	identity.plan = plan
	return identity, nil
}

type occurrenceCarrier interface {
	RenderOccurrence() (*rendercycle.Occurrence, error)
}

func eventOccurrence(event occurrenceCarrier, kind string) (*rendercycle.Occurrence, error) {
	if event == nil {
		return nil, fmt.Errorf("%s is nil", kind)
	}
	occurrence, err := event.RenderOccurrence()
	if err != nil {
		return nil, fmt.Errorf("%s occurrence: %w", kind, err)
	}
	if err := occurrence.ValidateAuthentication(); err != nil {
		return nil, fmt.Errorf("%s occurrence: %w", kind, err)
	}
	return occurrence, nil
}

func templateEventOccurrence(event *events.TemplateRenderedEvent) (*rendercycle.Occurrence, error) {
	return eventOccurrence(event, "template render")
}

func gateEventOccurrence(event *events.RenderGateCompletedEvent) (*rendercycle.Occurrence, error) {
	return eventOccurrence(event, "render gate")
}

func scheduledEventOccurrence(event *events.DeploymentScheduledEvent) (*rendercycle.Occurrence, error) {
	return eventOccurrence(event, "scheduled deployment")
}

func completedEventOccurrence(event *events.DeploymentCompletedEvent) (*rendercycle.Occurrence, error) {
	return eventOccurrence(event, "completed deployment")
}

func sameOccurrence(left, right *rendercycle.Occurrence) bool {
	if left == nil || right == nil {
		return false
	}
	same, err := left.Same(right)
	return err == nil && same
}

func sameOccurrenceOutput(left, right *rendercycle.Occurrence) bool {
	leftIdentity, leftErr := inspectOccurrence(left)
	rightIdentity, rightErr := inspectOccurrence(right)
	if leftErr != nil || rightErr != nil {
		return false
	}
	same, err := leftIdentity.output.SameRoot(rightIdentity.output)
	return err == nil && same
}

func deploymentConfigBytes(event *events.DeploymentScheduledEvent) int {
	occurrence, err := scheduledEventOccurrence(event)
	if err != nil {
		return 0
	}
	identity, err := inspectOccurrence(occurrence)
	if err != nil {
		return 0
	}
	return len(identity.config)
}

func deploymentPlanID(event *events.DeploymentScheduledEvent) string {
	occurrence, err := scheduledEventOccurrence(event)
	if err != nil {
		return ""
	}
	identity, err := inspectOccurrence(occurrence)
	if err != nil {
		return ""
	}
	return identity.planID
}

func deploymentContentChecksum(event *events.DeploymentScheduledEvent) string {
	occurrence, err := scheduledEventOccurrence(event)
	if err != nil {
		return ""
	}
	identity, err := inspectOccurrence(occurrence)
	if err != nil {
		return ""
	}
	return identity.checksum
}

func completedContentChecksum(event *events.DeploymentCompletedEvent) string {
	occurrence, err := completedEventOccurrence(event)
	if err != nil {
		return ""
	}
	identity, err := inspectOccurrence(occurrence)
	if err != nil {
		return ""
	}
	return identity.checksum
}
