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

package controller

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	coreconfig "gitlab.com/haproxy-haptic/haptic/pkg/core/config"
)

func TestBuildAndRegisterPluggableValidatorManagerRejectsMalformedGlob(t *testing.T) {
	setup := &componentSetup{}
	cfg := &coreconfig.Config{
		Validators: []coreconfig.ValidatorConfig{{
			Name:       "spoa-hub",
			SocketPath: "/var/run/haptic-validators/spoa-hub.sock",
			Files:      []string{"general/[broken"},
		}},
	}

	mgr, err := buildAndRegisterPluggableValidatorManager(setup, cfg, nil)
	require.Error(t, err)
	assert.Nil(t, mgr)
	assert.ErrorContains(t, err, "invalid file glob")
}
