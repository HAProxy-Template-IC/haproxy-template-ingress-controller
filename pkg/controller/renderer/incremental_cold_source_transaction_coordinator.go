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
	"context"
	"errors"
	"fmt"
	"math"
	"runtime"

	"golang.org/x/sync/errgroup"

	"gitlab.com/haproxy-haptic/haptic/pkg/incremental"
	"gitlab.com/haproxy-haptic/haptic/pkg/templating"
)

func (r *incrementalRenderSession) evaluateColdComponentSourceTransactionGraphWaves(
	ctx context.Context,
	renderer templating.IncrementalComponentSourceTransactionsRenderer,
	schedule *incrementalColdCarrierGraphSchedule,
) error {
	if r == nil || r.graphSession == nil || renderer == nil || schedule == nil || schedule.seal != schedule ||
		len(schedule.keys) == 0 || len(schedule.queryIndexes) != len(schedule.keys) {
		return errors.New("incremental cold source transaction graph execution is incomplete")
	}
	completedRoots := make([]incremental.ExactValueRoot, len(schedule.keys))
	results, err := r.graphSession.EvaluateAllColdExactBatch(
		ctx,
		func(batchCtx context.Context, batch incremental.ColdExactBatch) error {
			return r.runColdSourceTransactionBatch(batchCtx, renderer, schedule, batch, completedRoots)
		},
		schedule.keys...,
	)
	if err != nil {
		return err
	}
	if err := verifyColdSourceTransactionResults(results, schedule.keys, completedRoots); err != nil {
		return err
	}
	return context.Cause(ctx)
}

func (r *incrementalRenderSession) runColdSourceTransactionBatch(
	batchCtx context.Context,
	renderer templating.IncrementalComponentSourceTransactionsRenderer,
	schedule *incrementalColdCarrierGraphSchedule,
	batch incremental.ColdExactBatch,
	completedRoots []incremental.ExactValueRoot,
) error {
	plan, planErr := r.planColdComponentCarriers(batch)
	if planErr != nil {
		return planErr
	}
	if plan == nil || plan.logicalQueries != batch.Len() || batch.Len() != len(schedule.keys) {
		return errors.New("incremental cold source transaction runtime plan is incomplete")
	}
	for index, key := range schedule.keys {
		if batch.Query(index).Key() != key {
			return errors.New("incremental cold source transaction runtime query order changed")
		}
	}
	waves, waveErr := r.planIncrementalColdCarrierWaves(
		plan,
		schedule,
		incrementalColdCarrierWorkerLimit(runtime.GOMAXPROCS(0)),
	)
	if waveErr != nil {
		return fmt.Errorf("planning incremental cold source transaction waves: %w", waveErr)
	}
	coordinator, coordinatorErr := newIncrementalColdCarrierWaveCoordinator(
		batchCtx, r, batch, schedule, waves,
	)
	if coordinatorErr != nil {
		return coordinatorErr
	}
	coordinator.sourceTransactions = true
	defer coordinator.close()
	if runErr := coordinator.runSourceTransactions(renderer); runErr != nil {
		return runErr
	}
	copy(completedRoots, coordinator.completedRoots)
	return nil
}

func verifyColdSourceTransactionResults(
	results []incremental.ExactResult,
	keys []incremental.QueryKey,
	completedRoots []incremental.ExactValueRoot,
) error {
	if len(results) != len(keys) {
		return errors.New("incremental cold source transaction graph returned an incomplete result set")
	}
	for index := range results {
		if results[index].Key != keys[index] {
			return errors.New("incremental cold source transaction graph returned results out of order")
		}
		same, sameErr := results[index].Value.SameRoot(completedRoots[index])
		if sameErr != nil || !same {
			return errors.New("incremental cold source transaction graph returned a different exact root")
		}
	}
	return nil
}

func (c *incrementalColdCarrierWaveCoordinator) runSourceTransactions(
	renderer templating.IncrementalComponentSourceTransactionsRenderer,
) error {
	if !c.valid() || renderer == nil || !c.sourceTransactions {
		return errors.New("incremental cold source transaction coordinator has invalid provenance")
	}
	lifecycles, inputs, err := c.prepareSourceTransactionWorkers()
	if err != nil {
		return err
	}
	renderErr := c.runSourceTransactionWorkers(renderer, lifecycles, inputs)
	c.mu.Lock()
	terminal := c.terminal
	complete := c.wave == len(c.schedule.waves)
	c.mu.Unlock()
	if terminal != nil {
		return terminal
	}
	if renderErr != nil {
		return renderErr
	}
	if !complete {
		return errors.New("incremental cold source transaction renderer stopped before the final barrier")
	}
	for batchIndex, completed := range c.completedQueries {
		if !completed {
			return fmt.Errorf("incremental cold source transaction graph omitted batch item %d", batchIndex)
		}
	}
	return nil
}

func (c *incrementalColdCarrierWaveCoordinator) prepareSourceTransactionWorkers() (
	lifecycles []*incrementalColdCarrierWavesLifecycle,
	inputs []templating.IncrementalComponentSourceTransactionsInput,
	err error,
) {
	lifecycles = make([]*incrementalColdCarrierWavesLifecycle, len(c.schedule.workers))
	inputs = make([]templating.IncrementalComponentSourceTransactionsInput, len(c.schedule.workers))
	for workerIndex := range c.schedule.workers {
		lifecycle, lifecycleErr := newIncrementalColdCarrierWavesLifecycle(c, workerIndex)
		if lifecycleErr != nil {
			c.fail(lifecycleErr)
			return nil, nil, lifecycleErr
		}
		waves, wavesErr := c.session.coldSourceTransactionWaves(&c.schedule.workers[workerIndex])
		if wavesErr != nil {
			c.fail(wavesErr)
			return nil, nil, wavesErr
		}
		lifecycles[workerIndex] = lifecycle
		inputs[workerIndex] = templating.IncrementalComponentSourceTransactionsInput{
			Waves: waves, Lifecycle: lifecycle,
		}
	}
	return lifecycles, inputs, nil
}

