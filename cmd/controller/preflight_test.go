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

package main

import (
	"context"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"gitlab.com/haproxy-haptic/haptic/pkg/apis/haproxytemplate/v1alpha1"
	"gitlab.com/haproxy-haptic/haptic/pkg/controller/testrunner"
)

func TestCollectConfigDocuments(t *testing.T) {
	tests := []struct {
		name      string
		manifests map[string]string
		want      []string
		wantErr   string
	}{
		{
			name: "keeps only the config kinds",
			manifests: map[string]string{
				"haptic/templates/deployment.yaml":             "kind: Deployment\nmetadata:\n  name: c\n",
				"haptic/templates/haproxytemplateconfig.yaml":  "kind: HAProxyTemplateConfig\nmetadata:\n  name: cfg\n",
				"haptic/templates/haproxyvalidationtests.yaml": "kind: HAProxyValidationTests\nmetadata:\n  name: t\n",
			},
			want: []string{"name: cfg", "name: t"},
		},
		{
			name: "splits multi-document files",
			manifests: map[string]string{
				"haptic/templates/all.yaml": "kind: Service\nmetadata:\n  name: s\n" +
					"\n---\nkind: HAProxyTemplateConfig\nmetadata:\n  name: a\n" +
					"\n---\nkind: HAProxyTemplateConfig\nmetadata:\n  name: b\n",
			},
			want: []string{"name: a", "name: b"},
		},
		{
			name: "orders by manifest path so a failure reproduces",
			manifests: map[string]string{
				"haptic/templates/z.yaml": "kind: HAProxyTemplateConfig\nmetadata:\n  name: zzz\n",
				"haptic/templates/a.yaml": "kind: HAProxyTemplateConfig\nmetadata:\n  name: aaa\n",
			},
			want: []string{"name: aaa", "name: zzz"},
		},
		{
			name:      "no config is an error, not an empty pass",
			manifests: map[string]string{"haptic/templates/deployment.yaml": "kind: Deployment\n"},
			wantErr:   "no HAProxyTemplateConfig",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := collectConfigDocuments(tt.manifests)
			if tt.wantErr != "" {
				require.Error(t, err)
				assert.Contains(t, err.Error(), tt.wantErr)
				return
			}
			require.NoError(t, err)

			var names []string
			for _, doc := range strings.Split(got, "\n---\n") {
				for _, line := range strings.Split(doc, "\n") {
					if strings.HasPrefix(strings.TrimSpace(line), "name:") {
						names = append(names, strings.TrimSpace(line))
					}
				}
			}
			assert.Equal(t, tt.want, names)
		})
	}
}

// The zero schemaSource is `validate` without --schema-dir: no schemas, both
// served checkers nil (ResolveEffectiveSpec then keeps every candidate), and an
// empty type-bootstrap Result rather than an error.
func TestZeroSchemaSourceIsTheOfflineFallThrough(t *testing.T) {
	var zero schemaSource
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	spec := &v1alpha1.HAProxyTemplateConfigSpec{}

	resolution, err := zero.resolveEffectiveSpec(context.Background(), spec, logger)
	require.NoError(t, err)
	assert.NotNil(t, resolution)

	typed, err := zero.typeBootstrap(context.Background(), spec, logger)
	require.NoError(t, err)
	require.NotNil(t, typed)
	assert.Empty(t, typed.Types)
}

func TestNewDirSchemaSource(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))

	// An empty path is "no schemas", not an error.
	empty, err := newDirSchemaSource("", logger)
	require.NoError(t, err)
	assert.Equal(t, schemaSource{}, empty)

	loaded, err := newDirSchemaSource("../../tests/schemas", logger)
	require.NoError(t, err)
	require.NotNil(t, loaded.dir)
	assert.Positive(t, loaded.dir.Len())
	assert.Nil(t, loaded.live)

	// NewDirFetcher tolerates a missing directory by design, so the loader
	// itself does not error — TestPreflightSchemas covers the check that keeps
	// a typo from silently weakening preflight.
	absent, err := newDirSchemaSource(filepath.Join(t.TempDir(), "absent"), logger)
	require.NoError(t, err)
	assert.Zero(t, absent.dir.Len())
}

