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
	"errors"
	"fmt"
	"slices"

	iradix "github.com/hashicorp/go-immutable-radix/v2"

	"gitlab.com/haproxy-haptic/haptic/pkg/persistenttree"
	"gitlab.com/haproxy-haptic/haptic/pkg/rendercontent"
)

type incrementalGroupAddBatch struct {
	update                 *incrementalGroupIndexUpdate
	contributions          map[string]*incrementalContributionAddBatch
	publications           map[string]*incrementalPublicationAddBatch
	publicationLocations   map[string]*incrementalPublicationProjectionBatch
	publicationRanks       map[string]*incrementalPublicationProjectionBatch
	publicationCountDeltas map[string]int
	events                 map[string]*incrementalEventAddBatch
	http                   map[string]*incrementalHTTPAddBatch
}

type incrementalContributionAddBatch struct {
	key []byte
	txn *iradix.Txn[incrementalIndexedContribution]
}

type incrementalPublicationAddBatch struct {
	key      []byte
	txn      *persistenttree.Txn[incrementalIndexedPublication]
	previous *incrementalIndexedPublication
	cell     string
	ranked   bool
	created  bool
}

type incrementalPublicationProjectionBatch struct {
	key []byte
	txn *persistenttree.Txn[incrementalIndexedPublication]
}

type incrementalEventAddBatch struct {
	key []byte
	txn *iradix.Txn[incrementalIndexedEvent]
}

type incrementalHTTPAddBatch struct {
	key            []byte
	txn            *iradix.Txn[incrementalIndexedHTTP]
	representative *incrementalHTTPEffect
}

type incrementalOutputAddBatch struct {
	key     []byte
	output  rendercontent.Output
	changes []rendercontent.Change
}

func (u *incrementalGroupIndexUpdate) addPreparedBatch(
	ids []incrementalGroupInstanceID,
	instances []incrementalIndexedGroupInstance,
	results []incrementalComponentResult,
) error {
	if len(ids) != len(instances) || len(ids) != len(results) {
		return errors.New("incremental group batch has inconsistent prepared state")
	}
	batch := incrementalGroupAddBatch{
		update:                 u,
		contributions:          map[string]*incrementalContributionAddBatch{},
		publications:           map[string]*incrementalPublicationAddBatch{},
		publicationLocations:   map[string]*incrementalPublicationProjectionBatch{},
		publicationRanks:       map[string]*incrementalPublicationProjectionBatch{},
		publicationCountDeltas: map[string]int{},
		events:                 map[string]*incrementalEventAddBatch{},
		http:                   map[string]*incrementalHTTPAddBatch{},
	}
	for index := range ids {
		identityKey := incrementalGroupInstanceKey(ids[index])
		u.affectedInstances[string(identityKey)] = ids[index]
		u.preparedResults[string(identityKey)] = &results[index]
		if err := batch.add(&instances[index], &results[index], identityKey); err != nil {
			return err
		}
	}
	if err := batch.finish(); err != nil {
		return err
	}
	rememberCurrentContributionWinners(u.contributors, u.affectedContributions, u.affectedInstances)
	return u.refreshPreparedBatchChunks()
}

func (b *incrementalGroupAddBatch) add(
	instance *incrementalIndexedGroupInstance,
	result *incrementalComponentResult,
	identityKey []byte,
) error {
	if err := b.addContributions(instance.id, result); err != nil {
		return err
	}
	if err := b.addPublications(instance.id, result); err != nil {
		return err
	}
	if err := b.addEvents(instance.id, result); err != nil {
		return err
	}
	if err := addStatusPatchCalls(
		instance.id, result, b.update.status, b.update.memo.authority, b.update.removedStatus,
	); err != nil {
		return err
	}
	if err := b.addHTTP(instance); err != nil {
		return err
	}
	if _, duplicate := b.update.instances.Insert(identityKey, *instance); duplicate {
		return errors.New("incremental group batch repeats an instance")
	}
	return nil
}

func (b *incrementalGroupAddBatch) addContributions(
	id incrementalGroupInstanceID,
	result *incrementalComponentResult,
) error {
	for index := range result.Unique {
		value := result.Unique[index]
		key := incrementalContributionIdentityKey(value)
		keyString := string(key)
		entry := b.contributions[keyString]
		if entry == nil {
			tree, exists := b.update.contributors.Get(key)
			if !exists {
				tree = iradix.New[incrementalIndexedContribution]()
			} else if tree == nil {
				return errors.New("incremental contribution identity has no owners")
			} else if _, winner, found := tree.Root().Minimum(); found {
				winnerKey := incrementalGroupInstanceKey(winner.instance)
				b.update.affectedInstances[string(winnerKey)] = winner.instance
			}
			entry = &incrementalContributionAddBatch{key: key, txn: tree.Txn()}
			b.contributions[keyString] = entry
			b.update.affectedContributions[keyString] = key
		}
		location := incrementalGroupLocationKey(id, uint64(index))
		if _, duplicate := entry.txn.Insert(location, incrementalIndexedContribution{
			instance: id, location: string(location), value: value,
		}); duplicate {
			return errors.New("incremental contribution index repeats an instance")
		}
	}
	return nil
}

