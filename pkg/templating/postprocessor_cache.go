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

package templating

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
)

var (
	errPostProcessCacheIdentityMissing     = errors.New("post-process cache identity is missing")
	errPostProcessCacheComputeMissing      = errors.New("post-process cache compute function is missing")
	errPostProcessCacheBatchComputeMissing = errors.New("post-process cache batch compute function is missing")
	errPostProcessCacheComputePanicked     = errors.New("post-process cache compute function panicked")
	errPostProcessCacheTransactionClosed   = errors.New("post-process cache transaction is closed")
	errPostProcessCacheTransactionInFlight = errors.New("post-process cache transaction has computations in flight")
	errPostProcessCacheTransactionAborted  = errors.New("post-process cache transaction was aborted")
)

type postProcessCacheBatchCompute func(context.Context, []string) ([]string, error)

// postProcessCacheIdentity is allocated once for a compiler-certified processor chain.
type postProcessCacheIdentity struct {
	marker byte
}

type postProcessCacheKey struct {
	identity *postProcessCacheIdentity
	input    string
}

type postProcessCacheGeneration struct {
	entries map[postProcessCacheKey]string
}

type postProcessCache struct {
	active atomic.Pointer[postProcessCacheGeneration]
}

func newPostProcessCache() *postProcessCache {
	cache := &postProcessCache{}
	cache.active.Store(&postProcessCacheGeneration{entries: map[postProcessCacheKey]string{}})
	return cache
}

func newPostProcessCacheIdentity() *postProcessCacheIdentity {
	return &postProcessCacheIdentity{marker: 1}
}

func (c *postProcessCache) begin() *postProcessCacheTransaction {
	base := c.active.Load()
	return &postProcessCacheTransaction{
		cache:    c,
		base:     base,
		next:     make(map[postProcessCacheKey]string, len(base.entries)),
		inFlight: map[postProcessCacheKey]*postProcessCacheFlight{},
		state:    postProcessCacheTransactionOpen,
	}
}

type postProcessCacheTransactionState uint8

const (
	postProcessCacheTransactionOpen postProcessCacheTransactionState = iota
	postProcessCacheTransactionStaged
	postProcessCacheTransactionPublished
	postProcessCacheTransactionAborted
)

type postProcessCacheFlight struct {
	done  chan struct{}
	value string
	err   error
}

type postProcessCacheTransaction struct {
	cache *postProcessCache

	mu          sync.Mutex
	base        *postProcessCacheGeneration
	next        map[postProcessCacheKey]string
	inFlight    map[postProcessCacheKey]*postProcessCacheFlight
	state       postProcessCacheTransactionState
	failed      error
	publication *postProcessCachePublication
}

func (t *postProcessCacheTransaction) process(
	ctx context.Context,
	identity *postProcessCacheIdentity,
	input string,
	compute func(context.Context) (string, error),
) (string, error) {
	if identity == nil {
		t.fail(errPostProcessCacheIdentityMissing)
		return "", errPostProcessCacheIdentityMissing
	}
	if compute == nil {
		t.fail(errPostProcessCacheComputeMissing)
		return "", errPostProcessCacheComputeMissing
	}
	if err := context.Cause(ctx); err != nil {
		t.fail(err)
		return "", err
	}

	key := postProcessCacheKey{identity: identity, input: input}
	t.mu.Lock()
	if err := t.processErrorLocked(); err != nil {
		t.mu.Unlock()
		return "", err
	}
	if value, exists := t.next[key]; exists {
		t.mu.Unlock()
		return t.finishHit(ctx, value)
	}
	if flight, exists := t.inFlight[key]; exists {
		t.mu.Unlock()
		return t.waitForFlight(ctx, flight)
	}
	if value, exists := t.base.entries[key]; exists {
		t.next[key] = value
		t.mu.Unlock()
		return t.finishHit(ctx, value)
	}
	flight := &postProcessCacheFlight{done: make(chan struct{})}
	t.inFlight[key] = flight
	t.mu.Unlock()

	value, recovered, err := runPostProcessCacheCompute(ctx, compute)
	if cause := context.Cause(ctx); cause != nil {
		err = cause
	}
	if recovered != nil {
		_, _ = t.finishMiss(key, flight, "", errPostProcessCacheComputePanicked)
		panic(recovered)
	}
	return t.finishMiss(key, flight, value, err)
}

