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
	"strings"

	iradix "github.com/hashicorp/go-immutable-radix/v2"

	"gitlab.com/haproxy-haptic/haptic/pkg/incremental"
	"gitlab.com/haproxy-haptic/haptic/pkg/persistenttree"
	"gitlab.com/haproxy-haptic/haptic/pkg/rendercontent"
)

type incrementalValidatedGroupBatch struct {
	ids       []incrementalGroupInstanceID
	instances []incrementalIndexedGroupInstance
	results   []incrementalComponentResult
}

type incrementalEmptyGroupIndexBuilder struct {
	instances                    []persistenttree.Entry[incrementalIndexedGroupInstance]
	contributors                 *iradix.Txn[*iradix.Tree[incrementalIndexedContribution]]
	publications                 []persistenttree.Entry[*persistenttree.Tree[incrementalIndexedPublication]]
	publicationWinnersByLocation []persistenttree.Entry[*persistenttree.Tree[incrementalIndexedPublication]]
	publicationWinnersByRank     []persistenttree.Entry[*persistenttree.Tree[incrementalIndexedPublication]]
	publicationCounts            []persistenttree.Entry[int]
	events                       *iradix.Txn[*iradix.Tree[incrementalIndexedEvent]]
	status                       *iradix.Txn[incrementalIndexedStatusPatchCall]
	http                         *iradix.Txn[*iradix.Tree[incrementalIndexedHTTP]]
	outputs                      *iradix.Txn[incrementalComponentChunks]
	rankedText                   *iradix.Txn[incrementalRankedTextCell]
	memo                         *incrementalGroupMemo

	contributionBatches map[string]*incrementalEmptyContributionBatch
	publicationBatches  map[string]*incrementalEmptyPublicationBatch
	locationBatches     map[string]*incrementalEmptyPublicationProjectionBatch
	rankBatches         map[string]*incrementalEmptyPublicationProjectionBatch
	publicationCells    map[string]int
	eventBatches        map[string]*incrementalEmptyEventBatch
	httpBatches         map[string]*incrementalEmptyHTTPBatch
	rankedUpdates       map[string]*incrementalRankedTextUpdate
}

type incrementalEmptyContributionBatch struct {
	key []byte
	txn *iradix.Txn[incrementalIndexedContribution]
}

type incrementalEmptyPublicationBatch struct {
	key     []byte
	cell    string
	ranked  bool
	entries []persistenttree.Entry[incrementalIndexedPublication]
}

type incrementalEmptyPublicationProjectionBatch struct {
	key     []byte
	entries []persistenttree.Entry[incrementalIndexedPublication]
}

type incrementalEmptyEventBatch struct {
	key []byte
	txn *iradix.Txn[incrementalIndexedEvent]
}

type incrementalEmptyHTTPBatch struct {
	key            []byte
	txn            *iradix.Txn[incrementalIndexedHTTP]
	representative *incrementalHTTPEffect
}

func prepareIncrementalGroupBatch(
	index *incrementalGroupIndex,
	candidates []incrementalPreparedGroupInstance,
) (*incrementalValidatedGroupBatch, error) {
	batch := &incrementalValidatedGroupBatch{
		ids:       make([]incrementalGroupInstanceID, len(candidates)),
		instances: make([]incrementalIndexedGroupInstance, len(candidates)),
		results:   make([]incrementalComponentResult, len(candidates)),
	}
	transfer := &incrementalGroupBatchTransfer{ownership: make([]bool, len(candidates))}
	seen := make(map[string]struct{}, len(candidates))
	for candidateIndex := range candidates {
		if err := prepareIncrementalGroupBatchCandidate(
			index, candidates, candidateIndex, batch, transfer, seen,
		); err != nil {
			return nil, err
		}
	}
	if transfer.arena != nil && transfer.traditional {
		return nil, errors.New("incremental group batch mixes result authority generations")
	}
	if transfer.arena != nil {
		if err := transfer.arena.takeManyInto(
			transfer.fresh,
			transfer.keys,
			transfer.roots,
			batch.results,
			transfer.indexes,
		); err != nil {
			return nil, err
		}
	}
	for candidateIndex := range candidates {
		if !transfer.ownership[candidateIndex] || candidates[candidateIndex].fresh.arena != nil {
			continue
		}
		candidate := &candidates[candidateIndex]
		result, err := takeAuthenticatedFreshComponentResult(
			candidate.fresh, candidate.queryKey, candidate.encoded,
		)
		if err != nil {
			return nil, err
		}
		batch.results[candidateIndex] = result
	}
	for candidateIndex := range candidates {
		if err := validateIncrementalPublicationResultOwner(
			&batch.results[candidateIndex], batch.ids[candidateIndex],
		); err != nil {
			return nil, err
		}
		if err := validateIncrementalPublicationResultGroup(
			&batch.results[candidateIndex], candidates[candidateIndex].component.group,
		); err != nil {
			return nil, err
		}
	}
	return batch, nil
}

