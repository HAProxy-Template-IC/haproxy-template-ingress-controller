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
	"context"
	"errors"
	"fmt"
	"slices"
	"sync/atomic"

	iradix "github.com/hashicorp/go-immutable-radix/v2"

	"gitlab.com/haproxy-haptic/haptic/pkg/k8s/indexer"
	"gitlab.com/haproxy-haptic/haptic/pkg/stores"
)

// SnapshotBranch is an immutable selection of resource revisions from watched snapshots.
type SnapshotBranch struct {
	authority  *branchAuthority
	sequence   uint64
	numKeys    int
	entries    *iradix.Tree[*branchEntry]
	lookup     *iradix.Tree[[]*branchEntry]
	keys       *iradix.Tree[uint64]
	identities *iradix.Tree[uint64]
	history    []branchHistory
}

type branchAuthority struct {
	source   uint64
	sequence atomic.Uint64
}

type branchEntry struct {
	identity resourceIdentity
	keys     []string
	origin   stores.Revision
	value    any
	encoded  []byte
	lazy     *cachedReadSnapshot
	ref      resourceRef
}

type branchHistory struct {
	before  uint64
	changes []stores.RevisionChange
}

// BranchChange identifies an exact replacement or deletion captured from a snapshot.
type BranchChange struct {
	identity resourceIdentity
	entry    *branchEntry
}

// Namespace returns the resource namespace.
func (c BranchChange) Namespace() string { return c.identity.namespace }

// Name returns the resource name.
func (c BranchChange) Name() string { return c.identity.name }

// Revision identifies the captured observed revision; deletion has no value revision.
func (c BranchChange) Revision() stores.Revision {
	if c.entry == nil {
		return ""
	}
	return c.entry.origin
}

// Deleted reports whether the observed resource no longer exists.
func (c BranchChange) Deleted() bool { return c.entry == nil }

// NewSnapshotBranch creates an empty branch with an independent revision journal.
func NewSnapshotBranch(numKeys int) *SnapshotBranch {
	return &SnapshotBranch{
		authority:  &branchAuthority{source: allocateRevisionSource(&nextRevisionSource)},
		numKeys:    max(1, numKeys),
		entries:    iradix.New[*branchEntry](),
		lookup:     iradix.New[[]*branchEntry](),
		keys:       iradix.New[uint64](),
		identities: iradix.New[uint64](),
	}
}

// Changes captures replacements without fetching on-demand resource bodies.
func (b *SnapshotBranch) Changes(ctx context.Context, snapshot stores.ReadSnapshot, indexBy []string) ([]BranchChange, error) {
	observed, err := branchEntries(ctx, snapshot, indexBy)
	if err != nil {
		return nil, err
	}
	var changes []BranchChange
	for key, entry := range observed {
		if len(entry.keys) != b.numKeys {
			return nil, fmt.Errorf("resource %s/%s has %d index keys; expected %d", entry.identity.namespace, entry.identity.name, len(entry.keys), b.numKeys)
		}
		previous, found := b.entries.Get([]byte(key))
		if !found || previous.origin != entry.origin {
			changes = append(changes, BranchChange{identity: entry.identity, entry: entry})
		}
	}
	iterator := b.entries.Root().Iterator()
	for {
		key, entry, found := iterator.Next()
		if !found {
			break
		}
		if _, exists := observed[string(key)]; !exists {
			changes = append(changes, BranchChange{identity: entry.identity})
		}
	}
	slices.SortFunc(changes, func(a, z BranchChange) int { return compareBranchIdentity(a.identity, z.identity) })
	return changes, ctx.Err()
}

