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

package deployer

import (
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
)

// A pod's manifest names the plan THAT pod applied when the gate passed it, so
// a straggler's baseline promotion never waits on the newest render.
func TestValidatedPlanSet_ResolvesPerPod(t *testing.T) {
	set := newValidatedPlanSet()
	assert.Empty(t, set.resolve("plan-1"), "nothing is validated before the gate answers")

	set.add("plan-1")
	set.add("plan-2")
	assert.Equal(t, "plan-1", set.resolve("plan-1"), "a lagging pod is told its own passed plan")
	assert.Equal(t, "plan-2", set.resolve("plan-3"), "a pod on an unjudged plan is told the newest passed one")

	set.add("plan-1")
	assert.Equal(t, "plan-2", set.resolve("plan-3"), "re-recording a plan does not make it the newest")

	var newest string
	for i := range maxValidatedPlans {
		newest = fmt.Sprintf("filler-%d", i)
		set.add(newest)
	}
	assert.Equal(t, newest, set.resolve("plan-1"), "the set is bounded, and ages out the oldest first")
}
