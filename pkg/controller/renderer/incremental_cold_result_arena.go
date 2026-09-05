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
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"sync"
	"sync/atomic"

	"gitlab.com/haproxy-haptic/haptic/pkg/controller/renderer/internal/resultauthority"
	"gitlab.com/haproxy-haptic/haptic/pkg/incremental"
)

var incrementalColdResultArenaGeneration atomic.Uint64

type incrementalColdResultArenaValue struct {
	result      incrementalComponentResult
	httpEffects []incrementalHTTPEffect
	httpTaken   bool
}

type incrementalColdResultArenaSlotState uint32

const (
	incrementalColdResultArenaSlotEmpty incrementalColdResultArenaSlotState = iota
	incrementalColdResultArenaSlotFilling
	incrementalColdResultArenaSlotStaged
	incrementalColdResultArenaSlotInitialized
)

type incrementalColdResultArenaStageAuthority struct {
	seal       *incrementalColdResultArenaStageAuthority
	arena      *incrementalColdResultArena
	generation uint64
	slot       int
	key        incremental.QueryKey
}

type incrementalColdResultArena struct {
	seal         *incrementalColdResultArena
	session      *incrementalRenderSession
	graphSession *incremental.Session
	wave         int
	generation   uint64
	batchIndexes []int
	keys         []incremental.QueryKey
	authority    *resultauthority.Arena[*incrementalColdResultArenaValue, authenticatedFreshComponentEffects]
	owned        []incrementalColdResultArenaValue
	stage        []incrementalColdResultArenaStageAuthority
	encoded      []string
	metadata     []authenticatedFreshComponentEffects
	states       []atomic.Uint32
	fresh        []authenticatedFreshComponentResult
	ownershipMu  sync.RWMutex
	revoked      atomic.Bool
}

func newIncrementalColdResultArena(
	session *incrementalRenderSession,
	wave int,
	batchIndexes []int,
	keys []incremental.QueryKey,
) (*incrementalColdResultArena, error) {
	if session == nil || session.graphSession == nil || wave < 0 || len(batchIndexes) == 0 ||
		len(batchIndexes) != len(keys) || !slices.IsSorted(batchIndexes) {
		return nil, errors.New("incremental cold result arena is incomplete")
	}
	previousBatchIndex := -1
	for index, batchIndex := range batchIndexes {
		if batchIndex < 0 || keys[index].Opaque() == "" || batchIndex == previousBatchIndex {
			return nil, errors.New("incremental cold result arena has invalid slots")
		}
		previousBatchIndex = batchIndex
	}
	seenKeys := make(map[incremental.QueryKey]struct{}, len(keys))
	for _, key := range keys {
		if _, duplicate := seenKeys[key]; duplicate {
			return nil, errors.New("incremental cold result arena repeats a query")
		}
		seenKeys[key] = struct{}{}
	}
	generation := incrementalColdResultArenaGeneration.Add(1)
	if generation == 0 {
		return nil, errors.New("incremental cold result arena generation overflow")
	}
	authority, err := resultauthority.NewArena[
		*incrementalColdResultArenaValue,
		authenticatedFreshComponentEffects,
	](len(batchIndexes), generation)
	if err != nil {
		return nil, fmt.Errorf("allocating incremental cold result authority: %w", err)
	}
	arena := &incrementalColdResultArena{
		session:      session,
		graphSession: session.graphSession,
		wave:         wave,
		generation:   generation,
		batchIndexes: slices.Clone(batchIndexes),
		keys:         slices.Clone(keys),
		authority:    authority,
		owned:        make([]incrementalColdResultArenaValue, len(batchIndexes)),
		stage:        make([]incrementalColdResultArenaStageAuthority, len(batchIndexes)),
		encoded:      make([]string, len(batchIndexes)),
		metadata:     make([]authenticatedFreshComponentEffects, len(batchIndexes)),
		states:       make([]atomic.Uint32, len(batchIndexes)),
		fresh:        make([]authenticatedFreshComponentResult, len(batchIndexes)),
	}
	arena.seal = arena
	for slot := range arena.stage {
		stage := &arena.stage[slot]
		*stage = incrementalColdResultArenaStageAuthority{
			arena: arena, generation: generation, slot: slot, key: keys[slot],
		}
		stage.seal = stage
	}
	return arena, nil
}

