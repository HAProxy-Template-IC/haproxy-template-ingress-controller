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
	"fmt"
	"net/http"
	"os/exec"
	"sort"
	"strings"
	"testing"
	"time"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/intstr"
	"sigs.k8s.io/e2e-framework/klient"
	"sigs.k8s.io/e2e-framework/klient/k8s/resources"
	"sigs.k8s.io/e2e-framework/pkg/envconf"
	"sigs.k8s.io/e2e-framework/pkg/features"

	"gitlab.com/haproxy-haptic/haptic/tests/e2e/httpclient"
	"gitlab.com/haproxy-haptic/haptic/tests/testutil"
)

const varnishCacheStatefulSetName = HelmReleaseName + "-varnish-cache"

// TestHapticVarnishCache exercises the shared Varnish cache tier (70-caching.yaml)
// end-to-end: the chart-deployed Varnish StatefulSet, the loopback origin, and
// per-route caching. Runs only in the cache shard (HAPTIC_E2E_PROFILE=cache),
// which installs with cache.varnish.enabled=true.
//
// The VCL tags every response with X-Cache: HIT|MISS|STALE (vcl_deliver); this
// fixture sets no staleness window, so STALE cannot occur here. A HIT proves
// the full path: the tier is deployed and reachable, the first request's MISS
// fetched from the app through the HAProxy loopback, and the object was then
// served from Varnish's cache.
func TestHapticVarnishCache(t *testing.T) {
	RequireCacheProfile(t)
	t.Parallel()
	RunSimpleIngressTest(t, SimpleIngressTest{
		Description: "Ingress: HAPTIC shared Varnish cache tier",
		Host:        "ingress-haptic-cache.localdev.me",
		Annotations: map[string]string{
			"haproxy-haptic.org/cache-enable":        "true",
			"haproxy-haptic.org/cache-ttl":           "60",
			"haproxy-haptic.org/cache-exclude-paths": "/nocache",
		},
		Assess: []SimpleIngressAssertion{
			{
				Name: "cacheable GET is served from Varnish (X-Cache: HIT after warm-up)",
				Check: func(t *testing.T, host string) {
					// Polling absorbs Varnish startup and the MISS→HIT transition:
					// the first request(s) MISS (fetched from the app via the
					// HAProxy loopback), then the object is served from the cache.
					httpclient.New(t).GET(host, "/cache-probe").ExpectMatching(t,
						"response served from the Varnish cache (X-Cache: HIT)",
						func(resp *httpclient.Response) bool {
							return resp.Status == 200 && resp.Header.Get("X-Cache") == "HIT"
						})
				},
			},
			{
				Name: "excluded path bypasses the cache (no X-Cache header)",
				Check: func(t *testing.T, host string) {
					// cache-exclude-paths routes /nocache straight to the app, so
					// Varnish never sees it and adds no X-Cache header.
					httpclient.New(t).GET(host, "/nocache").ExpectMatching(t,
						"excluded path reaches the app directly, no X-Cache header",
						func(resp *httpclient.Response) bool {
							return resp.Status == 200 && resp.Echo != nil && resp.Header.Get("X-Cache") == ""
						})
				},
			},
		},
	})
	assertVarnishRejectsUnauthorizedPod(t)
	assertNoRateLimitStoreWithoutSharedLimiter(t)
}

func TestHapticVarnishGeneratedVCLCompiles(t *testing.T) {
	RequireCacheProfile(t)

	feature := features.New("Varnish: generated VCL compiles with the deployed image").
		Assess("every running Varnish pod compiles its mounted VCL", func(ctx context.Context, t *testing.T, cfg *envconf.Config) context.Context {
			client, err := cfg.NewClient()
			if err != nil {
				t.Fatalf("new client: %v", err)
			}
			pods, err := varnishPods(ctx, client)
			if err != nil {
				t.Fatalf("find Varnish pods: %v", err)
			}
			for _, pod := range pods {
				if err := compileVarnishVCL(ctx, pod); err != nil {
					t.Fatal(err)
				}
			}
			return ctx
		})

	testEnv.Test(t, feature.Feature())
}

