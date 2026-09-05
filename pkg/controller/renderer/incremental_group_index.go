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
	"bytes"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"

	iradix "github.com/hashicorp/go-immutable-radix/v2"

	"gitlab.com/haproxy-haptic/haptic/pkg/incremental"
	"gitlab.com/haproxy-haptic/haptic/pkg/persistenttree"
	"gitlab.com/haproxy-haptic/haptic/pkg/rendercontent"
	"gitlab.com/haproxy-haptic/haptic/pkg/templating"
)

var incrementalEmptyIndexedHTTPEffects = iradix.New[incrementalHTTPEffect]()

type incrementalGroupInstanceID struct {
	component string
	source    string
	namespace string
	name      string
}

type incrementalIndexedGroupInstance struct {
	id            incrementalGroupInstanceID
	encodedResult string
	httpEffects   *iradix.Tree[incrementalHTTPEffect]
}

type incrementalIndexedContribution struct {
	instance incrementalGroupInstanceID
	location string
	value    incrementalContribution
}

type incrementalIndexedPublication struct {
	instance incrementalGroupInstanceID
	location string
	cell     string
	key      string
	rank     string
	value    string
}

type incrementalPublishedWinner struct {
	instance incrementalGroupInstanceID
	location []byte
	value    incrementalPublishedValue
}

type incrementalIndexedEvent struct {
	location string
	value    templating.RenderedEvent
}

type incrementalIndexedHTTP struct {
	location string
	value    incrementalHTTPEffect
}

type incrementalIndexedStatusPatchCall struct {
	location string
	prepared *incrementalPreparedStatusPatchCall
}

type incrementalComponentChunks struct {
	output rendercontent.Output
}

type incrementalRankedTextCell struct {
	fragment       rendercontent.TextFragment
	projection     *persistenttree.Node[incrementalIndexedPublication]
	winnerCount    int
	unrankedCount  int
	nonStringCount int
}

type incrementalGroupIndex struct {
	instances                    *persistenttree.Tree[incrementalIndexedGroupInstance]
	contributors                 *iradix.Tree[*iradix.Tree[incrementalIndexedContribution]]
	publications                 *persistenttree.Tree[*persistenttree.Tree[incrementalIndexedPublication]]
	publicationWinnersByLocation *persistenttree.Tree[*persistenttree.Tree[incrementalIndexedPublication]]
	publicationWinnersByRank     *persistenttree.Tree[*persistenttree.Tree[incrementalIndexedPublication]]
	publicationCounts            *persistenttree.Tree[int]
	events                       *iradix.Tree[*iradix.Tree[incrementalIndexedEvent]]
	status                       *iradix.Tree[incrementalIndexedStatusPatchCall]
	http                         *iradix.Tree[*iradix.Tree[incrementalIndexedHTTP]]
	outputs                      *iradix.Tree[incrementalComponentChunks]
	rankedText                   *iradix.Tree[incrementalRankedTextCell]
	memo                         *incrementalGroupMemo
	memoAuthority                *incrementalGroupMemoAuthority
	memoGeneration               *incrementalGroupMemoGeneration
	auth                         incrementalGroupAuthentication
}

type incrementalGroupAuthentication struct {
	instances                    *persistenttree.Tree[incrementalIndexedGroupInstance]
	contributors                 *iradix.Tree[*iradix.Tree[incrementalIndexedContribution]]
	publications                 *persistenttree.Tree[*persistenttree.Tree[incrementalIndexedPublication]]
	publicationWinnersByLocation *persistenttree.Tree[*persistenttree.Tree[incrementalIndexedPublication]]
	publicationWinnersByRank     *persistenttree.Tree[*persistenttree.Tree[incrementalIndexedPublication]]
	publicationCounts            *persistenttree.Tree[int]
	events                       *iradix.Tree[*iradix.Tree[incrementalIndexedEvent]]
	status                       *iradix.Tree[incrementalIndexedStatusPatchCall]
	http                         *iradix.Tree[*iradix.Tree[incrementalIndexedHTTP]]
	outputs                      *iradix.Tree[incrementalComponentChunks]
	rankedText                   *iradix.Tree[incrementalRankedTextCell]
	memo                         *incrementalGroupMemo
	memoAuthority                *incrementalGroupMemoAuthority
	memoGeneration               *incrementalGroupMemoGeneration
}

type incrementalGroupIndexUpdate struct {
	instances                    *persistenttree.Txn[incrementalIndexedGroupInstance]
	contributors                 *iradix.Txn[*iradix.Tree[incrementalIndexedContribution]]
	publications                 *persistenttree.Txn[*persistenttree.Tree[incrementalIndexedPublication]]
	publicationWinnersByLocation *persistenttree.Txn[*persistenttree.Tree[incrementalIndexedPublication]]
	publicationWinnersByRank     *persistenttree.Txn[*persistenttree.Tree[incrementalIndexedPublication]]
	publicationCounts            *persistenttree.Txn[int]
	events                       *iradix.Txn[*iradix.Tree[incrementalIndexedEvent]]
	status                       *iradix.Txn[incrementalIndexedStatusPatchCall]
	http                         *iradix.Txn[*iradix.Tree[incrementalIndexedHTTP]]
	outputs                      *iradix.Txn[incrementalComponentChunks]
	rankedText                   *iradix.Txn[incrementalRankedTextCell]
	memo                         *incrementalGroupMemo
	basePublicationLocations     *persistenttree.Tree[*persistenttree.Tree[incrementalIndexedPublication]]
	basePublicationRanks         *persistenttree.Tree[*persistenttree.Tree[incrementalIndexedPublication]]
	publicationTransitions       map[string]incrementalPublicationTransition
	affectedContributions        map[string][]byte
	affectedInstances            map[string]incrementalGroupInstanceID
	preparedResults              map[string]*incrementalComponentResult
	removedStatus                map[string]incrementalIndexedStatusPatchCall
	rankedTextChanged            map[string]struct{}
}

type incrementalOptionalIndexedPublication struct {
	value   incrementalIndexedPublication
	present bool
}

type incrementalPublicationTransition struct {
	cell     string
	original incrementalOptionalIndexedPublication
	final    incrementalOptionalIndexedPublication
}

type incrementalOptionalComponentResult struct {
	value *incrementalComponentResult
}

type incrementalPreparedGroupInstance struct {
	instance    *incrementalInstanceResult
	component   *incrementalComponent
	queryKey    incremental.QueryKey
	fresh       *authenticatedFreshComponentResult
	encoded     incremental.ExactValueRoot
	httpEffects []incrementalHTTPEffect
}

func newIncrementalGroupIndex() *incrementalGroupIndex {
	memo := newIncrementalGroupMemo()
	index := &incrementalGroupIndex{
		instances:                    persistenttree.New[incrementalIndexedGroupInstance](),
		contributors:                 iradix.New[*iradix.Tree[incrementalIndexedContribution]](),
		publications:                 persistenttree.New[*persistenttree.Tree[incrementalIndexedPublication]](),
		publicationWinnersByLocation: persistenttree.New[*persistenttree.Tree[incrementalIndexedPublication]](),
		publicationWinnersByRank:     persistenttree.New[*persistenttree.Tree[incrementalIndexedPublication]](),
		publicationCounts:            persistenttree.New[int](),
		events:                       iradix.New[*iradix.Tree[incrementalIndexedEvent]](),
		status:                       iradix.New[incrementalIndexedStatusPatchCall](),
		http:                         iradix.New[*iradix.Tree[incrementalIndexedHTTP]](),
		outputs:                      iradix.New[incrementalComponentChunks](),
		rankedText:                   iradix.New[incrementalRankedTextCell](),
		memo:                         memo,
		memoAuthority:                memo.authority,
		memoGeneration:               memo.generation,
	}
	index.authenticate()
	return index
}

func (i *incrementalGroupIndex) replace(
	instance *incrementalInstanceResult,
	httpEffects []incrementalHTTPEffect,
) (*incrementalGroupIndex, error) {
	if instance == nil {
		return nil, errors.New("incremental group instance is nil")
	}
	if err := validateIncrementalInstanceResult(&instance.result); err != nil {
		return nil, fmt.Errorf("incremental component %q source %q %s/%s: %w",
			instance.component, instance.source, instance.namespace, instance.name, err)
	}
	encoded, err := json.Marshal(instance.result)
	if err != nil {
		return nil, fmt.Errorf("encoding incremental component %q result: %w", instance.component, err)
	}
	return i.replacePrepared(instance, string(encoded), &instance.result, httpEffects)
}

