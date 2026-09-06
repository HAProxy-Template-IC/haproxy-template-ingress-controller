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

package controller

import (
	"context"
	"io"
	"log/slog"
	"reflect"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"gitlab.com/haproxy-haptic/haptic/pkg/controller/helpers"
	"gitlab.com/haproxy-haptic/haptic/pkg/controller/renderer"
	"gitlab.com/haproxy-haptic/haptic/pkg/controller/typebootstrap"
	"gitlab.com/haproxy-haptic/haptic/pkg/dataplane"
	busevents "gitlab.com/haproxy-haptic/haptic/pkg/events"
	"gitlab.com/haproxy-haptic/haptic/pkg/stores"
	"gitlab.com/haproxy-haptic/haptic/pkg/stores/storetest"
)

// Admission is a gate on the config the fleet will run. A webhook still
// branching on the controller image's own HAProxy admits objects whose
// production render is a different config that nothing checked — reachable on
// every rolling upgrade, and permanent with a pinned-back haproxy image.
func TestAdmissionRendersWithTheFleetsCapabilities(t *testing.T) {
	const template = `{% if capabilities.supports_crt_list %}{{ fail("the fleet cannot load a crt-list") }}{% end %}
global
    daemon

defaults
    mode http
    timeout connect 5s
    timeout client 50s
    timeout server 50s

frontend http_front
    bind *:8080
    default_backend http_back

backend http_back
    server srv1 127.0.0.1:80
`

	cfg := currentFilesAdmissionConfig(template)
	engine, err := helpers.NewEngineFromConfigWithOptions(cfg, nil, nil,
		helpers.BuildAdditionalDeclarations(cfg, &typebootstrap.Result{
			Types:  map[string]reflect.Type{},
			Errors: map[string]error{},
		}), helpers.EngineOptions{})
	require.NoError(t, err)

	// The controller image's own HAProxy is newer than the fleet's, which is
	// what a rolling upgrade looks like from here.
	capabilities := renderer.NewCapabilitiesFanout(dataplane.Capabilities{SupportsCrtList: true})
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	renderService := renderer.NewRenderService(&renderer.RenderServiceConfig{
		Engine:             engine,
		Config:             cfg,
		Logger:             logger,
		Capabilities:       capabilities.Capabilities(),
		TypedResourceTypes: map[string]reflect.Type{},
	})
	capabilities.Add(renderService)
	validator, err := createDryRunValidator(
		cfg,
		busevents.NewEventBus(100),
		stores.NewRealStoreProvider(map[string]stores.Store{"ingresses": &storetest.MockStore{}}),
		&reconciliationWiring{
			renderService:         renderService,
			publishedCurrentFiles: newPublishedAuxFiles("haptic"),
			gvrMapper:             ingressRESTMapper(),
		},
		nil,
		logger,
	)
	require.NoError(t, err)

	admit := func() (bool, string) {
		allowed, reason, _ := validator.ValidateDirect(context.Background(),
			"networking.k8s.io/v1.Ingress", "default", "example", admissionIngress(), nil, "CREATE")
		return allowed, reason
	}

	allowed, reason := admit()
	require.False(t, allowed, "the seeded capabilities take the branch this template rejects")
	assert.Contains(t, reason, "the fleet cannot load a crt-list")

	// Discovery reports the fleet's lowest version, and the deploy side
	// re-sources every render that feeds a gate from it.
	capabilities.SetCapabilities(dataplane.Capabilities{SupportsCrtList: false})

	allowed, reason = admit()
	assert.True(t, allowed, "admission must judge the config the fleet will run: %s", reason)
}
