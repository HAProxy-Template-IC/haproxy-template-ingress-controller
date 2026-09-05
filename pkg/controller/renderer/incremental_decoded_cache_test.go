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
	"reflect"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"gitlab.com/haproxy-haptic/haptic/pkg/incremental"
	"gitlab.com/haproxy-haptic/haptic/pkg/templating"
)

func TestIncrementalDecodedCacheComputesOneExactKeyOnce(t *testing.T) {
	const callerCount = 128
	cache := incrementalDecodedCache[string, *int]{}
	start := make(chan struct{})
	release := make(chan struct{})
	entered := make(chan struct{}, callerCount)
	results := make(chan *int, callerCount)
	errChan := make(chan error, callerCount)
	value := 42
	var computes atomic.Int64
	var group sync.WaitGroup
	for range callerCount {
		group.Add(1)
		go func() {
			defer group.Done()
			<-start
			entered <- struct{}{}
			result, err := cache.loadOrCompute("same", incrementalDecodedCacheStringHash("same"), func() (*int, error) {
				computes.Add(1)
				<-release
				return &value, nil
			})
			results <- result
			errChan <- err
		}()
	}
	close(start)
	for range callerCount {
		<-entered
	}
	close(release)
	group.Wait()
	close(results)
	close(errChan)
	for err := range errChan {
		require.NoError(t, err)
	}
	for result := range results {
		assert.Same(t, &value, result)
	}
	assert.Equal(t, int64(1), computes.Load())
}

func TestIncrementalDecodedCacheComputesDistinctKeysConcurrently(t *testing.T) {
	const keyCount = 32
	cache := incrementalDecodedCache[string, int]{}
	start := make(chan struct{})
	release := make(chan struct{})
	builders := make(chan struct{}, keyCount)
	errChan := make(chan error, keyCount)
	var group sync.WaitGroup
	for index := range keyCount {
		group.Add(1)
		go func() {
			defer group.Done()
			<-start
			key := fmt.Sprintf("key-%02d", index)
			value, err := cache.loadOrCompute(key, incrementalDecodedCacheStringHash(key), func() (int, error) {
				builders <- struct{}{}
				<-release
				return index, nil
			})
			if err == nil && value != index {
				err = fmt.Errorf("key %q returned %d", key, value)
			}
			errChan <- err
		}()
	}
	close(start)
	for range keyCount {
		<-builders
	}
	close(release)
	group.Wait()
	close(errChan)
	for err := range errChan {
		require.NoError(t, err)
	}
}

func TestIncrementalDecodedCacheRejectsPoisonAndRetriesFailures(t *testing.T) {
	for _, poison := range []struct {
		name   string
		mutate func(*incrementalDecodedCacheEntry[string, int])
	}{
		{name: "seal", mutate: func(entry *incrementalDecodedCacheEntry[string, int]) { entry.seal = nil }},
		{name: "key", mutate: func(entry *incrementalDecodedCacheEntry[string, int]) { entry.key = "other" }},
		{name: "hash", mutate: func(entry *incrementalDecodedCacheEntry[string, int]) { entry.hash++ }},
		{name: "ready", mutate: func(entry *incrementalDecodedCacheEntry[string, int]) { entry.ready = nil }},
	} {
		t.Run(poison.name, func(t *testing.T) {
			cache := incrementalDecodedCache[string, int]{}
			key := "key"
			hash := incrementalDecodedCacheStringHash(key)
			_, err := cache.loadOrCompute(key, hash, func() (int, error) { return 1, nil })
			require.NoError(t, err)
			shard := &cache.shards[hash&(incrementalDecodedCacheShardCount-1)]
			shard.mu.Lock()
			poison.mutate(shard.entries[key])
			shard.mu.Unlock()

			_, _, err = cache.load(key, hash)
			require.ErrorIs(t, err, errIncrementalDecodedCacheProvenance)
		})
	}

	t.Run("failed construction", func(t *testing.T) {
		cache := incrementalDecodedCache[string, int]{}
		key := "key"
		hash := incrementalDecodedCacheStringHash(key)
		failure := errors.New("failed")
		var computes atomic.Int64
		_, err := cache.loadOrCompute(key, hash, func() (int, error) {
			computes.Add(1)
			return 0, failure
		})
		require.ErrorIs(t, err, failure)
		value, err := cache.loadOrCompute(key, hash, func() (int, error) {
			computes.Add(1)
			return 2, nil
		})
		require.NoError(t, err)
		assert.Equal(t, 2, value)
		assert.Equal(t, int64(2), computes.Load())
	})
}