func (i *incrementalGroupIndex) replacePrepared(
	instance *incrementalInstanceResult,
	encoded string,
	result *incrementalComponentResult,
	httpEffects []incrementalHTTPEffect,
) (*incrementalGroupIndex, error) {
	if instance == nil || result == nil {
		return nil, errors.New("incremental group instance has no prepared result")
	}
	if err := validateIncrementalInstanceResult(result); err != nil {
		return nil, fmt.Errorf("incremental component %q source %q %s/%s: %w",
			instance.component, instance.source, instance.namespace, instance.name, err)
	}
	canonical, err := json.Marshal(result)
	if err != nil {
		return nil, fmt.Errorf("encoding incremental component %q result: %w", instance.component, err)
	}
	if !stringBytesEqual(encoded, canonical) {
		return nil, errors.New("prepared incremental component result does not match its canonical encoding")
	}
	indexedHTTP, err := newIncrementalIndexedHTTPEffects(httpEffects)
	if err != nil {
		return nil, err
	}
	id := incrementalGroupInstanceID{
		component: instance.component,
		source:    instance.source,
		namespace: instance.namespace,
		name:      instance.name,
	}
	if err := validateIncrementalPublicationResultOwner(result, id); err != nil {
		return nil, err
	}
	return i.apply(id, &incrementalIndexedGroupInstance{
		id: id, encodedResult: encoded, httpEffects: indexedHTTP,
	}, result)
}

func (i *incrementalGroupIndex) remove(
	component, source, namespace, name string,
) (*incrementalGroupIndex, error) {
	return i.apply(incrementalGroupInstanceID{
		component: component,
		source:    source,
		namespace: namespace,
		name:      name,
	}, nil, nil)
}

func (i *incrementalGroupIndex) addPreparedBatch(
	instances []incrementalPreparedGroupInstance,
) (*incrementalGroupIndex, []incrementalComponentResult, error) {
	if err := i.validateAuthentication(); err != nil {
		return nil, nil, err
	}
	if len(instances) == 0 {
		return i, nil, nil
	}
	batch, err := prepareIncrementalGroupBatch(i, instances)
	if err != nil {
		return nil, nil, err
	}
	empty, err := i.authenticatedStructurallyEmpty()
	if err != nil {
		return nil, nil, err
	}
	var updated *incrementalGroupIndex
	if empty {
		updated, err = i.addPreparedEmptyBatch(batch)
	} else {
		updated, err = i.addPreparedPersistentBatch(batch)
	}
	if err != nil {
		return nil, nil, err
	}
	for index := range instances {
		if err := updated.validatePublicationPaths(batch.ids[index], &batch.results[index]); err != nil {
			return nil, nil, err
		}
	}
	updated.authenticate()
	return updated, batch.results, nil
}

func (i *incrementalGroupIndex) addPreparedPersistentBatch(
	batch *incrementalValidatedGroupBatch,
) (*incrementalGroupIndex, error) {
	update, err := newIncrementalGroupIndexUpdate(i, batch.ids[0])
	if err != nil {
		return nil, err
	}
	if err := update.addPreparedBatch(batch.ids, batch.instances, batch.results); err != nil {
		return nil, err
	}
	if err := update.refreshRankedText(); err != nil {
		return nil, err
	}
	return update.commit()
}

func (i *incrementalGroupIndex) apply(
	id incrementalGroupInstanceID,
	next *incrementalIndexedGroupInstance,
	prepared *incrementalComponentResult,
) (*incrementalGroupIndex, error) {
	if err := i.validateAuthentication(); err != nil {
		return nil, err
	}
	identityKey := incrementalGroupInstanceKey(id)
	previous, existed := i.instances.Root().Get(identityKey)
	if !existed && next == nil {
		return i, nil
	}
	update, err := newIncrementalGroupIndexUpdate(i, id)
	if err != nil {
		return nil, err
	}
	if prepared != nil {
		update.preparedResults[string(identityKey)] = prepared
	}
	previousResult, err := update.removePrevious(i, id, &previous, existed)
	if err != nil {
		return nil, err
	}
	nextResult, err := update.addNext(identityKey, next)
	if err != nil {
		return nil, err
	}
	if err := update.refreshChunks(); err != nil {
		return nil, err
	}
	if err := update.refreshRankedText(); err != nil {
		return nil, err
	}
	updated, err := update.commit()
	if err != nil {
		return nil, err
	}
	if err := updated.validateChangedPublicationPaths(id, previousResult.value, nextResult.value); err != nil {
		return nil, err
	}
	updated.authenticate()
	return updated, nil
}

func newIncrementalGroupIndexUpdate(
	index *incrementalGroupIndex,
	id incrementalGroupInstanceID,
) (*incrementalGroupIndexUpdate, error) {
	memo, err := index.memo.fork()
	if err != nil {
		return nil, err
	}
	return &incrementalGroupIndexUpdate{
		instances:                    index.instances.Txn(),
		contributors:                 index.contributors.Txn(),
		publications:                 index.publications.Txn(),
		publicationWinnersByLocation: index.publicationWinnersByLocation.Txn(),
		publicationWinnersByRank:     index.publicationWinnersByRank.Txn(),
		publicationCounts:            index.publicationCounts.Txn(),
		events:                       index.events.Txn(),
		status:                       index.status.Txn(),
		http:                         index.http.Txn(),
		outputs:                      index.outputs.Txn(),
		rankedText:                   index.rankedText.Txn(),
		memo:                         memo,
		basePublicationLocations:     index.publicationWinnersByLocation,
		basePublicationRanks:         index.publicationWinnersByRank,
		publicationTransitions:       make(map[string]incrementalPublicationTransition),
		affectedContributions:        make(map[string][]byte),
		preparedResults:              make(map[string]*incrementalComponentResult),
		removedStatus:                make(map[string]incrementalIndexedStatusPatchCall),
		rankedTextChanged:            make(map[string]struct{}),
		affectedInstances: map[string]incrementalGroupInstanceID{
			string(incrementalGroupInstanceKey(id)): id,
		},
	}, nil
}

func (u *incrementalGroupIndexUpdate) removePrevious(
	index *incrementalGroupIndex,
	id incrementalGroupInstanceID,
	previous *incrementalIndexedGroupInstance,
	exists bool,
) (incrementalOptionalComponentResult, error) {
	if !exists {
		return incrementalOptionalComponentResult{}, nil
	}
	result, err := decodeIndexedGroupInstanceResult(previous)
	if err != nil {
		return incrementalOptionalComponentResult{}, err
	}
	if err := index.validatePublicationPaths(id, &result); err != nil {
		return incrementalOptionalComponentResult{}, err
	}
	if err := removeIndexedGroupInstance(
		previous, &result, u.contributors, u.publications,
		u.publicationWinnersByLocation, u.publicationWinnersByRank, u.publicationCounts,
		u.events, u.status, u.http, u.memo.authority, u.removedStatus,
		u.publicationTransitions, u.affectedContributions, u.affectedInstances,
	); err != nil {
		return incrementalOptionalComponentResult{}, err
	}
	return incrementalOptionalComponentResult{value: &result}, nil
}

func (u *incrementalGroupIndexUpdate) addNext(
	identityKey []byte,
	next *incrementalIndexedGroupInstance,
) (incrementalOptionalComponentResult, error) {
	if next == nil {
		u.instances.Delete(identityKey)
		return incrementalOptionalComponentResult{}, nil
	}
	result := u.preparedResults[string(identityKey)]
	if result == nil {
		decoded, err := decodeIndexedGroupInstanceResult(next)
		if err != nil {
			return incrementalOptionalComponentResult{}, err
		}
		result = &decoded
	}
	if err := addIndexedGroupInstance(
		next, result, u.contributors, u.publications,
		u.publicationWinnersByLocation, u.publicationWinnersByRank, u.publicationCounts,
		u.events, u.status, u.http, u.memo.authority, u.removedStatus,
		u.publicationTransitions, u.affectedContributions, u.affectedInstances,
	); err != nil {
		return incrementalOptionalComponentResult{}, err
	}
	u.instances.Insert(identityKey, *next)
	return incrementalOptionalComponentResult{value: result}, nil
}

func (u *incrementalGroupIndexUpdate) refreshChunks() error {
	rememberCurrentContributionWinners(
		u.contributors, u.affectedContributions, u.affectedInstances,
	)
	keys := make([]string, 0, len(u.affectedInstances))
	for key := range u.affectedInstances {
		keys = append(keys, key)
	}
	slices.Sort(keys)
	for _, key := range keys {
		if err := refreshIncrementalComponentChunk(
			u.instances, u.contributors, u.outputs, u.affectedInstances[key], u.preparedResults,
		); err != nil {
			return err
		}
	}
	return nil
}

