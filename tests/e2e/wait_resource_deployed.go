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
	"sigs.k8s.io/e2e-framework/klient"
)

// Wait for the resource the test just applied to report the controller's
// deployed-phase status, instead of inspecting the shared HAProxyCfg.
//
// The controller already writes exactly this signal per resource, and since
// statusapplier gates the "deployed" variant on Succeeded == Total it means
// "every HAProxy replica has this configuration" — which is the question these
// tests are asking. Reading it from the resource under test removes the whole
// class of problems that came from inferring it out of a shared object: no
// marker to choose, no checksum-to-generation join, no dependence on which spec
// versions this process's watch happened to receive, and no ~2MB object rescanned
// by ~30 concurrent waiters. A watch that misses events cannot lie here, because
// the predicate is level-triggered on the resource's own current state.
//
// Kubernetes convention does the ordering: the chart stamps the route's own
// metadata.generation into each condition's observedGeneration, so "this status
// describes THIS version of my resource" is decidable from the object alone.

// resourceDeployedTimeout bounds the per-resource wait. Same budget reasoning as
// the fleet-wide wait it replaces — reconcile debounce (<=2s) + one
// minDeploymentInterval (5s) + the per-pod sync (~1-2s).
const resourceDeployedTimeout = 12 * time.Second

// gatewayGVR and httpRouteGVR are declared in gateway_churn_test.go.
var (
	ingressGVR   = schema.GroupVersionResource{Group: "networking.k8s.io", Version: "v1", Resource: "ingresses"}
	grpcRouteGVR = schema.GroupVersionResource{Group: "gateway.networking.k8s.io", Version: "v1", Resource: "grpcroutes"}
)

// waitForIngressDeployed blocks until the Ingress carries a load-balancer
// address. The chart writes the address under the "deployed" variant and an
// EMPTY list under "deployFailed", so a non-empty list is unambiguous.
func waitForIngressDeployed(ctx context.Context, t *testing.T, client klient.Client, namespace, name string) {
	t.Helper()
	waitForResourceDeployed(ctx, t, client, ingressGVR, namespace, name,
		func(obj *unstructured.Unstructured) (bool, string) {
			addrs, found, err := unstructured.NestedSlice(obj.Object, "status", "loadBalancer", "ingress")
			if err != nil || !found || len(addrs) == 0 {
				return false, "status.loadBalancer.ingress is empty (not deployed to every replica yet)"
			}
			return true, ""
		})
}

// waitForRouteDeployed blocks until every parent of the route reports conditions
// for the route's CURRENT generation. The chart emits route status under the
// "deployed" variant only, so the presence of up-to-date parents is itself the
// deployed signal; comparing observedGeneration to metadata.generation is what
// makes a status left over from a previous version of the route not count.
func waitForRouteDeployed(ctx context.Context, t *testing.T, client klient.Client, gvr schema.GroupVersionResource, namespace, name string) {
	t.Helper()
	waitForResourceDeployed(ctx, t, client, gvr, namespace, name,
		func(obj *unstructured.Unstructured) (bool, string) {
			parents, found, err := unstructured.NestedSlice(obj.Object, "status", "parents")
			if err != nil || !found || len(parents) == 0 {
				return false, "status.parents is empty (controller has not reported a deploy for this route)"
			}
			gen := obj.GetGeneration()
			for _, p := range parents {
				pm, ok := p.(map[string]any)
				if !ok {
					return false, "malformed status.parents entry"
				}
				conds, _, _ := unstructured.NestedSlice(pm, "conditions")
				if len(conds) == 0 {
					return false, "a parent has no conditions yet"
				}
				for _, c := range conds {
					cm, ok := c.(map[string]any)
					if !ok {
						return false, "malformed condition"
					}
					og, _, _ := unstructured.NestedInt64(cm, "observedGeneration")
					if og != gen {
						typ, _, _ := unstructured.NestedString(cm, "type")
						return false, fmt.Sprintf("condition %q is at observedGeneration %d, route is at %d", typ, og, gen)
					}
				}
			}
			return true, ""
		})
}

// waitForGatewayDeployed blocks until the Gateway reports Programmed=True for
// its current generation.
func waitForGatewayDeployed(ctx context.Context, t *testing.T, client klient.Client, namespace, name string) {
	t.Helper()
	waitForResourceDeployed(ctx, t, client, gatewayGVR, namespace, name,
		func(obj *unstructured.Unstructured) (bool, string) {
			conds, found, err := unstructured.NestedSlice(obj.Object, "status", "conditions")
			if err != nil || !found {
				return false, "status.conditions absent"
			}
			gen := obj.GetGeneration()
			for _, c := range conds {
				cm, ok := c.(map[string]any)
				if !ok {
					continue
				}
				typ, _, _ := unstructured.NestedString(cm, "type")
				if typ != "Programmed" {
					continue
				}
				status, _, _ := unstructured.NestedString(cm, "status")
				og, _, _ := unstructured.NestedInt64(cm, "observedGeneration")
				if status == "True" && og == gen {
					return true, ""
				}
				return false, fmt.Sprintf("Programmed=%s at observedGeneration %d, gateway is at %d", status, og, gen)
			}
			return false, "no Programmed condition yet"
		})
}

// waitForResourceDeployed polls the named resource until ready reports true.
//
// A poll, not a watch: the predicate is level-triggered on the object's own
// state, so there is nothing an edge could carry that a re-read does not, and a
// poll cannot be defeated by a 410 resync dropping the transition we needed —
// which is precisely how the shared-object watch failed (#122).
func waitForResourceDeployed(
	ctx context.Context,
	t *testing.T,
	client klient.Client,
	gvr schema.GroupVersionResource,
	namespace, name string,
	ready func(*unstructured.Unstructured) (bool, string),
) {
	t.Helper()

	dyn, err := dynamic.NewForConfig(client.RESTConfig())
	if err != nil {
		t.Fatalf("waitForResourceDeployed: dynamic client: %v", err)
	}

	ctx, cancel := context.WithTimeout(ctx, resourceDeployedTimeout)
	defer cancel()

	lastReason := "no observation yet"
	for {
		obj, getErr := dyn.Resource(gvr).Namespace(namespace).Get(ctx, name, metav1.GetOptions{})
		switch {
		case getErr != nil:
			lastReason = fmt.Sprintf("get %s %s/%s: %v", gvr.Resource, namespace, name, getErr)
		default:
			done, reason := ready(obj)
			if done {
				return
			}
			lastReason = reason
		}

		select {
		case <-ctx.Done():
			t.Fatalf("waitForResourceDeployed %s %s/%s: %v (last state: %s)",
				gvr.Resource, namespace, name, ctx.Err(), lastReason)
		case <-time.After(250 * time.Millisecond):
		}
	}
}
