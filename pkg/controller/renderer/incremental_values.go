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

package renderer

import (
	"context"
	"errors"
	"fmt"

	"gitlab.com/haproxy-haptic/haptic/pkg/templating"
)

func (r *incrementalRenderSession) IncrementalValues(
	ctx context.Context,
	group, cell string,
) ([]any, error) {
	r.renderMu.Lock()
	defer r.renderMu.Unlock()
	index, err := r.incrementalValuesIndex(ctx, group, cell)
	if err != nil {
		return nil, err
	}
	if r.publicationGeneration != nil {
		if err := r.publicationGeneration.authenticateAuthority(r.publicationAuthority); err != nil {
			return nil, err
		}
		input, winners, inputErr := incrementalSelectorValuesInputWithWinners(index, group, cell)
		if inputErr != nil {
			return nil, inputErr
		}
		values, _, resolved, resolveErr := r.publicationGeneration.resolveSelectorValues(
			index, group, input, winners,
		)
		if resolveErr != nil || resolved {
			return values, resolveErr
		}
	}
	return decodeIncrementalPublishedWinners(index, cell)
}

func (r *incrementalRenderSession) IncrementalValuesCertified(
	ctx context.Context,
	group, cell string,
) (*templating.IncrementalCertifiedValues, error) {
	r.renderMu.Lock()
	defer r.renderMu.Unlock()
	index, err := r.incrementalValuesIndex(ctx, group, cell)
	if err != nil {
		return nil, err
	}
	values, certificate, err := r.certifiedPublicationValues(index, group, cell)
	if err != nil {
		return nil, err
	}
	certified := templating.NewIncrementalCertifiedValues(values, certificate)
	if certified == nil {
		return nil, errors.New("incremental publication memo has an invalid immutable certificate")
	}
	if err := r.recordExactCycleIncrementalObservation(
		ctx, exactCycleIncrementalValues, group, "", cell, "", certified,
	); err != nil {
		return nil, err
	}
	return certified, nil
}

// IncrementalValueCount answers a root's presence test from the cell's winner
// count. The root then depends on the count alone, so the values themselves
// never reach it and a change to one of them does not re-run the root.
func (r *incrementalRenderSession) IncrementalValueCount(
	ctx context.Context,
	group, cell string,
) (int, error) {
	r.renderMu.Lock()
	defer r.renderMu.Unlock()
	index, err := r.incrementalValuesIndex(ctx, group, cell)
	if err != nil {
		return 0, err
	}
	count, err := index.publishedWinnerCount(cell)
	if err != nil {
		return 0, err
	}
	if err := r.recordExactCycleIncrementalObservation(
		ctx, exactCycleIncrementalValueCount, group, "", cell, "", count,
	); err != nil {
		return 0, err
	}
	return count, nil
}

func (r *incrementalRenderSession) incrementalValuesIndex(
	ctx context.Context,
	group, cell string,
) (*incrementalGroupIndex, error) {
	if err := validateIncrementalValueRequest(r.state, group, cell); err != nil {
		return nil, err
	}
	r.valueAccesses[group]++
	if err := r.evaluateGroup(ctx, group); err != nil {
		return nil, err
	}
	if err := r.refreshGroup(group); err != nil {
		return nil, err
	}
	index := r.groupIndexes[group]
	if index == nil {
		return nil, errors.New("incremental publication index is unavailable")
	}
	return index, nil
}

func (r *coldIncrementalRenderer) IncrementalValues(
	ctx context.Context,
	group, cell string,
) ([]any, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	index, err := r.incrementalValuesIndex(ctx, group, cell)
	if err != nil {
		return nil, err
	}
	if r.publicationGeneration != nil {
		if err := r.publicationGeneration.authenticateAuthority(r.publicationAuthority); err != nil {
			return nil, err
		}
		input, winners, inputErr := incrementalSelectorValuesInputWithWinners(index, group, cell)
		if inputErr != nil {
			return nil, inputErr
		}
		values, _, resolved, resolveErr := r.publicationGeneration.resolveSelectorValues(
			index, group, input, winners,
		)
		if resolveErr != nil || resolved {
			return values, resolveErr
		}
	}
	return decodeIncrementalPublishedWinners(index, cell)
}

func (r *coldIncrementalRenderer) IncrementalValuesCertified(
	ctx context.Context,
	group, cell string,
) (*templating.IncrementalCertifiedValues, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	index, err := r.incrementalValuesIndex(ctx, group, cell)
	if err != nil {
		return nil, err
	}
	values, certificate, err := r.certifiedPublicationValues(index, group, cell)
	if err != nil {
		return nil, err
	}
	certified := templating.NewIncrementalCertifiedValues(values, certificate)
	if certified == nil {
		return nil, errors.New("incremental publication memo has an invalid immutable certificate")
	}
	return certified, nil
}

func (r *coldIncrementalRenderer) IncrementalValueCount(
	ctx context.Context,
	group, cell string,
) (int, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	index, err := r.incrementalValuesIndex(ctx, group, cell)
	if err != nil {
		return 0, err
	}
	return index.publishedWinnerCount(cell)
}

func (r *coldIncrementalRenderer) incrementalValuesIndex(
	ctx context.Context,
	group, cell string,
) (*incrementalGroupIndex, error) {
	if err := validateIncrementalValueRequest(r.state, group, cell); err != nil {
		return nil, err
	}
	r.valueAccesses[group]++
	if _, err := r.renderGroup(ctx, group); err != nil {
		return nil, err
	}
	index := r.groupIndexes[group]
	if index == nil {
		return nil, errors.New("incremental publication index is unavailable")
	}
	return index, nil
}

func validateIncrementalValueRequest(state *incrementalRenderState, group, cell string) error {
	if state == nil {
		return errors.New("incremental values have no render state")
	}
	if group == "" || cell == "" {
		return errors.New("incremental_values requires a non-empty group and cell")
	}
	components, exists := state.groups[group]
	if !exists {
		return fmt.Errorf("incremental group %q is not configured", group)
	}
	for index := range components {
		if components[index].publishValue {
			return nil
		}
	}
	return fmt.Errorf("incremental group %q does not declare publishValue", group)
}

func decodeIncrementalPublishedWinners(index *incrementalGroupIndex, cell string) ([]any, error) {
	winners, err := index.publishedWinners(cell)
	if err != nil {
		return nil, err
	}
	values := make([]any, len(winners))
	for winnerIndex := range winners {
		value, decodeErr := decodeResourceValue(winners[winnerIndex].value.Value)
		if decodeErr != nil {
			return nil, fmt.Errorf("decoding incremental publication %q/%q: %w",
				cell, winners[winnerIndex].value.Key, decodeErr)
		}
		values[winnerIndex] = value
	}
	return values, nil
}
