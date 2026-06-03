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

package webhook

import (
	"crypto/rand"
	"crypto/rsa"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestCertReloader_ServesRotatedCertificate is the core hot-rotation contract:
// after the backing files change, GetCertificate must serve the new cert
// without the reloader being recreated.
func TestCertReloader_ServesRotatedCertificate(t *testing.T) {
	dir := t.TempDir()
	certFile := filepath.Join(dir, "tls.crt")
	keyFile := filepath.Join(dir, "tls.key")

	writeKeypair(t, certFile, keyFile, 0x1A)

	r, err := newCertReloader(certFile, keyFile)
	require.NoError(t, err)

	got, err := r.GetCertificate(nil)
	require.NoError(t, err)
	require.NotNil(t, got)
	assert.Equal(t, int64(0x1A), leafSerial(t, got), "initially serves cert A")

	// Rotate the files in place to cert B (as cert-manager / kubelet would).
	writeKeypair(t, certFile, keyFile, 0x2B)

	got2, err := r.GetCertificate(nil)
	require.NoError(t, err)
	assert.Equal(t, int64(0x2B), leafSerial(t, got2), "serves the rotated cert B without recreation")
}

// TestCertReloader_KeepsLastGoodOnBadRotation ensures a half-written or invalid
// rotation never breaks serving — the reloader keeps the last good cert.
func TestCertReloader_KeepsLastGoodOnBadRotation(t *testing.T) {
	dir := t.TempDir()
	certFile := filepath.Join(dir, "tls.crt")
	keyFile := filepath.Join(dir, "tls.key")
	writeKeypair(t, certFile, keyFile, 0x1A)

	r, err := newCertReloader(certFile, keyFile)
	require.NoError(t, err)
	_, err = r.GetCertificate(nil)
	require.NoError(t, err)

	// Corrupt the cert file (simulates a torn write mid-rotation).
	require.NoError(t, os.WriteFile(certFile, []byte("-----BEGIN CERTIFICATE-----\nnonsense\n-----END CERTIFICATE-----\n"), 0o600))

	got, err := r.GetCertificate(nil)
	require.NoError(t, err, "a bad rotation must not break serving")
	assert.Equal(t, int64(0x1A), leafSerial(t, got), "keeps serving the last good cert")
}

// TestCertReloader_MissingFilesFailConstruction makes initial absence a hard
// error (the server must not start serving without a valid cert).
func TestCertReloader_MissingFilesFailConstruction(t *testing.T) {
	dir := t.TempDir()
	_, err := newCertReloader(filepath.Join(dir, "tls.crt"), filepath.Join(dir, "tls.key"))
	require.Error(t, err)
}

func writeKeypair(t *testing.T, certFile, keyFile string, serial int64) {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	require.NoError(t, err)

	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(serial),
		Subject:      pkix.Name{CommonName: "test"},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(24 * time.Hour),
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	require.NoError(t, err)

	certPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
	keyPEM := pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(key)})
	require.NoError(t, os.WriteFile(certFile, certPEM, 0o600))
	require.NoError(t, os.WriteFile(keyFile, keyPEM, 0o600))
}

func leafSerial(t *testing.T, cert *tls.Certificate) int64 {
	t.Helper()
	leaf := cert.Leaf
	if leaf == nil {
		var err error
		leaf, err = x509.ParseCertificate(cert.Certificate[0])
		require.NoError(t, err)
	}
	return leaf.SerialNumber.Int64()
}
