//go:build e2e

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

package e2e

import (
	"context"
	"os/exec"
	"strings"
	"testing"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/e2e-framework/pkg/envconf"
	"sigs.k8s.io/e2e-framework/pkg/features"

	hapticclient "gitlab.com/haproxy-haptic/haptic/pkg/generated/clientset/versioned"
)

// TestObservedGenerationIsTruthful is the ground-truth check behind issue #122.
//
// Every other test now trusts status.deployedToPods[].ObservedGeneration to
// decide convergence, so that field has to be verified against something that
// cannot be derived from the same bookkeeping. If the publisher recorded the
// wrong generation, every wait in the suite would pass EARLY — a silent
// false-positive, strictly worse than the false-negative it replaced, and
// invisible to any test that reads the field to decide when to assert.
//
// So this reads the config HAProxy is actually running, off disk in the pod,
// and requires it to be the spec the CR claims that pod is at. One test pays
// the exec cost; the rest read the cheap field.
func TestObservedGenerationIsTruthful(t *testing.T) {
	feature := features.New("Status: observedGeneration matches the config on the pod").
		Assess("a pod at the current spec generation is running that spec's config",
			func(ctx context.Context, t *testing.T, cfg *envconf.Config) context.Context {
				hc, err := hapticclient.NewForConfig(cfg.Client().RESTConfig())
				if err != nil {
					t.Fatalf("build haptic clientset: %v", err)
				}
				cfgs := hc.HaproxyTemplateICV1alpha1().HAProxyCfgs(ControllerNamespace)
				name := HAProxyConfigName + "-haproxycfg"

				// Remember the content of every generation we see, then wait for a
				// pod to report one of them. Requiring the pod to reach the CURRENT
				// generation does not work under the parallel suite: the spec
				// advances faster than pods deploy, so a pod is essentially never
				// at the newest generation and the check would time out on a
				// perfectly healthy cluster (it did, in CI, at 90s).
				contentByGeneration := map[int64]string{}
				var specContent, podName string
				var atGeneration int64

				deadline := time.Now().Add(90 * time.Second)
				for time.Now().Before(deadline) && podName == "" {
					obj, gerr := cfgs.Get(ctx, name, metav1.GetOptions{})
					if gerr != nil {
						t.Fatalf("get HAProxyCfg: %v", gerr)
					}
					if obj.Spec.Compressed {
						t.Skip("spec.content is compressed; this check reads it verbatim")
					}
					if obj.Generation > 0 {
						contentByGeneration[obj.Generation] = obj.Spec.Content
					}
					for _, p := range obj.Status.DeployedToPods {
						if content, known := contentByGeneration[p.ObservedGeneration]; known {
							specContent, podName, atGeneration = content, p.PodName, p.ObservedGeneration
							break
						}
					}
					if podName == "" {
						time.Sleep(time.Second)
					}
				}
				if podName == "" {
					t.Fatal("no pod reported a generation whose spec content this test had captured")
				}
				t.Logf("pod %s reports observedGeneration=%d, whose spec content is known", podName, atGeneration)

				// Ground truth: what HAProxy actually has on disk.
				onDisk := haproxyConfigOnDisk(ctx, t, podName)

				// The dataplane API rewrites the config it stores (it names the
				// anonymous defaults section, reorders directives, drops blank
				// lines — see issue #121), so the bytes are NOT comparable.
				// Compare content that survives that normalisation instead:
				// every backend name the spec declares must be present on disk.
				missing := backendsMissingFrom(specContent, onDisk)
				if len(missing) > 0 {
					t.Fatalf("pod %s claims observedGeneration=%d, but %d backend(s) from that spec are absent from its on-disk config: %v\n"+
						"observedGeneration is not truthful — every wait in this suite trusts it",
						podName, atGeneration, len(missing), missing)
				}
				return ctx
			}).Feature()

	testEnv.Test(t, feature)
}

// haproxyConfigOnDisk returns the running configuration from the pod's haproxy
// container — the state no controller bookkeeping can fake.
func haproxyConfigOnDisk(ctx context.Context, t *testing.T, podName string) string {
	t.Helper()
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()

	out, err := exec.CommandContext(ctx, "kubectl", "-n", ControllerNamespace,
		"exec", podName, "-c", "haproxy", "--", "cat", "/etc/haproxy/haproxy.cfg").Output()
	if err != nil {
		t.Fatalf("read on-disk config from %s: %v", podName, err)
	}
	return string(out)
}

// backendsMissingFrom returns the backend names declared in spec that do not
// appear in onDisk. Backend names are stable identifiers the dataplane's
// normalisation preserves, which byte comparison would not survive.
func backendsMissingFrom(spec, onDisk string) []string {
	var missing []string
	for _, line := range strings.Split(spec, "\n") {
		name, ok := strings.CutPrefix(strings.TrimSpace(line), "backend ")
		if !ok {
			continue
		}
		name = strings.TrimSpace(name)
		if name == "" {
			continue
		}
		if !declaresBackend(onDisk, name) {
			missing = append(missing, name)
		}
	}
	return missing
}

// declaresBackend reports whether cfg has a `backend <name>` section. Matching
// on the whole token matters: a substring test would accept "backend foobar"
// as proof that "foo" is present, quietly weakening the ground truth this file
// exists to provide.
func declaresBackend(cfg, name string) bool {
	for _, line := range strings.Split(cfg, "\n") {
		rest, ok := strings.CutPrefix(strings.TrimSpace(line), "backend ")
		if !ok {
			continue
		}
		fields := strings.Fields(rest)
		if len(fields) > 0 && fields[0] == name {
			return true
		}
	}
	return false
}
