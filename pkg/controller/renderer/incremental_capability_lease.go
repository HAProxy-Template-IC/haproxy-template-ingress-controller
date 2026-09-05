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
	"sync"
	"sync/atomic"

	"gitlab.com/haproxy-haptic/haptic/pkg/controller/rendercontext"
	"gitlab.com/haproxy-haptic/haptic/pkg/incremental"
	"gitlab.com/haproxy-haptic/haptic/pkg/templating"
)

const (
	incrementalCapabilityLeasePrepared uint32 = iota
	incrementalCapabilityLeaseActive
	incrementalCapabilityLeaseRevoking
	incrementalCapabilityLeaseRevoked
)

type incrementalCapabilityLeaseContextKey struct{}
type incrementalCapabilityInvocationContextKey struct{}

type incrementalCapabilityAuthority struct {
	seal           *incrementalCapabilityAuthority
	nextGeneration atomic.Uint64
	resourceErrors *rendercontext.ResourceErrorCollector
}

type incrementalBatchReaderLease struct {
	seal       *incrementalBatchReaderLease
	authority  *incrementalCapabilityAuthority
	generation uint64
	state      atomic.Uint32
	gate       sync.RWMutex
	errMu      sync.Mutex
	violation  error

	ctx             context.Context
	invocationCtx   context.Context
	reader          incremental.Reader
	derivedResolver *incrementalQueryDerivedResourceResolver
	derived         *rendercontext.DerivedResourceView
}

func newIncrementalCapabilityAuthority(
	resourceErrors *rendercontext.ResourceErrorCollector,
) *incrementalCapabilityAuthority {
	authority := &incrementalCapabilityAuthority{resourceErrors: resourceErrors}
	authority.seal = authority
	return authority
}

func (a *incrementalCapabilityAuthority) newLease(
	ctx context.Context,
	reader incremental.Reader,
	session *incrementalRenderSession,
) (*incrementalBatchReaderLease, context.Context, error) {
	if a == nil || a.seal != a || reader == nil || session == nil {
		return nil, nil, errors.New("incremental component capability authority is unavailable")
	}
	lease := &incrementalBatchReaderLease{
		authority:  a,
		generation: a.nextGeneration.Add(1),
		reader:     reader,
	}
	lease.seal = lease
	lease.ctx = context.WithValue(ctx, incrementalCapabilityLeaseContextKey{}, lease)
	lease.ctx = templating.WithIncrementalExecutionLease(lease.ctx, lease)
	lease.invocationCtx = context.WithValue(
		lease.ctx,
		incrementalCapabilityInvocationContextKey{},
		lease,
	)
	lease.derivedResolver = &incrementalQueryDerivedResourceResolver{
		ctx: lease.ctx, reader: reader, session: session,
	}
	lease.derived = rendercontext.NewDerivedResourceViewWithResolver(lease.derivedResolver)
	lease.derived.Freeze()
	return lease, lease.ctx, nil
}

func (l *incrementalBatchReaderLease) activate() error {
	if l == nil || l.seal != l || l.authority == nil || l.authority.seal != l.authority ||
		l.reader == nil || l.derivedResolver == nil || l.derived == nil ||
		l.ctx.Value(incrementalCapabilityLeaseContextKey{}) != l ||
		l.invocationCtx.Value(incrementalCapabilityInvocationContextKey{}) != l {
		return errors.New("incremental component capability lease has invalid provenance")
	}
	if !l.state.CompareAndSwap(incrementalCapabilityLeasePrepared, incrementalCapabilityLeaseActive) {
		return fmt.Errorf("incremental component capability lease generation %d is not prepared", l.generation)
	}
	return nil
}

func (l *incrementalBatchReaderLease) revoke() {
	if l == nil || !l.state.CompareAndSwap(
		incrementalCapabilityLeaseActive,
		incrementalCapabilityLeaseRevoking,
	) {
		panic("incremental component capability lease activation was corrupted")
	}
	l.gate.Lock()
	l.state.Store(incrementalCapabilityLeaseRevoked)
	l.gate.Unlock()
}

func (l *incrementalBatchReaderLease) begin(
	ctx context.Context,
	operation string,
) (context.Context, func(), error) {
	if l == nil || l.seal != l || l.authority == nil || l.authority.seal != l.authority ||
		ctx == nil || ctx.Value(incrementalCapabilityLeaseContextKey{}) != l {
		err := fmt.Errorf("%s has an invalid incremental component capability lease", operation)
		if l != nil {
			l.fail(err)
		}
		return nil, nil, err
	}
	if cause := context.Cause(ctx); cause != nil {
		l.fail(cause)
		return nil, nil, cause
	}
	if l.state.Load() != incrementalCapabilityLeaseActive {
		err := fmt.Errorf("%s used inactive incremental component capability generation %d", operation, l.generation)
		l.fail(err)
		return nil, nil, err
	}
	l.gate.RLock()
	if l.state.Load() != incrementalCapabilityLeaseActive {
		l.gate.RUnlock()
		err := fmt.Errorf("%s used inactive incremental component capability generation %d", operation, l.generation)
		l.fail(err)
		return nil, nil, err
	}
	return l.invocationCtx, l.gate.RUnlock, nil
}

