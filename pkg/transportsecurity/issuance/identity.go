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
	"bytes"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"regexp"
	"slices"
	"strings"
	"time"
)

var dnsLabel = regexp.MustCompile(`^[a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?$`)

func (a *Authority) Issue(dnsName string, usage x509.ExtKeyUsage, now time.Time, lifetime time.Duration) (KeyPair, error) {
	if !validDNSName(dnsName) || lifetime < time.Second {
		return KeyPair{}, errors.New("an exact DNS identity and a lifetime of at least one second are required")
	}
	if usage != x509.ExtKeyUsageClientAuth && usage != x509.ExtKeyUsageServerAuth {
		return KeyPair{}, errors.New("identity must have either client or server authentication usage")
	}
	until := now.Add(lifetime).UTC().Truncate(time.Second)
	if err := a.validDuring(now, until); err != nil {
		return KeyPair{}, err
	}
	serial, err := serialNumber()
	if err != nil {
		return KeyPair{}, err
	}
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return KeyPair{}, fmt.Errorf("generate identity key: %w", err)
	}
	template := &x509.Certificate{
		SerialNumber: serial, DNSNames: []string{dnsName},
		NotBefore: a.backdate(now), NotAfter: until, BasicConstraintsValid: true,
		KeyUsage: x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{usage},
	}
	der, err := x509.CreateCertificate(rand.Reader, template, a.certificate, key.Public(), a.key)
	if err != nil {
		return KeyPair{}, fmt.Errorf("issue identity certificate: %w", err)
	}
	return encodeKeyPair(der, key)
}

func validDNSName(name string) bool {
	if name == "" || len(name) > 253 {
		return false
	}
	for _, label := range strings.Split(name, ".") {
		if !dnsLabel.MatchString(label) {
			return false
		}
	}
	return true
}

// CheckIdentity verifies persisted identity material even after its expiry.
func (a *Authority) CheckIdentity(pair KeyPair, name string, usage x509.ExtKeyUsage) (time.Time, error) {
	identity, err := tls.X509KeyPair(pair.Certificate, pair.PrivateKey)
	if err != nil {
		return time.Time{}, fmt.Errorf("load identity key pair: %w", err)
	}
	leaf := identity.Leaf
	if leaf.IsCA || !slices.Equal(leaf.DNSNames, []string{name}) || !slices.Equal(leaf.ExtKeyUsage, []x509.ExtKeyUsage{usage}) {
		return time.Time{}, errors.New("persisted identity has unexpected names or certificate usages")
	}
	if leaf.NotBefore.Before(a.certificate.NotBefore) || leaf.NotAfter.After(a.certificate.NotAfter) || !leaf.NotAfter.After(leaf.NotBefore) {
		return time.Time{}, errors.New("persisted identity validity exceeds its CA validity")
	}
	if err := leaf.CheckSignatureFrom(a.certificate); err != nil {
		return time.Time{}, fmt.Errorf("verify persisted identity issuer: %w", err)
	}
	return leaf.NotAfter, nil
}

func (a *Authority) CheckTransition(pair KeyPair, previousCA []byte, until time.Time) error {
	identity, err := tls.X509KeyPair(pair.Certificate, pair.PrivateKey)
	if err != nil {
		return fmt.Errorf("load transition identity: %w", err)
	}
	if len(previousCA) == 0 && until.IsZero() {
		if len(identity.Certificate) != 1 {
			return errors.New("identity has a transition certificate without previous CA trust")
		}
		return nil
	}
	if len(identity.Certificate) != 2 || until.IsZero() || until.Sub(a.certificate.NotBefore) > 24*time.Hour+validityBackdate {
		return errors.New("identity transition needs one bridge and a bounded trust deadline")
	}
	previous, err := parseCertificate(previousCA)
	if err != nil {
		return fmt.Errorf("parse previous CA: %w", err)
	}
	if err := previous.CheckSignatureFrom(previous); err != nil {
		return fmt.Errorf("previous CA must be self-signed: %w", err)
	}
	bridge, err := x509.ParseCertificate(identity.Certificate[1])
	if err != nil {
		return fmt.Errorf("parse transition certificate: %w", err)
	}
	return a.checkBridge(bridge, previous, until)
}

func (a *Authority) checkBridge(bridge, previous *x509.Certificate, until time.Time) error {
	if !bridge.IsCA || !bridge.BasicConstraintsValid || !bridge.MaxPathLenZero ||
		!bytes.Equal(bridge.RawSubjectPublicKeyInfo, a.certificate.RawSubjectPublicKeyInfo) ||
		!bytes.Equal(bridge.RawSubject, a.certificate.RawSubject) {
		return errors.New("transition certificate does not identify the current CA")
	}
	if !bridge.NotAfter.Equal(until) || !until.After(bridge.NotBefore) ||
		bridge.NotBefore.Before(previous.NotBefore) || until.After(previous.NotAfter) || until.After(a.certificate.NotAfter) {
		return errors.New("transition certificate validity does not match its trust deadline")
	}
	if err := bridge.CheckSignatureFrom(previous); err != nil {
		return fmt.Errorf("verify transition certificate: %w", err)
	}
	return nil
}
