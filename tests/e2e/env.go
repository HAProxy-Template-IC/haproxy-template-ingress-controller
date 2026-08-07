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
	"encoding/json"
	"fmt"
	"os"
	"strconv"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/util/flowcontrol"
	"sigs.k8s.io/e2e-framework/klient"
	"sigs.k8s.io/e2e-framework/klient/k8s/resources"

	hapticclient "gitlab.com/haproxy-haptic/haptic/pkg/generated/clientset/versioned"
	"gitlab.com/haproxy-haptic/haptic/tests/testutil"
)

// lookupEnv is a thin wrapper around os.LookupEnv for use inside the
// build-tag-gated package. Returns the value and a boolean indicating
// whether the variable was set.
func lookupEnv(key string) (string, bool) {
	return os.LookupEnv(key)
}

// pipelineStatus is the subset of the controller's /debug/vars/pipeline
// response that the e2e suite cares about. Mirrors PipelineStatus from
// pkg/controller/debug/state.go but pulls in only the deployment phase.
type pipelineStatus struct {
	Deployment *deploymentStatus `json:"deployment"`
}

type deploymentStatus struct {
	Status             string `json:"status"`
	Timestamp          string `json:"timestamp"`
	EndpointsTotal     int    `json:"endpoints_total"`
	EndpointsSucceeded int    `json:"endpoints_succeeded"`
	EndpointsFailed    int    `json:"endpoints_failed"`
}

// WaitForE2EEnvironmentReady blocks until the controller reports HTTP
// 200 on /healthz. The /healthz endpoint aggregates every lifecycle
// component and the "initialized" gate added in
// pkg/controller/infrastructure.go, which only flips to healthy after:
//
//   - state.SetInitialized() (iteration setup completed all 8 stages),
//   - every registered component has left StatusPending / StatusStarting —
//     on the leader replica that means StartLeaderOnlyComponents finished
//     bringing the deployer, scheduler, coordinator, etc. up to
//     StatusRunning; on a follower they reach StatusStandby instead.
//
// Picking /healthz over "first deployment succeeded" deliberately decouples
// the wait from any reconciliation outcome: on a fresh cluster with no
// routing resources there is nothing to deploy and pipeline state would
// stay empty forever. /healthz tells us the controller is *ready to accept
// work*, which is precisely what the conformance suites need before they
// start applying Gateway / HTTPRoute fixtures.
//
// Called from TestMain after helm install + fixture deploy. The job here
// is just "the cluster is no longer in a setup-time inconsistent state."
func WaitForE2EEnvironmentReady(ctx context.Context, client klient.Client) error {
	cfg := testutil.SlowWaitConfig()
	cfg.Timeout = DefaultEnvironmentReadyTimeout

	if err := waitForLabelledPodReady(ctx, client, ControllerNamespace, LabelSelectorController, cfg); err != nil {
		return fmt.Errorf("controller pod not ready: %w", err)
	}

	// Verify the debug endpoint is serving — this catches early the
	// case where the controller pod is "Running" but its debug HTTP
	// server hasn't bound yet.
	cs, err := newClientsetForE2E(client.RESTConfig())
	if err != nil {
		return fmt.Errorf("build clientset: %w", err)
	}
	dc := &debugClient{
		clientset:   cs,
		namespace:   ControllerNamespace,
		serviceName: DebugServiceNameValue,
		port:        strconv.Itoa(DebugPort),
		loopback: testutil.NewLoopbackPodClient(
			client.RESTConfig(), cs, ControllerNamespace, LabelSelectorController, DebugPort,
		),
	}
	if err := testutil.WaitForConditionWithDescription(ctx, cfg,
		"controller /healthz returns 200 (all components ready, leader-only running)",
		func(ctx context.Context) (bool, error) {
			return dc.healthzReady(ctx)
		}); err != nil {
		return err
	}

	// /healthz reports the controller as ready before it has finished its
	// initial HAProxy-pod discovery + deploy cycle. Without this extra wait,
	// the first per-test waitForControllerDeployed call races against
	// discovery — on a cold cluster the controller takes ~50s after start
	// to discover both HAProxy replicas, while individual test budgets
	// (per the 15s rule in tests/e2e/CLAUDE.md) are far tighter than that.
	// Block here until the cluster's chart-default HAProxy replicas are
	// all in HAProxyCfg.status.deployedToPods at the current spec.checksum.
	// After this, per-test reactions are sub-second.
	if err := waitForInitialHAProxyDeployment(ctx, client, cfg); err != nil {
		return err
	}

	// The GatewayClass is created by the CONTROLLER at runtime (SSA via the
	// gateway library's k8sResources — it exists exactly when the
	// gatewayclasses CRD is served, surviving install-order races). Test
	// fixtures reference it by name the moment they create Gateways, so
	// block until the first render+apply cycle has produced it.
	return waitForGatewayClass(ctx, client, cfg)
}

