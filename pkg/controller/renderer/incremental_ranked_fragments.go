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

package renderer

import (
	"context"
	"errors"

	"gitlab.com/haproxy-haptic/haptic/pkg/templating"
)

func (r *incrementalRenderSession) IncrementalRankedFragments(
	ctx context.Context,
	group, cell string,
) (string, error) {
	fragment, err := r.IncrementalRankedTextFragment(ctx, group, cell)
	if err != nil {
		return "", err
	}
	return materializeIncrementalTextFragment(fragment)
}

func (r *incrementalRenderSession) IncrementalRankedTextFragment(
	ctx context.Context,
	group, cell string,
) (templating.TextFragment, error) {
	r.renderMu.Lock()
	defer r.renderMu.Unlock()
	return r.incrementalRankedTextFragment(ctx, group, cell, "")
}

func (r *incrementalRenderSession) incrementalRankedTextFragment(
	ctx context.Context,
	group, cell, delimiter string,
) (templating.TextFragment, error) {
	if err := validateIncrementalValueRequest(r.state, group, cell); err != nil {
		return nil, err
	}
	scope, _ := templating.IncrementalScope(ctx)
	if err := r.requireProducerGroupCall(group, scope); err != nil {
		return nil, err
	}
	r.valueAccesses[group]++
	if r.groupIndexes[group] == nil {
		return nil, errors.New("incremental publication index is unavailable")
	}
	fragment, err := r.groupIndexes[group].rankedTextFragment(cell, delimiter)
	if err != nil {
		return nil, err
	}
	if err := r.recordExactCycleIncrementalObservation(
		ctx, exactCycleIncrementalRanked, group, "", cell, delimiter, fragment,
	); err != nil {
		return nil, err
	}
	return fragment, nil
}

func (r *incrementalRenderSession) IncrementalRankedFragmentsJoin(
	ctx context.Context,
	group, cell, delimiter string,
) (string, error) {
	fragment, err := r.IncrementalRankedTextFragmentJoin(ctx, group, cell, delimiter)
	if err != nil {
		return "", err
	}
	return materializeIncrementalTextFragment(fragment)
}

func (r *incrementalRenderSession) IncrementalRankedTextFragmentJoin(
	ctx context.Context,
	group, cell, delimiter string,
) (templating.TextFragment, error) {
	r.renderMu.Lock()
	defer r.renderMu.Unlock()
	return r.incrementalRankedTextFragment(ctx, group, cell, delimiter)
}

func (r *coldIncrementalRenderer) IncrementalRankedFragments(
	ctx context.Context,
	group, cell string,
) (string, error) {
	fragment, err := r.IncrementalRankedTextFragment(ctx, group, cell)
	if err != nil {
		return "", err
	}
	return materializeIncrementalTextFragment(fragment)
}

func (r *coldIncrementalRenderer) IncrementalRankedTextFragment(
	ctx context.Context,
	group, cell string,
) (templating.TextFragment, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.incrementalRankedTextFragment(ctx, group, cell, "")
}

func (r *coldIncrementalRenderer) incrementalRankedTextFragment(
	ctx context.Context,
	group, cell, delimiter string,
) (templating.TextFragment, error) {
	if err := validateIncrementalValueRequest(r.state, group, cell); err != nil {
		return nil, err
	}
	scope, _ := templating.IncrementalScope(ctx)
	if err := r.requireProducerGroupCall(group, scope); err != nil {
		return nil, err
	}
	r.valueAccesses[group]++
	if r.groupIndexes[group] == nil {
		return nil, errors.New("incremental publication index is unavailable")
	}
	return r.groupIndexes[group].rankedTextFragment(cell, delimiter)
}

func (r *coldIncrementalRenderer) IncrementalRankedFragmentsJoin(
	ctx context.Context,
	group, cell, delimiter string,
) (string, error) {
	fragment, err := r.IncrementalRankedTextFragmentJoin(ctx, group, cell, delimiter)
	if err != nil {
		return "", err
	}
	return materializeIncrementalTextFragment(fragment)
}

func (r *coldIncrementalRenderer) IncrementalRankedTextFragmentJoin(
	ctx context.Context,
	group, cell, delimiter string,
) (templating.TextFragment, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.incrementalRankedTextFragment(ctx, group, cell, delimiter)
}

func decodeIncrementalRankedFragments(index *incrementalGroupIndex, cell string) (string, error) {
	return decodeIncrementalRankedFragmentsJoin(index, cell, "")
}

func decodeIncrementalRankedFragmentsJoin(index *incrementalGroupIndex, cell, delimiter string) (string, error) {
	if index == nil {
		return "", errors.New("incremental publication index is unavailable")
	}
	return index.rankedFragments(cell, delimiter)
}

// IncrementalRankedFragmentBytes reports the joined length of a ranked cell.
// The fragment tree already carries its length, so this answers without
// materialising the text — the point of the call.
func (r *incrementalRenderSession) IncrementalRankedFragmentBytes(
	ctx context.Context,
	group, cell string,
) (int, error) {
	fragment, err := r.IncrementalRankedTextFragment(ctx, group, cell)
	if err != nil {
		return 0, err
	}
	return incrementalTextFragmentBytes(fragment)
}

// IncrementalRankedFragmentBytes reports the joined length of a ranked cell.
func (r *coldIncrementalRenderer) IncrementalRankedFragmentBytes(
	ctx context.Context,
	group, cell string,
) (int, error) {
	fragment, err := r.IncrementalRankedTextFragment(ctx, group, cell)
	if err != nil {
		return 0, err
	}
	return incrementalTextFragmentBytes(fragment)
}

func incrementalTextFragmentBytes(fragment templating.TextFragment) (int, error) {
	sized, ok := fragment.(interface{ Bytes() (int, error) })
	if !ok {
		return 0, errors.New("incremental ranked fragment cannot report its length")
	}
	return sized.Bytes()
}
