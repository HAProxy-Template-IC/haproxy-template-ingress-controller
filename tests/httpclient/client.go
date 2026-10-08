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

// Package httpclient provides HTTP, HTTPS, and mTLS assertions for explicit test endpoints.
package httpclient

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"net"
	"net/http"
	"strconv"
	"strings"
	"testing"
	"time"

	"gitlab.com/haproxy-haptic/haptic/tests/testutil"
)

// Client is a fluent HTTP client targeting an explicit test endpoint.
// One Client may serve many Requests; concurrent use is safe.
type Client struct {
	endpointHost string

	httpPort  int
	httpsPort int

	// waitCfg is the retry/backoff policy applied to Expect* sinks.
	waitCfg testutil.WaitConfig

	// transport is shared across all non-mTLS requests for connection pooling.
	transport *http.Transport

	// Capture failure evidence before the test removes its fixtures.
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

type Config struct {
	Host          string
	HTTPPort      int
	HTTPSPort     int
	TLS           *tls.Config
	OnPollTimeout PollTimeoutSnapshot
}

// New dials the explicit endpoint while preserving the requested host and TLS SNI.
func New(config *Config) *Client {
	transport := newSharedTransport(config.Host, config.HTTPSPort)
	if config.TLS != nil {
		transport.TLSClientConfig = config.TLS.Clone()
	}
	return &Client{
		endpointHost: config.Host, httpPort: config.HTTPPort, httpsPort: config.HTTPSPort,
		waitCfg:   testutil.WaitConfig{InitialInterval: 100 * time.Millisecond, MaxInterval: 2 * time.Second, Timeout: 15 * time.Second, Multiplier: 2},
		transport: transport, onPollTimeout: config.OnPollTimeout,
	}
}

// CloseIdleConnections drops the shared transport's pooled keepalive
// connections so the next request dials a fresh one. Useful when polling for a
// not-yet-live route across a reload: a request that 404s on the pre-change
// HAProxy worker pins a keepalive connection to that (draining) worker, which
// keeps answering with the old config until it closes; forcing a fresh dial lets
// the retry reach a current worker generation.
func (c *Client) CloseIdleConnections() { c.transport.CloseIdleConnections() }

// Dial the explicit endpoint while keeping the requested host for TLS SNI.
func newSharedTransport(endpointHost string, httpsPort int) *http.Transport {
	dialer := &net.Dialer{
		Timeout:   5 * time.Second,
		KeepAlive: 30 * time.Second,
	}
	target := net.JoinHostPort(endpointHost, strconv.Itoa(httpsPort))
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
		TLSClientConfig: &tls.Config{
			MinVersion: tls.VersionTLS12,
		},
	}
}

// transportForClientCert returns a transport with the given client cert
// installed and the CA pinned. Used by Request.Do when WithClientCert was
// set; it is built per-request rather than shared because each test gets
// its own cert/CA pair.
func transportForClientCert(endpointHost string, httpsPort int, clientCert *tls.Certificate, ca []byte) (*http.Transport, error) {
	pool := x509.NewCertPool()
	if !pool.AppendCertsFromPEM(ca) {
		return nil, errors.New("failed to parse CA PEM")
	}
	t := newSharedTransport(endpointHost, httpsPort)
	t.TLSClientConfig = &tls.Config{
		Certificates: []tls.Certificate{*clientCert},
		RootCAs:      pool,
		MinVersion:   tls.VersionTLS12,
	}
	return t, nil
}