func (u *incrementalGroupIndexUpdate) commit() (*incrementalGroupIndex, error) {
	publicationLocations := u.publicationWinnersByLocation.Commit()
	publicationRanks := u.publicationWinnersByRank.Commit()
	changedCells := make(map[string]struct{})
	touchedCells := make(map[string]struct{})
	for index := range u.publicationTransitions {
		transition := u.publicationTransitions[index]
		touchedCells[transition.cell] = struct{}{}
		if transition.original != transition.final {
			changedCells[transition.cell] = struct{}{}
		}
	}
	for cell := range touchedCells {
		if _, changed := changedCells[cell]; changed {
			if err := u.memo.invalidateCell(cell); err != nil {
				return nil, err
			}
			continue
		}
		publicationLocations = restoreIncrementalPublicationProjectionCell(
			publicationLocations, u.basePublicationLocations, cell,
		)
		publicationRanks = restoreIncrementalPublicationProjectionCell(
			publicationRanks, u.basePublicationRanks, cell,
		)
	}
	if err := u.bindRankedTextProjections(publicationRanks); err != nil {
		return nil, err
	}
	return &incrementalGroupIndex{
		instances:                    u.instances.Commit(),
		contributors:                 u.contributors.Commit(),
		publications:                 u.publications.Commit(),
		publicationWinnersByLocation: publicationLocations,
		publicationWinnersByRank:     publicationRanks,
		publicationCounts:            u.publicationCounts.Commit(),
		events:                       u.events.Commit(),
		status:                       u.status.Commit(),
		http:                         u.http.Commit(),
		outputs:                      u.outputs.Commit(),
		rankedText:                   u.rankedText.Commit(),
		memo:                         u.memo,
		memoAuthority:                u.memo.authority,
		memoGeneration:               u.memo.generation,
	}, nil
}

func restoreIncrementalPublicationProjectionCell(
	current *persistenttree.Tree[*persistenttree.Tree[incrementalIndexedPublication]],
	base *persistenttree.Tree[*persistenttree.Tree[incrementalIndexedPublication]],
	cell string,
) *persistenttree.Tree[*persistenttree.Tree[incrementalIndexedPublication]] {
	key := incrementalOrderedTuple(cell)
	txn := current.Txn()
	projection, exists := base.Root().Get(key)
	if exists {
		txn.Insert(key, projection)
	} else {
		txn.Delete(key)
	}
	return txn.Commit()
}

func removeIndexedGroupInstance(
	instance *incrementalIndexedGroupInstance,
	result *incrementalComponentResult,
	contributors *iradix.Txn[*iradix.Tree[incrementalIndexedContribution]],
	publications *persistenttree.Txn[*persistenttree.Tree[incrementalIndexedPublication]],
	publicationWinnersByLocation *persistenttree.Txn[*persistenttree.Tree[incrementalIndexedPublication]],
	publicationWinnersByRank *persistenttree.Txn[*persistenttree.Tree[incrementalIndexedPublication]],
	publicationCounts *persistenttree.Txn[int],
	events *iradix.Txn[*iradix.Tree[incrementalIndexedEvent]],
	status *iradix.Txn[incrementalIndexedStatusPatchCall],
	httpInputs *iradix.Txn[*iradix.Tree[incrementalIndexedHTTP]],
	statusAuthority *incrementalGroupMemoAuthority,
	removedStatus map[string]incrementalIndexedStatusPatchCall,
	publicationTransitions map[string]incrementalPublicationTransition,
	affectedContributions map[string][]byte,
	affectedInstances map[string]incrementalGroupInstanceID,
) error {
	rememberContributionWinners(result.Unique, contributors, affectedContributions, affectedInstances)
	if err := removeContributions(instance.id, result, contributors, affectedContributions); err != nil {
		return err
	}
	if err := removePublications(
		instance.id, result, publications,
		publicationWinnersByLocation, publicationWinnersByRank, publicationCounts, publicationTransitions,
	); err != nil {
		return err
	}
	if err := removeEvents(instance.id, result, events); err != nil {
		return err
	}
	if err := removeStatusPatchCalls(instance.id, result, status, statusAuthority, removedStatus); err != nil {
		return err
	}
	return removeHTTP(instance, httpInputs)
}

func addIndexedGroupInstance(
	instance *incrementalIndexedGroupInstance,
	result *incrementalComponentResult,
	contributors *iradix.Txn[*iradix.Tree[incrementalIndexedContribution]],
	publications *persistenttree.Txn[*persistenttree.Tree[incrementalIndexedPublication]],
	publicationWinnersByLocation *persistenttree.Txn[*persistenttree.Tree[incrementalIndexedPublication]],
	publicationWinnersByRank *persistenttree.Txn[*persistenttree.Tree[incrementalIndexedPublication]],
	publicationCounts *persistenttree.Txn[int],
	events *iradix.Txn[*iradix.Tree[incrementalIndexedEvent]],
	status *iradix.Txn[incrementalIndexedStatusPatchCall],
	httpInputs *iradix.Txn[*iradix.Tree[incrementalIndexedHTTP]],
	statusAuthority *incrementalGroupMemoAuthority,
	removedStatus map[string]incrementalIndexedStatusPatchCall,
	publicationTransitions map[string]incrementalPublicationTransition,
	affectedContributions map[string][]byte,
	affectedInstances map[string]incrementalGroupInstanceID,
) error {
	rememberContributionWinners(result.Unique, contributors, affectedContributions, affectedInstances)
	if err := addContributions(instance.id, result, contributors, affectedContributions); err != nil {
		return err
	}
	if err := addPublications(
		instance.id, result, publications,
		publicationWinnersByLocation, publicationWinnersByRank, publicationCounts, publicationTransitions,
	); err != nil {
		return err
	}
	if err := addEvents(instance.id, result, events); err != nil {
		return err
	}
	if err := addStatusPatchCalls(instance.id, result, status, statusAuthority, removedStatus); err != nil {
		return err
	}
	return addHTTP(instance, httpInputs)
}

func cloneIndexedComponentResult(source *incrementalComponentResult) incrementalComponentResult {
	result := cloneIncrementalComponentResult(source)
	result.Derivations = nil
	return result
}

func decodeIndexedGroupInstanceResult(
	instance *incrementalIndexedGroupInstance,
) (incrementalComponentResult, error) {
	if instance == nil {
		return incrementalComponentResult{}, errors.New("incremental group instance has no result")
	}
	if instance.encodedResult == "" {
		return incrementalComponentResult{}, errors.New("incremental group instance has no result")
	}
	result, err := decodeIncrementalComponentResultString(instance.encodedResult)
	if err != nil {
		return incrementalComponentResult{}, fmt.Errorf("decoding incremental publication result: %w", err)
	}
	return result, nil
}

func newIncrementalIndexedHTTPEffects(
	effects []incrementalHTTPEffect,
) (*iradix.Tree[incrementalHTTPEffect], error) {
	if len(effects) == 0 {
		return incrementalEmptyIndexedHTTPEffects, nil
	}
	txn := iradix.New[incrementalHTTPEffect]().Txn()
	for index := range effects {
		effect := effects[index]
		if effect.inputID == 0 {
			return nil, errors.New("incremental HTTP effect has no input identity")
		}
		if _, duplicate := txn.Insert(incrementalHTTPIdentityKey(effect.inputID), effect); duplicate {
			return nil, fmt.Errorf("incremental HTTP effects repeat input %d", effect.inputID)
		}
	}
	return txn.Commit(), nil
}

func indexedHTTPEffects(tree *iradix.Tree[incrementalHTTPEffect]) []incrementalHTTPEffect {
	if tree == nil {
		return nil
	}
	result := make([]incrementalHTTPEffect, 0, tree.Len())
	tree.Root().Walk(func(_ []byte, effect incrementalHTTPEffect) bool {
		result = append(result, effect)
		return false
	})
	return result
}

func rememberContributionWinners(
	values []incrementalContribution,
	contributors *iradix.Txn[*iradix.Tree[incrementalIndexedContribution]],
	affected map[string][]byte,
	instances map[string]incrementalGroupInstanceID,
) {
	for index := range values {
		key := incrementalContributionIdentityKey(values[index])
		affected[string(key)] = key
		tree, exists := contributors.Get(key)
		if !exists {
			continue
		}
		_, winner, exists := tree.Root().Minimum()
		if exists {
			instances[string(incrementalGroupInstanceKey(winner.instance))] = winner.instance
		}
	}
}

func rememberCurrentContributionWinners(
	contributors *iradix.Txn[*iradix.Tree[incrementalIndexedContribution]],
	affected map[string][]byte,
	instances map[string]incrementalGroupInstanceID,
) {
	for _, key := range affected {
		tree, exists := contributors.Get(key)
		if !exists {
			continue
		}
		_, winner, exists := tree.Root().Minimum()
		if exists {
			instances[string(incrementalGroupInstanceKey(winner.instance))] = winner.instance
		}
	}
}

