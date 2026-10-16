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

package conformance

import (
	"errors"
	"fmt"
	"net"
	"net/netip"
	"regexp"
	"strings"
	"syscall"
	"time"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

var networkErrnos = []syscall.Errno{syscall.EHOSTUNREACH, syscall.ENETUNREACH, syscall.ECONNREFUSED}

var networkErrorTexts = []string{"no route to host", "network is unreachable", "connection refused", "i/o timeout"}

// isNetworkLayerError reports whether a round trip failed before an HTTP exchange with HAProxy began.
func isNetworkLayerError(err error) bool {
	if err == nil {
		return false
	}
	for _, errno := range networkErrnos {
		if errors.Is(err, errno) {
			return true
		}
	}
	var opErr *net.OpError
	if errors.As(err, &opErr) {
		return opErr.Op == "dial" && opErr.Timeout()
	}
	// Only a chain flattened to text by %v reaches here.
	message := err.Error()
	for _, text := range networkErrorTexts {
		if strings.Contains(message, text) {
			return true
		}
	}
	return false
}

func dialedIP(urlHost string) (netip.Addr, bool) {
	host, _, err := net.SplitHostPort(urlHost)
	if err != nil {
		host = strings.Trim(urlHost, "[]")
	}
	addr, err := netip.ParseAddr(host)
	if err != nil {
		return netip.Addr{}, false
	}
	return addr.Unmap(), true
}

func servicesAnnouncing(services []corev1.Service, ip netip.Addr) []corev1.Service {
	var selected []corev1.Service
	for i := range services {
		for _, ingress := range services[i].Status.LoadBalancer.Ingress {
			if addr, err := netip.ParseAddr(ingress.IP); err == nil && addr.Unmap() == ip {
				selected = append(selected, services[i])
				break
			}
		}
	}
	return selected
}

func gatewaysWithAddress(gateways []unstructured.Unstructured, ip netip.Addr) []unstructured.Unstructured {
	var selected []unstructured.Unstructured
	for i := range gateways {
		addresses, _, _ := unstructured.NestedSlice(gateways[i].Object, "status", "addresses")
		for _, entry := range addresses {
			address, _ := entry.(map[string]any)
			value, _ := address["value"].(string)
			if addr, err := netip.ParseAddr(value); err == nil && addr.Unmap() == ip {
				selected = append(selected, gateways[i])
				break
			}
		}
	}
	return selected
}

// nodeNetworkScript runs in a kube-proxy pod (hostNetwork), so it reads the node's netns.
func nodeNetworkScript(ip netip.Addr) string {
	pattern := regexp.QuoteMeta(ip.String()) + "([^0-9a-fA-F.:]|$)"
	return fmt.Sprintf(`echo '# iptables-save'; iptables-save 2>&1 | grep -E -- '%[1]s'
echo '# nft list ruleset'; nft list ruleset 2>&1 | grep -E -- '%[1]s'
echo '# conntrack -L -d %[2]s'; conntrack -L -d %[2]s 2>&1
echo '# /proc/net/arp'; cat /proc/net/arp`, pattern, ip.String())
}

func truncateCapture(data []byte, limit int) string {
	if len(data) <= limit {
		return string(data)
	}
	return string(data[:limit]) + fmt.Sprintf("\n# truncated: %d of %d bytes kept\n", limit, len(data))
}

// networkCaptureGate skips the 1-3 s of unreachability every fresh VIP shows and bounds captures per target.
type networkCaptureGate struct {
	persistFor  time.Duration
	spacing     time.Duration
	maxCaptures int

	firstFailure map[string]time.Time
	lastCapture  map[string]time.Time
	captures     map[string]int
}

func newNetworkCaptureGate() *networkCaptureGate {
	return &networkCaptureGate{
		persistFor:   5 * time.Second,
		spacing:      20 * time.Second,
		maxCaptures:  3,
		firstFailure: map[string]time.Time{},
		lastCapture:  map[string]time.Time{},
		captures:     map[string]int{},
	}
}

// admit returns the 1-based capture number, or 0 when this failure is not captured.
func (g *networkCaptureGate) admit(key string, now time.Time) int {
	first, seen := g.firstFailure[key]
	if !seen {
		g.firstFailure[key] = now
		return 0
	}
	if now.Sub(first) < g.persistFor || g.captures[key] >= g.maxCaptures {
		return 0
	}
	if last, ok := g.lastCapture[key]; ok && now.Sub(last) < g.spacing {
		return 0
	}
	g.lastCapture[key] = now
	g.captures[key]++
	return g.captures[key]
}
