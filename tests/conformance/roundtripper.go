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

//go:build gateway_conformance

package conformance

import (
	"context"
	"fmt"
	"net"
	"strconv"
	"sync"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	clientset "k8s.io/client-go/kubernetes"
	"sigs.k8s.io/gateway-api/conformance/utils/config"
	"sigs.k8s.io/gateway-api/conformance/utils/roundtripper"

	"gitlab.com/haproxy-haptic/haptic/tests/kindutil"
)

// httpNodePort and httpsNodePort are the host-side ports the e2e kind
// cluster exposes via extraPortMappings. The chart's user-facing service
// has containerPort 30080 / 30443 NodePorts; kind translates those to
// the host's 31080 / 31443 (see tests/e2e/main_test.go e2eKindConfig).
//
// In DinD the host is the docker-service container; locally it's
// 127.0.0.1. Either way these ports are reachable from the test process,
// while the metallb-assigned LoadBalancer IPs (172.19.255.0/24) are not
// because the test process sits on a different docker network than the
// kind nodes.
//
// Dynamic NodePorts (allocated by the apiserver for the chart's
// gateway-listener-ports Service) are NOT in extraPortMappings and
// therefore aren't reachable via 127.0.0.1; for those we fall back to
// dialing the kind node's docker-network IP directly.
const (
	httpNodePort  = 31080
	httpsNodePort = 31443
)

// haproxyServiceNamespace + haproxyServiceName identify the chart-emitted
// HAProxy Service. Both static (helm-owned: http/https/stats) and dynamic
// (haptic-owned: gw-<port>-<proto>) port entries live on this same Service
// — the chart's gateway-listener-ports snippet does a partial-ownership
// SSA patch on it (see features-090-gateway-listener-ports-service in
// libraries/gateway.yaml plus AnnotationOwnership in
// pkg/controller/resourceapplier). The conformance RoundTripper looks
// the Service up by namespace+name at suite-init time, then refreshes
// on cache miss as conformance fixtures land additional Gateways.
const (
	haproxyServiceNamespace = "haptic"
	haproxyServiceName      = "haptic-haproxy"
)

// portRoute carries the destination needed to reach a given Gateway
// listener port from the test process. `nodeIP` + `nodePort` is dialed
// instead of (LBIP, port) because the conformance test process can't
// reach the kind cluster's metallb LB IPs directly.
type portRoute struct {
	nodeIP   string
	nodePort int
}

// portRouter holds the merged static + dynamic port table behind a
// mutex. Conformance tests apply fixtures throughout the run (including
// Gateways declaring fresh listener ports), so the table must refresh
// on cache misses — not just at suite-init time. We do that lazily: a
// dial for an unknown port triggers a re-query against the cluster.
//
// Two lookup keys are maintained:
//
//   - `table` (port → portRoute): chart-static path. Used when no
//     per-Gateway Service matches the dial address. 80/443 seeded at
//     suite init plus dynamic entries from the chart's main Service
//     (`haptic-haproxy`).
//   - `byLBIP` (LB-IP → port → NodePort): per-Gateway path (phase 6 of
//     the per-Gateway-IP refactor). Each Gateway with HTTPS listeners
//     has its own LoadBalancer Service in the controller namespace
//     whose `status.loadBalancer.ingress[].ip` is the Gateway's
//     status.addresses entry. The Service exposes the listener ports
//     mapped to apiserver-allocated NodePorts; we discover them by
//     listing controller-namespace Services labelled
//     `gateway.networking.k8s.io/gateway-name` and key by the LB IP
//     so dials targeting the per-Gateway address find their own
//     NodePort instead of falling through to the chart-static
//     bind-line config.
type portRouter struct {
	cs       clientset.Interface
	hostIP   string
	mu       sync.RWMutex
	table    map[int]portRoute
	byLBIP   map[string]map[int]int
	lastSync time.Time
}

func newPortRouter(cs clientset.Interface, hostIP string, initial map[int]portRoute) *portRouter {
	return &portRouter{cs: cs, hostIP: hostIP, table: initial, byLBIP: map[string]map[int]int{}}
}