func (a *incrementalColdResultArena) validateAuthority() error {
	if a == nil || a.seal != a || a.session == nil || a.graphSession == nil ||
		a.session.graphSession != a.graphSession || a.wave < 0 || a.generation == 0 ||
		a.authority == nil || a.revoked.Load() || len(a.batchIndexes) == 0 ||
		len(a.batchIndexes) != len(a.keys) || len(a.batchIndexes) != len(a.owned) ||
		len(a.batchIndexes) != len(a.stage) || len(a.batchIndexes) != len(a.encoded) ||
		len(a.batchIndexes) != len(a.metadata) || len(a.batchIndexes) != len(a.states) ||
		len(a.batchIndexes) != len(a.fresh) {
		return errors.New("incremental cold result arena has invalid provenance")
	}
	return nil
}

func (a *incrementalColdResultArena) slotForBatchIndex(batchIndex int) (int, bool) {
	if err := a.validateAuthority(); err != nil {
		return 0, false
	}
	return slices.BinarySearch(a.batchIndexes, batchIndex)
}

func (a *incrementalColdResultArena) validStageAuthority(
	slot int,
	key incremental.QueryKey,
) bool {
	if slot < 0 || slot >= len(a.stage) {
		return false
	}
	stage := &a.stage[slot]
	return stage.seal == stage && stage.arena == a && stage.generation == a.generation &&
		stage.slot == slot && stage.key == key && a.keys[slot] == key
}

func (a *incrementalColdResultArena) initialize(
	slot int,
	key incremental.QueryKey,
	result *incrementalComponentResult,
	effects authenticatedFreshComponentEffects,
	httpEffects []incrementalHTTPEffect,
) (*authenticatedFreshComponentResult, error) {
	if err := a.validateAuthority(); err != nil {
		return nil, err
	}
	if slot < 0 || slot >= len(a.keys) || a.keys[slot] != key {
		return nil, errors.New("incremental cold result arena slot has invalid provenance")
	}
	canonical, err := json.Marshal(result)
	if err != nil {
		return nil, fmt.Errorf("encoding fresh incremental component result: %w", err)
	}
	encoded := string(canonical)
	a.ownershipMu.Lock()
	defer a.ownershipMu.Unlock()
	if err := a.validateAuthority(); err != nil {
		return nil, err
	}
	if !a.validStageAuthority(slot, key) ||
		incrementalColdResultArenaSlotState(a.states[slot].Load()) != incrementalColdResultArenaSlotEmpty {
		return nil, errors.New("incremental cold result arena slot has invalid provenance")
	}
	a.states[slot].Store(uint32(incrementalColdResultArenaSlotFilling))
	owned := &a.owned[slot]
	*owned = incrementalColdResultArenaValue{result: *result, httpEffects: httpEffects}
	ref, err := a.authority.InitializeOwned(
		slot,
		key,
		encoded,
		owned,
		&effects,
	)
	if err != nil {
		*owned = incrementalColdResultArenaValue{}
		a.states[slot].Store(uint32(incrementalColdResultArenaSlotEmpty))
		return nil, err
	}
	fresh := &a.fresh[slot]
	*fresh = authenticatedFreshComponentResult{
		key: key, encoded: encoded,
		arena: a, arenaRef: ref, arenaSlot: slot, arenaGen: a.generation,
	}
	fresh.seal = fresh
	a.states[slot].Store(uint32(incrementalColdResultArenaSlotInitialized))
	return fresh, nil
}