func TestHapticVarnishCacheFaultRecovery(t *testing.T) {
	RequireCacheProfile(t)

	runID := time.Now().UnixNano()
	host := fmt.Sprintf("cache-fault-%d.localdev.me", runID)
	slowHost := fmt.Sprintf("cache-slow-fault-%d.localdev.me", runID)
	staleHost := fmt.Sprintf("cache-stale-fault-%d.localdev.me", runID)
	privateHost := fmt.Sprintf("cache-private-fault-%d.localdev.me", runID)
	faultPath := fmt.Sprintf("/cache-fault-%d", runID)
	recoveryPath := fmt.Sprintf("/cache-recovery-%d", runID)

	feature := features.New("Ingress: Varnish outage fails open and recovers").
		Setup(func(ctx context.Context, t *testing.T, cfg *envconf.Config) context.Context {
			client, err := cfg.NewClient()
			if err != nil {
				t.Fatalf("new client: %v", err)
			}
			ns := NamespaceForTest(ctx, t, client)
			DumpLogsOnFailure(t, ns)
			backend := NewEchoServerBackend(ctx, t, client, ns)
			slowBackend := newSlowCacheOrigin(ctx, t, client, ns)
			NewIngress(ctx, t, client, ns, IngressSpec{
				Name:           "echo",
				Host:           host,
				BackendService: backend.Service,
				BackendPort:    backend.Port,
				Annotations: map[string]string{
					"haproxy-haptic.org/cache-enable":        "true",
					"haproxy-haptic.org/cache-ttl":           "60",
					"haproxy-haptic.org/cache-exclude-paths": "/nocache",
				},
			})
			NewIngress(ctx, t, client, ns, IngressSpec{
				Name:           "slow-origin",
				Host:           slowHost,
				BackendService: slowBackend.Service,
				BackendPort:    slowBackend.Port,
				Annotations: map[string]string{
					"haproxy-haptic.org/cache-enable":         "true",
					"haproxy-haptic.org/cache-ttl":            "60",
					"haproxy-haptic.org/cache-stale-if-error": "60",
				},
			})
			NewIngress(ctx, t, client, ns, IngressSpec{
				Name:           "echo-private",
				Host:           privateHost,
				BackendService: backend.Service,
				BackendPort:    backend.Port,
				Annotations: map[string]string{
					"haproxy-haptic.org/cache-enable":        "true",
					"haproxy-haptic.org/cache-ttl":           "60",
					"haproxy-haptic.org/cache-key":           "src,header:X-Api-Version",
					"haproxy-haptic.org/cache-exclude-paths": "/nocache",
					"haproxy-haptic.org/response-set-header": `Cache-Control max-age=60, public, private="Set-Cookie", public-key=keep`,
				},
			})
			NewIngress(ctx, t, client, ns, IngressSpec{
				Name:           "stale-origin",
				Host:           staleHost,
				BackendService: slowBackend.Service,
				BackendPort:    slowBackend.Port,
				Annotations: map[string]string{
					"haproxy-haptic.org/cache-enable":         "true",
					"haproxy-haptic.org/cache-ttl":            "1",
					"haproxy-haptic.org/cache-stale-if-error": "60",
				},
			})
			return ctx
		}).
		Assess("HAProxy stays available, bypasses a failed cache, and resumes caching", func(ctx context.Context, t *testing.T, cfg *envconf.Config) context.Context {
			client, err := cfg.NewClient()
			if err != nil {
				t.Fatalf("new client: %v", err)
			}
			hc := httpclient.New(t)
			hc.GET(host, faultPath).ExpectMatching(t,
				"cache is warm before fault injection",
				func(resp *httpclient.Response) bool {
					return resp.Status == http.StatusOK && resp.Header.Get("X-Cache") == "HIT"
				})

			originalReplicas, err := varnishReplicaCount(ctx, client)
			if err != nil {
				t.Fatalf("read Varnish replica count: %v", err)
			}
			if originalReplicas < 1 {
				t.Fatalf("Varnish starts with %d replicas, want at least 1", originalReplicas)
			}

			stoppedPods, err := stopVarnishProcesses(ctx, client)
			if err != nil {
				t.Fatalf("stop Varnish processes: %v", err)
			}
			processesStopped := true
			t.Cleanup(func() {
				if !processesStopped {
					return
				}
				cleanupCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
				defer cancel()
				if err := continueVarnishProcesses(cleanupCtx, stoppedPods); err != nil {
					t.Errorf("continue Varnish processes: %v", err)
				}
			})

			hangStarted := time.Now()
			resp, err := hc.GET(host, faultPath+"-hung").Do(ctx)
			if err != nil {
				t.Fatalf("request through stopped Varnish processes: %v", err)
			}
			if elapsed := time.Since(hangStarted); elapsed > 4*time.Second {
				t.Fatalf("stopped Varnish delayed direct fallback for %s, want at most 4s", elapsed)
			}
			if resp.Status != http.StatusOK || resp.Echo == nil || resp.Header.Get("X-Cache") != "" {
				t.Fatalf("stopped Varnish fallback: status=%d echo=%t X-Cache=%q", resp.Status, resp.Echo != nil, resp.Header.Get("X-Cache"))
			}
			assertEchoCacheHeadersStripped(t, resp)
			findAccessLogRecordWhere(ctx, t, hangStarted,
				fmt.Sprintf("a direct-origin retry for stopped Varnish on host %s", host),
				func(rec map[string]any) bool {
					return rec["host"] == host && rec["path"] == faultPath+"-hung" &&
						rec["server"] == "ORIGIN_FALLBACK" && rec["cache_degraded"] == "1"
				})
			if err := continueVarnishProcesses(ctx, stoppedPods); err != nil {
				t.Fatalf("continue Varnish processes: %v", err)
			}
			processesStopped = false
			hc.GET(host, faultPath+"-resumed").ExpectMatching(t,
				"cache resumes after a hung Varnish process recovers",
				func(resp *httpclient.Response) bool {
					return resp.Status == http.StatusOK && resp.Header.Get("X-Cache") == "HIT"
				})

			slowPath := faultPath + "-slow-origin"
			slowStarted := time.Now()
			resp, err = hc.GET(slowHost, slowPath).Do(ctx)
			if err != nil {
				t.Fatalf("request through a cache miss that exceeds the cache response timeout: %v", err)
			}
			if elapsed := time.Since(slowStarted); elapsed > 3*time.Second {
				t.Fatalf("slow cache miss delayed direct fallback for %s, want at most 3s", elapsed)
			}
			if resp.Status != http.StatusOK || string(resp.Body) != "slow-cache-origin" {
				t.Fatalf("slow cache miss fallback: status=%d body=%q", resp.Status, resp.Body)
			}
			findAccessLogRecordWhere(ctx, t, slowStarted,
				fmt.Sprintf("a direct-origin retry for slow cache miss on host %s", slowHost),
				func(rec map[string]any) bool {
					return rec["host"] == slowHost && rec["path"] == slowPath &&
						rec["server"] == "ORIGIN_FALLBACK" && rec["cache_degraded"] == "1"
				})

			coldStalePath := faultPath + "-cold-stale-origin-failure"
			coldStaleStarted := time.Now()
			resp, err = hc.GET(slowHost, coldStalePath).Do(ctx)
			if err != nil {
				t.Fatalf("cold stale-if-error request through a failed Varnish origin fetch: %v", err)
			}
			if elapsed := time.Since(coldStaleStarted); elapsed > 3*time.Second {
				t.Fatalf("cold stale-if-error fallback took %s, want at most 3s", elapsed)
			}
			if resp.Status != http.StatusOK || string(resp.Body) != "slow-cache-origin" || resp.Header.Get("X-Cache") != "" {
				t.Fatalf("cold stale-if-error fallback: status=%d body=%q X-Cache=%q", resp.Status, resp.Body, resp.Header.Get("X-Cache"))
			}
			findAccessLogRecordWhere(ctx, t, coldStaleStarted,
				fmt.Sprintf("a direct-origin retry after a cold stale-if-error fetch failure on host %s", slowHost),
				func(rec map[string]any) bool {
					return rec["host"] == slowHost && rec["path"] == coldStalePath &&
						rec["server"] == "ORIGIN_FALLBACK" && rec["cache_degraded"] == "1"
				})

			stalePath := faultPath + "-stale-if-error-recovery"
			hc.GET(staleHost, stalePath).ExpectMatching(t,
				"an expired object is served stale only after its synchronous refresh fails",
				func(resp *httpclient.Response) bool {
					return resp.Status == http.StatusOK && string(resp.Body) == "slow-cache-origin" &&
						resp.Header.Get("X-Cache") == "STALE"
				})

			originErrorPath := faultPath + "-origin-status-500"
			originErrorStarted := time.Now()
			resp, err = hc.GET(slowHost, originErrorPath).Do(ctx)
			if err != nil {
				t.Fatalf("request origin status 500 through stale-if-error cache: %v", err)
			}
			if resp.Status != http.StatusInternalServerError || string(resp.Body) != "origin-500" || resp.Header.Get("X-Cache") != "MISS" {
				t.Fatalf("cold origin error changed by stale-if-error: status=%d body=%q X-Cache=%q", resp.Status, resp.Body, resp.Header.Get("X-Cache"))
			}
			findAccessLogRecordWhere(ctx, t, originErrorStarted,
				fmt.Sprintf("an unretried cold origin error on host %s", slowHost),
				func(rec map[string]any) bool {
					degraded, found := rec["cache_degraded"]
					return rec["host"] == slowHost && rec["path"] == originErrorPath &&
						rec["server"] == "CACHE_DISPATCH" && (!found || degraded == "")
				})

			status425Path := faultPath + "-origin-status-425"
			resp, err = hc.GET(slowHost, status425Path).Do(ctx)
			if err != nil {
				t.Fatalf("request origin status 425 through cache: %v", err)
			}
			if resp.Status != http.StatusTooEarly || string(resp.Body) != "origin-425" || resp.Header.Get("X-Cache") != "MISS" {
				t.Fatalf("origin status 425 changed by cache failover: status=%d body=%q X-Cache=%q", resp.Status, resp.Body, resp.Header.Get("X-Cache"))
			}
			if resp.Header.Get("X-Haptic-Cache-Origin-425") != "" {
				t.Fatal("cache failover marker leaked to the client")
			}

			forgedTransportPath := faultPath + "-origin-forged-transport-failure"
			forgedTransportStarted := time.Now()
			resp, err = hc.GET(slowHost, forgedTransportPath).Do(ctx)
			if err != nil {
				t.Fatalf("request origin response with a forged transport-failure marker through cache: %v", err)
			}
			if resp.Status != http.StatusOK || string(resp.Body) != "origin-forged-transport-failure" || resp.Header.Get("X-Cache") != "MISS" {
				t.Fatalf("origin transport-failure marker triggered cache failover: status=%d body=%q X-Cache=%q", resp.Status, resp.Body, resp.Header.Get("X-Cache"))
			}
			if resp.Header.Get("X-Haptic-Cache-Origin-Transport-Failure") != "" {
				t.Fatal("origin transport-failure marker leaked to the client")
			}
			findAccessLogRecordWhere(ctx, t, forgedTransportStarted,
				fmt.Sprintf("an unretried forged transport-failure marker on host %s", slowHost),
				func(rec map[string]any) bool {
					degraded, found := rec["cache_degraded"]
					return rec["host"] == slowHost && rec["path"] == forgedTransportPath &&
						rec["server"] == "CACHE_DISPATCH" && (!found || degraded == "")
				})

			restoreRequired := true
			t.Cleanup(func() {
				if !restoreRequired {
					return
				}
				cleanupCtx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
				defer cancel()
				if err := setVarnishReplicas(cleanupCtx, client, originalReplicas); err != nil {
					t.Errorf("restore Varnish replicas: %v", err)
					return
				}
				if err := waitForVarnishReplicasReady(cleanupCtx, client, originalReplicas); err != nil {
					t.Errorf("wait for restored Varnish replicas: %v", err)
				}
			})

			beforeHAProxy, err := readyHAProxyRuntimeStates(ctx, client)
			if err != nil {
				t.Fatalf("read HAProxy runtime state before Varnish fault: %v", err)
			}
			if len(beforeHAProxy) < 2 {
				t.Fatalf("Varnish fault test needs both HAProxy pods, found %d", len(beforeHAProxy))
			}
			stopAvailabilityMonitor, err := startCacheEligibleAvailabilityMonitors(
				ctx, beforeHAProxy, host, faultPath+"-availability")
			if err != nil {
				t.Fatalf("start per-pod HAProxy availability monitors: %v", err)
			}
			monitorStopped := false
			defer func() {
				if !monitorStopped {
					_ = stopAvailabilityMonitor()
				}
			}()

			faultStarted := time.Now()
			if err := setVarnishReplicas(ctx, client, 0); err != nil {
				t.Fatalf("scale Varnish to zero: %v", err)
			}
			if err := waitForVarnishFailOpen(ctx, client, hc, host, faultPath); err != nil {
				t.Fatalf("wait for Varnish fail-open: %v", err)
			}
			findAccessLogRecordWhere(ctx, t, faultStarted,
				fmt.Sprintf("a cache-degraded fallback for host %s", host),
				func(rec map[string]any) bool {
					return rec["host"] == host && rec["path"] == faultPath && rec["cache_degraded"] == "1"
				})
			assertCacheHeadersStrippedOnFallback(t, hc, host, faultPath+"-headers")
			assertPrivateDownstreamCacheContract(t, hc, privateHost, faultPath+"-private", http.MethodGet)
			assertPrivateDownstreamCacheContract(t, hc, privateHost, "/nocache-private", http.MethodGet)
			assertPrivateDownstreamCacheContract(t, hc, privateHost, faultPath+"-private-post", http.MethodPost)
			excludedPath := fmt.Sprintf("/nocache-%d", runID)
			hc.GET(host, excludedPath).ExpectMatching(t,
				"excluded path remains a non-degraded direct request",
				func(resp *httpclient.Response) bool {
					return resp.Status == http.StatusOK && resp.Echo != nil && resp.Header.Get("X-Cache") == ""
				})
			findAccessLogRecordWhere(ctx, t, faultStarted,
				fmt.Sprintf("a non-degraded cache-excluded request for host %s", host),
				func(rec map[string]any) bool {
					degraded, found := rec["cache_degraded"]
					return rec["host"] == host && rec["path"] == excludedPath && (!found || degraded == "")
				})

			if err := setVarnishReplicas(ctx, client, originalReplicas); err != nil {
				t.Fatalf("restore Varnish replicas: %v", err)
			}
			if err := waitForVarnishReplicasReady(ctx, client, originalReplicas); err != nil {
				t.Fatalf("wait for Varnish recovery: %v", err)
			}
			hc.GET(host, recoveryPath).ExpectMatching(t,
				"cache resumes serving hits after Varnish recovery",
				func(resp *httpclient.Response) bool {
					return resp.Status == http.StatusOK && resp.Header.Get("X-Cache") == "HIT"
				})

			restoreRequired = false
			monitorErr := stopAvailabilityMonitor()
			monitorStopped = true
			if monitorErr != nil {
				t.Fatalf("HAProxy became unavailable during the Varnish fault: %v", monitorErr)
			}
			afterHAProxy, err := readyHAProxyRuntimeStates(ctx, client)
			if err != nil {
				t.Fatalf("read HAProxy runtime state after Varnish fault: %v", err)
			}
			if err := compareHAProxyRuntimeStates(beforeHAProxy, afterHAProxy); err != nil {
				t.Fatalf("HAProxy runtime changed during the Varnish fault: %v", err)
			}
			return ctx
		}).
		Feature()

	testEnv.Test(t, feature)
}

