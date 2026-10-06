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
	"context"
	"fmt"
	"sync"
	"sync/atomic"
	"time"

	"gitlab.com/haproxy-haptic/haptic/pkg/controller/rendercontext"
)

// coldRenderGate admits one reconcile render at a time until the service has
// published its first graph, and holds it until that render's graph is
// published or its transaction is dropped. A cold render costs the whole
// resource set; two at once double the memory that is short when a cold
// render is slow, and the later one would only repeat work the first is about
// to commit (#285).
type coldRenderGate struct {
	slot  chan struct{}
	warm  func() bool
	ready atomic.Bool

	mu      sync.Mutex
	running *coldRenderRun
}

type coldRenderRun struct {
	gate    *coldRenderGate
	started time.Time
	done    chan struct{}
	once    sync.Once
}

func newColdRenderGate(warm func() bool) *coldRenderGate {
	return &coldRenderGate{slot: make(chan struct{}, 1), warm: warm}
}

// observeWarm latches ready once the service holds a graph. It reads the
// render state under its lock, so probes use ready instead.
func (g *coldRenderGate) observeWarm() bool {
	if g.ready.Load() {
		return true
	}
	if !g.warm() {
		return false
	}
	g.ready.Store(true)
	return true
}

func (g *coldRenderGate) acquire(ctx context.Context) (*coldRenderRun, error) {
	select {
	case g.slot <- struct{}{}:
	case <-ctx.Done():
		return nil, fmt.Errorf("waiting for the cold render in flight: %w", context.Cause(ctx))
	}
	run := &coldRenderRun{gate: g, started: time.Now(), done: make(chan struct{})}
	g.mu.Lock()
	g.running = run
	g.mu.Unlock()
	return run, nil
}

func (r *coldRenderRun) release() {
	if r == nil {
		return
	}
	r.once.Do(func() {
		r.gate.mu.Lock()
		r.gate.running = nil
		r.gate.mu.Unlock()
		close(r.done)
		<-r.gate.slot
	})
}

// await waits for the cold render in flight, if any.
func (g *coldRenderGate) await(ctx context.Context) error {
	g.mu.Lock()
	run := g.running
	g.mu.Unlock()
	if run == nil {
		return nil
	}
	select {
	case <-run.done:
		return nil
	case <-ctx.Done():
		return &coldGraphPendingError{running: time.Since(run.started)}
	}
}

// coldGraphPendingError denies an admission request that arrived before the
// replica could validate. It deliberately doesn't wrap the context error: the
// pipeline replaces a cause-wrapping error with a bare "canceled" message.
type coldGraphPendingError struct {
	running time.Duration
}

func (e *coldGraphPendingError) Error() string {
	return fmt.Sprintf("This controller replica is still building its first render (%s) and can't validate yet. "+
		"Changes are denied until it finishes. "+
		"Retry shortly; if this persists, raise the controller's memory and CPU limits.", e.running.Round(time.Second))
}

// FirstGraphPublished reports whether this service has published a render
// graph, after which no render pays the full cold cost again. Lock-free for
// probes. A service that doesn't run cold renders to completion reports true.
func (s *RenderService) FirstGraphPublished() bool {
	return s.coldRenders == nil || s.coldRenders.ready.Load()
}

// coldClaim is a render's place at the gate; the zero value renders under the
// render timeout.
type coldClaim struct {
	run *coldRenderRun
}

func (c coldClaim) cold() bool { return c.run != nil }

// claimColdRender decides how a render meets the gate. A reconcile render
// without a published graph takes the gate and runs without the render
// timeout; one that finds a graph after waiting renders warm under it. An
// admission render waits for a cold render in flight, never takes the gate,
// and keeps its own deadline.
func (s *RenderService) claimColdRender(ctx context.Context, mode rendercontext.RenderMode) (coldClaim, error) {
	gate := s.coldRenders
	if gate == nil || gate.ready.Load() {
		return coldClaim{}, nil
	}
	if mode == rendercontext.RenderModeAdmission {
		return coldClaim{}, gate.await(ctx)
	}
	run, err := gate.acquire(ctx)
	if err != nil {
		return coldClaim{}, err
	}
	if gate.observeWarm() {
		run.release()
		return coldClaim{}, nil
	}
	return coldClaim{run: run}, nil
}

// holdUntilSettled keeps the gate until the pipeline commits or drops the
// render; a cold commit returns once its graph is published.
func (c coldClaim) holdUntilSettled(result *RenderResult, err error) (*RenderResult, error) {
	r := c.run
	if r == nil {
		return result, err
	}
	if err != nil || result == nil || result.InputTransaction == nil {
		r.release()
		return result, err
	}
	result.InputTransaction = &coldRenderTransaction{RenderInputTransaction: result.InputTransaction, run: r}
	return result, nil
}

type coldRenderTransaction struct {
	RenderInputTransaction
	run *coldRenderRun
}

func (t *coldRenderTransaction) Commit(ctx context.Context) error {
	defer t.run.release()
	err := t.RenderInputTransaction.Commit(ctx)
	if err == nil {
		t.run.gate.observeWarm()
	}
	return err
}

func (t *coldRenderTransaction) Abort() {
	defer t.run.release()
	t.RenderInputTransaction.Abort()
}
