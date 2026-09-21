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

// Package transportsecurity provides reloadable mutual TLS for HTTP connections.
package transportsecurity

import (
	"bytes"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

const maxTrustOverlap = 24 * time.Hour

// Source reads tls.crt, tls.key, and ca.crt from one directory revision.
type Source struct {
	directory string
	peerName  string
	now       func() time.Time
	mu        sync.Mutex
	cached    *snapshot
}

type material struct {
	Certificate []byte
	Key         []byte
	CA          []byte
	PreviousCA  []byte
}

type snapshot struct {
	identity    [sha256.Size]byte
	certificate tls.Certificate
	roots       *x509.CertPool
}

// NewSource validates the initial revision. peerName is the required peer DNS SAN.
func NewSource(directory, peerName string) (*Source, error) {
	if directory == "" || peerName == "" {
		return nil, errors.New("TLS directory and peer name are required")
	}
	s := &Source{directory: directory, peerName: peerName, now: time.Now}
	if _, err := s.load(); err != nil {
		return nil, err
	}
	return s, nil
}

func (s *Source) load() (*snapshot, error) {
	directory, err := filepath.EvalSymlinks(s.directory)
	if err != nil {
		return nil, fmt.Errorf("resolve TLS directory: %w", err)
	}
	m, err := readMaterial(directory, s.now())
	if err != nil {
		return nil, err
	}
	encoded, err := json.Marshal(m)
	if err != nil {
		return nil, fmt.Errorf("fingerprint TLS material: %w", err)
	}
	identity := sha256.Sum256(encoded)
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.cached == nil || s.cached.identity != identity {
		next, err := parseMaterial(m, identity)
		if err != nil {
			return nil, err
		}
		s.cached = next
	}
	leaf := s.cached.certificate.Leaf
	if now := s.now(); now.Before(leaf.NotBefore) || !now.Before(leaf.NotAfter) {
		return nil, errors.New("TLS identity certificate is outside its validity period; replace tls.crt and tls.key")
	}
	return s.cached, nil
}

func readMaterial(directory string, now time.Time) (*material, error) {
	root, err := os.OpenRoot(directory)
	if err != nil {
		return nil, fmt.Errorf("open TLS directory: %w", err)
	}
	defer root.Close()
	m := &material{}
	for name, dst := range map[string]*[]byte{"tls.crt": &m.Certificate, "tls.key": &m.Key, "ca.crt": &m.CA} {
		data, err := root.ReadFile(name)
		if err != nil {
			return nil, fmt.Errorf("read TLS %s: %w", name, err)
		}
		*dst = data
	}
	previous, err := readOptional(root, "previous-ca.crt")
	if err != nil {
		return nil, err
	}
	deadline, err := readOptional(root, "previous-ca-until")
	if err != nil {
		return nil, err
	}
	if len(previous) == 0 && len(deadline) == 0 {
		return m, nil
	}
	if len(previous) == 0 || len(deadline) == 0 {
		return nil, errors.New("previous-ca.crt and previous-ca-until must be supplied together")
	}
	until, err := time.Parse(time.RFC3339, strings.TrimSpace(string(deadline)))
	if err != nil {
		return nil, errors.New("previous-ca-until must be an RFC3339 timestamp")
	}
	if until.Sub(now) > maxTrustOverlap {
		return nil, errors.New("previous CA trust exceeds 24 hours; shorten previous-ca-until")
	}
	if now.Before(until) {
		m.PreviousCA = previous
	}
	return m, nil
}

func readOptional(root *os.Root, name string) ([]byte, error) {
	data, err := root.ReadFile(name)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read TLS trust overlap: %w", err)
	}
	return data, nil
}

func parseMaterial(m *material, identity [sha256.Size]byte) (*snapshot, error) {
	certificate, err := tls.X509KeyPair(m.Certificate, m.Key)
	if err != nil {
		return nil, fmt.Errorf("load TLS identity: %w", err)
	}
	certificate.Leaf, err = x509.ParseCertificate(certificate.Certificate[0])
	if err != nil {
		return nil, fmt.Errorf("parse TLS identity: %w", err)
	}
	roots := x509.NewCertPool()
	if err := appendCA(roots, m.CA); err != nil {
		return nil, fmt.Errorf("load ca.crt: %w", err)
	}
	if len(m.PreviousCA) > 0 {
		if err := appendCA(roots, m.PreviousCA); err != nil {
			return nil, fmt.Errorf("load previous-ca.crt: %w", err)
		}
	}
	return &snapshot{identity: identity, certificate: certificate, roots: roots}, nil
}

func appendCA(pool *x509.CertPool, data []byte) error {
	if len(bytes.TrimSpace(data)) == 0 {
		return errors.New("CA bundle is empty")
	}
	for len(bytes.TrimSpace(data)) > 0 {
		if !bytes.HasPrefix(bytes.TrimSpace(data), []byte("-----BEGIN CERTIFICATE-----")) {
			return errors.New("CA bundle contains invalid certificate PEM")
		}
		block, rest := pem.Decode(data)
		if block == nil || block.Type != "CERTIFICATE" {
			return errors.New("CA bundle contains invalid certificate PEM")
		}
		certificate, err := x509.ParseCertificate(block.Bytes)
		if err != nil {
			return fmt.Errorf("parse CA certificate: %w", err)
		}
		if !certificate.IsCA || !certificate.BasicConstraintsValid || certificate.KeyUsage&x509.KeyUsageCertSign == 0 {
			return errors.New("CA bundle contains a certificate without CA signing authority")
		}
		pool.AddCert(certificate)
		data = rest
	}
	return nil
}