func newSlowCacheOrigin(ctx context.Context, t *testing.T, client klient.Client, namespace string) BackendRef {
	t.Helper()
	const name = "slow-cache-origin"
	labels := map[string]string{"app": name}
	script := `const http = require("http");
const body = "slow-cache-origin";
const varnishFetches = new Map();
http.createServer((req, res) => {
  const send = () => {
    res.writeHead(200, {"Content-Type": "text/plain", "Content-Length": Buffer.byteLength(body)});
    res.end(body);
  };
  if (req.url === "/health") {
    send();
  } else if (req.url.includes("origin-status-425")) {
    res.writeHead(425, {"Content-Type": "text/plain", "Content-Length": 10});
    res.end("origin-425");
  } else if (req.url.includes("origin-status-500")) {
    res.writeHead(500, {"Content-Type": "text/plain", "Content-Length": 10});
    res.end("origin-500");
  } else if (req.url.includes("origin-forged-transport-failure")) {
    const forgedBody = req.headers["x-varnish"] ? "origin-forged-transport-failure" : "unexpected-direct-fallback";
    res.writeHead(200, {
      "Content-Type": "text/plain",
      "Content-Length": Buffer.byteLength(forgedBody),
      "X-Haptic-Cache-Origin-Transport-Failure": "1"
    });
    res.end(forgedBody);
  } else if (req.url.includes("cold-stale-origin-failure")) {
    if (req.headers["x-varnish"]) {
      req.socket.destroy();
    } else {
      send();
    }
  } else if (req.url.includes("stale-if-error-recovery") && req.headers["x-varnish"]) {
    const count = (varnishFetches.get(req.url) || 0) + 1;
    varnishFetches.set(req.url, count);
    if (count > 1) {
      req.socket.destroy();
    } else {
      send();
    }
  } else {
    setTimeout(send, 750);
  }
}).listen(80);`
	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: namespace, Labels: labels},
		Spec: corev1.PodSpec{Containers: []corev1.Container{{
			Name:            "server",
			Image:           echoServerImage,
			ImagePullPolicy: corev1.PullIfNotPresent,
			Command:         []string{"node", "-e", script},
			Ports: []corev1.ContainerPort{{
				Name: "http", ContainerPort: 80, Protocol: corev1.ProtocolTCP,
			}},
			ReadinessProbe: &corev1.Probe{
				ProbeHandler: corev1.ProbeHandler{HTTPGet: &corev1.HTTPGetAction{
					Path: "/health", Port: intstr.FromString("http"),
				}},
				PeriodSeconds: 1, SuccessThreshold: 1, FailureThreshold: 1, TimeoutSeconds: 1,
			},
		}}},
	}
	if err := client.Resources(namespace).Create(ctx, pod); err != nil {
		t.Fatalf("create slow cache origin Pod: %v", err)
	}
	service := &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: namespace, Labels: labels},
		Spec: corev1.ServiceSpec{
			Selector: labels,
			Ports: []corev1.ServicePort{{
				Name: "http", Port: 80, TargetPort: intstr.FromString("http"), Protocol: corev1.ProtocolTCP,
			}},
		},
	}
	if err := client.Resources(namespace).Create(ctx, service); err != nil {
		t.Fatalf("create slow cache origin Service: %v", err)
	}
	if err := waitForServiceEndpointReady(ctx, client, namespace, name); err != nil {
		t.Fatalf("slow cache origin endpoint not ready: %v", err)
	}
	return BackendRef{Service: name, Port: 80}
}

