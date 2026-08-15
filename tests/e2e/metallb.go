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
	"net"
	"os/exec"
	"strings"
	"time"
)

// metallbVersion is the MetalLB version we install into the e2e cluster.
// Pin a specific version so cluster bring-up is reproducible across runs
// and CI runners; bumping is a deliberate change. v0.15.3 is the latest
// stable release at the time of writing.
const metallbVersion = "v0.15.3"

// metallbManifestURL is the canonical "native" install manifest from the
// metallb release, the same one Envoy Gateway / Istio / etc. use in their
// kind-based conformance setups. It bundles the controller, speaker
// daemonset, CRDs, RBAC, and webhook.
const metallbManifestURL = "https://raw.githubusercontent.com/metallb/metallb/" + metallbVersion + "/config/manifests/metallb-native.yaml"

// installMetalLB installs MetalLB into the kind cluster and configures an
// IPAddressPool covering the upper end of the kind Docker network's IPv4
// subnet. Without this, LoadBalancer-typed Services sit Pending forever
// in kind (kind has no built-in load balancer). The Gateway API
// conformance suite expects Gateway.status.addresses to populate with
// reachable IPs, which requires real LoadBalancer support.
//
// The address pool is derived from the configured kind Docker network;
// pinning a range would break on daemons that allocate another subnet.
//
// Idempotent: re-running on a cluster that already has MetalLB installed
// just re-applies the same manifests (no-op via SSA), so the standard
// KEEP_CLUSTER=true reuse path keeps working.
func installMetalLB(ctx context.Context) (context.Context, error) {
	// Step 1: apply the MetalLB manifest. This is upstream's bundle —
	// CRDs, controller deployment, speaker daemonset, RBAC, ValidatingWebhook.
	apply := exec.CommandContext(ctx, "kubectl", "apply",
		"--kubeconfig", kubeconfigPath,
		"-f", metallbManifestURL)
	if out, err := apply.CombinedOutput(); err != nil {
		return ctx, fmt.Errorf("apply metallb manifest: %w (output: %s)", err, out)
	}

	// Step 2: wait for the controller deployment to be available. The
	// IPAddressPool / L2Advertisement we apply next are validated by
	// MetalLB's webhook, so applying them too early races the webhook's
	// own pod startup and surfaces as TLS handshake errors. Waiting
	// for the controller deployment is the right gate — it's the
	// component the webhook lives in.
	wait := exec.CommandContext(ctx, "kubectl", "wait",
		"--kubeconfig", kubeconfigPath,
		"--namespace", "metallb-system",
		"--for=condition=Available",
		"--timeout=180s",
		"deployment/controller")
	if out, err := wait.CombinedOutput(); err != nil {
		return ctx, fmt.Errorf("wait for metallb controller: %w (output: %s)", err, out)
	}

	// Step 3: derive the IPv4 range from the kind Docker network.
	addressRange, err := metalLBAddressRange(ctx, e2eCluster.DockerNetwork)
	if err != nil {
		return ctx, fmt.Errorf("derive metallb address range: %w", err)
	}

	// Step 4: create the IPAddressPool + L2Advertisement. We retry the
	// apply because MetalLB's validating webhook can briefly 503 even
	// after the controller deployment reports Available — the webhook
	// readiness probe and the deployment's readiness probe race. The
	// retry loop terminates on success or the context deadline; no
	// unbounded sleeps.
	pool := fmt.Sprintf(`apiVersion: metallb.io/v1beta1
kind: IPAddressPool
metadata:
  name: e2e-pool
  namespace: metallb-system
spec:
  addresses:
    - %s
---
apiVersion: metallb.io/v1beta1
kind: L2Advertisement
metadata:
  name: e2e-l2
  namespace: metallb-system
spec:
  ipAddressPools:
    - e2e-pool
`, addressRange)

	for attempt := 0; ; attempt++ {
		applyPool := exec.CommandContext(ctx, "kubectl", "apply",
			"--kubeconfig", kubeconfigPath,
			"-f", "-")
		applyPool.Stdin = strings.NewReader(pool)
		out, err := applyPool.CombinedOutput()
		if err == nil {
			return ctx, nil
		}
		if attempt >= 12 {
			return ctx, fmt.Errorf("apply metallb address pool after %d retries: %w (output: %s)", attempt, err, out)
		}
		// Wait between attempts: the metallb webhook may need a few seconds
		// after its Deployment reports Available before it can serve admission.
		// Without this sleep, all 12 retries fall through `default` in well
		// under a second and the loop exits before the webhook is ready.
		select {
		case <-ctx.Done():
			return ctx, fmt.Errorf("apply metallb address pool: %w (last error: %v, output: %s)", ctx.Err(), err, out)
		case <-time.After(5 * time.Second):
		}
	}
}

// metalLBAddressRange computes the IPv4 address range to hand to
// MetalLB's IPAddressPool from the kind Docker network's IPAM
// configuration. We take the upper sliver (.200–.250 of the third
// octet) to leave room for the kind nodes themselves, which Docker
// allocates from the bottom of the subnet.
func metalLBAddressRange(ctx context.Context, networkName string) (string, error) {
	out, err := exec.CommandContext(ctx, "docker", "network", "inspect", networkName,
		"--format", "{{json .IPAM.Config}}").Output()
	if err != nil {
		return "", fmt.Errorf("inspect kind docker network %q: %w", networkName, err)
	}
	var configs []struct {
		Subnet string `json:"Subnet"`
	}
	if err := json.NewDecoder(bytes.NewReader(out)).Decode(&configs); err != nil {
		return "", fmt.Errorf("parse IPAM.Config: %w", err)
	}

	for _, c := range configs {
		if strings.Contains(c.Subnet, ":") {
			// IPv6 — skip; conformance suite IP family defaults to IPv4
			// and our chart's address discovery is IPv4-only today.
			continue
		}
		_, ipnet, err := net.ParseCIDR(c.Subnet)
		if err != nil {
			return "", fmt.Errorf("parse subnet %q: %w", c.Subnet, err)
		}
		// Build "<a>.<b>.<c>.200-<a>.<b>.<c>.250" from a /16-or-narrower
		// IPv4 subnet. Kind defaults to 172.18.0.0/16, which makes the
		// third-octet selection deterministic; on hosts with a /24 we
		// still get 50 usable IPs in the same /24.
		ip4 := ipnet.IP.To4()
		if ip4 == nil {
			return "", fmt.Errorf("subnet %q is not IPv4", c.Subnet)
		}
		// Use the topmost reachable octet within the kind range. For a
		// /16 (172.18.0.0/16) we jump to 172.18.255.x. For narrower
		// subnets fall back to the same third octet as the network.
		ones, bits := ipnet.Mask.Size()
		var thirdOctet byte
		if bits-ones >= 16 {
			thirdOctet = 255
		} else {
			thirdOctet = ip4[2]
		}
		base := fmt.Sprintf("%d.%d.%d", ip4[0], ip4[1], thirdOctet)
		return fmt.Sprintf("%s.200-%s.250", base, base), nil
	}
	return "", fmt.Errorf("no IPv4 subnet found on kind docker network %q", networkName)
}
