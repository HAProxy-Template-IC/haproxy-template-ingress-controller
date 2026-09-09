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

package statuspatchprojection_test

import (
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	projection "gitlab.com/haproxy-haptic/haptic/pkg/templating/internal/statuspatchprojection"
)

func TestPlanDeltaAuthenticatesBothRootsAndPropagatesVisitorErrors(t *testing.T) {
	previousOwner, currentOwner := new(int), new(int)
	previous, err := projection.NewPlan(previousOwner)
	require.NoError(t, err)
	leafOwner, leaf := newProjection(t, "leaf", projectionInputFor("route", "uid", "rv", "rendered"))
	current, err := previous.Replace(previousOwner, currentOwner, "routes", leaf, leafOwner)
	require.NoError(t, err)
	var targets []projection.Metadata
	collect := func(target projection.Metadata) error {
		targets = append(targets, target)
		return nil
	}
	require.NoError(t, current.VisitChangedTargets(currentOwner, previous, previousOwner, collect))
	require.Len(t, targets, 1)
	assert.Equal(t, "route", targets[0].Name)

	copied := *current
	require.Error(t, copied.VisitChangedTargets(currentOwner, previous, previousOwner, collect))
	require.Error(t, current.VisitChangedTargets(currentOwner, &copied, currentOwner, collect))
	require.Error(t, current.VisitChangedTargets(previousOwner, previous, previousOwner, collect))
	require.Error(t, current.VisitChangedTargets(currentOwner, previous, currentOwner, collect))
	require.Error(t, current.VisitChangedTargets(currentOwner, previous, previousOwner, nil))
	visitorErr := errors.New("visitor failed")
	require.ErrorIs(t, current.VisitChangedTargets(currentOwner, previous, previousOwner, func(projection.Metadata) error {
		return visitorErr
	}), visitorErr)
	visits := 0
	visitPatch := func(patch projection.PlanPatch) error {
		visits++
		assert.Same(t, leaf, patch.Group.Root)
		return visitorErr
	}
	require.ErrorIs(t, current.VisitTargetPatches(currentOwner, &targets[0], visitPatch), visitorErr)
	assert.Equal(t, 1, visits)
	require.Error(t, current.VisitTargetPatches(currentOwner, &targets[0], nil))
	require.Error(t, current.VisitTargetPatches(currentOwner, nil, visitPatch))
	require.Error(t, copied.VisitTargetPatches(currentOwner, &targets[0], visitPatch))
	require.NoError(t, current.VisitTargetPatches(currentOwner, &projection.Metadata{Name: "absent"}, visitPatch))
	assert.Equal(t, 1, visits)
	targets = nil
	require.NoError(t, previous.VisitChangedTargets(previousOwner, current, currentOwner, collect))
	require.Len(t, targets, 1)
	assert.Equal(t, "route", targets[0].Name)
}
