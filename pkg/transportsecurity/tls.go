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

package transportsecurity

import (
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"net/http"
	"sync"
	"time"
)

// ServerConfig permits anonymous probes; authenticated handlers must call VerifyClient.
func (s *Source) ServerConfig() *tls.Config {
	return &tls.Config{
		MinVersion: tls.VersionTLS13,
		GetConfigForClient: func(*tls.ClientHelloInfo) (*tls.Config, error) {
			current, err := s.load()
			if err != nil {
				return nil, err
			}
			return &tls.Config{
				MinVersion:             tls.VersionTLS13,
				Certificates:           []tls.Certificate{current.certificate},
				ClientCAs:              current.roots,
				ClientAuth:             tls.VerifyClientCertIfGiven,
				SessionTicketsDisabled: true,
				Time:                   s.now,
			}, nil
		},
	}
}

// VerifyClient checks current trust and identity even on an existing connection.
func (s *Source) VerifyClient(state *tls.ConnectionState) error {
	if state == nil || state.Version < tls.VersionTLS13 || len(state.PeerCertificates) == 0 {
		return errors.New("a TLS 1.3 client certificate is required")
	}
	current, err := s.load()
	if err != nil {
		return err
	}
	intermediates := x509.NewCertPool()
	for _, certificate := range state.PeerCertificates[1:] {
		intermediates.AddCert(certificate)
	}
	_, err = state.PeerCertificates[0].Verify(x509.VerifyOptions{
		Roots:         current.roots,
		Intermediates: intermediates,
		DNSName:       s.peerName,
		KeyUsages:     []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth},
		CurrentTime:   s.now(),
	})
	if err != nil {
		return fmt.Errorf("verify TLS client identity: %w", err)
	}
	return nil
}

// Transport replaces connection pools when identity or active trust changes.
type Transport struct {
	source  *Source
	base    *http.Transport
	mu      sync.Mutex
	current *snapshot
	pool    *http.Transport
	created time.Time
}

// NewTransport copies base; callers retain ownership of the original transport.
func NewTransport(source *Source, base *http.Transport) *Transport {
	return &Transport{source: source, base: base.Clone()}
}

func (t *Transport) RoundTrip(request *http.Request) (*http.Response, error) {
	if request.URL.Scheme != "https" {
		return nil, errors.New("TLS transport requires an HTTPS endpoint")
	}
	current, err := t.source.load()
	if err != nil {
		return nil, err
	}
	t.mu.Lock()
	now := t.source.now()
	// Bound certificate expiry on idle pools even when the files don't change.
	if t.current != current || now.Sub(t.created) >= 30*time.Second {
		if t.pool != nil {
			t.pool.CloseIdleConnections()
		}
		t.pool = t.base.Clone()
		t.pool.TLSClientConfig = &tls.Config{
			MinVersion:   tls.VersionTLS13,
			Certificates: []tls.Certificate{current.certificate},
			RootCAs:      current.roots,
			ServerName:   t.source.peerName,
			Time:         t.source.now,
		}
		t.current, t.created = current, now
	}
	pool := t.pool
	t.mu.Unlock()
	return pool.RoundTrip(request)
}

// CloseIdleConnections closes the active pool without changing trust.
func (t *Transport) CloseIdleConnections() {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.pool != nil {
		t.pool.CloseIdleConnections()
	}
}
