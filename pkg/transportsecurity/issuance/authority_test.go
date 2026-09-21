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

package issuance

import (
	"crypto/tls"
	"crypto/x509"
	"encoding/pem"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestAuthorityTransition(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Second)
	previous, err := NewAuthority("issuer", now, 30*24*time.Hour)
	require.NoError(t, err)
	next, err := NewAuthority("issuer", now, 365*24*time.Hour)
	require.NoError(t, err)
	bridge, err := previous.CrossSign(next, now, now.Add(24*time.Hour))
	require.NoError(t, err)

	for _, role := range []struct {
		name  string
		usage x509.ExtKeyUsage
	}{
		{"agent.example.internal", x509.ExtKeyUsageServerAuth},
		{"controller.example.internal", x509.ExtKeyUsageClientAuth},
	} {
		t.Run(role.name, func(t *testing.T) {
			oldIdentity, issueErr := previous.Issue(role.name, role.usage, now, 30*24*time.Hour)
			require.NoError(t, issueErr)
			newIdentity, issueErr := next.Issue(role.name, role.usage, now, 365*24*time.Hour)
			require.NoError(t, issueErr)
			tests := []struct {
				name     string
				identity KeyPair
				roots    []*Authority
				at       time.Time
				valid    bool
			}{
				{"old trust accepts new identity", newIdentity, []*Authority{previous}, now, true},
				{"new trust accepts new identity", newIdentity, []*Authority{next}, now, true},
				{"old identity during overlap", oldIdentity, []*Authority{previous, next}, now, true},
				{"old identity after revocation", oldIdentity, []*Authority{next}, now, false},
				{"bridge expires", newIdentity, []*Authority{previous}, now.Add(25 * time.Hour), false},
				{"new chain outlives bridge and old CA", newIdentity, []*Authority{next}, now.Add(31 * 24 * time.Hour), true},
				{"new identity expires", newIdentity, []*Authority{next}, now.Add(366 * 24 * time.Hour), false},
			}
			for _, test := range tests {
				t.Run(test.name, func(t *testing.T) {
					verifyErr := verifyIdentity(t, test.identity, bridge, test.roots, role.name, role.usage, test.at)
					if test.valid {
						require.NoError(t, verifyErr)
					} else {
						require.Error(t, verifyErr)
					}
				})
			}
			require.Error(t, verifyIdentity(t, newIdentity, bridge, []*Authority{next}, "wrong.example.internal", role.usage, now))
			otherUsage := x509.ExtKeyUsageClientAuth
			if role.usage == x509.ExtKeyUsageClientAuth {
				otherUsage = x509.ExtKeyUsageServerAuth
			}
			require.Error(t, verifyIdentity(t, newIdentity, bridge, []*Authority{next}, role.name, otherUsage, now))
		})
	}
}

func verifyIdentity(tb testing.TB, identity KeyPair, bridge []byte, roots []*Authority, name string, usage x509.ExtKeyUsage, at time.Time) error {
	tb.Helper()
	pair, err := tls.X509KeyPair(identity.Certificate, identity.PrivateKey)
	require.NoError(tb, err)
	trust := x509.NewCertPool()
	for _, ca := range roots {
		trust.AddCert(ca.certificate)
	}
	intermediates := x509.NewCertPool()
	require.True(tb, intermediates.AppendCertsFromPEM(bridge))
	_, err = pair.Leaf.Verify(x509.VerifyOptions{
		Roots: trust, Intermediates: intermediates, DNSName: name,
		KeyUsages: []x509.ExtKeyUsage{usage}, CurrentTime: at,
	})
	return err
}