func varnishReplicaCount(ctx context.Context, client klient.Client) (int32, error) {
	sts := &appsv1.StatefulSet{}
	if err := client.Resources(ControllerNamespace).Get(
		ctx, varnishCacheStatefulSetName, ControllerNamespace, sts,
	); err != nil {
		return 0, fmt.Errorf("get StatefulSet %s/%s: %w", ControllerNamespace, varnishCacheStatefulSetName, err)
	}
	if sts.Spec.Replicas == nil {
		return 0, fmt.Errorf("StatefulSet %s/%s has no fixed replica count", ControllerNamespace, varnishCacheStatefulSetName)
	}
	return *sts.Spec.Replicas, nil
}

func setVarnishReplicas(ctx context.Context, client klient.Client, replicas int32) error {
	waitCfg := testutil.FastWaitConfig()
	waitCfg.Timeout = 30 * time.Second
	return testutil.WaitForConditionWithDescription(ctx, waitCfg,
		fmt.Sprintf("Varnish StatefulSet spec.replicas=%d", replicas),
		func(ctx context.Context) (bool, error) {
			sts := &appsv1.StatefulSet{}
			if err := client.Resources(ControllerNamespace).Get(
				ctx, varnishCacheStatefulSetName, ControllerNamespace, sts,
			); err != nil {
				return false, err
			}
			if sts.Spec.Replicas != nil && *sts.Spec.Replicas == replicas {
				return true, nil
			}
			desired := replicas
			sts.Spec.Replicas = &desired
			if err := client.Resources(ControllerNamespace).Update(ctx, sts); err != nil {
				return false, err
			}
			return true, nil
		})
}