func (l *incrementalBatchReaderLease) validateInvocation(ctx context.Context) error {
	if l == nil || l.seal != l || ctx == nil ||
		ctx.Value(incrementalCapabilityLeaseContextKey{}) != l ||
		ctx.Value(incrementalCapabilityInvocationContextKey{}) != l {
		return errors.New("incremental component resource invocation has invalid provenance")
	}
	state := l.state.Load()
	if state != incrementalCapabilityLeaseActive && state != incrementalCapabilityLeaseRevoking {
		return fmt.Errorf("incremental component resource invocation generation %d is inactive", l.generation)
	}
	return nil
}

func (l *incrementalBatchReaderLease) executionContext() context.Context {
	if l == nil {
		return nil
	}
	return l.ctx
}

func (l *incrementalBatchReaderLease) BeginIncrementalExecution(
	ctx context.Context,
	operation string,
) (func(), error) {
	_, release, err := l.begin(ctx, operation)
	return release, err
}

func (l *incrementalBatchReaderLease) BeforeIncrementalNativeCall(ctx context.Context) error {
	if l == nil || l.seal != l || l.authority == nil || l.authority.seal != l.authority ||
		ctx == nil || ctx.Value(incrementalCapabilityLeaseContextKey{}) != l {
		err := errors.New("native call has an invalid incremental component capability lease")
		if l != nil {
			l.fail(err)
		}
		return err
	}
	if cause := context.Cause(ctx); cause != nil {
		l.fail(cause)
		return cause
	}
	if l.state.Load() != incrementalCapabilityLeaseActive {
		err := fmt.Errorf(
			"native call used inactive incremental component capability generation %d",
			l.generation,
		)
		l.fail(err)
		return err
	}
	return nil
}

// ValidateIncrementalResourceInvocation binds one shallow resource facade to
// the exact component execution that created it.
func (l *incrementalBatchReaderLease) ValidateIncrementalResourceInvocation(ctx context.Context) error {
	if l == nil || l.seal != l || ctx == nil ||
		ctx.Value(incrementalCapabilityLeaseContextKey{}) != l ||
		l.state.Load() != incrementalCapabilityLeaseActive {
		generation := uint64(0)
		if l != nil {
			generation = l.generation
		}
		err := fmt.Errorf("resource capability used outside incremental component generation %d", generation)
		if l != nil {
			l.fail(err)
		}
		return err
	}
	if cause := context.Cause(ctx); cause != nil {
		l.fail(cause)
		return cause
	}
	return nil
}

func (l *incrementalBatchReaderLease) beginResourceInvocation(
	ctx context.Context,
) (context.Context, func(), error) {
	if l == nil || l.seal != l || l.authority == nil || l.authority.seal != l.authority ||
		ctx == nil || ctx.Value(incrementalCapabilityLeaseContextKey{}) != l {
		generation := uint64(0)
		if l != nil {
			generation = l.generation
		}
		err := fmt.Errorf("resource capability used outside incremental component generation %d", generation)
		if l != nil {
			l.fail(err)
		}
		return nil, nil, err
	}
	if cause := context.Cause(ctx); cause != nil {
		l.fail(cause)
		return nil, nil, cause
	}
	if l.state.Load() != incrementalCapabilityLeaseActive {
		err := fmt.Errorf("resource capability used outside incremental component generation %d", l.generation)
		l.fail(err)
		return nil, nil, err
	}
	l.gate.RLock()
	if l.state.Load() != incrementalCapabilityLeaseActive {
		l.gate.RUnlock()
		err := fmt.Errorf("resource capability used outside incremental component generation %d", l.generation)
		l.fail(err)
		return nil, nil, err
	}
	return l.invocationCtx, l.gate.RUnlock, nil
}

func (l *incrementalBatchReaderLease) fail(err error) {
	if l == nil || err == nil {
		return
	}
	l.errMu.Lock()
	if l.violation == nil {
		l.violation = err
	}
	l.errMu.Unlock()
	if l.authority != nil && l.authority.resourceErrors != nil {
		l.authority.resourceErrors.Record(err)
	}
}

func (l *incrementalBatchReaderLease) err() error {
	if l == nil {
		return nil
	}
	l.errMu.Lock()
	defer l.errMu.Unlock()
	return l.violation
}

func (l *incrementalBatchReaderLease) publicationError() error {
	if l == nil {
		return nil
	}
	if l.seal != l || l.authority == nil || l.authority.seal != l.authority {
		return errors.New("incremental component capability lease has invalid provenance")
	}
	if state := l.state.Load(); state != incrementalCapabilityLeaseRevoked {
		return fmt.Errorf(
			"incremental component capability lease generation %d is not revoked (state %d)",
			l.generation,
			state,
		)
	}
	return l.err()
}

func beginIncrementalCapability(
	lease *incrementalBatchReaderLease,
	operation string,
) (func(), error) {
	if lease == nil {
		return func() {}, nil
	}
	_, release, err := lease.begin(lease.executionContext(), operation)
	return release, err
}
