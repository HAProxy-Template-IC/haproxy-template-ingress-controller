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

package inputisolation

import (
	"errors"
	"fmt"
	"log/slog"
	"math/bits"
	"slices"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/haproxy-haptic/HAPTIC/pkg/controller/pipeline"
	"github.com/haproxy-haptic/HAPTIC/pkg/controller/rendercontext"
	"github.com/haproxy-haptic/HAPTIC/pkg/controller/renderer"
	"github.com/haproxy-haptic/HAPTIC/pkg/controller/testutil"
	"github.com/haproxy-haptic/HAPTIC/pkg/controller/typebootstrap"
	"github.com/haproxy-haptic/HAPTIC/pkg/controller/validation"
	"github.com/haproxy-haptic/HAPTIC/pkg/core/config"
	"github.com/haproxy-haptic/HAPTIC/pkg/dataplane"
	k8sstore "github.com/haproxy-haptic/HAPTIC/pkg/k8s/store"
	"github.com/haproxy-haptic/HAPTIC/pkg/stores"
	"github.com/haproxy-haptic/HAPTIC/pkg/templating"
)

const widgetBackends = testutil.MinimalHAProxyConfig + `{% for _, item := range resources.widgets.List() %}
backend w_{{ tostring(item | dig("metadata", "name")) }}
    server s1 127.0.0.1:80
{% end %}`

func judgeHAProxy(t *testing.T, judge haproxyVerdict) {
	t.Helper()
	haproxyJudge.Store(&judge)
	haproxyChecks.Store(0)
	t.Cleanup(func() { haproxyJudge.Store(nil) })
}

func haproxyAlert(format string, args ...any) ([]byte, error) {
	output := fmt.Sprintf("[ALERT]    (%d) : config : %s\n", haproxyChecks.Load()+1000, fmt.Sprintf(format, args...)) +
		"[ALERT]    (1) : config : Fatal errors found in configuration.\n"
	return []byte(output), errors.New("exit status 1")
}

func alertLine(rendered, needle string) int {
	for index, line := range strings.Split(rendered, "\n") {
		if strings.Contains(line, needle) {
			return index + 1
		}
	}
	return 0
}

func checkedWidgetSelector(t *testing.T, names ...string) (*Service, stores.StoreProvider) {
	t.Helper()
	watches := map[string]config.WatchedResource{
		"widgets": {APIVersion: "example.test/v1", Resources: "widgets", IndexBy: []string{"metadata.namespace", "metadata.name"}},
	}
	engine, err := templating.New(map[string]string{"haproxy.cfg": widgetBackends}, &templating.Options{
		EntryPoints:  []string{"haproxy.cfg"},
		Declarations: typebootstrap.BuildEngineDeclarations(&typebootstrap.Result{}, "widgets"),
	})
	require.NoError(t, err)
	cfg := &config.Config{HAProxyConfig: config.HAProxyConfig{Template: widgetBackends}, WatchedResources: watches}
	validatingPipeline := pipeline.New(&pipeline.PipelineConfig{
		Renderer: renderer.NewRenderService(&renderer.RenderServiceConfig{
			Engine: engine, Config: cfg, Logger: slog.Default(),
			Capabilities: dataplane.Capabilities{SupportsCrtList: true, SupportsMapStorage: true, SupportsGeneralStorage: true},
		}),
		Validator: validation.NewValidationService(&validation.ValidationServiceConfig{Logger: slog.Default(), SkipDNSValidation: true}),
		Logger:    slog.Default(),
	})
	live := k8sstore.NewMemoryStore(2)
	for _, name := range names {
		require.NoError(t, live.Add(widget(name, "healthy"), []string{"default", name}))
	}
	return New(validatingPipeline, watches, nil), stores.NewRealStoreProvider(map[string]stores.Store{"widgets": live})
}

func widgetNames(prefix string, count int) []string {
	names := make([]string, count)
	for index := range names {
		names[index] = fmt.Sprintf("%s-%03d", prefix, index)
	}
	return names
}

func rejectedNames(t *testing.T, selector *Service) []string {
	t.Helper()
	_, rejected, ready := selector.Snapshot()
	require.True(t, ready)
	names := make([]string, 0, len(rejected))
	for index := range rejected {
		names = append(names, rejected[index].Name)
	}
	slices.Sort(names)
	return names
}

