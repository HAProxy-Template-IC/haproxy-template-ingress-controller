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
	"context"
	"io"
	"log/slog"
	"reflect"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"k8s.io/apimachinery/pkg/api/meta"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"

	"gitlab.com/haproxy-haptic/haptic/pkg/controller/helpers"
	"gitlab.com/haproxy-haptic/haptic/pkg/controller/renderer"
	"gitlab.com/haproxy-haptic/haptic/pkg/controller/testutil"
	"gitlab.com/haproxy-haptic/haptic/pkg/controller/typebootstrap"
	coreconfig "gitlab.com/haproxy-haptic/haptic/pkg/core/config"
	"gitlab.com/haproxy-haptic/haptic/pkg/dataplane"
	"gitlab.com/haproxy-haptic/haptic/pkg/dataplane/auxiliaryfiles"
	busevents "gitlab.com/haproxy-haptic/haptic/pkg/events"
	"gitlab.com/haproxy-haptic/haptic/pkg/stores"
	"gitlab.com/haproxy-haptic/haptic/pkg/stores/storetest"
	"gitlab.com/haproxy-haptic/haptic/pkg/templating"
)

func TestAdmissionCurrentFilesUsesPublishedSnapshotOnEveryReplica(t *testing.T) {
	const template = `{% if currentFiles["gate"] == "deny" && len(resources.ingresses.List()) > 0 %}{{ fail("published currentFiles denied proposal") }}{% end %}
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

	tests := []struct {
		name      string
		published string
		accepted  string
		wantAllow bool
	}{
		{name: "published allow overrides leader accepted deny", published: "allow", accepted: "deny", wantAllow: true},
		{name: "published deny overrides leader accepted allow", published: "deny", accepted: "allow", wantAllow: false},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			published := newPublishedAuxFiles("haptic")
			setPublishedFiles(published, map[string]map[string]string{
				haproxyGeneralFileGVR.String(): {"gate": test.published},
			})
			authority := newCurrentFilesAuthority(published)
			generation := authority.BeginTerm()
			authority.Accept(generation, "plan-test", &dataplane.AuxiliaryFiles{
				GeneralFiles: []auxiliaryfiles.GeneralFile{{Path: "general/gate", Content: test.accepted}},
			})
			assert.Equal(t, test.accepted, currentFilesSnapshot(t, authority, generation)["gate"])
			assert.Equal(t, test.published, currentFilesPublishedSnapshot(t, authority)["gate"])

			cfg := currentFilesAdmissionConfig(template)
			bootstrapResult := &typebootstrap.Result{
				Types:  map[string]reflect.Type{},
				Errors: map[string]error{},
			}
			declarations := helpers.BuildAdditionalDeclarations(cfg, bootstrapResult)
			engine, err := helpers.NewEngineFromConfigWithOptions(cfg, nil, nil, declarations, helpers.EngineOptions{})
			require.NoError(t, err)
			countingEngine := &renderCountingEngine{Engine: engine}
			logger := slog.New(slog.NewTextHandler(io.Discard, nil))
			wiring := &reconciliationWiring{
				renderService: renderer.NewRenderService(&renderer.RenderServiceConfig{
					Engine:             countingEngine,
					Config:             cfg,
					Logger:             logger,
					Capabilities:       renderer.NewCapabilitiesFanout(dataplane.Capabilities{}).Capabilities(),
					TypedResourceTypes: map[string]reflect.Type{},
				}),
				publishedCurrentFiles: published,
				gvrMapper:             ingressRESTMapper(),
			}
			validator, err := createDryRunValidator(
				cfg,
				busevents.NewEventBus(100),
				stores.NewRealStoreProvider(map[string]stores.Store{"ingresses": &storetest.MockStore{}}),
				wiring,
				nil,
				logger,
			)
			require.NoError(t, err)

			allowed, reason, _ := validator.ValidateDirect(
				context.Background(),
				"networking.k8s.io/v1.Ingress",
				"default",
				"example",
				admissionIngress(),
				nil,
				"CREATE",
			)

			assert.Equal(t, test.wantAllow, allowed)
			assert.Positive(t, countingEngine.renderCalls.Load())
			if test.wantAllow {
				assert.Empty(t, reason)
			} else {
				assert.Contains(t, reason, "published currentFiles denied proposal")
			}
		})
	}
}

type renderCountingEngine struct {
	templating.Engine
	renderCalls atomic.Int64
}

func (e *renderCountingEngine) RenderWithProfiling(ctx context.Context, templateName string, templateContext map[string]any) (string, []templating.IncludeStats, error) {
	e.renderCalls.Add(1)
	return e.Engine.RenderWithProfiling(ctx, templateName, templateContext)
}

func TestCreateDryRunValidatorRequiresReconciliationRenderService(t *testing.T) {
	_, err := createDryRunValidator(
		currentFilesAdmissionConfig(testutil.MinimalHAProxyConfig),
		busevents.NewEventBus(10),
		stores.NewRealStoreProvider(nil),
		&reconciliationWiring{},
		nil,
		slog.New(slog.NewTextHandler(io.Discard, nil)),
	)
	require.EqualError(t, err, "dry-run validation requires the reconciliation render service")
}

func currentFilesAdmissionConfig(template string) *coreconfig.Config {
	return &coreconfig.Config{
		Dataplane: coreconfig.DataplaneConfig{
			MapsDir:           "/etc/haproxy/maps",
			SSLCertsDir:       "/etc/haproxy/ssl",
			GeneralStorageDir: "/etc/haproxy/general",
		},
		WatchedResources: map[string]coreconfig.WatchedResource{
			"ingresses": {
				APIVersion:              "networking.k8s.io/v1",
				Resources:               "ingresses",
				EnableValidationWebhook: true,
			},
		},
		HAProxyConfig: coreconfig.HAProxyConfig{Template: template},
	}
}

func ingressRESTMapper() meta.RESTMapper {
	mapper := meta.NewDefaultRESTMapper(nil)
	mapper.AddSpecific(
		schema.GroupVersionKind{Group: "networking.k8s.io", Version: "v1", Kind: "Ingress"},
		schema.GroupVersionResource{Group: "networking.k8s.io", Version: "v1", Resource: "ingresses"},
		schema.GroupVersionResource{Group: "networking.k8s.io", Version: "v1", Resource: "ingress"},
		meta.RESTScopeNamespace,
	)
	return mapper
}

func admissionIngress() *unstructured.Unstructured {
	return &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": "networking.k8s.io/v1",
		"kind":       "Ingress",
		"metadata": map[string]any{
			"name":      "example",
			"namespace": "default",
		},
	}}
}