// waitForGatewayClass blocks until the chart's GatewayClass exists. It is
// created by the controller's resource applier on the first render after
// the gatewayclasses CRD resolves, not by Helm.
func waitForGatewayClass(ctx context.Context, client klient.Client, cfg testutil.WaitConfig) error {
	dyn, err := dynamic.NewForConfig(client.RESTConfig())
	if err != nil {
		return fmt.Errorf("build dynamic client: %w", err)
	}
	gvr := schema.GroupVersionResource{Group: "gateway.networking.k8s.io", Version: "v1", Resource: "gatewayclasses"}
	return testutil.WaitForConditionWithDescription(ctx, cfg,
		"controller-created GatewayClass exists",
		func(ctx context.Context) (bool, error) {
			_, err := dyn.Resource(gvr).Get(ctx, gatewayClassName, metav1.GetOptions{})
			if err != nil {
				return false, err
			}
			return true, nil
		})
}

// waitForInitialHAProxyDeployment blocks until every chart-default HAProxy
// replica is reported in HAProxyCfg.status.deployedToPods at the current
// spec.checksum — i.e., the controller has finished its initial discovery
// + deploy cycle for all HAProxy pods. This is a one-time suite-startup
// wait; per-test fixtures rely on this having completed before they apply
// resources with the tighter 15s reaction budget.
func waitForInitialHAProxyDeployment(ctx context.Context, client klient.Client, cfg testutil.WaitConfig) error {
	expectedReplicas, err := discoverHAProxyReplicaCount(ctx, client)
	if err != nil {
		return fmt.Errorf("discover HAProxy replica count: %w", err)
	}

	hc, err := hapticclient.NewForConfig(client.RESTConfig())
	if err != nil {
		return fmt.Errorf("build haptic clientset: %w", err)
	}

	cfgName := HAProxyConfigName + "-haproxycfg"
	return testutil.WaitForConditionWithDescription(ctx, cfg,
		fmt.Sprintf("HAProxyCfg deployed to %d HAProxy replicas (initial sync)", expectedReplicas),
		func(ctx context.Context) (bool, error) {
			obj, err := hc.HaproxyTemplateICV1alpha1().HAProxyCfgs(ControllerNamespace).Get(ctx, cfgName, metav1.GetOptions{})
			if err != nil {
				return false, fmt.Errorf("get HAProxyCfg %s/%s: %w", ControllerNamespace, cfgName, err)
			}
			want := obj.Spec.Checksum
			if want == "" {
				return false, fmt.Errorf("HAProxyCfg.spec.checksum not populated yet")
			}
			deployed := obj.Status.DeployedToPods
			if len(deployed) < expectedReplicas {
				return false, fmt.Errorf("only %d/%d HAProxy pods reported deployed", len(deployed), expectedReplicas)
			}
			for _, p := range deployed {
				if p.Checksum != want {
					return false, fmt.Errorf("pod %s at checksum %q, spec is %q", p.PodName, p.Checksum, want)
				}
			}
			return true, nil
		})
}

// discoverHAProxyReplicaCount returns the spec.replicas of the chart's
// HAProxy Deployment. This is sourced live so it tracks any chart-values
// override (CI shards may pin a different replicaCount).
func discoverHAProxyReplicaCount(ctx context.Context, client klient.Client) (int, error) {
	dep := &appsv1.Deployment{}
	if err := client.Resources(ControllerNamespace).Get(ctx, HAProxyDeploymentName, ControllerNamespace, dep); err != nil {
		return 0, fmt.Errorf("get deployment %s/%s: %w", ControllerNamespace, HAProxyDeploymentName, err)
	}
	if dep.Spec.Replicas == nil {
		return 1, nil
	}
	return int(*dep.Spec.Replicas), nil
}

