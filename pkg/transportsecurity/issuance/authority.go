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

// Package issuance creates identities and bounded CA transition certificates.
package issuance

import (
	"bytes"
	"crypto"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"errors"
	"fmt"
	"math/big"
	"time"
)

// KeyPair contains PEM data. Neither field belongs in logs or diagnostics.
type KeyPair struct {
	Certificate []byte
	PrivateKey  []byte
}

type Authority struct {
	certificate *x509.Certificate
	key         crypto.Signer
}

const (
	validityBackdate   = 5 * time.Minute
	certificatePEMType = "CERTIFICATE"
)

func NewAuthority(name string, now time.Time, lifetime time.Duration) (*Authority, error) {
	if name == "" || lifetime < time.Second {
		return nil, errors.New("CA name and a lifetime of at least one second are required")
	}
	serial, err := serialNumber()
	if err != nil {
		return nil, err
	}
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, fmt.Errorf("generate CA key: %w", err)
	}
	template := &x509.Certificate{
		SerialNumber: serial, Subject: pkix.Name{CommonName: name},
		NotBefore: now.Add(-validityBackdate), NotAfter: now.Add(lifetime),
		IsCA: true, BasicConstraintsValid: true, MaxPathLen: 1,
		KeyUsage: x509.KeyUsageCertSign | x509.KeyUsageCRLSign,
	}
	der, err := x509.CreateCertificate(rand.Reader, template, template, key.Public(), key)
	if err != nil {
		return nil, fmt.Errorf("issue CA certificate: %w", err)
	}
	certificate, err := x509.ParseCertificate(der)
	if err != nil {
		return nil, fmt.Errorf("parse issued CA certificate: %w", err)
	}
	return &Authority{certificate: certificate, key: key}, nil
}

// ParseAuthority allows expired CAs so callers can recover their persisted state.
func ParseAuthority(pair KeyPair) (*Authority, error) {
	certificate, err := parseCertificate(pair.Certificate)
	if err != nil {
		return nil, err
	}
	if !certificate.IsCA || !certificate.BasicConstraintsValid || certificate.KeyUsage&x509.KeyUsageCertSign == 0 {
		return nil, errors.New("issuer certificate must be a CA with certificate signing usage")
	}
	if err := certificate.CheckSignatureFrom(certificate); err != nil {
		return nil, fmt.Errorf("issuer certificate must be self-signed: %w", err)
	}
	identity, err := tls.X509KeyPair(pair.Certificate, pair.PrivateKey)
	if err != nil {
		return nil, fmt.Errorf("load CA key pair: %w", err)
	}
	key, ok := identity.PrivateKey.(crypto.Signer)
	if !ok {
		return nil, errors.New("CA private key cannot sign certificates")
	}
	return &Authority{certificate: certificate, key: key}, nil
}

func (a *Authority) ExpiresAt() time.Time {
	return a.certificate.NotAfter
}

func (a *Authority) KeyPair() (KeyPair, error) {
	return encodeKeyPair(a.certificate.Raw, a.key)
}

// CrossSign bridges old trust to the next CA only until the supplied deadline.
func (a *Authority) CrossSign(next *Authority, now, until time.Time) ([]byte, error) {
	if next == nil || !until.After(now) {
		return nil, errors.New("next CA and a future overlap deadline are required")
	}
	if err := a.validDuring(now, until); err != nil {
		return nil, err
	}
	if err := next.validDuring(now, until); err != nil {
		return nil, err
	}
	if a.certificate.MaxPathLenZero {
		return nil, errors.New("current CA cannot authorize an intermediate transition certificate")
	}
	serial, err := serialNumber()
	if err != nil {
		return nil, err
	}
	notBefore := next.backdate(now)
	if notBefore.Before(a.certificate.NotBefore) {
		notBefore = a.certificate.NotBefore
	}
	template := &x509.Certificate{
		SerialNumber: serial, Subject: next.certificate.Subject,
		SubjectKeyId: next.certificate.SubjectKeyId,
		NotBefore:    notBefore, NotAfter: until,
		IsCA: true, BasicConstraintsValid: true, MaxPathLen: 0, MaxPathLenZero: true,
		KeyUsage: x509.KeyUsageCertSign | x509.KeyUsageCRLSign,
	}
	der, err := x509.CreateCertificate(rand.Reader, template, a.certificate, next.key.Public(), a.key)
	if err != nil {
		return nil, fmt.Errorf("cross-sign next CA: %w", err)
	}
	return pem.EncodeToMemory(&pem.Block{Type: certificatePEMType, Bytes: der}), nil
}

func (a *Authority) validDuring(now, until time.Time) error {
	if now.Before(a.certificate.NotBefore) || !now.Before(a.certificate.NotAfter) {
		return errors.New("CA certificate is outside its validity period")
	}
	if until.After(a.certificate.NotAfter) {
		return errors.New("requested certificate would outlive its CA")
	}
	return nil
}

func (a *Authority) backdate(now time.Time) time.Time {
	notBefore := now.Add(-validityBackdate)
	if notBefore.Before(a.certificate.NotBefore) {
		return a.certificate.NotBefore
	}
	return notBefore
}

func serialNumber() (*big.Int, error) {
	limit := new(big.Int).Lsh(big.NewInt(1), 128)
	serial, err := rand.Int(rand.Reader, limit)
	if err != nil {
		return nil, fmt.Errorf("generate certificate serial: %w", err)
	}
	if serial.Sign() == 0 {
		serial.SetInt64(1)
	}
	return serial, nil
}

func parseCertificate(data []byte) (*x509.Certificate, error) {
	data = bytes.TrimSpace(data)
	block, remainder := pem.Decode(data)
	if !bytes.HasPrefix(data, []byte("-----BEGIN CERTIFICATE-----")) || block == nil || block.Type != certificatePEMType || len(bytes.TrimSpace(remainder)) != 0 {
		return nil, errors.New("issuer must contain exactly one PEM certificate")
	}
	certificate, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		return nil, fmt.Errorf("parse issuer certificate: %w", err)
	}
	return certificate, nil
}

func encodeKeyPair(der []byte, key crypto.Signer) (KeyPair, error) {
	privateDER, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		return KeyPair{}, fmt.Errorf("encode private key: %w", err)
	}
	return KeyPair{
		Certificate: pem.EncodeToMemory(&pem.Block{Type: certificatePEMType, Bytes: der}),
		PrivateKey:  pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: privateDER}),
	}, nil
}
