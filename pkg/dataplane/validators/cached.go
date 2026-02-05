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

// ValidateServer validates a Server with caching.
func (c *CachedValidator) ValidateServer(m *models.Server) error {
	hash := c.set.HashServer(m)
	if result, ok := c.cache.Get(hash); ok {
		return result
	}
	result := c.set.ValidateServer(m)
	c.cache.Add(hash, result)
	return result
}

// ValidateServerTemplate validates a ServerTemplate with caching.
func (c *CachedValidator) ValidateServerTemplate(m *models.ServerTemplate) error {
	hash := c.set.HashServerTemplate(m)
	if result, ok := c.cache.Get(hash); ok {
		return result
	}
	result := c.set.ValidateServerTemplate(m)
	c.cache.Add(hash, result)
	return result
}

// ValidateBind validates a Bind with caching.
func (c *CachedValidator) ValidateBind(m *models.Bind) error {
	hash := c.set.HashBind(m)
	if result, ok := c.cache.Get(hash); ok {
		return result
	}
	result := c.set.ValidateBind(m)
	c.cache.Add(hash, result)
	return result
}

// ValidateHTTPRequestRule validates an HTTPRequestRule with caching.
func (c *CachedValidator) ValidateHTTPRequestRule(m *models.HTTPRequestRule) error {
	hash := c.set.HashHTTPRequestRule(m)
	if result, ok := c.cache.Get(hash); ok {
		return result
	}
	result := c.set.ValidateHTTPRequestRule(m)
	c.cache.Add(hash, result)
	return result
}

// ValidateHTTPResponseRule validates an HTTPResponseRule with caching.
func (c *CachedValidator) ValidateHTTPResponseRule(m *models.HTTPResponseRule) error {
	hash := c.set.HashHTTPResponseRule(m)
	if result, ok := c.cache.Get(hash); ok {
		return result
	}
	result := c.set.ValidateHTTPResponseRule(m)
	c.cache.Add(hash, result)
	return result
}

// ValidateTCPRequestRule validates a TCPRequestRule with caching.
func (c *CachedValidator) ValidateTCPRequestRule(m *models.TCPRequestRule) error {
	hash := c.set.HashTCPRequestRule(m)
	if result, ok := c.cache.Get(hash); ok {
		return result
	}
	result := c.set.ValidateTCPRequestRule(m)
	c.cache.Add(hash, result)
	return result
}

// ValidateTCPResponseRule validates a TCPResponseRule with caching.
func (c *CachedValidator) ValidateTCPResponseRule(m *models.TCPResponseRule) error {
	hash := c.set.HashTCPResponseRule(m)
	if result, ok := c.cache.Get(hash); ok {
		return result
	}
	result := c.set.ValidateTCPResponseRule(m)
	c.cache.Add(hash, result)
	return result
}

// ValidateHTTPAfterResponseRule validates an HTTPAfterResponseRule with caching.
func (c *CachedValidator) ValidateHTTPAfterResponseRule(m *models.HTTPAfterResponseRule) error {
	hash := c.set.HashHTTPAfterResponseRule(m)
	if result, ok := c.cache.Get(hash); ok {
		return result
	}
	result := c.set.ValidateHTTPAfterResponseRule(m)
	c.cache.Add(hash, result)
	return result
}

// ValidateHTTPErrorRule validates an HTTPErrorRule with caching.
func (c *CachedValidator) ValidateHTTPErrorRule(m *models.HTTPErrorRule) error {
	hash := c.set.HashHTTPErrorRule(m)
	if result, ok := c.cache.Get(hash); ok {
		return result
	}
	result := c.set.ValidateHTTPErrorRule(m)
	c.cache.Add(hash, result)
	return result
}

// ValidateServerSwitchingRule validates a ServerSwitchingRule with caching.
func (c *CachedValidator) ValidateServerSwitchingRule(m *models.ServerSwitchingRule) error {
	hash := c.set.HashServerSwitchingRule(m)
	if result, ok := c.cache.Get(hash); ok {
		return result
	}
	result := c.set.ValidateServerSwitchingRule(m)
	c.cache.Add(hash, result)
	return result
}

// ValidateBackendSwitchingRule validates a BackendSwitchingRule with caching.
func (c *CachedValidator) ValidateBackendSwitchingRule(m *models.BackendSwitchingRule) error {
	hash := c.set.HashBackendSwitchingRule(m)
	if result, ok := c.cache.Get(hash); ok {
		return result
	}
	result := c.set.ValidateBackendSwitchingRule(m)
	c.cache.Add(hash, result)
	return result
}

// ValidateStickRule validates a StickRule with caching.
func (c *CachedValidator) ValidateStickRule(m *models.StickRule) error {
	hash := c.set.HashStickRule(m)
	if result, ok := c.cache.Get(hash); ok {
		return result
	}
	result := c.set.ValidateStickRule(m)
	c.cache.Add(hash, result)
	return result
}

// ValidateACL validates an ACL with caching.
func (c *CachedValidator) ValidateACL(m *models.ACL) error {
	hash := c.set.HashACL(m)
	if result, ok := c.cache.Get(hash); ok {
		return result
	}
	result := c.set.ValidateACL(m)
	c.cache.Add(hash, result)
	return result
}

// ValidateFilter validates a Filter with caching.
func (c *CachedValidator) ValidateFilter(m *models.Filter) error {
	hash := c.set.HashFilter(m)
	if result, ok := c.cache.Get(hash); ok {
		return result
	}
	result := c.set.ValidateFilter(m)
	c.cache.Add(hash, result)
	return result
}

// ValidateLogTarget validates a LogTarget with caching.
func (c *CachedValidator) ValidateLogTarget(m *models.LogTarget) error {
	hash := c.set.HashLogTarget(m)
	if result, ok := c.cache.Get(hash); ok {
		return result
	}
	result := c.set.ValidateLogTarget(m)
	c.cache.Add(hash, result)
	return result
}

// ValidateHTTPCheck validates an HTTPCheck with caching.
func (c *CachedValidator) ValidateHTTPCheck(m *models.HTTPCheck) error {
	hash := c.set.HashHTTPCheck(m)
	if result, ok := c.cache.Get(hash); ok {
		return result
	}
	result := c.set.ValidateHTTPCheck(m)
	c.cache.Add(hash, result)
	return result
}

// ValidateTCPCheck validates a TCPCheck with caching.
func (c *CachedValidator) ValidateTCPCheck(m *models.TCPCheck) error {
	hash := c.set.HashTCPCheck(m)
	if result, ok := c.cache.Get(hash); ok {
		return result
	}
	result := c.set.ValidateTCPCheck(m)
	c.cache.Add(hash, result)
	return result
}

// ValidateCapture validates a Capture with caching.
func (c *CachedValidator) ValidateCapture(m *models.Capture) error {
	hash := c.set.HashCapture(m)
	if result, ok := c.cache.Get(hash); ok {
		return result
	}
	result := c.set.ValidateCapture(m)
	c.cache.Add(hash, result)
	return result
}
