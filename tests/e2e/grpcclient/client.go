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

// Package grpcclient is the e2e suite's TLS-aware gRPC client for
// exercising GRPCRoute traffic against the chart's HTTPS+ALPN-h2
// listeners. Mirrors the SNI-rewriting trick that httpclient uses for
// HTTPS so the wire connection lands on the kind NodePort while the TLS
// handshake carries the test-supplied SNI/authority — equivalent to
// `grpcurl -authority <host> -insecure-skipverify <nodeport>`.
package grpcclient

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"net"
	"strconv"
	"testing"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"

	"gitlab.com/haproxy-haptic/haptic/tests/kindutil"
)

// DefaultHTTPSNodePort is the host-side NodePort the e2e kind cluster
// maps to HAProxy's :443 (see tests/e2e/constants.go HTTPSHostPort).
// Kept in sync with the httpclient's default.
const DefaultHTTPSNodePort = 31443

// Client is a TLS-aware gRPC dialer for the e2e suite. Construct with
// New(t) and call Dial(ctx, host) to get a *grpc.ClientConn whose TLS
// handshake carries `host` as the SNI / authority.
type Client struct {
	t         *testing.T
	httpsPort int
	nodeIP    string
	caBundle  *x509.CertPool // nil → InsecureSkipVerify (test-only default)
}

// New returns a Client wired to the e2e cluster's HTTPS NodePort. The
// nodeIP follows the DinD-aware resolution httpclient uses: 127.0.0.1
// outside DinD, the docker-host IP inside.
func New(t *testing.T) *Client {
	t.Helper()
	ip, err := resolveNodeIP()
	if err != nil {
		t.Fatalf("resolve node IP: %v", err)
	}
	return &Client{t: t, httpsPort: DefaultHTTPSNodePort, nodeIP: ip}
}

// WithCA replaces the trust store with the supplied PEM bundle. Use
// when the test cert chains up to a known CA the test suite generated.
// Without this (the default), the dialer uses InsecureSkipVerify=true,
// which mirrors httpclient.New's default and is appropriate for routing
// tests where chain validation isn't the assertion.
func (c *Client) WithCA(pem []byte) *Client {
	pool := x509.NewCertPool()
	if !pool.AppendCertsFromPEM(pem) {
		c.t.Fatalf("grpcclient: AppendCertsFromPEM failed (bad PEM bundle)")
	}
	c.caBundle = pool
	return c
}

// Dial opens a TLS-protected gRPC connection to the kind NodePort whose
// TLS handshake carries `host` as both SNI and HTTP/2 :authority. The
// caller owns the *grpc.ClientConn and is responsible for Close().
func (c *Client) Dial(ctx context.Context, host string) (*grpc.ClientConn, error) {
	tlsCfg := buildTLSConfig(host, c.caBundle)
	target := c.nodeIP + ":" + strconv.Itoa(c.httpsPort)
	dialer := &net.Dialer{}
	conn, err := grpc.DialContext(ctx, target,
		grpc.WithTransportCredentials(credentials.NewTLS(tlsCfg)),
		grpc.WithAuthority(host),
		grpc.WithContextDialer(func(ctx context.Context, _ string) (net.Conn, error) {
			return dialer.DialContext(ctx, "tcp", target)
		}),
		grpc.WithBlock(),
	)
	if err != nil {
		return nil, fmt.Errorf("grpc.DialContext %q (host=%s): %w", target, host, err)
	}
	return conn, nil
}

// buildTLSConfig assembles the test dialer's *tls.Config. When
// caBundle is nil, the dialer skips chain verification — appropriate
// for routing tests against self-signed test certs (chart-side cert
// chain validation is exercised by other e2e tests).
func buildTLSConfig(host string, caBundle *x509.CertPool) *tls.Config {
	cfg := &tls.Config{
		ServerName: host,
		MinVersion: tls.VersionTLS12,
		NextProtos: []string{"h2"},
	}
	if caBundle != nil {
		cfg.RootCAs = caBundle
	} else {
		// Test-only: routing tests use self-signed certs and don't
		// assert on the chain. The chart's chain-validation behaviour
		// is exercised by separate e2e tests with WithCA().
		cfg.InsecureSkipVerify = true
	}
	return cfg
}

// resolveNodeIP returns an IPv4 NodePort IP, or 127.0.0.1 outside DinD.
// Mirrors httpclient.resolveNodeIP — kept in sync because the two
// clients dial the same NodePort.
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
	return "", errors.New("no IPv4 address for DinD hostname")
}