func (b *incrementalGroupAddBatch) addPublications(
	id incrementalGroupInstanceID,
	result *incrementalComponentResult,
) error {
	for index := range result.Published {
		value := result.Published[index]
		key := incrementalPublicationIdentityKey(value.Cell, value.Key)
		keyString := string(key)
		entry := b.publications[keyString]
		if entry == nil {
			created, err := b.newPublicationBatch(key, value.Cell, value.Rank)
			if err != nil {
				return err
			}
			entry = created
			b.publications[keyString] = entry
		} else if entry.cell != value.Cell || entry.ranked != (value.Rank != "") {
			return errors.New("incremental publication identity mixes ranked and unranked owners")
		}
		location := incrementalGroupLocationKey(id, uint64(index))
		if _, duplicate := entry.txn.Insert(
			incrementalPublicationOwnerKey(value.Rank, location),
			incrementalIndexedPublication{
				instance: id, location: string(location), cell: value.Cell,
				key: value.Key, rank: value.Rank, value: string(value.Value),
			},
		); duplicate {
			return errors.New("incremental publication index repeats an instance")
		}
	}
	return nil
}

func (b *incrementalGroupAddBatch) newPublicationBatch(
	key []byte,
	cell, rank string,
) (*incrementalPublicationAddBatch, error) {
	tree, exists := b.update.publications.Get(key)
	entry := &incrementalPublicationAddBatch{
		key: key, cell: cell, ranked: rank != "", created: !exists,
	}
	if !exists {
		tree = persistenttree.New[incrementalIndexedPublication]()
		b.publicationCountDeltas[cell]++
	} else {
		if tree == nil {
			return nil, errors.New("incremental publication identity has no owners")
		}
		_, winner, found := tree.Root().Minimum()
		if !found {
			return nil, errors.New("incremental publication identity has no owners")
		}
		entry.previous = &winner
		if (winner.rank == "") != (rank == "") {
			return nil, errors.New("incremental publication identity mixes ranked and unranked owners")
		}
	}
	entry.txn = tree.Txn()
	return entry, nil
}

func (b *incrementalGroupAddBatch) addEvents(
	id incrementalGroupInstanceID,
	result *incrementalComponentResult,
) error {
	for index := range result.Events {
		value := result.Events[index]
		key := incrementalEventIdentityKey(&value)
		keyString := string(key)
		entry := b.events[keyString]
		if entry == nil {
			tree, exists := b.update.events.Get(key)
			if !exists {
				tree = iradix.New[incrementalIndexedEvent]()
			} else if tree == nil {
				return errors.New("incremental event identity has no owners")
			}
			entry = &incrementalEventAddBatch{key: key, txn: tree.Txn()}
			b.events[keyString] = entry
		}
		location := incrementalGroupLocationKey(id, uint64(index))
		if _, duplicate := entry.txn.Insert(location, incrementalIndexedEvent{
			location: string(location), value: value,
		}); duplicate {
			return errors.New("incremental event index repeats an instance")
		}
	}
	return nil
}

func (b *incrementalGroupAddBatch) addHTTP(instance *incrementalIndexedGroupInstance) error {
	locationIndex := uint64(0)
	var addErr error
	instance.httpEffects.Root().Walk(func(_ []byte, value incrementalHTTPEffect) bool {
		key := incrementalHTTPIdentityKey(value.inputID)
		keyString := string(key)
		entry := b.http[keyString]
		if entry == nil {
			tree, exists := b.update.http.Get(key)
			if !exists {
				tree = iradix.New[incrementalIndexedHTTP]()
			} else if tree == nil {
				addErr = errors.New("incremental HTTP identity has no owners")
				return true
			}
			entry = &incrementalHTTPAddBatch{key: key, txn: tree.Txn()}
			if _, representative, found := tree.Root().Maximum(); found {
				representativeValue := representative.value
				entry.representative = &representativeValue
			}
			b.http[keyString] = entry
		}
		if entry.representative != nil &&
			!sameHTTPReusableSnapshot(&entry.representative.snapshot, &value.snapshot) {
			addErr = fmt.Errorf("incremental HTTP input %d has conflicting snapshots", value.inputID)
			return true
		}
		if entry.representative == nil {
			representative := value
			entry.representative = &representative
		}
		location := incrementalGroupLocationKey(instance.id, locationIndex)
		locationIndex++
		if _, duplicate := entry.txn.Insert(location, incrementalIndexedHTTP{
			location: string(location), value: value,
		}); duplicate {
			addErr = errors.New("incremental HTTP index repeats an instance")
			return true
		}
		return false
	})
	return addErr
}

