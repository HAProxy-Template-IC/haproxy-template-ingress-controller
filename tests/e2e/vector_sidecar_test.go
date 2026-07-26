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
	"bytes"
	"context"
	"fmt"
	"os/exec"
	"strings"
	"testing"
	"time"

	"sigs.k8s.io/e2e-framework/pkg/envconf"
	"sigs.k8s.io/e2e-framework/pkg/features"

	"gitlab.com/haproxy-haptic/haptic/tests/e2e/httpclient"
	"gitlab.com/haproxy-haptic/haptic/tests/testutil"
)

// execInHAProxyPod runs a command inside one container of an HAProxy pod.
func execInHAProxyPod(ctx context.Context, pod, container string, argv ...string) (string, error) {
	args := []string{
		"--kubeconfig", kubeconfigPath,
		"-n", ControllerNamespace,
		"exec", pod, "-c", container, "--",
	}
	args = append(args, argv...)
	cmd := exec.CommandContext(ctx, "kubectl", args...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return stdout.String(), fmt.Errorf("kubectl exec %s -c %s %v: %w (stderr: %s)",
			pod, container, argv, err, stderr.String())
	}
	return stdout.String(), nil
}

// podJSONPath reads one jsonpath expression off an HAProxy pod.
func podJSONPath(ctx context.Context, pod, expr string) (string, error) {
	cmd := exec.CommandContext(ctx, "kubectl",
		"--kubeconfig", kubeconfigPath,
		"-n", ControllerNamespace,
		"get", "pod", pod, "-o", "jsonpath="+expr,
	)
	var stdout bytes.Buffer
	cmd.Stdout = &stdout
	if err := cmd.Run(); err != nil {
		return "", err
	}
	return strings.TrimSpace(stdout.String()), nil
}

// curlStatus returns the HTTP status code curl saw for url, from inside the
// haproxy container. curl (not wget — the Debian HAProxy image ships no wget)
// with -o /dev/null so a non-2xx response still exits 0 and the code is the
// only thing read.
func curlStatus(ctx context.Context, pod, url string) (string, error) {
	out, err := execInHAProxyPod(ctx, pod, "haproxy",
		"curl", "-s", "-o", "/dev/null", "-w", "%{http_code}", url)
	return strings.TrimSpace(out), err
}

// apiProxyGet fetches a pod port through the API server proxy — the same
// pod-IP path Prometheus uses, and it needs no tooling inside the container.
// Returns the body; err is non-nil when the proxy could not reach the port.
func apiProxyGet(ctx context.Context, pod string, port int, path string) (string, error) {
	cmd := exec.CommandContext(ctx, "kubectl",
		"--kubeconfig", kubeconfigPath,
		"get", "--raw",
		fmt.Sprintf("/api/v1/namespaces/%s/pods/%s:%d/proxy/%s", ControllerNamespace, pod, port, path),
	)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return stdout.String(), fmt.Errorf("%w (stderr: %s)", err, strings.TrimSpace(stderr.String()))
	}
	return stdout.String(), nil
}

// TestVectorSidecar covers the parts of the vector wiring that only a live
// cluster can establish. The chart's validationTests prove the config renders;
// they cannot prove that HAProxy's datagrams arrive, that the pushed config
// actually reached the sidecar, or that the metrics restriction behaves both ways.
func TestVectorSidecar(t *testing.T) {
	t.Parallel()

	var pod string

	feature := features.New("Vector sidecar: log transport, merged metrics, loopback-only exporter").
		Setup(func(ctx context.Context, t *testing.T, cfg *envconf.Config) context.Context {
			pods := listHAProxyPods(t)
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
		Assess("the sidecar is Ready, which only happens after the rendered config is pushed", func(ctx context.Context, t *testing.T, cfg *envconf.Config) context.Context {
			// Readiness targets the prometheus_exporter port, and the exporter exists
			// ONLY in the config HAPTIC pushes — the bootstrap ConfigMap omits it. A
			// Ready vector container therefore proves the whole chain completed:
			// render -> dataplane general-storage -> file-watch -> graceful reload.
			// It would also catch a subPath mount silently killing inotify.
			//
			// POLLED, not sampled once: this transition is exactly what the gate
			// exists to delay. The suite's readiness wait covers the controller
			// pipeline, not this sidecar's first reload plus its probe's
			// initialDelaySeconds, so a single read races the thing under test.
			var ready string
			err := testutil.WaitForCondition(ctx, testutil.FastWaitConfig(), func(c context.Context) (bool, error) {
				var err error
				ready, err = podJSONPath(c, pod,
					`{.status.containerStatuses[?(@.name=="vector")].ready}`)
				if err != nil {
					return false, nil
				}
				return ready == "true", nil
			})
			if err != nil {
				// A genuine failure here means the config never landed, so show what
				// the sidecar itself said rather than only the timeout.
				logs, _ := execInHAProxyPod(ctx, pod, "haproxy", "sh", "-c",
					"ls -l /etc/haproxy/general/vector.yaml 2>&1 || true")
				t.Fatalf("vector container never became Ready (last=%q): the rendered config never "+
					"reached it, or its file-watch never fired.\nconfig file state: %s", ready, logs)
			}
			return ctx
		}).
		Assess("one endpoint carries HAProxy's and Vector's metrics together", func(ctx context.Context, t *testing.T, cfg *envconf.Config) context.Context {
			// The point of fronting the endpoints: haproxy_* (re-exported) and
			// vector_* (internal) on a single port.
			// Fetched through the API server proxy, which reaches the pod IP exactly
			// as Prometheus does via the PodMonitor — so this asserts the real scrape
			// path, not just that something is listening on loopback.
			var body string
			err := testutil.WaitForCondition(ctx, testutil.FastWaitConfig(), func(c context.Context) (bool, error) {
				out, err := apiProxyGet(c, pod, 9598, "metrics")
				if err != nil {
					return false, nil
				}
				body = out
				return strings.Contains(body, "haproxy_") && strings.Contains(body, "vector_"), nil
			})
			if err != nil {
				t.Fatalf("merged /metrics never carried both metric families: %v (got %d bytes)", err, len(body))
			}
			// haproxy_* can only be there if vector reached HAProxy's exporter over
			// loopback, so this doubles as proof the loopback side of the gate works.
			for _, want := range []string{"haproxy_", "vector_"} {
				if !strings.Contains(body, want) {
					t.Errorf("merged /metrics is missing %q series", want)
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