func TestAuthorityPersistence(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Second)
	authority, err := NewAuthority("issuer", now, 24*time.Hour)
	require.NoError(t, err)
	encoded, err := authority.KeyPair()
	require.NoError(t, err)
	restored, err := ParseAuthority(encoded)
	require.NoError(t, err)
	require.Equal(t, authority.ExpiresAt(), restored.ExpiresAt())
	identity, err := restored.Issue("agent.internal", x509.ExtKeyUsageServerAuth, now, time.Hour)
	require.NoError(t, err)
	pair, err := tls.X509KeyPair(identity.Certificate, identity.PrivateKey)
	require.NoError(t, err)
	require.NoError(t, pair.Leaf.CheckSignatureFrom(authority.certificate))
	expires, err := restored.CheckIdentity(identity, "agent.internal", x509.ExtKeyUsageServerAuth)
	require.NoError(t, err)
	require.Equal(t, now.Add(time.Hour), expires)
	_, err = restored.CheckIdentity(identity, "controller.internal", x509.ExtKeyUsageServerAuth)
	require.Error(t, err)
	_, err = restored.CheckIdentity(identity, "agent.internal", x509.ExtKeyUsageClientAuth)
	require.Error(t, err)

	other, err := NewAuthority("other", now, time.Hour)
	require.NoError(t, err)
	otherEncoded, err := other.KeyPair()
	require.NoError(t, err)
	_, err = other.CheckIdentity(identity, "agent.internal", x509.ExtKeyUsageServerAuth)
	require.Error(t, err)
	for _, test := range []struct {
		name string
		pair KeyPair
	}{
		{"missing certificate", KeyPair{PrivateKey: encoded.PrivateKey}},
		{"missing key", KeyPair{Certificate: encoded.Certificate}},
		{"mismatched key", KeyPair{Certificate: encoded.Certificate, PrivateKey: otherEncoded.PrivateKey}},
		{"leaf as CA", identity},
		{"leading garbage", KeyPair{Certificate: append([]byte("unexpected\n"), encoded.Certificate...), PrivateKey: encoded.PrivateKey}},
		{"trailing garbage", KeyPair{Certificate: append(encoded.Certificate, []byte("unexpected\n")...), PrivateKey: encoded.PrivateKey}},
	} {
		t.Run(test.name, func(t *testing.T) {
			_, parseErr := ParseAuthority(test.pair)
			require.Error(t, parseErr)
		})
	}
	_, err = ParseAuthority(encoded)
	require.NoError(t, err)
	_, err = restored.Issue("agent.internal", x509.ExtKeyUsageServerAuth, now.Add(25*time.Hour), time.Hour)
	require.ErrorContains(t, err, "outside its validity period")
}

func TestAuthorityRefusesInvalidIssuance(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Second)
	authority, err := NewAuthority("issuer", now, 24*time.Hour)
	require.NoError(t, err)
	for _, name := range []string{"", "*.example.internal", "name with spaces", "-invalid", "invalid-", strings.Repeat("a", 64) + ".internal"} {
		_, err = authority.Issue(name, x509.ExtKeyUsageServerAuth, now, time.Hour)
		require.Error(t, err)
	}
	for _, usage := range []x509.ExtKeyUsage{x509.ExtKeyUsageAny, x509.ExtKeyUsageCodeSigning} {
		_, err = authority.Issue("agent.internal", usage, now, time.Hour)
		require.Error(t, err)
	}
	for _, lifetime := range []time.Duration{0, -time.Second, 25 * time.Hour} {
		_, err = authority.Issue("agent.internal", x509.ExtKeyUsageServerAuth, now, lifetime)
		require.Error(t, err)
	}
	next, err := NewAuthority("next", now, 48*time.Hour)
	require.NoError(t, err)
	_, err = authority.CrossSign(nil, now, now.Add(time.Hour))
	require.Error(t, err)
	_, err = authority.CrossSign(next, now, now)
	require.Error(t, err)
	_, err = authority.CrossSign(next, now, now.Add(25*time.Hour))
	require.ErrorContains(t, err, "outlive")
	_, err = next.CrossSign(authority, now, now.Add(25*time.Hour))
	require.ErrorContains(t, err, "outlive")
	_, err = authority.CrossSign(next, now.Add(25*time.Hour), now.Add(26*time.Hour))
	require.ErrorContains(t, err, "outside its validity period")
	_, err = NewAuthority("", now, time.Hour)
	require.Error(t, err)
	_, err = NewAuthority("issuer", now, 0)
	require.Error(t, err)
}

func TestCrossSignConstraints(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Second)
	authority, err := NewAuthority("issuer", now, 24*time.Hour)
	require.NoError(t, err)
	next, err := NewAuthority("next", now, 48*time.Hour)
	require.NoError(t, err)
	bridge, err := authority.CrossSign(next, now, now.Add(time.Hour))
	require.NoError(t, err)
	block, _ := pem.Decode(bridge)
	require.NotNil(t, block)
	certificate, err := x509.ParseCertificate(block.Bytes)
	require.NoError(t, err)
	require.True(t, certificate.MaxPathLenZero)
	require.True(t, certificate.NotBefore.Before(now))
	require.Equal(t, now.Add(time.Hour), certificate.NotAfter)
	require.Equal(t, next.certificate.RawSubjectPublicKeyInfo, certificate.RawSubjectPublicKeyInfo)
	require.NoError(t, certificate.CheckSignatureFrom(authority.certificate))
	forbidden := &Authority{certificate: certificate, key: next.key}
	_, err = forbidden.CrossSign(authority, now, now.Add(time.Hour))
	require.ErrorContains(t, err, "cannot authorize")
}
