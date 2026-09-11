package main

import (
	"fmt"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"gitlab.com/haproxy-haptic/haptic/pkg/controller/testrunner"
)

func vectorManifests(image string) map[string]string {
	return map[string]string{"deployment": fmt.Sprintf(`kind: Deployment
spec:
  template:
    spec:
      containers:
        - name: vector
          image: %q
`, image)}
}

func varnishWorkload(kind, image, configMap string) string {
	return fmt.Sprintf(`kind: %s
spec:
  template:
    spec:
      containers:
        - name: varnish
          image: %q
          volumeMounts:
            - name: vcl
              mountPath: /etc/varnish
      volumes:
        - name: vcl
          configMap:
            name: %s
`, kind, image, configMap)
}

func TestCollectSidecarConfigsPreservesVariants(t *testing.T) {
	t.Setenv("HAPTIC_VECTOR_IMAGE", "")
	t.Setenv("HAPTIC_VARNISH_IMAGE", "")
	results := sidecarResults()
	second := sidecarResults().TestResults[0]
	second.TestName = "second"
	second.RenderedFiles["vector.yaml"] = "sources: {different: {}}\n"
	second.RenderedK8sResources["cache"] = strings.ReplaceAll(second.RenderedK8sResources["cache"], "svc.ns.svc", "other.ns.svc")
	third := sidecarResults().TestResults[0]
	third.TestName = "third-image"
	third.RenderedK8sResources["cache"] = strings.ReplaceAll(third.RenderedK8sResources["cache"], "varnish:chart-test", "varnish:other-image")
	results.TestResults = append(results.TestResults, second, third, results.TestResults[0])

	manifests := vectorManifests("vector:chart-test")
	manifests["haptic/templates/NOTES.txt"] = "Install instructions: not: YAML: ["
	configs, err := collectSidecarConfigs(manifests, results)
	require.NoError(t, err)
	require.Len(t, configs, 5, "only identical content, names, and images may be deduplicated")
	assert.Equal(t, "t", configs[0].testName)
	assert.Equal(t, "vector:chart-test", configs[0].image)
	assert.Contains(t, configs[1].content, "svc.ns.svc")
	assert.Equal(t, "second", configs[2].testName)
	assert.Contains(t, configs[3].content, "other.ns.svc")
	assert.Equal(t, "varnish:other-image", configs[4].image)

	again, err := collectSidecarConfigs(vectorManifests("vector:chart-test"), results)
	require.NoError(t, err)
	assert.Equal(t, configs, again)
}

func TestCollectSidecarConfigsRejectsUnverifiableImages(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(map[string]string, *testrunner.TestResults)
		want   string
	}{
		{"missing Vector workload", func(m map[string]string, _ *testrunner.TestResults) { delete(m, "deployment") }, "rendered vector image"},
		{"empty Vector image", func(m map[string]string, _ *testrunner.TestResults) {
			m["deployment"] = vectorManifests("")["deployment"]
		}, "rendered vector image"},
		{"ambiguous Vector image", func(m map[string]string, _ *testrunner.TestResults) {
			m["other"] = vectorManifests("other:image")["deployment"]
		}, "rendered vector image"},
		{"unmounted VCL", func(_ map[string]string, r *testrunner.TestResults) {
			r.TestResults[0].RenderedK8sResources["cache"] = strings.ReplaceAll(r.TestResults[0].RenderedK8sResources["cache"], "name: vcl", "name: wrong-mount")
			r.TestResults[0].RenderedK8sResources["cache"] = strings.Replace(r.TestResults[0].RenderedK8sResources["cache"], "name: wrong-mount", "name: vcl", 1)
		}, "rendered varnish image"},
		{"wrong ConfigMap", func(_ map[string]string, r *testrunner.TestResults) {
			r.TestResults[0].RenderedK8sResources["cache"] = strings.Replace(r.TestResults[0].RenderedK8sResources["cache"], "name: cache-vcl", "name: other-vcl", 1)
		}, "rendered varnish image"},
		{"wrong namespace", func(_ map[string]string, r *testrunner.TestResults) {
			r.TestResults[0].RenderedK8sResources["cache"] = strings.Replace(r.TestResults[0].RenderedK8sResources["cache"], "name: cache-vcl", "name: cache-vcl\n  namespace: elsewhere", 1)
		}, "rendered varnish image"},
		{"empty Varnish image", func(_ map[string]string, r *testrunner.TestResults) {
			r.TestResults[0].RenderedK8sResources["cache"] = strings.ReplaceAll(r.TestResults[0].RenderedK8sResources["cache"], "varnish:chart-test", "")
		}, "rendered varnish image"},
		{"invalid later document", func(_ map[string]string, r *testrunner.TestResults) {
			r.TestResults[0].RenderedK8sResources["cache"] += "---\nbroken: [\n"
		}, "reading rendered cache"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv("HAPTIC_VECTOR_IMAGE", "")
			t.Setenv("HAPTIC_VARNISH_IMAGE", "")
			manifests, results := vectorManifests("vector:chart-test"), sidecarResults()
			tt.mutate(manifests, results)
			_, err := collectSidecarConfigs(manifests, results)
			require.ErrorContains(t, err, tt.want)
		})
	}
}

func TestSidecarImageOverridesMustMatch(t *testing.T) {
	for _, kind := range []string{"vector", "varnish"} {
		for _, override := range []string{"", "image:chart-test", "image:wrong"} {
			t.Run(kind+"/"+override, func(t *testing.T) {
				env := "HAPTIC_" + strings.ToUpper(kind) + "_IMAGE"
				t.Setenv(env, override)
				image, err := sidecarImage(kind, map[string]bool{"image:chart-test": true})
				if override == "image:wrong" {
					require.ErrorContains(t, err, "differs from rendered image")
					assert.ErrorContains(t, err, "unset "+env)
					return
				}
				require.NoError(t, err)
				assert.Equal(t, "image:chart-test", image)
			})
		}
	}
}

func TestVCLUsesItsOwnWorkloadImage(t *testing.T) {
	t.Setenv("HAPTIC_VARNISH_IMAGE", "")
	for _, kind := range []string{"Deployment", "StatefulSet"} {
		t.Run(kind, func(t *testing.T) {
			results := sidecarResults()
			results.TestResults[0].RenderedK8sResources["cache"] = strings.ReplaceAll(results.TestResults[0].RenderedK8sResources["cache"], "StatefulSet", kind)
			results.TestResults[0].RenderedK8sResources["unrelated"] = varnishWorkload(kind, "varnish:unrelated", "other-cache-vcl")
			configs, err := collectVarnishConfigs(&results.TestResults[0])
			require.NoError(t, err)
			require.Len(t, configs, 1)
			assert.Equal(t, "varnish:chart-test", configs[0].image)
		})
	}
}

func TestSidecarCollectorIgnoresOtherResourceShapes(t *testing.T) {
	objects, err := decodeSidecarManifests(map[string]string{
		"custom": "kind: Widget\nspec:\n  template: free-form\n  securityContext: custom\ndata:\n  nested: [1, 2]\n",
	})
	require.NoError(t, err)
	require.Len(t, objects, 1)
	assert.Nil(t, objects[0].pod)
	assert.Empty(t, objects[0].Data)
}