func (b *incrementalGroupAddBatch) finish() error {
	for _, key := range sortedIncrementalBatchKeys(b.contributions) {
		entry := b.contributions[key]
		b.update.contributors.Insert(entry.key, entry.txn.Commit())
	}
	for _, key := range sortedIncrementalBatchKeys(b.events) {
		entry := b.events[key]
		b.update.events.Insert(entry.key, entry.txn.Commit())
	}
	for _, key := range sortedIncrementalBatchKeys(b.http) {
		entry := b.http[key]
		b.update.http.Insert(entry.key, entry.txn.Commit())
	}
	if err := b.finishPublications(); err != nil {
		return err
	}
	return b.finishPublicationCounts()
}

func (b *incrementalGroupAddBatch) finishPublicationEntry(
	entry *incrementalPublicationAddBatch,
) error {
	updated := entry.txn.Commit()
	_, next, found := updated.Root().Minimum()
	if !found {
		return errors.New("incremental publication identity has no owners")
	}
	b.update.publications.Insert(entry.key, updated)
	if err := recordIncrementalPublicationTransition(
		b.update.publicationTransitions, entry.previous, &next,
	); err != nil {
		return err
	}
	if entry.previous != nil && *entry.previous == next {
		return nil
	}
	if entry.previous != nil {
		if err := b.removePublicationProjection(b.publicationLocations, entry.previous, false); err != nil {
			return err
		}
		if err := b.removePublicationProjection(b.publicationRanks, entry.previous, true); err != nil {
			return err
		}
	}
	if err := b.addPublicationProjection(b.publicationLocations, &next, false); err != nil {
		return err
	}
	return b.addPublicationProjection(b.publicationRanks, &next, true)
}

func (b *incrementalGroupAddBatch) finishPublications() error {
	for _, key := range sortedIncrementalBatchKeys(b.publications) {
		if err := b.finishPublicationEntry(b.publications[key]); err != nil {
			return err
		}
	}
	if err := b.finishPublicationProjection(
		b.publicationLocations, b.update.publicationWinnersByLocation,
	); err != nil {
		return err
	}
	return b.finishPublicationProjection(b.publicationRanks, b.update.publicationWinnersByRank)
}

func (b *incrementalGroupAddBatch) projection(
	entries map[string]*incrementalPublicationProjectionBatch,
	winner *incrementalIndexedPublication,
	root *persistenttree.Txn[*persistenttree.Tree[incrementalIndexedPublication]],
) (*incrementalPublicationProjectionBatch, error) {
	cellKey := incrementalOrderedTuple(winner.cell)
	keyString := string(cellKey)
	entry := entries[keyString]
	if entry != nil {
		return entry, nil
	}
	tree, exists := root.Get(cellKey)
	if !exists {
		tree = persistenttree.New[incrementalIndexedPublication]()
	} else if tree == nil {
		return nil, errors.New("incremental publication winner projection has an empty cell")
	}
	entry = &incrementalPublicationProjectionBatch{key: cellKey, txn: tree.Txn()}
	entries[keyString] = entry
	return entry, nil
}

func (b *incrementalGroupAddBatch) removePublicationProjection(
	entries map[string]*incrementalPublicationProjectionBatch,
	winner *incrementalIndexedPublication,
	ranked bool,
) error {
	root := b.update.publicationWinnersByLocation
	if ranked {
		root = b.update.publicationWinnersByRank
	}
	entry, err := b.projection(entries, winner, root)
	if err != nil {
		return err
	}
	key := incrementalPublicationProjectionKey(winner, ranked)
	indexed, exists := entry.txn.Get(key)
	if !exists || indexed != *winner {
		return errors.New("incremental publication winner projection does not match its owner")
	}
	if _, removed := entry.txn.Delete(key); !removed {
		return errors.New("incremental publication winner projection is missing a winner")
	}
	return nil
}

