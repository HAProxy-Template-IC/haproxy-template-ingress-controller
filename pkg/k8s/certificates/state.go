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

package certificates

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"time"

	"gitlab.com/haproxy-haptic/haptic/pkg/transportsecurity/issuance"
)

type identity struct {
	Target
	Pair issuance.KeyPair `json:"pair"`
}

type generation struct {
	Version       int              `json:"version"`
	Authority     issuance.KeyPair `json:"authority"`
	Identities    []identity       `json:"identities"`
	PreviousCA    []byte           `json:"previous_ca,omitempty"`
	PreviousUntil time.Time        `json:"previous_until,omitzero"`
}

func newGeneration(cfg *Config, now time.Time, previous *generation) (*generation, error) {
	now = now.UTC().Truncate(time.Second)
	ca, err := issuance.NewAuthority(cfg.IssuerSecret, now, cfg.Lifetime)
	if err != nil {
		return nil, err
	}
	pair, err := ca.KeyPair()
	if err != nil {
		return nil, err
	}
	result := &generation{Version: 1, Authority: pair}
	bridge, err := result.prepareOverlap(ca, previous, now, cfg.Overlap)
	if err != nil {
		return nil, err
	}
	for _, target := range cfg.Targets {
		pair, err := ca.Issue(target.DNSName, target.Usage, now, cfg.Lifetime)
		if err != nil {
			return nil, fmt.Errorf("issue identity %s: %w", target.SecretName, err)
		}
		pair.Certificate = append(pair.Certificate, bridge...)
		result.Identities = append(result.Identities, identity{Target: target, Pair: pair})
	}
	return result, nil
}

func (g *generation) prepareOverlap(next *issuance.Authority, previous *generation, now time.Time, overlap time.Duration) ([]byte, error) {
	if previous == nil {
		return []byte{}, nil
	}
	old, err := issuance.ParseAuthority(previous.Authority)
	if err != nil {
		return nil, err
	}
	if !old.ExpiresAt().After(now) {
		return []byte{}, nil
	}
	until := now.Add(overlap)
	if until.After(old.ExpiresAt()) {
		until = old.ExpiresAt()
	}
	bridge, err := old.CrossSign(next, now, until)
	if err != nil {
		return nil, err
	}
	g.PreviousCA = previous.Authority.Certificate
	g.PreviousUntil = until
	return bridge, nil
}

func readGeneration(data []byte, cfg *Config) (*generation, time.Time, error) {
	var result generation
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&result); err != nil {
		return nil, time.Time{}, errors.New("issuer Secret contains invalid certificate state; restore its backup")
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		return nil, time.Time{}, errors.New("issuer Secret contains trailing certificate state; restore its backup")
	}
	if result.Version != 1 || len(result.Identities) != len(cfg.Targets) {
		return nil, time.Time{}, errors.New("issuer Secret has an unsupported version or identity set; restore its configuration")
	}
	expires, err := result.validate(cfg)
	if err != nil {
		return nil, time.Time{}, err
	}
	return &result, expires, nil
}

func (g *generation) validate(cfg *Config) (time.Time, error) {
	ca, err := issuance.ParseAuthority(g.Authority)
	if err != nil {
		return time.Time{}, fmt.Errorf("invalid issuer Secret authority: %w", err)
	}
	expires := ca.ExpiresAt()
	for index, target := range cfg.Targets {
		stored := g.Identities[index]
		if stored.Target != target {
			return time.Time{}, errors.New("managed identity names or usages changed; restore the installed TLS settings")
		}
		identityExpiry, err := ca.CheckIdentity(stored.Pair, target.DNSName, target.Usage)
		if err != nil {
			return time.Time{}, fmt.Errorf("invalid identity %s in issuer Secret: %w", target.SecretName, err)
		}
		if err := ca.CheckTransition(stored.Pair, g.PreviousCA, g.PreviousUntil); err != nil {
			return time.Time{}, fmt.Errorf("invalid identity %s transition in issuer Secret: %w", target.SecretName, err)
		}
		if identityExpiry.Before(expires) {
			expires = identityExpiry
		}
	}
	if (len(g.PreviousCA) == 0) != g.PreviousUntil.IsZero() {
		return time.Time{}, errors.New("issuer Secret has incomplete previous CA trust; restore its backup")
	}
	return expires, nil
}

func (g *generation) identityData(identity *identity, now time.Time) map[string][]byte {
	data := map[string][]byte{
		"tls.crt": identity.Pair.Certificate,
		"tls.key": identity.Pair.PrivateKey,
		"ca.crt":  g.Authority.Certificate,
	}
	if now.Before(g.PreviousUntil) {
		data["previous-ca.crt"] = g.PreviousCA
		data["previous-ca-until"] = []byte(g.PreviousUntil.UTC().Format(time.RFC3339))
	}
	return data
}
