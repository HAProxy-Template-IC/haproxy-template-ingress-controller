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
	"errors"
	"fmt"

	purehttpstore "gitlab.com/haproxy-haptic/haptic/pkg/httpstore"
)

// InputRetrySeed carries immutable HTTP reads across one internal render retry.
type InputRetrySeed struct {
	inputs map[string]retryInput
}

type retryInput struct {
	source    *purehttpstore.StagedSource
	result    inputFetchResult
	candidate *purehttpstore.InitialCandidate
}

// RetrySeed snapshots fetched inputs without transferring transaction state.
func (t *InputTransaction) RetrySeed() *InputRetrySeed {
	if t == nil {
		return nil
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.state != transactionOpen || len(t.results) == 0 {
		return nil
	}
	seed := &InputRetrySeed{inputs: make(map[string]retryInput, len(t.results))}
	for url, result := range t.results {
		source := t.sources[url]
		if source == nil || result == nil {
			continue
		}
		candidate := t.candidates[url]
		if result.snapshot.Token.Kind() == purehttpstore.SnapshotInitialCandidate && candidate == nil {
			continue
		}
		seed.inputs[url] = retryInput{
			source:    source,
			result:    *result,
			candidate: candidate,
		}
	}
	if len(seed.inputs) == 0 {
		return nil
	}
	return seed
}

func (t *InputTransaction) adoptRetryInput(
	url string,
	descriptor purehttpstore.SourceDescriptor,
) (*inputFetchResult, bool, error) {
	t.mu.Lock()
	if t.state != transactionOpen {
		t.mu.Unlock()
		return nil, false, errors.New("render input transaction is no longer open")
	}
	if t.results[url] != nil {
		t.mu.Unlock()
		return nil, false, nil
	}
	if t.retrySeed == nil {
		t.mu.Unlock()
		return nil, false, nil
	}
	seed, exists := t.retrySeed.inputs[url]
	if !exists || seed.source == nil || seed.source.Descriptor() != descriptor {
		t.mu.Unlock()
		return nil, false, nil
	}
	t.mu.Unlock()
	if !t.component.verifyStagedSource(seed.source) {
		return nil, false, nil
	}

	t.mu.Lock()
	defer t.mu.Unlock()
	if t.state != transactionOpen {
		return nil, false, errors.New("render input transaction is no longer open")
	}
	if result := t.results[url]; result != nil {
		return result, true, nil
	}
	if previous := t.sources[url]; previous != nil && previous.Descriptor() != descriptor {
		return nil, false, fmt.Errorf("HTTP source %s changed within one render", url)
	}
	result := seed.result
	t.sources[url] = seed.source
	t.results[url] = &result
	if seed.candidate != nil {
		t.candidates[url] = seed.candidate
	}
	return &result, true, nil
}