func removeContributions(
	id incrementalGroupInstanceID,
	result *incrementalComponentResult,
	contributors *iradix.Txn[*iradix.Tree[incrementalIndexedContribution]],
	affected map[string][]byte,
) error {
	locationIndex := uint64(0)
	for index := range result.Unique {
		value := result.Unique[index]
		key := incrementalContributionIdentityKey(value)
		affected[string(key)] = key
		tree, exists := contributors.Get(key)
		if !exists {
			return errors.New("incremental contribution index is missing an identity")
		}
		txn := tree.Txn()
		if _, removed := txn.Delete(incrementalGroupLocationKey(id, locationIndex)); !removed {
			return errors.New("incremental contribution index is missing an instance")
		}
		locationIndex++
		updated := txn.Commit()
		if updated.Len() == 0 {
			contributors.Delete(key)
		} else {
			contributors.Insert(key, updated)
		}
	}
	return nil
}

func addContributions(
	id incrementalGroupInstanceID,
	result *incrementalComponentResult,
	contributors *iradix.Txn[*iradix.Tree[incrementalIndexedContribution]],
	affected map[string][]byte,
) error {
	locationIndex := uint64(0)
	for index := range result.Unique {
		value := result.Unique[index]
		key := incrementalContributionIdentityKey(value)
		affected[string(key)] = key
		tree, exists := contributors.Get(key)
		if !exists {
			tree = iradix.New[incrementalIndexedContribution]()
		}
		location := incrementalGroupLocationKey(id, locationIndex)
		locationIndex++
		txn := tree.Txn()
		if _, duplicate := txn.Insert(location, incrementalIndexedContribution{
			instance: id,
			location: string(location),
			value:    value,
		}); duplicate {
			return errors.New("incremental contribution index repeats an instance")
		}
		contributors.Insert(key, txn.Commit())
	}
	return nil
}

func removePublications(
	id incrementalGroupInstanceID,
	result *incrementalComponentResult,
	publications *persistenttree.Txn[*persistenttree.Tree[incrementalIndexedPublication]],
	publicationWinnersByLocation *persistenttree.Txn[*persistenttree.Tree[incrementalIndexedPublication]],
	publicationWinnersByRank *persistenttree.Txn[*persistenttree.Tree[incrementalIndexedPublication]],
	publicationCounts *persistenttree.Txn[int],
	publicationTransitions map[string]incrementalPublicationTransition,
) error {
	for index := range result.Published {
		value := &result.Published[index]
		key := incrementalPublicationIdentityKey(value.Cell, value.Key)
		tree, exists := publications.Get(key)
		if !exists || tree == nil {
			return errors.New("incremental publication index is missing an identity")
		}
		_, previousWinner, hasPreviousWinner := tree.Root().Minimum()
		if !hasPreviousWinner {
			return errors.New("incremental publication identity has no owners")
		}
		txn := tree.Txn()
		location := incrementalGroupLocationKey(id, uint64(index))
		if _, removed := txn.Delete(incrementalPublicationOwnerKey(value.Rank, location)); !removed {
			return errors.New("incremental publication index is missing an instance")
		}
		updated := txn.Commit()
		_, nextWinnerValue, hasNextWinner := updated.Root().Minimum()
		var nextWinner *incrementalIndexedPublication
		if hasNextWinner {
			nextWinner = &nextWinnerValue
		}
		if updated.Len() == 0 {
			publications.Delete(key)
			if err := adjustIncrementalPublicationCount(publicationCounts, value.Cell, -1); err != nil {
				return err
			}
		} else {
			publications.Insert(key, updated)
		}
		if err := refreshIncrementalPublicationWinnerProjections(
			publicationWinnersByLocation, publicationWinnersByRank,
			publicationTransitions, &previousWinner, nextWinner,
		); err != nil {
			return err
		}
	}
	return nil
}

func addPublications(
	id incrementalGroupInstanceID,
	result *incrementalComponentResult,
	publications *persistenttree.Txn[*persistenttree.Tree[incrementalIndexedPublication]],
	publicationWinnersByLocation *persistenttree.Txn[*persistenttree.Tree[incrementalIndexedPublication]],
	publicationWinnersByRank *persistenttree.Txn[*persistenttree.Tree[incrementalIndexedPublication]],
	publicationCounts *persistenttree.Txn[int],
	publicationTransitions map[string]incrementalPublicationTransition,
) error {
	for index := range result.Published {
		value := result.Published[index]
		key := incrementalPublicationIdentityKey(value.Cell, value.Key)
		tree, previousWinner, err := publicationOwnersForAdd(
			publications, publicationCounts, key, &value,
		)
		if err != nil {
			return err
		}
		location := incrementalGroupLocationKey(id, uint64(index))
		txn := tree.Txn()
		if _, duplicate := txn.Insert(incrementalPublicationOwnerKey(value.Rank, location), incrementalIndexedPublication{
			instance: id,
			location: string(location),
			cell:     value.Cell,
			key:      value.Key,
			rank:     value.Rank,
			value:    string(value.Value),
		}); duplicate {
			return errors.New("incremental publication index repeats an instance")
		}
		updated := txn.Commit()
		_, nextWinner, hasNextWinner := updated.Root().Minimum()
		if !hasNextWinner {
			return errors.New("incremental publication identity has no owners")
		}
		publications.Insert(key, updated)
		if err := refreshIncrementalPublicationWinnerProjections(
			publicationWinnersByLocation, publicationWinnersByRank,
			publicationTransitions, previousWinner, &nextWinner,
		); err != nil {
			return err
		}
	}
	return nil
}

func publicationOwnersForAdd(
	publications *persistenttree.Txn[*persistenttree.Tree[incrementalIndexedPublication]],
	publicationCounts *persistenttree.Txn[int],
	key []byte,
	value *incrementalPublishedValue,
) (*persistenttree.Tree[incrementalIndexedPublication], *incrementalIndexedPublication, error) {
	tree, exists := publications.Get(key)
	if !exists {
		if err := adjustIncrementalPublicationCount(publicationCounts, value.Cell, 1); err != nil {
			return nil, nil, err
		}
		return persistenttree.New[incrementalIndexedPublication](), nil, nil
	}
	if tree == nil {
		return nil, nil, errors.New("incremental publication identity has no owners")
	}
	_, winner, exists := tree.Root().Minimum()
	if !exists {
		return nil, nil, errors.New("incremental publication identity has no owners")
	}
	if (winner.rank == "") != (value.Rank == "") {
		return nil, nil, errors.New("incremental publication identity mixes ranked and unranked owners")
	}
	return tree, &winner, nil
}

func refreshIncrementalPublicationWinnerProjections(
	byLocation *persistenttree.Txn[*persistenttree.Tree[incrementalIndexedPublication]],
	byRank *persistenttree.Txn[*persistenttree.Tree[incrementalIndexedPublication]],
	transitions map[string]incrementalPublicationTransition,
	previous *incrementalIndexedPublication,
	next *incrementalIndexedPublication,
) error {
	if err := recordIncrementalPublicationTransition(transitions, previous, next); err != nil {
		return err
	}
	if previous != nil && next != nil && *previous == *next {
		return nil
	}
	if previous != nil {
		if err := removeIncrementalPublicationWinnerProjection(byLocation, previous, false); err != nil {
			return err
		}
		if err := removeIncrementalPublicationWinnerProjection(byRank, previous, true); err != nil {
			return err
		}
	}
	if next != nil {
		if err := addIncrementalPublicationWinnerProjection(byLocation, next, false); err != nil {
			return err
		}
		if err := addIncrementalPublicationWinnerProjection(byRank, next, true); err != nil {
			return err
		}
	}
	return nil
}

func recordIncrementalPublicationTransition(
	transitions map[string]incrementalPublicationTransition,
	previous *incrementalIndexedPublication,
	next *incrementalIndexedPublication,
) error {
	if transitions == nil || previous == nil && next == nil {
		return errors.New("incremental publication transition is unavailable")
	}
	representative := previous
	if representative == nil {
		representative = next
	}
	if previous != nil && next != nil && (previous.cell != next.cell || previous.key != next.key) {
		return errors.New("incremental publication transition changes identity")
	}
	identity := string(incrementalPublicationIdentityKey(representative.cell, representative.key))
	previousValue := optionalIncrementalIndexedPublication(previous)
	nextValue := optionalIncrementalIndexedPublication(next)
	transition, exists := transitions[identity]
	if !exists {
		transitions[identity] = incrementalPublicationTransition{
			cell: representative.cell, original: previousValue, final: nextValue,
		}
		return nil
	}
	if transition.cell != representative.cell || transition.final != previousValue {
		return errors.New("incremental publication transition is discontinuous")
	}
	transition.final = nextValue
	transitions[identity] = transition
	return nil
}

