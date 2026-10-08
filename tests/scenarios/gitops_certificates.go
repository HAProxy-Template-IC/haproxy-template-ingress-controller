// Copyright 2026 Philipp Hossner
// SPDX-License-Identifier: Apache-2.0

package scenarios

import (
	"context"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/base64"
	"math/big"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"gitlab.com/haproxy-haptic/haptic/tests/fixtures"
)

func (g *gitops) externalCertificates(ctx context.Context) error {
	now := time.Now()
	ca, err := fixtures.NewCertificate(&x509.Certificate{SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "HAPTIC GitOps test CA"}, NotBefore: now.Add(-time.Hour), NotAfter: now.Add(365 * 24 * time.Hour), IsCA: true, BasicConstraintsValid: true, KeyUsage: x509.KeyUsageCertSign | x509.KeyUsageDigitalSignature}, fixtures.RSA2048, nil)
	if err != nil {
		return err
	}
	g.webhookCA = base64.StdEncoding.EncodeToString(ca.PEM)
	for i, identity := range []struct{ name, dns string }{{gitopsWebhookSecret, "haptic-webhook.haptic.svc"}, {"upgrade-default-tls", "tls.upgrade.test"}} {
		cert, err := fixtures.NewCertificate(&x509.Certificate{SerialNumber: big.NewInt(int64(i + 2)), Subject: pkix.Name{CommonName: identity.dns}, DNSNames: []string{identity.dns}, NotBefore: now.Add(-time.Hour), NotAfter: now.Add(90 * 24 * time.Hour), BasicConstraintsValid: true, KeyUsage: x509.KeyUsageDigitalSignature | x509.KeyUsageKeyEncipherment, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}}, fixtures.RSA2048, ca)
		if err != nil {
			return err
		}
		secret := corev1.Secret{TypeMeta: metav1.TypeMeta{APIVersion: "v1", Kind: "Secret"}, ObjectMeta: metav1.ObjectMeta{Name: identity.name, Namespace: g.session.Namespace}, Type: corev1.SecretTypeTLS, Data: map[string][]byte{corev1.TLSCertKey: cert.PEM, corev1.TLSPrivateKeyKey: cert.KeyPEM, "ca.crt": ca.PEM}}
		if err := g.session.Apply(ctx, &secret); err != nil {
			return err
		}
	}
	return nil
}