func (a *incrementalColdResultArena) stageResult(
	slot int,
	key incremental.QueryKey,
	result *incrementalComponentResult,
	effects authenticatedFreshComponentEffects,
	httpEffects []incrementalHTTPEffect,
) error {
	if a == nil || result == nil {
		return errors.New("incremental cold result arena staged result is unavailable")
	}
	canonical, err := json.Marshal(result)
	if err != nil {
		return fmt.Errorf("encoding fresh incremental component result: %w", err)
	}
	encoded := string(canonical)
	a.ownershipMu.RLock()
	defer a.ownershipMu.RUnlock()
	if err := a.validateAuthority(); err != nil {
		return err
	}
	if !a.validStageAuthority(slot, key) || !a.states[slot].CompareAndSwap(
		uint32(incrementalColdResultArenaSlotEmpty),
		uint32(incrementalColdResultArenaSlotFilling),
	) {
		return errors.New("incremental cold result arena staging slot is unavailable")
	}
	a.owned[slot] = incrementalColdResultArenaValue{result: *result, httpEffects: httpEffects}
	*result = incrementalComponentResult{}
	a.encoded[slot] = encoded
	a.metadata[slot] = effects
	a.states[slot].Store(uint32(incrementalColdResultArenaSlotStaged))
	return nil
}

func stagePreparedComponentResultIntoArena(
	prepared *preparedIncrementalComponent,
	text string,
	arena *incrementalColdResultArena,
	slot int,
) error {
	if prepared == nil || prepared.component == nil || prepared.recorder == nil ||
		prepared.httpFetcher == nil || arena == nil {
		return errors.New("incremental component arena staging is incomplete")
	}
	if err := prepared.lease.publicationError(); err != nil {
		return fmt.Errorf("incremental component %q capability lease: %w", prepared.component.name, err)
	}
	result, effects, err := prepared.recorder.validatedResult(
		prepared.component,
		prepared.source,
		prepared.namespace,
		prepared.name,
		text,
	)
	if err != nil {
		return fmt.Errorf("incremental component %q result: %w", prepared.component.name, err)
	}
	if err := arena.stageResult(
		slot,
		prepared.queryKey,
		&result,
		effects,
		prepared.httpFetcher.result(),
	); err != nil {
		return fmt.Errorf("incremental component %q arena result: %w", prepared.component.name, err)
	}
	return nil
}

func (a *incrementalColdResultArena) validateStagedRange() error {
	if err := a.validateAuthority(); err != nil {
		return err
	}
	for slot := range a.keys {
		if !a.validStageAuthority(slot, a.keys[slot]) ||
			incrementalColdResultArenaSlotState(a.states[slot].Load()) != incrementalColdResultArenaSlotStaged ||
			a.encoded[slot] == "" || a.fresh[slot] != (authenticatedFreshComponentResult{}) {
			return fmt.Errorf("incremental cold result arena staged slot %d has invalid provenance", slot)
		}
	}
	return nil
}

func (a *incrementalColdResultArena) initializeStagedMany() error {
	if a == nil {
		return errors.New("incremental cold result arena staged range is unavailable")
	}
	a.ownershipMu.Lock()
	defer a.ownershipMu.Unlock()
	if err := a.validateStagedRange(); err != nil {
		return err
	}
	requests := make([]resultauthority.InitializeRequest[
		*incrementalColdResultArenaValue,
		authenticatedFreshComponentEffects,
	], len(a.keys))
	for slot := range a.keys {
		requests[slot] = resultauthority.InitializeRequest[
			*incrementalColdResultArenaValue,
			authenticatedFreshComponentEffects,
		]{
			Index: slot, Key: a.keys[slot], Encoded: a.encoded[slot],
			Value: &a.owned[slot], Metadata: &a.metadata[slot],
		}
	}
	refs, err := a.authority.InitializeOwnedMany(requests)
	if err != nil {
		return err
	}
	if len(refs) != len(a.keys) {
		return errors.New("incremental cold result arena initialization returned an incomplete range")
	}
	for slot := range refs {
		fresh := &a.fresh[slot]
		*fresh = authenticatedFreshComponentResult{
			key: a.keys[slot], encoded: a.encoded[slot],
			arena: a, arenaRef: refs[slot], arenaSlot: slot, arenaGen: a.generation,
		}
		fresh.seal = fresh
		a.states[slot].Store(uint32(incrementalColdResultArenaSlotInitialized))
	}
	return nil
}