type incrementalGroupBatchTransfer struct {
	ownership   []bool
	arena       *incrementalColdResultArena
	fresh       []*authenticatedFreshComponentResult
	keys        []incremental.QueryKey
	roots       []incremental.ExactValueRoot
	indexes     []int
	traditional bool
}

func (t *incrementalGroupBatchTransfer) record(
	candidate *incrementalPreparedGroupInstance,
	candidateIndex int,
) error {
	t.ownership[candidateIndex] = true
	if candidate.fresh.arena == nil {
		t.traditional = true
		return nil
	}
	if t.arena == nil {
		t.arena = candidate.fresh.arena
	} else if t.arena != candidate.fresh.arena {
		return errors.New("incremental group batch spans multiple result arenas")
	}
	t.fresh = append(t.fresh, candidate.fresh)
	t.keys = append(t.keys, candidate.queryKey)
	t.roots = append(t.roots, candidate.encoded)
	t.indexes = append(t.indexes, candidateIndex)
	return nil
}

func prepareIncrementalGroupBatchCandidate(
	index *incrementalGroupIndex,
	candidates []incrementalPreparedGroupInstance,
	candidateIndex int,
	batch *incrementalValidatedGroupBatch,
	transfer *incrementalGroupBatchTransfer,
	seen map[string]struct{},
) error {
	candidate := &candidates[candidateIndex]
	if candidate.instance == nil || candidate.component == nil {
		return errors.New("incremental group batch has an incomplete instance")
	}
	if candidate.component.name != candidate.instance.component {
		return errors.New("incremental group batch component provenance does not match")
	}
	certified, err := validateAuthenticatedFreshComponentEffects(
		candidate.fresh,
		candidate.queryKey,
		candidate.encoded,
		candidate.component,
		candidate.instance.source,
		candidate.instance.namespace,
		candidate.instance.name,
	)
	if err != nil {
		return incrementalInstanceError(candidate.instance, err)
	}
	if certified {
		if recordErr := transfer.record(candidate, candidateIndex); recordErr != nil {
			return recordErr
		}
	} else if materializeErr := materializeIncrementalGroupBatchResult(
		candidate, candidateIndex, batch,
	); materializeErr != nil {
		return materializeErr
	}
	httpEffects, err := newIncrementalIndexedHTTPEffects(candidate.httpEffects)
	if err != nil {
		return err
	}
	id := incrementalGroupInstanceID{
		component: candidate.instance.component,
		source:    candidate.instance.source,
		namespace: candidate.instance.namespace,
		name:      candidate.instance.name,
	}
	identityKey := incrementalGroupInstanceKey(id)
	if _, duplicate := seen[string(identityKey)]; duplicate {
		return errors.New("incremental group batch repeats an instance")
	}
	seen[string(identityKey)] = struct{}{}
	if _, exists := index.instances.Root().Get(identityKey); exists {
		return errors.New("incremental group batch can only add new instances")
	}
	batch.ids[candidateIndex] = id
	batch.instances[candidateIndex] = incrementalIndexedGroupInstance{
		id: id, encodedResult: candidate.fresh.encoded, httpEffects: httpEffects,
	}
	return nil
}

func materializeIncrementalGroupBatchResult(
	candidate *incrementalPreparedGroupInstance,
	candidateIndex int,
	batch *incrementalValidatedGroupBatch,
) error {
	result, err := materializeAuthenticatedFreshComponentResult(
		candidate.fresh, candidate.queryKey, candidate.encoded,
	)
	if err != nil {
		return err
	}
	if err := validateIncrementalEffects(
		candidate.component,
		candidate.instance.source,
		candidate.instance.namespace,
		candidate.instance.name,
		&result,
	); err != nil {
		return incrementalInstanceError(candidate.instance, err)
	}
	batch.results[candidateIndex] = result
	return nil
}