func TestPreflightSchemas(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	t.Cleanup(func() { preflightSchemaDir, preflightKubeconfig = "", "" })

	t.Run("a directory with no schemas is refused, not silently accepted", func(t *testing.T) {
		preflightSchemaDir = filepath.Join(t.TempDir(), "typo")
		preflightKubeconfig = ""

		_, err := preflightSchemas(logger)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "holds no schemas")
	})

	t.Run("a populated directory keeps the run offline", func(t *testing.T) {
		preflightSchemaDir = "../../tests/schemas"
		preflightKubeconfig = ""

		schemas, err := preflightSchemas(logger)
		require.NoError(t, err)
		assert.Nil(t, schemas.live, "--schema-dir must not reach for the cluster")
		assert.Positive(t, schemas.dir.Len())
	})

	t.Run("without a directory it goes to the cluster and says so on failure", func(t *testing.T) {
		preflightSchemaDir = ""
		preflightKubeconfig = filepath.Join(t.TempDir(), "no-such-kubeconfig")

		_, err := preflightSchemas(logger)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "reads the API schemas from the cluster")
		assert.Contains(t, err.Error(), "--schema-dir")
	})
}

// Cluster round-trips get a ceiling; reading a local directory cannot hang on a
// network, so an offline source must not inherit one.
func TestWithLiveDeadline(t *testing.T) {
	offlineCtx, cancel := schemaSource{}.withLiveDeadline(context.Background())
	defer cancel()
	_, hasDeadline := offlineCtx.Deadline()
	assert.False(t, hasDeadline)

	liveCtx, cancel2 := schemaSource{live: &liveCluster{}}.withLiveDeadline(context.Background())
	defer cancel2()
	deadline, hasDeadline := liveCtx.Deadline()
	require.True(t, hasDeadline, "a stale kubeconfig would otherwise hang the run")
	assert.WithinDuration(t, time.Now().Add(liveSchemaTimeout), deadline, 5*time.Second)
}

func TestCollectVCLData(t *testing.T) {
	out := map[string]string{}

	collectVCLData("kind: ConfigMap\ndata:\n  default.vcl: |\n    vcl 4.1;\n  notes.txt: hello\n", out)
	collectVCLData("kind: Service\ndata:\n  other.vcl: nope\n", out)
	collectVCLData("this: is: not: yaml:\n", out)

	assert.Equal(t, map[string]string{"default.vcl": "vcl 4.1;\n"}, out)
}

func TestVCLBackendHosts(t *testing.T) {
	vcl := `backend default {
    .host = "haptic-cache-origin.haptic.svc.cluster.local";
    .port = "8090";
}
backend other {
    .host  =  "second.example";
}
backend dup {
    .host = "haptic-cache-origin.haptic.svc.cluster.local";
}`
	// Sorted and de-duplicated: each host becomes one --add-host argument.
	assert.Equal(t, []string{"haptic-cache-origin.haptic.svc.cluster.local", "second.example"},
		vclBackendHosts(vcl))
	assert.Empty(t, vclBackendHosts("vcl 4.1;\n"))
}

func TestContainerRuntimeRejectsMissingOverride(t *testing.T) {
	t.Setenv("HAPTIC_CONTAINER_RUNTIME", "definitely-not-a-container-runtime")

	_, err := containerRuntime()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "not executable")
}

// Nothing to compile means nothing to check — but only because the render
// produced neither, not because the runtime was missing.
func TestCheckRenderedSidecarConfigsSkipsWhenNothingRendered(t *testing.T) {
	t.Setenv("HAPTIC_CONTAINER_RUNTIME", "definitely-not-a-container-runtime")

	results := &testrunner.TestResults{TestResults: []testrunner.TestResult{{
		TestName:             "t",
		RenderedFiles:        map[string]string{"haproxy.cfg": "global"},
		RenderedK8sResources: map[string]string{"cm": "kind: ConfigMap\ndata:\n  a.txt: b\n"},
	}}}

	require.NoError(t, checkRenderedSidecarConfigs(context.Background(), results))
}

// stubRuntime writes a fake `docker` onto PATH that records its argv and exits
// with the given code, so the invocation and error-wrapping paths are testable
// without pulling images.
func stubRuntime(t *testing.T, exitCode int) (argvFile string) {
	t.Helper()
	dir := t.TempDir()
	argvFile = filepath.Join(dir, "argv")
	script := filepath.Join(dir, "fake-runtime")
	body := "#!/bin/sh\nprintf '%s\\n' \"$@\" >> " + argvFile +
		"\necho 'fake runtime output'\nexit " + strconv.Itoa(exitCode) + "\n"
	require.NoError(t, os.WriteFile(script, []byte(body), 0o700))
	t.Setenv("HAPTIC_CONTAINER_RUNTIME", script)
	return argvFile
}

func sidecarResults() *testrunner.TestResults {
	return &testrunner.TestResults{TestResults: []testrunner.TestResult{{
		TestName:      "t",
		RenderedFiles: map[string]string{"vector.yaml": "sources: {}\n"},
		RenderedK8sResources: map[string]string{
			"cm": "kind: ConfigMap\ndata:\n  default.vcl: |\n    backend b { .host = \"svc.ns.svc\"; }\n",
		},
	}}}
}

