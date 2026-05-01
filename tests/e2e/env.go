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

	corev1 "k8s.io/api/core/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/util/flowcontrol"
	"sigs.k8s.io/e2e-framework/klient"
	"sigs.k8s.io/e2e-framework/klient/k8s/resources"

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

// WaitForE2EEnvironmentReady blocks until the controller is up and able
// to serve its debug endpoint. We deliberately stop short of waiting for
// HAProxy to be Ready or for the deployment pipeline to report
// "succeeded": the chart's HAProxy readiness probe returns 503 until a
// backend exists, and no backend exists until a test applies an Ingress.
// That's a per-test concern, handled by the httpclient's retry policy.
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
	}
	return testutil.WaitForConditionWithDescription(ctx, cfg, "controller debug endpoint serving",
		func(ctx context.Context) (bool, error) {
			if _, err := dc.getPipelineStatus(ctx); err != nil {
				return false, err
			}
			return true, nil
		})
}

// debugClient is the minimal API-proxy client the e2e suite needs.
// Mirrors tests/acceptance/DebugClient but trimmed to the methods the
// e2e suite uses, so we don't pull a separate package across build tags.
type debugClient struct {
	clientset   kubernetes.Interface
	namespace   string
	serviceName string
	port        string
}

func (dc *debugClient) getPipelineStatus(ctx context.Context) (*pipelineStatus, error) {
	body, err := dc.clientset.CoreV1().Services(dc.namespace).ProxyGet(
		"http", dc.serviceName, dc.port, DebugPathPipeline, nil,
	).DoRaw(ctx)
	if err != nil {
		return nil, err
	}
	var st pipelineStatus
	if err := json.Unmarshal(body, &st); err != nil {
		return nil, fmt.Errorf("decode pipeline status: %w (body=%s)", err, body)
	}
	return &st, nil
}

// getRenderedConfig returns the current rendered haproxy.cfg as the
// controller sees it. The /debug/vars/rendered response wraps the config in
// a {"config": "..."} envelope; we unwrap it to a plain string.
func (dc *debugClient) getRenderedConfig(ctx context.Context) (string, error) {
	body, err := dc.clientset.CoreV1().Services(dc.namespace).ProxyGet(
		"http", dc.serviceName, dc.port, DebugPathRendered, nil,
	).DoRaw(ctx)
	if err != nil {
		return "", err
	}
	var envelope struct {
		Config string `json:"config"`
	}
	if err := json.Unmarshal(body, &envelope); err != nil {
		return "", fmt.Errorf("decode rendered config: %w (body=%s)", err, body)
	}
	return envelope.Config, nil
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
