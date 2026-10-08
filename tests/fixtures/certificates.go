// Copyright 2026 Philipp Hossner
// SPDX-License-Identifier: Apache-2.0

package fixtures

import (
	"crypto"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/pem"
	"errors"
	"fmt"
)

type KeyAlgorithm int

const (
	RSA2048 KeyAlgorithm = iota
	ECDSAP256
)

type Certificate struct {
	Parsed *x509.Certificate
	Key    crypto.Signer
	DER    []byte
	PEM    []byte
	KeyPEM []byte
}

// NewCertificate self-signs when authority is nil. Each call generates a fresh key.
func NewCertificate(template *x509.Certificate, algorithm KeyAlgorithm, authority *Certificate) (*Certificate, error) {
	if template == nil {
		return nil, errors.New("certificate template is required")
	}
	key, keyPEM, err := certificateKey(algorithm)
	if err != nil {
		return nil, err
	}
	parent, signer := template, key
	if authority != nil {
		if authority.Parsed == nil || authority.Key == nil {
			return nil, errors.New("certificate authority needs its certificate and signing key")
		}
		parent, signer = authority.Parsed, authority.Key
	}
	der, err := x509.CreateCertificate(rand.Reader, template, parent, key.Public(), signer)
	if err != nil {
		return nil, fmt.Errorf("create certificate: %w", err)
	}
	parsed, err := x509.ParseCertificate(der)
	if err != nil {
		return nil, err
	}
	return &Certificate{Parsed: parsed, Key: key, DER: der, PEM: pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), KeyPEM: keyPEM}, nil
}

func certificateKey(algorithm KeyAlgorithm) (crypto.Signer, []byte, error) {
	switch algorithm {
	case RSA2048:
		key, err := rsa.GenerateKey(rand.Reader, 2048)
		if err != nil {
			return nil, nil, err
		}
		return key, pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(key)}), nil
	case ECDSAP256:
		key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
		if err != nil {
			return nil, nil, err
		}
		der, err := x509.MarshalECPrivateKey(key)
		if err != nil {
			return nil, nil, err
		}
		return key, pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: der}), nil
	default:
		return nil, nil, fmt.Errorf("unknown certificate key algorithm %d", algorithm)
	}
}
