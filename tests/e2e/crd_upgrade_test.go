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

//go:build e2e

package e2e

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"testing"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/e2e-framework/pkg/envconf"
	"sigs.k8s.io/e2e-framework/pkg/features"
)

// effectiveResolution mirrors pkg/core/config.Resolution as served by
// /debug/vars/effectiveConfigResolution (introspection marshals the Go
// struct, so keys are the exported field names).
type effectiveResolution struct {
	ResolvedVersions map[string]string `json:"ResolvedVersions"`
	Unavailable      []string          `json:"Unavailable"`
	StrippedSnippets []string          `json:"StrippedSnippets"`
	StrippedTests    []string          `json:"StrippedTests"`
}

// healthzSettled reports whether /healthz is 200 WITHOUT any entry riding
// the reinit grace period (applyReinitGrace annotates tolerated entries with
// "reinitializing (grace period)"). During a reinit the endpoint already
// reports 200 — by design, so kubelet doesn't kill the pod — so tests that
// must sequence AFTER a reinit completes need this stronger signal.
func (dc *debugClient) healthzSettled(ctx context.Context) bool {
	body, err := dc.clientset.CoreV1().Services(dc.namespace).ProxyGet(
		"http", dc.serviceName, dc.port, HealthzPath, nil,
	).DoRaw(ctx)
	if err != nil {
		return false
	}
	return !bytes.Contains(body, []byte("reinitializing (grace period)"))
}

func (dc *debugClient) getEffectiveResolution(ctx context.Context) (*effectiveResolution, error) {
	body, err := dc.loopback.Get(ctx, "/debug/vars/effectiveConfigResolution")
	if err != nil {
		return nil, err
	}
	var res effectiveResolution
	if err := json.Unmarshal(body, &res); err != nil {
		return nil, fmt.Errorf("decode effective resolution: %w (body=%s)", err, body)
	}
	return &res, nil
}

// controllerPodFingerprint captures identity + restart counts of the
// controller pods so the test can prove convergence happened WITHOUT a pod
// restart (the whole point of the runtime CRD watch).
func controllerPodFingerprint(ctx context.Context, dc *debugClient) (map[string]int32, error) {
	pods, err := dc.clientset.CoreV1().Pods(ControllerNamespace).List(ctx,
		metav1.ListOptions{LabelSelector: LabelSelectorController})
	if err != nil {
		return nil, err
	}
	out := make(map[string]int32, len(pods.Items))
	for i := range pods.Items {
		p := &pods.Items[i]
		var restarts int32
		for _, cs := range p.Status.ContainerStatuses {
			restarts += cs.RestartCount
		}
		out[string(p.UID)] = restarts
	}
	return out, nil
}

// kubectlJSON runs kubectl with -o json and returns the raw output.
func kubectlJSON(ctx context.Context, args ...string) ([]byte, error) {
	full := append([]string{"--kubeconfig", kubeconfigPath}, args...)
	full = append(full, "-o", "json")
	out, err := exec.CommandContext(ctx, "kubectl", full...).Output()
	if err != nil {
		return nil, fmt.Errorf("kubectl %v: %w", args, err)
	}
	return out, nil
}