func TestInputIndependentStartupFailureSkipsTheInputSearch(t *testing.T) {
	const inputs = 200
	judgeHAProxy(t, func(string) ([]byte, error) {
		return haproxyAlert("parsing [haproxy.cfg:2] : unknown keyword 'bogus' in 'global' section")
	})
	selector, provider := checkedWidgetSelector(t, widgetNames("w", inputs)...)
	_, err := selector.Execute(t.Context(), provider, rendercontext.RenderModeReconcile)
	require.ErrorContains(t, err, "unknown keyword 'bogus'")
	require.ErrorIs(t, err, dataplane.ErrHAProxyRefused)
	t.Logf("haproxy -c invocations: %d for %d watched inputs", haproxyChecks.Load(), inputs)
	require.Equal(t, int64(2), haproxyChecks.Load(), "the observed view and the view without watched inputs")
	_, _, ready := selector.Snapshot()
	require.False(t, ready)
}

func TestFailureThatMovesWithInputsIsStillReportedOnce(t *testing.T) {
	judgeHAProxy(t, func(rendered string) ([]byte, error) {
		return haproxyAlert("parsing [haproxy.cfg:%d] : unknown keyword 'bogus' in 'backend' section", strings.Count(rendered, "\n"))
	})
	selector, provider := checkedWidgetSelector(t, widgetNames("w", 50)...)
	_, err := selector.Execute(t.Context(), provider, rendercontext.RenderModeReconcile)
	require.Error(t, err)
	require.Equal(t, int64(2), haproxyChecks.Load())
}

func TestBadInputsAmongManyAreIsolatedByBisection(t *testing.T) {
	const inputs = 256
	names := widgetNames("w", inputs)
	bad := []string{names[17], names[128], names[200]}
	judgeHAProxy(t, func(rendered string) ([]byte, error) {
		for _, name := range bad {
			if line := alertLine(rendered, "backend w_"+name); line > 0 {
				return haproxyAlert("parsing [haproxy.cfg:%d] : 'server s1' : invalid address", line+1)
			}
		}
		return nil, nil
	})
	selector, provider := checkedWidgetSelector(t, names...)
	result, err := selector.Execute(t.Context(), provider, rendercontext.RenderModeReconcile)
	require.NoError(t, err)
	require.Equal(t, bad, rejectedNames(t, selector))
	require.Contains(t, result.HAProxyConfig, "backend w_"+names[inputs-1])
	t.Logf("haproxy -c invocations: %d for %d bad among %d watched inputs", haproxyChecks.Load(), len(bad), inputs)
	require.LessOrEqual(t, haproxyChecks.Load(), int64(2+2*len(bad)*bits.Len(inputs)))
}

func TestRequiredDependencyStillIsolatesAnUnnamedCulprit(t *testing.T) {
	names := append(widgetNames("h", 20), "required", "zz-bad")
	judgeHAProxy(t, func(rendered string) ([]byte, error) {
		if alertLine(rendered, "backend w_required") == 0 {
			return haproxyAlert("Proxy 'http_front': unable to find required use_backend: 'w_required'.")
		}
		if line := alertLine(rendered, "backend w_zz-bad"); line > 0 {
			return haproxyAlert("parsing [haproxy.cfg:%d] : unknown keyword 'bogus' in 'backend' section", line+1)
		}
		return nil, nil
	})
	selector, provider := checkedWidgetSelector(t, names...)
	result, err := selector.Execute(t.Context(), provider, rendercontext.RenderModeReconcile)
	require.NoError(t, err)
	require.Equal(t, []string{"zz-bad"}, rejectedNames(t, selector))
	require.Contains(t, result.HAProxyConfig, "backend w_required")
	require.Contains(t, result.HAProxyConfig, "backend w_h-019")
	require.Greater(t, haproxyChecks.Load(), int64(2), "a cause that disappears with the inputs still searches them")
}

func TestStaticCauseBesideAnInputCauseSkipsTheInputSearch(t *testing.T) {
	names := widgetNames("w", 50)
	judgeHAProxy(t, func(rendered string) ([]byte, error) {
		output := "[ALERT]    (7) : config : parsing [haproxy.cfg:2] : unknown keyword 'bogus' in 'global' section\n"
		if line := alertLine(rendered, "backend w_"+names[3]); line > 0 {
			output += fmt.Sprintf("[ALERT]    (7) : config : parsing [haproxy.cfg:%d] : 'server s1' : invalid address\n", line+1)
		}
		return []byte(output), errors.New("exit status 1")
	})
	selector, provider := checkedWidgetSelector(t, names...)
	_, err := selector.Execute(t.Context(), provider, rendercontext.RenderModeReconcile)
	require.ErrorContains(t, err, "unknown keyword 'bogus'")
	require.Equal(t, int64(2), haproxyChecks.Load())
}
