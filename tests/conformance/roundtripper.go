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
	"errors"
	"fmt"
	"net"
	"os"
	"strconv"
	"sync"
	"syscall"
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
//  1. Per-LB-IP exact match: if the host matches a known LB IP
//     (chart's main Service OR a per-Gateway Service), return its
//     specific NodePort for `port`. Crucially, when the host IS a
//     known LB IP but the port isn't bound on that Service, return
//     "no route" instead of falling through to the chart-static
//     port table — that's what makes mTLS-blocked / phase-4-skipped
//     listeners actually unreachable (the
//     GatewayFrontendInvalidDefaultClientCertificateValidation
//     test depends on the dial returning err != nil).
//  2. Port-only fallback: when the host is unknown (no LB IP match,
//     e.g. test infrastructure dials the kind node IP directly),
//     use the chart-static `table` to find a NodePort by listener
//     port alone. Refreshes once on cache miss.
//
// Refreshes the byLBIP / table caches lazily on the first cache
// miss for a given burst of dials.
func (r *portRouter) lookup(ctx context.Context, host string, port int) (portRoute, bool) {
	r.mu.RLock()
	perPort, hostKnown := r.byLBIP[host]
	r.mu.RUnlock()

	// Host with non-empty IP but not yet in byLBIP: refresh the
	// cache and re-check. Newly-realized per-Gateway LB IPs land
	// here on first dial — without the refresh the cache would
	// return "host unknown" and we'd fall through to the chart-
	// static port table, which would dial the wrong HAProxy bind.
	if !hostKnown && host != "" {
		r.refresh(ctx)
		r.mu.RLock()
		perPort, hostKnown = r.byLBIP[host]
		r.mu.RUnlock()
	}

	if hostKnown {
		if np, ok := perPort[port]; ok {
			return portRoute{nodeIP: r.hostIP, nodePort: np}, true
		}
		// Host is a known LB IP but doesn't expose this port — the
		// cached byLBIP snapshot may predate the chart adding a
		// non-default Gateway listener port to the main Service
		// (HTTPRouteListenerPortMatching: Gateway listener-2 on
		// port 8080 is observed by the chart, port 8080 gets
		// appended to haptic-haproxy.spec.ports with its own
		// NodePort, but our cache is from before the apply).
		// Refresh and re-check before giving up. We still return
		// false on the second miss instead of falling through to
		// `table`, because the chart-static port table would dial
		// the wrong HAProxy bind for per-Gateway flows (the
		// GatewayFrontendInvalidDefaultClientCertificateValidation
		// test relies on the connection-refused signal from phase 4).
		r.refresh(ctx)
		r.mu.RLock()
		perPort = r.byLBIP[host]
		r.mu.RUnlock()
		if np, ok := perPort[port]; ok {
			return portRoute{nodeIP: r.hostIP, nodePort: np}, true
		}
		return portRoute{}, false
	}

	// Host unknown even after refresh — fall back to the chart-
	// static `table` lookup by port alone. Hits when the test
	// process dials the kind node IP directly (e.g. dynamic
	// listener ports without a per-Gateway Service).
	r.mu.RLock()
	defer r.mu.RUnlock()
	route, ok := r.table[port]
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
			conn, dialErr := dialer.DialContext(ctx, network, net.JoinHostPort(route.nodeIP, strconv.Itoa(route.nodePort)))
			if dialErr == nil {
				return conn, nil
			}
			// connection-refused on a previously-cached NodePort means
			// the apiserver re-allocated it: when a Gateway-listener
			// port disappears from the chart's Service (because its
			// Gateway was deleted) and later reappears (because a new
			// Gateway claims the same listener port), Kubernetes
			// releases the old NodePort and picks a fresh random one
			// from the NodePort range. The byLBIP cache holds the
			// stale NodePort number; the host is still known so
			// portRouter.lookup happily returns it. Refresh the cache
			// and retry once before bubbling the dial error up to
			// the framework's retry loop. We only do this on
			// connection-refused (and similar "socket gone") errors,
			// not on context deadlines or TLS handshake failures,
			// so a genuinely-broken backend still surfaces fast.
			if !isStaleNodePortError(dialErr) {
				return nil, dialErr
			}
			router.refresh(ctx)
			route2, err := dialPortForAddress(ctx, address, router)
			if err != nil {
				return nil, dialErr
			}
			return dialer.DialContext(ctx, network, net.JoinHostPort(route2.nodeIP, strconv.Itoa(route2.nodePort)))
		},
	}, nil
}

