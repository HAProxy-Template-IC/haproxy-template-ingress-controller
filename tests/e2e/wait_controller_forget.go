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
	"strings"
	"sync"
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
// 1s, but we allow a bounded 15s budget so transient API-server slowness
// and the chart's 5s minDeploymentInterval during parallel teardown don't
// cause spurious cleanup-warning logs.
const controllerForgetTimeout = 15 * time.Second

// controllerDeployedTimeout caps the post-apply wait for the HAProxyCfg
// status to report every HAProxy pod at a render containing the marker.
// Convergence is bounded by the controller's own pacing: reconcile debounce
// (≤2s) + one deploy interval (minDeploymentInterval, 5s chart default) + the
// per-pod Sync/reload (~1-2s). Latest-wins coalescing means a freshly applied
// resource rides the NEXT deploy regardless of how many sibling tests churn
// concurrently, so ~7s is the realistic worst case. 12s is the 2x-headroom
// cap: generous enough to never flake on a healthy controller, tight enough
// that a genuine convergence regression fails the test loudly instead of
// hiding behind a 90s budget (a wait that legitimately needs >12s here would
// itself be the bug).
const controllerDeployedTimeout = 12 * time.Second

type controllerForgetCleanupKey struct {
	test      *testing.T
	namespace string
}

var controllerForgetCleanupKeys sync.Map

// registerControllerForgetNamespaceCleanup schedules one wait after every Ingress cleanup in the namespace.
func registerControllerForgetNamespaceCleanup(t *testing.T, client klient.Client, namespace string) {
	t.Helper()
	key := controllerForgetCleanupKey{test: t, namespace: namespace}
	if _, loaded := controllerForgetCleanupKeys.LoadOrStore(key, struct{}{}); loaded {
		return
	}

	t.Cleanup(func() {
		defer controllerForgetCleanupKeys.Delete(key)
		waitForControllerForgetNamespace(context.Background(), t, client, namespace)
	})
}

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

	// Read the rendered config straight from the HAProxyCfg CR (the same source
	// waitForControllerDeployed uses) rather than the apiserver service-proxy to
	// the controller's debug endpoint. The proxy path (ProxyGet -> controller
	// :8080, fetching the full rendered config every poll) strains under
	// parallel-test cleanup churn and returns the opaque
	// `an error on the server ("unknown")`; a direct CR GET is served from the
	// apiserver's watch cache and never proxies to a pod.
	hc, err := hapticclient.NewForConfig(client.RESTConfig())
	if err != nil {
		t.Logf("waitForControllerForgetNamespace: build haptic clientset: %v (skipping wait)", err)
		return
	}
	cfgName := HAProxyConfigName + "-haproxycfg"

	cfg := testutil.FastWaitConfig()
	cfg.Timeout = controllerForgetTimeout

	err = testutil.WaitForConditionWithDescription(ctx, cfg,
		"controller forgot namespace "+namespace,
		func(ctx context.Context) (bool, error) {
			obj, err := hc.HaproxyTemplateICV1alpha1().HAProxyCfgs(ControllerNamespace).Get(ctx, cfgName, metav1.GetOptions{})
			if err != nil {
				return false, err
			}
			if obj.Spec.Compressed {
				return false, fmt.Errorf("HAProxyCfg.spec.content is compressed; forget-namespace wait does not decompress")
			}
			return !strings.Contains(obj.Spec.Content, namespace), nil
		})
	if err != nil {
		t.Logf("waitForControllerForgetNamespace %q: %v (cleanup proceeding anyway)", namespace, err)
	}
}
