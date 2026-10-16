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
	"errors"
	"fmt"
	"io"
	"log/slog"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/testutil"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"

	"github.com/haproxy-haptic/HAPTIC/pkg/controller/events"
	"github.com/haproxy-haptic/HAPTIC/pkg/controller/helpers"
	"github.com/haproxy-haptic/HAPTIC/pkg/controller/inputisolation"
	"github.com/haproxy-haptic/HAPTIC/pkg/controller/metrics"
	"github.com/haproxy-haptic/HAPTIC/pkg/controller/pipeline"
	"github.com/haproxy-haptic/HAPTIC/pkg/controller/rendercontext"
	"github.com/haproxy-haptic/HAPTIC/pkg/controller/renderer"
	"github.com/haproxy-haptic/HAPTIC/pkg/controller/rendergate"
	ctrltestutil "github.com/haproxy-haptic/HAPTIC/pkg/controller/testutil"
	"github.com/haproxy-haptic/HAPTIC/pkg/controller/typebootstrap"
	"github.com/haproxy-haptic/HAPTIC/pkg/controller/validation"
	"github.com/haproxy-haptic/HAPTIC/pkg/controller/warmer"
	coreconfig "github.com/haproxy-haptic/HAPTIC/pkg/core/config"
	"github.com/haproxy-haptic/HAPTIC/pkg/dataplane"
	k8sstore "github.com/haproxy-haptic/HAPTIC/pkg/k8s/store"
	"github.com/haproxy-haptic/HAPTIC/pkg/stores"
)

const endpointChurnTemplate = `global
    default-path origin /etc/haproxy
defaults
    mode http
    timeout connect 5s
    timeout client 30s
    timeout server 30s
backend be
{%- for _, slice := range resources.endpoints.List() %}
{%- for _, endpoint := range toSlice(slice | dig("endpoints")) %}
{%- for _, address := range toSlice(endpoint | dig("addresses")) %}
    server {{ tostring(address) }} {{ tostring(address) }}:80
{%- end %}
{%- end %}
{%- end %}
`

type countingHAProxy struct{ checks atomic.Int32 }

func (*countingHAProxy) Version(context.Context) (string, error) {
	return "HAProxy version 3.2.0 2025/01/01 - https://haproxy.org/\n", nil
}

func (c *countingHAProxy) Check(context.Context, string, ...string) ([]byte, error) {
	c.checks.Add(1)
	return nil, nil
}

func endpointSlice(address string) *unstructured.Unstructured {
	return &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": "discovery.k8s.io/v1", "kind": "EndpointSlice",
		"metadata":  map[string]any{"namespace": "default", "name": "web-abc"},
		"endpoints": []any{map[string]any{"addresses": []any{address}}},
	}}
}

// replica is one controller's reconcile path: input acceptance in front of
// the render, and the render gate behind it.
type replica struct {
	selector  *inputisolation.Service
	unchecked *pipeline.Pipeline
	gate      rendergate.ServiceChecker
}

func newReplica(t *testing.T, m *metrics.Metrics) *replica {
	t.Helper()
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	cfg := &coreconfig.Config{
		Controller: coreconfig.ControllerConfig{RenderGateInterval: "1ms"},
		WatchedResources: map[string]coreconfig.WatchedResource{
			"endpoints": {
				APIVersion: "discovery.k8s.io/v1", Resources: "endpointslices",
				IndexBy: []string{"metadata.namespace", "metadata.name"},
			},
		},
		HAProxyConfig: coreconfig.HAProxyConfig{Template: endpointChurnTemplate},
	}
	declarations := helpers.BuildAdditionalDeclarations(cfg, &typebootstrap.Result{Types: map[string]reflect.Type{}, Errors: map[string]error{}})
	engine, err := helpers.NewEngineFromConfigWithOptions(cfg, nil, nil, declarations, helpers.EngineOptions{})
	require.NoError(t, err)
	renderService := renderer.NewRenderService(&renderer.RenderServiceConfig{
		Engine: engine, Config: cfg, Logger: logger,
		Capabilities: dataplane.Capabilities{SupportsCrtList: true, SupportsMapStorage: true, SupportsGeneralStorage: true},
	})
	checks := newCheckServices(cfg, logger, m)
	return &replica{
		selector: inputisolation.New(pipeline.New(&pipeline.PipelineConfig{
			Renderer: renderService, Validator: checks.input, CommitValidator: checks.input, Logger: logger,
		}), cfg.WatchedResources, logger),
		unchecked: pipeline.New(&pipeline.PipelineConfig{Renderer: renderService, Logger: logger}),
		gate:      rendergate.ServiceChecker{Service: checks.renderGate},
	}
}