func (a *incrementalColdResultArena) stagedCompletionValues(
	batch incremental.ColdExactBatch,
	completedQueries []bool,
) ([]incremental.ColdExactBatchValue, error) {
	if a == nil {
		return nil, errors.New("incremental cold result arena staged completion is unavailable")
	}
	a.ownershipMu.RLock()
	defer a.ownershipMu.RUnlock()
	if err := a.validateStagedRange(); err != nil {
		return nil, err
	}
	if batch.Len() == 0 || len(completedQueries) != batch.Len() {
		return nil, errors.New("incremental cold result arena staged completion is incomplete")
	}
	values := make([]incremental.ColdExactBatchValue, len(a.keys))
	for slot, batchIndex := range a.batchIndexes {
		if batchIndex < 0 || batchIndex >= batch.Len() || completedQueries[batchIndex] ||
			batch.Query(batchIndex).Key() != a.keys[slot] {
			return nil, errors.New("incremental cold result arena staged completion slot is unavailable")
		}
		values[slot] = incremental.ColdExactBatchValue{
			Index: batchIndex, Key: a.keys[slot], Value: a.encoded[slot],
		}
	}
	return values, nil
}

func (a *incrementalColdResultArena) validateWrapper(
	fresh *authenticatedFreshComponentResult,
	key incremental.QueryKey,
) error {
	if err := a.validateAuthority(); err != nil {
		return err
	}
	if fresh == nil || fresh.authority != nil || fresh.arena != a || fresh.arenaRef == nil ||
		fresh.arenaGen != a.generation || fresh.arenaSlot < 0 || fresh.arenaSlot >= len(a.fresh) ||
		&a.fresh[fresh.arenaSlot] != fresh || a.keys[fresh.arenaSlot] != key {
		return errors.New("fresh incremental component result has invalid arena provenance")
	}
	return nil
}

func (a *incrementalColdResultArena) pending(
	fresh *authenticatedFreshComponentResult,
	key incremental.QueryKey,
) error {
	if err := a.validateWrapper(fresh, key); err != nil {
		return err
	}
	return fresh.arenaRef.Pending(key, fresh.encoded, fresh.root)
}

func (a *incrementalColdResultArena) bind(
	fresh *authenticatedFreshComponentResult,
	key incremental.QueryKey,
	root incremental.ExactValueRoot,
) error {
	if err := a.validateWrapper(fresh, key); err != nil {
		return err
	}
	return fresh.arenaRef.Bind(key, fresh.encoded, fresh.root, root)
}

func (a *incrementalColdResultArena) completionValues(
	batch incremental.ColdExactBatch,
	completedQueries []bool,
) ([]incremental.ColdExactBatchValue, error) {
	a.ownershipMu.Lock()
	defer a.ownershipMu.Unlock()
	if err := a.validateAuthority(); err != nil {
		return nil, err
	}
	if batch.Len() == 0 || len(completedQueries) != batch.Len() ||
		len(a.batchIndexes) != len(a.fresh) {
		return nil, errors.New("incremental cold result arena completion wave is incomplete")
	}
	values := make([]incremental.ColdExactBatchValue, len(a.fresh))
	for resultIndex, batchIndex := range a.batchIndexes {
		if batchIndex < 0 || batchIndex >= batch.Len() || completedQueries[batchIndex] {
			return nil, errors.New("incremental cold result arena completion slot is unavailable")
		}
		query := batch.Query(batchIndex)
		fresh := &a.fresh[resultIndex]
		if fresh.key != query.Key() {
			return nil, errors.New("incremental cold result arena completion query changed")
		}
		if err := a.validateWrapper(fresh, fresh.key); err != nil {
			return nil, err
		}
		if err := fresh.arenaRef.Pending(fresh.key, fresh.encoded, fresh.root); err != nil {
			return nil, err
		}
		values[resultIndex] = incremental.ColdExactBatchValue{
			Index: batchIndex,
			Key:   fresh.key,
			Value: fresh.encoded,
		}
	}
	return values, nil
}

