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
	"errors"
	"fmt"
	"slices"

	"gitlab.com/haproxy-haptic/haptic/pkg/persistenttree"
	"gitlab.com/haproxy-haptic/haptic/pkg/rendercontent"
)

type incrementalRankedTextUpdate struct {
	cell    incrementalRankedTextCell
	changes map[string]rendercontent.TextFragmentChange
}

func (u *incrementalGroupIndexUpdate) refreshRankedText() error {
	updates := make(map[string]*incrementalRankedTextUpdate)
	transitionKeys := make([]string, 0, len(u.publicationTransitions))
	for key := range u.publicationTransitions {
		transitionKeys = append(transitionKeys, key)
	}
	slices.Sort(transitionKeys)
	for _, key := range transitionKeys {
		if err := u.stageRankedTextTransition(updates, key); err != nil {
			return err
		}
	}
	for _, cell := range sortedIncrementalBatchKeys(updates) {
		if err := u.commitRankedTextCell(cell, updates[cell]); err != nil {
			return err
		}
	}
	return nil
}

func (u *incrementalGroupIndexUpdate) stageRankedTextTransition(
	updates map[string]*incrementalRankedTextUpdate,
	key string,
) error {
	transition := u.publicationTransitions[key]
	if transition.original == transition.final {
		return nil
	}
	update, err := u.rankedTextUpdate(updates, transition.cell)
	if err != nil {
		return err
	}
	if transition.original.present {
		if err := removeRankedTextWinner(update, transition.cell, &transition.original.value); err != nil {
			return err
		}
	}
	if transition.final.present {
		if err := addRankedTextWinner(update, transition.cell, &transition.final.value); err != nil {
			return err
		}
	}
	return nil
}

func (u *incrementalGroupIndexUpdate) commitRankedTextCell(
	cell string,
	update *incrementalRankedTextUpdate,
) error {
	if update.cell.winnerCount < 0 || update.cell.unrankedCount < 0 ||
		update.cell.nonStringCount < 0 ||
		update.cell.unrankedCount+update.cell.nonStringCount > update.cell.winnerCount {
		return errors.New("incremental ranked text index has invalid winner counts")
	}
	changes := make([]rendercontent.TextFragmentChange, 0, len(update.changes))
	for _, key := range sortedIncrementalBatchKeys(update.changes) {
		changes = append(changes, update.changes[key])
	}
	fragment, err := update.cell.fragment.Apply(changes)
	if err != nil {
		return fmt.Errorf("updating incremental ranked text cell %q: %w", cell, err)
	}
	update.cell.fragment = fragment
	parts, err := fragment.Parts()
	if err != nil {
		return err
	}
	if parts+update.cell.unrankedCount+update.cell.nonStringCount != update.cell.winnerCount {
		return errors.New("incremental ranked text index does not cover its winners")
	}
	cellKey := incrementalOrderedTuple(cell)
	if update.cell.winnerCount == 0 {
		u.rankedText.Delete(cellKey)
	} else {
		update.cell.projection = nil
		u.rankedText.Insert(cellKey, update.cell)
	}
	u.rankedTextChanged[cell] = struct{}{}
	return nil
}

func (u *incrementalGroupIndexUpdate) rankedTextUpdate(
	updates map[string]*incrementalRankedTextUpdate,
	cell string,
) (*incrementalRankedTextUpdate, error) {
	if update := updates[cell]; update != nil {
		return update, nil
	}
	cellKey := incrementalOrderedTuple(cell)
	state, exists := u.rankedText.Get(cellKey)
	projection, projectionExists := u.basePublicationRanks.Root().Get(cellKey)
	if !exists {
		if projectionExists {
			return nil, errors.New("incremental ranked text index is missing a cell")
		}
		state.fragment = rendercontent.EmptyTextFragment()
	} else if err := validateIncrementalRankedTextCell(state, projection, projectionExists); err != nil {
		return nil, err
	}
	update := &incrementalRankedTextUpdate{
		cell: state, changes: make(map[string]rendercontent.TextFragmentChange),
	}
	updates[cell] = update
	return update, nil
}

