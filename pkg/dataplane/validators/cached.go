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

package validators

import (
	"github.com/haproxytech/client-native/v6/models"
)

// CachedValidator provides cached validation for HAProxy models.
// It combines a Cache with a ValidatorSet to provide high-performance
// validation with content-based caching.
type CachedValidator struct {
	cache *Cache
	set   *ValidatorSet
}

// NewCachedValidator creates a new cached validator for a specific HAProxy version.
func NewCachedValidator(major, minor int) *CachedValidator {
	return &CachedValidator{
		cache: NewCache(),
		set:   ForVersion(major, minor),
	}
}

// validateCached looks up a cached validation result keyed by the model's
// content hash, falling back to running the validator and caching the outcome.
//
// hasher and validator are the ValidatorSet's per-type function-value fields,
// which can legitimately be nil: HAProxy DataPlane API versions have different
// validator surface areas, so a ValidatorSet built for a version that lacks a
// feature leaves the corresponding fields nil. A nil validator means "no
// validation performed, treated as valid" (returns nil), matching the previous
// callIfSet semantics. When there is no validator there is nothing to cache.
func validateCached[T any](c *CachedValidator, m T, hasher func(T) uint64, validator func(T) error) error {
	if validator == nil {
		return nil
	}
	if hasher == nil {
		return validator(m)
	}
	hash := hasher(m)
	if result, ok := c.cache.Get(hash); ok {
		return result
	}
	result := validator(m)
	c.cache.Add(hash, result)
	return result
}

// ValidateServer validates a Server with caching.
func (c *CachedValidator) ValidateServer(m *models.Server) error {
	return validateCached(c, m, c.set.hashServer, c.set.validateServer)
}

// ValidateServerTemplate validates a ServerTemplate with caching.
func (c *CachedValidator) ValidateServerTemplate(m *models.ServerTemplate) error {
	return validateCached(c, m, c.set.hashServerTemplate, c.set.validateServerTemplate)
}

// ValidateBind validates a Bind with caching.
func (c *CachedValidator) ValidateBind(m *models.Bind) error {
	return validateCached(c, m, c.set.hashBind, c.set.validateBind)
}

// ValidateHTTPRequestRule validates an HTTPRequestRule with caching.
func (c *CachedValidator) ValidateHTTPRequestRule(m *models.HTTPRequestRule) error {
	return validateCached(c, m, c.set.hashHTTPRequestRule, c.set.validateHTTPRequestRule)
}

// ValidateHTTPResponseRule validates an HTTPResponseRule with caching.
func (c *CachedValidator) ValidateHTTPResponseRule(m *models.HTTPResponseRule) error {
	return validateCached(c, m, c.set.hashHTTPResponseRule, c.set.validateHTTPResponseRule)
}

// ValidateTCPRequestRule validates a TCPRequestRule with caching.
func (c *CachedValidator) ValidateTCPRequestRule(m *models.TCPRequestRule) error {
	return validateCached(c, m, c.set.hashTCPRequestRule, c.set.validateTCPRequestRule)
}

// ValidateTCPResponseRule validates a TCPResponseRule with caching.
func (c *CachedValidator) ValidateTCPResponseRule(m *models.TCPResponseRule) error {
	return validateCached(c, m, c.set.hashTCPResponseRule, c.set.validateTCPResponseRule)
}

// ValidateHTTPAfterResponseRule validates an HTTPAfterResponseRule with caching.
func (c *CachedValidator) ValidateHTTPAfterResponseRule(m *models.HTTPAfterResponseRule) error {
	return validateCached(c, m, c.set.hashHTTPAfterResponse, c.set.validateHTTPAfterResponse)
}

// ValidateHTTPErrorRule validates an HTTPErrorRule with caching.
func (c *CachedValidator) ValidateHTTPErrorRule(m *models.HTTPErrorRule) error {
	return validateCached(c, m, c.set.hashHTTPErrorRule, c.set.validateHTTPErrorRule)
}

// ValidateServerSwitchingRule validates a ServerSwitchingRule with caching.
func (c *CachedValidator) ValidateServerSwitchingRule(m *models.ServerSwitchingRule) error {
	return validateCached(c, m, c.set.hashServerSwitchingRule, c.set.validateServerSwitchingRule)
}

// ValidateBackendSwitchingRule validates a BackendSwitchingRule with caching.
func (c *CachedValidator) ValidateBackendSwitchingRule(m *models.BackendSwitchingRule) error {
	return validateCached(c, m, c.set.hashBackendSwitching, c.set.validateBackendSwitching)
}

// ValidateStickRule validates a StickRule with caching.
func (c *CachedValidator) ValidateStickRule(m *models.StickRule) error {
	return validateCached(c, m, c.set.hashStickRule, c.set.validateStickRule)
}

// ValidateACL validates an ACL with caching.
func (c *CachedValidator) ValidateACL(m *models.ACL) error {
	return validateCached(c, m, c.set.hashACL, c.set.validateACL)
}

// ValidateFilter validates a Filter with caching.
func (c *CachedValidator) ValidateFilter(m *models.Filter) error {
	return validateCached(c, m, c.set.hashFilter, c.set.validateFilter)
}

// ValidateLogTarget validates a LogTarget with caching.
func (c *CachedValidator) ValidateLogTarget(m *models.LogTarget) error {
	return validateCached(c, m, c.set.hashLogTarget, c.set.validateLogTarget)
}

// ValidateHTTPCheck validates an HTTPCheck with caching.
func (c *CachedValidator) ValidateHTTPCheck(m *models.HTTPCheck) error {
	return validateCached(c, m, c.set.hashHTTPCheck, c.set.validateHTTPCheck)
}

// ValidateTCPCheck validates a TCPCheck with caching.
func (c *CachedValidator) ValidateTCPCheck(m *models.TCPCheck) error {
	return validateCached(c, m, c.set.hashTCPCheck, c.set.validateTCPCheck)
}

// ValidateCapture validates a Capture with caching.
func (c *CachedValidator) ValidateCapture(m *models.Capture) error {
	return validateCached(c, m, c.set.hashCapture, c.set.validateCapture)
}