func (a *incrementalColdResultArena) bindCompleted(
	results []incremental.ExactResult,
) error {
	a.ownershipMu.Lock()
	defer a.ownershipMu.Unlock()
	if err := a.validateAuthority(); err != nil {
		return err
	}
	if len(results) == 0 || len(results) != len(a.fresh) {
		return errors.New("incremental cold result arena completed range is incomplete")
	}
	requests := make([]resultauthority.BindRequest[
		*incrementalColdResultArenaValue,
		authenticatedFreshComponentEffects,
	], len(results))
	for index := range results {
		fresh := &a.fresh[index]
		if results[index].Key != a.keys[index] {
			return errors.New("incremental cold result arena completed range changed query order")
		}
		if err := a.validateWrapper(fresh, results[index].Key); err != nil {
			return err
		}
		requests[index] = resultauthority.BindRequest[
			*incrementalColdResultArenaValue,
			authenticatedFreshComponentEffects,
		]{
			Ref:       fresh.arenaRef,
			Key:       results[index].Key,
			Encoded:   fresh.encoded,
			OwnerRoot: fresh.root,
			Root:      results[index].Value,
		}
	}
	if err := resultauthority.BindMany(requests); err != nil {
		return err
	}
	if err := a.validateAuthority(); err != nil {
		return err
	}
	for index := range results {
		a.fresh[index].root = results[index].Value
	}
	return nil
}

func (r *incrementalRenderSession) completeColdResultArenaWave(
	batch incremental.ColdExactBatch,
	arena *incrementalColdResultArena,
	completedQueries []bool,
) ([]incremental.ExactResult, error) {
	if r == nil || r.state == nil || r.state.graph == nil || arena == nil || arena.session != r {
		return nil, errors.New("incremental cold result arena completion has invalid provenance")
	}
	values, err := arena.completionValues(batch, completedQueries)
	if err != nil {
		return nil, err
	}
	return r.completeColdResultArenaValues(batch, arena, values)
}

func (r *incrementalRenderSession) completeStagedColdResultArenaWave(
	batch incremental.ColdExactBatch,
	arena *incrementalColdResultArena,
	completedQueries []bool,
) ([]incremental.ExactResult, error) {
	if r == nil || r.state == nil || r.state.graph == nil || arena == nil || arena.session != r {
		return nil, errors.New("incremental cold staged result arena completion has invalid provenance")
	}
	values, err := arena.stagedCompletionValues(batch, completedQueries)
	if err != nil {
		return nil, err
	}
	if err := arena.initializeStagedMany(); err != nil {
		return nil, err
	}
	return r.completeColdResultArenaValues(batch, arena, values)
}

func (r *incrementalRenderSession) completeColdResultArenaValues(
	batch incremental.ColdExactBatch,
	arena *incrementalColdResultArena,
	values []incremental.ColdExactBatchValue,
) ([]incremental.ExactResult, error) {
	results, err := batch.CompleteWave(values...)
	if err != nil {
		return nil, err
	}
	if len(results) != len(values) {
		return nil, errors.New("incremental cold result arena completion returned an incomplete range")
	}
	for index := range results {
		if results[index].Key != values[index].Key {
			return nil, errors.New("incremental cold result arena completion returned results out of order")
		}
		if err := r.state.graph.ValidateExactValue(results[index].Key, results[index].Value); err != nil {
			return nil, err
		}
	}
	if err := arena.bindCompleted(results); err != nil {
		return nil, err
	}
	return results, nil
}

