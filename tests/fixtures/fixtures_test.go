// Copyright 2026 Philipp Hossner
// SPDX-License-Identifier: Apache-2.0

package fixtures

import (
	"crypto/tls"
	"crypto/x509"
	"math/big"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestEmbeddedAssetsReturnIndependentBytes(t *testing.T) {
	first, err := ChartUpgrade("values.yaml")
	require.NoError(t, err)
	first[0] = '!'
	next, err := ChartUpgrade("values.yaml")
	require.NoError(t, err)
	require.NotEqual(t, first, next)
	_, err = ChartUpgrade("../values.yaml")
	require.Error(t, err)
}

func TestCertificateFactoriesPreserveIdentityUsageAndFreshKeys(t *testing.T) {
	now := time.Now()
	for _, algorithm := range []KeyAlgorithm{RSA2048, ECDSAP256} {
		ca, err := NewCertificate(&x509.Certificate{SerialNumber: big.NewInt(1), NotBefore: now.Add(-time.Hour), NotAfter: now.Add(time.Hour), IsCA: true, BasicConstraintsValid: true, KeyUsage: x509.KeyUsageCertSign}, algorithm, nil)
		require.NoError(t, err)
		template := &x509.Certificate{SerialNumber: big.NewInt(123), DNSNames: []string{"test.local"}, NotBefore: now.Add(-time.Hour), NotAfter: now.Add(time.Hour), KeyUsage: x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}}
		leaf, err := NewCertificate(template, algorithm, ca)
		require.NoError(t, err)
		_, err = tls.X509KeyPair(leaf.PEM, leaf.KeyPEM)
		require.NoError(t, err)
		roots := x509.NewCertPool()
		roots.AddCert(ca.Parsed)
		_, err = leaf.Parsed.Verify(x509.VerifyOptions{Roots: roots, DNSName: "test.local", KeyUsages: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}})
		require.NoError(t, err)
		_, err = leaf.Parsed.Verify(x509.VerifyOptions{Roots: roots, DNSName: "wrong.local"})
		require.Error(t, err)
		_, err = leaf.Parsed.Verify(x509.VerifyOptions{Roots: roots, DNSName: "test.local", KeyUsages: []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth}})
		require.Error(t, err)
		require.Equal(t, int64(123), leaf.Parsed.SerialNumber.Int64())
		replacement, err := NewCertificate(template, algorithm, ca)
		require.NoError(t, err)
		require.NotEqual(t, leaf.KeyPEM, replacement.KeyPEM)
	}
}
