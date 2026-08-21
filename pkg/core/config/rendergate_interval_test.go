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

package config

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

// The render gate's duty-cycle knob: absent and unparsable values fall back to
// the default that keeps a 3000-route check clear of the admission webhook's
// budget.
func TestControllerConfig_GetRenderGateInterval(t *testing.T) {
	assert.Equal(t, DefaultRenderGateInterval, (&ControllerConfig{}).GetRenderGateInterval(),
		"empty RenderGateInterval falls back to DefaultRenderGateInterval")
	assert.Equal(t, 250*time.Millisecond,
		(&ControllerConfig{RenderGateInterval: "250ms"}).GetRenderGateInterval())
	assert.Equal(t, DefaultRenderGateInterval,
		(&ControllerConfig{RenderGateInterval: "garbage"}).GetRenderGateInterval(),
		"invalid RenderGateInterval falls back to DefaultRenderGateInterval")
}
