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

package httpstore

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"sync"

	"golang.org/x/sync/singleflight"

	purehttpstore "gitlab.com/haproxy-haptic/haptic/pkg/httpstore"
)

type transactionState uint8

const (
	transactionOpen transactionState = iota
	transactionCommitted
	transactionAborted
)

// InputTransaction owns authoritative HTTP candidates for one render.
type InputTransaction struct {
	component  *Component
	fetchGroup singleflight.Group
	mu         sync.Mutex
	state      transactionState
	sources    map[string]purehttpstore.SourceState
	results    map[string]*inputFetchResult
	candidates map[string]*purehttpstore.InitialCandidate
}

type inputFetchResult struct {
	content string
	err     error
}

func newInputTransaction(component *Component) *InputTransaction {
	return &InputTransaction{
		component:  component,
		sources:    make(map[string]purehttpstore.SourceState),
		results:    make(map[string]*inputFetchResult),
		candidates: make(map[string]*purehttpstore.InitialCandidate),
	}
}

func (t *InputTransaction) fetch(
	ctx context.Context,
	url string,
	state purehttpstore.SourceState,
) (string, error) {
	if err := t.pinSource(url, state); err != nil {
		return "", err
	}
	key := fmt.Sprintf("%s\x00%s\x00%d", url, state.Identity, state.Generation)
	if result, err := t.cachedResult(key); result != nil || err != nil {
		if err != nil {
			return "", err
		}
		return result.content, result.err
	}

	value, err, _ := t.fetchGroup.Do(key, func() (any, error) {
		if result, err := t.cachedResult(key); result != nil || err != nil {
			return result, err
		}

		content, candidate, fetchErr := t.component.store.PrepareInitial(ctx, url, state)
		result := &inputFetchResult{content: content, err: fetchErr}

		t.mu.Lock()
		defer t.mu.Unlock()
		if t.state != transactionOpen {
			return nil, errors.New("render input transaction is no longer open")
		}
		if previous, exists := t.results[key]; exists {
			return previous, nil
		}
		if candidate != nil {
			if previous, exists := t.candidates[url]; exists && previous != candidate {
				return nil, fmt.Errorf("HTTP source %s changed within one render", url)
			}
			t.candidates[url] = candidate
		}
		t.results[key] = result
		return result, nil
	})
	if err != nil {
		return "", err
	}
	result, ok := value.(*inputFetchResult)
	if !ok {
		return "", errors.New("HTTP candidate fetch returned an invalid result")
	}
	return result.content, result.err
}

func (t *InputTransaction) pinSource(url string, state purehttpstore.SourceState) error {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.state != transactionOpen {
		return errors.New("render input transaction is no longer open")
	}
	if previous, exists := t.sources[url]; exists {
		if previous.Identity != state.Identity || previous.Generation != state.Generation {
			return fmt.Errorf("HTTP source %s changed within one render", url)
		}
		return nil
	}
	t.sources[url] = state
	return nil
}

func (t *InputTransaction) cachedResult(key string) (*inputFetchResult, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.state != transactionOpen {
		return nil, errors.New("render input transaction is no longer open")
	}
	return t.results[key], nil
}

// Commit atomically accepts every candidate used by the validated render.
func (t *InputTransaction) Commit(ctx context.Context) error {
	t.mu.Lock()
	defer t.mu.Unlock()

	switch t.state {
	case transactionCommitted:
		return nil
	case transactionAborted:
		return errors.New("render input transaction was aborted")
	}
	if cause := context.Cause(ctx); cause != nil {
		return fmt.Errorf("committing render inputs: %w", cause)
	}

	urls := make([]string, 0, len(t.candidates))
	for url := range t.candidates {
		urls = append(urls, url)
	}
	slices.Sort(urls)
	candidates := make([]*purehttpstore.InitialCandidate, 0, len(urls))
	for _, url := range urls {
		candidates = append(candidates, t.candidates[url])
	}
	if err := t.component.CommitInitialCandidates(ctx, candidates); err != nil {
		return err
	}
	t.state = transactionCommitted
	t.sources = nil
	t.results = nil
	t.candidates = nil
	return nil
}

// Abort discards render-local candidates.
func (t *InputTransaction) Abort() {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.state != transactionOpen {
		return
	}
	t.state = transactionAborted
	t.sources = nil
	t.results = nil
	t.candidates = nil
}
