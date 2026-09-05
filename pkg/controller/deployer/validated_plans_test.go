// Copyright 2026 Philipp Hossner
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0

package deployer

import (
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestValidatedPlanSetResolvesOnlyExactOccurrence(t *testing.T) {
	set := newValidatedPlanSet()
	plan1 := exactTestPlan("collision", "config-A")
	plan2 := exactTestPlan("collision", "config-B")
	occurrence1 := mustOccurrenceFor(plan1, "config-A", nil, nil)
	occurrence2 := mustOccurrenceFor(plan2, "config-B", nil, nil)
	set.addOccurrence(occurrence1)

	assert.Equal(t, planReference{id: plan1.ID, proof: "agent-proof-1"},
		set.resolve(plan1.ID, "agent-proof-1", plan1, occurrence1))
	assert.Empty(t, set.resolve(plan2.ID, "agent-proof-1", plan2, occurrence2))
	assert.Equal(t, planReference{id: plan1.ID, proof: "agent-proof-2"},
		set.resolve(plan1.ID, "agent-proof-2", plan1, occurrence1))
}

func TestValidatedPlanSetBoundsExactOccurrences(t *testing.T) {
	set := newValidatedPlanSet()
	var oldestOccurrence, newestOccurrence = mustTestOccurrence("config-0", "plan-0", nil), mustTestOccurrence("config-new", "plan-new", nil)
	set.addOccurrence(oldestOccurrence)
	for i := 1; i < maxValidatedPlans; i++ {
		set.addOccurrence(mustTestOccurrence(fmt.Sprintf("config-%d", i), fmt.Sprintf("plan-%d", i), nil))
	}
	set.addOccurrence(newestOccurrence)
	oldest, err := materializeOccurrence(oldestOccurrence)
	assert.NoError(t, err)
	newest, err := materializeOccurrence(newestOccurrence)
	assert.NoError(t, err)

	assert.Len(t, set.order, maxValidatedPlans)
	assert.Empty(t, set.resolve(oldest.planID, "proof-0", oldest.plan, oldestOccurrence))
	assert.Equal(t, planReference{id: newest.planID, proof: "proof-new"},
		set.resolve(newest.planID, "proof-new", newest.plan, newestOccurrence))
}