// lookup resolves an inbound dial target to a portRoute.
//
// Resolution order:
//  1. Per-Gateway: if `host` matches a per-Gateway Service's LB IP,
//     return that Service's NodePort for `port`. This is what gives
//     each Gateway its own bind-line SSL config (verify required vs
//     optional, ca-ignore-err) — the conformance suite dials the
//     Gateway's status.addresses IP and lands on the per-Gateway
//     bind in HAProxy.
//  2. Chart-static fallback: lookup by port alone, returning the
//     chart's main Service NodePort. Used by Ingress TLS, pinned-IP
//     Gateways (`spec.addresses` set), and all HTTP traffic.
//
// Refreshes from the cluster on cache miss in either layer.
func (r *portRouter) lookup(ctx context.Context, host string, port int) (portRoute, bool) {
	r.mu.RLock()
	if perPort, ok := r.byLBIP[host]; ok {
		if np, ok := perPort[port]; ok {
			r.mu.RUnlock()
			return portRoute{nodeIP: r.hostIP, nodePort: np}, true
		}
	}
	route, ok := r.table[port]
	r.mu.RUnlock()
	if ok {
		return route, true
	}
	// Cache miss: refresh both tables from the cluster. Fixtures
	// applied by the conformance test framework after suite-init
	// populate the chart's main Service (dynamic listener ports)
	// AND emit per-Gateway Services (phase 3) asynchronously; this
	// lazy refresh picks both up without requiring the test to call
	// into us first.
	r.refresh(ctx)
	r.mu.RLock()
	defer r.mu.RUnlock()
	if perPort, ok := r.byLBIP[host]; ok {
		if np, ok := perPort[port]; ok {
			return portRoute{nodeIP: r.hostIP, nodePort: np}, true
		}
	}
	route, ok = r.table[port]
	return route, ok
}

func (r *portRouter) refresh(ctx context.Context) {
	r.mu.Lock()
	defer r.mu.Unlock()
	// Throttle to once per second — many Get/POST calls in a single
	// test will land here in quick succession; we only need one
	// cluster query per burst.
	if time.Since(r.lastSync) < time.Second {
		return
	}
	dyn, err := discoverDynamicNodePorts(ctx, r.cs)
	if err == nil {
		for port, np := range dyn {
			r.table[port] = portRoute{nodeIP: r.hostIP, nodePort: np}
		}
	}
	perGw, err := discoverPerGatewayNodePorts(ctx, r.cs)
	if err == nil {
		// Replace the per-LB-IP cache wholesale so we don't keep
		// stale entries from torn-down test fixtures (each test
		// applies + cleans up its own Gateway).
		r.byLBIP = perGw
	}
	r.lastSync = time.Now()
}

// newNodePortRoundTripper wraps roundtripper.DefaultRoundTripper with a
// CustomDialContext that ignores the conformance suite's URL host (the
// Gateway.Status address — a metallb LoadBalancer IP unreachable from the
// test process) and dials the right NodePort instead. The HTTP Host
// header and the TLS SNI are preserved untouched, so HAProxy still
// performs hostname-based routing and certificate selection correctly.
//
// router holds the dynamic port table. 80/443 always route to the
// chart's static haproxy-service NodePorts via the host loopback
// (127.0.0.1 or DinD docker-service); dynamic listener ports route via
// the kind node's docker-network IP directly because they aren't in
// kind's extraPortMappings. Cache misses trigger a live re-query.
func newNodePortRoundTripper(timeoutCfg config.TimeoutConfig, debug bool, router *portRouter) (roundtripper.RoundTripper, error) {
	dialer := &net.Dialer{Timeout: 10 * time.Second, KeepAlive: 30 * time.Second}
	return &roundtripper.DefaultRoundTripper{
		Debug:         debug,
		TimeoutConfig: timeoutCfg,
		CustomDialContext: func(ctx context.Context, network, address string) (net.Conn, error) {
			route, err := dialPortForAddress(ctx, address, router)
			if err != nil {
				return nil, err
			}
			return dialer.DialContext(ctx, network, net.JoinHostPort(route.nodeIP, strconv.Itoa(route.nodePort)))
		},
	}, nil
}