func waitForVarnishReplicasReady(ctx context.Context, client klient.Client, replicas int32) error {
	waitCfg := testutil.FastWaitConfig()
	waitCfg.Timeout = DefaultPerTestSetupTimeout
	return testutil.WaitForConditionWithDescription(ctx, waitCfg,
		fmt.Sprintf("%d Varnish replicas ready", replicas),
		func(ctx context.Context) (bool, error) {
			sts := &appsv1.StatefulSet{}
			if err := client.Resources(ControllerNamespace).Get(
				ctx, varnishCacheStatefulSetName, ControllerNamespace, sts,
			); err != nil {
				return false, err
			}
			desired := int32(-1)
			if sts.Spec.Replicas != nil {
				desired = *sts.Spec.Replicas
			}
			if desired != replicas ||
				sts.Status.ObservedGeneration < sts.Generation ||
				sts.Status.Replicas != replicas ||
				sts.Status.ReadyReplicas != replicas ||
				sts.Status.CurrentReplicas != replicas {
				return false, fmt.Errorf(
					"generation=%d/%d spec=%d replicas=%d ready=%d current=%d",
					sts.Status.ObservedGeneration, sts.Generation, desired,
					sts.Status.Replicas, sts.Status.ReadyReplicas, sts.Status.CurrentReplicas,
				)
			}
			return true, nil
		})
}