func optionalIncrementalIndexedPublication(
	value *incrementalIndexedPublication,
) incrementalOptionalIndexedPublication {
	if value == nil {
		return incrementalOptionalIndexedPublication{}
	}
	return incrementalOptionalIndexedPublication{value: *value, present: true}
}

func removeIncrementalPublicationWinnerProjection(
	projection *persistenttree.Txn[*persistenttree.Tree[incrementalIndexedPublication]],
	winner *incrementalIndexedPublication,
	ranked bool,
) error {
	if projection == nil || winner == nil {
		return errors.New("incremental publication winner projection is unavailable")
	}
	cellKey := incrementalOrderedTuple(winner.cell)
	cell, exists := projection.Get(cellKey)
	if !exists || cell == nil {
		return errors.New("incremental publication winner projection is missing a cell")
	}
	key := incrementalPublicationProjectionKey(winner, ranked)
	indexed, exists := cell.Root().Get(key)
	if !exists || indexed != *winner {
		return errors.New("incremental publication winner projection does not match its owner")
	}
	txn := cell.Txn()
	if _, removed := txn.Delete(key); !removed {
		return errors.New("incremental publication winner projection is missing a winner")
	}
	updated := txn.Commit()
	if updated.Len() == 0 {
		projection.Delete(cellKey)
	} else {
		projection.Insert(cellKey, updated)
	}
	return nil
}

func addIncrementalPublicationWinnerProjection(
	projection *persistenttree.Txn[*persistenttree.Tree[incrementalIndexedPublication]],
	winner *incrementalIndexedPublication,
	ranked bool,
) error {
	if projection == nil || winner == nil {
		return errors.New("incremental publication winner projection is unavailable")
	}
	cellKey := incrementalOrderedTuple(winner.cell)
	cell, exists := projection.Get(cellKey)
	if !exists {
		cell = persistenttree.New[incrementalIndexedPublication]()
	} else if cell == nil {
		return errors.New("incremental publication winner projection has an empty cell")
	}
	txn := cell.Txn()
	if _, duplicate := txn.Insert(incrementalPublicationProjectionKey(winner, ranked), *winner); duplicate {
		return errors.New("incremental publication winner projection repeats a location")
	}
	projection.Insert(cellKey, txn.Commit())
	return nil
}

func incrementalPublicationProjectionKey(
	winner *incrementalIndexedPublication,
	ranked bool,
) []byte {
	if !ranked {
		return []byte(winner.location)
	}
	return incrementalOrderedTuple(winner.rank, winner.location)
}

func adjustIncrementalPublicationCount(counts *persistenttree.Txn[int], cell string, delta int) error {
	if counts == nil {
		return errors.New("incremental publication count index is unavailable")
	}
	key := incrementalOrderedTuple(cell)
	current, _ := counts.Get(key)
	next := current + delta
	if next < 0 {
		return errors.New("incremental publication count is negative")
	}
	if next == 0 {
		counts.Delete(key)
	} else {
		counts.Insert(key, next)
	}
	return nil
}

func removeEvents(
	id incrementalGroupInstanceID,
	result *incrementalComponentResult,
	events *iradix.Txn[*iradix.Tree[incrementalIndexedEvent]],
) error {
	locationIndex := uint64(0)
	for index := range result.Events {
		key := incrementalEventIdentityKey(&result.Events[index])
		tree, exists := events.Get(key)
		if !exists {
			return errors.New("incremental event index is missing an identity")
		}
		txn := tree.Txn()
		if _, removed := txn.Delete(incrementalGroupLocationKey(id, locationIndex)); !removed {
			return errors.New("incremental event index is missing an instance")
		}
		locationIndex++
		updated := txn.Commit()
		if updated.Len() == 0 {
			events.Delete(key)
		} else {
			events.Insert(key, updated)
		}
	}
	return nil
}

func addEvents(
	id incrementalGroupInstanceID,
	result *incrementalComponentResult,
	events *iradix.Txn[*iradix.Tree[incrementalIndexedEvent]],
) error {
	locationIndex := uint64(0)
	for index := range result.Events {
		value := result.Events[index]
		key := incrementalEventIdentityKey(&value)
		tree, exists := events.Get(key)
		if !exists {
			tree = iradix.New[incrementalIndexedEvent]()
		}
		location := incrementalGroupLocationKey(id, locationIndex)
		locationIndex++
		txn := tree.Txn()
		if _, duplicate := txn.Insert(location, incrementalIndexedEvent{location: string(location), value: value}); duplicate {
			return errors.New("incremental event index repeats an instance")
		}
		events.Insert(key, txn.Commit())
	}
	return nil
}

func removeStatusPatchCalls(
	id incrementalGroupInstanceID,
	result *incrementalComponentResult,
	status *iradix.Txn[incrementalIndexedStatusPatchCall],
	authority *incrementalGroupMemoAuthority,
	removed map[string]incrementalIndexedStatusPatchCall,
) error {
	for index := range result.StatusPatches {
		location := incrementalGroupLocationKey(id, uint64(index))
		indexed, exists := status.Get(location)
		if !exists || indexed.location != string(location) ||
			validateIncrementalPreparedStatusPatchCall(indexed.prepared, authority, indexed.location) != nil ||
			!incrementalPreparedStatusPatchCallMatches(indexed.prepared, &result.StatusPatches[index]) {
			return errors.New("incremental statusPatch index does not match its result")
		}
		if removed != nil {
			removed[string(location)] = indexed
		}
		status.Delete(location)
	}
	return nil
}

func addStatusPatchCalls(
	id incrementalGroupInstanceID,
	result *incrementalComponentResult,
	status *iradix.Txn[incrementalIndexedStatusPatchCall],
	authority *incrementalGroupMemoAuthority,
	removed map[string]incrementalIndexedStatusPatchCall,
) error {
	return addStatusPatchCallsForInstanceKey(
		incrementalGroupInstanceKey(id), result, status, authority, removed,
	)
}

func addStatusPatchCallsForInstanceKey(
	instanceKey []byte,
	result *incrementalComponentResult,
	status *iradix.Txn[incrementalIndexedStatusPatchCall],
	authority *incrementalGroupMemoAuthority,
	removed map[string]incrementalIndexedStatusPatchCall,
) error {
	for index := range result.StatusPatches {
		location := incrementalGroupLocationKeyForInstanceKey(instanceKey, uint64(index))
		call := cloneIncrementalStatusPatchCalls(result.StatusPatches[index : index+1])[0]
		if previous, exists := removed[string(location)]; exists &&
			validateIncrementalPreparedStatusPatchCall(previous.prepared, authority, previous.location) == nil &&
			incrementalPreparedStatusPatchCallMatches(previous.prepared, &call) {
			if _, duplicate := status.Insert(location, previous); duplicate {
				return errors.New("incremental statusPatch index repeats an operation")
			}
			delete(removed, string(location))
			continue
		}
		prepared, err := newIncrementalPreparedStatusPatchCall(authority, string(location), &call)
		if err != nil {
			return err
		}
		if _, duplicate := status.Insert(location, incrementalIndexedStatusPatchCall{
			location: string(location),
			prepared: prepared,
		}); duplicate {
			return errors.New("incremental statusPatch index repeats an operation")
		}
	}
	return nil
}

func removeHTTP(
	instance *incrementalIndexedGroupInstance,
	httpInputs *iradix.Txn[*iradix.Tree[incrementalIndexedHTTP]],
) error {
	locationIndex := uint64(0)
	var removeErr error
	instance.httpEffects.Root().Walk(func(_ []byte, effect incrementalHTTPEffect) bool {
		key := incrementalHTTPIdentityKey(effect.inputID)
		tree, exists := httpInputs.Get(key)
		if !exists {
			removeErr = errors.New("incremental HTTP index is missing an input")
			return true
		}
		txn := tree.Txn()
		if _, removed := txn.Delete(incrementalGroupLocationKey(instance.id, locationIndex)); !removed {
			removeErr = errors.New("incremental HTTP index is missing an instance")
			return true
		}
		locationIndex++
		updated := txn.Commit()
		if updated.Len() == 0 {
			httpInputs.Delete(key)
		} else {
			httpInputs.Insert(key, updated)
		}
		return false
	})
	return removeErr
}

