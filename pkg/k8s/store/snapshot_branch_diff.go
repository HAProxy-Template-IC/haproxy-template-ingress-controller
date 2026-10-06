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

	"gitlab.com/haproxy-haptic/haptic/pkg/stores"
)

// ChangesFrom compares selected roots without reading lazy resource bodies.
func (b *SnapshotBranch) ChangesFrom(ctx context.Context, previous stores.ReadSnapshot) ([]stores.RevisionChange, bool, error) {
	before, supported := previous.(*SnapshotBranch)
	if !supported || before == nil || before.authority != b.authority {
		return nil, false, nil
	}
	var changes []stores.RevisionChange
	iterator := b.entries.Root().Iterator()
	for {
		if err := ctx.Err(); err != nil {
			return nil, false, err
		}
		key, entry, found := iterator.Next()
		if !found {
			break
		}
		old, existed := before.entries.Get(key)
		if existed && old.origin == entry.origin {
			continue
		}
		change := stores.RevisionChange{Sequence: b.sequence, Namespace: entry.identity.namespace, Name: entry.identity.name, NewKeys: slices.Clone(entry.keys)}
		if existed {
			change.OldKeys = slices.Clone(old.keys)
		}
		changes = append(changes, change)
	}
	iterator = before.entries.Root().Iterator()
	for {
		if err := ctx.Err(); err != nil {
			return nil, false, err
		}
		key, entry, found := iterator.Next()
		if !found {
			break
		}
		if _, remains := b.entries.Get(key); remains {
			continue
		}
		changes = append(changes, stores.RevisionChange{Sequence: b.sequence, Namespace: entry.identity.namespace, Name: entry.identity.name, Deleted: true, OldKeys: slices.Clone(entry.keys)})
	}
	slices.SortFunc(changes, func(a, z stores.RevisionChange) int {
		return compareBranchIdentity(resourceIdentity{namespace: a.Namespace, name: a.Name}, resourceIdentity{namespace: z.Namespace, name: z.Name})
	})
	return changes, true, nil
}
