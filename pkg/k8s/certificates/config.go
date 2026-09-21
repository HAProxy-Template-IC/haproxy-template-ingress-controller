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

// Package certificates renews certificate authorities and identity Secrets.
package certificates

import (
	"crypto/x509"
	"errors"
	"fmt"
	"time"

	"k8s.io/apimachinery/pkg/util/validation"
)

type Target struct {
	SecretName string           `json:"secret_name"`
	DNSName    string           `json:"dns_name"`
	Usage      x509.ExtKeyUsage `json:"usage"`
}

type Config struct {
	Namespace    string
	IssuerSecret string
	Targets      []Target
	Lifetime     time.Duration
	RenewBefore  time.Duration
	Overlap      time.Duration
}

func (c *Config) validate() error {
	if len(validation.IsDNS1123Label(c.Namespace)) != 0 || len(validation.IsDNS1123Subdomain(c.IssuerSecret)) != 0 {
		return errors.New("certificate namespace and issuer Secret name must be valid Kubernetes names")
	}
	if c.Lifetime <= 0 || c.RenewBefore <= 0 || c.RenewBefore >= c.Lifetime {
		return errors.New("certificate renewal lead time must be positive and shorter than the lifetime")
	}
	if c.Overlap <= 0 || c.Overlap > 24*time.Hour || c.Overlap > c.RenewBefore {
		return errors.New("certificate trust overlap must be positive and fit within 24 hours and the renewal lead time")
	}
	if len(c.Targets) == 0 {
		return errors.New("at least one certificate identity is required")
	}
	seen := map[string]bool{c.IssuerSecret: true}
	for _, target := range c.Targets {
		if len(validation.IsDNS1123Subdomain(target.SecretName)) != 0 || seen[target.SecretName] {
			return fmt.Errorf("identity Secret %q must have a valid, distinct name", target.SecretName)
		}
		seen[target.SecretName] = true
		if len(validation.IsDNS1123Subdomain(target.DNSName)) != 0 {
			return fmt.Errorf("identity Secret %q needs an exact DNS name", target.SecretName)
		}
		if target.Usage != x509.ExtKeyUsageClientAuth && target.Usage != x509.ExtKeyUsageServerAuth {
			return fmt.Errorf("identity Secret %q needs client or server authentication usage", target.SecretName)
		}
	}
	return nil
}

type Result struct {
	Created   bool
	Renewed   bool
	ExpiresAt time.Time
}
