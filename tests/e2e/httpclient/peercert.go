// Copyright 2026 Philipp Hossner
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

package httpclient

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"net"
	"strconv"
	"time"
)

// tlsProbeTimeout bounds one handshake. The probe answers from the node's
// own NodePort, so a slow one is a sick data plane, not a slow network.
const tlsProbeTimeout = 5 * time.Second

// PeerCertificate returns the leaf certificate HAProxy presents for host.
//
// It answers the question no HTTP response can: WHICH certificate is being
// served. A test that replaces a certificate's Secret with unusable bytes
// asserts against this, because a 200 alone would also be satisfied by the
// new certificate having taken effect.
//
// The dial mirrors the shared transport's rewrite — wire target is the
// NodePort, SNI is host — so HAProxy selects exactly the certificate a real
// client for that hostname would get.
func (c *Client) PeerCertificate(ctx context.Context, host string) (*x509.Certificate, error) {
	dialer := &net.Dialer{Timeout: tlsProbeTimeout}
	conn, err := tls.DialWithDialer(dialer, "tcp", c.nodeIP+":"+strconv.Itoa(c.httpsPort), &tls.Config{
		ServerName: host,
		// The served certificate is the subject under test, not the trust
		// anchor: verifying it here would fail on every self-signed fixture.
		InsecureSkipVerify: true, // #nosec G402 — the caller compares the certificate itself
		MinVersion:         tls.VersionTLS12,
	})
	if err != nil {
		return nil, fmt.Errorf("tls dial %s: %w", host, err)
	}
	defer func() { _ = conn.Close() }()

	if deadline, ok := ctx.Deadline(); ok {
		_ = conn.SetDeadline(deadline)
	}
	certs := conn.ConnectionState().PeerCertificates
	if len(certs) == 0 {
		return nil, fmt.Errorf("tls handshake with %s presented no certificate", host)
	}
	return certs[0], nil
}
