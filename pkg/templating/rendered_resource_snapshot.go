// Copyright 2026 Philipp Hossner
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
// http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package templating

import (
	"errors"
	"fmt"
	"reflect"
	"slices"
	"strings"
)

type renderedResourceSnapshotEntry struct {
	namespace  string
	name       string
	apiVersion string
	kind       string
	object     statusPatchProjectionValue

	// createOnlyFields travels with the entry: the applier needs the paths the
	// configuration declared, and a snapshot that dropped them re-applied them
	// on a live cluster while every unit test passed.
	createOnlyFields []string
}

type renderedResourceSnapshotStorage struct {
	entries []*renderedResourceSnapshotEntry
}

type renderedResourceSnapshotAuthentication struct {
	owner   *RenderedResourceSnapshot
	storage *renderedResourceSnapshotStorage
	count   int
}

// RenderedResourceSnapshot is an authenticated immutable desired-resource set.
type RenderedResourceSnapshot struct {
	storage *renderedResourceSnapshotStorage
	auth    renderedResourceSnapshotAuthentication
	seal    *RenderedResourceSnapshot
}

// Snapshot freezes the collector and reuses previous when every resource is exact.
func (c *RenderedResourceCollector) Snapshot(
	previous ...*RenderedResourceSnapshot,
) (*RenderedResourceSnapshot, error) {
	if c == nil {
		return nil, errors.New("k8sResources: collector is nil")
	}
	if len(previous) > 1 {
		return nil, errors.New("k8sResources: more than one previous snapshot")
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.snapshot != nil {
		if err := c.snapshot.validate(); err != nil {
			return nil, err
		}
		return c.snapshot, nil
	}
	var prior *RenderedResourceSnapshot
	if len(previous) == 1 && previous[0] != nil {
		if err := previous[0].validate(); err != nil {
			return nil, fmt.Errorf("k8sResources: previous snapshot: %w", err)
		}
		prior = previous[0]
	}
	entries, err := c.snapshotEntriesLocked(prior)
	if err != nil {
		return nil, err
	}
	if sameRenderedResourceSnapshotEntries(entries, prior) {
		c.frozen = true
		c.snapshot = prior
		return prior, nil
	}
	storage := &renderedResourceSnapshotStorage{entries: entries}
	snapshot := &RenderedResourceSnapshot{storage: storage}
	snapshot.auth = renderedResourceSnapshotAuthentication{owner: snapshot, storage: storage, count: len(entries)}
	snapshot.seal = snapshot
	c.frozen = true
	c.snapshot = snapshot
	return snapshot, nil
}

func (c *RenderedResourceCollector) snapshotEntriesLocked(
	prior *RenderedResourceSnapshot,
) ([]*renderedResourceSnapshotEntry, error) {
	keys := make([]string, 0, len(c.resources))
	for key := range c.resources {
		keys = append(keys, key)
	}
	slices.Sort(keys)
	entries := make([]*renderedResourceSnapshotEntry, len(keys))
	for index, key := range keys {
		resource := c.resources[key]
		projection, exists := c.projections[key]
		if resource == nil || !exists {
			return nil, fmt.Errorf("k8sResources %s: object has invalid provenance", key)
		}
		entry := &renderedResourceSnapshotEntry{
			namespace: strings.Clone(resource.Namespace), apiVersion: strings.Clone(resource.APIVersion),
			kind: strings.Clone(resource.Kind), name: strings.Clone(resource.Name), object: projection,
			createOnlyFields: slices.Clone(resource.CreateOnlyFields),
		}
		if prior != nil && index < len(prior.storage.entries) &&
			exactRenderedResourceEntry(prior.storage.entries[index], entry) {
			entry = prior.storage.entries[index]
		}
		entries[index] = entry
	}
	return entries, nil
}

func sameRenderedResourceSnapshotEntries(
	entries []*renderedResourceSnapshotEntry,
	prior *RenderedResourceSnapshot,
) bool {
	if prior == nil || len(entries) != len(prior.storage.entries) {
		return false
	}
	for index := range entries {
		if entries[index] != prior.storage.entries[index] {
			return false
		}
	}
	return true
}

// ValidateAuthentication verifies the exact private ownership chain.
func (s *RenderedResourceSnapshot) ValidateAuthentication() error {
	return s.validate()
}

func (s *RenderedResourceSnapshot) validate() error {
	if s == nil || s.seal != s || s.storage == nil || s.auth.owner != s ||
		s.auth.storage != s.storage || s.auth.count != len(s.storage.entries) {
		return errors.New("k8sResources snapshot has invalid provenance")
	}
	return nil
}

// Len returns the number of desired resources.
func (s *RenderedResourceSnapshot) Len() (int, error) {
	if err := s.validate(); err != nil {
		return 0, err
	}
	return len(s.storage.entries), nil
}

// SameRoot reports authenticated storage identity.
func (s *RenderedResourceSnapshot) SameRoot(other *RenderedResourceSnapshot) (bool, error) {
	if err := s.validate(); err != nil {
		return false, err
	}
	if err := other.validate(); err != nil {
		return false, err
	}
	return s.storage == other.storage, nil
}

// ExactEqual compares complete resource identity without trusting a digest.
func (s *RenderedResourceSnapshot) ExactEqual(other *RenderedResourceSnapshot) (bool, error) {
	if err := s.validate(); err != nil {
		return false, err
	}
	if err := other.validate(); err != nil {
		return false, err
	}
	if s.storage == other.storage {
		return true, nil
	}
	if len(s.storage.entries) != len(other.storage.entries) {
		return false, nil
	}
	for index := range s.storage.entries {
		if !exactRenderedResourceEntry(s.storage.entries[index], other.storage.entries[index]) {
			return false, nil
		}
	}
	return true, nil
}

// Resources returns a fully detached compatibility view.
func (s *RenderedResourceSnapshot) Resources() ([]RenderedResource, error) {
	if err := s.validate(); err != nil {
		return nil, err
	}
	resources := make([]RenderedResource, len(s.storage.entries))
	for index, entry := range s.storage.entries {
		object, err := entry.object.materializeObject()
		if err != nil {
			return nil, fmt.Errorf("k8sResources snapshot entry %d: %w", index, err)
		}
		resources[index] = RenderedResource{
			Namespace: entry.namespace, Name: entry.name, APIVersion: entry.apiVersion,
			Kind: entry.kind, Object: object,
			CreateOnlyFields: slices.Clone(entry.createOnlyFields),
		}
	}
	return resources, nil
}

func exactRenderedResourceEntry(left, right *renderedResourceSnapshotEntry) bool {
	return left != nil && right != nil && left.namespace == right.namespace && left.name == right.name &&
		left.apiVersion == right.apiVersion && left.kind == right.kind &&
		slices.Equal(left.createOnlyFields, right.createOnlyFields) &&
		reflect.DeepEqual(left.object, right.object)
}
