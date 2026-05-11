//go:build gateway_conformance

// Custom gRPC client wrapper that rewrites the dial target to the kind
// extraPortMapping equivalent in Docker-in-Docker (the same fix
// `roundtripper.go`'s `remapNodePortForDinD` applies to HTTP dials).
//
// The upstream `grpc.DefaultClient` calls `grpc.NewClient(address, ...)`
// where `address` comes straight from `Gateway.status.addresses` — a
// metallb-allocated LoadBalancer IP that isn't routable from outside
// DinD. Without intervention every GRPCRoute conformance test fails
// with `transport: Error while dialing: dial tcp <LB-IP>:80: i/o
// timeout`. The HTTP RoundTripper handles the same situation via
// `CustomDialContext`; the gRPC client has no equivalent hook, so we
// wrap the upstream `DefaultClient` and rewrite the address argument
// before delegating.

package conformance

import (
	"fmt"
	"net"
	"strconv"
	"testing"
	"time"

	gatewaygrpc "sigs.k8s.io/gateway-api/conformance/utils/grpc"

	"gitlab.com/haproxy-haptic/haptic/tests/kindutil"
)

// dindRewritingGRPCClient is a `gatewaygrpc.Client` that translates
// `<LB-IP>:<port>` dial targets into `<DinD-hostname-IP>:<host-mapped-port>`
// when running inside Docker-in-Docker. Outside DinD the address is
// passed through unchanged.
//
// The translation is the LB-facing → host-extraPortMapping pair:
//
//	80   → 31080 (kind extraPortMapping for HTTP)
//	443  → 31443 (kind extraPortMapping for HTTPS)
//	8404 → 31404 (kind extraPortMapping for stats)
//
// Note: this is a DIFFERENT mapping from roundtripper.go's
// `remapNodePortForDinD`. The HTTP RoundTripper sees K8s-assigned
// NodePorts (30080, 30443, 30404) and translates those to the host
// ports. The gRPC client sees the LB-FACING port (80, 443) directly
// from the test fixture's Gateway.status.addresses URL and needs a
// different lookup. The destination's the same DinD host — just
// reached through different upstream code paths.
//
// Other ports fall through unchanged: a per-Gateway HTTPS gRPC dial
// can't be reached from outside DinD regardless, so producing a fast
// failure with the original address in the error message is more
// useful than mismapping onto a port that does work but routes
// elsewhere.
type dindRewritingGRPCClient struct {
	inner  gatewaygrpc.Client
	dindIP string
}

func newGRPCClient() (gatewaygrpc.Client, error) {
	inner := &gatewaygrpc.DefaultClient{}
	if !kindutil.IsDockerInDocker() {
		return inner, nil
	}
	dindIP, err := resolveIPv4(kindutil.GetDindHostname())
	if err != nil {
		return nil, fmt.Errorf("resolve DinD hostname for gRPC client: %w", err)
	}
	return &dindRewritingGRPCClient{inner: inner, dindIP: dindIP}, nil
}

func (c *dindRewritingGRPCClient) SendRPC(t *testing.T, address string, expected gatewaygrpc.ExpectedResponse, timeout time.Duration) (*gatewaygrpc.Response, error) {
	t.Helper()
	return c.inner.SendRPC(t, c.rewrite(address), expected, timeout)
}

func (c *dindRewritingGRPCClient) Close() { c.inner.Close() }

// rewrite translates a `<host>:<port>` target onto its DinD-reachable
// equivalent. Falls back to the original address on parse errors so
// the test framework's existing error path still surfaces the dial
// failure with the original context.
func (c *dindRewritingGRPCClient) rewrite(address string) string {
	host, portStr, err := net.SplitHostPort(address)
	if err != nil {
		// Plain `<host>` (no port) — assume the conformance suite
		// will append a default, can't translate. Pass through.
		return address
	}
	port, err := strconv.Atoi(portStr)
	if err != nil {
		return address
	}
	mapped := mapLBPortToHostPort(port)
	if mapped == 0 {
		// No translation available for this port. The dial will fail
		// regardless; let the upstream client surface the canonical
		// error against the unmodified address.
		return address
	}
	_ = host // address rewriting drops the LB IP intentionally — the
	// kind extraPortMapping reaches the chart's HAProxy frontend on
	// the DinD host, and the dial target identity carries no further
	// meaning (HAProxy routes by Host header / SNI / path, not by
	// destination IP).
	return net.JoinHostPort(c.dindIP, strconv.Itoa(mapped))
}

// mapLBPortToHostPort returns the kind-extraPortMapping host port for
// a chart-static LB port, or 0 when no mapping exists. The mapping
// pairs are pinned in the e2e kind config (tests/e2e/main_test.go:
// e2eKindConfig).
func mapLBPortToHostPort(lbPort int) int {
	switch lbPort {
	case 80:
		return 31080
	case 443:
		return 31443
	case 8404:
		return 31404
	}
	return 0
}
