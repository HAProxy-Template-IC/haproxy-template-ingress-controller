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

// Package httpclient is the fluent HTTP/HTTPS/mTLS client used by the
// full-stack e2e suite — typed, retrying, and DinD-aware.
//
// Design goals:
//   - Condition-based waits: every assertion polls a real predicate under
//     exponential backoff. No time.Sleep.
//   - DinD-aware: routes through the kind NodePort using the docker host
//     when DOCKER_HOST is set; localhost otherwise.
//   - SNI-correct: HTTPS requests preserve the user-supplied hostname for
//     SNI while dialing the kind NodePort IP, equivalent to curl --resolve.
//   - Typed echo-server response: tests assert on Echo.Headers["x-auth-user"]
//     instead of grep-matching JSON.
package httpclient

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"net"
	"net/http"
	"strconv"
	"strings"
	"testing"
	"time"

	"gitlab.com/haproxy-haptic/haptic/tests/kindutil"
	"gitlab.com/haproxy-haptic/haptic/tests/testutil"
)

// Default ports are the *host-side* ports the e2e kind cluster exposes
// (via extraPortMappings). Distinct from the dev cluster's 30080/30443 so
// the two clusters can coexist; see tests/e2e/constants.go HTTPHostPort.
const (
	defaultHTTPPort  = 31080
	defaultHTTPSPort = 31443
)

// Client is a fluent HTTP client targeting the dev-env HAProxy NodePorts.
// One Client may serve many Requests; concurrent use is safe.
type Client struct {
	// nodeIP is the resolved NodePort IP (IPv4-preferred). In DinD this is
	// the docker hostname's IPv4 address; locally it's 127.0.0.1.
	nodeIP string

	// httpPort/httpsPort default to 30080/30443 but are overridable for
	// alternative chart layouts.
	httpPort  int
	httpsPort int

	// waitCfg is the retry/backoff policy applied to Expect* sinks.
	waitCfg testutil.WaitConfig

	// transport is shared across all non-mTLS requests for connection pooling.
	transport *http.Transport

	// onPollTimeout, if non-nil, is invoked when poll() exhausts its
	// retry budget — BEFORE the timeout error propagates up to t.Fatalf
	// and the test's t.Cleanup chain. Used by tests/e2e to snapshot
	// the chart's rendered HAProxyCfg + the running pod's
	// /etc/haproxy tree while the test's fixtures are still alive
	// (the standard DumpLogsOnFailure runs in t.Cleanup after fixture
	// deletion, so its haproxycfg.yaml capture sees post-cleanup
	// state instead of the failing-moment state).
	//
	// Package-private to httpclient: tests/e2e sets it via
	// SetDefaultPollTimeoutSnapshot during TestMain init so every
	// Client returned by New picks it up automatically.
	onPollTimeout PollTimeoutSnapshot
}

// PollTimeoutSnapshot is the callback invoked when a poll exhausts
// its retry budget. Implementations receive the test handle, a
// human-readable description of what was being polled, the last
// response observed (may be nil if every attempt errored), and the
// last error returned by the inner Do (may be nil if responses came
// back but the predicate never matched).
//
// Implementations MUST be best-effort and side-effect-only: they
// cannot influence the test outcome (the timeout error still
// propagates) and they must not call t.FailNow / t.Fatalf
// themselves, which would short-circuit the existing diagnostic
// chain.
type PollTimeoutSnapshot func(t *testing.T, description string, lastResp *Response, lastErr error)

// defaultPollTimeoutSnapshot is the package-default callback used
// when New constructs a Client with no per-instance override. The
// e2e test harness's TestMain registers a callback that dumps
// HAProxy state to debug-logs/<test>/; httpclient itself only
// stores the function pointer.
var defaultPollTimeoutSnapshot PollTimeoutSnapshot

// SetDefaultPollTimeoutSnapshot registers the callback that
// newly-constructed Clients pick up by default. Pass nil to
// disable. Intended to be called once from TestMain in the
// tests/e2e package; safe to call multiple times (last write wins).
//
// Implemented as a package-level setter rather than a Client option
// so existing tests that call httpclient.New(t) directly get the
// snapshot behaviour automatically — no per-test wiring change.
func SetDefaultPollTimeoutSnapshot(fn PollTimeoutSnapshot) {
	defaultPollTimeoutSnapshot = fn
}

