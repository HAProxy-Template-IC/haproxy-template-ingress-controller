// Copyright 2026 Philipp Hossner
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
// http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package kindutil

import (
	"fmt"
	"strings"
)

// SyntheticBackendRanges are the address ranges test fixtures use for backends
// that must not exist: pilot-load's simulated pods (10.0.0.10 upward, no knob)
// and the RFC 5737 test networks. Nothing in a kind cluster lives in them
// (services 10.96.0.0/16, pods 10.244.0.0/16).
//
// The same list is in scripts/lib/cluster.sh for the shell-created clusters.
var SyntheticBackendRanges = []string{"10.0.0.0/12", "192.0.2.0/24", "198.51.100.0/24", "203.0.113.0/24"}

// BlackholeSyntheticBackends makes SyntheticBackendRanges unreachable inside the
// cluster's node, so HAProxy's health checks to synthetic backends fail on the
// node instead of leaving it. Without this every check is a SYN that misses
// every cluster route, is NAT'd to the host and forwarded to the LAN's default
// gateway: 126,000 concurrent connections at 5,000 routes, enough to take a
// home router down.
func BlackholeSyntheticBackends(clusterName string) error {
	node := clusterName + "-control-plane"
	for _, cidr := range SyntheticBackendRanges {
		out, err := RunDocker(nil, "exec", node, "ip", "route", "replace", "blackhole", cidr)
		if err != nil {
			return fmt.Errorf("blackhole %s on kind node %s: %w (%s)", cidr, node, err, strings.TrimSpace(out))
		}
	}
	return nil
}