func (r *replica) render(t *testing.T, provider stores.StoreProvider) *pipeline.PipelineResult {
	t.Helper()
	result, err := r.selector.Execute(pipeline.WithPendingContentWithheld(t.Context()), provider, rendercontext.RenderModeReconcile)
	require.NoError(t, err)
	return result
}

// TestEndpointChurnRunsOneHAProxyCheckPerRender pins issue #335: the render
// gate checked the very bytes input acceptance had just checked, so the leader
// ran two `haproxy -c` processes per render.
func TestEndpointChurnRunsOneHAProxyCheckPerRender(t *testing.T) {
	const renders = 10
	for _, leader := range []bool{true, false} {
		t.Run(fmt.Sprintf("leader=%t", leader), func(t *testing.T) {
			haproxy := &countingHAProxy{}
			t.Cleanup(dataplane.SetHAProxyExecutor(haproxy))
			m := metrics.NewMetrics(prometheus.NewRegistry())
			r := newReplica(t, m)
			live := k8sstore.NewMemoryStore(2)
			provider := stores.NewRealStoreProvider(map[string]stores.Store{"endpoints": live})
			require.NoError(t, live.Add(endpointSlice("10.0.0.1"), []string{"default", "web-abc"}))

			for i := range renders {
				require.NoError(t, live.Update(endpointSlice(fmt.Sprintf("10.0.1.%d", i)), []string{"default", "web-abc"}))
				result := r.render(t, provider)
				require.Contains(t, result.HAProxyConfig, fmt.Sprintf("10.0.1.%d:80", i))
				if leader {
					require.NoError(t, r.gateCheck(t, result))
				}
			}

			assert.Equal(t, int32(renders), haproxy.checks.Load(), "one haproxy -c per render")
			assert.InDelta(t, renders, testutil.ToFloat64(m.HAProxyChecksTotal.WithLabelValues("input", validation.CheckPassed)), 0)
			assert.InDelta(t, 0, testutil.ToFloat64(m.HAProxyChecksTotal.WithLabelValues("render_gate", validation.CheckPassed)), 0)
			wantReused := 0
			if leader {
				wantReused = renders
			}
			assert.InDelta(t, wantReused, testutil.ToFloat64(m.HAProxyChecksReusedTotal.WithLabelValues("render_gate", validation.CheckPassed)), 0)
		})
	}
}

