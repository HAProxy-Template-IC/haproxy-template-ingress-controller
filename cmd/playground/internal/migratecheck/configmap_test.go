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

package migratecheck

import (
	"encoding/json"
	"os"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	k8syaml "k8s.io/apimachinery/pkg/util/yaml"
	"sigs.k8s.io/yaml"
)

func configMapCoverage() []CoverageSource {
	cov := coverage()
	cov[0].ConfigMap = &ConfigMapCoverage{
		Detect: ConfigMapDetect{
			Names:  []string{"acme-controller"},
			Labels: map[string]string{"app": "acme", "component": "controller"},
		},
		Settings: []ConfigMapSetting{
			{Keys: []ConfigMapKey{{Name: "pool-*"}}, Status: StatusDropped, Note: "pooled"},
			{Keys: []ConfigMapKey{{Name: "read-timeout", Default: "60"}, {Name: "send-timeout"}}, Setting: "timeout_server", Status: StatusDifferent, Note: "one timeout"},
			{Keys: []ConfigMapKey{{Name: "pool-size"}}, Setting: "maxconn", Status: StatusSupported},
		},
	}
	return cov
}

func TestClassify_ConfigMapByName(t *testing.T) {
	cms := []ConfigMap{{
		Namespace: "acme", Name: "acme-controller",
		Data: map[string]string{"send-timeout": "30", "pool-idle": "10", "pool-size": "5", "worker-rlimit": "1"},
	}}

	report := Classify(configMapCoverage(), nil, cms)

	require.Len(t, report.Sources, 1)
	require.Len(t, report.Sources[0].ConfigMaps, 1)
	got := report.Sources[0].ConfigMaps[0]
	assert.Equal(t, "acme-controller", got.Name)
	assert.Equal(t, []ConfigMapFinding{
		{Key: "pool-idle", Value: "10", Status: StatusDropped, Note: "pooled"},
		{Key: "pool-size", Value: "5", Status: StatusSupported, Setting: "maxconn"},
		{Key: "send-timeout", Value: "30", Status: StatusDifferent, Setting: "timeout_server", Note: "one timeout"},
		{Key: "worker-rlimit", Value: "1", Status: StatusUnknown, Note: undeclaredConfigMapKeyNote},
	}, got.Findings, "exact keys beat a matching prefix; undeclared keys are unknown")
	assert.Equal(t, 4, report.CheckedConfigMapKeys)
	assert.Equal(t, 1, report.TotalConfigMaps)
	assert.Equal(t, 0, report.CheckedAnnotations)
	assert.Equal(t, 1, report.Counts[StatusUnknown])
	assert.Equal(t, 1, report.Sources[0].Counts[StatusDifferent])
}

func TestClassify_ConfigMapDetection(t *testing.T) {
	labels := map[string]string{"app": "acme", "component": "controller", "extra": "x"}
	tests := []struct {
		name string
		cm   ConfigMap
		want bool
	}{
		{name: "declared name", cm: ConfigMap{Name: "acme-controller"}, want: true},
		{name: "labels and a declared key", cm: ConfigMap{Name: "release-acme", Labels: labels, Data: map[string]string{"read-timeout": "5"}}, want: true},
		{name: "labels and a prefix key", cm: ConfigMap{Name: "release-acme", Labels: labels, Data: map[string]string{"pool-idle": "5"}}, want: true},
		{name: "labels without a declared key", cm: ConfigMap{Name: "acme-tcp", Labels: labels, Data: map[string]string{"9000": "ns/svc:80"}}, want: false},
		{name: "declared key without labels", cm: ConfigMap{Name: "app-settings", Data: map[string]string{"read-timeout": "5"}}, want: false},
		{name: "partial labels", cm: ConfigMap{Name: "release-acme", Labels: map[string]string{"app": "acme"}, Data: map[string]string{"read-timeout": "5"}}, want: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			report := Classify(configMapCoverage(), nil, []ConfigMap{tt.cm})
			assert.Equal(t, tt.want, len(report.Sources) == 1)
		})
	}
}

