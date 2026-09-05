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

package templating

import (
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// A component that calls one closure from inside another crashed the render with
// "interface conversion: ... is map[string]templating.ResourceStore, not
// *runtime.callable" -- the inner call found a global where the captured closure
// should be. It reached a cluster: the webhook handler panicked, net/http
// dropped the connection, and every apply against that gateway came back
// "failed calling webhook ... EOF".
//
// The chart shape is util-publish-gateway-route-filter-maps: publishLine is
// declared once per render, markAny closes over it, and the route loop calls
// markAny.
func TestIncrementalComponentVectorCallsAClosureCapturedByAnotherClosure(t *testing.T) {
	engine := newIncrementalVectorTestEngine(t, `{%%
		var lines = []string{}
		for _, ruleKey := range []string{"0", "1"} {
			var routeID = source + "_" + ruleKey
			var sequence = 0
			var publishLine = func(cell string, line string) {
				sequence++
				lines = append(lines, cell + ":" + line)
			}
			var anySeen = map[string]bool{}
			var publishModifiers = func(bucket string) {
				var markAny = func() {
					var key = routeID + "|" + bucket
					if !anySeen[key] {
						anySeen[key] = true
						publishLine(bucket, routeID + "|any")
					}
				}
				markAny()
			}
			publishModifiers("req")
		}
	%%}{{ join(lines, ",") }}`)

	input := newIncrementalVectorTestInput(t, engine, 3, func(index int, values map[string]any) {
		values["source"] = fmt.Sprintf("source-%d", index)
	})
	lifecycle := input.Lifecycle.(*incrementalVectorTestLifecycle)

	require.NoError(t, engine.RenderIncrementalComponentVector(t.Context(), "component", input))

	assert.Equal(t,
		[]string{
			"req:source-0_0|any,req:source-0_1|any",
			"req:source-1_0|any,req:source-1_1|any",
			"req:source-2_0|any,req:source-2_1|any",
		},
		lifecycle.outputs,
		"each lane must call its own captured closure")
}