// dialPortForAddress maps the conformance suite's intended dial target
// to the matching node IP + NodePort. The host part of `address` is
// the Gateway's status address (a metallb LoadBalancer IP); when a
// per-Gateway Service emits its own LB IP for that Gateway, the
// router's per-LB-IP table returns the Gateway's specific NodePort,
// which lets the conformance test exercise per-Gateway bind-line SSL
// config. Unrecognised (host, port) tuples return an error rather
// than silently routing to the wrong listener.
func dialPortForAddress(ctx context.Context, address string, router *portRouter) (portRoute, error) {
	host, p, err := net.SplitHostPort(address)
	if err != nil {
		return portRoute{}, fmt.Errorf("parse address %q: %w", address, err)
	}
	pi := 80
	if p != "" {
		pi, err = strconv.Atoi(p)
		if err != nil {
			return portRoute{}, fmt.Errorf("parse port %q: %w", p, err)
		}
	}
	if route, ok := router.lookup(ctx, host, pi); ok {
		return route, nil
	}
	return portRoute{}, fmt.Errorf("unexpected dial target %q (no NodePort mapping configured for host=%q port=%d)", address, host, pi)
}

// buildInitialPortTable seeds the router with the chart's static
// haproxy-service NodePorts (80/443) routed via the host loopback
// (127.0.0.1 or DinD docker-service alias). Dynamic entries are
// discovered lazily by portRouter.refresh on cache miss.
func buildInitialPortTable(ctx context.Context, cs clientset.Interface) (map[int]portRoute, string, error) {
	host := nodePortHost()
	hostIP, err := resolveIPv4(host)
	if err != nil {
		return nil, "", fmt.Errorf("resolve loopback host %q: %w", host, err)
	}
	out := map[int]portRoute{
		80:  {nodeIP: hostIP, nodePort: httpNodePort},
		443: {nodeIP: hostIP, nodePort: httpsNodePort},
	}

	// The dynamic NodePorts allocated by the apiserver for the chart's
	// gateway-listener-ports Service are reachable via the kind node's
	// docker-network InternalIP (e.g. 172.19.0.2:30808), not via the
	// host loopback because they aren't in kind extraPortMappings.
	// Discover the node IP once at suite-init; refresh() reuses it.
	nodeIP, err := discoverNodeInternalIP(ctx, cs)
	if err != nil {
		return out, "", err
	}
	return out, nodeIP, nil
}

// discoverNodeInternalIP returns the first InternalIP it finds across
// the cluster's Node resources. Any single node works for NodePort
// traffic — kube-proxy load-balances to the right pod regardless.
func discoverNodeInternalIP(ctx context.Context, cs clientset.Interface) (string, error) {
	nodes, err := cs.CoreV1().Nodes().List(ctx, metav1.ListOptions{})
	if err != nil {
		return "", fmt.Errorf("list nodes for NodePort lookup: %w", err)
	}
	for _, n := range nodes.Items {
		for _, addr := range n.Status.Addresses {
			if addr.Type == corev1.NodeInternalIP {
				return addr.Address, nil
			}
		}
	}
	return "", fmt.Errorf("no node InternalIP found")
}