func TestClassify_SourceWithoutConfigMapCoverageIgnoresConfigMaps(t *testing.T) {
	report := Classify(coverage(), nil, []ConfigMap{{Name: "acme-controller", Data: map[string]string{"a": "b"}}})
	assert.Empty(t, report.Sources)
	assert.Zero(t, report.CheckedConfigMapKeys)
}

func TestConfigMapFromUnstructured(t *testing.T) {
	u := &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": "v1",
		"kind":       "ConfigMap",
		"metadata": map[string]any{
			"namespace": "ingress-nginx", "name": "ingress-nginx-controller",
			"labels": map[string]any{"app": "acme"},
		},
		"data": map[string]any{"keep-alive": "75"},
	}}
	assert.Equal(t, ConfigMap{
		Namespace: "ingress-nginx", Name: "ingress-nginx-controller",
		Labels: map[string]string{"app": "acme"},
		Data:   map[string]string{"keep-alive": "75"},
	}, ConfigMapFromUnstructured(u))
}

func decodeConfigMap(t *testing.T, manifest string) ConfigMap {
	t.Helper()
	var obj map[string]any
	require.NoError(t, k8syaml.NewYAMLOrJSONDecoder(strings.NewReader(manifest), 4096).Decode(&obj))
	return ConfigMapFromUnstructured(&unstructured.Unstructured{Object: obj})
}

func TestConfigMapFromUnstructured_NonStringValues(t *testing.T) {
	cm := decodeConfigMap(t, `apiVersion: v1
kind: ConfigMap
metadata: {name: ingress-nginx-controller}
data:
  keep-alive: 75
  ratio: 0.5
  hsts: true
  ssl-ciphers:
  quoted: "75"
  nested-map: {a: b}
  nested-list: [1, two]
`)
	assert.Equal(t, map[string]string{
		"keep-alive":  "75",
		"ratio":       "0.5",
		"hsts":        "true",
		"ssl-ciphers": "null",
		"quoted":      "75",
		"nested-map":  `{"a":"b"}`,
		"nested-list": `[1,"two"]`,
	}, cm.Data, "every key survives, in the form it was written")
	assert.Equal(t, map[string]string{
		"keep-alive":  nonStringValueProblem,
		"ratio":       nonStringValueProblem,
		"hsts":        nonStringValueProblem,
		"ssl-ciphers": nonStringValueProblem,
		"nested-map":  nonStringValueProblem,
		"nested-list": nonStringValueProblem,
	}, cm.KeyProblems)
	assert.Empty(t, cm.Problem)

	report := Classify(configMapCoverage(), nil, []ConfigMap{
		decodeConfigMap(t, "kind: ConfigMap\nmetadata: {name: acme-controller}\ndata: {send-timeout: 30, pool-size: \"5\"}\n"),
	})
	require.Len(t, report.Sources, 1)
	assert.Equal(t, []ConfigMapFinding{
		{Key: "pool-size", Value: "5", Status: StatusSupported, Setting: "maxconn"},
		{Key: "send-timeout", Value: "30", Status: StatusDifferent, Setting: "timeout_server", Note: "one timeout", Problem: nonStringValueProblem},
	}, report.Sources[0].ConfigMaps[0].Findings)
}

func TestConfigMapFromUnstructured_UnreadableData(t *testing.T) {
	for name, data := range map[string]string{"list": "[a, b]", "string": "oops", "number": "3"} {
		t.Run(name, func(t *testing.T) {
			cm := decodeConfigMap(t, "kind: ConfigMap\nmetadata: {name: acme-controller}\ndata: "+data+"\n")
			assert.Empty(t, cm.Data)
			assert.Contains(t, cm.Problem, "data is a "+name)

			report := Classify(configMapCoverage(), nil, []ConfigMap{cm})
			require.Len(t, report.Sources, 1, "a ConfigMap matched by name is reported with its problem")
			assert.Equal(t, cm.Problem, report.Sources[0].ConfigMaps[0].Problem)
		})
	}

	cm := decodeConfigMap(t, "kind: ConfigMap\nmetadata: {name: c}\ndata:\n")
	assert.Empty(t, cm.Problem, "null data is an empty ConfigMap")
	assert.Empty(t, cm.Data)
}