// TestGatewayAPICRDUpgradeInPlace proves the runtime version-detection
// contract end to end (runtime-version-detection spec: "CRD Change
// Reinitialization"): removing a watched kind's CRD strips its features at
// runtime, re-installing it re-activates them — with NO helm operation and
// NO controller pod restart.
//
// The test uses the TCPRoute CRD as the guinea pig because it is optional,
// self-contained (its features strip atomically), and unused by every other
// e2e test. It deliberately does NOT call t.Parallel(): deleting a CRD is a
// cluster-global act, and running it in the sequential phase (while all
// parallel tests are still queued) keeps it isolated. The CRD is restored
// and convergence re-verified before the test returns.
func TestGatewayAPICRDUpgradeInPlace(t *testing.T) {
	const crdName = "tcproutes.gateway.networking.k8s.io"

	var (
		dc          *debugClient
		crdManifest []byte
		fingerprint map[string]int32
	)

	waitResolution := func(ctx context.Context, t *testing.T, wantWatched bool) *effectiveResolution {
		t.Helper()
		deadline := time.Now().Add(3 * time.Minute)
		var last *effectiveResolution
		var lastErr error
		for time.Now().Before(deadline) {
			res, err := dc.getEffectiveResolution(ctx)
			if err == nil {
				last = res
				if _, watched := res.ResolvedVersions["tcproutes"]; watched == wantWatched {
					// The resolution flips at iteration start; also require
					// the controller to be fully SETTLED (healthy without
					// the reinit grace annotation) so this test — and the
					// sequential tests after it — don't race the rebuild
					// (e.g. the admission webhook coming back up).
					if dc.healthzSettled(ctx) {
						return res
					}
				}
			} else {
				lastErr = err
			}
			time.Sleep(2 * time.Second)
		}
		t.Fatalf("timed out waiting for tcproutes watched=%v (last resolution: %+v, last error: %v)",
			wantWatched, last, lastErr)
		return nil
	}

	feature := features.New("Runtime CRD upgrade: remove + reinstall TCPRoute CRD without helm or pod restart").
		Setup(func(ctx context.Context, t *testing.T, cfg *envconf.Config) context.Context {
			client, err := cfg.NewClient()
			if err != nil {
				t.Fatalf("new client: %v", err)
			}
			cs, err := newClientsetForE2E(client.RESTConfig())
			if err != nil {
				t.Fatalf("build clientset: %v", err)
			}
			dc = newDebugClient(client.RESTConfig(), cs)

			// Baseline: TCPRoute watched at v1 (the suite installs the
			// v1.6.0 standard channel).
			res := waitResolution(ctx, t, true)
			if got := res.ResolvedVersions["tcproutes"]; got != "gateway.networking.k8s.io/v1" {
				t.Fatalf("baseline: tcproutes resolved to %q, want gateway.networking.k8s.io/v1", got)
			}

			fingerprint, err = controllerPodFingerprint(ctx, dc)
			if err != nil {
				t.Fatalf("pod fingerprint: %v", err)
			}
			if len(fingerprint) == 0 {
				t.Fatal("no controller pods found for fingerprinting")
			}

			// Capture the CRD manifest for restoration, cleaned of
			// server-populated fields so kubectl apply can re-create it.
			raw, err := kubectlJSON(ctx, "get", "crd", crdName)
			if err != nil {
				t.Fatalf("capture CRD: %v", err)
			}
			var obj map[string]any
			if err := json.Unmarshal(raw, &obj); err != nil {
				t.Fatalf("decode CRD: %v", err)
			}
			delete(obj, "status")
			if md, ok := obj["metadata"].(map[string]any); ok {
				for _, f := range []string{"uid", "resourceVersion", "creationTimestamp", "generation", "managedFields"} {
					delete(md, f)
				}
			}
			crdManifest, err = json.Marshal(obj)
			if err != nil {
				t.Fatalf("re-encode CRD: %v", err)
			}
			return ctx
		}).
		Assess("deleting the CRD strips the TCPRoute feature at runtime", func(ctx context.Context, t *testing.T, cfg *envconf.Config) context.Context {
			cmd := exec.CommandContext(ctx, "kubectl", "--kubeconfig", kubeconfigPath, "delete", "crd", crdName)
			if out, err := cmd.CombinedOutput(); err != nil {
				t.Fatalf("delete CRD: %v (output: %s)", err, out)
			}

			res := waitResolution(ctx, t, false)
			found := false
			for _, name := range res.Unavailable {
				if name == "tcproutes" {
					found = true
				}
			}
			if !found {
				t.Fatalf("tcproutes not listed unavailable after CRD deletion: %+v", res)
			}
			if len(res.StrippedSnippets) == 0 || len(res.StrippedTests) == 0 {
				t.Fatalf("expected TCPRoute snippets/tests to be stripped, got %+v", res)
			}
			return ctx
		}).
		Assess("reinstalling the CRD re-activates the feature without helm or pod restart", func(ctx context.Context, t *testing.T, cfg *envconf.Config) context.Context {
			if err := kubectlApplyStdin(ctx, crdManifest); err != nil {
				t.Fatalf("restore CRD: %v", err)
			}

			res := waitResolution(ctx, t, true)
			if got := res.ResolvedVersions["tcproutes"]; got != "gateway.networking.k8s.io/v1" {
				t.Fatalf("after reinstall: tcproutes resolved to %q, want gateway.networking.k8s.io/v1", got)
			}
			if len(res.Unavailable) != 0 {
				t.Fatalf("after reinstall: unexpected unavailable resources: %v", res.Unavailable)
			}

			after, err := controllerPodFingerprint(ctx, dc)
			if err != nil {
				t.Fatalf("pod fingerprint after: %v", err)
			}
			for uid, restarts := range fingerprint {
				got, ok := after[uid]
				if !ok {
					t.Fatalf("controller pod %s was replaced during the CRD upgrade (runtime convergence must not restart pods)", uid)
				}
				if got != restarts {
					t.Fatalf("controller pod %s restarted during the CRD upgrade (%d -> %d restarts)", uid, restarts, got)
				}
			}
			return ctx
		}).
		Feature()

	testEnv.Test(t, feature)
}