// discoverDynamicNodePorts queries the chart-emitted HAProxy Service and
// returns a map from listener port to apiserver-allocated NodePort,
// skipping the entries already covered by the static seed (80, 443, 8404).
// The chart's gateway-listener-ports snippet partial-patches this Service
// to add `gw-<port>-<proto>` entries; the apiserver allocates a NodePort
// per entry (since the Service type is NodePort or LoadBalancer in the
// test environment) and we read them back here.
//
// Returns an empty map (not an error) if no dynamic entries exist; the
// snippet only contributes entries when at least one Gateway/ListenerSet
// declares a non-default listener port.
func discoverDynamicNodePorts(ctx context.Context, cs clientset.Interface) (map[int]int, error) {
	out := map[int]int{}
	svc, err := cs.CoreV1().Services(haproxyServiceNamespace).Get(ctx, haproxyServiceName, metav1.GetOptions{})
	if err != nil {
		return nil, fmt.Errorf("get %s/%s Service: %w", haproxyServiceNamespace, haproxyServiceName, err)
	}
	if svc.Spec.Type != corev1.ServiceTypeNodePort && svc.Spec.Type != corev1.ServiceTypeLoadBalancer {
		return out, nil
	}
	// Static entries seeded separately by buildInitialPortTable; skip
	// them here so the dynamic refresh path doesn't overwrite the
	// loopback host with the kind node IP.
	staticPorts := map[int32]bool{80: true, 443: true, 8404: true}
	for _, p := range svc.Spec.Ports {
		if p.NodePort == 0 || staticPorts[p.Port] {
			continue
		}
		out[int(p.Port)] = int(p.NodePort)
	}
	return out, nil
}

// discoverPerGatewayNodePorts queries the controller-namespace Services
// labelled `gateway.networking.k8s.io/gateway-name` (per-Gateway LB
// Services emitted by phase 3 of the per-Gateway-IP refactor) and
// returns a map from realized LB IP to (listener-port → NodePort).
//
// The chart's `features-090-gateway-per-gateway-services` snippet emits
// one Service per HTTPS Gateway. MetalLB allocates an LB IP per
// Service (visible in `status.loadBalancer.ingress[].ip`); the
// apiserver allocates a NodePort per port entry. The roundtripper
// uses this map so a dial to the Gateway's status.addresses IP lands
// on the Gateway's specific bind-line SSL config rather than the
// shared chart-static one.
//
// Returns an empty map (not an error) when no per-Gateway Services
// exist — the chart only emits them for HTTPS Gateways without
// `spec.addresses`.
func discoverPerGatewayNodePorts(ctx context.Context, cs clientset.Interface) (map[string]map[int]int, error) {
	out := map[string]map[int]int{}
	svcs, err := cs.CoreV1().Services(haproxyServiceNamespace).List(ctx, metav1.ListOptions{
		LabelSelector: "gateway.networking.k8s.io/gateway-name",
	})
	if err != nil {
		return nil, fmt.Errorf("list per-Gateway Services in %s: %w", haproxyServiceNamespace, err)
	}
	for _, svc := range svcs.Items {
		if svc.Spec.Type != corev1.ServiceTypeNodePort && svc.Spec.Type != corev1.ServiceTypeLoadBalancer {
			continue
		}
		// Skip Services whose IP isn't realized yet — without an LB
		// IP we have nothing to key by, and the chart-static
		// fallback handles the transitional state.
		var lbIPs []string
		for _, ing := range svc.Status.LoadBalancer.Ingress {
			if ing.IP != "" {
				lbIPs = append(lbIPs, ing.IP)
			}
		}
		if len(lbIPs) == 0 {
			continue
		}
		ports := map[int]int{}
		for _, p := range svc.Spec.Ports {
			if p.NodePort == 0 {
				continue
			}
			ports[int(p.Port)] = int(p.NodePort)
		}
		if len(ports) == 0 {
			continue
		}
		for _, ip := range lbIPs {
			out[ip] = ports
		}
	}
	return out, nil
}

// resolveIPv4 returns the first IPv4 address the resolver reports for
// host, or an error if none.
func resolveIPv4(host string) (string, error) {
	addrs, err := net.LookupIP(host)
	if err != nil {
		return "", err
	}
	for _, a := range addrs {
		if v4 := a.To4(); v4 != nil {
			return v4.String(), nil
		}
	}
	return "", fmt.Errorf("no IPv4 address for %q (got %v)", host, addrs)
}

// nodePortHost returns the hostname the test process should target for
// kind's static extraPortMappings: the docker-service alias in DinD, or
// 127.0.0.1 locally.
func nodePortHost() string {
	if kindutil.IsDockerInDocker() {
		return kindutil.GetDindHostname()
	}
	return "127.0.0.1"
}
