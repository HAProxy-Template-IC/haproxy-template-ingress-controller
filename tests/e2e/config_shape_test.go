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

// TestConfigShape pins what the chart installs: one HAProxyTemplateConfig per
// enabled template library plus the operator's own config, every one marked
// spec.partial, tests inline, merged by the controller in CRD_NAME order.
//
// This deliberately overturns the single-object shape an earlier version of
// this test called "the point, not an implementation detail". That shape was
// the correct answer to the July 2026 revert of !1440, whose two reasons have
// since been dissolved rather than ignored (ADR-0016):
//
//   - "a fragment is not a config, so the CRD must make every field optional"
//     — that price was already paid: ADR-0014 dropped the Required markers and
//     they were never restored. spec.partial plus the CRD's CEL rule now gives
//     apply-time completeness back for standalone objects, enforced by the
//     apiserver, which a webhook (failurePolicy: Ignore) never guaranteed.
//   - "validators already running in clusters judge a fragment as a complete
//     config" — the per-object config webhook is GONE: it structurally cannot
//     judge a multi-object change (it sees the mid-batch state), and the
//     apply-crds hook strips its legacy entry from live clusters during the
//     upgrade. Whole-set validation happens where the whole set is visible:
//     the pre-rollout preflight hook and the fail-closed load gate.
//
// What the split buys is the reason the controller exists as CRDs at all:
// per-object size budgets (worst library: 44% of etcd's limit, versus 99.4%
// for the single object that motivated all of this), and composability — an
// arbitrary number of small configs merged in a declared order.
func TestConfigShape(t *testing.T) {
	feature := features.New("chart installs one config object per library, merged by the controller").
		Assess("several partial objects, operator's config among them", func(ctx context.Context, t *testing.T, cfg *envconf.Config) context.Context {
			items := listHaptic(ctx, t, cfg, configGVR)
			if len(items) < 2 {
				t.Fatalf("expected one HAProxyTemplateConfig per enabled library plus the operator's, got %d — "+
					"a single object puts every library back under one etcd size budget", len(items))
			}

			var haveOperator bool
			var haproxyConfigOwners []string
			for _, item := range items {
				spec, _, _ := unstructured.NestedMap(item.Object, "spec")
				partial, _, _ := unstructured.NestedBool(item.Object, "spec", "partial")
				if !partial {
					t.Fatalf("%s is not marked spec.partial — the CEL completeness rule would "+
						"reject it at apply time, since no chart object is complete alone", item.GetName())
				}
				if _, ok := spec["podSelector"]; ok {
					haveOperator = true
				}
				if _, ok := spec["haproxyConfig"]; ok {
					haproxyConfigOwners = append(haproxyConfigOwners, item.GetName())
				}
				if _, ok := spec["validationTestsSelector"]; ok {
					t.Fatalf("%s carries validationTestsSelector, which is retired: tests live inline", item.GetName())
				}
			}
			if !haveOperator {
				t.Fatal("no object carries podSelector: the operator's own config is missing from the set")
			}
			if len(haproxyConfigOwners) != 1 {
				t.Fatalf("haproxyConfig must have exactly one owner (base), got %v — the controller "+
					"rejects a second non-last owner as a silent template replacement", haproxyConfigOwners)
			}
			return ctx
		}).
		Assess("tests ride the library objects", func(ctx context.Context, t *testing.T, cfg *envconf.Config) context.Context {
			total := 0
			for _, item := range listHaptic(ctx, t, cfg, configGVR) {
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