func TestIncrementalDecodedCacheConcurrentFailureUnblocksWaiters(t *testing.T) {
	const waiterCount = 128
	cache := incrementalDecodedCache[string, int]{}
	key := "key"
	hash := incrementalDecodedCacheStringHash(key)
	failure := errors.New("failed")
	started := make(chan struct{})
	release := make(chan struct{})
	ownerResult := make(chan error, 1)
	go func() {
		_, err := cache.loadOrCompute(key, hash, func() (int, error) {
			close(started)
			<-release
			return 0, failure
		})
		ownerResult <- err
	}()
	<-started
	shard := &cache.shards[hash&(incrementalDecodedCacheShardCount-1)]
	shard.mu.Lock()
	entry := shard.entries[key]
	shard.mu.Unlock()
	require.NotNil(t, entry)

	startWaiters := make(chan struct{})
	waiterStarted := make(chan struct{}, waiterCount)
	waiterResults := make(chan error, waiterCount)
	var group sync.WaitGroup
	for range waiterCount {
		group.Add(1)
		go func() {
			defer group.Done()
			<-startWaiters
			waiterStarted <- struct{}{}
			_, err := awaitIncrementalDecodedCacheEntry(entry, key, hash)
			waiterResults <- err
		}()
	}
	close(startWaiters)
	for range waiterCount {
		<-waiterStarted
	}
	close(release)
	group.Wait()
	close(waiterResults)
	require.ErrorIs(t, <-ownerResult, failure)
	for err := range waiterResults {
		require.ErrorIs(t, err, failure)
	}

	var retryComputes atomic.Int64
	value, err := cache.loadOrCompute(key, hash, func() (int, error) {
		retryComputes.Add(1)
		return 2, nil
	})
	require.NoError(t, err)
	assert.Equal(t, 2, value)
	assert.Equal(t, int64(1), retryComputes.Load())
}

func TestIncrementalDecodedCachePanicUnblocksWaitersAndRetries(t *testing.T) {
	const waiterCount = 128
	cache := incrementalDecodedCache[string, int]{}
	key := "key"
	hash := incrementalDecodedCacheStringHash(key)
	panicValue := errors.New("panic")
	started := make(chan struct{})
	release := make(chan struct{})
	ownerPanic := make(chan any, 1)
	go func() {
		defer func() {
			ownerPanic <- recover()
		}()
		_, _ = cache.loadOrCompute(key, hash, func() (int, error) {
			close(started)
			<-release
			panic(panicValue)
		})
	}()
	<-started
	shard := &cache.shards[hash&(incrementalDecodedCacheShardCount-1)]
	shard.mu.Lock()
	entry := shard.entries[key]
	shard.mu.Unlock()
	require.NotNil(t, entry)

	startWaiters := make(chan struct{})
	waiterStarted := make(chan struct{}, waiterCount)
	waiterResults := make(chan error, waiterCount)
	var group sync.WaitGroup
	for range waiterCount {
		group.Add(1)
		go func() {
			defer group.Done()
			<-startWaiters
			waiterStarted <- struct{}{}
			_, err := awaitIncrementalDecodedCacheEntry(entry, key, hash)
			waiterResults <- err
		}()
	}
	close(startWaiters)
	for range waiterCount {
		<-waiterStarted
	}
	close(release)
	group.Wait()
	close(waiterResults)
	assert.Same(t, panicValue, <-ownerPanic)
	for err := range waiterResults {
		require.ErrorIs(t, err, errIncrementalDecodedCacheBuildPanic)
	}

	var retryComputes atomic.Int64
	value, err := cache.loadOrCompute(key, hash, func() (int, error) {
		retryComputes.Add(1)
		return 2, nil
	})
	require.NoError(t, err)
	assert.Equal(t, 2, value)
	assert.Equal(t, int64(1), retryComputes.Load())
}

func TestDecodedPublicationInputsShareCertifiedValueConcurrently(t *testing.T) {
	const inputCount = 256
	renderSession := &incrementalRenderSession{}
	start := make(chan struct{})
	results := make(chan decodedPublicationResult, inputCount)
	var group sync.WaitGroup
	for index := range inputCount {
		group.Add(1)
		go func() {
			defer group.Done()
			key := incrementalSelectorInputKey("policies", "targets", fmt.Sprintf("service-%03d", index))
			reader := &decodedInputFallbackReader{input: incremental.Input{
				Key:      key,
				Revision: incremental.NewRevision("revision-1"),
				Found:    true,
				Value:    []byte(`{"name":"policy"}`),
			}}
			<-start
			value, certificate, found, err := renderSession.decodePublicationInput(reader, key)
			results <- decodedPublicationResult{
				value: value, certificate: certificate, found: found, err: err,
			}
		}()
	}
	close(start)
	group.Wait()
	close(results)
	var firstValue uintptr
	var firstCertificate *templating.IncrementalImmutableCertificate
	for result := range results {
		require.NoError(t, result.err)
		require.True(t, result.found)
		pointer := reflect.ValueOf(result.value).Pointer()
		if firstValue == 0 {
			firstValue = pointer
			firstCertificate = result.certificate
		}
		assert.Equal(t, firstValue, pointer)
		assert.Same(t, firstCertificate, result.certificate)
	}
	assert.Equal(t, inputCount, renderSession.decodedInputs.len())
	assert.Equal(t, 1, renderSession.decodedObjects.len())
}

type decodedPublicationResult struct {
	value       any
	certificate *templating.IncrementalImmutableCertificate
	found       bool
	err         error
}
