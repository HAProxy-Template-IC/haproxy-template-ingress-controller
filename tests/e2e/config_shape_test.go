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

var configGVR = schema.GroupVersionResource{
	Group: "haproxy-haptic.org", Version: "v1alpha1", Resource: "haproxytemplateconfigs",
}

var libraryGVR = schema.GroupVersionResource{
	Group:    "haproxy-haptic.org",
	Version:  "v1alpha1",
	Resource: "haproxytemplatelibraries",
}

// TestConfigShape pins what the chart installs.
//
// The chart renders ONE HAProxyTemplateConfig plus one HAProxyTemplateLibrary
// per enabled library. The config stays small enough to read and edit (~1% of
// etcd's per-object limit); the snippets carry the bulk, which measured 94%
// templateSnippets + validationTests.
//
// Snippets carry content only — no podSelector, watchedResources or dataplane —
// so a library cannot redefine the controller's operational identity. That is
// stronger than the spec.partial model this replaces, which waived the CRD's
// completeness rule for every object in the set.
func TestConfigShape(t *testing.T) {
	feature := features.New("chart installs one config object per library, merged by the controller").
		Assess("exactly one config, referencing every library's snippets", func(ctx context.Context, t *testing.T, cfg *envconf.Config) context.Context {
			configs := listHaptic(ctx, t, cfg, configGVR)
			if len(configs) != 1 {
				t.Fatalf("expected exactly one HAProxyTemplateConfig, got %d — bulk content belongs "+
					"in HAProxyTemplateLibrary, leaving one config an operator can read and edit", len(configs))
			}
			config := configs[0]

			if _, ok, _ := unstructured.NestedMap(config.Object, "spec", "podSelector"); !ok {
				t.Fatal("the config carries no podSelector; no snippet can supply it, so the " +
					"apiserver's CEL rule should have refused this object")
			}
			if watched, _, _ := unstructured.NestedMap(config.Object, "spec", "watchedResources"); len(watched) == 0 {
				t.Fatal("the config declares no watchedResources; the union of every library's is meant to land here")
			}

			refs, _, _ := unstructured.NestedSlice(config.Object, "spec", "libraryRefs")
			if len(refs) == 0 {
				t.Fatal("the config references no HAProxyTemplateLibrary: the libraries would not be merged at all")
			}

			observed := map[string]string{}
			for _, item := range listHaptic(ctx, t, cfg, libraryGVR) {
				revision, _, _ := unstructured.NestedString(item.Object, "spec", "revision")
				observed[item.GetName()] = revision

				for _, field := range []string{"podSelector", "watchedResources", "dataplane", "validators"} {
					if _, ok := item.Object["spec"].(map[string]any)[field]; ok {
						t.Fatalf("%s carries spec.%s — a library must not be able to redefine the "+
							"controller's operational identity", item.GetName(), field)
					}
				}
			}

			// Every reference must resolve at the revision it names, or the
			// controller holds last-good and the fleet silently stops updating.
			for i, entry := range refs {
				fields, _ := entry.(map[string]any)
				name, _ := fields["name"].(string)
				want, _ := fields["revision"].(string)
				got, present := observed[name]
				if !present {
					t.Fatalf("libraryRefs[%d] names %q, which does not exist", i, name)
				}
				if got != want {
					t.Fatalf("libraryRefs[%d] expects %s at revision %q, but it reports %q", i, name, want, got)
				}
			}
			return ctx
		}).
		Assess("tests ride the snippets objects", func(ctx context.Context, t *testing.T, cfg *envconf.Config) context.Context {
			total := 0
			for _, item := range listHaptic(ctx, t, cfg, libraryGVR) {
				suite, _, _ := unstructured.NestedMap(item.Object, "spec", "validationTests")
				total += len(suite)
			}
			if total == 0 {
				t.Fatal("no object carries validationTests: an empty suite passes unconditionally, " +
					"so the load gate would be running nothing")
			}
			return ctx
		}).
		Assess("the controller validated the set and stamped EVERY source", func(ctx context.Context, t *testing.T, cfg *envconf.Config) context.Context {
			// The verdict is a property of the merged set, and observedGeneration
			// is only meaningful against the same object's metadata.generation —
			// so every object must carry Validated=True at its own generation.
			// A designated primary could not represent a shard edit at all.
			deadline := time.Now().Add(2 * time.Minute)
			var last string
			for time.Now().Before(deadline) {
				items := listHaptic(ctx, t, cfg, configGVR)
				allStamped := true
				last = ""
				for _, item := range items {
					if reason, ok := validatedAtOwnGeneration(&item); !ok {
						allStamped = false
						last = fmt.Sprintf("%s: %s", item.GetName(), reason)
						break
					}
				}
				if allStamped {
					return ctx
				}
				time.Sleep(2 * time.Second)
			}
			t.Fatalf("controller never stamped every source of the merged set: %s", last)
			return ctx
		}).Feature()

	testEnv.Test(t, feature)
}

// validatedAtOwnGeneration reports whether the object carries Validated=True
// with observedGeneration matching its own metadata.generation.
func validatedAtOwnGeneration(item *unstructured.Unstructured) (string, bool) {
	conditions, _, _ := unstructured.NestedSlice(item.Object, "status", "conditions")
	for _, c := range conditions {
		cond, ok := c.(map[string]any)
		if !ok || cond["type"] != "Validated" {
			continue
		}
		if cond["status"] != "True" {
			return fmt.Sprintf("Validated=%v (%v)", cond["status"], cond["reason"]), false
		}
		observed, _ := cond["observedGeneration"].(int64)
		if observed != item.GetGeneration() {
			return fmt.Sprintf("Validated=True but observedGeneration=%d, generation=%d", observed, item.GetGeneration()), false
		}
		return "", true
	}
	return "no Validated condition", false
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