func waitForVarnishFailOpen(
	ctx context.Context,
	client klient.Client,
	hc *httpclient.Client,
	host string,
	path string,
) error {
	waitCfg := testutil.WaitConfig{
		InitialInterval: 100 * time.Millisecond,
		MaxInterval:     time.Second,
		Timeout:         time.Minute,
		Multiplier:      1.5,
	}
	return testutil.WaitForConditionWithDescription(ctx, waitCfg,
		"Varnish scale-down and direct-origin cache fallback",
		func(ctx context.Context) (bool, error) {
			sts := &appsv1.StatefulSet{}
			if err := client.Resources(ControllerNamespace).Get(
				ctx, varnishCacheStatefulSetName, ControllerNamespace, sts,
			); err != nil {
				return false, err
			}
			desired := int32(-1)
			if sts.Spec.Replicas != nil {
				desired = *sts.Spec.Replicas
			}
			if desired != 0 ||
				sts.Status.ObservedGeneration < sts.Generation ||
				sts.Status.Replicas != 0 || sts.Status.ReadyReplicas != 0 {
				return false, fmt.Errorf(
					"Varnish still converging to zero: generation=%d/%d spec=%d replicas=%d ready=%d",
					sts.Status.ObservedGeneration, sts.Generation, desired,
					sts.Status.Replicas, sts.Status.ReadyReplicas,
				)
			}

			resp, err := hc.GET(host, path).Do(ctx)
			if err != nil {
				return false, err
			}
			if resp.Status == http.StatusOK && resp.Echo != nil && resp.Header.Get("X-Cache") == "" {
				return true, nil
			}
			return false, fmt.Errorf("status=%d echo=%t X-Cache=%q", resp.Status, resp.Echo != nil, resp.Header.Get("X-Cache"))
		})
}

type haproxyRuntimeState struct {
	podUID       string
	containerID  string
	restartCount int32
}

func readyHAProxyRuntimeStates(ctx context.Context, client klient.Client) (map[string]haproxyRuntimeState, error) {
	var pods corev1.PodList
	if err := client.Resources(ControllerNamespace).List(
		ctx, &pods, resources.WithLabelSelector(LabelSelectorHAProxy),
	); err != nil {
		return nil, fmt.Errorf("list HAProxy pods: %w", err)
	}

	states := make(map[string]haproxyRuntimeState, len(pods.Items))
	for i := range pods.Items {
		pod := &pods.Items[i]
		if pod.DeletionTimestamp != nil || !podReady(*pod) {
			continue
		}
		for _, status := range pod.Status.ContainerStatuses {
			if status.Name != "haproxy" {
				continue
			}
			if status.ContainerID == "" || status.State.Running == nil {
				return nil, fmt.Errorf("HAProxy pod %s has no running HAProxy container", pod.Name)
			}
			states[pod.Name] = haproxyRuntimeState{
				podUID:       string(pod.UID),
				containerID:  status.ContainerID,
				restartCount: status.RestartCount,
			}
			break
		}
		if _, found := states[pod.Name]; !found {
			return nil, fmt.Errorf("HAProxy pod %s has no HAProxy container status", pod.Name)
		}
	}
	if len(states) == 0 {
		return nil, fmt.Errorf("no Ready HAProxy pods")
	}
	return states, nil
}

func compareHAProxyRuntimeStates(before, after map[string]haproxyRuntimeState) error {
	if len(after) != len(before) {
		return fmt.Errorf("Ready pod count changed from %d to %d", len(before), len(after))
	}
	for pod, want := range before {
		got, found := after[pod]
		if !found {
			return fmt.Errorf("pod %s was replaced or became NotReady", pod)
		}
		if got.podUID != want.podUID || got.containerID != want.containerID {
			return fmt.Errorf("pod %s or its HAProxy container was replaced", pod)
		}
		if got.restartCount != want.restartCount {
			return fmt.Errorf("pod %s HAProxy restart count changed from %d to %d", pod, want.restartCount, got.restartCount)
		}
	}
	return nil
}

func startCacheEligibleAvailabilityMonitors(
	ctx context.Context,
	pods map[string]haproxyRuntimeState,
	host string,
	path string,
) (func() error, error) {
	names := make([]string, 0, len(pods))
	for pod := range pods {
		names = append(names, pod)
	}
	sort.Strings(names)

	stops := make([]func() error, 0, len(names))
	for _, pod := range names {
		stop, err := startSelectedHAProxyAvailabilityMonitor(ctx, pod, host, path+"-"+pod)
		if err != nil {
			for _, stopStarted := range stops {
				_ = stopStarted()
			}
			return nil, fmt.Errorf("pod %s: %w", pod, err)
		}
		stops = append(stops, stop)
	}

	return func() error {
		var failures []string
		for i, stop := range stops {
			if err := stop(); err != nil {
				failures = append(failures, fmt.Sprintf("%s: %v", names[i], err))
			}
		}
		if len(failures) > 0 {
			return fmt.Errorf("%s", strings.Join(failures, "; "))
		}
		return nil
	}, nil
}

func stopVarnishProcesses(ctx context.Context, client klient.Client) ([]string, error) {
	pods, err := varnishPods(ctx, client)
	if err != nil {
		return nil, err
	}
	stopped := make([]string, 0, len(pods))
	for _, pod := range pods {
		if err := signalVarnishProcess(ctx, pod, "STOP"); err != nil {
			_ = continueVarnishProcesses(ctx, stopped)
			return nil, err
		}
		stopped = append(stopped, pod)
	}
	return stopped, nil
}