func (i *incrementalGroupIndex) authenticatedStructurallyEmpty() (bool, error) {
	if err := i.validateAuthentication(); err != nil {
		return false, err
	}
	return i.instances.Len() == 0 && i.contributors.Len() == 0 &&
		i.publications.Len() == 0 && i.publicationWinnersByLocation.Len() == 0 &&
		i.publicationWinnersByRank.Len() == 0 && i.publicationCounts.Len() == 0 &&
		i.events.Len() == 0 && i.status.Len() == 0 && i.http.Len() == 0 &&
		i.outputs.Len() == 0 && i.rankedText.Len() == 0, nil
}

func (i *incrementalGroupIndex) addPreparedEmptyBatch(
	batch *incrementalValidatedGroupBatch,
) (*incrementalGroupIndex, error) {
	empty, err := i.authenticatedStructurallyEmpty()
	if err != nil {
		return nil, err
	}
	if !empty {
		return nil, errors.New("incremental group batch requires an empty index")
	}
	if batch == nil || len(batch.ids) == 0 || len(batch.ids) != len(batch.instances) ||
		len(batch.ids) != len(batch.results) {
		return nil, errors.New("incremental group batch has inconsistent prepared state")
	}
	memo, err := i.memo.fork()
	if err != nil {
		return nil, err
	}
	builder := &incrementalEmptyGroupIndexBuilder{
		instances:                    make([]persistenttree.Entry[incrementalIndexedGroupInstance], 0, len(batch.ids)),
		contributors:                 i.contributors.Txn(),
		publications:                 nil,
		publicationWinnersByLocation: nil,
		publicationWinnersByRank:     nil,
		publicationCounts:            nil,
		events:                       i.events.Txn(),
		status:                       i.status.Txn(),
		http:                         i.http.Txn(),
		outputs:                      i.outputs.Txn(),
		rankedText:                   i.rankedText.Txn(),
		memo:                         memo,
	}
	for batchIndex := range batch.ids {
		if err := builder.add(
			batch.ids[batchIndex], &batch.instances[batchIndex], &batch.results[batchIndex],
		); err != nil {
			return nil, err
		}
	}
	return builder.finish(batch)
}

func (b *incrementalEmptyGroupIndexBuilder) add(
	id incrementalGroupInstanceID,
	instance *incrementalIndexedGroupInstance,
	result *incrementalComponentResult,
) error {
	identityKey := incrementalGroupInstanceKey(id)
	b.instances = append(b.instances, persistenttree.NewEntry(identityKey, *instance))
	if err := b.addContributions(id, identityKey, result); err != nil {
		return err
	}
	if err := b.addPublications(id, identityKey, result); err != nil {
		return err
	}
	if err := b.addEvents(identityKey, result); err != nil {
		return err
	}
	if err := addStatusPatchCallsForInstanceKey(
		identityKey, result, b.status, b.memo.authority, nil,
	); err != nil {
		return err
	}
	return b.addHTTP(instance, identityKey)
}

func (b *incrementalEmptyGroupIndexBuilder) addContributions(
	id incrementalGroupInstanceID,
	instanceKey []byte,
	result *incrementalComponentResult,
) error {
	for valueIndex := range result.Unique {
		value := result.Unique[valueIndex]
		key := incrementalContributionIdentityKey(value)
		keyString := string(key)
		entry := b.contributionBatches[keyString]
		if entry == nil {
			if b.contributionBatches == nil {
				b.contributionBatches = map[string]*incrementalEmptyContributionBatch{}
			}
			entry = &incrementalEmptyContributionBatch{
				key: key, txn: iradix.New[incrementalIndexedContribution]().Txn(),
			}
			b.contributionBatches[keyString] = entry
		}
		location := incrementalGroupLocationKeyForInstanceKey(instanceKey, uint64(valueIndex))
		if _, duplicate := entry.txn.Insert(location, incrementalIndexedContribution{
			instance: id, location: string(location), value: value,
		}); duplicate {
			return errors.New("incremental contribution index repeats an instance")
		}
	}
	return nil
}