// Apply creates a branch containing exactly the supplied changes.
func (b *SnapshotBranch) Apply(changes []BranchChange) *SnapshotBranch {
	changes = slices.DeleteFunc(slices.Clone(changes), func(change BranchChange) bool {
		previous, found := b.entries.Get([]byte(resourceCacheKey(change.identity.namespace, change.identity.name)))
		if change.entry == nil {
			return !found
		}
		return found && previous.origin == change.entry.origin
	})
	if len(changes) == 0 {
		return b
	}
	sequence := allocateRevisionSource(&b.authority.sequence)
	entries, keys, identities := b.entries.Txn(), b.keys.Txn(), b.identities.Txn()
	lookup := b.lookup.Txn()
	revisions := make([]stores.RevisionChange, 0, len(changes))
	for _, change := range changes {
		key := []byte(resourceCacheKey(change.identity.namespace, change.identity.name))
		revision := stores.RevisionChange{
			Sequence: sequence, Namespace: change.identity.namespace, Name: change.identity.name,
			Deleted: change.entry == nil,
		}
		if previous, found := entries.Get(key); found {
			revision.OldKeys = previous.keys
			removeBranchLookup(lookup, previous)
		}
		if change.entry == nil {
			entries.Delete(key)
		} else {
			entries.Insert(key, change.entry)
			lookupKey := []byte(indexer.EncodeKey(change.entry.keys))
			bucket, _ := lookup.Get(lookupKey)
			lookup.Insert(lookupKey, append(slices.Clone(bucket), change.entry))
			revision.NewKeys = change.entry.keys
		}
		updateBranchKeyRevisions(keys, lookup, sequence, revision.OldKeys, revision.NewKeys)
		if change.entry == nil {
			identities.Delete(key)
		} else {
			identities.Insert(key, sequence)
		}
		revisions = append(revisions, revision)
	}
	history := append(slices.Clone(b.history), branchHistory{before: b.sequence, changes: revisions})
	retained := 0
	for index := len(history) - 1; index >= 0; index-- {
		retained += len(history[index].changes)
		if retained > defaultRevisionJournalCapacity {
			history = slices.Clone(history[index+1:])
			break
		}
	}
	return &SnapshotBranch{
		authority: b.authority, sequence: sequence, numKeys: b.numKeys,
		entries: entries.Commit(), lookup: lookup.Commit(), keys: keys.Commit(), identities: identities.Commit(), history: history,
	}
}

func updateBranchKeyRevisions(keys *iradix.Txn[uint64], lookup *iradix.Txn[[]*branchEntry], sequence uint64, keySets ...[]string) {
	for _, keySet := range keySets {
		for count := 1; count <= len(keySet); count++ {
			prefix := []byte(indexer.EncodeKey(keySet[:count]))
			iterator := lookup.Root().Iterator()
			iterator.SeekPrefix(prefix)
			if _, _, found := iterator.Next(); found {
				keys.Insert(prefix, sequence)
			} else {
				keys.Delete(prefix)
			}
		}
	}
}

func removeBranchLookup(lookup *iradix.Txn[[]*branchEntry], entry *branchEntry) {
	key := []byte(indexer.EncodeKey(entry.keys))
	bucket, _ := lookup.Get(key)
	retained := slices.DeleteFunc(slices.Clone(bucket), func(other *branchEntry) bool { return other.identity == entry.identity })
	if len(retained) == 0 {
		lookup.Delete(key)
	} else {
		lookup.Insert(key, retained)
	}
}

// Pin returns this immutable root.
func (b *SnapshotBranch) Pin() (stores.ReadSnapshot, error) { return b, nil }

// RevisionSource identifies this branch family.
func (b *SnapshotBranch) RevisionSource() stores.RevisionSource {
	return stores.RevisionSource(b.authority.source)
}

// IdentityOrderSource certifies namespace/name ordering.
func (b *SnapshotBranch) IdentityOrderSource() stores.RevisionSource { return b.RevisionSource() }

// ExactRevisionJournalSource certifies the journal's source.
func (b *SnapshotBranch) ExactRevisionJournalSource() stores.RevisionSource {
	return b.RevisionSource()
}

// Sequence returns this root's immutable watermark.
func (b *SnapshotBranch) Sequence() uint64 { return b.sequence }

// ListRevision identifies this root's membership and values.
func (b *SnapshotBranch) ListRevision() stores.Revision {
	return revisionToken(b.authority.source, "list", "", b.sequence)
}