func continueVarnishProcesses(ctx context.Context, pods []string) error {
	var errs []string
	for _, pod := range pods {
		if err := signalVarnishProcess(ctx, pod, "CONT"); err != nil {
			errs = append(errs, err.Error())
		}
	}
	if len(errs) > 0 {
		return fmt.Errorf("continue Varnish in %s", strings.Join(errs, "; "))
	}
	return nil
}

func varnishPods(ctx context.Context, client klient.Client) ([]string, error) {
	var pods corev1.PodList
	if err := client.Resources(ControllerNamespace).List(ctx, &pods,
		resources.WithLabelSelector("app.kubernetes.io/name=haptic-varnish-cache")); err != nil {
		return nil, fmt.Errorf("list Varnish pods: %w", err)
	}
	names := make([]string, 0, len(pods.Items))
	for _, pod := range pods.Items {
		if pod.Status.Phase == corev1.PodRunning {
			names = append(names, pod.Name)
		}
	}
	if len(names) == 0 {
		return nil, fmt.Errorf("no running Varnish pods")
	}
	return names, nil
}

func compileVarnishVCL(ctx context.Context, pod string) error {
	compileCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()

	const script = `compile_log=$(mktemp)
trap 'rm -f "$compile_log"' EXIT
if varnishd -C -f /etc/varnish/default.vcl >"$compile_log" 2>&1; then
  exit 0
fi
cat "$compile_log" >&2
exit 1`
	out, err := exec.CommandContext(compileCtx, "kubectl",
		"--kubeconfig", kubeconfigPath, "-n", ControllerNamespace,
		"exec", pod, "-c", "varnish", "--", "/bin/sh", "-c", script,
	).CombinedOutput()
	if err != nil {
		return fmt.Errorf("compile generated VCL in Varnish pod %s: %w: %s", pod, err, strings.TrimSpace(string(out)))
	}
	return nil
}

func signalVarnishProcess(ctx context.Context, pod, signal string) error {
	script := `count=0
for pid in $(cat /proc/1/task/1/children); do
  if [ "$pid" != "$$" ]; then
    kill -` + signal + ` "$pid"
    count=$((count + 1))
  fi
done
[ "$count" -gt 0 ]`
	out, err := exec.CommandContext(ctx, "kubectl",
		"--kubeconfig", kubeconfigPath, "-n", ControllerNamespace,
		"exec", pod, "-c", "varnish", "--", "/bin/sh", "-c", script,
	).CombinedOutput()
	if err != nil {
		return fmt.Errorf("signal Varnish pod %s with SIG%s: %w: %s", pod, signal, err, strings.TrimSpace(string(out)))
	}
	return nil
}

func assertPrivateDownstreamCacheContract(t *testing.T, hc *httpclient.Client, host, path, method string) {
	t.Helper()
	hc.GET(host, path).
		WithMethod(method).
		WithHeader("X-Api-Version", "v2").
		ExpectMatching(t,
			"a Varnish-bypassing response preserves the downstream cache contract",
			func(resp *httpclient.Response) bool {
				if resp.Status != http.StatusOK || resp.Echo == nil || resp.Header.Get("X-Cache") != "" {
					return false
				}
				directives := map[string]bool{}
				for _, value := range resp.Header.Values("Cache-Control") {
					for _, directive := range strings.Split(value, ",") {
						directives[strings.ToLower(strings.TrimSpace(directive))] = true
					}
				}
				vary := map[string]bool{}
				for _, value := range resp.Header.Values("Vary") {
					for _, field := range strings.Split(value, ",") {
						vary[strings.ToLower(strings.TrimSpace(field))] = true
					}
				}
				return directives["private"] && !directives["public"] && directives["max-age=60"] &&
					directives[`private="set-cookie"`] && directives["public-key=keep"] && vary["x-api-version"]
			})
}

func assertCacheHeadersStrippedOnFallback(t *testing.T, hc *httpclient.Client, host, path string) {
	t.Helper()
	reservedHeaders := cacheReservedHeaders()
	req := hc.GET(host, path)
	for _, name := range reservedHeaders {
		req.WithHeader(name, "forged")
	}
	req.ExpectMatching(t,
		"direct fallback strips the cache tier's private request headers",
		func(resp *httpclient.Response) bool {
			return resp.Status == http.StatusOK && resp.Header.Get("X-Cache") == "" && echoCacheHeadersStripped(resp)
		})
}

func assertEchoCacheHeadersStripped(t *testing.T, resp *httpclient.Response) {
	t.Helper()
	if !echoCacheHeadersStripped(resp) {
		t.Fatal("origin received a private X-Haptic cache protocol header")
	}
}

func echoCacheHeadersStripped(resp *httpclient.Response) bool {
	if resp.Echo == nil {
		return false
	}
	for _, name := range cacheReservedHeaders() {
		if _, found := resp.Echo.Headers[strings.ToLower(name)]; found {
			return false
		}
	}
	return true
}

