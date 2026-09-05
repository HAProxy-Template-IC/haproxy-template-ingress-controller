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

package rendercontext

import (
	"errors"
	"reflect"
	"sync"
	"sync/atomic"

	"gitlab.com/haproxy-haptic/haptic/pkg/templating"
)

// ResourceItemCache reuses immutable typed resource projections within one render.
type ResourceItemCache struct {
	seal *ResourceItemCache

	mu      sync.Mutex
	entries map[resourceItemCacheKey]*wrappedResourceItem
	frames  sync.Map
	revoked atomic.Bool
}

type resourceItemCacheKey struct {
	resourceName string
	elementType  reflect.Type
	sourceType   reflect.Type
	source       uintptr
}

type wrappedResourceItem struct {
	seal        *wrappedResourceItem
	key         resourceItemCacheKey
	value       reflect.Value
	certificate *templating.IncrementalImmutableCertificate
	source      any
}

// NewResourceItemCache returns an empty render-scoped cache.
func NewResourceItemCache() *ResourceItemCache {
	cache := &ResourceItemCache{entries: make(map[resourceItemCacheKey]*wrappedResourceItem)}
	cache.seal = cache
	return cache
}

func (c *ResourceItemCache) valid() bool {
	return c != nil && c.seal == c && !c.revoked.Load()
}

// Revoke drops all render-only resource materializations.
func (c *ResourceItemCache) Revoke() {
	if c == nil || !c.revoked.CompareAndSwap(false, true) {
		return
	}
	c.mu.Lock()
	c.entries = nil
	c.mu.Unlock()
	c.frames.Clear()
}

func (c *ResourceItemCache) load(
	key resourceItemCacheKey,
	source any,
) (*wrappedResourceItem, bool, error) {
	if !c.valid() {
		return nil, false, errors.New("resource item cache has invalid provenance")
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if !c.valid() || c.entries == nil {
		return nil, false, errors.New("resource item cache has invalid provenance")
	}
	entry, found := c.entries[key]
	if !found {
		return nil, false, nil
	}
	if !entry.valid(key, source) {
		return nil, false, errors.New("resource item cache entry has invalid provenance")
	}
	return entry, true, nil
}

func (c *ResourceItemCache) loadOrStore(
	key resourceItemCacheKey,
	source any,
	value reflect.Value,
	certificate *templating.IncrementalImmutableCertificate,
) (*wrappedResourceItem, error) {
	expectedKey, keyable := resourceItemKey(key.resourceName, key.elementType, source)
	if !c.valid() || !keyable || expectedKey != key || key.elementType == nil || !value.IsValid() ||
		value.Type() != reflect.PointerTo(key.elementType) || certificate == nil ||
		!certificate.Guards(value.Interface()) {
		return nil, errors.New("resource item cache candidate has invalid provenance")
	}
	candidate := &wrappedResourceItem{
		key: key, value: value, certificate: certificate, source: source,
	}
	candidate.seal = candidate
	c.mu.Lock()
	defer c.mu.Unlock()
	if !c.valid() || c.entries == nil {
		return nil, errors.New("resource item cache has invalid provenance")
	}
	if entry := c.entries[key]; entry != nil {
		if !entry.valid(key, source) {
			return nil, errors.New("resource item cache entry has invalid provenance")
		}
		return entry, nil
	}
	c.entries[key] = candidate
	return candidate, nil
}

func (e *wrappedResourceItem) valid(key resourceItemCacheKey, source any) bool {
	expectedKey, keyable := resourceItemKey(key.resourceName, key.elementType, source)
	return e != nil && e.seal == e && e.key == key && sameResourceItemSource(e.source, source) &&
		keyable && expectedKey == key && key.elementType != nil && e.value.IsValid() &&
		e.value.Type() == reflect.PointerTo(key.elementType) && e.certificate != nil &&
		e.certificate.Guards(e.value.Interface())
}

func resourceItemKey(resourceName string, elementType reflect.Type, source any) (resourceItemCacheKey, bool) {
	value := reflect.ValueOf(source)
	if !value.IsValid() || (value.Kind() != reflect.Map && value.Kind() != reflect.Pointer) || value.IsNil() {
		return resourceItemCacheKey{}, false
	}
	return resourceItemCacheKey{
		resourceName: resourceName,
		elementType:  elementType,
		sourceType:   value.Type(),
		source:       value.Pointer(),
	}, true
}

func sameResourceItemSource(left, right any) bool {
	leftValue := reflect.ValueOf(left)
	rightValue := reflect.ValueOf(right)
	if !leftValue.IsValid() || !rightValue.IsValid() || leftValue.Type() != rightValue.Type() ||
		(leftValue.Kind() != reflect.Map && leftValue.Kind() != reflect.Pointer) {
		return false
	}
	return leftValue.IsNil() == rightValue.IsNil() && !leftValue.IsNil() && leftValue.Pointer() == rightValue.Pointer()
}