func (b *incrementalEmptyGroupIndexBuilder) addPublications(
	id incrementalGroupInstanceID,
	instanceKey []byte,
	result *incrementalComponentResult,
) error {
	for valueIndex := range result.Published {
		value := result.Published[valueIndex]
		key := incrementalPublicationIdentityKey(value.Cell, value.Key)
		keyString := string(key)
		entry := b.publicationBatches[keyString]
		if entry == nil {
			if b.publicationBatches == nil {
				b.publicationBatches = map[string]*incrementalEmptyPublicationBatch{}
			}
			entry = &incrementalEmptyPublicationBatch{
				key: key, cell: value.Cell, ranked: value.Rank != "",
			}
			b.publicationBatches[keyString] = entry
		} else if entry.cell != value.Cell || entry.ranked != (value.Rank != "") {
			return errors.New("incremental publication identity mixes ranked and unranked owners")
		}
		location := incrementalGroupLocationKeyForInstanceKey(instanceKey, uint64(valueIndex))
		entry.entries = append(entry.entries, persistenttree.NewEntry(
			incrementalPublicationOwnerKey(value.Rank, location),
			incrementalIndexedPublication{
				instance: id, location: string(location), cell: value.Cell,
				key: value.Key, rank: value.Rank, value: string(value.Value),
			},
		))
	}
	return nil
}

func (b *incrementalEmptyGroupIndexBuilder) addEvents(
	instanceKey []byte,
	result *incrementalComponentResult,
) error {
	for valueIndex := range result.Events {
		value := result.Events[valueIndex]
		key := incrementalEventIdentityKey(&value)
		keyString := string(key)
		entry := b.eventBatches[keyString]
		if entry == nil {
			if b.eventBatches == nil {
				b.eventBatches = map[string]*incrementalEmptyEventBatch{}
			}
			entry = &incrementalEmptyEventBatch{
				key: key, txn: iradix.New[incrementalIndexedEvent]().Txn(),
			}
			b.eventBatches[keyString] = entry
		}
		location := incrementalGroupLocationKeyForInstanceKey(instanceKey, uint64(valueIndex))
		if _, duplicate := entry.txn.Insert(location, incrementalIndexedEvent{
			location: string(location), value: value,
		}); duplicate {
			return errors.New("incremental event index repeats an instance")
		}
	}
	return nil
}