func (a *incrementalColdResultArena) validate(
	fresh *authenticatedFreshComponentResult,
	key incremental.QueryKey,
	root incremental.ExactValueRoot,
) error {
	if err := a.validateWrapper(fresh, key); err != nil {
		return err
	}
	return fresh.arenaRef.Validate(key, fresh.encoded, fresh.root, root)
}

func (a *incrementalColdResultArena) materialize(
	fresh *authenticatedFreshComponentResult,
	key incremental.QueryKey,
	root incremental.ExactValueRoot,
) (incrementalComponentResult, error) {
	a.ownershipMu.Lock()
	defer a.ownershipMu.Unlock()
	if err := a.validateWrapper(fresh, key); err != nil {
		return incrementalComponentResult{}, err
	}
	value, err := fresh.arenaRef.Materialize(
		key,
		fresh.encoded,
		fresh.root,
		root,
		func(value **incrementalColdResultArenaValue) *incrementalColdResultArenaValue {
			return *value
		},
	)
	if err != nil {
		return incrementalComponentResult{}, err
	}
	if value != &a.owned[fresh.arenaSlot] {
		return incrementalComponentResult{}, errors.New("incremental cold result arena owns a different slot value")
	}
	return cloneIncrementalComponentResult(&value.result), nil
}

func validateIncrementalColdResultArenaEncoding(
	result *incrementalComponentResult,
	encoded string,
) error {
	if result == nil {
		return errors.New("incremental cold result arena value is unavailable")
	}
	canonical, err := json.Marshal(result)
	if err != nil {
		return fmt.Errorf("encoding incremental cold result arena value: %w", err)
	}
	if !stringBytesEqual(encoded, canonical) {
		return errors.New("incremental cold result arena value changed after encoding")
	}
	return nil
}

func (a *incrementalColdResultArena) take(
	fresh *authenticatedFreshComponentResult,
	key incremental.QueryKey,
	root incremental.ExactValueRoot,
) (incrementalComponentResult, error) {
	a.ownershipMu.Lock()
	defer a.ownershipMu.Unlock()
	if err := a.validateWrapper(fresh, key); err != nil {
		return incrementalComponentResult{}, err
	}
	owned := &a.owned[fresh.arenaSlot]
	preflight, err := fresh.arenaRef.Materialize(
		key,
		fresh.encoded,
		fresh.root,
		root,
		func(value **incrementalColdResultArenaValue) *incrementalColdResultArenaValue {
			return *value
		},
	)
	if err != nil {
		return incrementalComponentResult{}, err
	}
	if preflight != owned {
		return incrementalComponentResult{}, errors.New("incremental cold result arena owns a different slot value")
	}
	if err := validateIncrementalColdResultArenaEncoding(&owned.result, fresh.encoded); err != nil {
		return incrementalComponentResult{}, err
	}
	value, err := fresh.arenaRef.Take(key, fresh.encoded, fresh.root, root)
	if err != nil {
		return incrementalComponentResult{}, err
	}
	if value != owned {
		return incrementalComponentResult{}, errors.New("incremental cold result arena transferred a different slot value")
	}
	result := value.result
	value.result = incrementalComponentResult{}
	return result, nil
}

