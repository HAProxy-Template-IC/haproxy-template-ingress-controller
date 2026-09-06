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
	"reflect"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"gitlab.com/haproxy-haptic/haptic/pkg/controller/helpers"
	"gitlab.com/haproxy-haptic/haptic/pkg/controller/rendercontext"
	"gitlab.com/haproxy-haptic/haptic/pkg/controller/testutil"
	"gitlab.com/haproxy-haptic/haptic/pkg/controller/typebootstrap"
	"gitlab.com/haproxy-haptic/haptic/pkg/core/config"
)

func TestAttemptExtraContextSharesGuardedCopyUntilConfigChanges(t *testing.T) {
	cfg := &config.Config{
		Dataplane: testDataplaneConfig(),
		TemplatingSettings: config.TemplatingSettings{ExtraContext: map[string]any{
			"nested": map[string]any{"value": "v1"},
		}},
		HAProxyConfig: config.HAProxyConfig{Template: `value={{ extraContext["nested"]["value"] }}`},
	}
	declarations := helpers.BuildAdditionalDeclarations(cfg, &typebootstrap.Result{
		Types: map[string]reflect.Type{}, Kinds: map[string]string{}, Errors: map[string]error{},
	})
	engine, err := helpers.NewEngineFromConfigWithOptions(cfg, nil, nil, declarations, helpers.EngineOptions{})
	require.NoError(t, err)
	_, logger := testutil.NewTestBusAndLogger()
	service := NewRenderService(&RenderServiceConfig{Engine: engine, Config: cfg, Logger: logger})
	require.NotNil(t, service.exactCycleProgram)
	reconcile := []rendercontext.RenderMode{rendercontext.RenderModeReconcile}
	identity := func(value map[string]any) uintptr { return reflect.ValueOf(value).Pointer() }

	first, firstCertificate, err := service.attemptExtraContext(reconcile)
	require.NoError(t, err)
	require.NotNil(t, firstCertificate)
	assert.True(t, firstCertificate.Guards(first))
	assert.NotEqual(t, identity(cfg.TemplatingSettings.ExtraContext), identity(first))

	second, secondCertificate, err := service.attemptExtraContext(reconcile)
	require.NoError(t, err)
	assert.Equal(t, identity(first), identity(second))
	assert.Same(t, firstCertificate, secondCertificate)

	admission, admissionCertificate, err := service.attemptExtraContext(
		[]rendercontext.RenderMode{rendercontext.RenderModeAdmission},
	)
	require.NoError(t, err)
	assert.Nil(t, admissionCertificate)
	assert.NotEqual(t, identity(first), identity(admission))
	assert.Equal(t, first, admission)

	cfg.TemplatingSettings.ExtraContext["nested"].(map[string]any)["value"] = "v2"
	third, thirdCertificate, err := service.attemptExtraContext(reconcile)
	require.NoError(t, err)
	assert.NotEqual(t, identity(first), identity(third))
	assert.NotSame(t, firstCertificate, thirdCertificate)
	assert.Equal(t, "v2", third["nested"].(map[string]any)["value"])
	assert.Equal(t, "v1", first["nested"].(map[string]any)["value"])
}
