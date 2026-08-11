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

package httpstore

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
)

type sourceSpec struct {
	options  FetchOptions
	auth     *AuthConfig
	identity string
}

type identityInput struct {
	Options FetchOptions
	Auth    *AuthConfig
}

// SourceIdentity returns an opaque identity for the effective fetch policy.
func SourceIdentity(opts FetchOptions, auth *AuthConfig) (string, error) {
	spec, err := normalizeSource(opts, auth)
	if err != nil {
		return "", err
	}
	return spec.identity, nil
}

func normalizeSource(opts FetchOptions, auth *AuthConfig) (sourceSpec, error) {
	options := opts.WithDefaults()
	normalizedAuthValue, err := normalizeAuth(auth)
	if err != nil {
		return sourceSpec{}, err
	}
	var normalizedAuth *AuthConfig
	if normalizedAuthValue.Type != "" || len(normalizedAuthValue.Headers) > 0 {
		normalizedAuth = &normalizedAuthValue
	}
	identityAuth := canonicalIdentityAuth(normalizedAuth)

	identity, err := hashIdentity(identityInput{Options: options, Auth: identityAuth})
	if err != nil {
		return sourceSpec{}, err
	}
	return sourceSpec{
		options:  options,
		auth:     normalizedAuth,
		identity: identity,
	}, nil
}

func normalizeAuth(auth *AuthConfig) (AuthConfig, error) {
	if auth == nil {
		return AuthConfig{}, nil
	}

	normalized := AuthConfig{Type: auth.Type}
	switch auth.Type {
	case AuthTypeBasic:
		if auth.Username == "" && auth.Password == "" {
			return AuthConfig{}, nil
		}
		normalized.Username = auth.Username
		normalized.Password = auth.Password
	case AuthTypeBearer:
		if auth.Token == "" {
			return AuthConfig{}, nil
		}
		normalized.Token = auth.Token
	default:
		headers := make(map[string]string, len(auth.Headers))
		canonicalValues := make(map[string]string, len(auth.Headers))
		for name, value := range auth.Headers {
			canonicalName := http.CanonicalHeaderKey(name)
			if previous, exists := canonicalValues[canonicalName]; exists && previous != value {
				return AuthConfig{}, fmt.Errorf("authentication header %q has conflicting values", canonicalName)
			}
			canonicalValues[canonicalName] = value
			headers[name] = value
		}
		if len(headers) > 0 {
			normalized.Headers = headers
		} else {
			return AuthConfig{}, nil
		}
	}
	return normalized, nil
}

func canonicalIdentityAuth(auth *AuthConfig) *AuthConfig {
	if auth == nil || len(auth.Headers) == 0 {
		return auth
	}

	canonical := *auth
	canonical.Headers = make(map[string]string, len(auth.Headers))
	for name, value := range auth.Headers {
		canonical.Headers[http.CanonicalHeaderKey(name)] = value
	}
	return &canonical
}

func hashIdentity(value any) (string, error) {
	encoded, err := json.Marshal(value)
	if err != nil {
		return "", fmt.Errorf("encoding HTTP source identity: %w", err)
	}
	digest := sha256.Sum256(encoded)
	return hex.EncodeToString(digest[:]), nil
}
