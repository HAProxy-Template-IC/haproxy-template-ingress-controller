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

// Package tlstest creates temporary certificate revisions for transport tests.
package tlstest

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

type Authority struct {
	Certificate *x509.Certificate
	Key         *ecdsa.PrivateKey
	PEM         []byte
}

type Identity struct {
	Certificate []byte
	Key         []byte
}

func NewAuthority(tb testing.TB, name string) Authority {
	tb.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	require.NoError(tb, err)
	certificate := &x509.Certificate{
		SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: name},
		NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(48 * time.Hour),
		IsCA: true, BasicConstraintsValid: true, KeyUsage: x509.KeyUsageCertSign,
	}
	der, err := x509.CreateCertificate(rand.Reader, certificate, certificate, &key.PublicKey, key)
	require.NoError(tb, err)
	certificate, err = x509.ParseCertificate(der)
	require.NoError(tb, err)
	return Authority{Certificate: certificate, Key: key, PEM: pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})}
}

func NewIdentity(tb testing.TB, ca Authority, name string, usage x509.ExtKeyUsage) Identity {
	tb.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	require.NoError(tb, err)
	certificate := &x509.Certificate{
		SerialNumber: big.NewInt(2), DNSNames: []string{name},
		NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(12 * time.Hour),
		KeyUsage: x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{usage},
	}
	der, err := x509.CreateCertificate(rand.Reader, certificate, ca.Certificate, &key.PublicKey, ca.Key)
	require.NoError(tb, err)
	private, err := x509.MarshalPKCS8PrivateKey(key)
	require.NoError(tb, err)
	return Identity{
		Certificate: pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}),
		Key:         pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: private}),
	}
}

func Publish(tb testing.TB, link string, leaf Identity, ca, previous []byte, until time.Time) {
	tb.Helper()
	revision, err := os.MkdirTemp(filepath.Dir(link), "revision-")
	require.NoError(tb, err)
	files := map[string][]byte{"tls.crt": leaf.Certificate, "tls.key": leaf.Key, "ca.crt": ca}
	if previous != nil {
		files["previous-ca.crt"] = previous
		files["previous-ca-until"] = []byte(until.Format(time.RFC3339))
	}
	for name, content := range files {
		require.NoError(tb, os.WriteFile(filepath.Join(revision, name), content, 0o600))
	}
	require.NoError(tb, os.Symlink(revision, link+"-next"))
	require.NoError(tb, os.Rename(link+"-next", link))
}