// New constructs a Client targeting the running dev environment.
//
// Resolution order for the NodePort host:
//  1. If DOCKER_HOST is set to tcp://..., use that hostname's IPv4 address.
//     Kind's extraPortMappings only listen on IPv4 (listenAddress: "0.0.0.0"),
//     so we cannot use IPv6 even when the docker hostname has an AAAA record.
//  2. Otherwise, use 127.0.0.1.
//
// Calls t.Fatalf if the NodePort cannot be reached at all (resolution
// failure). This is a setup error, not a test failure.
func New(t *testing.T) *Client {
	t.Helper()
	nodeIP, err := resolveNodeIP()
	if err != nil {
		t.Fatalf("httpclient: resolve NodePort host: %v", err)
	}
	t.Logf("httpclient: NodePort host = %s", nodeIP)

	return &Client{
		nodeIP:    nodeIP,
		httpPort:  defaultHTTPPort,
		httpsPort: defaultHTTPSPort,
		waitCfg: testutil.WaitConfig{
			InitialInterval: 100 * time.Millisecond,
			MaxInterval:     2 * time.Second,
			// 15s cap. haptic must apply a routing change (reconcile -> render
			// -> validate -> deploy -> reload) and HAProxy must serve it well
			// within 10s — even under the full parallel suite's churn. Backend
			// pod readiness is gated separately (waitForServiceEndpointReady,
			// before any probe runs), so this budget bounds ONLY haptic's own
			// reaction. A probe that needs >15s is a convergence regression to
			// surface, not a tail to absorb behind a generous ceiling. Tests
			// probing a deliberately transient condition (e.g. rate-limit
			// windows) opt into a longer budget via WithRetryBudget.
			Timeout:    15 * time.Second,
			Multiplier: 2.0,
		},
		transport:     newSharedTransport(nodeIP, defaultHTTPSPort),
		onPollTimeout: defaultPollTimeoutSnapshot,
	}
}

// WithHTTPPort overrides the default HTTP NodePort.
func (c *Client) WithHTTPPort(port int) *Client { c.httpPort = port; return c }

// WithHTTPSPort overrides the default HTTPS NodePort. The shared transport
// is rebuilt so the SNI-rewriting DialContext targets the new port.
func (c *Client) WithHTTPSPort(port int) *Client {
	c.httpsPort = port
	c.transport = newSharedTransport(c.nodeIP, port)
	return c
}

// WithRetryBudget overrides the default total wait budget. Useful for tests
// that intentionally probe a transient condition (e.g. rate-limit windows).
func (c *Client) WithRetryBudget(budget time.Duration) *Client {
	c.waitCfg.Timeout = budget
	return c
}

// NodeIP returns the resolved NodePort IP. Exposed for tests that need to
// build raw connections (e.g., proxy-protocol probes).
func (c *Client) NodeIP() string { return c.nodeIP }

// HTTPPort returns the HTTP NodePort.
func (c *Client) HTTPPort() int { return c.httpPort }

// HTTPSPort returns the HTTPS NodePort.
func (c *Client) HTTPSPort() int { return c.httpsPort }

// resolveNodeIP returns an IPv4 NodePort IP, or 127.0.0.1 outside DinD.
func resolveNodeIP() (string, error) {
	if !kindutil.IsDockerInDocker() {
		return "127.0.0.1", nil
	}
	host := kindutil.GetDindHostname()
	addrs, err := net.LookupIP(host)
	if err != nil {
		return "", fmt.Errorf("lookup %q: %w", host, err)
	}
	for _, a := range addrs {
		if v4 := a.To4(); v4 != nil {
			return v4.String(), nil
		}
	}
	return "", fmt.Errorf("no IPv4 address for %q (got %v)", host, addrs)
}

// newSharedTransport returns an *http.Transport whose DialContext rewrites
// any "<host>:443" target to the NodePort. This is the curl --resolve
// equivalent: the wire connection lands on the kind NodePort, but the TLS
// handshake's SNI value is the original hostname so HAProxy picks the
// correct certificate.
//
// HTTP requests (port 80) are not rewritten — tests construct those URLs
// directly against nodeIP:HTTPNodePort with a Host: header.
func newSharedTransport(nodeIP string, httpsPort int) *http.Transport {
	dialer := &net.Dialer{
		Timeout:   5 * time.Second,
		KeepAlive: 30 * time.Second,
	}
	target := nodeIP + ":" + strconv.Itoa(httpsPort)
	return &http.Transport{
		DialContext: func(ctx context.Context, network, address string) (net.Conn, error) {
			if strings.HasSuffix(address, ":443") {
				return dialer.DialContext(ctx, network, target)
			}
			return dialer.DialContext(ctx, network, address)
		},
		TLSHandshakeTimeout:   5 * time.Second,
		ResponseHeaderTimeout: 5 * time.Second,
		IdleConnTimeout:       30 * time.Second,
		MaxIdleConns:          16,
		// Self-signed certs are the default in dev-env. Requests that pin
		// a CA via WithClientCert get a per-request transport that
		// overrides this.
		TLSClientConfig: &tls.Config{
			InsecureSkipVerify: true, // #nosec G402 — dev-env uses self-signed certs
			MinVersion:         tls.VersionTLS12,
		},
	}
}

// transportForClientCert returns a transport with the given client cert
// installed and the CA pinned. Used by Request.Do when WithClientCert was
// set; it is built per-request rather than shared because each test gets
// its own cert/CA pair.
func transportForClientCert(nodeIP string, httpsPort int, clientCert tls.Certificate, ca []byte) (*http.Transport, error) {
	pool := x509.NewCertPool()
	if !pool.AppendCertsFromPEM(ca) {
		return nil, errors.New("failed to parse CA PEM")
	}
	t := newSharedTransport(nodeIP, httpsPort)
	t.TLSClientConfig = &tls.Config{
		Certificates: []tls.Certificate{clientCert},
		RootCAs:      pool,
		MinVersion:   tls.VersionTLS12,
	}
	return t, nil
}
