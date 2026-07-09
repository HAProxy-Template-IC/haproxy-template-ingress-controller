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

//go:build js && wasm

package main

import (
	"crypto/ecdsa"
	"crypto/ed25519"
	"crypto/rsa"
	"crypto/x509"
	"encoding/pem"
	"fmt"
	"strings"
	"time"
)

// decodeCerts parses each rendered SSL-certificate file's PEM into concise,
// JS-ready info for the certs tab: one summary per certificate plus any
// private-key note and parse errors. Playground display only — the controller
// never decodes what it deploys; this is purely to make the tab readable.
func decodeCerts(certs map[string]string, now time.Time) map[string]any {
	out := make(map[string]any, len(certs))
	for path, content := range certs {
		out[path] = decodeCertFile(content, now)
	}
	return out
}

func decodeCertFile(content string, now time.Time) map[string]any {
	certsOut := []any{}
	keys := []any{}
	errs := []any{}
	rest := []byte(content)
	for {
		var block *pem.Block
		block, rest = pem.Decode(rest)
		if block == nil {
			break
		}
		switch {
		case block.Type == "CERTIFICATE":
			c, err := x509.ParseCertificate(block.Bytes)
			if err != nil {
				errs = append(errs, "certificate: "+err.Error())
				continue
			}
			certsOut = append(certsOut, certToJS(c, now))
		case strings.Contains(block.Type, "PRIVATE KEY"):
			keys = append(keys, keyNote(block))
		}
	}
	return map[string]any{"certs": certsOut, "keys": keys, "errors": errs}
}

func certToJS(c *x509.Certificate, now time.Time) map[string]any {
	sans := []any{}
	for _, d := range c.DNSNames {
		sans = append(sans, d)
	}
	for _, ip := range c.IPAddresses {
		sans = append(sans, ip.String())
	}
	for _, u := range c.URIs {
		sans = append(sans, u.String())
	}
	for _, e := range c.EmailAddresses {
		sans = append(sans, e)
	}
	subject := c.Subject.CommonName
	if subject == "" && len(c.DNSNames) > 0 {
		subject = c.DNSNames[0]
	}
	return map[string]any{
		"subject":     subject,
		"subjectFull": c.Subject.String(),
		"issuer":      c.Issuer.CommonName,
		"issuerFull":  c.Issuer.String(),
		"sans":        sans,
		"notBefore":   c.NotBefore.UTC().Format("2006-01-02"),
		"notAfter":    c.NotAfter.UTC().Format("2006-01-02"),
		"expired":     now.After(c.NotAfter),
		"notYetValid": now.Before(c.NotBefore),
		"daysLeft":    float64(int(c.NotAfter.Sub(now).Hours() / 24)),
		"serial":      c.SerialNumber.Text(16),
		"keyType":     keyType(c.PublicKey, c.PublicKeyAlgorithm.String()),
		"isCA":        c.IsCA,
		"selfSigned":  c.Subject.String() == c.Issuer.String(),
		"sigAlg":      c.SignatureAlgorithm.String(),
		"version":     float64(c.Version),
	}
}

// keyType names a public key by algorithm and strength (e.g. "RSA 2048",
// "ECDSA P-256"), falling back to the algorithm name.
func keyType(pub any, fallback string) string {
	switch k := pub.(type) {
	case *rsa.PublicKey:
		return fmt.Sprintf("RSA %d", k.N.BitLen())
	case *ecdsa.PublicKey:
		return "ECDSA " + k.Curve.Params().Name
	case ed25519.PublicKey:
		return "Ed25519"
	default:
		return fallback
	}
}

// keyNote describes a private-key PEM block (type and, where cheaply available,
// strength) without exposing any secret material.
func keyNote(block *pem.Block) string {
	if k, err := x509.ParsePKCS1PrivateKey(block.Bytes); err == nil {
		return fmt.Sprintf("RSA private key (%d-bit)", k.N.BitLen())
	}
	if k, err := x509.ParseECPrivateKey(block.Bytes); err == nil {
		return "ECDSA private key (" + k.Curve.Params().Name + ")"
	}
	if k, err := x509.ParsePKCS8PrivateKey(block.Bytes); err == nil {
		switch key := k.(type) {
		case *rsa.PrivateKey:
			return fmt.Sprintf("RSA private key (%d-bit)", key.N.BitLen())
		case *ecdsa.PrivateKey:
			return "ECDSA private key (" + key.Curve.Params().Name + ")"
		case ed25519.PrivateKey:
			return "Ed25519 private key"
		}
	}
	return strings.ToLower(block.Type)
}
