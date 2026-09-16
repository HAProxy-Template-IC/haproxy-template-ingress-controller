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

//go:build e2e

package e2e

import (
	"context"
	"fmt"
	"net/http"
	"testing"

	networkingv1 "k8s.io/api/networking/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/util/retry"
	"sigs.k8s.io/e2e-framework/klient"
	"sigs.k8s.io/e2e-framework/pkg/envconf"
	"sigs.k8s.io/e2e-framework/pkg/features"

	"gitlab.com/haproxy-haptic/haptic/tests/e2e/httpclient"
)

// TestHapticCanaryWeightStepIsReloadFree proves a canary rollout step is a map
// edit: the weight lives in ing-canary-weight.map, so moving it from 0 to 100
// and back switches where plain traffic lands without reloading HAProxy on any
// version. Weights 0 and 100 are the two values rand(100) can't blur, so each
// step has one right answer.
//
// Serial like the other reload-free suites: a reload count is attributable only
// on a quiet fleet.
func TestHapticCanaryWeightStepIsReloadFree(t *testing.T) {
	const (
		host       = "ingress-haptic-canary-rf.localdev.me"
		canaryName = "echo-canary-rf"
	)
	var (
		client klient.Client
		cs     kubernetes.Interface
		ns     string
	)
	feature := features.New("Ingress: haproxy-haptic.org/canary-weight step is reload-free").
		Setup(func(ctx context.Context, t *testing.T, cfg *envconf.Config) context.Context {
			t.Helper()
			var err error
			if client, err = cfg.NewClient(); err != nil {
				t.Fatalf("new client: %v", err)
			}
			if cs, err = newClientsetForE2E(client.RESTConfig()); err != nil {
				t.Fatalf("build clientset: %v", err)
			}
			ns = NamespaceForTest(ctx, t, client)
			DumpLogsOnFailure(t, ns)
			mainBackend := NewEchoServerBackend(ctx, t, client, ns)
			canaryBackend := NewEchoServerV2Backend(ctx, t, client, ns)
			NewIngress(ctx, t, client, ns, &IngressSpec{
				Name:           "echo-main-rf",
				Host:           host,
				BackendService: mainBackend.Service,
				BackendPort:    mainBackend.Port,
			})
			NewIngress(ctx, t, client, ns, &IngressSpec{
				Name:           canaryName,
				Host:           host,
				BackendService: canaryBackend.Service,
				BackendPort:    canaryBackend.Port,
				Annotations: map[string]string{
					"haproxy-haptic.org/canary":        "true",
					"haproxy-haptic.org/canary-weight": "0",
				},
			})
			httpclient.New(t).GET(host, "/").ExpectEchoEnvironment(t, "")
			return ctx
		}).
		Assess("stepping the weight to 100 and back moves plain traffic without a reload",
			func(ctx context.Context, t *testing.T, cfg *envconf.Config) context.Context {
				t.Helper()
				hc := httpclient.New(t)
				waitFleetQuiescent(ctx, t, client, cs)
				before := captureReloadFingerprint(ctx, t, cs)

				setCanaryWeight(ctx, t, client, ns, canaryName, "100")
				reloadFreeReaction(ctx, t, cs, host+"/ lands on the canary", echoEnvironmentIs(hc, host, "v2"))
				setCanaryWeight(ctx, t, client, ns, canaryName, "0")
				reloadFreeReaction(ctx, t, cs, host+"/ lands on the main", echoEnvironmentIs(hc, host, ""))

				assertReloadFree(t, before, captureReloadFingerprint(ctx, t, cs), "canary weight 0→100→0")
				return ctx
			}).
		Feature()
	testEnv.Test(t, feature)
}

func setCanaryWeight(ctx context.Context, t *testing.T, client klient.Client, ns, name, weight string) {
	t.Helper()
	// The controller writes the Ingress status concurrently, so a bare Get→Update
	// can lose the optimistic-concurrency race.
	if err := retry.RetryOnConflict(retry.DefaultRetry, func() error {
		ing := &networkingv1.Ingress{}
		if err := client.Resources(ns).Get(ctx, name, ns, ing); err != nil {
			return err
		}
		ing.Annotations["haproxy-haptic.org/canary-weight"] = weight
		return client.Resources(ns).Update(ctx, ing)
	}); err != nil {
		t.Fatalf("set canary-weight %s on %s/%s: %v", weight, ns, name, err)
	}
}

// echoEnvironmentIs reports whether a plain request to host reaches the echo
// server started with ENVIRONMENT=want.
func echoEnvironmentIs(hc *httpclient.Client, host, want string) func(ctx context.Context) (bool, error) {
	return func(ctx context.Context) (bool, error) {
		resp, err := hc.GET(host, "/").Do(ctx)
		if err != nil {
			return false, err
		}
		if resp.Status != http.StatusOK || resp.Echo == nil {
			return false, fmt.Errorf("status %d, echo body %t", resp.Status, resp.Echo != nil)
		}
		return resp.Echo.Environment == want, nil
	}
}