func (c *incrementalColdCarrierWaveCoordinator) runSourceTransactionWorkers(
	renderer templating.IncrementalComponentSourceTransactionsRenderer,
	lifecycles []*incrementalColdCarrierWavesLifecycle,
	inputs []templating.IncrementalComponentSourceTransactionsInput,
) error {
	var renderGroup errgroup.Group
	for workerIndex := range lifecycles {
		lifecycle := lifecycles[workerIndex]
		input := inputs[workerIndex]
		renderGroup.Go(func() error {
			err := renderIncrementalColdSourceTransactions(c.ctx, renderer, input)
			if err != nil {
				batchIndex, mapped := lifecycle.remapError(err)
				lifecycle.fail(batchIndex, mapped)
				return mapped
			}
			if err := lifecycle.validateComplete(); err != nil {
				lifecycle.fail(math.MaxInt, err)
				return err
			}
			return nil
		})
	}
	return renderGroup.Wait()
}

func renderIncrementalColdSourceTransactions(
	ctx context.Context,
	renderer templating.IncrementalComponentSourceTransactionsRenderer,
	input templating.IncrementalComponentSourceTransactionsInput,
) (err error) {
	defer func() {
		if recovered := recover(); recovered != nil {
			err = fmt.Errorf("incremental cold source transaction renderer panic: %v", recovered)
		}
	}()
	return renderer.RenderIncrementalComponentSourceTransactions(ctx, input)
}

func (c *incrementalColdCarrierWaveCoordinator) loadSourceTransactionWave(
	worker int,
	wave int,
) (templating.IncrementalComponentSourceTransactionBatch, *incrementalColdCarrierLifecycle, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if !c.valid() || !c.sourceTransactions || c.closed || worker < 0 || worker >= len(c.schedule.workers) ||
		wave < 0 || wave >= len(c.schedule.waves) {
		return templating.IncrementalComponentSourceTransactionBatch{}, nil,
			errors.New("incremental cold source transaction wave load has invalid provenance")
	}
	if c.terminal != nil {
		return templating.IncrementalComponentSourceTransactionBatch{}, nil, c.terminal
	}
	if wave != c.wave {
		return templating.IncrementalComponentSourceTransactionBatch{}, nil,
			fmt.Errorf("incremental cold source transaction wave %d is unavailable", wave)
	}
	if c.prepared == nil {
		prepared, err := c.prepareWaveLocked(wave)
		if err != nil {
			c.terminal = err
			c.abortPreparedLocked(err)
			c.cond.Broadcast()
			return templating.IncrementalComponentSourceTransactionBatch{}, nil, err
		}
		c.prepared = prepared
	}
	render := c.prepared.sourceTransactions[worker]
	work := &c.prepared.works[worker]
	if len(c.schedule.workers[worker].waves[wave].lanes) == 0 {
		if render != nil || work.lifecycle != nil {
			return templating.IncrementalComponentSourceTransactionBatch{}, nil,
				errors.New("incremental cold source transaction empty wave has state")
		}
		return templating.IncrementalComponentSourceTransactionBatch{
			Bindings: map[string]any{}, Contexts: []context.Context{}, ChildContexts: []context.Context{},
		}, nil, nil
	}
	if render == nil || render.execution == nil || work.lifecycle == nil {
		return templating.IncrementalComponentSourceTransactionBatch{}, nil,
			errors.New("incremental cold source transaction wave load is incomplete")
	}
	return render.batch, work.lifecycle, nil
}

func (l *incrementalColdCarrierWavesLifecycle) LoadSourceTransactionWave(
	ctx context.Context,
	wave int,
) (templating.IncrementalComponentSourceTransactionBatch, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if ctx == nil || !l.validLocked() || !l.coordinator.sourceTransactions || l.terminal != nil ||
		l.loaded || l.sealing || wave != l.wave || wave >= len(l.offsets)-1 {
		return templating.IncrementalComponentSourceTransactionBatch{},
			errors.New("incremental cold source transaction lifecycle cannot load this wave")
	}
	if cause := context.Cause(ctx); cause != nil {
		l.terminal = cause
		return templating.IncrementalComponentSourceTransactionBatch{}, cause
	}
	batch, inner, err := l.coordinator.loadSourceTransactionWave(l.worker, wave)
	if err != nil {
		l.terminal = err
		return templating.IncrementalComponentSourceTransactionBatch{}, err
	}
	wantItems := l.offsets[wave+1] - l.offsets[wave]
	if (wantItems == 0) != (inner == nil) ||
		(wantItems == 0 && (len(batch.Contexts) != 0 || len(batch.ChildContexts) != 0 || len(batch.Bindings) != 0)) ||
		(wantItems > 0 && (len(batch.Contexts) == 0 || len(batch.ChildContexts) != wantItems)) {
		err := fmt.Errorf("incremental cold source transaction wave %d loaded an invalid shape", wave)
		l.terminal = err
		l.coordinator.workerFailed(l.worker, wave, math.MaxInt, err)
		return templating.IncrementalComponentSourceTransactionBatch{}, err
	}
	l.inner = inner
	l.loaded = true
	return batch, nil
}
