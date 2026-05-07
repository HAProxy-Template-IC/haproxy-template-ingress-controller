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
	"time"

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
const (
	httpNodePort  = 31080
	httpsNodePort = 31443
)

// newNodePortRoundTripper wraps roundtripper.DefaultRoundTripper with a
// CustomDialContext that ignores the conformance suite's URL host (the
// Gateway.Status address — a metallb LoadBalancer IP unreachable from the
// test process) and dials the kind cluster's NodePort instead. The HTTP
// Host header and the TLS SNI are preserved untouched, so HAProxy still
// performs hostname-based routing and certificate selection correctly.
func newNodePortRoundTripper(timeoutCfg config.TimeoutConfig, debug bool) (roundtripper.RoundTripper, error) {
	host := nodePortHost()
	addrs, err := net.LookupIP(host)
	if err != nil {
		return nil, fmt.Errorf("resolve NodePort host %q: %w", host, err)
	}
	var nodeIP string
	for _, a := range addrs {
		if v4 := a.To4(); v4 != nil {
			nodeIP = v4.String()
			break
		}
	}
	if nodeIP == "" {
		return nil, fmt.Errorf("no IPv4 address for NodePort host %q (got %v)", host, addrs)
	}

	dialer := &net.Dialer{Timeout: 10 * time.Second, KeepAlive: 30 * time.Second}
	return &roundtripper.DefaultRoundTripper{
		Debug:         debug,
		TimeoutConfig: timeoutCfg,
		CustomDialContext: func(ctx context.Context, network, address string) (net.Conn, error) {
			port, err := dialPortForAddress(address)
			if err != nil {
				return nil, err
			}
			return dialer.DialContext(ctx, network, net.JoinHostPort(nodeIP, strconv.Itoa(port)))
		},
	}, nil
}

// dialPortForAddress maps the conformance suite's intended dial port
// to the matching NodePort. Anything :80 → http NodePort, anything :443
// → https NodePort. Unrecognised ports return an error rather than silently
// routing to the wrong listener.
func dialPortForAddress(address string) (int, error) {
	_, p, err := net.SplitHostPort(address)
	if err != nil {
		return 0, fmt.Errorf("parse address %q: %w", address, err)
	}
	switch p {
	case "80", "":
		return httpNodePort, nil
	case "443":
		return httpsNodePort, nil
	default:
		return 0, fmt.Errorf("unexpected port %q in conformance dial target %q (only 80 and 443 are routed)", p, address)
	}
}

// nodePortHost returns the hostname the test process should target for
// NodePort traffic: the docker-service alias in DinD, or 127.0.0.1 locally.
func nodePortHost() string {
	if kindutil.IsDockerInDocker() {
		return kindutil.GetDindHostname()
	}
	return "127.0.0.1"
}