func addHTTP(
	instance *incrementalIndexedGroupInstance,
	httpInputs *iradix.Txn[*iradix.Tree[incrementalIndexedHTTP]],
) error {
	locationIndex := uint64(0)
	var addErr error
	instance.httpEffects.Root().Walk(func(_ []byte, value incrementalHTTPEffect) bool {
		key := incrementalHTTPIdentityKey(value.inputID)
		tree, exists := httpInputs.Get(key)
		if !exists {
			tree = iradix.New[incrementalIndexedHTTP]()
		}
		if _, representative, found := tree.Root().Maximum(); found &&
			!sameHTTPReusableSnapshot(&representative.value.snapshot, &value.snapshot) {
			addErr = fmt.Errorf("incremental HTTP input %d has conflicting snapshots", value.inputID)
			return true
		}
		location := incrementalGroupLocationKey(instance.id, locationIndex)
		locationIndex++
		txn := tree.Txn()
		if _, duplicate := txn.Insert(location, incrementalIndexedHTTP{location: string(location), value: value}); duplicate {
			addErr = errors.New("incremental HTTP index repeats an instance")
			return true
		}
		httpInputs.Insert(key, txn.Commit())
		return false
	})
	return addErr
}

func refreshIncrementalComponentChunk(
	instances *persistenttree.Txn[incrementalIndexedGroupInstance],
	contributors *iradix.Txn[*iradix.Tree[incrementalIndexedContribution]],
	outputs *iradix.Txn[incrementalComponentChunks],
	id incrementalGroupInstanceID,
	prepared map[string]*incrementalComponentResult,
) error {
	identityKey := incrementalGroupInstanceKey(id)
	instance, exists := instances.Get(identityKey)
	chunk := ""
	if exists {
		result := prepared[string(identityKey)]
		if result == nil {
			decoded, err := decodeIndexedGroupInstanceResult(&instance)
			if err != nil {
				return err
			}
			result = &decoded
		}
		var err error
		chunk, err = incrementalInstanceChunk(&instance, result, contributors)
		if err != nil {
			return err
		}
	}
	componentKey := []byte(id.component)
	component, componentExists := outputs.Get(componentKey)
	if !componentExists {
		component = incrementalComponentChunks{output: rendercontent.Empty()}
	} else if err := component.output.ValidateAuthentication(); err != nil {
		return errors.New("incremental component output is unavailable")
	}
	chunkKey := string(incrementalComponentInstanceKey(id))
	previous, chunkExists, err := component.output.Get(chunkKey)
	if err != nil {
		return err
	}
	if chunkExists && previous == chunk || !chunkExists && chunk == "" {
		return nil
	}
	updated, err := component.output.WithText(chunkKey, chunk)
	if err != nil {
		return err
	}
	parts, err := updated.Parts()
	if err != nil {
		return err
	}
	if parts == 0 {
		outputs.Delete(componentKey)
	} else {
		outputs.Insert(componentKey, incrementalComponentChunks{output: updated})
	}
	return nil
}

func incrementalInstanceChunk(
	instance *incrementalIndexedGroupInstance,
	result *incrementalComponentResult,
	contributors *iradix.Txn[*iradix.Tree[incrementalIndexedContribution]],
) (string, error) {
	if len(result.BackendPlan) != 0 || len(result.BackendPlanOutput) != 0 ||
		result.BackendPlanDigest != "" {
		return "", nil
	}
	if result.Text != "" {
		return result.Text, nil
	}
	var output strings.Builder
	locationIndex := uint64(0)
	for index := range result.Unique {
		value := result.Unique[index]
		tree, exists := contributors.Get(incrementalContributionIdentityKey(value))
		if !exists {
			return "", errors.New("incremental contribution index is missing a value")
		}
		winnerKey, _, exists := tree.Root().Minimum()
		if !exists {
			return "", errors.New("incremental contribution index is empty")
		}
		if bytes.Equal(winnerKey, incrementalGroupLocationKey(instance.id, locationIndex)) {
			output.WriteString(value.Value)
		}
		locationIndex++
	}
	return output.String(), nil
}

func (i *incrementalGroupIndex) output(component string) (string, error) {
	output, err := i.outputContent(component)
	if err != nil {
		return "", err
	}
	return output.String()
}

func (i *incrementalGroupIndex) outputContent(component string) (rendercontent.Output, error) {
	if err := i.validateAuthentication(); err != nil {
		return rendercontent.Output{}, err
	}
	value, exists := i.outputs.Root().Get([]byte(component))
	if !exists {
		return rendercontent.Empty(), nil
	}
	if err := value.output.ValidateAuthentication(); err != nil {
		return rendercontent.Output{}, err
	}
	return value.output, nil
}

func (i *incrementalGroupIndex) hasOutput() (bool, error) {
	if err := i.validateAuthentication(); err != nil {
		return false, err
	}
	return i.outputs.Len() != 0, nil
}

func (i *incrementalGroupIndex) renderedEvents() ([]templating.RenderedEvent, error) {
	if err := i.validateAuthentication(); err != nil {
		return nil, err
	}
	result := make([]templating.RenderedEvent, 0, i.events.Len())
	i.events.Root().Walk(func(_ []byte, contributors *iradix.Tree[incrementalIndexedEvent]) bool {
		_, event, exists := contributors.Root().Maximum()
		if exists {
			result = append(result, event.value)
		}
		return false
	})
	return result, nil
}

func (i *incrementalGroupIndex) statusPatchCalls() ([]incrementalStatusPatchCall, error) {
	if err := i.validateAuthentication(); err != nil {
		return nil, err
	}
	result := make([]incrementalStatusPatchCall, 0, i.status.Len())
	var validationErr error
	i.status.Root().Walk(func(_ []byte, indexed incrementalIndexedStatusPatchCall) bool {
		if err := validateIncrementalPreparedStatusPatchCall(indexed.prepared, i.memo.authority, indexed.location); err != nil {
			validationErr = err
			return true
		}
		result = append(result, indexed.prepared.call())
		return false
	})
	if validationErr != nil {
		return nil, validationErr
	}
	return result, nil
}

func (i *incrementalGroupIndex) httpEffects() ([]incrementalHTTPEffect, error) {
	if err := i.validateAuthentication(); err != nil {
		return nil, err
	}
	result := make([]incrementalHTTPEffect, 0, i.http.Len())
	i.http.Root().Walk(func(_ []byte, contributors *iradix.Tree[incrementalIndexedHTTP]) bool {
		_, effect, exists := contributors.Root().Maximum()
		if exists {
			result = append(result, effect.value)
		}
		return false
	})
	return result, nil
}

func (i *incrementalGroupIndex) publishedWinners(cell string) ([]incrementalPublishedWinner, error) {
	if i == nil {
		return nil, errors.New("incremental publication index is unavailable")
	}
	if err := i.validateAuthentication(); err != nil {
		return nil, err
	}
	projection, exists := i.publicationWinnersByLocation.Root().Get(incrementalOrderedTuple(cell))
	if !exists {
		return []incrementalPublishedWinner{}, nil
	}
	if projection == nil || projection.Len() == 0 {
		return nil, errors.New("incremental publication winner projection has an empty cell")
	}
	result := make([]incrementalPublishedWinner, 0, projection.Len())
	projection.Root().Walk(func(_ string, winner incrementalIndexedPublication) bool {
		result = append(result, detachIncrementalPublishedWinner(&winner))
		return false
	})
	return result, nil
}

func (i *incrementalGroupIndex) rankedPublishedWinners(cell string) ([]incrementalPublishedWinner, error) {
	if i == nil {
		return nil, errors.New("incremental publication index is unavailable")
	}
	if err := i.validateAuthentication(); err != nil {
		return nil, err
	}
	projection, exists := i.publicationWinnersByRank.Root().Get(incrementalOrderedTuple(cell))
	if !exists {
		return []incrementalPublishedWinner{}, nil
	}
	if projection == nil || projection.Len() == 0 {
		return nil, errors.New("incremental ranked publication winner projection has an empty cell")
	}
	result := make([]incrementalPublishedWinner, 0, projection.Len())
	projection.Root().Walk(func(_ string, winner incrementalIndexedPublication) bool {
		result = append(result, detachIncrementalPublishedWinner(&winner))
		return false
	})
	return result, nil
}

func detachIncrementalPublishedWinner(winner *incrementalIndexedPublication) incrementalPublishedWinner {
	return incrementalPublishedWinner{
		instance: winner.instance,
		location: []byte(winner.location),
		value: incrementalPublishedValue{
			Cell: winner.cell, Key: winner.key, Rank: winner.rank, Value: []byte(winner.value),
		},
	}
}