// isStaleNodePortError reports whether the dial error looks like
// "the NodePort I cached doesn't exist anymore" — typically a TCP
// RST or ECONNREFUSED. Net errors that wrap syscall errno are the
// signal; context deadlines and TLS errors are not.
func isStaleNodePortError(err error) bool {
	if err == nil {
		return false
	}
	var opErr *net.OpError
	if errors.As(err, &opErr) {
		var sysErr *os.SyscallError
		if errors.As(opErr.Err, &sysErr) {
			return errors.Is(sysErr.Err, syscall.ECONNREFUSED) || errors.Is(sysErr.Err, syscall.ECONNRESET)
		}
	}
	return false
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
	// docker-network InternalIP (e.g. 172.19.0.2:30808) — but only
	// from contexts that share that docker network. In a flat local
	// setup the kind container's IP is routable from the test
	// process. In Docker-in-Docker (GitLab CI), the kind node lives
	// inside the DinD container's docker daemon; the kind node IP
	// isn't reachable from the outer job container, which is where
	// the test process actually runs. The DinD hostname is — every
	// NodePort opened on a kind node is also reachable on the DinD
	// container's IP at the same port number (kind binds NodePorts
	// to 0.0.0.0 inside the DinD container's host namespace). So in
	// DinD, route ALL dynamic NodePorts through the DinD hostname
	// IP, matching the static 80/443 entries above. Failing to do
	// this is what causes the conformance shards in CI to hit
	// `dial tcp <kind-internal-ip>:<NodePort>: i/o timeout` on
	// every per-Gateway lookup.
	if kindutil.IsDockerInDocker() {
		return out, hostIP, nil
	}
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
		out[int(p.Port)] = remapNodePortForDinD(int(p.NodePort))
	}
	return out, nil
}

