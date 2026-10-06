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
	"maps"
	"runtime"
	"sync"

	purehttpstore "gitlab.com/haproxy-haptic/haptic/pkg/httpstore"
)

// ErrCandidatePending discards a render until its HTTP candidates have been fetched.
var ErrCandidatePending = errors.New("HTTP candidates are being fetched outside the render")

type deferredCandidatesKey struct{}

// DeferredCandidates retains only source reads while the pipeline releases its render graph.
type DeferredCandidates struct {
	ctx       context.Context
	cancel    context.CancelFunc
	mu        sync.Mutex
	pending   map[string]*deferredCandidate
	requested bool
	workers   sync.WaitGroup
}

type deferredCandidate struct {
	input     retryInput
	component *Component
	ctx       context.Context
	done      chan struct{}
	cancel    context.CancelFunc
	started   bool
}

// WithDeferredCandidates moves initial HTTP I/O outside a pipeline's render attempts.
func WithDeferredCandidates(ctx context.Context) (context.Context, *DeferredCandidates) {
	ctx, cancel := context.WithCancel(ctx)
	candidates := &DeferredCandidates{ctx: ctx, cancel: cancel, pending: make(map[string]*deferredCandidate)}
	return context.WithValue(ctx, deferredCandidatesKey{}, candidates), candidates
}

func deferredCandidatesFromContext(ctx context.Context) *DeferredCandidates {
	candidates, _ := ctx.Value(deferredCandidatesKey{}).(*DeferredCandidates)
	return candidates
}

func (d *DeferredCandidates) schedule(component *Component, source *purehttpstore.StagedSource) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.requested = true
	if previous := d.pending[source.URL()]; previous != nil {
		if previous.input.source.Descriptor() == source.Descriptor() && component.verifyStagedSource(previous.input.source) {
			return
		}
		previous.cancel()
	}
	ctx, cancel := context.WithCancel(d.ctx)
	d.pending[source.URL()] = &deferredCandidate{
		input: retryInput{source: source}, component: component,
		ctx: ctx, done: make(chan struct{}), cancel: cancel,
	}
}

func (p *deferredCandidate) fetch() {
	defer close(p.done)
	defer p.cancel()
	source := p.input.source
	snapshot, candidate, err := p.component.store.PrepareStagedSnapshot(p.ctx, source)
	if snapshot.URL == "" {
		snapshot.URL = source.URL()
		snapshot.Descriptor = source.Descriptor()
	}
	p.input.result = inputFetchResult{snapshot: snapshot, err: err}
	p.input.candidate = candidate
}

func (d *DeferredCandidates) retrySeed(existing *InputRetrySeed) *InputRetrySeed {
	if d == nil {
		return existing
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	seed := &InputRetrySeed{inputs: make(map[string]retryInput, len(d.pending))}
	for url, pending := range d.pending {
		select {
		case <-pending.done:
			seed.inputs[url] = pending.input
		default:
		}
	}
	if existing != nil {
		maps.Copy(seed.inputs, existing.inputs)
	}
	return seed
}

// Await waits only after the render and its cold gate have been released.
func (d *DeferredCandidates) Await(ctx context.Context) (bool, error) {
	d.mu.Lock()
	if !d.requested {
		d.mu.Unlock()
		return false, nil
	}
	if ctx.Err() != nil {
		d.mu.Unlock()
		return false, context.Cause(ctx)
	}
	d.requested = false
	pending := make([]*deferredCandidate, 0, len(d.pending))
	for _, candidate := range d.pending {
		pending = append(pending, candidate)
	}
	d.startFetchesLocked(pending)
	d.mu.Unlock()
	for _, candidate := range pending {
		select {
		case <-candidate.done:
		case <-ctx.Done():
			return false, context.Cause(ctx)
		}
	}
	return true, nil
}

func (d *DeferredCandidates) startFetchesLocked(pending []*deferredCandidate) {
	queue := make(chan *deferredCandidate, len(pending))
	for _, candidate := range pending {
		if !candidate.started {
			candidate.started = true
			queue <- candidate
		}
	}
	close(queue)
	for range min(runtime.GOMAXPROCS(0), len(queue)) {
		d.workers.Go(func() {
			for candidate := range queue {
				candidate.fetch()
			}
		})
	}
}

// Discard drops staged reads before retrying a failed publication fence.
func (d *DeferredCandidates) Discard() {
	d.mu.Lock()
	defer d.mu.Unlock()
	for _, candidate := range d.pending {
		candidate.cancel()
	}
	clear(d.pending)
}

// Close cancels and drains every fetch owned by this pipeline execution.
func (d *DeferredCandidates) Close() {
	d.cancel()
	d.workers.Wait()
}