// debugClient is the minimal API-proxy client the e2e suite needs.
// Mirrors tests/acceptance/DebugClient but trimmed to the methods the
// e2e suite uses, so we don't pull a separate package across build tags.
type debugClient struct {
	clientset   kubernetes.Interface
	namespace   string
	serviceName string
	port        string
	// /debug/* is loopback-only, so it is reached by port-forward. The health
	// endpoints stay on the Service proxy, which the kubelet also uses.
	loopback *testutil.LoopbackPodClient
}

func (dc *debugClient) getPipelineStatus(ctx context.Context) (*pipelineStatus, error) {
	body, err := dc.loopback.Get(ctx, DebugPathPipeline)
	if err != nil {
		return nil, err
	}
	var st pipelineStatus
	if err := json.Unmarshal(body, &st); err != nil {
		return nil, fmt.Errorf("decode pipeline status: %w (body=%s)", err, body)
	}
	return &st, nil
}

// healthzReady polls /healthz and returns (true, nil) when the controller
// reports HTTP 200, i.e. every health-checked component (including the
// "initialized" gate) is reporting healthy. On 503 the body is parsed so
// the caller's wait loop surfaces which component is still unhealthy —
// invaluable when the e2e suite hangs in CI on a follower that never
// became leader or a leader-only component that failed to start.
func (dc *debugClient) healthzReady(ctx context.Context) (bool, error) {
	body, err := dc.clientset.CoreV1().Services(dc.namespace).ProxyGet(
		"http", dc.serviceName, dc.port, HealthzPath, nil,
	).DoRaw(ctx)
	if err == nil {
		return true, nil
	}
	// Surface the per-component error block to the wait-loop log so a
	// stuck "initialized" entry (leader not acquired, leader-only
	// component still pending) shows up directly in CI output rather
	// than hidden behind a generic 503.
	var parsed map[string]struct {
		Healthy bool   `json:"healthy"`
		Error   string `json:"error,omitempty"`
	}
	if jerr := json.Unmarshal(body, &parsed); jerr == nil {
		var unhealthy []string
		for name, comp := range parsed {
			if !comp.Healthy {
				if comp.Error != "" {
					unhealthy = append(unhealthy, name+": "+comp.Error)
				} else {
					unhealthy = append(unhealthy, name)
				}
			}
		}
		if len(unhealthy) > 0 {
			return false, fmt.Errorf("/healthz unhealthy: %v", unhealthy)
		}
	}
	return false, fmt.Errorf("/healthz not ready: %w", err)
}

// newClientsetForE2E builds a clientset with rate-limiting disabled. The
// e2e suite's parallel tests would otherwise saturate the default rate
// limiter against the API server.
func newClientsetForE2E(cfg *rest.Config) (kubernetes.Interface, error) {
	c := rest.CopyConfig(cfg)
	c.RateLimiter = flowcontrol.NewFakeAlwaysRateLimiter()
	return kubernetes.NewForConfig(c)
}

// waitForLabelledPodReady polls until at least one pod matching the label
// selector has condition Ready=True.
func waitForLabelledPodReady(ctx context.Context, client klient.Client, namespace, labelSelector string, cfg testutil.WaitConfig) error {
	return testutil.WaitForConditionWithDescription(ctx, cfg, "pod ready: "+labelSelector,
		func(ctx context.Context) (bool, error) {
			var pods corev1.PodList
			if err := client.Resources(namespace).List(ctx, &pods, resources.WithLabelSelector(labelSelector)); err != nil {
				return false, err
			}
			if len(pods.Items) == 0 {
				return false, fmt.Errorf("no pods match %q", labelSelector)
			}
			for i := range pods.Items {
				for _, c := range pods.Items[i].Status.Conditions {
					if c.Type == corev1.PodReady && c.Status == corev1.ConditionTrue {
						return true, nil
					}
				}
			}
			return false, fmt.Errorf("pod present but not Ready")
		})
}
