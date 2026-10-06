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

package inputisolation

import (
	"context"
	"fmt"
	"maps"
	"slices"

	"gitlab.com/haproxy-haptic/haptic/pkg/stores"
)

type storeFence struct {
	alias    string
	provider stores.SnapshotProvider
	fencer   stores.SnapshotCommitFencer
	source   stores.RevisionSource
}

func (s *Service) pinObservedStores(ctx context.Context, observed stores.StoreProvider) (map[string]stores.ReadSnapshot, error) {
	fences := make([]storeFence, 0, len(s.watches))
	for _, alias := range slices.Sorted(maps.Keys(s.watches)) {
		value := observed.GetStore(alias)
		provider, pins := value.(stores.SnapshotProvider)
		fencer, fencesWrites := value.(stores.SnapshotCommitFencer)
		source, revisions := value.(interface{ RevisionSource() stores.RevisionSource })
		if !pins || !fencesWrites || !revisions || source.RevisionSource() == 0 {
			return nil, fmt.Errorf("watched resource %q cannot pin a fenced input snapshot", alias)
		}
		fences = append(fences, storeFence{alias: alias, provider: provider, fencer: fencer, source: source.RevisionSource()})
	}
	slices.SortStableFunc(fences, func(a, b storeFence) int {
		if a.source < b.source {
			return -1
		}
		if a.source > b.source {
			return 1
		}
		return 0
	})
	var release []func()
	defer func() {
		for index := len(release) - 1; index >= 0; index-- {
			release[index]()
		}
	}()
	var last stores.RevisionSource
	for _, fence := range fences {
		if fence.source == last {
			continue
		}
		unlock, err := fence.fencer.AcquireSnapshotCommitFence(ctx)
		if err != nil {
			return nil, err
		}
		release = append(release, unlock)
		last = fence.source
	}
	result := make(map[string]stores.ReadSnapshot, len(fences))
	for _, fence := range fences {
		snapshot, err := fence.provider.Pin()
		if err != nil {
			return nil, fmt.Errorf("pinning watched resource %q: %w", fence.alias, err)
		}
		result[fence.alias] = snapshot
	}
	return result, ctx.Err()
}
