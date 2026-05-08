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

// gatewayPortsServiceLabel identifies the chart-managed Service that
// exposes per-Gateway-listener-port NodePorts. The chart emits one such
// Service named `haptic-gw-listener-ports` in the controller namespace
// with a port entry per unique non-default Gateway/ListenerSet listener
// port — see features-090-gateway-listener-ports-service in
// libraries/gateway.yaml. The conformance RoundTripper queries by this
// label at suite-init time to build the dynamic port→NodePort table.
const gatewayPortsServiceLabel = "haproxy-haptic.org/role=gateway-listener-ports"

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
type portRouter struct {
	cs       clientset.Interface
	hostIP   string
	mu       sync.RWMutex
	table    map[int]portRoute
	lastSync time.Time
}

func newPortRouter(cs clientset.Interface, hostIP string, initial map[int]portRoute) *portRouter {
	return &portRouter{cs: cs, hostIP: hostIP, table: initial}
}

func (r *portRouter) lookup(ctx context.Context, port int) (portRoute, bool) {
	r.mu.RLock()
	route, ok := r.table[port]
	r.mu.RUnlock()
	if ok {
		return route, true
	}
	// Cache miss: refresh the dynamic NodePort table from the cluster.
	// Fixtures applied by the conformance test framework after
	// suite-init populate the chart's gateway-listener-ports Service
	// asynchronously; this lazy refresh picks them up without
	// requiring the test to call into us first.
	r.refresh(ctx)
	r.mu.RLock()
	route, ok = r.table[port]
	r.mu.RUnlock()
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
	if err != nil {
		// Best-effort refresh; keep the existing table on error.
		return
	}
	r.lastSync = time.Now()
	for port, np := range dyn {
		r.table[port] = portRoute{nodeIP: r.hostIP, nodePort: np}
	}
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

// dialPortForAddress maps the conformance suite's intended dial port to
// the matching node IP + NodePort. Unrecognised ports return an error
// rather than silently routing to the wrong listener.
func dialPortForAddress(ctx context.Context, address string, router *portRouter) (portRoute, error) {
	_, p, err := net.SplitHostPort(address)
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
	if route, ok := router.lookup(ctx, pi); ok {
		return route, nil
	}
	return portRoute{}, fmt.Errorf("unexpected port %q in conformance dial target %q (no NodePort mapping configured)", p, address)
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

// discoverDynamicNodePorts queries the chart-emitted
// gateway-listener-ports Service and returns a map from listener port
// to apiserver-allocated NodePort. Returns an empty map (not an error)
// if the Service is absent — the chart only emits it when there are
// non-default Gateway listener ports.
func discoverDynamicNodePorts(ctx context.Context, cs clientset.Interface) (map[int]int, error) {
	out := map[int]int{}
	svcs, err := cs.CoreV1().Services("").List(ctx, metav1.ListOptions{
		LabelSelector: gatewayPortsServiceLabel,
	})
	if err != nil {
		return nil, fmt.Errorf("list listener-port Services: %w", err)
	}
	for _, svc := range svcs.Items {
		if svc.Spec.Type != corev1.ServiceTypeNodePort && svc.Spec.Type != corev1.ServiceTypeLoadBalancer {
			continue
		}
		for _, p := range svc.Spec.Ports {
			if p.NodePort == 0 {
				continue
			}
			out[int(p.Port)] = int(p.NodePort)
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