func (b *incrementalGroupAddBatch) addPublicationProjection(
	entries map[string]*incrementalPublicationProjectionBatch,
	winner *incrementalIndexedPublication,
	ranked bool,
) error {
	root := b.update.publicationWinnersByLocation
	if ranked {
		root = b.update.publicationWinnersByRank
	}
	entry, err := b.projection(entries, winner, root)
	if err != nil {
		return err
	}
	if _, duplicate := entry.txn.Insert(incrementalPublicationProjectionKey(winner, ranked), *winner); duplicate {
		return errors.New("incremental publication winner projection repeats a location")
	}
	return nil
}

func (b *incrementalGroupAddBatch) finishPublicationProjection(
	entries map[string]*incrementalPublicationProjectionBatch,
	root *persistenttree.Txn[*persistenttree.Tree[incrementalIndexedPublication]],
) error {
	for _, key := range sortedIncrementalBatchKeys(entries) {
		entry := entries[key]
		tree := entry.txn.Commit()
		if tree.Len() == 0 {
			root.Delete(entry.key)
		} else {
			root.Insert(entry.key, tree)
		}
	}
	return nil
}

func (b *incrementalGroupAddBatch) finishPublicationCounts() error {
	cells := make([]string, 0, len(b.publicationCountDeltas))
	for cell := range b.publicationCountDeltas {
		cells = append(cells, cell)
	}
	slices.Sort(cells)
	for _, cell := range cells {
		key := incrementalOrderedTuple(cell)
		current, _ := b.update.publicationCounts.Get(key)
		next := current + b.publicationCountDeltas[cell]
		if next <= 0 {
			return errors.New("incremental publication count is not positive")
		}
		b.update.publicationCounts.Insert(key, next)
	}
	return nil
}

func (u *incrementalGroupIndexUpdate) refreshPreparedBatchChunks() error {
	instanceKeys := make([]string, 0, len(u.affectedInstances))
	for key := range u.affectedInstances {
		instanceKeys = append(instanceKeys, key)
	}
	slices.Sort(instanceKeys)
	components := map[string]*incrementalOutputAddBatch{}
	for _, instanceKey := range instanceKeys {
		id := u.affectedInstances[instanceKey]
		identityKey := incrementalGroupInstanceKey(id)
		instance, exists := u.instances.Get(identityKey)
		chunk := ""
		if exists {
			computed, err := u.instanceChunk(identityKey, &instance)
			if err != nil {
				return err
			}
			chunk = computed
		}
		entry, err := u.outputBatch(components, id.component)
		if err != nil {
			return err
		}
		chunkKey := string(incrementalComponentInstanceKey(id))
		previous, found, err := entry.output.Get(chunkKey)
		if err != nil {
			return err
		}
		if found && previous == chunk || !found && chunk == "" {
			continue
		}
		entry.changes = append(entry.changes, rendercontent.Change{Key: chunkKey, Text: chunk})
	}
	return u.applyOutputBatches(components)
}

func (u *incrementalGroupIndexUpdate) instanceChunk(
	identityKey []byte,
	instance *incrementalIndexedGroupInstance,
) (string, error) {
	result := u.preparedResults[string(identityKey)]
	if result == nil {
		decoded, err := decodeIndexedGroupInstanceResult(instance)
		if err != nil {
			return "", err
		}
		result = &decoded
	}
	return incrementalInstanceChunk(instance, result, u.contributors)
}

func (u *incrementalGroupIndexUpdate) outputBatch(
	components map[string]*incrementalOutputAddBatch,
	component string,
) (*incrementalOutputAddBatch, error) {
	if entry := components[component]; entry != nil {
		return entry, nil
	}
	componentKey := []byte(component)
	chunks, found := u.outputs.Get(componentKey)
	if !found {
		chunks = incrementalComponentChunks{output: rendercontent.Empty()}
	} else if err := chunks.output.ValidateAuthentication(); err != nil {
		return nil, errors.New("incremental component output is unavailable")
	}
	entry := &incrementalOutputAddBatch{key: componentKey, output: chunks.output}
	components[component] = entry
	return entry, nil
}

func (u *incrementalGroupIndexUpdate) applyOutputBatches(
	components map[string]*incrementalOutputAddBatch,
) error {
	for _, component := range sortedIncrementalBatchKeys(components) {
		entry := components[component]
		output, err := entry.output.Apply(entry.changes)
		if err != nil {
			return err
		}
		parts, err := output.Parts()
		if err != nil {
			return err
		}
		if parts == 0 {
			u.outputs.Delete(entry.key)
		} else {
			u.outputs.Insert(entry.key, incrementalComponentChunks{output: output})
		}
	}
	return nil
}

func sortedIncrementalBatchKeys[T any](values map[string]T) []string {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	slices.Sort(keys)
	return keys
}