func (b *incrementalEmptyGroupIndexBuilder) addHTTP(
	instance *incrementalIndexedGroupInstance,
	instanceKey []byte,
) error {
	locationIndex := uint64(0)
	var addErr error
	instance.httpEffects.Root().Walk(func(_ []byte, value incrementalHTTPEffect) bool {
		key := incrementalHTTPIdentityKey(value.inputID)
		keyString := string(key)
		entry := b.httpBatches[keyString]
		if entry == nil {
			if b.httpBatches == nil {
				b.httpBatches = map[string]*incrementalEmptyHTTPBatch{}
			}
			entry = &incrementalEmptyHTTPBatch{
				key: key, txn: iradix.New[incrementalIndexedHTTP]().Txn(),
			}
			b.httpBatches[keyString] = entry
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
		location := incrementalGroupLocationKeyForInstanceKey(instanceKey, locationIndex)
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

func (b *incrementalEmptyGroupIndexBuilder) finish(
	batch *incrementalValidatedGroupBatch,
) (*incrementalGroupIndex, error) {
	slices.SortFunc(b.instances, func(left, right persistenttree.Entry[incrementalIndexedGroupInstance]) int {
		return strings.Compare(left.Key, right.Key)
	})
	instances, err := persistenttree.NewFromSorted(b.instances)
	if err != nil {
		return nil, err
	}
	for _, key := range sortedIncrementalBatchKeys(b.contributionBatches) {
		entry := b.contributionBatches[key]
		b.contributors.Insert(entry.key, entry.txn.Commit())
	}
	for _, key := range sortedIncrementalBatchKeys(b.eventBatches) {
		entry := b.eventBatches[key]
		b.events.Insert(entry.key, entry.txn.Commit())
	}
	for _, key := range sortedIncrementalBatchKeys(b.httpBatches) {
		entry := b.httpBatches[key]
		b.http.Insert(entry.key, entry.txn.Commit())
	}
	if err := b.finishPublications(); err != nil {
		return nil, err
	}
	publications, err := persistenttree.NewFrom(b.publications)
	if err != nil {
		return nil, err
	}
	publicationLocations, err := persistenttree.NewFrom(b.publicationWinnersByLocation)
	if err != nil {
		return nil, err
	}
	publicationRanks, err := persistenttree.NewFrom(b.publicationWinnersByRank)
	if err != nil {
		return nil, err
	}
	publicationCounts, err := persistenttree.NewFrom(b.publicationCounts)
	if err != nil {
		return nil, err
	}
	if err := b.finishOutputs(batch); err != nil {
		return nil, err
	}
	if err := b.finishRankedText(publicationRanks); err != nil {
		return nil, err
	}
	return &incrementalGroupIndex{
		instances:                    instances,
		contributors:                 b.contributors.Commit(),
		publications:                 publications,
		publicationWinnersByLocation: publicationLocations,
		publicationWinnersByRank:     publicationRanks,
		publicationCounts:            publicationCounts,
		events:                       b.events.Commit(),
		status:                       b.status.Commit(),
		http:                         b.http.Commit(),
		outputs:                      b.outputs.Commit(),
		rankedText:                   b.rankedText.Commit(),
		memo:                         b.memo,
		memoAuthority:                b.memo.authority,
		memoGeneration:               b.memo.generation,
	}, nil
}

func (b *incrementalEmptyGroupIndexBuilder) finishPublications() error {
	b.preparePublicationProjectionCapacity()
	for _, key := range sortedIncrementalBatchKeys(b.publicationBatches) {
		if err := b.finishPublicationBatch(b.publicationBatches[key]); err != nil {
			return err
		}
	}
	for _, key := range sortedIncrementalBatchKeys(b.locationBatches) {
		entry := b.locationBatches[key]
		projection, err := newIncrementalEmptyPublicationTree(
			entry.entries, "incremental publication winner projection repeats a location",
		)
		if err != nil {
			return err
		}
		b.publicationWinnersByLocation = append(
			b.publicationWinnersByLocation,
			persistenttree.NewEntry(entry.key, projection),
		)
	}
	for _, key := range sortedIncrementalBatchKeys(b.rankBatches) {
		entry := b.rankBatches[key]
		projection, err := newIncrementalEmptyPublicationTree(
			entry.entries, "incremental publication winner projection repeats a location",
		)
		if err != nil {
			return err
		}
		b.publicationWinnersByRank = append(
			b.publicationWinnersByRank,
			persistenttree.NewEntry(entry.key, projection),
		)
	}
	for _, cell := range sortedIncrementalBatchKeys(b.publicationCells) {
		count := b.publicationCells[cell]
		if count <= 0 {
			return errors.New("incremental publication count is not positive")
		}
		b.publicationCounts = append(
			b.publicationCounts,
			persistenttree.NewEntry(incrementalOrderedTuple(cell), count),
		)
	}
	return nil
}

func (b *incrementalEmptyGroupIndexBuilder) finishPublicationBatch(
	entry *incrementalEmptyPublicationBatch,
) error {
	owners, err := newIncrementalEmptyPublicationTree(
		entry.entries, "incremental publication index repeats an instance",
	)
	if err != nil {
		return err
	}
	_, winner, found := owners.Root().Minimum()
	if !found {
		return errors.New("incremental publication identity has no owners")
	}
	b.publications = append(b.publications, persistenttree.NewEntry(entry.key, owners))
	if b.publicationCells == nil {
		b.publicationCells = map[string]int{}
	}
	b.publicationCells[winner.cell]++
	if err := b.addPublicationProjection(&winner, false); err != nil {
		return err
	}
	if err := b.addPublicationProjection(&winner, true); err != nil {
		return err
	}
	return b.addRankedTextWinner(&winner)
}

func (b *incrementalEmptyGroupIndexBuilder) preparePublicationProjectionCapacity() {
	counts := make(map[string]int)
	for _, batch := range b.publicationBatches {
		counts[batch.cell]++
	}
	if len(counts) == 0 {
		return
	}
	b.publications = make(
		[]persistenttree.Entry[*persistenttree.Tree[incrementalIndexedPublication]],
		0,
		len(b.publicationBatches),
	)
	b.publicationWinnersByLocation = make(
		[]persistenttree.Entry[*persistenttree.Tree[incrementalIndexedPublication]],
		0,
		len(counts),
	)
	b.publicationWinnersByRank = make(
		[]persistenttree.Entry[*persistenttree.Tree[incrementalIndexedPublication]],
		0,
		len(counts),
	)
	b.publicationCounts = make([]persistenttree.Entry[int], 0, len(counts))
	b.publicationCells = make(map[string]int, len(counts))
	b.locationBatches = make(map[string]*incrementalEmptyPublicationProjectionBatch, len(counts))
	b.rankBatches = make(map[string]*incrementalEmptyPublicationProjectionBatch, len(counts))
	for cell, count := range counts {
		key := incrementalOrderedTuple(cell)
		b.locationBatches[cell] = &incrementalEmptyPublicationProjectionBatch{
			key: key, entries: make([]persistenttree.Entry[incrementalIndexedPublication], 0, count),
		}
		b.rankBatches[cell] = &incrementalEmptyPublicationProjectionBatch{
			key: key, entries: make([]persistenttree.Entry[incrementalIndexedPublication], 0, count),
		}
	}
}

func (b *incrementalEmptyGroupIndexBuilder) addPublicationProjection(
	winner *incrementalIndexedPublication,
	ranked bool,
) error {
	batches := b.locationBatches
	if ranked {
		batches = b.rankBatches
	}
	if batches == nil {
		batches = map[string]*incrementalEmptyPublicationProjectionBatch{}
		if ranked {
			b.rankBatches = batches
		} else {
			b.locationBatches = batches
		}
	}
	entry := batches[winner.cell]
	if entry == nil {
		entry = &incrementalEmptyPublicationProjectionBatch{
			key: incrementalOrderedTuple(winner.cell),
		}
		batches[winner.cell] = entry
	}
	entry.entries = append(
		entry.entries,
		persistenttree.NewEntry(incrementalPublicationProjectionKey(winner, ranked), *winner),
	)
	return nil
}

func newIncrementalEmptyPublicationTree(
	entries []persistenttree.Entry[incrementalIndexedPublication],
	duplicateMessage string,
) (*persistenttree.Tree[incrementalIndexedPublication], error) {
	slices.SortFunc(entries, func(left, right persistenttree.Entry[incrementalIndexedPublication]) int {
		return strings.Compare(left.Key, right.Key)
	})
	for index := 1; index < len(entries); index++ {
		if entries[index-1].Key == entries[index].Key {
			return nil, errors.New(duplicateMessage)
		}
	}
	return persistenttree.NewFromSorted(entries)
}

func (b *incrementalEmptyGroupIndexBuilder) addRankedTextWinner(
	winner *incrementalIndexedPublication,
) error {
	if b.rankedUpdates == nil {
		b.rankedUpdates = map[string]*incrementalRankedTextUpdate{}
	}
	update := b.rankedUpdates[winner.cell]
	if update == nil {
		update = &incrementalRankedTextUpdate{
			cell:    incrementalRankedTextCell{fragment: rendercontent.EmptyTextFragment()},
			changes: map[string]rendercontent.TextFragmentChange{},
		}
		b.rankedUpdates[winner.cell] = update
	}
	return addRankedTextWinner(update, winner.cell, winner)
}

func (b *incrementalEmptyGroupIndexBuilder) finishRankedText(
	projections *persistenttree.Tree[*persistenttree.Tree[incrementalIndexedPublication]],
) error {
	for _, cell := range sortedIncrementalBatchKeys(b.rankedUpdates) {
		update := b.rankedUpdates[cell]
		if update.cell.winnerCount <= 0 || update.cell.unrankedCount < 0 ||
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
		parts, err := fragment.Parts()
		if err != nil {
			return err
		}
		if parts+update.cell.unrankedCount+update.cell.nonStringCount != update.cell.winnerCount {
			return errors.New("incremental ranked text index does not cover its winners")
		}
		key := incrementalOrderedTuple(cell)
		projection, exists := projections.Root().Get(key)
		if !exists || projection == nil || projection.Len() != update.cell.winnerCount {
			return errors.New("incremental ranked text projection has an invalid winner count")
		}
		update.cell.fragment = fragment
		update.cell.projection = projection.Root()
		b.rankedText.Insert(key, update.cell)
	}
	return nil
}

func (b *incrementalEmptyGroupIndexBuilder) finishOutputs(
	batch *incrementalValidatedGroupBatch,
) error {
	changes := map[string][]rendercontent.Change{}
	for batchIndex := range batch.ids {
		chunk, err := incrementalInstanceChunk(
			&batch.instances[batchIndex], &batch.results[batchIndex], b.contributors,
		)
		if err != nil {
			return err
		}
		if chunk == "" {
			continue
		}
		id := batch.ids[batchIndex]
		changes[id.component] = append(changes[id.component], rendercontent.Change{
			Key: string(incrementalComponentInstanceKey(id)), Text: chunk,
		})
	}
	for _, component := range sortedIncrementalBatchKeys(changes) {
		output, err := rendercontent.Empty().Apply(changes[component])
		if err != nil {
			return err
		}
		parts, err := output.Parts()
		if err != nil {
			return err
		}
		if parts == 0 {
			continue
		}
		b.outputs.Insert([]byte(component), incrementalComponentChunks{output: output})
	}
	return nil
}