func TestCheckRenderedSidecarConfigsInvokesBothCompilers(t *testing.T) {
	argvFile := stubRuntime(t, 0)

	require.NoError(t, checkRenderedSidecarConfigs(context.Background(), sidecarResults()))

	argv, err := os.ReadFile(argvFile)
	require.NoError(t, err)
	got := string(argv)

	assert.Contains(t, got, "validate", "vector config was not validated")
	assert.Contains(t, got, "/w/vector.yaml")
	assert.Contains(t, got, "varnishd", "VCL was not compiled")
	assert.Contains(t, got, "/w/default.vcl")
	// Backend hostnames are faked so varnishd's compile-time resolution can't
	// depend on cluster DNS that doesn't exist on the pipeline host.
	assert.Contains(t, got, "--add-host")
	assert.Contains(t, got, "svc.ns.svc:127.0.0.1")
}

func TestCheckRenderedSidecarConfigsReportsTheConsequence(t *testing.T) {
	stubRuntime(t, 1)

	err := checkRenderedSidecarConfigs(context.Background(), sidecarResults())
	require.Error(t, err)
	// vector.yaml is checked first, so its message is the one that surfaces.
	assert.Contains(t, err.Error(), "never become ready")
	assert.Contains(t, err.Error(), "fake runtime output", "the compiler's own output must reach the operator")
}

// writeProbeChart writes a minimal chart that echoes the values and release
// options it was rendered with. Rendering the real chart here would cost ~20s
// per case and would mostly test the chart; this tests renderChartManifests.
func writeProbeChart(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "Chart.yaml"),
		[]byte("apiVersion: v2\nname: probe\nversion: 0.1.0\n"), 0o600))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "values.yaml"),
		[]byte("a: from-defaults\nb: from-defaults\n"), 0o600))
	require.NoError(t, os.MkdirAll(filepath.Join(dir, "templates"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "templates", "cfg.yaml"), []byte(
		`{{- if eq .Values.a "boom" }}{{ fail "the values are not acceptable" }}{{- end }}
kind: HAProxyTemplateConfig
metadata:
  name: {{ .Release.Name }}-{{ .Release.Namespace }}
data:
  a: {{ .Values.a }}
  b: {{ .Values.b }}
  gatewayServed: {{ .Capabilities.APIVersions.Has "gateway.networking.k8s.io/v1/GatewayClass" }}
`), 0o600))
	return dir
}

func writeValues(t *testing.T, dir, name, body string) string {
	t.Helper()
	path := filepath.Join(dir, name)
	require.NoError(t, os.WriteFile(path, []byte(body), 0o600))
	return path
}

func TestRenderChartManifests(t *testing.T) {
	chart := writeProbeChart(t)
	vdir := t.TempDir()

	preflightNamespace, preflightRelease = "probe-ns", "probe-rel"
	t.Cleanup(func() { preflightNamespace, preflightRelease = "haptic", "haptic" })

	t.Run("later values file wins, others merge", func(t *testing.T) {
		base := writeValues(t, vdir, "base.yaml", "a: from-base\nb: from-base\n")
		over := writeValues(t, vdir, "over.yaml", "a: from-over\n")

		manifests, err := renderChartManifests(chart, []string{base, over})
		require.NoError(t, err)
		docs, err := collectConfigDocuments(manifests)
		require.NoError(t, err)

		assert.Contains(t, docs, "a: from-over", "the second values file did not win")
		assert.Contains(t, docs, "b: from-base", "the first values file was discarded instead of merged")
		// The namespace an operator deploys to is the one that gets validated —
		// which is what catches an assertion pinned to the CI namespace.
		assert.Contains(t, docs, "name: probe-rel-probe-ns")
	})

	t.Run("gateway API capability is always declared", func(t *testing.T) {
		vals := writeValues(t, vdir, "plain.yaml", "a: x\n")

		manifests, err := renderChartManifests(chart, []string{vals})
		require.NoError(t, err)
		docs, err := collectConfigDocuments(manifests)
		require.NoError(t, err)

		// Without it the chart silently drops its Gateway library and the check
		// would validate a config the cluster never receives.
		assert.Contains(t, docs, "gatewayServed: true")
	})

	t.Run("a chart fail() is reported as a values rejection", func(t *testing.T) {
		vals := writeValues(t, vdir, "bad.yaml", "a: boom\n")

		_, err := renderChartManifests(chart, []string{vals})
		require.Error(t, err)
		assert.Contains(t, err.Error(), "the chart rejects these values")
		assert.Contains(t, err.Error(), "the values are not acceptable")
	})

	t.Run("an unreadable values file names itself", func(t *testing.T) {
		_, err := renderChartManifests(chart, []string{filepath.Join(vdir, "absent.yaml")})
		require.Error(t, err)
		assert.Contains(t, err.Error(), "absent.yaml")
	})
}