func TestParseCoverage_ConfigMap(t *testing.T) {
	const head = `[{"source":"acme","detect":{"ingressClasses":["acme"],"annotationPrefixes":["acme.io/"]},"annotations":{"acme.io/a":{"status":"supported","note":"same"}},"configMap":`
	tests := []struct {
		name    string
		section string
		wantErr string
	}{
		{name: "valid", section: `{"detect":{"names":["c"]},"settings":[{"keys":[{"name":"k","default":"1"}],"setting":"s","status":"supported"}]}`},
		{name: "no detection", section: `{"settings":[{"keys":[{"name":"k"}],"status":"supported"}]}`, wantErr: "no ConfigMap detection rules"},
		{name: "empty name", section: `{"detect":{"names":[" "]},"settings":[{"keys":[{"name":"k"}],"status":"supported"}]}`, wantErr: "empty ConfigMap name"},
		{name: "no settings", section: `{"detect":{"names":["c"]}}`, wantErr: "no ConfigMap settings"},
		{name: "no keys", section: `{"detect":{"names":["c"]},"settings":[{"status":"supported"}]}`, wantErr: "has no keys"},
		{name: "bare wildcard", section: `{"detect":{"names":["c"]},"settings":[{"keys":[{"name":"*"}],"status":"supported"}]}`, wantErr: "empty key"},
		{name: "duplicate key", section: `{"detect":{"names":["c"]},"settings":[{"keys":[{"name":"k"}],"status":"supported"},{"keys":[{"name":"k"}],"status":"supported"}]}`, wantErr: "twice"},
		{name: "invalid status", section: `{"detect":{"names":["c"]},"settings":[{"keys":[{"name":"k"}],"status":"maybe"}]}`, wantErr: "invalid status"},
		{name: "non-supported without note", section: `{"detect":{"names":["c"]},"settings":[{"keys":[{"name":"k"}],"status":"dropped"}]}`, wantErr: "has no note"},
		{name: "unknown field", section: `{"detect":{"names":["c"]},"rows":[]}`, wantErr: "unknown field"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := ParseCoverage([]byte(head + tt.section + `}]`))
			if tt.wantErr != "" {
				require.ErrorContains(t, err, tt.wantErr)
				return
			}
			require.NoError(t, err)
			require.NotNil(t, got[0].ConfigMap)
			assert.Equal(t, "1", got[0].ConfigMap.Settings[0].Keys[0].Default)
		})
	}
}

func TestBundledIngressNginxConfigMapCoverage(t *testing.T) {
	data, err := os.ReadFile("../../../../charts/haptic/charts/nginx-ingress/90-migration-coverage.yaml")
	require.NoError(t, err)
	var file struct {
		Coverage json.RawMessage `json:"_migrationCoverage"`
	}
	require.NoError(t, yaml.Unmarshal(data, &file))
	cov, err := ParseCoverage(file.Coverage)
	require.NoError(t, err)

	helmLabels := map[string]string{"app.kubernetes.io/name": "ingress-nginx", "app.kubernetes.io/component": "controller"}
	report := Classify(cov, nil, []ConfigMap{
		{
			Namespace: "ingress-nginx", Name: "edge-ingress-nginx-controller", Labels: helmLabels,
			Data: map[string]string{"hsts": "true", "keep-alive": "75", "upstream-keepalive-connections": "320", "lua-shared-dicts": "x"},
		},
		{Namespace: "ingress-nginx", Name: "edge-ingress-nginx-tcp", Labels: helmLabels, Data: map[string]string{"9000": "default/db:5432"}},
	})

	require.Len(t, report.Sources, 1)
	require.Len(t, report.Sources[0].ConfigMaps, 1)
	statuses := map[string]Status{}
	for _, f := range report.Sources[0].ConfigMaps[0].Findings {
		statuses[f.Key] = f.Status
	}
	assert.Equal(t, map[string]Status{
		"hsts":                           StatusDifferent,
		"keep-alive":                     StatusSupported,
		"upstream-keepalive-connections": StatusDropped,
		"lua-shared-dicts":               StatusUnknown,
	}, statuses)
}