func cacheReservedHeaders() []string {
	return []string{
		"X-Haptic-Backend",
		"X-Haptic-Cache-Fetch",
		"X-Haptic-Cache-Probe",
		"X-Haptic-Cache-Vary",
		"X-Haptic-Cache-Skip-CT",
		"X-Haptic-Cache-Max-Size",
		"X-Haptic-Cache-TTL",
		"X-Haptic-Cache-Grace",
		"X-Haptic-Cache-SWR",
		"X-Haptic-Cache-Stale-If-Error",
		"X-Haptic-Cache-Keep",
		"X-Haptic-Cache-Strip-Cookie",
		"X-Haptic-Cache-Negative-TTL",
		"X-Haptic-Cache-Auth",
		"X-Haptic-Cache-Uncacheable",
		"X-Haptic-Cache-Origin-425",
		"X-Haptic-Cache-Origin-Transport-Failure",
		"X-Haptic-Cache-Failure",
	}
}

// assertNoRateLimitStoreWithoutSharedLimiter proves the chart deploys no shared
// rate-limit store in a shard that never enabled the shared limiter.
//
// This shard is the right place for it, and the ONLY one that can catch this failure
// mode. rateLimit.shared.managedStore.enabled defaults to true while
// rateLimit.shared.enabled defaults to false, and the store's render gate once consulted
// only the former — so the controller rendered a Valkey StatefulSet, a PDB and two
// Services that nothing consumed. On a default install that surfaced (invisibly) as a
// forbidden-apply hot loop, which scripts/test-helm-defaults.sh now fails on. But THIS
// shard sets cache.varnish.enabled=true, and the Role's apps grant is or-combined across
// the two tiers — so here the StatefulSet apply SUCCEEDS and the useless workload is
// really created, with no error anywhere to notice it. Only counting the objects catches
// that, which is why a log-scanning guard is not enough on its own.
func assertNoRateLimitStoreWithoutSharedLimiter(t *testing.T) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	out, err := exec.CommandContext(ctx, "kubectl",
		"--kubeconfig", kubeconfigPath, "-n", ControllerNamespace,
		"get", "statefulset,poddisruptionbudget,service",
		"-l", labelSelectorRateLimitStore, "-o", "name").CombinedOutput()
	if err != nil {
		t.Fatalf("list rate-limit store objects: %v: %s", err, out)
	}
	if found := string(bytes.TrimSpace(out)); found != "" {
		t.Fatalf("shared rate limiting is disabled in this shard, but the chart deployed store objects:\n%s", found)
	}
}

// assertVarnishRejectsUnauthorizedPod proves the cache NetworkPolicy is
// enforced, rather than merely present. The probe runs in Varnish's namespace
// without the release-scoped HAProxy labels. DNS must resolve, but the TCP
// connection to Varnish must time out. The preceding cache HIT assertion proves
// the same endpoint was reachable through the allowed HAProxy path first.
func assertVarnishRejectsUnauthorizedPod(t *testing.T) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()

	podName := fmt.Sprintf("varnish-policy-probe-%d", time.Now().UnixNano())
	service := HelmReleaseName + "-varnish-cache." + ControllerNamespace + ".svc.cluster.local"
	script := fmt.Sprintf(
		`set -u; if ! nslookup %s >/dev/null; then echo DNS_ERROR; exit 0; fi; if curl -s -o /dev/null --connect-timeout 2 --max-time 4 http://%s:6081/; then echo UNEXPECTEDLY_ALLOWED; else code=$?; if [ "$code" = 28 ]; then echo DENIED; else echo "PROBE_ERROR_$code"; fi; fi`,
		service, service)
	kubectlArgs := func(extra ...string) []string {
		return append([]string{"--kubeconfig", kubeconfigPath, "-n", ControllerNamespace}, extra...)
	}

	run := exec.CommandContext(ctx, "kubectl", kubectlArgs(
		"run", podName,
		"--restart=Never",
		"--image="+VarnishPolicyProbeImage,
		"--quiet",
		"--command", "--",
		"sh", "-c", script,
	)...)
	var runErr bytes.Buffer
	run.Stderr = &runErr
	if err := run.Run(); err != nil {
		t.Fatalf("create unauthorized Varnish probe pod: %v; stderr: %s", err, runErr.String())
	}
	t.Cleanup(func() {
		cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cleanupCancel()
		_ = exec.CommandContext(cleanupCtx, "kubectl", kubectlArgs(
			"delete", "pod", podName, "--now", "--ignore-not-found")...).Run()
	})

	wait := exec.CommandContext(ctx, "kubectl", kubectlArgs(
		"wait", "--for=jsonpath={.status.phase}=Succeeded",
		"pod/"+podName, "--timeout=30s")...)
	var waitErr bytes.Buffer
	wait.Stderr = &waitErr
	if err := wait.Run(); err != nil {
		logs, _ := exec.CommandContext(ctx, "kubectl", kubectlArgs("logs", podName)...).CombinedOutput()
		t.Fatalf("unauthorized Varnish probe was not denied cleanly: %v; stderr: %s; logs: %s", err, waitErr.String(), logs)
	}

	logs, err := exec.CommandContext(ctx, "kubectl", kubectlArgs("logs", podName)...).CombinedOutput()
	if err != nil {
		t.Fatalf("read unauthorized Varnish probe logs: %v: %s", err, logs)
	}
	if string(bytes.TrimSpace(logs)) != "DENIED" {
		t.Fatalf("unexpected unauthorized Varnish probe result: %q", logs)
	}
}