// TestFollowerChurnRunsOneHAProxyCheckPerInterval pins the follower half of
// issue #335: the warmer accepted every trigger's render, so a follower ran one
// `haproxy -c` per endpoint change for renders it never deploys.
func TestFollowerChurnRunsOneHAProxyCheckPerInterval(t *testing.T) {
	const interval = time.Second
	haproxy := &countingHAProxy{}
	t.Cleanup(dataplane.SetHAProxyExecutor(haproxy))
	m := metrics.NewMetrics(prometheus.NewRegistry())
	r := newReplica(t, m)
	live := k8sstore.NewMemoryStore(2)
	provider := stores.NewRealStoreProvider(map[string]stores.Store{"endpoints": live})
	require.NoError(t, live.Add(endpointSlice("10.0.0.1"), []string{"default", "web-abc"}))
	bus, logger := ctrltestutil.NewTestBusAndLogger()
	follower := warmer.New(&warmer.Config{
		EventBus: bus, Pipeline: r.selector, StoreProvider: provider,
		CurrentFilesSource: newCurrentFilesAuthority(newPublishedAuxFiles("haptic")).PublishedExactSource,
		Interval:           interval, Metrics: m, Logger: logger,
	})
	bus.Start()
	go func() { _ = follower.Start(t.Context()) }()

	bus.Publish(events.NewReconciliationTriggeredEvent("resource_change", true))
	select {
	case <-follower.Warmed():
	case <-time.After(ctrltestutil.EventTimeout):
		t.Fatal("the follower never rendered")
	}
	for i := range 10 {
		require.NoError(t, live.Update(endpointSlice(fmt.Sprintf("10.0.1.%d", i)), []string{"default", "web-abc"}))
		bus.Publish(events.NewReconciliationTriggeredEvent("resource_change", true))
	}

	require.Eventually(t, func() bool {
		accepted, _, ready := r.selector.Snapshot()
		if !ready {
			return false
		}
		items, err := accepted.GetStore("endpoints").List()
		if err != nil || len(items) != 1 {
			return false
		}
		slice, ok := items[0].(*unstructured.Unstructured)
		return ok && strings.Contains(fmt.Sprint(slice.Object), "10.0.1.9")
	}, interval+ctrltestutil.EventTimeout, 10*time.Millisecond, "the follower accepts the latest inputs")
	assert.Equal(t, int32(2), haproxy.checks.Load(), "one haproxy -c for the first trigger, one for the coalesced churn")
	assert.InDelta(t, 2, testutil.ToFloat64(m.HAProxyChecksTotal.WithLabelValues("input", validation.CheckPassed)), 0)
}

type refusingHAProxy struct{ checks atomic.Int32 }

func (*refusingHAProxy) Version(context.Context) (string, error) {
	return "HAProxy version 3.2.0 2025/01/01 - https://haproxy.org/\n", nil
}

func (r *refusingHAProxy) Check(context.Context, string, ...string) ([]byte, error) {
	r.checks.Add(1)
	return []byte("[ALERT]    (1) : config : refused\n"), errors.New("exit status 1")
}

func (r *replica) gateCheck(t *testing.T, result *pipeline.PipelineResult) error {
	t.Helper()
	checksum, err := result.OutputSnapshot.ContentChecksum()
	require.NoError(t, err)
	return r.gate.CheckOutput(t.Context(), result.OutputSnapshot, checksum)
}

// TestRenderGateChecksBytesInputAcceptanceNeverSaw shows the gate's reuse is
// bound to the exact bytes: an output nobody checked runs `haproxy -c` and
// gets HAProxy's own answer, while the accepted one keeps its verdict.
func TestRenderGateChecksBytesInputAcceptanceNeverSaw(t *testing.T) {
	t.Cleanup(dataplane.SetHAProxyExecutor(&countingHAProxy{}))
	r := newReplica(t, nil)
	live := k8sstore.NewMemoryStore(2)
	provider := stores.NewRealStoreProvider(map[string]stores.Store{"endpoints": live})
	require.NoError(t, live.Add(endpointSlice("10.0.0.1"), []string{"default", "web-abc"}))
	accepted := r.render(t, provider)

	require.NoError(t, live.Update(endpointSlice("10.0.0.66"), []string{"default", "web-abc"}))
	unchecked, err := r.unchecked.Execute(t.Context(), provider, rendercontext.RenderModeReconcile)
	require.NoError(t, err)
	require.Contains(t, unchecked.HAProxyConfig, "10.0.0.66")

	refusing := &refusingHAProxy{}
	t.Cleanup(dataplane.SetHAProxyExecutor(refusing))
	require.ErrorIs(t, r.gateCheck(t, unchecked), dataplane.ErrHAProxyRefused)
	assert.Equal(t, int32(1), refusing.checks.Load())
	require.ErrorIs(t, r.gateCheck(t, accepted), dataplane.ErrHAProxyRefused,
		"a verdict from another executor is never reused")
	assert.Equal(t, int32(2), refusing.checks.Load())
}