func removeRankedTextWinner(
	update *incrementalRankedTextUpdate,
	cell string,
	winner *incrementalIndexedPublication,
) error {
	if winner.cell != cell {
		return errors.New("incremental ranked text transition changes cell")
	}
	update.cell.winnerCount--
	if winner.rank == "" {
		update.cell.unrankedCount--
		return nil
	}
	value, err := decodeResourceValue([]byte(winner.value))
	if err != nil {
		return fmt.Errorf("decoding incremental ranked fragment %q/%q: %w", cell, winner.key, err)
	}
	if _, ok := value.(string); !ok {
		update.cell.nonStringCount--
		return nil
	}
	return setRankedTextChange(update, rendercontent.TextFragmentChange{
		Key: string(incrementalPublicationProjectionKey(winner, true)),
	})
}

func addRankedTextWinner(
	update *incrementalRankedTextUpdate,
	cell string,
	winner *incrementalIndexedPublication,
) error {
	if winner.cell != cell {
		return errors.New("incremental ranked text transition changes cell")
	}
	update.cell.winnerCount++
	if winner.rank == "" {
		update.cell.unrankedCount++
		return nil
	}
	value, err := decodeResourceValue([]byte(winner.value))
	if err != nil {
		return fmt.Errorf("decoding incremental ranked fragment %q/%q: %w", cell, winner.key, err)
	}
	text, ok := value.(string)
	if !ok {
		update.cell.nonStringCount++
		return nil
	}
	return setRankedTextChange(update, rendercontent.TextFragmentChange{
		Key:     string(incrementalPublicationProjectionKey(winner, true)),
		Text:    text,
		Present: true,
	})
}

func setRankedTextChange(
	update *incrementalRankedTextUpdate,
	change rendercontent.TextFragmentChange,
) error {
	if previous, exists := update.changes[change.Key]; exists && previous != change {
		if !previous.Present && change.Present {
			update.changes[change.Key] = change
			return nil
		}
		return errors.New("incremental ranked text transitions collide")
	}
	update.changes[change.Key] = change
	return nil
}

func (u *incrementalGroupIndexUpdate) bindRankedTextProjections(
	projections *persistenttree.Tree[*persistenttree.Tree[incrementalIndexedPublication]],
) error {
	for _, cell := range sortedIncrementalBatchKeys(u.rankedTextChanged) {
		key := incrementalOrderedTuple(cell)
		state, exists := u.rankedText.Get(key)
		projection, projectionExists := projections.Root().Get(key)
		if exists != projectionExists {
			return errors.New("incremental ranked text index does not match its projection")
		}
		if !exists {
			continue
		}
		if projection == nil || projection.Len() != state.winnerCount {
			return errors.New("incremental ranked text projection has an invalid winner count")
		}
		state.projection = projection.Root()
		u.rankedText.Insert(key, state)
	}
	return nil
}

func validateIncrementalRankedTextCell(
	cell incrementalRankedTextCell,
	projection *persistenttree.Tree[incrementalIndexedPublication],
	projectionExists bool,
) error {
	if err := cell.fragment.ValidateAuthentication(); err != nil {
		return errors.New("incremental ranked text cell has an invalid fragment")
	}
	parts, err := cell.fragment.Parts()
	if err != nil {
		return err
	}
	if !projectionExists || projection == nil || cell.projection == nil ||
		cell.projection != projection.Root() || cell.winnerCount != projection.Len() ||
		cell.unrankedCount < 0 || cell.nonStringCount < 0 ||
		cell.unrankedCount+cell.nonStringCount > cell.winnerCount ||
		parts+cell.unrankedCount+cell.nonStringCount != cell.winnerCount {
		return errors.New("incremental ranked text cell does not match its projection")
	}
	return nil
}