type postProcessCacheBatchEntry struct {
	key     postProcessCacheKey
	indices []int
	flight  *postProcessCacheFlight
	value   string
	hit     bool
	owned   bool
}

func (t *postProcessCacheTransaction) processBatch(
	ctx context.Context,
	identity *postProcessCacheIdentity,
	inputs []string,
	compute postProcessCacheBatchCompute,
) ([]string, error) {
	if identity == nil {
		t.fail(errPostProcessCacheIdentityMissing)
		return nil, errPostProcessCacheIdentityMissing
	}
	if compute == nil {
		t.fail(errPostProcessCacheBatchComputeMissing)
		return nil, errPostProcessCacheBatchComputeMissing
	}
	if err := context.Cause(ctx); err != nil {
		t.fail(err)
		return nil, err
	}
	if len(inputs) == 0 {
		return []string{}, nil
	}

	entries := newPostProcessCacheBatchEntries(identity, inputs)
	owned, err := t.claimBatchEntries(entries)
	if err != nil {
		return nil, err
	}
	if len(owned) > 0 {
		t.computeBatchMisses(ctx, entries, owned, compute)
	}

	results := make([]string, len(inputs))
	for index := range entries {
		entry := &entries[index]
		value := entry.value
		if !entry.hit {
			var err error
			value, err = t.waitForFlight(ctx, entry.flight)
			if err != nil {
				return nil, err
			}
		} else if err := context.Cause(ctx); err != nil {
			t.fail(err)
			return nil, err
		}
		for _, outputIndex := range entry.indices {
			results[outputIndex] = value
		}
	}
	return results, nil
}

func newPostProcessCacheBatchEntries(
	identity *postProcessCacheIdentity,
	inputs []string,
) []postProcessCacheBatchEntry {
	entries := make([]postProcessCacheBatchEntry, 0, len(inputs))
	entryByKey := make(map[postProcessCacheKey]int, len(inputs))
	for index, input := range inputs {
		key := postProcessCacheKey{identity: identity, input: input}
		if entryIndex, exists := entryByKey[key]; exists {
			entries[entryIndex].indices = append(entries[entryIndex].indices, index)
			continue
		}
		entryByKey[key] = len(entries)
		entries = append(entries, postProcessCacheBatchEntry{key: key, indices: []int{index}})
	}
	return entries
}

func (t *postProcessCacheTransaction) claimBatchEntries(
	entries []postProcessCacheBatchEntry,
) ([]int, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if err := t.processErrorLocked(); err != nil {
		return nil, err
	}
	owned := make([]int, 0, len(entries))
	for index := range entries {
		entry := &entries[index]
		if value, exists := t.next[entry.key]; exists {
			entry.value = value
			entry.hit = true
			continue
		}
		if flight, exists := t.inFlight[entry.key]; exists {
			entry.flight = flight
			continue
		}
		if value, exists := t.base.entries[entry.key]; exists {
			t.next[entry.key] = value
			entry.value = value
			entry.hit = true
			continue
		}
		entry.flight = &postProcessCacheFlight{done: make(chan struct{})}
		entry.owned = true
		t.inFlight[entry.key] = entry.flight
		owned = append(owned, index)
	}
	return owned, nil
}

