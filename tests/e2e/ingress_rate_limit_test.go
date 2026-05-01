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
	"os/exec"
	"strings"
	"testing"
	"time"

	"sigs.k8s.io/e2e-framework/pkg/envconf"
	"sigs.k8s.io/e2e-framework/pkg/features"

	"gitlab.com/haproxy-haptic/haptic/tests/e2e/httpclient"
)

// TestIngressRateLimit covers test_ingress_rate_limit: HAProxy's
// stick-table-backed rate-limit annotations gate requests by source IP.
// Verification has to come from *inside* the cluster — DinD NAT
// randomises the source IP for connections originating from the test
// host, so a parallel burst from the host hits HAProxy with N different
// src IPs and never trips the limit. We use kubectl exec into a
// per-test alpine/curl pod, where every request shares one cluster-IP
// and the limit reliably fires.
//
// Mirror of the bash assert_rate_limited helper, which uses the same
// pattern (xargs -P10 from inside an in-cluster pod).
func TestIngressRateLimit(t *testing.T) {
	t.Parallel()
	host := "ingress-ratelimit.localdev.me"

	// Conservative numbers: rate-limit at 5/period, send 20 requests.
	// Even with some racing, well above the limit.
	const (
		rateLimit  = 5
		burstTotal = 20
	)

	feature := features.New("Ingress: rate-limit annotation enforces from inside cluster").
		Setup(func(ctx context.Context, t *testing.T, cfg *envconf.Config) context.Context {
			client, err := cfg.NewClient()
			if err != nil {
				t.Fatalf("new client: %v", err)
			}
			ns := NamespaceForTest(ctx, t, client)
			DumpLogsOnFailure(t, ns)
			backend := NewEchoServerBackend(ctx, t, client, ns)
			NewIngress(ctx, t, client, ns, IngressSpec{
				Name:           "echo-ratelimit",
				Host:           host,
				Path:           "/",
				BackendService: backend.Service,
				BackendPort:    backend.Port,
				Annotations: map[string]string{
					"haproxy.org/rate-limit-requests":    fmt.Sprintf("%d", rateLimit),
					"haproxy.org/rate-limit-period":      "10s",
					"haproxy.org/rate-limit-size":        "100k",
					"haproxy.org/rate-limit-status-code": "429",
				},
			})
			// Wait for HAProxy to pick up the new Ingress before bursting.
			httpclient.New(t).GET(host, "/").ExpectOK(t)
			return StoreNamespaceInContext(ctx, ns)
		}).
		Assess("burst from in-cluster pod trips the limit (≥1 × 429)", func(ctx context.Context, t *testing.T, cfg *envconf.Config) context.Context {
			ns, err := GetNamespaceFromContext(ctx)
			if err != nil {
				t.Fatalf("get namespace: %v", err)
			}
			ok, blocked := rateLimitBurstFromCluster(ctx, t, ns, host, burstTotal)
			if blocked == 0 {
				t.Fatalf("expected at least one 429 from a burst of %d requests, got %d×200 / %d×429",
					burstTotal, ok, blocked)
			}
			t.Logf("rate-limit fired: %d×200, %d×429 over %d requests", ok, blocked, burstTotal)
			return ctx
		}).
		Feature()
	testEnv.Test(t, feature)
}

// StoreNamespaceInContext / GetNamespaceFromContext let Setup pass the
// per-test namespace name through to Assess. e2e-framework features
// share *envconf.Config across phases, but envconf doesn't carry
// arbitrary values — context does.
type namespaceKey struct{}

// StoreNamespaceInContext returns ctx with the namespace name attached
// under a private key, retrievable via GetNamespaceFromContext.
func StoreNamespaceInContext(ctx context.Context, namespace string) context.Context {
	return context.WithValue(ctx, namespaceKey{}, namespace)
}

// GetNamespaceFromContext retrieves the namespace name stored by
// StoreNamespaceInContext. Returns an error if not set.
func GetNamespaceFromContext(ctx context.Context) (string, error) {
	v, ok := ctx.Value(namespaceKey{}).(string)
	if !ok || v == "" {
		return "", fmt.Errorf("namespace not in context")
	}
	return v, nil
}

// rateLimitBurstFromCluster runs `total` concurrent curls against
// http://<host>/ from inside a kubectl-run alpine/curl pod, returning
// (ok, blocked) counts (200 vs 429). Other status codes are silently
// dropped (transient; not load-bearing for this test).
//
// The pod is created and deleted per call. We use --restart=Never +
// --rm so the pod tears down even if the test fails. Logs come from
// the curl exit-code list piped through awk for accumulation.
func rateLimitBurstFromCluster(ctx context.Context, t *testing.T, namespace, host string, total int) (ok, blocked int) {
	t.Helper()

	// alpine/curl includes both `curl` and `xargs`. The HAProxy NodePort
	// is also reachable via the in-cluster Service (port 80, host
	// header sets the route), so we go via the chart's haptic-haproxy
	// Service rather than the host-side NodePort to keep the source
	// IP consistent.
	cmdScript := fmt.Sprintf(
		`seq 1 %d | xargs -P 10 -I{} curl -s -o /dev/null -w "%%{http_code}\n" `+
			`-H "Host: %s" http://haptic-haproxy.haptic.svc/`,
		total, host)

	podName := fmt.Sprintf("ratelimit-burst-%d", time.Now().UnixNano())
	cmd := exec.CommandContext(ctx, "kubectl",
		"--kubeconfig", kubeconfigPath,
		"-n", namespace,
		"run", podName,
		"--rm", "-i",
		"--restart=Never",
		"--image=alpine/curl:latest",
		"--quiet",
		"--command", "--",
		"sh", "-c", cmdScript,
	)
	var out, errBuf bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &errBuf
	if err := cmd.Run(); err != nil {
		t.Fatalf("rate-limit burst pod failed: %v\nstdout: %s\nstderr: %s", err, out.String(), errBuf.String())
	}

	for _, line := range strings.Split(strings.TrimSpace(out.String()), "\n") {
		switch strings.TrimSpace(line) {
		case "200":
			ok++
		case "429":
			blocked++
		}
	}
	return ok, blocked
}
