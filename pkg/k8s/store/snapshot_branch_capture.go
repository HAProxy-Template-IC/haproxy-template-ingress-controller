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
	"slices"

	iradix "github.com/hashicorp/go-immutable-radix/v2"

	"gitlab.com/haproxy-haptic/haptic/pkg/k8s/indexer"
	"gitlab.com/haproxy-haptic/haptic/pkg/stores"
)

// branchCapture proves a branch equals one observed root outside suspects, so a
// later capture inspects only suspects plus identities journaled since (#297).
type branchCapture struct {
	source   stores.RevisionSource
	sequence uint64
	indexBy  []string
	suspects *iradix.Tree[struct{}]
}

func (b *SnapshotBranch) recordCapture(snapshot stores.ReadSnapshot, indexBy []string, changes []BranchChange) {
	b.capture.Store(&branchCapture{
		source: snapshot.RevisionSource(), sequence: snapshot.Sequence(),
		indexBy: slices.Clone(indexBy), suspects: withSuspects(iradix.New[struct{}](), changes),
	})
}

// derivedCapture keeps the parent's proof valid for a branch that differs from it only by applied.
func (b *SnapshotBranch) derivedCapture(applied []BranchChange) *branchCapture {
	parent := b.capture.Load()
	if parent == nil {
		return nil
	}
	derived := *parent
	derived.suspects = withSuspects(parent.suspects, applied)
	return &derived
}

func withSuspects(suspects *iradix.Tree[struct{}], changes []BranchChange) *iradix.Tree[struct{}] {
	txn := suspects.Txn()
	for _, change := range changes {
		txn.Insert([]byte(resourceCacheKey(change.identity.namespace, change.identity.name)), struct{}{})
	}
	return txn.Commit()
}

// captureCandidates returns every identity that may differ from snapshot, or false when only a full scan can tell.
func (b *SnapshotBranch) captureCandidates(snapshot stores.ReadSnapshot, indexBy []string) (map[string]struct{}, bool) {
	capture := b.capture.Load()
	if capture == nil || capture.source != snapshot.RevisionSource() || capture.sequence > snapshot.Sequence() ||
		!slices.Equal(capture.indexBy, indexBy) {
		return nil, false
	}
	touched, complete := snapshotChangesSince(snapshot, capture.sequence)
	if !complete {
		return nil, false
	}
	candidates := make(map[string]struct{}, capture.suspects.Len()+len(touched))
	iterator := capture.suspects.Root().Iterator()
	for key, _, found := iterator.Next(); found; key, _, found = iterator.Next() {
		candidates[string(key)] = struct{}{}
	}
	for _, change := range touched {
		if change.Name == "" {
			return nil, false
		}
		candidates[resourceCacheKey(change.Namespace, change.Name)] = struct{}{}
	}
	return candidates, true
}

// snapshotChangesSince may report changes after the snapshot; callers only use them as candidates.
func snapshotChangesSince(snapshot stores.ReadSnapshot, sequence uint64) ([]stores.RevisionChange, bool) {
	switch value := snapshot.(type) {
	case *memoryReadSnapshot:
		if value.journal == nil {
			return nil, false
		}
		_, changes, complete := value.journal.ChangesSince(sequence)
		return changes, complete
	case *cachedReadSnapshot:
		_, changes, complete := value.store.ChangesSince(sequence)
		return changes, complete
	case *SnapshotBranch:
		return value.ChangesBetween(sequence, value.sequence)
	default:
		return nil, false
	}
}

func candidateEntries(ctx context.Context, snapshot stores.ReadSnapshot, indexBy []string, previous *SnapshotBranch, candidates map[string]struct{}) (observed map[string]*branchEntry, deleted [][]byte, err error) {
	idx, err := indexer.New(indexer.Config{IndexBy: indexBy})
	if err != nil {
		return nil, nil, err
	}
	indexBy = slices.Clone(indexBy)
	observed = make(map[string]*branchEntry)
	for candidate := range candidates {
		if err := ctx.Err(); err != nil {
			return nil, nil, err
		}
		key := []byte(candidate)
		if !snapshotContainsIdentity(snapshot, key) {
			if _, retained := previous.entries.Get(key); retained {
				deleted = append(deleted, key)
			}
			continue
		}
		entry, err := changedSnapshotEntry(snapshot, key, idx, indexBy, previous)
		if err != nil {
			return nil, nil, err
		}
		if entry != nil {
			observed[candidate] = entry
		}
	}
	return observed, deleted, nil
}

// changedSnapshotEntry returns nil when the present identity is unchanged from previous.
func changedSnapshotEntry(snapshot stores.ReadSnapshot, key []byte, idx *indexer.Indexer, indexBy []string, previous *SnapshotBranch) (*branchEntry, error) {
	switch value := snapshot.(type) {
	case *memoryReadSnapshot:
		item, _ := value.root.locations.Get(key)
		_, entry, err := value.changedBranchEntry(item, idx, indexBy, previous)
		if entry != nil {
			entry.encoded = value.encodedItem(entry)
		}
		return entry, err
	case *cachedReadSnapshot:
		ref, _ := value.root.locations.Get(key)
		return value.changedBranchEntry(key, &ref, previous), nil
	case *SnapshotBranch:
		return value.changedBranchEntry(key, previous), nil
	default:
		return nil, stores.ErrSnapshotUnsupported
	}
}

func (snapshot *memoryReadSnapshot) encodedItem(entry *branchEntry) []byte {
	bucket, found := snapshot.root.data.Get([]byte(indexer.EncodeKey(entry.keys)))
	if !found || !bucket.encoded {
		return nil
	}
	for index, item := range bucket.items {
		if identity, identified := identifyResource(item); identified && identity == entry.identity {
			return bucket.encodedItems[index]
		}
	}
	return nil
}