// GetRevision identifies a configured index lookup.
func (b *SnapshotBranch) GetRevision(keys ...string) stores.Revision {
	if len(keys) == 0 || len(keys) > b.numKeys {
		return ""
	}
	key := indexer.EncodeKey(keys)
	version, _ := b.keys.Get([]byte(key))
	return revisionToken(b.authority.source, "get", key, version)
}

// IdentityRevision identifies a namespace/name lookup, including absence.
func (b *SnapshotBranch) IdentityRevision(namespace, name string) stores.Revision {
	key := resourceCacheKey(namespace, name)
	version, _ := b.identities.Get([]byte(key))
	return revisionToken(b.authority.source, "identity", key, version)
}

// ChangesSince returns exact changes only when sequence is an ancestor of this root.
func (b *SnapshotBranch) ChangesSince(sequence uint64) (uint64, []stores.RevisionChange, bool) {
	changes, complete := b.ChangesBetween(sequence, b.sequence)
	return b.sequence, changes, complete
}

// ChangesBetween proves ancestry without treating sibling revisions as mutations.
func (b *SnapshotBranch) ChangesBetween(from, through uint64) ([]stores.RevisionChange, bool) {
	if through < from || through > b.sequence {
		return nil, false
	}
	if from == b.sequence {
		return nil, true
	}
	for index, history := range b.history {
		if history.before != from {
			continue
		}
		if from == through {
			return nil, true
		}
		var changes []stores.RevisionChange
		for _, batch := range b.history[index:] {
			sequence := batch.changes[len(batch.changes)-1].Sequence
			if sequence > through {
				return nil, false
			}
			for _, change := range batch.changes {
				change.OldKeys = slices.Clone(change.OldKeys)
				change.NewKeys = slices.Clone(change.NewKeys)
				changes = append(changes, change)
			}
			if sequence == through {
				return changes, true
			}
		}
		return nil, false
	}
	return nil, false
}

// AcquireSnapshotCommitFence needs no lock because a branch never changes.
func (b *SnapshotBranch) AcquireSnapshotCommitFence(ctx context.Context) (func(), error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return func() {}, nil
}

var errImmutableBranch = errors.New("snapshot branch is immutable; apply captured changes to create a new branch")

// Add refuses in-place mutations.
func (b *SnapshotBranch) Add(any, []string) error { return errImmutableBranch }

// Update refuses in-place mutations.
func (b *SnapshotBranch) Update(any, []string) error { return errImmutableBranch }

// Delete refuses in-place mutations.
func (b *SnapshotBranch) Delete(string, string, []string) error { return errImmutableBranch }

// Clear refuses in-place mutations.
func (b *SnapshotBranch) Clear() error { return errImmutableBranch }

func (e *branchEntry) read(ctx context.Context) (any, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if e.lazy == nil {
		return e.value, nil
	}
	value, err := e.lazy.readRefImmutable(ctx, &e.ref)
	if err != nil {
		// A retained branch outlives one render; transient read errors must not stick.
		e.lazy.mu.Lock()
		for key, load := range e.lazy.loads {
			if load.err != nil {
				delete(e.lazy.loads, key)
			}
		}
		e.lazy.mu.Unlock()
	}
	return value, err
}

func branchEntries(ctx context.Context, snapshot stores.ReadSnapshot, indexBy []string) (map[string]*branchEntry, error) {
	switch value := snapshot.(type) {
	case *memoryReadSnapshot:
		return memoryBranchEntries(ctx, value, indexBy)
	case *cachedReadSnapshot:
		return cachedBranchEntries(ctx, value)
	case *SnapshotBranch:
		entries := make(map[string]*branchEntry, value.entries.Len())
		iterator := value.entries.Root().Iterator()
		for {
			if err := ctx.Err(); err != nil {
				return nil, err
			}
			key, entry, found := iterator.Next()
			if !found {
				break
			}
			entries[string(key)] = entry
		}
		return entries, nil
	default:
		return nil, fmt.Errorf("snapshot type %T cannot retain exact resource revisions: %w", snapshot, stores.ErrSnapshotUnsupported)
	}
}
