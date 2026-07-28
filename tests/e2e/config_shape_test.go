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
	"fmt"
	"testing"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/dynamic"
	"sigs.k8s.io/e2e-framework/pkg/envconf"
	"sigs.k8s.io/e2e-framework/pkg/features"
)

var (
	configGVR = schema.GroupVersionResource{
		Group: "haproxy-haptic.org", Version: "v1alpha1", Resource: "haproxytemplateconfigs",
	}
	testsGVR = schema.GroupVersionResource{
		Group: "haproxy-haptic.org", Version: "v1alpha1", Resource: "haproxyvalidationtests",
	}
)

// TestConfigShape pins what the chart installs: exactly one configuration
// object, with its validation tests in a companion object.
//
// The shape is the point, not an implementation detail. Splitting the
// configuration across objects is what made it impossible to validate as a
// whole, and what broke `helm upgrade` from a pre-split release — the running
// old webhook judged each fragment standalone and denied it.
func TestConfigShape(t *testing.T) {
	feature := features.New("chart installs one config plus a companion tests object").
		Assess("exactly one HAProxyTemplateConfig", func(ctx context.Context, t *testing.T, cfg *envconf.Config) context.Context {
			items := listHaptic(ctx, t, cfg, configGVR)
			if len(items) != 1 {
				names := make([]string, 0, len(items))
				for _, i := range items {
					names = append(names, i.GetName())
				}
				t.Fatalf("expected exactly one HAProxyTemplateConfig, got %d: %v — "+
					"a configuration spread across objects cannot be validated as a whole", len(items), names)
			}
			return ctx
		}).
		Assess("the config is complete on its own", func(ctx context.Context, t *testing.T, cfg *envconf.Config) context.Context {
			config := listHaptic(ctx, t, cfg, configGVR)[0]
			spec, _, _ := unstructured.NestedMap(config.Object, "spec")
			for _, field := range []string{"podSelector", "credentialsSecretRef", "haproxyConfig", "watchedResources"} {
				if _, present := spec[field]; !present {
					t.Fatalf("spec.%s missing: the single object must be complete, which is what lets "+
						"admission and the load gate judge it without fetching anything else", field)
				}
			}
			return ctx
		}).
		Assess("tests live in a companion object the config selects", func(ctx context.Context, t *testing.T, cfg *envconf.Config) context.Context {
			config := listHaptic(ctx, t, cfg, configGVR)[0]
			spec, _, _ := unstructured.NestedMap(config.Object, "spec")

			if _, inline := spec["validationTests"]; inline {
				t.Fatal("the chart should not inline validationTests: they are ~36% of the spec and " +
					"are what pushed a migration profile past etcd's per-object limit")
			}
			if _, ok := spec["validationTestsSelector"]; !ok {
				t.Fatal("spec.validationTestsSelector missing — without it the companion object is never found")
			}

			tests := listHaptic(ctx, t, cfg, testsGVR)
			if len(tests) != 1 {
				t.Fatalf("expected one HAProxyValidationTests, got %d", len(tests))
			}
			suite, _, _ := unstructured.NestedMap(tests[0].Object, "spec", "validationTests")
			if len(suite) == 0 {
				t.Fatal("companion object carries no tests: an empty suite passes unconditionally, " +
					"so this would look identical to a suite that ran and passed")
			}
			return ctx
		}).
		Assess("the controller loaded that suite", func(ctx context.Context, t *testing.T, cfg *envconf.Config) context.Context {
			// requireValidationTests refuses a configuration that ends up with no
			// tests, so Validated=True here is the controller reporting it found
			// and ran the companion suite — not merely that the config parsed.
			config := listHaptic(ctx, t, cfg, configGVR)[0]
			required, _, _ := unstructured.NestedBool(config.Object, "spec", "requireValidationTests")
			if !required {
				t.Fatal("spec.requireValidationTests is not set, so a lost suite would load silently")
			}

			// Polled, not asserted once: the condition is written after the load
			// gate runs, which is asynchronous with the readiness the suite waits
			// on. A single read races it.
			deadline := time.Now().Add(2 * time.Minute)
			var last string
			for time.Now().Before(deadline) {
				current := listHaptic(ctx, t, cfg, configGVR)[0]
				conditions, _, _ := unstructured.NestedSlice(current.Object, "status", "conditions")
				for _, c := range conditions {
					cond, ok := c.(map[string]any)
					if !ok || cond["type"] != "Validated" {
						continue
					}
					if cond["status"] == "True" {
						return ctx
					}
					last = fmt.Sprintf("Validated=%v (%v)", cond["status"], cond["reason"])
				}
				time.Sleep(2 * time.Second)
			}
			if last == "" {
				last = "no Validated condition ever appeared"
			}
			t.Fatalf("controller never accepted the config plus its suite: %s", last)
			return ctx
		}).Feature()

	testEnv.Test(t, feature)
}

func listHaptic(ctx context.Context, t *testing.T, cfg *envconf.Config, gvr schema.GroupVersionResource) []unstructured.Unstructured {
	t.Helper()
	dyn, err := dynamic.NewForConfig(cfg.Client().RESTConfig())
	if err != nil {
		t.Fatalf("dynamic client: %v", err)
	}
	list, err := dyn.Resource(gvr).Namespace(ControllerNamespace).List(ctx, metav1.ListOptions{})
	if err != nil {
		t.Fatalf("listing %s: %v", gvr.Resource, err)
	}
	return list.Items
}
