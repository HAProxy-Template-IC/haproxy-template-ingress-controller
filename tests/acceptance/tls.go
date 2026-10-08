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

//go:build acceptance

package acceptance

import (
	"crypto/x509"
	"crypto/x509/pkix"
	"math/big"
	"time"

	"gitlab.com/haproxy-haptic/haptic/tests/fixtures"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// newTLSSecret builds a kubernetes.io/tls Secret with the standard
// tls.crt / tls.key keys (consumed both by the controller's API-fetch path
// and, when mounted, as files for the reloading server).
func newTLSSecret(namespace, name string, certPEM, keyPEM []byte) *corev1.Secret {
	return &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: namespace},
		Type:       corev1.SecretTypeTLS,
		Data: map[string][]byte{
			"tls.crt": certPEM,
			"tls.key": keyPEM,
		},
	}
}

// genSelfSignedServerCert builds a self-signed server certificate with the
// given serial number and DNS SAN. Distinct serials let the test detect a
// rotation purely from the served certificate.
func genSelfSignedServerCert(serial int64, dnsName string) (certPEM, keyPEM []byte, err error) {
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(serial),
		Subject:      pkix.Name{CommonName: dnsName},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(24 * time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature | x509.KeyUsageKeyEncipherment,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		DNSNames:     []string{dnsName},
	}

	cert, err := fixtures.NewCertificate(tmpl, fixtures.RSA2048, nil)
	if err != nil {
		return nil, nil, err
	}
	return cert.PEM, cert.KeyPEM, nil
}
