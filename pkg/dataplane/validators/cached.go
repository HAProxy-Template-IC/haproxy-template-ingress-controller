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

// NewCachedValidatorWithCache creates a cached validator with a pre-existing cache.
// Use this to share a cache across multiple validator instances.
func NewCachedValidatorWithCache(cache *Cache, major, minor int) *CachedValidator {
	return &CachedValidator{
		cache: cache,
		set:   ForVersion(major, minor),
	}
}

// ValidatorSet returns the underlying validator set.
func (c *CachedValidator) ValidatorSet() *ValidatorSet {
	return c.set
}

// Cache returns the underlying cache.
func (c *CachedValidator) Cache() *Cache {
	return c.cache
}

// validateCached looks up a cached validation result keyed by the model's
// content hash, falling back to running the validator and caching the outcome.
func validateCached[T any](c *CachedValidator, m T, hasher func(T) uint64, validator func(T) error) error {
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
	return validateCached(c, m, c.set.HashServer, c.set.ValidateServer)
}

// ValidateServerTemplate validates a ServerTemplate with caching.
func (c *CachedValidator) ValidateServerTemplate(m *models.ServerTemplate) error {
	return validateCached(c, m, c.set.HashServerTemplate, c.set.ValidateServerTemplate)
}

// ValidateBind validates a Bind with caching.
func (c *CachedValidator) ValidateBind(m *models.Bind) error {
	return validateCached(c, m, c.set.HashBind, c.set.ValidateBind)
}

// ValidateHTTPRequestRule validates an HTTPRequestRule with caching.
func (c *CachedValidator) ValidateHTTPRequestRule(m *models.HTTPRequestRule) error {
	return validateCached(c, m, c.set.HashHTTPRequestRule, c.set.ValidateHTTPRequestRule)
}

// ValidateHTTPResponseRule validates an HTTPResponseRule with caching.
func (c *CachedValidator) ValidateHTTPResponseRule(m *models.HTTPResponseRule) error {
	return validateCached(c, m, c.set.HashHTTPResponseRule, c.set.ValidateHTTPResponseRule)
}

// ValidateTCPRequestRule validates a TCPRequestRule with caching.
func (c *CachedValidator) ValidateTCPRequestRule(m *models.TCPRequestRule) error {
	return validateCached(c, m, c.set.HashTCPRequestRule, c.set.ValidateTCPRequestRule)
}

// ValidateTCPResponseRule validates a TCPResponseRule with caching.
func (c *CachedValidator) ValidateTCPResponseRule(m *models.TCPResponseRule) error {
	return validateCached(c, m, c.set.HashTCPResponseRule, c.set.ValidateTCPResponseRule)
}

// ValidateHTTPAfterResponseRule validates an HTTPAfterResponseRule with caching.
func (c *CachedValidator) ValidateHTTPAfterResponseRule(m *models.HTTPAfterResponseRule) error {
	return validateCached(c, m, c.set.HashHTTPAfterResponseRule, c.set.ValidateHTTPAfterResponseRule)
}

// ValidateHTTPErrorRule validates an HTTPErrorRule with caching.
func (c *CachedValidator) ValidateHTTPErrorRule(m *models.HTTPErrorRule) error {
	return validateCached(c, m, c.set.HashHTTPErrorRule, c.set.ValidateHTTPErrorRule)
}

// ValidateServerSwitchingRule validates a ServerSwitchingRule with caching.
func (c *CachedValidator) ValidateServerSwitchingRule(m *models.ServerSwitchingRule) error {
	return validateCached(c, m, c.set.HashServerSwitchingRule, c.set.ValidateServerSwitchingRule)
}

// ValidateBackendSwitchingRule validates a BackendSwitchingRule with caching.
func (c *CachedValidator) ValidateBackendSwitchingRule(m *models.BackendSwitchingRule) error {
	return validateCached(c, m, c.set.HashBackendSwitchingRule, c.set.ValidateBackendSwitchingRule)
}

// ValidateStickRule validates a StickRule with caching.
func (c *CachedValidator) ValidateStickRule(m *models.StickRule) error {
	return validateCached(c, m, c.set.HashStickRule, c.set.ValidateStickRule)
}

// ValidateACL validates an ACL with caching.
func (c *CachedValidator) ValidateACL(m *models.ACL) error {
	return validateCached(c, m, c.set.HashACL, c.set.ValidateACL)
}

// ValidateFilter validates a Filter with caching.
func (c *CachedValidator) ValidateFilter(m *models.Filter) error {
	return validateCached(c, m, c.set.HashFilter, c.set.ValidateFilter)
}

// ValidateLogTarget validates a LogTarget with caching.
func (c *CachedValidator) ValidateLogTarget(m *models.LogTarget) error {
	return validateCached(c, m, c.set.HashLogTarget, c.set.ValidateLogTarget)
}

// ValidateHTTPCheck validates an HTTPCheck with caching.
func (c *CachedValidator) ValidateHTTPCheck(m *models.HTTPCheck) error {
	return validateCached(c, m, c.set.HashHTTPCheck, c.set.ValidateHTTPCheck)
}

// ValidateTCPCheck validates a TCPCheck with caching.
func (c *CachedValidator) ValidateTCPCheck(m *models.TCPCheck) error {
	return validateCached(c, m, c.set.HashTCPCheck, c.set.ValidateTCPCheck)
}

// ValidateCapture validates a Capture with caching.
func (c *CachedValidator) ValidateCapture(m *models.Capture) error {
	return validateCached(c, m, c.set.HashCapture, c.set.ValidateCapture)
}
