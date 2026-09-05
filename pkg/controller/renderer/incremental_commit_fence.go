package renderer

import (
	"context"
	"fmt"
	"slices"
	"strings"

	"gitlab.com/haproxy-haptic/haptic/pkg/stores"
)

type incrementalCommitFence struct {
	source stores.RevisionSource
	alias  string
	fencer stores.SnapshotCommitFencer
}

func (r *incrementalRenderSession) acquireStoreCommitFences(ctx context.Context) (func(), error) {
	fences := make([]incrementalCommitFence, 0, len(r.baseSnapshots))
	for alias, snapshot := range r.baseSnapshots {
		if snapshot == nil || snapshot.RevisionSource() == 0 {
			return nil, fmt.Errorf("watched resource %q has no exact snapshot source", alias)
		}
		fencer, ok := r.baseStores[alias].(stores.SnapshotCommitFencer)
		if !ok {
			return nil, fmt.Errorf("watched resource %q: %w", alias, stores.ErrSnapshotCommitFenceUnsupported)
		}
		fences = append(fences, incrementalCommitFence{
			source: snapshot.RevisionSource(),
			alias:  alias,
			fencer: fencer,
		})
	}
	slices.SortFunc(fences, func(left, right incrementalCommitFence) int {
		if left.source < right.source {
			return -1
		}
		if left.source > right.source {
			return 1
		}
		return strings.Compare(left.alias, right.alias)
	})

	releases := make([]func(), 0, len(fences))
	releaseAll := func() {
		for index := len(releases) - 1; index >= 0; index-- {
			releases[index]()
		}
	}
	var acquiredSource stores.RevisionSource
	for _, fence := range fences {
		if fence.source == acquiredSource {
			continue
		}
		release, err := fence.fencer.AcquireSnapshotCommitFence(ctx)
		if err != nil {
			releaseAll()
			return nil, fmt.Errorf("fencing watched resource %q: %w", fence.alias, err)
		}
		if release == nil {
			releaseAll()
			return nil, fmt.Errorf("fencing watched resource %q returned no release", fence.alias)
		}
		releases = append(releases, release)
		acquiredSource = fence.source
	}
	return releaseAll, nil
}
