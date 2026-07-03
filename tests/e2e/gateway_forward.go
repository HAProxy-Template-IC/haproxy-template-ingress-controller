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
	"bufio"
	"context"
	"fmt"
	"os"
	"os/exec"
	"regexp"
	"strconv"
	"testing"
	"time"
)

// GatewayForward is a live kubectl port-forward tunnel to a Gateway's
// per-Gateway Service. HTTPPort/HTTPSPort are local (127.0.0.1) ports
// mapped to the Service's 80/443; a port is 0 when it wasn't requested
// (the Gateway has no listener on it).
//
// The chart exposes each Gateway through a dedicated LoadBalancer Service
// (`gw-<gatewayNamespace>-<gatewayName>` in the chart namespace) rather
// than the shared HAProxy NodePorts — that per-Gateway isolation is the
// point of the per-Gateway-IP design, and it is what the upstream
// conformance suite exercises via the LB address directly. The e2e suite
// runs OUTSIDE the kind network (plain `go test` on the host, or a GitLab
// job container in DinD where the MetalLB IPs are unroutable), so tests
// reach the Service through `kubectl port-forward`, which tunnels via the
// apiserver and works identically in both environments.
// ponytail: the tunnel is not restarted if it dies mid-test; each test's
// tunnel lives for seconds — add auto-restart only if SPDY flakiness
// actually shows up in CI.
type GatewayForward struct {
	HTTPPort  int
	HTTPSPort int
}

var forwardLineRe = regexp.MustCompile(`Forwarding from 127\.0\.0\.1:(\d+) -> (\d+)`)

// ForwardGateway starts a kubectl port-forward to the per-Gateway Service
// of the given Gateway and returns the local port mapping. servicePorts
// lists the Service ports to forward (80 for HTTP listeners, 443 for
// HTTPS/TLS listeners). The tunnel is torn down via t.Cleanup.
//
// The per-Gateway Service is created by the chart's render pipeline, so it
// appears shortly after the Gateway; the wait below covers that
// propagation. Traffic through the tunnel lands on one HAProxy pod's
// per-Gateway bind — routing behavior is identical to the LB path minus
// the load-balancer hop.
func ForwardGateway(ctx context.Context, t *testing.T, gatewayNamespace, gatewayName string, servicePorts ...int) GatewayForward {
	t.Helper()
	if len(servicePorts) == 0 {
		servicePorts = []int{80}
	}
	// Gate on Programmed=True first: the chart flips it only after HAProxy
	// reload verification, i.e. once the per-Gateway bind is actually
	// listening. Tunneling earlier is fatal — kubectl port-forward treats
	// the first refused upstream connection as "lost connection to pod"
	// and exits, so a tunnel opened before the bind exists dies on the
	// test's first probe.
	if out, err := exec.CommandContext(ctx, "kubectl", "--kubeconfig", kubeconfigPath,
		"-n", gatewayNamespace, "wait", "--for=condition=Programmed",
		"gateway/"+gatewayName, "--timeout=30s").CombinedOutput(); err != nil {
		t.Fatalf("gateway %s/%s never became Programmed: %v (%s)", gatewayNamespace, gatewayName, err, out)
	}

	// Discover the per-Gateway Service by its gateway labels rather than
	// computing its name: the chart shortens long names to fit the 63-char
	// Service name cap, so the naming scheme is chart-internal. The labels
	// (gateway-name + gateway-namespace) are the stable public contract —
	// the upstream GatewayInfrastructure conformance test discovers the
	// Service the same way.
	selector := fmt.Sprintf(
		"gateway.networking.k8s.io/gateway-name=%s,gateway.networking.k8s.io/gateway-namespace=%s",
		gatewayName, gatewayNamespace)
	var svc string
	waitCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	for {
		out, err := exec.CommandContext(waitCtx, "kubectl", "--kubeconfig", kubeconfigPath,
			"-n", ControllerNamespace, "get", "service", "-l", selector,
			"-o", "jsonpath={.items[0].metadata.name}").Output()
		if err == nil && len(out) > 0 {
			svc = string(out)
			break
		}
		select {
		case <-waitCtx.Done():
			t.Fatalf("per-Gateway Service for %s/%s never appeared (selector %q): %v",
				gatewayNamespace, gatewayName, selector, err)
		case <-time.After(500 * time.Millisecond):
		}
	}

	args := []string{"--kubeconfig", kubeconfigPath, "-n", ControllerNamespace, "port-forward", "service/" + svc}
	for _, p := range servicePorts {
		args = append(args, ":"+strconv.Itoa(p)) // random local port
	}
	// Deliberately context.Background(): the tunnel must outlive the Setup
	// phase's ctx and is torn down via t.Cleanup below.
	fwdCtx, stop := context.WithCancel(context.Background())
	cmd := exec.CommandContext(fwdCtx, "kubectl", args...)
	cmd.Stderr = os.Stderr
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		stop()
		t.Fatalf("port-forward stdout pipe: %v", err)
	}
	if err := cmd.Start(); err != nil {
		stop()
		t.Fatalf("start kubectl port-forward %s: %v", svc, err)
	}
	t.Cleanup(func() {
		stop()
		_ = cmd.Wait()
	})

	// Parse the "Forwarding from 127.0.0.1:<local> -> <target>" lines.
	// kubectl reports the resolved TARGET port (the pod's per-Gateway bind
	// port, chart-allocated), not the Service port we asked for — so local
	// ports are assigned to the requested servicePorts by ORDER, which is
	// the order kubectl emits them. The IPv6 twin lines ("[::1]:...") repeat
	// the same local port and are deduplicated.
	var fwd GatewayForward
	scanner := bufio.NewScanner(stdout)
	deadline := time.After(20 * time.Second)
	seen := map[int]bool{}
	parsed := 0
	lines := make(chan string, 8)
	go func() {
		for scanner.Scan() {
			lines <- scanner.Text()
		}
		close(lines)
	}()
	for parsed < len(servicePorts) {
		select {
		case line, ok := <-lines:
			if !ok {
				t.Fatalf("kubectl port-forward %s exited before forwarding (parsed %d/%d ports)", svc, parsed, len(servicePorts))
			}
			m := forwardLineRe.FindStringSubmatch(line)
			if m == nil {
				continue
			}
			local, _ := strconv.Atoi(m[1])
			if seen[local] {
				continue
			}
			seen[local] = true
			switch servicePorts[parsed] {
			case 80:
				fwd.HTTPPort = local
			case 443:
				fwd.HTTPSPort = local
			default:
				t.Fatalf("ForwardGateway: unsupported service port %d (only 80/443)", servicePorts[parsed])
			}
			parsed++
		case <-deadline:
			t.Fatalf("kubectl port-forward %s: timed out waiting for forwarding lines (parsed %d/%d)", svc, parsed, len(servicePorts))
		}
	}
	return fwd
}