func (t *postProcessCacheTransaction) computeBatchMisses(
	ctx context.Context,
	entries []postProcessCacheBatchEntry,
	owned []int,
	compute postProcessCacheBatchCompute,
) {
	misses := make([]string, len(owned))
	for index, entryIndex := range owned {
		misses[index] = entries[entryIndex].key.input
	}
	values, recovered, err := runPostProcessCacheBatchCompute(ctx, misses, compute)
	if cause := context.Cause(ctx); cause != nil {
		err = cause
	}
	if recovered != nil {
		err = errPostProcessCacheComputePanicked
	}
	if err == nil && len(values) != len(owned) {
		err = fmt.Errorf("post-process cache batch returned %d of %d outputs", len(values), len(owned))
	}
	err = remapPostProcessCacheBatchError(err, entries, owned)
	t.finishBatchMisses(entries, owned, values, err)
	if recovered != nil {
		panic(recovered)
	}
}

func runPostProcessCacheBatchCompute(
	ctx context.Context,
	inputs []string,
	compute postProcessCacheBatchCompute,
) (values []string, recovered any, err error) {
	defer func() {
		recovered = recover()
	}()
	values, err = compute(ctx, inputs)
	return values, nil, err
}

func remapPostProcessCacheBatchError(
	err error,
	entries []postProcessCacheBatchEntry,
	owned []int,
) error {
	if err == nil {
		return nil
	}
	var indexed interface{ BatchIndex() int }
	if !errors.As(err, &indexed) {
		return err
	}
	index := indexed.BatchIndex()
	if index < 0 || index >= len(owned) {
		return err
	}
	var batchErr *PostProcessBatchError
	if errors.As(err, &batchErr) {
		return &PostProcessBatchError{
			Index: entries[owned[index]].indices[0],
			Err:   batchErr.Err,
		}
	}
	return err
}

func (t *postProcessCacheTransaction) finishBatchMisses(
	entries []postProcessCacheBatchEntry,
	owned []int,
	values []string,
	err error,
) {
	t.mu.Lock()
	for _, entryIndex := range owned {
		delete(t.inFlight, entries[entryIndex].key)
	}
	switch {
	case t.state != postProcessCacheTransactionOpen:
		err = errPostProcessCacheTransactionClosed
	case err != nil:
		if t.failed == nil {
			t.failed = err
		}
		err = t.failed
	case t.failed != nil:
		err = t.failed
	}
	for index, entryIndex := range owned {
		entry := &entries[entryIndex]
		if err != nil {
			entry.flight.err = err
		} else {
			value := values[index]
			t.next[entry.key] = value
			entry.flight.value = value
		}
		close(entry.flight.done)
	}
	t.mu.Unlock()
}

func runPostProcessCacheCompute(
	ctx context.Context,
	compute func(context.Context) (string, error),
) (value string, recovered any, err error) {
	defer func() {
		recovered = recover()
	}()
	value, err = compute(ctx)
	return value, nil, err
}

func (t *postProcessCacheTransaction) finishHit(ctx context.Context, value string) (string, error) {
	if err := context.Cause(ctx); err != nil {
		t.fail(err)
		return "", err
	}
	return value, nil
}

func (t *postProcessCacheTransaction) waitForFlight(
	ctx context.Context,
	flight *postProcessCacheFlight,
) (string, error) {
	select {
	case <-flight.done:
		if flight.err != nil {
			return "", flight.err
		}
		return t.finishHit(ctx, flight.value)
	case <-ctx.Done():
		err := context.Cause(ctx)
		t.fail(err)
		return "", err
	}
}

func (t *postProcessCacheTransaction) finishMiss(
	key postProcessCacheKey,
	flight *postProcessCacheFlight,
	value string,
	err error,
) (string, error) {
	t.mu.Lock()
	delete(t.inFlight, key)
	switch {
	case t.state != postProcessCacheTransactionOpen:
		flight.err = errPostProcessCacheTransactionClosed
	case err != nil:
		if t.failed == nil {
			t.failed = err
		}
		flight.err = t.failed
	case t.failed != nil:
		flight.err = t.failed
	default:
		t.next[key] = value
		flight.value = value
	}
	close(flight.done)
	resultErr := flight.err
	t.mu.Unlock()
	if resultErr != nil {
		return "", resultErr
	}
	return value, nil
}