func (i *incrementalGroupIndex) publishedWinnerCount(cell string) (int, error) {
	if i == nil {
		return 0, errors.New("incremental publication index is unavailable")
	}
	if err := i.validateAuthentication(); err != nil {
		return 0, err
	}
	count, _ := i.publicationCounts.Root().Get(incrementalOrderedTuple(cell))
	return count, nil
}

func (i *incrementalGroupIndex) allPublishedWinners() ([]incrementalPublishedWinner, error) {
	if i == nil {
		return nil, errors.New("incremental publication index is unavailable")
	}
	if err := i.validateAuthentication(); err != nil {
		return nil, err
	}
	result := make([]incrementalPublishedWinner, 0, i.publications.Len())
	i.publications.Root().Walk(func(_ string, owners *persistenttree.Tree[incrementalIndexedPublication]) bool {
		_, winner, exists := owners.Root().Minimum()
		if exists {
			result = append(result, detachIncrementalPublishedWinner(&winner))
		}
		return false
	})
	slices.SortFunc(result, func(left, right incrementalPublishedWinner) int {
		return bytes.Compare(left.location, right.location)
	})
	return result, nil
}

func (i *incrementalGroupIndex) publishedWinner(
	cell, key string,
) (incrementalPublishedWinner, bool, error) {
	if i == nil {
		return incrementalPublishedWinner{}, false, errors.New("incremental publication index is unavailable")
	}
	if err := i.validateAuthentication(); err != nil {
		return incrementalPublishedWinner{}, false, err
	}
	owners, exists := i.publications.Root().Get(incrementalPublicationIdentityKey(cell, key))
	if !exists || owners == nil {
		return incrementalPublishedWinner{}, false, nil
	}
	_, winner, exists := owners.Root().Minimum()
	if !exists {
		return incrementalPublishedWinner{}, false, errors.New("incremental publication identity has no owners")
	}
	return detachIncrementalPublishedWinner(&winner), true, nil
}

func (i *incrementalGroupIndex) validateAuthentication() error {
	return i.validateAuthenticationWithAudit(nil)
}

func (i *incrementalGroupIndex) validateAuthenticationWithAudit(auditVisits *int) error {
	if i.authenticated() {
		return nil
	}
	if err := i.auditPublications(auditVisits); err != nil {
		return err
	}
	return errors.New("incremental group authentication seal does not match its roots")
}

func (i *incrementalGroupIndex) auditPublications(auditVisits *int) error {
	if i == nil || i.instances == nil || i.publications == nil ||
		i.publicationWinnersByLocation == nil || i.publicationWinnersByRank == nil ||
		i.publicationCounts == nil {
		return errors.New("incremental publication index is unavailable")
	}
	seen := make(map[string]struct{})
	if err := i.auditPublicationResults(seen, auditVisits); err != nil {
		return err
	}
	if err := i.auditPublicationEntries(seen, auditVisits); err != nil {
		return err
	}
	if err := i.auditPublicationWinnerProjections(auditVisits); err != nil {
		return err
	}
	return i.auditPublicationCounts(auditVisits)
}

func (i *incrementalGroupIndex) auditPublicationResults(
	seen map[string]struct{},
	auditVisits *int,
) error {
	var auditErr error
	i.instances.Root().Walk(func(_ string, instance incrementalIndexedGroupInstance) bool {
		incrementAuditVisits(auditVisits)
		result, err := decodeIndexedGroupInstanceResult(&instance)
		if err != nil {
			auditErr = err
			return true
		}
		if err := validateIncrementalInstanceResult(&result); err != nil {
			auditErr = fmt.Errorf("incremental publication result is invalid: %w", err)
			return true
		}
		for index := range result.Published {
			value := &result.Published[index]
			identity := incrementalPublicationIdentityKey(value.Cell, value.Key)
			tree, exists := i.publications.Root().Get(identity)
			if !exists {
				auditErr = errors.New("incremental publication index is missing an identity")
				return true
			}
			location := incrementalGroupLocationKey(instance.id, uint64(index))
			ownerKey := incrementalPublicationOwnerKey(value.Rank, location)
			indexed, exists := tree.Root().Get(ownerKey)
			if !exists || indexed.instance != instance.id || indexed.location != string(location) ||
				indexed.cell != value.Cell || indexed.key != value.Key || indexed.rank != value.Rank ||
				indexed.value != string(value.Value) {
				auditErr = errors.New("incremental publication index does not match its result")
				return true
			}
			seen[string(incrementalOrderedTuple(string(identity), string(ownerKey)))] = struct{}{}
		}
		return false
	})
	return auditErr
}

func (i *incrementalGroupIndex) auditPublicationEntries(
	seen map[string]struct{},
	auditVisits *int,
) error {
	var auditErr error
	i.publications.Root().Walk(func(identity string, owners *persistenttree.Tree[incrementalIndexedPublication]) bool {
		incrementAuditVisits(auditVisits)
		if owners == nil || owners.Len() == 0 {
			auditErr = errors.New("incremental publication index has an empty identity")
			return true
		}
		owners.Root().Walk(func(ownerKey string, _ incrementalIndexedPublication) bool {
			if _, exists := seen[string(incrementalOrderedTuple(identity, ownerKey))]; !exists {
				auditErr = errors.New("incremental publication index has no matching result")
				return true
			}
			return false
		})
		return auditErr != nil
	})
	return auditErr
}

func (i *incrementalGroupIndex) auditPublicationCounts(auditVisits *int) error {
	expected := map[string]int{}
	i.publications.Root().Walk(func(_ string, owners *persistenttree.Tree[incrementalIndexedPublication]) bool {
		incrementAuditVisits(auditVisits)
		_, winner, exists := owners.Root().Minimum()
		if exists {
			expected[winner.cell]++
		}
		return false
	})
	var auditErr error
	i.publicationCounts.Root().Walk(func(key string, count int) bool {
		incrementAuditVisits(auditVisits)
		cellParts, ok := decodeIncrementalOrderedTuple([]byte(key))
		if !ok || len(cellParts) != 1 {
			auditErr = errors.New("incremental publication count index has an invalid cell")
			return true
		}
		cell := cellParts[0]
		if count <= 0 || expected[cell] != count {
			auditErr = errors.New("incremental publication count index does not match its publications")
			return true
		}
		delete(expected, cell)
		return false
	})
	if auditErr != nil {
		return auditErr
	}
	if len(expected) != 0 {
		return errors.New("incremental publication count index is missing a cell")
	}
	return nil
}

func (i *incrementalGroupIndex) auditPublicationWinnerProjections(auditVisits *int) error {
	var auditErr error
	i.publications.Root().Walk(func(_ string, owners *persistenttree.Tree[incrementalIndexedPublication]) bool {
		incrementAuditVisits(auditVisits)
		if owners == nil {
			auditErr = errors.New("incremental publication identity has no owners")
			return true
		}
		_, winner, exists := owners.Root().Minimum()
		if !exists {
			auditErr = errors.New("incremental publication identity has no owners")
			return true
		}
		if err := i.auditPublicationWinnerProjectionEntry(i.publicationWinnersByLocation, &winner, false); err != nil {
			auditErr = err
			return true
		}
		if err := i.auditPublicationWinnerProjectionEntry(i.publicationWinnersByRank, &winner, true); err != nil {
			auditErr = err
			return true
		}
		return false
	})
	if auditErr != nil {
		return auditErr
	}
	if err := i.auditPublicationWinnerProjectionRoot(
		i.publicationWinnersByLocation, false, auditVisits,
	); err != nil {
		return err
	}
	return i.auditPublicationWinnerProjectionRoot(i.publicationWinnersByRank, true, auditVisits)
}

func (i *incrementalGroupIndex) auditPublicationWinnerProjectionEntry(
	projection *persistenttree.Tree[*persistenttree.Tree[incrementalIndexedPublication]],
	winner *incrementalIndexedPublication,
	ranked bool,
) error {
	cell, exists := projection.Root().Get(incrementalOrderedTuple(winner.cell))
	if !exists || cell == nil {
		return errors.New("incremental publication winner projection is missing a cell")
	}
	indexed, exists := cell.Root().Get(incrementalPublicationProjectionKey(winner, ranked))
	if !exists || indexed != *winner {
		return errors.New("incremental publication winner projection does not match its owner")
	}
	return nil
}

