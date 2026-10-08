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

// tlsProbeTimeout bounds one handshake.
const tlsProbeTimeout = 5 * time.Second

// PeerCertificate observes the served leaf independently of certificate trust.
func (c *Client) PeerCertificate(ctx context.Context, host string) (*x509.Certificate, error) {
	dialer := tls.Dialer{NetDialer: &net.Dialer{Timeout: tlsProbeTimeout}, Config: &tls.Config{
		ServerName: host,
		// The served certificate is the subject under test, not the trust
		// anchor: verifying it here would fail on every self-signed fixture.
		InsecureSkipVerify: true, // #nosec G402 — the caller compares the certificate itself
		MinVersion:         tls.VersionTLS12,
	}}
	conn, err := dialer.DialContext(ctx, "tcp", net.JoinHostPort(c.endpointHost, strconv.Itoa(c.httpsPort)))
	if err != nil {
		return nil, fmt.Errorf("tls dial %s: %w", host, err)
	}
	defer func() { _ = conn.Close() }()

	if deadline, ok := ctx.Deadline(); ok {
		_ = conn.SetDeadline(deadline)
	}
	tlsConn, ok := conn.(*tls.Conn)
	if !ok {
		return nil, fmt.Errorf("TLS dial returned %T", conn)
	}
	certs := tlsConn.ConnectionState().PeerCertificates
	if len(certs) == 0 {
		return nil, fmt.Errorf("tls handshake with %s presented no certificate", host)
	}
	return certs[0], nil
}
