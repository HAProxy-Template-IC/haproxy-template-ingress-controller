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

package renderer

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestAssemblyReuseTransition_OscillationIsNotFirstSight(t *testing.T) {
	service := &RenderService{}

	changed, firstSight := service.assemblyReuseTransition("")
	assert.True(t, changed)
	assert.True(t, firstSight, "the first engaged state is news")

	changed, _ = service.assemblyReuseTransition("")
	assert.False(t, changed, "an unchanged state logs nothing")

	changed, firstSight = service.assemblyReuseTransition("post-process-not-identity")
	assert.True(t, changed)
	assert.True(t, firstSight, "the first fallback reason is news")

	// A non-identity post-process chain flips engaged↔fallback on every
	// content change; the oscillation must not repeat at Info.
	changed, firstSight = service.assemblyReuseTransition("")
	assert.True(t, changed)
	assert.False(t, firstSight)
	changed, firstSight = service.assemblyReuseTransition("post-process-not-identity")
	assert.True(t, changed)
	assert.False(t, firstSight)

	changed, firstSight = service.assemblyReuseTransition("section-unregistered")
	assert.True(t, changed)
	assert.True(t, firstSight, "a reason not seen before is news")
}