func (a *incrementalColdResultArena) takeManyInto(
	fresh []*authenticatedFreshComponentResult,
	keys []incremental.QueryKey,
	roots []incremental.ExactValueRoot,
	destination []incrementalComponentResult,
	destinationIndexes []int,
) error {
	a.ownershipMu.Lock()
	defer a.ownershipMu.Unlock()
	if err := a.validateAuthority(); err != nil {
		return err
	}
	if len(fresh) == 0 || len(fresh) != len(keys) || len(fresh) != len(roots) ||
		len(fresh) != len(destinationIndexes) || len(destination) == 0 {
		return errors.New("incremental cold result arena transfer is incomplete")
	}
	requests := make([]resultauthority.TakeRequest[
		*incrementalColdResultArenaValue,
		authenticatedFreshComponentEffects,
	], len(fresh))
	previousDestinationIndex := -1
	for index := range fresh {
		destinationIndex := destinationIndexes[index]
		if destinationIndex < 0 || destinationIndex >= len(destination) ||
			destinationIndex <= previousDestinationIndex {
			return errors.New("incremental cold result arena destination has invalid provenance")
		}
		previousDestinationIndex = destinationIndex
		if err := a.validateWrapper(fresh[index], keys[index]); err != nil {
			return err
		}
		requests[index] = resultauthority.TakeRequest[
			*incrementalColdResultArenaValue,
			authenticatedFreshComponentEffects,
		]{
			Ref:       fresh[index].arenaRef,
			Key:       keys[index],
			Encoded:   fresh[index].encoded,
			OwnerRoot: fresh[index].root,
			Root:      roots[index],
		}
	}
	for index := range fresh {
		if err := validateIncrementalColdResultArenaEncoding(
			&a.owned[fresh[index].arenaSlot].result,
			fresh[index].encoded,
		); err != nil {
			return err
		}
	}
	values, err := resultauthority.TakeMany(requests)
	if err != nil {
		return err
	}
	for index := range values {
		if values[index] != &a.owned[fresh[index].arenaSlot] {
			return errors.New("incremental cold result arena transferred a different slot range")
		}
	}
	for index := range values {
		destination[destinationIndexes[index]] = values[index].result
		values[index].result = incrementalComponentResult{}
	}
	return nil
}

func (a *incrementalColdResultArena) metadataMatches(
	fresh *authenticatedFreshComponentResult,
	key incremental.QueryKey,
	root incremental.ExactValueRoot,
	effects authenticatedFreshComponentEffects,
) error {
	if err := a.validateWrapper(fresh, key); err != nil {
		return err
	}
	return fresh.arenaRef.MetadataMatches(key, fresh.encoded, fresh.root, root, effects)
}

func (a *incrementalColdResultArena) takeHTTPEffectsMany() ([][]incrementalHTTPEffect, error) {
	a.ownershipMu.Lock()
	defer a.ownershipMu.Unlock()
	if err := a.validateAuthority(); err != nil {
		return nil, err
	}
	if len(a.fresh) == 0 {
		return nil, errors.New("incremental cold result HTTP transfer is empty")
	}
	for index := range a.fresh {
		fresh := &a.fresh[index]
		key := a.keys[index]
		if err := a.validateWrapper(fresh, key); err != nil {
			return nil, err
		}
		if err := fresh.arenaRef.Validate(
			key,
			fresh.encoded,
			fresh.root,
			fresh.root,
		); err != nil {
			return nil, err
		}
		value, err := fresh.arenaRef.Materialize(
			key,
			fresh.encoded,
			fresh.root,
			fresh.root,
			func(value **incrementalColdResultArenaValue) *incrementalColdResultArenaValue {
				return *value
			},
		)
		if err != nil {
			return nil, err
		}
		if value != &a.owned[fresh.arenaSlot] || value.httpTaken {
			return nil, errors.New("incremental cold result HTTP ownership is unavailable")
		}
	}
	effects := make([][]incrementalHTTPEffect, len(a.fresh))
	for index := range a.fresh {
		value := &a.owned[index]
		effects[index] = value.httpEffects
		value.httpEffects = nil
		value.httpTaken = true
	}
	return effects, nil
}

func (a *incrementalColdResultArena) revoke() {
	if a == nil || !a.revoked.CompareAndSwap(false, true) {
		return
	}
	if a.authority != nil {
		a.authority.Revoke()
	}
	a.ownershipMu.Lock()
	clear(a.owned)
	clear(a.encoded)
	clear(a.metadata)
	for slot := range a.states {
		a.states[slot].Store(uint32(incrementalColdResultArenaSlotEmpty))
	}
	a.ownershipMu.Unlock()
}