func (i *incrementalGroupIndex) auditPublicationWinnerProjectionRoot(
	projection *persistenttree.Tree[*persistenttree.Tree[incrementalIndexedPublication]],
	ranked bool,
	auditVisits *int,
) error {
	var auditErr error
	projection.Root().Walk(func(cellKey string, winners *persistenttree.Tree[incrementalIndexedPublication]) bool {
		incrementAuditVisits(auditVisits)
		parts, ok := decodeIncrementalOrderedTuple([]byte(cellKey))
		if !ok || len(parts) != 1 || winners == nil || winners.Len() == 0 {
			auditErr = errors.New("incremental publication winner projection has an invalid cell")
			return true
		}
		cell := parts[0]
		winners.Root().Walk(func(key string, winner incrementalIndexedPublication) bool {
			incrementAuditVisits(auditVisits)
			if winner.cell != cell || !bytes.Equal([]byte(key), incrementalPublicationProjectionKey(&winner, ranked)) {
				auditErr = errors.New("incremental publication winner projection has an invalid location")
				return true
			}
			owners, exists := i.publications.Root().Get(
				incrementalPublicationIdentityKey(winner.cell, winner.key),
			)
			if !exists || owners == nil {
				auditErr = errors.New("incremental publication winner projection has no identity")
				return true
			}
			_, expected, exists := owners.Root().Minimum()
			if !exists || expected != winner {
				auditErr = errors.New("incremental publication winner projection is not the current owner")
				return true
			}
			return false
		})
		return auditErr != nil
	})
	return auditErr
}

func incrementAuditVisits(visits *int) {
	if visits != nil {
		(*visits)++
	}
}

func (i *incrementalGroupIndex) authenticate() {
	i.auth = incrementalGroupAuthentication{
		instances:                    i.instances,
		contributors:                 i.contributors,
		publications:                 i.publications,
		publicationWinnersByLocation: i.publicationWinnersByLocation,
		publicationWinnersByRank:     i.publicationWinnersByRank,
		publicationCounts:            i.publicationCounts,
		events:                       i.events,
		status:                       i.status,
		http:                         i.http,
		outputs:                      i.outputs,
		rankedText:                   i.rankedText,
		memo:                         i.memo,
		memoAuthority:                i.memoAuthority,
		memoGeneration:               i.memoGeneration,
	}
}

func (i *incrementalGroupIndex) authenticated() bool {
	return i != nil && i.rootsAvailable() && i.auth.matches(i)
}

func (i *incrementalGroupIndex) rootsAvailable() bool {
	return i.instances != nil && i.contributors != nil && i.publications != nil &&
		i.publicationWinnersByLocation != nil && i.publicationWinnersByRank != nil &&
		i.publicationCounts != nil &&
		i.events != nil && i.status != nil && i.http != nil && i.outputs != nil && i.rankedText != nil && i.memo.valid() &&
		i.memo.authority == i.memoAuthority && i.memo.generation == i.memoGeneration
}

func (a *incrementalGroupAuthentication) matches(i *incrementalGroupIndex) bool {
	return a.instances == i.instances && a.contributors == i.contributors &&
		a.publications == i.publications &&
		a.publicationWinnersByLocation == i.publicationWinnersByLocation &&
		a.publicationWinnersByRank == i.publicationWinnersByRank &&
		a.publicationCounts == i.publicationCounts && a.events == i.events &&
		a.status == i.status && a.http == i.http && a.outputs == i.outputs && a.rankedText == i.rankedText && a.memo == i.memo &&
		a.memoAuthority == i.memoAuthority && a.memoGeneration == i.memoGeneration
}

func (i *incrementalGroupIndex) validatePublicationPaths(
	id incrementalGroupInstanceID,
	result *incrementalComponentResult,
) error {
	for index := range result.Published {
		value := &result.Published[index]
		owners, exists := i.publications.Root().Get(incrementalPublicationIdentityKey(value.Cell, value.Key))
		if !exists || owners == nil {
			return errors.New("incremental publication index is missing an identity")
		}
		location := incrementalGroupLocationKey(id, uint64(index))
		indexed, exists := owners.Root().Get(incrementalPublicationOwnerKey(value.Rank, location))
		if !exists || indexed.instance != id || indexed.location != string(location) ||
			indexed.cell != value.Cell || indexed.key != value.Key || indexed.rank != value.Rank ||
			indexed.value != string(value.Value) {
			return errors.New("incremental publication index does not match its result")
		}
	}
	return nil
}

func (i *incrementalGroupIndex) validateReplacedPublicationPaths(
	id incrementalGroupInstanceID,
	previous, next *incrementalComponentResult,
) error {
	for index := range previous.Published {
		value := &previous.Published[index]
		if next != nil && index < len(next.Published) &&
			next.Published[index].Cell == value.Cell && next.Published[index].Key == value.Key &&
			next.Published[index].Rank == value.Rank {
			continue
		}
		owners, exists := i.publications.Root().Get(incrementalPublicationIdentityKey(value.Cell, value.Key))
		if !exists {
			continue
		}
		location := incrementalGroupLocationKey(id, uint64(index))
		if _, exists := owners.Root().Get(incrementalPublicationOwnerKey(value.Rank, location)); exists {
			return errors.New("incremental publication index retained a removed instance")
		}
	}
	return nil
}

func (i *incrementalGroupIndex) validateChangedPublicationPaths(
	id incrementalGroupInstanceID,
	previous, next *incrementalComponentResult,
) error {
	if previous != nil {
		if err := i.validateReplacedPublicationPaths(id, previous, next); err != nil {
			return err
		}
	}
	if next != nil {
		return i.validatePublicationPaths(id, next)
	}
	return nil
}

func incrementalGroupInstanceKey(id incrementalGroupInstanceID) []byte {
	return incrementalOrderedTuple(id.component, id.source, id.namespace, id.name)
}

func incrementalComponentInstanceKey(id incrementalGroupInstanceID) []byte {
	return incrementalOrderedTuple(id.source, id.namespace, id.name)
}

func incrementalGroupLocationKey(id incrementalGroupInstanceID, index uint64) []byte {
	return incrementalGroupLocationKeyForInstanceKey(incrementalGroupInstanceKey(id), index)
}

func incrementalGroupLocationKeyForInstanceKey(instanceKey []byte, index uint64) []byte {
	key := make([]byte, len(instanceKey)+8)
	copy(key, instanceKey)
	var encoded [8]byte
	binary.BigEndian.PutUint64(encoded[:], index)
	copy(key[len(instanceKey):], encoded[:])
	return key
}

func incrementalContributionIdentityKey(value incrementalContribution) []byte {
	return incrementalOrderedTuple(value.Cell, value.Key)
}

func incrementalPublicationIdentityKey(cell, key string) []byte {
	return incrementalOrderedTuple(cell, key)
}

func incrementalPublicationOwnerKey(rank string, location []byte) []byte {
	if rank == "" {
		return location
	}
	return append(incrementalOrderedTuple(rank), location...)
}

func incrementalEventIdentityKey(value *templating.RenderedEvent) []byte {
	return incrementalOrderedTuple(
		value.Namespace,
		value.Name,
		value.APIVersion,
		value.Kind,
		value.Type,
		value.Reason,
		value.Message,
	)
}

func incrementalHTTPIdentityKey(id uint64) []byte {
	var key [8]byte
	binary.BigEndian.PutUint64(key[:], id)
	return key[:]
}

// appendIncrementalOrderedTuplePart appends one part in the tuple encoding: a
// 0x00 byte becomes 0x00 0xff, and the part is closed by the two-zero
// separator. It exists so a caller that knows its parts can build the whole key
// in one buffer instead of materialising each part as a string first.
func appendIncrementalOrderedTuplePart(dst []byte, part string) []byte {
	for index := range len(part) {
		if part[index] == 0 {
			dst = append(dst, 0, 0xff)
		} else {
			dst = append(dst, part[index])
		}
	}
	return append(dst, 0, 0)
}

// appendIncrementalOrderedTupleUint appends a zero-padded decimal as a tuple
// part. Decimal digits are never 0x00, so no escaping applies. A value wider
// than width keeps all its digits, matching the %0*d verb.
func appendIncrementalOrderedTupleUint(dst []byte, value uint64, width int) []byte {
	var digits [20]byte
	end := len(digits)
	for {
		end--
		digits[end] = byte('0' + value%10)
		value /= 10
		if value == 0 {
			break
		}
	}
	for pad := width - (len(digits) - end); pad > 0; pad-- {
		dst = append(dst, '0')
	}
	dst = append(dst, digits[end:]...)
	return append(dst, 0, 0)
}

func incrementalOrderedTuple(parts ...string) []byte {
	length := 0
	for _, part := range parts {
		length += len(part) + strings.Count(part, "\x00") + 2
	}
	encoded := make([]byte, length)
	offset := 0
	for _, part := range parts {
		for index := range len(part) {
			if part[index] == 0 {
				encoded[offset+1] = 0xff
				offset += 2
			} else {
				encoded[offset] = part[index]
				offset++
			}
		}
		offset += 2
	}
	return encoded
}
