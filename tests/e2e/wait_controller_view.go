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
	"context"
	"fmt"
	"strconv"
	"strings"
	"testing"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/e2e-framework/klient"

	hapticclient "gitlab.com/haproxy-haptic/haptic/pkg/generated/clientset/versioned"
	"gitlab.com/haproxy-haptic/haptic/tests/testutil"
)

// controllerForgetTimeout caps how long the test cleanup waits for the
// controller's rendered config to no longer mention a deleted Ingress's
// namespace. The controller's informer typically catches up in well under
// 1s, but we allow a generous budget so transient API-server slowness
// during parallel teardown doesn't cause spurious cleanup-warning logs.
const controllerForgetTimeout = 10 * time.Second

// controllerDeployedTimeout caps the post-apply wait for HAProxyCfg
// status to report all HAProxy pods at the latest spec.Checksum. Under
// parallel-test load the controller may be processing several reconciles
// back-to-back; allow generous headroom so the wait survives normal
// contention without spurious failures.
const controllerDeployedTimeout = 90 * time.Second

// waitForControllerForgetNamespace polls the controller's /debug/vars/rendered
// endpoint until the rendered haproxy.cfg no longer contains the given
// namespace. This is used during test cleanup, after an Ingress has been
// explicitly deleted from the API server, to bound the window in which the
// controller's resource-store still contains the (now stale) Ingress.
//
// The race we close: apiserver Delete returns synchronously, but the
// controller's watcher has its own latency. While the watcher is catching
// up, the controller's render still includes the Ingress; if another
// parallel test's webhook validation fires during that window and the
// referenced Secret has already been cascade-deleted, the render fails
// with an [ALERT]-level "unable to find userlist" / "Secret does not
// exist" error and admission is denied for the unrelated resource.
//
// On timeout we log and return without failing the test — cleanup is
// best-effort, and the test that triggered the cleanup has already
// completed. The cap exists so a stuck controller can't wedge the suite.
func waitForControllerForgetNamespace(ctx context.Context, t *testing.T, client klient.Client, namespace string) {
	t.Helper()

	cs, err := newClientsetForE2E(client.RESTConfig())
	if err != nil {
		t.Logf("waitForControllerForgetNamespace: build clientset: %v (skipping wait)", err)
		return
	}
	dc := &debugClient{
		clientset:   cs,
		namespace:   ControllerNamespace,
		serviceName: DebugServiceNameValue,
		port:        strconv.Itoa(DebugPort),
	}

	cfg := testutil.FastWaitConfig()
	cfg.Timeout = controllerForgetTimeout

	err = testutil.WaitForConditionWithDescription(ctx, cfg,
		"controller forgot namespace "+namespace,
		func(ctx context.Context) (bool, error) {
			rendered, err := dc.getRenderedConfig(ctx)
			if err != nil {
				return false, err
			}
			return !strings.Contains(rendered, namespace), nil
		})
	if err != nil {
		t.Logf("waitForControllerForgetNamespace %q: %v (cleanup proceeding anyway)", namespace, err)
	}
}

// waitForControllerDeployed blocks until the controller's HAProxyCfg
// reports that EVERY HAProxy pod has applied the spec's current
// checksum AND the rendered spec.Content contains the supplied marker.
//
// This is the controller's authoritative post-convergence signal,
// surviving across reconciliation cycles (unlike /debug/vars/pipeline,
// whose phase status is wiped on every new trigger):
//
//   - spec.Checksum is what the publisher wrote for the latest render
//     (config + auxiliary file content fed through
//     dataplane.ComputeContentChecksum, the same function the deployer
//     stamps onto each ConfigAppliedToPodEvent.Checksum).
//   - status.deployedToPods[].Checksum carries that per-pod checksum,
//     updated when the deployer's per-endpoint Sync returns success
//     (i.e., the dataplane API confirmed reload via VerifyReload polling).
//   - When every entry's Checksum equals spec.Checksum, every HAProxy
//     pod has applied and reload-verified the current spec.
//
// The marker check (spec.Content contains `marker`) ensures the resource
// our caller just applied is actually in the latest render — without
// it, a deployment that happened BEFORE our apply (and stably matched
// on the older spec.Checksum) could satisfy the per-pod equality.
// The marker is typically the resource's namespace, which the chart
// embeds in backend names.
//
// Reading both Checksum and Content from the SAME HAProxyCfg Get
// (single apiserver call per poll, no service proxy) keeps the wait
// cheap under parallel-test load — earlier revisions that also fetched
// /debug/vars/rendered hammered the API-server proxy and got "unknown"
// 5xx errors as the proxy buckled.
//
// Fail on timeout: every assertion that follows assumes convergence;
// silently proceeding would just cascade into a misleading later
// failure.
func waitForControllerDeployed(ctx context.Context, t *testing.T, client klient.Client, marker string) {
	t.Helper()

	hc, err := hapticclient.NewForConfig(client.RESTConfig())
	if err != nil {
		t.Fatalf("waitForControllerDeployed: build haptic clientset: %v", err)
	}

	cfg := testutil.DefaultWaitConfig()
	cfg.Timeout = controllerDeployedTimeout

	cfgName := HAProxyConfigName + "-haproxycfg"
	if err := testutil.WaitForConditionWithDescription(ctx, cfg,
		"HAProxyCfg deployed "+marker,
		func(ctx context.Context) (bool, error) {
			obj, err := hc.HaproxyTemplateICV1alpha1().HAProxyCfgs(ControllerNamespace).Get(ctx, cfgName, metav1.GetOptions{})
			if err != nil {
				return false, fmt.Errorf("get HAProxyCfg %s/%s: %w", ControllerNamespace, cfgName, err)
			}
			want := obj.Spec.Checksum
			if want == "" {
				return false, fmt.Errorf("HAProxyCfg.spec.checksum not populated yet")
			}
			if obj.Spec.Compressed {
				// Defensive: e2e configs are well under the default 1 MiB
				// compression threshold (~60 KiB observed), so this branch
				// should never fire. If it does, raise the threshold or
				// add a decompressor here — falling through would cause a
				// spurious "marker not in rendered config" failure.
				return false, fmt.Errorf("HAProxyCfg.spec.content is compressed; e2e wait does not decompress")
			}
			if !strings.Contains(obj.Spec.Content, marker) {
				return false, fmt.Errorf("marker %q not yet in HAProxyCfg.spec.content", marker)
			}
			deployed := obj.Status.DeployedToPods
			if len(deployed) == 0 {
				return false, fmt.Errorf("HAProxyCfg.status.deployedToPods empty (controller hasn't reported any pod yet)")
			}
			for _, p := range deployed {
				if p.Checksum != want {
					return false, fmt.Errorf("pod %s at checksum %q, spec is %q", p.PodName, p.Checksum, want)
				}
			}
			return true, nil
		}); err != nil {
		t.Fatalf("waitForControllerDeployed %q: %v", marker, err)
	}
}
