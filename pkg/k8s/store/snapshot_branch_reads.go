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

package store

import (
	"cmp"
	"context"
	"fmt"
	"slices"

	iradix "github.com/hashicorp/go-immutable-radix/v2"

	"gitlab.com/haproxy-haptic/haptic/pkg/k8s/indexer"
	"gitlab.com/haproxy-haptic/haptic/pkg/stores"
)

func memoryBranchEntries(ctx context.Context, snapshot *memoryReadSnapshot, indexBy []string) (map[string]*branchEntry, error) {
	idx, err := indexer.New(indexer.Config{IndexBy: indexBy})
	if err != nil {
		return nil, err
	}
	result := make(map[string]*branchEntry, snapshot.root.locations.Len())
	iterator := snapshot.root.data.Root().Iterator()
	for {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		_, bucket, found := iterator.Next()
		if !found {
			break
		}
		for index, value := range bucket.items {
			identity, identified := identifyResource(value)
			if !identified {
				return nil, stores.ErrSnapshotUnsupported
			}
			keys, err := idx.ExtractKeys(value)
			if err != nil {
				return nil, err
			}
			entry := &branchEntry{
				identity: identity, keys: keys, value: value,
				origin: snapshot.IdentityRevision(identity.namespace, identity.name),
			}
			if bucket.encoded {
				entry.encoded = bucket.encodedItems[index]
			}
			result[resourceCacheKey(identity.namespace, identity.name)] = entry
		}
	}
	return result, nil
}

func cachedBranchEntries(ctx context.Context, snapshot *cachedReadSnapshot) (map[string]*branchEntry, error) {
	result := make(map[string]*branchEntry, snapshot.root.locations.Len())
	iterator := snapshot.root.locations.Root().Iterator()
	for {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		key, ref, found := iterator.Next()
		if !found {
			break
		}
		warm := iradix.New[cachedSnapshotValue]()
		if value, found := snapshot.root.warm.Get(key); found {
			warm, _, _ = warm.Insert(key, value)
		}
		// Retain only this reference's warm value, not an entire older store root.
		lazy := &cachedReadSnapshot{
			root: &cachedReadRoot{warm: warm}, store: snapshot.store,
			pinnedAt: snapshot.pinnedAt, loads: map[string]*cachedSnapshotLoad{},
		}
		result[string(key)] = &branchEntry{
			identity: resourceIdentity{namespace: ref.namespace, name: ref.name},
			keys:     slices.Clone(ref.indexKeys), origin: snapshot.IdentityRevision(ref.namespace, ref.name),
			lazy: lazy, ref: cloneResourceRef(&ref),
		}
	}
	return result, nil
}

func compareBranchIdentity(a, b resourceIdentity) int {
	if order := cmp.Compare(a.namespace, b.namespace); order != 0 {
		return order
	}
	return cmp.Compare(a.name, b.name)
}

func (b *SnapshotBranch) readProjection(ctx context.Context, keys []string) (values []any, encodings [][]byte, complete bool, readErr error) {
	if len(keys) > b.numKeys {
		return nil, nil, false, fmt.Errorf("too many index keys: got %d, expected %d", len(keys), b.numKeys)
	}
	var selected []*branchEntry
	iterator := b.lookup.Root().Iterator()
	if len(keys) > 0 {
		iterator.SeekPrefix([]byte(indexer.EncodeKey(keys)))
	}
	for {
		if err := ctx.Err(); err != nil {
			return nil, nil, false, err
		}
		_, bucket, found := iterator.Next()
		if !found {
			break
		}
		selected = append(selected, bucket...)
	}
	slices.SortFunc(selected, func(a, z *branchEntry) int { return compareBranchIdentity(a.identity, z.identity) })
	items, encoded := make([]any, len(selected)), make([][]byte, len(selected))
	allEncoded := true
	for index, entry := range selected {
		value, err := entry.read(ctx)
		if err != nil {
			return nil, nil, false, err
		}
		items[index], encoded[index] = value, entry.encoded
		allEncoded = allEncoded && entry.encoded != nil
	}
	return items, encoded, allEncoded, nil
}

// Get returns detached resources matching the configured index keys.
func (b *SnapshotBranch) Get(keys ...string) ([]any, error) {
	return b.GetContext(context.Background(), keys...)
}

// GetContext performs a cancellation-aware index lookup.
func (b *SnapshotBranch) GetContext(ctx context.Context, keys ...string) ([]any, error) {
	if len(keys) == 0 {
		return nil, fmt.Errorf("at least one index key is required")
	}
	items, _, _, err := b.readProjection(ctx, keys)
	if err != nil {
		return nil, err
	}
	return cloneMemorySnapshotItems(items)
}

// List returns detached resources in namespace/name order.
func (b *SnapshotBranch) List() ([]any, error) { return b.ListContext(context.Background()) }

// ListContext performs a cancellation-aware collection read.
func (b *SnapshotBranch) ListContext(ctx context.Context) ([]any, error) {
	items, _, _, err := b.readProjection(ctx, nil)
	if err != nil {
		return nil, err
	}
	return cloneMemorySnapshotItems(items)
}

// GetIdentity returns a detached resource by namespace/name.
func (b *SnapshotBranch) GetIdentity(namespace, name string) (item any, found bool, err error) {
	return b.GetIdentityContext(context.Background(), namespace, name)
}

// GetIdentityContext performs a cancellation-aware identity lookup.
func (b *SnapshotBranch) GetIdentityContext(ctx context.Context, namespace, name string) (item any, found bool, err error) {
	entry, found := b.entries.Get([]byte(resourceCacheKey(namespace, name)))
	if !found {
		return nil, false, ctx.Err()
	}
	value, err := entry.read(ctx)
	if err != nil {
		return nil, false, err
	}
	value, err = cloneMemorySnapshotValue(value)
	return value, err == nil, err
}

// ListSnapshot returns a detached collection and its immutable watermark.
func (b *SnapshotBranch) ListSnapshot() (items []any, sequence uint64, err error) {
	items, err = b.List()
	return items, b.sequence, err
}

// GetSnapshot returns a detached lookup and its exact revision.
func (b *SnapshotBranch) GetSnapshot(keys ...string) (items []any, revision stores.Revision, sequence uint64, err error) {
	items, err = b.Get(keys...)
	return items, b.GetRevision(keys...), b.sequence, err
}

// IdentitySnapshot returns a detached identity lookup and its exact revision.
func (b *SnapshotBranch) IdentitySnapshot(namespace, name string) (item any, found bool, revision stores.Revision, sequence uint64, err error) {
	item, found, err = b.GetIdentity(namespace, name)
	return item, found, b.IdentityRevision(namespace, name), b.sequence, err
}
