// Copyright 2025 Philipp Hossner
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0

//go:build e2e

package e2e

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"sigs.k8s.io/e2e-framework/pkg/envconf"
	"sigs.k8s.io/e2e-framework/pkg/features"

	"gitlab.com/haproxy-haptic/haptic/tests/e2e/httpclient"
	"gitlab.com/haproxy-haptic/haptic/tests/testutil"
)

func vectorChildPID(ctx context.Context, pod string) (string, error) {
	out, err := execInHAProxyPod(ctx, pod, "vector", "/bin/sh", "-c", `
for _proc in /proc/[0-9]*; do
  _comm=
  IFS= read -r _comm < "$_proc/comm" || true
  if [ "$_comm" = vector ]; then
    printf '%s\n' "${_proc##*/}"
    exit 0
  fi
done
exit 1
`)
	return strings.TrimSpace(out), err
}

// TestVectorSidecar covers the parts of the vector wiring that only a live
// cluster can establish. The chart's validationTests prove the config renders;
// they cannot prove that HAProxy's datagrams arrive, that the pushed config
// actually reached the sidecar, or that the metrics restriction behaves both ways.
//
// Deliberately NOT t.Parallel(): the restart assess kills the Vector child of a
// shared HAProxy pod. Export briefly stops while the supervisor recovers it, so a
// concurrent test could misdiagnose that intentional fault as its own failure.
func TestVectorSidecar(t *testing.T) {
	var pod string
	var pods []string

	feature := features.New("Vector sidecar: log transport, merged metrics, loopback-only exporter").
		Setup(func(ctx context.Context, t *testing.T, cfg *envconf.Config) context.Context {
			pods = listHAProxyPods(t)
			if len(pods) == 0 {
				t.Fatal("no HAProxy pods found")
			}
			pod = pods[0]
			// Enabled by default; skip rather than fail if this run disabled it, so
			// the suite stays honest in both configurations.
			if resolveAccessLogContainer(ctx, t) != "vector" {
				t.Skip("vector sidecar not deployed in this configuration")
			}
			return ctx
		}).
		Assess("the rendered config activates Vector's metrics exporter", func(ctx context.Context, t *testing.T, cfg *envconf.Config) context.Context {
			for _, candidate := range pods {
				var body string
				err := testutil.WaitForCondition(ctx, testutil.FastWaitConfig(), func(c context.Context) (bool, error) {
					out, err := apiProxyGet(c, candidate, VectorMetricsPort, "metrics")
					if err != nil {
						return false, nil
					}
					body = out
					return strings.Contains(body, "vector_"), nil
				})
				if err != nil {
					configState, _ := execInHAProxyPod(ctx, candidate, "haproxy", "sh", "-c",
						"ls -l /etc/haproxy/general/vector.yaml 2>&1 || true")
					t.Fatalf("Vector's metrics exporter never activated on pod %s: %v (got %d bytes)\nconfig file state: %s",
						candidate, err, len(body), configState)
				}
			}
			return ctx
		}).
		Assess("each endpoint carries HAProxy's and Vector's metrics together", func(ctx context.Context, t *testing.T, cfg *envconf.Config) context.Context {
			// The point of fronting the endpoints: haproxy_* (re-exported) and
			// vector_* (internal) on a single port.
			// Fetched through the API server proxy, which reaches the pod IP exactly
			// as Prometheus does via the PodMonitor — so this asserts the real scrape
			// path, not just that something is listening on loopback.
			for _, candidate := range pods {
				var body string
				err := testutil.WaitForCondition(ctx, testutil.FastWaitConfig(), func(c context.Context) (bool, error) {
					out, err := apiProxyGet(c, candidate, VectorMetricsPort, "metrics")
					if err != nil {
						return false, nil
					}
					body = out
					return strings.Contains(body, "haproxy_") && strings.Contains(body, "vector_"), nil
				})
				if err != nil {
					t.Fatalf("merged /metrics on pod %s never carried both metric families: %v (got %d bytes)",
						candidate, err, len(body))
				}
				// haproxy_* can only be there if vector reached HAProxy's exporter over
				// loopback, so this doubles as proof the loopback side of the gate works.
				for _, want := range []string{"haproxy_", "vector_"} {
					if !strings.Contains(body, want) {
						t.Errorf("merged /metrics on pod %s is missing %q series", candidate, want)
					}
				}
			}
			return ctx
		}).
		Assess("HAProxy answers /metrics on loopback only, while the probes stay on the pod IP", func(ctx context.Context, t *testing.T, cfg *envconf.Config) context.Context {
			// The gate is on `dst` (the address connected TO) rather than on the bind,
			// because /healthz and /ready share this listener and the kubelet probes
			// the POD IP. Binding to loopback would make every probe fail. Both
			// directions are asserted, because only checking the allowed one would
			// pass even if the restriction did nothing.
			loop, err := curlStatus(ctx, pod, "http://127.0.0.1:8404/metrics")
			if err != nil {
				t.Fatalf("probing /metrics over loopback: %v", err)
			}
			if loop != "200" {
				t.Errorf("/metrics must answer over loopback — that is how vector scrapes it; got HTTP %s", loop)
			}

			ip, err := podJSONPath(ctx, pod, "{.status.podIP}")
			if err != nil || ip == "" {
				t.Fatalf("resolving pod IP: %v (ip=%q)", err, ip)
			}
			// Falls through to the status frontend's default when use-service does not
			// fire, which answers 503 — assert "not 200" rather than a specific code so
			// the test tracks the restriction, not HAProxy's choice of fall-through.
			viaPodIP, err := curlStatus(ctx, pod, fmt.Sprintf("http://%s:8404/metrics", ip))
			if err != nil {
				t.Fatalf("probing /metrics via the pod IP: %v", err)
			}
			if viaPodIP == "200" {
				t.Errorf("/metrics is still answerable on the pod IP (%s), so the dst gate did not apply", ip)
			}

			// The probes must keep working on the pod IP or the pod never goes Ready.
			// Checked through the API server proxy, i.e. from outside the pod.
			for _, probe := range []struct{ path, want string }{{"healthz", "OK"}, {"ready", "READY"}} {
				out, err := apiProxyGet(ctx, pod, 8404, probe.path)
				if err != nil {
					t.Fatalf("/%s must stay reachable on the pod IP for the kubelet's probes: %v", probe.path, err)
				}
				if !strings.Contains(out, probe.want) {
					t.Errorf("/%s returned %q, want it to contain %q", probe.path, strings.TrimSpace(out), probe.want)
				}
			}
			return ctx
		}).
		Assess("the watchdog restarts a wedged Vector child without affecting HAProxy", func(ctx context.Context, t *testing.T, cfg *envconf.Config) context.Context {
			client, err := cfg.NewClient()
			if err != nil {
				t.Fatalf("new client: %v", err)
			}
			ns := NamespaceForTest(ctx, t, client)
			DumpLogsOnFailure(t, ns)
			backend := NewEchoServerBackend(ctx, t, client, ns)
			host := fmt.Sprintf("vector-child-restart-%d.localdev.me", time.Now().UnixNano())
			NewIngress(ctx, t, client, ns, IngressSpec{
				Name:           "echo-vector-child-restart",
				Host:           host,
				BackendService: backend.Service,
				BackendPort:    backend.Port,
			})

			availabilityPath := "/vector-child-availability"
			err = testutil.WaitForCondition(ctx, testutil.FastWaitConfig(), func(c context.Context) (bool, error) {
				status, err := selectedHAProxyStatus(c, pod, host, availabilityPath)
				return err == nil && status == "200", nil
			})
			if err != nil {
				t.Fatalf("selected HAProxy pod %s never served the fault-test route: %v", pod, err)
			}

			beforeRestarts, err := podJSONPath(ctx, pod,
				`{.status.containerStatuses[?(@.name=="vector")].restartCount}`)
			if err != nil {
				t.Fatalf("reading vector restartCount: %v", err)
			}
			beforePID, err := vectorChildPID(ctx, pod)
			if err != nil {
				t.Fatalf("finding Vector child PID: %v", err)
			}

			stopAvailabilityMonitor, err := startSelectedHAProxyAvailabilityMonitor(ctx, pod, host, availabilityPath)
			if err != nil {
				t.Fatalf("starting selected-pod HAProxy availability monitor: %v", err)
			}
			monitorStopped := false
			defer func() {
				if !monitorStopped {
					_ = stopAvailabilityMonitor()
				}
			}()

			faultStarted := time.Now()
			if _, err := execInHAProxyPod(ctx, pod, "vector", "/bin/sh", "-c",
				`kill -STOP "$1"`, "stop-vector-child", beforePID); err != nil {
				t.Fatalf("stopping Vector child PID %s: %v", beforePID, err)
			}
			childRecovered := false
			defer func() {
				if !childRecovered {
					cleanupCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
					defer cancel()
					_, _ = execInHAProxyPod(cleanupCtx, pod, "vector", "/bin/sh", "-c",
						`kill -CONT "$1" 2>/dev/null || true`, "resume-vector-child", beforePID)
				}
			}()

			var recoveredPID, metrics string
			recoveryWait := testutil.WaitConfig{
				InitialInterval: 100 * time.Millisecond,
				MaxInterval:     time.Second,
				Timeout:         time.Minute,
				Multiplier:      1.5,
			}
			err = testutil.WaitForCondition(ctx, recoveryWait, func(c context.Context) (bool, error) {
				pid, err := vectorChildPID(c, pod)
				if err != nil || pid == beforePID {
					return false, nil
				}
				body, err := apiProxyGet(c, pod, VectorMetricsPort, "metrics")
				if err != nil {
					return false, nil
				}
				recoveredPID, metrics = pid, body
				return strings.Contains(metrics, "haproxy_") && strings.Contains(metrics, "vector_"), nil
			})
			if err != nil {
				socketState, _ := execInHAProxyPod(ctx, pod, "haproxy", "sh", "-c",
					"ls -l /run/vector/ 2>&1 || true")
				t.Fatalf("Vector watchdog did not replace stopped PID %s: %v (new PID=%q, metrics=%d bytes)\nsocket dir: %s",
					beforePID, err, recoveredPID, len(metrics), socketState)
			}
			childRecovered = true

			afterRestarts, err := podJSONPath(ctx, pod,
				`{.status.containerStatuses[?(@.name=="vector")].restartCount}`)
			if err != nil {
				t.Fatalf("reading vector restartCount after child recovery: %v", err)
			}
			if afterRestarts != beforeRestarts {
				t.Fatalf("Vector container restarted while its supervisor should have recovered only the child: %s -> %s",
					beforeRestarts, afterRestarts)
			}

			marker := fmt.Sprintf("/vector-child-recovered-%d", time.Now().UnixNano())
			status, err := selectedHAProxyStatus(ctx, pod, host, marker)
			if err != nil || status != "200" {
				t.Fatalf("selected HAProxy pod %s did not serve the recovery marker (status=%q): %v", pod, status, err)
			}
			rec := findAccessLogRecordInPod(ctx, t, pod, faultStarted, marker)
			if got := recordString(t, rec, "host"); got != host {
				t.Errorf("host = %q, want %q", got, host)
			}
			if got := recordString(t, rec, "instance_pod"); got != pod {
				t.Errorf("instance_pod = %q, want %q", got, pod)
			}

			monitorErr := stopAvailabilityMonitor()
			monitorStopped = true
			if monitorErr != nil {
				t.Fatalf("HAProxy became unavailable while the Vector child restarted: %v", monitorErr)
			}

			return ctx
		}).
		Assess("an access-log record reaches the sidecar over the Unix socket", func(ctx context.Context, t *testing.T, cfg *envconf.Config) context.Context {
			// End-to-end proof that the datagrams arrive. A UNIX datagram sender gets
			// NO error when nothing is listening, so a wrong socket path would look
			// identical to a working one from HAProxy's side.
			client, err := cfg.NewClient()
			if err != nil {
				t.Fatalf("new client: %v", err)
			}
			ns := NamespaceForTest(ctx, t, client)
			DumpLogsOnFailure(t, ns)
			backend := NewEchoServerBackend(ctx, t, client, ns)
			host := "vector-socket-log.localdev.me"
			NewIngress(ctx, t, client, ns, IngressSpec{
				Name:           "echo-vector-log",
				Host:           host,
				BackendService: backend.Service,
				BackendPort:    backend.Port,
			})

			// Unique per run, so the log filter can't match another test's traffic.
			marker := "/vector-log-" + ns
			since := time.Now().Add(-5 * time.Second)
			httpclient.New(t).GET(host, marker).ExpectOK(t)

			// findAccessLogRecord reads the container resolved above, i.e. `vector`.
			rec := findAccessLogRecord(ctx, t, since, marker)
			if got := recordString(t, rec, "host"); got != host {
				t.Errorf("host = %q, want %q", got, host)
			}
			return ctx
		}).
		Feature()

	testEnv.Test(t, feature)
}
