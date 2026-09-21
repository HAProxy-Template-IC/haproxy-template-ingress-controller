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
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"gitlab.com/haproxy-haptic/haptic/pkg/transportsecurity/tlstest"
)

func TestSourceRejectsInvalidReplacement(t *testing.T) {
	ca := tlstest.NewAuthority(t, "CA")
	leaf := tlstest.NewIdentity(t, ca, "agent.example.test", x509.ExtKeyUsageServerAuth)
	for _, tc := range []struct {
		name  string
		file  string
		value []byte
	}{
		{name: "private key mismatch", file: "tls.key", value: tlstest.NewIdentity(t, ca, "other", x509.ExtKeyUsageClientAuth).Key},
		{name: "empty trust", file: "ca.crt", value: []byte{}},
		{name: "trailing corrupt trust", file: "ca.crt", value: append(append([]byte{}, ca.PEM...), []byte("corrupt")...)},
		{name: "leaf is not a CA", file: "ca.crt", value: leaf.Certificate},
		{name: "missing overlap deadline", file: "previous-ca.crt", value: ca.PEM},
		{name: "missing overlap CA", file: "previous-ca-until", value: []byte(time.Now().Format(time.RFC3339))},
	} {
		t.Run(tc.name, func(t *testing.T) {
			link := filepath.Join(t.TempDir(), "active")
			tlstest.Publish(t, link, leaf, ca.PEM, nil, time.Time{})
			source, err := NewSource(link, "controller.example.test")
			require.NoError(t, err)
			require.NoError(t, os.WriteFile(filepath.Join(link, tc.file), tc.value, 0o600))
			_, err = source.load()
			require.Error(t, err)
		})
	}
}

func TestSourceTrustOverlapDeadline(t *testing.T) {
	ca := tlstest.NewAuthority(t, "new")
	oldCA := tlstest.NewAuthority(t, "old")
	leaf := tlstest.NewIdentity(t, ca, "agent.example.test", x509.ExtKeyUsageServerAuth)
	oldClient := tlstest.NewIdentity(t, oldCA, "controller.example.test", x509.ExtKeyUsageClientAuth)
	pair, err := tls.X509KeyPair(oldClient.Certificate, oldClient.Key)
	require.NoError(t, err)
	certificate, err := x509.ParseCertificate(pair.Certificate[0])
	require.NoError(t, err)
	state := &tls.ConnectionState{Version: tls.VersionTLS13, PeerCertificates: []*x509.Certificate{certificate}}
	link := filepath.Join(t.TempDir(), "active")
	now := time.Now().Truncate(time.Second)
	until := now.Add(time.Hour)
	tlstest.Publish(t, link, leaf, ca.PEM, oldCA.PEM, until)
	source, err := NewSource(link, "controller.example.test")
	require.NoError(t, err)
	source.now = func() time.Time { return now }
	require.NoError(t, source.VerifyClient(state))
	now = until
	require.Error(t, source.VerifyClient(state))
	tlstest.Publish(t, link, leaf, ca.PEM, oldCA.PEM, now.Add(25*time.Hour))
	_, err = source.load()
	require.ErrorContains(t, err, "exceeds 24 hours")
}

func TestSourceIdentityExpiry(t *testing.T) {
	ca := tlstest.NewAuthority(t, "CA")
	link := filepath.Join(t.TempDir(), "active")
	leaf := tlstest.NewIdentity(t, ca, "agent.example.test", x509.ExtKeyUsageServerAuth)
	tlstest.Publish(t, link, leaf, ca.PEM, nil, time.Time{})
	source, err := NewSource(link, "controller.example.test")
	require.NoError(t, err)
	source.now = func() time.Time { return time.Now().Add(13 * time.Hour) }
	_, err = source.load()
	require.ErrorContains(t, err, "outside its validity period")
}