func (t *postProcessCacheTransaction) processErrorLocked() error {
	if t.state != postProcessCacheTransactionOpen {
		return errPostProcessCacheTransactionClosed
	}
	return t.failed
}

func (t *postProcessCacheTransaction) fail(err error) {
	if err == nil {
		return
	}
	t.mu.Lock()
	if t.state == postProcessCacheTransactionOpen && t.failed == nil {
		t.failed = err
	}
	t.mu.Unlock()
}

func (t *postProcessCacheTransaction) stage(ctx context.Context) (*postProcessCachePublication, error) {
	if err := context.Cause(ctx); err != nil {
		t.fail(err)
		return nil, err
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	if err := t.processErrorLocked(); err != nil {
		return nil, err
	}
	if len(t.inFlight) != 0 {
		return nil, errPostProcessCacheTransactionInFlight
	}
	if err := context.Cause(ctx); err != nil {
		t.failed = err
		return nil, err
	}
	publication := &postProcessCachePublication{
		cache:       t.cache,
		base:        t.base,
		generation:  &postProcessCacheGeneration{entries: t.next},
		transaction: t,
	}
	t.base = nil
	t.next = nil
	t.state = postProcessCacheTransactionStaged
	t.publication = publication
	return publication, nil
}

func (t *postProcessCacheTransaction) abort() {
	t.mu.Lock()
	if t.state == postProcessCacheTransactionPublished || t.state == postProcessCacheTransactionAborted {
		t.mu.Unlock()
		return
	}
	publication := t.publication
	if publication == nil {
		t.state = postProcessCacheTransactionAborted
		t.failed = errPostProcessCacheTransactionAborted
		t.base = nil
		t.next = nil
	}
	t.mu.Unlock()
	if publication != nil {
		publication.abort()
	}
}

type postProcessCachePublication struct {
	once sync.Once

	cache       *postProcessCache
	base        *postProcessCacheGeneration
	generation  *postProcessCacheGeneration
	transaction *postProcessCacheTransaction
}

func (p *postProcessCachePublication) publish() bool {
	published := false
	p.once.Do(func() {
		cache := p.cache
		base := p.base
		generation := p.generation
		if cache == nil || base == nil || generation == nil || p.transaction == nil {
			panic("post-process cache publication is incomplete")
		}
		if !p.finish(postProcessCacheTransactionPublished) {
			panic("post-process cache publication lost its transaction")
		}
		publishPostProcessCacheGeneration(cache, base, generation)
		published = true
	})
	return published
}

func publishPostProcessCacheGeneration(
	cache *postProcessCache,
	base,
	generation *postProcessCacheGeneration,
) {
	for {
		active := cache.active.Load()
		next := generation
		if active != base {
			next = mergePostProcessCacheGenerations(active, generation)
		}
		if cache.active.CompareAndSwap(active, next) {
			return
		}
	}
}

func mergePostProcessCacheGenerations(
	active,
	staged *postProcessCacheGeneration,
) *postProcessCacheGeneration {
	entries := make(map[postProcessCacheKey]string, len(active.entries)+len(staged.entries))
	for key, value := range active.entries {
		entries[key] = value
	}
	for key, value := range staged.entries {
		entries[key] = value
	}
	return &postProcessCacheGeneration{entries: entries}
}

func (p *postProcessCachePublication) abort() bool {
	aborted := false
	p.once.Do(func() {
		aborted = p.finish(postProcessCacheTransactionAborted)
	})
	return aborted
}

func (p *postProcessCachePublication) finish(state postProcessCacheTransactionState) bool {
	t := p.transaction
	if t == nil {
		return false
	}
	t.mu.Lock()
	if t.publication != p || t.state != postProcessCacheTransactionStaged {
		t.mu.Unlock()
		return false
	}
	t.publication = nil
	t.state = state
	if state == postProcessCacheTransactionAborted {
		t.failed = errPostProcessCacheTransactionAborted
	}
	t.mu.Unlock()
	p.generation = nil
	p.base = nil
	p.transaction = nil
	return true
}
