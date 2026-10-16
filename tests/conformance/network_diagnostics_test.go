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

//go:build !gateway_conformance

package conformance

import (
	"context"
	"crypto/tls"
	"errors"
	"io"
	"net"
	"net/netip"
	"net/url"
	"os"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

type timeoutError struct{}

func (timeoutError) Error() string   { return "deadline" }
func (timeoutError) Timeout() bool   { return true }
func (timeoutError) Temporary() bool { return true }

func dialError(op string, err error) error {
	return &url.Error{Op: "Get", URL: "http://172.18.255.208/", Err: &net.OpError{Op: op, Net: "tcp", Err: err}}
}

func TestIsNetworkLayerError(t *testing.T) {
	for _, tc := range []struct {
		name string
		err  error
		want bool
	}{
		{name: "no route to host", err: dialError("dial", os.NewSyscallError("connect", syscall.EHOSTUNREACH)), want: true},
		{name: "network unreachable", err: dialError("dial", os.NewSyscallError("connect", syscall.ENETUNREACH)), want: true},
		{name: "connection refused", err: dialError("dial", os.NewSyscallError("connect", syscall.ECONNREFUSED)), want: true},
		{name: "dial timeout", err: dialError("dial", timeoutError{}), want: true},
		{name: "flattened message", err: errors.New("Get \"http://x/\": dial tcp 172.18.255.208:80: connect: no route to host"), want: true},
		{name: "flattened i/o timeout", err: errors.New("dial tcp 172.18.255.208:80: i/o timeout"), want: true},
		{name: "read timeout after connect", err: dialError("read", timeoutError{})},
		{name: "tls rejection", err: &url.Error{Op: "Get", URL: "https://x/", Err: tls.AlertError(40)}},
		{name: "peer closed", err: dialError("read", io.EOF)},
		{name: "canceled", err: context.Canceled},
		{name: "nil", err: nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.want, isNetworkLayerError(tc.err))
		})
	}
}

func TestDialedIP(t *testing.T) {
	for _, tc := range []struct {
		host string
		want string
	}{
		{host: "172.18.255.208:80", want: "172.18.255.208"},
		{host: "172.18.255.208", want: "172.18.255.208"},
		{host: "[fc00:f853:ccd:e793::64]:443", want: "fc00:f853:ccd:e793::64"},
		{host: "[::ffff:172.18.255.208]:80", want: "172.18.255.208"},
		{host: "gateway.example.com:80"},
		{host: ""},
	} {
		t.Run(tc.host, func(t *testing.T) {
			ip, ok := dialedIP(tc.host)
			assert.Equal(t, tc.want != "", ok)
			if ok {
				assert.Equal(t, tc.want, ip.String())
			}
		})
	}
}

func loadBalancerService(name string, ips ...string) corev1.Service {
	service := corev1.Service{ObjectMeta: metav1.ObjectMeta{Namespace: "gateway-conformance-infra", Name: name}}
	for _, ip := range ips {
		service.Status.LoadBalancer.Ingress = append(service.Status.LoadBalancer.Ingress, corev1.LoadBalancerIngress{IP: ip})
	}
	return service
}

func TestServicesAnnouncingSelectsExactIngressIP(t *testing.T) {
	services := []corev1.Service{
		loadBalancerService("prefix-collision", "172.18.255.20"),
		loadBalancerService("pending"),
		loadBalancerService("dual-stack", "fc00::1", "172.18.255.208"),
		loadBalancerService("hostname-only"),
	}
	services[3].Status.LoadBalancer.Ingress = []corev1.LoadBalancerIngress{{Hostname: "lb.example.com"}}

	selected := servicesAnnouncing(services, netip.MustParseAddr("172.18.255.208"))

	require.Len(t, selected, 1)
	assert.Equal(t, "dual-stack", selected[0].Name)
}

func TestGatewaysWithAddressSelectsStatusAddress(t *testing.T) {
	gateway := func(name string, addresses ...any) unstructured.Unstructured {
		object := map[string]any{"metadata": map[string]any{"name": name}}
		if addresses != nil {
			object["status"] = map[string]any{"addresses": addresses}
		}
		return unstructured.Unstructured{Object: object}
	}
	gateways := []unstructured.Unstructured{
		gateway("unprogrammed"),
		gateway("other", map[string]any{"type": "IPAddress", "value": "172.18.255.209"}),
		gateway("malformed", "172.18.255.208"),
		gateway("match", map[string]any{"type": "IPAddress", "value": "172.18.255.208"}),
	}

	selected := gatewaysWithAddress(gateways, netip.MustParseAddr("172.18.255.208"))

	require.Len(t, selected, 1)
	assert.Equal(t, "match", selected[0].GetName())
}

func TestNodeNetworkScriptMatchesOnlyTheDialedIP(t *testing.T) {
	script := nodeNetworkScript(netip.MustParseAddr("172.18.255.20"))

	assert.Contains(t, script, `grep -E -- '172\.18\.255\.20([^0-9a-fA-F.:]|$)'`)
	assert.Contains(t, script, "conntrack -L -d 172.18.255.20 ")
	assert.Contains(t, script, "cat /proc/net/arp")
	assert.NotContains(t, script, "kubectl")
}

func TestTruncateCaptureBoundsPayload(t *testing.T) {
	assert.Equal(t, "short", truncateCapture([]byte("short"), 10))

	truncated := truncateCapture([]byte(strings.Repeat("x", 100)), 10)

	assert.True(t, strings.HasPrefix(truncated, strings.Repeat("x", 10)+"\n# truncated: 10 of 100 bytes kept"))
}

func TestNetworkCaptureGateSkipsFreshVIPWindowAndBoundsCaptures(t *testing.T) {
	gate := newNetworkCaptureGate()
	start := time.Unix(0, 0)
	at := start.Add

	assert.Zero(t, gate.admit("a", at(0)), "first failure starts the window")
	assert.Zero(t, gate.admit("a", at(3*time.Second)), "fresh VIPs are unreachable for 1-3 s")
	assert.Equal(t, 1, gate.admit("a", at(6*time.Second)))
	assert.Zero(t, gate.admit("a", at(9*time.Second)), "captures are spaced")
	assert.Zero(t, gate.admit("b", at(9*time.Second)), "each target has its own window")
	assert.Equal(t, 2, gate.admit("a", at(26*time.Second)))
	assert.Equal(t, 3, gate.admit("a", at(46*time.Second)))
	assert.Zero(t, gate.admit("a", at(187*time.Second)), "captures per target are capped")
	assert.Equal(t, 1, gate.admit("b", at(15*time.Second)))
}