// discoverPerGatewayNodePorts queries the controller-namespace
// LoadBalancer Services and returns a map from realized LB IP to
// (listener-port → NodePort). Populates two layers:
//
//   - The chart's main `haptic-haproxy` Service. Its LB IP is the
//     destination for HTTP-only Gateways (which don't get their
//     own per-Gateway Service) and for pinned-IP Gateways before
//     phase 3 took over. Without including it, the byLBIP path
//     would fall through to the port-only `table` for those dials,
//     which is what the chart-static-fallback was designed for —
//     but the InvalidDefault conformance test specifically expects
//     a connection refusal when its per-Gateway bind doesn't exist,
//     so leaving the chart-static fallback in for ALL unknown LB
//     IPs masks that signal. Better to make every realized LB IP
//     resolvable directly.
//
//   - Per-Gateway Services emitted by phase 3, labelled
//     `gateway.networking.k8s.io/gateway-name`. MetalLB allocates
//     a unique LB IP per Service; the apiserver allocates a
//     NodePort per port entry. A dial to the Gateway's
//     status.addresses IP lands on the Gateway's specific bind-
//     line SSL config (or RSTs at the pod boundary if phase 4
//     declined to emit a bind for an mTLS-blocked listener).
//
// Returns an empty map (not an error) when no LoadBalancer Services
// have realized IPs — phase 3 only emits per-Gateway Services for
// HTTPS Gateways without `spec.addresses`, so a chart with only
// HTTP Gateways still surfaces the main Service.
func discoverPerGatewayNodePorts(ctx context.Context, cs clientset.Interface) (map[string]map[int]int, error) {
	out := map[string]map[int]int{}

	collect := func(svc *corev1.Service) {
		if svc.Spec.Type != corev1.ServiceTypeNodePort && svc.Spec.Type != corev1.ServiceTypeLoadBalancer {
			return
		}
		var lbIPs []string
		for _, ing := range svc.Status.LoadBalancer.Ingress {
			if ing.IP != "" {
				lbIPs = append(lbIPs, ing.IP)
			}
		}
		if len(lbIPs) == 0 {
			return
		}
		ports := map[int]int{}
		for _, p := range svc.Spec.Ports {
			if p.NodePort == 0 {
				continue
			}
			ports[int(p.Port)] = remapNodePortForDinD(int(p.NodePort))
		}
		if len(ports) == 0 {
			return
		}
		for _, ip := range lbIPs {
			out[ip] = ports
		}
	}

	mainSvc, err := cs.CoreV1().Services(haproxyServiceNamespace).Get(ctx, haproxyServiceName, metav1.GetOptions{})
	if err == nil {
		collect(mainSvc)
	}

	gwSvcs, err := cs.CoreV1().Services(haproxyServiceNamespace).List(ctx, metav1.ListOptions{
		LabelSelector: "gateway.networking.k8s.io/gateway-name",
	})
	if err != nil {
		return nil, fmt.Errorf("list per-Gateway Services in %s: %w", haproxyServiceNamespace, err)
	}
	for i := range gwSvcs.Items {
		collect(&gwSvcs.Items[i])
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

// e2eKindNodePortRange* / e2eKindNodePortHostShift mirror the kind
// extraPortMapping range declared in tests/e2e/main_test.go. The
// kube-apiserver's `--service-node-port-range` is narrowed to
// [Start, End], and the kind cluster pre-declares one extraPortMapping
// per port in that range, shifting containerPort N to hostPort
// N + HostShift. So every NodePort the apiserver might allocate —
// chart-static OR per-Gateway dynamic — is reachable from outside
// DinD at the shifted host port. Keep these constants in lockstep
// with `e2eNodePortRange*` in tests/e2e/main_test.go.
const (
	e2eKindNodePortRangeStart = 30000
	e2eKindNodePortRangeEnd   = 30299
	e2eKindNodePortHostShift  = 1000
)

// remapNodePortForDinD translates a K8s-assigned NodePort to its kind
// extraPortMapping equivalent when running in DinD.
//
// In a flat local docker setup the kind node container is on the host's
// docker network, so the apiserver-assigned NodePort is reachable as
// `kindNodeIP:<nodePort>` directly. In GitLab CI's DinD, the kind node
// lives inside the DinD container's docker daemon — its docker network
// isn't routable from the outer job container. The e2e kind config
// (tests/e2e/main_test.go:e2eKindConfig) compensates with one
// extraPortMapping per port in [e2eKindNodePortRangeStart, …End],
// shifting containerPort N to hostPort N + e2eKindNodePortHostShift.
//
// Translation rule: in DinD, any NodePort inside the configured range
// is reachable at port (nodePort + HostShift) on the DinD hostname.
// NodePorts outside the range — should never happen, since the
// apiserver is constrained to the same range via service-node-port-range —
// fall through unchanged so dial errors surface loudly instead of being
// silently mismapped onto a port that does work.
//
// Outside DinD: no translation. The K8s NodePort is what kind nodes
// bind on the local docker daemon's network; that IS reachable.
func remapNodePortForDinD(nodePort int) int {
	if !kindutil.IsDockerInDocker() {
		return nodePort
	}
	if nodePort >= e2eKindNodePortRangeStart && nodePort <= e2eKindNodePortRangeEnd {
		return nodePort + e2eKindNodePortHostShift
	}
	return nodePort
}
