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
	"log/slog"
	"sync"
	"sync/atomic"
	"time"

	"gitlab.com/haproxy-haptic/haptic/pkg/httpstore"
	"gitlab.com/haproxy-haptic/haptic/pkg/incremental"
)

var errIncrementalCacheSuperseded = errors.New("incremental cache build was superseded")

// maxColdCacheBuildWait is how long the commit that started a cold graph waits
// for it. Measured build cost is 4ms at 100 routes and 127ms at 3000, so this
// covers construction with room to spare while staying far inside the budget a
// render is allowed to spend before its successor is held up.
//
// The wait pays for itself immediately: a warm render is 2ms against 878ms cold
// at 3000 routes. Without it the next render commits first, the build's output
// reservation is no longer the committed one, and it is discarded — measured on
// a busy e2e job as zero completed builds and no render under 458ms.
const maxColdCacheBuildWait = 300 * time.Millisecond

// awaitColdCacheBuild waits for the cold graph the caller just handed off.
//
// Callers must have released the build's publication first: the build blocks on
// that, so waiting before it deadlocks.
func awaitColdCacheBuild(ctx context.Context, build *incrementalCacheBuild, wait time.Duration) {
	if build == nil || build.ready == nil {
		return
	}
	timer := time.NewTimer(wait)
	defer timer.Stop()
	select {
	case <-build.ready.done:
	case <-timer.C:
	case <-ctx.Done():
	}
}

type incrementalCacheBuilder struct {
	mu                sync.Mutex
	desiredGeneration uint64
	closed            bool
	running           bool
	current           *incrementalCacheBuild
	pending           *incrementalCacheBuild
	lastBuildMs       atomic.Int64
	wg                sync.WaitGroup
	hooks             incrementalCacheBuilderHooks
}

// LastBuildMs is what the most recent completed cache build cost. Zero until one
// completes, which is itself the signal: a cache that never builds leaves every
// render cold.
func (b *incrementalCacheBuilder) LastBuildMs() int64 {
	if b == nil {
		return 0
	}
	return b.lastBuildMs.Load()
}

type incrementalCacheBuilderHooks struct {
	afterHTTPPrepare        func(context.Context, uint64)
	afterDependencyPrepare  func(context.Context, uint64)
	beforeColdPublication   func(context.Context, uint64, incrementalColdPublicationStage)
	beforeOutputReservation func(context.Context, uint64)
	beforePrepare           func(context.Context, uint64)
	afterPrepare            func(context.Context, uint64)
	beforeRendererPublish   func(context.Context, uint64)
}

type incrementalColdPublicationStage string

const (
	incrementalColdPublicationHTTP      incrementalColdPublicationStage = "HTTP inputs"
	incrementalColdPublicationOwnership incrementalColdPublicationStage = "HTTP ownership"
	incrementalColdPublicationState     incrementalColdPublicationStage = "renderer state"
	incrementalColdPublicationOutput    incrementalColdPublicationStage = "authoritative output"
)

// IncrementalCacheBuildObserver observes optional cold-cache construction outside render latency.
type IncrementalCacheBuildObserver interface {
	IncrementalCacheBuildStarted(context.Context, IncrementalCacheBuildIdentity)
	IncrementalCacheBuildCompleted(IncrementalCacheBuildIdentity, error)
}

// IncrementalCacheBuildIdentity authenticates one cold-cache build and its output reservation.
type IncrementalCacheBuildIdentity struct {
	identity *incrementalCacheBuildIdentity
}

type incrementalCacheBuildAuthority struct {
	seal  *incrementalCacheBuildAuthority
	state *incrementalRenderState
}

type incrementalCacheBuildIdentity struct {
	seal        *incrementalCacheBuildIdentity
	authority   *incrementalCacheBuildAuthority
	reservation *renderOutputReservation
	generation  uint64
}

type incrementalColdCacheDraftState uint8

const (
	incrementalColdCacheDraftOpen incrementalColdCacheDraftState = iota + 1
	incrementalColdCacheDraftSealed
	incrementalColdCacheDraftTransferred
	incrementalColdCacheDraftMaterialized
	incrementalColdCacheDraftRevoked
)

type incrementalColdCacheDraftMaterializerIdentity struct {
	seal *incrementalColdCacheDraftMaterializerIdentity
}

type incrementalColdCacheDraftMaterializer interface {
	incrementalColdCacheDraftMaterializerIdentity() *incrementalColdCacheDraftMaterializerIdentity
	validateIncrementalColdCacheDraftMaterializer() error
	materializeIncrementalColdCacheDraft(context.Context, *incrementalRenderSession) error
}

type incrementalColdCacheDraftAuthentication struct {
	seal                *incrementalColdCacheDraftAuthentication
	draft               *incrementalColdCacheDraft
	state               *incrementalRenderState
	session             *incrementalRenderSession
	graphSession        *incremental.Session
	materializer        *incrementalColdCacheDraftMaterializerIdentity
	outputGeneration    uint64
	baseGraphGeneration uint64
}

type incrementalColdCacheDraftTransferAuthentication struct {
	seal  *incrementalColdCacheDraftTransferAuthentication
	draft *incrementalColdCacheDraft
	owner *incrementalCacheBuild
}

type incrementalColdCacheDraft struct {
	seal                *incrementalColdCacheDraft
	state               *incrementalRenderState
	session             *incrementalRenderSession
	graphSession        *incremental.Session
	materializer        incrementalColdCacheDraftMaterializer
	outputGeneration    uint64
	baseGraphGeneration uint64
	authentication      *incrementalColdCacheDraftAuthentication
	transfer            *incrementalColdCacheDraftTransferAuthentication
	owner               *incrementalCacheBuild

	mu            sync.Mutex
	lifecycle     incrementalColdCacheDraftState
	materializing bool
}

func newIncrementalColdCacheDraft(
	session *incrementalRenderSession,
	outputGeneration uint64,
	materializer incrementalColdCacheDraftMaterializer,
) (*incrementalColdCacheDraft, error) {
	if session == nil || session.state == nil || session.state.graph == nil ||
		session.graphSession == nil || outputGeneration == 0 || materializer == nil {
		return nil, errors.New("incremental cold cache draft is incomplete")
	}
	if _, err := validateIncrementalColdCacheDraftMaterializer(materializer); err != nil {
		return nil, fmt.Errorf("incremental cold cache draft materializer: %w", err)
	}
	baseGeneration := session.graphSession.BaseGeneration()
	if session.state.graph.Generation() != baseGeneration {
		return nil, errors.New("incremental cold cache draft has a stale graph base")
	}
	draft := &incrementalColdCacheDraft{
		state:               session.state,
		session:             session,
		graphSession:        session.graphSession,
		materializer:        materializer,
		outputGeneration:    outputGeneration,
		baseGraphGeneration: baseGeneration,
		lifecycle:           incrementalColdCacheDraftOpen,
	}
	draft.seal = draft
	return draft, nil
}

func validateIncrementalColdCacheDraftMaterializer(
	materializer incrementalColdCacheDraftMaterializer,
) (identity *incrementalColdCacheDraftMaterializerIdentity, err error) {
	defer func() {
		if recovered := recover(); recovered != nil {
			identity = nil
			err = fmt.Errorf("incremental cold cache materializer authentication panicked: %v", recovered)
		}
	}()
	if materializer == nil {
		return nil, errors.New("incremental cold cache materializer is unavailable")
	}
	identity = materializer.incrementalColdCacheDraftMaterializerIdentity()
	if identity == nil || identity.seal != identity {
		return nil, errors.New("incremental cold cache materializer has invalid provenance")
	}
	if err := materializer.validateIncrementalColdCacheDraftMaterializer(); err != nil {
		return nil, err
	}
	return identity, nil
}

func (d *incrementalColdCacheDraft) sealDraft() error {
	if d == nil {
		return errors.New("incremental cold cache draft is unavailable")
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.lifecycle != incrementalColdCacheDraftOpen {
		return errors.New("incremental cold cache draft is not open")
	}
	if err := d.validateStructureLocked(); err != nil {
		d.lifecycle = incrementalColdCacheDraftRevoked
		return err
	}
	materializerIdentity, err := validateIncrementalColdCacheDraftMaterializer(d.materializer)
	if err != nil {
		d.lifecycle = incrementalColdCacheDraftRevoked
		return fmt.Errorf("incremental cold cache draft materializer: %w", err)
	}
	authentication := &incrementalColdCacheDraftAuthentication{
		draft: d, state: d.state, session: d.session, graphSession: d.graphSession,
		materializer: materializerIdentity, outputGeneration: d.outputGeneration,
		baseGraphGeneration: d.baseGraphGeneration,
	}
	authentication.seal = authentication
	d.authentication = authentication
	d.lifecycle = incrementalColdCacheDraftSealed
	return d.validateLocked(incrementalColdCacheDraftSealed)
}

func (d *incrementalColdCacheDraft) validateStructureLocked() error {
	if d.seal != d || d.state == nil || d.state.graph == nil || d.session == nil ||
		d.session.state != d.state || d.graphSession == nil || d.session.graphSession != d.graphSession ||
		d.materializer == nil || d.outputGeneration == 0 ||
		d.session.graphSession.BaseGeneration() != d.baseGraphGeneration {
		return errors.New("incremental cold cache draft has invalid provenance")
	}
	materializerIdentity, err := validateIncrementalColdCacheDraftMaterializer(d.materializer)
	if err != nil {
		return fmt.Errorf("incremental cold cache draft materializer: %w", err)
	}
	if d.authentication != nil && d.authentication.materializer != materializerIdentity {
		return errors.New("incremental cold cache draft materializer changed")
	}
	if d.state.graph.Generation() != d.baseGraphGeneration {
		return errors.New("incremental cold cache draft has a stale graph base")
	}
	return nil
}

func (d *incrementalColdCacheDraft) validateLocked(
	want incrementalColdCacheDraftState,
) error {
	if err := d.validateStructureLocked(); err != nil {
		return err
	}
	authentication := d.authentication
	if authentication == nil || authentication.seal != authentication ||
		authentication.draft != d || authentication.state != d.state ||
		authentication.session != d.session || authentication.graphSession != d.graphSession ||
		authentication.outputGeneration != d.outputGeneration ||
		authentication.baseGraphGeneration != d.baseGraphGeneration || d.lifecycle != want {
		return errors.New("incremental cold cache draft authentication changed")
	}
	if want == incrementalColdCacheDraftSealed {
		if d.owner != nil || d.transfer != nil || d.materializing {
			return errors.New("incremental cold cache draft sealed ownership changed")
		}
		return nil
	}
	transfer := d.transfer
	if transfer == nil || transfer.seal != transfer || transfer.draft != d ||
		transfer.owner == nil || transfer.owner != d.owner {
		return errors.New("incremental cold cache draft transfer ownership changed")
	}
	return nil
}

func (d *incrementalColdCacheDraft) revoke() {
	if d == nil {
		return
	}
	d.mu.Lock()
	if d.lifecycle != incrementalColdCacheDraftMaterialized {
		d.lifecycle = incrementalColdCacheDraftRevoked
	}
	d.mu.Unlock()
}

type incrementalCacheReadyAuthority struct {
	seal  *incrementalCacheReadyAuthority
	state *incrementalRenderState
}

func newIncrementalCacheBuildAuthority(state *incrementalRenderState) *incrementalCacheBuildAuthority {
	authority := &incrementalCacheBuildAuthority{state: state}
	authority.seal = authority
	return authority
}

func newIncrementalCacheBuildIdentity(
	authority *incrementalCacheBuildAuthority,
	reservation *renderOutputReservation,
	generation uint64,
) IncrementalCacheBuildIdentity {
	identity := &incrementalCacheBuildIdentity{
		authority: authority, reservation: reservation, generation: generation,
	}
	identity.seal = identity
	return IncrementalCacheBuildIdentity{identity: identity}
}

// ValidateAuthentication verifies that the identity belongs to its exact renderer state.
func (i IncrementalCacheBuildIdentity) ValidateAuthentication() error {
	identity := i.identity
	if identity == nil || identity.seal != identity || identity.authority == nil ||
		identity.authority.seal != identity.authority || identity.authority.state == nil ||
		identity.authority.state.cacheBuildAuthority != identity.authority || identity.reservation == nil ||
		identity.reservation.validate(identity.reservation.service, identity.generation) != nil ||
		identity.reservation.service.incremental != identity.authority.state || identity.generation == 0 {
		return errors.New("incremental cache build identity has invalid provenance")
	}
	return nil
}

// Generation returns the authenticated output generation.
func (i IncrementalCacheBuildIdentity) Generation() (uint64, error) {
	if err := i.ValidateAuthentication(); err != nil {
		return 0, err
	}
	return i.identity.generation, nil
}

// Same reports whether both handles authenticate the same exact build.
func (i IncrementalCacheBuildIdentity) Same(other IncrementalCacheBuildIdentity) bool {
	return i.ValidateAuthentication() == nil && other.ValidateAuthentication() == nil &&
		i.identity == other.identity
}

type incrementalCacheReadySignal struct {
	seal       *incrementalCacheReadySignal
	authority  *incrementalCacheReadyAuthority
	generation uint64
	done       chan struct{}
	once       sync.Once
	mu         sync.Mutex
	completed  bool
	err        error
}

func newIncrementalCacheReadyAuthority(state *incrementalRenderState) *incrementalCacheReadyAuthority {
	authority := &incrementalCacheReadyAuthority{state: state}
	authority.seal = authority
	return authority
}

func newIncrementalCacheReadySignal(
	authority *incrementalCacheReadyAuthority,
	generation uint64,
) *incrementalCacheReadySignal {
	ready := &incrementalCacheReadySignal{
		authority: authority, generation: generation, done: make(chan struct{}),
	}
	ready.seal = ready
	return ready
}

func (s *incrementalCacheReadySignal) complete(err error) {
	if s == nil {
		return
	}
	s.once.Do(func() {
		s.mu.Lock()
		s.err = err
		s.completed = true
		s.mu.Unlock()
		close(s.done)
	})
}

func (s *incrementalCacheReadySignal) result() error {
	if s == nil {
		return errors.New("incremental cache readiness signal is unavailable")
	}
	<-s.done
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.err
}

func (s *incrementalCacheReadySignal) validate(
	authority *incrementalCacheReadyAuthority,
	generation uint64,
) error {
	if s == nil || s.seal != s || s.authority == nil || s.authority != authority ||
		s.authority.seal != s.authority || s.authority.state == nil ||
		s.authority.state.cacheReadyAuthority != s.authority || s.generation == 0 ||
		s.generation != generation || s.done == nil {
		return errors.New("incremental cache readiness has invalid provenance")
	}
	completed, _ := s.completion()
	if !completed {
		select {
		case <-s.done:
			return errors.New("incremental cache readiness closed without completion")
		default:
		}
	}
	return nil
}

func (s *incrementalCacheReadySignal) completion() (bool, error) {
	if s == nil {
		return false, errors.New("incremental cache readiness signal is unavailable")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.completed, s.err
}

func (s *incrementalRenderState) validateIncrementalCacheReadinessLocked() error {
	authority := s.cacheReadyAuthority
	if authority == nil || authority.seal != authority || authority.state != s {
		return errors.New("incremental cache readiness authority has invalid provenance")
	}
	if !s.cachePending {
		if s.cachePendingGeneration != 0 || s.cacheReadySignal != nil {
			return errors.New("incremental cache readiness retains a signal after completion")
		}
		return nil
	}
	if s.cachePendingGeneration == 0 || s.cacheReadySignal == nil {
		return errors.New("incremental cache readiness has no authenticated pending signal")
	}
	if err := s.cacheReadySignal.validate(authority, s.cachePendingGeneration); err != nil {
		return err
	}
	completed, completionErr := s.cacheReadySignal.completion()
	if completed && completionErr == nil {
		return errors.New("incremental cache readiness completed successfully while pending")
	}
	return nil
}

type incrementalCacheBuild struct {
	builder           *incrementalCacheBuilder
	session           *incrementalRenderSession
	generation        uint64
	active            httpstore.ActiveLeaseToken
	ctx               context.Context
	cancel            context.CancelCauseFunc
	logger            *slog.Logger
	ready             *incrementalCacheReadySignal
	reservationSource func() (*renderOutputReservation, error)
	reservation       *renderOutputReservation
	superseded        *incrementalCacheBuild
	identity          IncrementalCacheBuildIdentity
	observer          IncrementalCacheBuildObserver
	publicationReady  chan struct{}
	finalizeRenderer  func()
	publicationOnce   sync.Once
	observerStarted   atomic.Bool
	finishOnce        sync.Once
	draftMu           sync.Mutex
	draft             *incrementalColdCacheDraft
	draftAccepting    bool
	startedAt         time.Time
}

func (b *incrementalCacheBuild) transferColdCacheDraft(draft *incrementalColdCacheDraft) error {
	if b == nil || draft == nil {
		return errors.New("incremental cold cache draft transfer is incomplete")
	}
	b.draftMu.Lock()
	defer b.draftMu.Unlock()
	if !b.draftAccepting || b.draft != nil {
		return errors.New("incremental cold cache draft transfer is closed")
	}
	draft.mu.Lock()
	defer draft.mu.Unlock()
	if err := draft.validateLocked(incrementalColdCacheDraftSealed); err != nil {
		draft.lifecycle = incrementalColdCacheDraftRevoked
		return err
	}
	if b.builder == nil || b.session == nil || b.session.state == nil ||
		b.builder != &b.session.state.cache || b.session != draft.session ||
		b.session.state != draft.state || b.session.graphSession != draft.graphSession ||
		b.generation != draft.outputGeneration ||
		b.session.graphSession.BaseGeneration() != draft.baseGraphGeneration {
		draft.lifecycle = incrementalColdCacheDraftRevoked
		return errors.New("incremental cold cache draft belongs to another build")
	}
	draft.owner = b
	transfer := &incrementalColdCacheDraftTransferAuthentication{draft: draft, owner: b}
	transfer.seal = transfer
	draft.transfer = transfer
	draft.lifecycle = incrementalColdCacheDraftTransferred
	b.draft = draft
	return nil
}

func (b *incrementalCacheBuild) validateDraftMaterializationFence() error {
	if b == nil || b.builder == nil || b.session == nil || b.session.state == nil ||
		b.builder != &b.session.state.cache || b.ctx == nil || b.generation == 0 {
		return errors.New("incremental cold cache build has invalid provenance")
	}
	if cause := context.Cause(b.ctx); cause != nil {
		return cause
	}
	b.builder.mu.Lock()
	current := b.builder.current
	desired := b.builder.desiredGeneration
	closed := b.builder.closed
	b.builder.mu.Unlock()
	if closed || current != b || desired != b.generation {
		return errIncrementalCacheSuperseded
	}
	if b.reservation == nil {
		return errors.New("incremental cache output reservation is unavailable")
	}
	if err := b.identity.ValidateAuthentication(); err != nil {
		return err
	}
	if err := b.reservation.validateCommittedCacheBuild(b.session.state, b.generation); err != nil {
		return err
	}
	return nil
}

func (b *incrementalCacheBuild) materializeColdCacheDraft() (err error) {
	b.draftMu.Lock()
	b.draftAccepting = false
	draft := b.draft
	b.draftMu.Unlock()
	if draft == nil {
		return nil
	}
	if err := b.validateDraftMaterializationFence(); err != nil {
		draft.revoke()
		return err
	}
	draft.mu.Lock()
	if err := draft.validateLocked(incrementalColdCacheDraftTransferred); err != nil ||
		draft.owner != b || draft.materializing {
		draft.lifecycle = incrementalColdCacheDraftRevoked
		draft.mu.Unlock()
		if err != nil {
			return err
		}
		return errors.New("incremental cold cache draft has conflicting ownership")
	}
	draft.materializing = true
	draft.mu.Unlock()
	defer func() {
		if recovered := recover(); recovered != nil {
			err = fmt.Errorf("materializing incremental cold cache draft panicked: %v", recovered)
		}
		draft.mu.Lock()
		draft.materializing = false
		if err != nil {
			draft.lifecycle = incrementalColdCacheDraftRevoked
		}
		draft.mu.Unlock()
	}()
	if err := draft.materializer.materializeIncrementalColdCacheDraft(b.ctx, b.session); err != nil {
		return err
	}
	if cause := context.Cause(b.ctx); cause != nil {
		return cause
	}
	if err := b.validateDraftMaterializationFence(); err != nil {
		return err
	}
	draft.mu.Lock()
	defer draft.mu.Unlock()
	if err = draft.validateLocked(incrementalColdCacheDraftTransferred); err != nil ||
		draft.owner != b || !draft.materializing {
		if err == nil {
			err = errors.New("incremental cold cache draft ownership changed during materialization")
		}
		return err
	}
	draft.lifecycle = incrementalColdCacheDraftMaterialized
	return nil
}

func (b *incrementalCacheBuilder) supersede(generation uint64) {
	if b == nil || generation == 0 {
		return
	}
	b.mu.Lock()
	if generation > b.desiredGeneration {
		b.desiredGeneration = generation
		if b.current != nil {
			b.current.cancel(errIncrementalCacheSuperseded)
		}
		if b.pending != nil {
			b.pending.cancel(errIncrementalCacheSuperseded)
		}
	}
	b.mu.Unlock()
}

func (b *incrementalCacheBuilder) publishCold(
	ctx context.Context,
	state *incrementalRenderState,
	base *incrementalStateSnapshot,
	build *incrementalCacheBuild,
	publish,
	abort func(),
) (published bool, err error) {
	if b == nil || state == nil || base == nil || build == nil || build.session == nil {
		return false, errors.New("incremental cache publication is incomplete")
	}
	// Every refusal aborts what the caller prepared: the prepared HTTP lease
	// ownership holds the session's release lock and the shared HTTP state
	// lock, and the commit's own deferred cleanup takes the former next.
	b.mu.Lock()
	if b.closed || build.generation == 0 || build.generation != b.desiredGeneration {
		b.mu.Unlock()
		callIncrementalCacheAbort(abort)
		return false, incremental.ErrCommitConflict
	}
	state.mu.Lock()
	if state.retiring || state.retired || state.snapshot != base {
		state.mu.Unlock()
		b.mu.Unlock()
		callIncrementalCacheAbort(abort)
		return false, incremental.ErrCommitConflict
	}
	if cause := context.Cause(ctx); cause != nil {
		state.mu.Unlock()
		b.mu.Unlock()
		callIncrementalCacheAbort(abort)
		return false, cause
	}
	if publishErr := runColdCachePublishLocked(state, publish, abort); publishErr != nil {
		state.mu.Unlock()
		b.mu.Unlock()
		return false, publishErr
	}
	previous := b.enqueueLocked(build)
	state.mu.Unlock()
	b.mu.Unlock()
	if previous != nil {
		previous.cancel(errIncrementalCacheSuperseded)
		build.superseded = previous
	}
	return true, nil
}

func runColdCachePublishLocked(
	state *incrementalRenderState,
	publish, abort func(),
) error {
	if publish == nil {
		return nil
	}
	publishErr := callIncrementalCachePublish(publish)
	if publishErr == nil {
		return nil
	}
	var abortErr error
	if abort != nil {
		abortErr = callIncrementalCachePublish(abort)
	}
	if !errors.Is(publishErr, errRequiredRenderPublication) {
		state.cachePublicationErr = publishErr
	}
	if abortErr != nil {
		state.cachePublicationErr = errors.Join(state.cachePublicationErr, abortErr)
	}
	return errors.Join(publishErr, abortErr)
}

func callIncrementalCacheAbort(abort func()) {
	if abort != nil {
		abort()
	}
}

func callIncrementalCachePublish(publish func()) (err error) {
	defer func() {
		if recovered := recover(); recovered != nil {
			if publication, ok := recovered.(requiredRenderPublicationPanic); ok {
				err = fmt.Errorf("%w: %w", errRequiredRenderPublication, publication.err)
				return
			}
			err = fmt.Errorf("publishing synchronous incremental cache state panicked: %v", recovered)
		}
	}()
	publish()
	return nil
}

func (b *incrementalCacheBuilder) enqueueLocked(build *incrementalCacheBuild) *incrementalCacheBuild {
	if !b.running {
		b.running = true
		b.current = build
		b.wg.Add(1)
		go b.run(build)
		return nil
	}
	previous := b.pending
	b.pending = build
	return previous
}

func (b *incrementalCacheBuilder) run(build *incrementalCacheBuild) {
	defer b.wg.Done()
	for build != nil {
		build.execute()
		b.mu.Lock()
		if b.current == build {
			b.current = nil
		}
		build = b.pending
		b.pending = nil
		if build == nil {
			b.running = false
			b.mu.Unlock()
			return
		}
		b.current = build
		b.mu.Unlock()
	}
}

func newIncrementalCacheBuild(
	ctx context.Context,
	builder *incrementalCacheBuilder,
	session *incrementalRenderSession,
	generation uint64,
	active httpstore.ActiveLeaseToken,
	reservationSource func() (*renderOutputReservation, error),
	logger *slog.Logger,
) *incrementalCacheBuild {
	buildCtx, cancel := withoutCancelWithCause(ctx)
	return &incrementalCacheBuild{
		builder: builder, session: session, generation: generation, active: active,
		ctx: buildCtx, cancel: cancel, logger: logger,
		reservationSource: reservationSource,
		observer:          session.state.cacheBuildObserver,
		publicationReady:  make(chan struct{}),
		draftAccepting:    true,
	}
}

func (b *incrementalCacheBuild) execute() {
	succeeded := false
	var buildErr error
	defer func() {
		if recovered := recover(); recovered != nil {
			buildErr = fmt.Errorf("incremental cache build panicked: %v", recovered)
		}
		if buildErr != nil && b.logger != nil && !errors.Is(buildErr, errIncrementalCacheSuperseded) &&
			!errors.Is(buildErr, context.Canceled) {
			b.logger.Debug("Discarding asynchronous incremental render cache", "reason", buildErr)
		}
		b.finish(succeeded, buildErr)
	}()
	<-b.publicationReady
	b.startedAt = time.Now()
	if cause := context.Cause(b.ctx); cause != nil {
		buildErr = cause
		return
	}
	hooks := b.builder.snapshotHooks()
	if hooks.beforeOutputReservation != nil {
		hooks.beforeOutputReservation(b.ctx, b.generation)
	}
	if cause := context.Cause(b.ctx); cause != nil {
		buildErr = cause
		return
	}
	reservation, err := b.resolveOutputReservation()
	if err != nil {
		buildErr = err
		return
	}
	b.reservation = reservation
	b.identity = newIncrementalCacheBuildIdentity(
		b.session.state.cacheBuildAuthority,
		reservation,
		b.generation,
	)
	b.notifyStarted()
	if err := b.materializeColdCacheDraft(); err != nil {
		buildErr = err
		return
	}
	if hooks.beforePrepare != nil {
		hooks.beforePrepare(b.ctx, b.generation)
	}
	prepared, err := b.session.graphSession.PrepareGraphCommit(b.ctx)
	if err != nil {
		buildErr = err
		return
	}
	defer func() {
		if !succeeded {
			_ = prepared.Abort()
		}
	}()
	if hooks.afterPrepare != nil {
		hooks.afterPrepare(b.ctx, b.generation)
	}
	err = prepared.PublishWithPreparedPublisher(
		b.ctx,
		func(verifyCtx context.Context, inputs []incremental.InputRevision) (bool, error) {
			return b.session.verifyCachePublicationResources(verifyCtx, inputs)
		},
		func(retired []incremental.InputKey) (incremental.CommitPublication, error) {
			return b.prepareRendererPublication(retired)
		},
	)
	if err != nil {
		buildErr = err
		return
	}
	if b.finalizeRenderer == nil {
		buildErr = errors.New("incremental cache renderer publication has no finalizer")
		return
	}
	b.finalizeRenderer()
	b.finalizeRenderer = nil
	succeeded = true
}

func (b *incrementalCacheBuild) resolveOutputReservation() (*renderOutputReservation, error) {
	if b == nil || b.session == nil || b.session.state == nil || b.reservationSource == nil {
		return nil, errors.New("incremental cache output reservation binding is incomplete")
	}
	reservation, err := b.reservationSource()
	if err != nil {
		return nil, err
	}
	if err := reservation.validateCommittedCacheBuild(b.session.state, b.generation); err != nil {
		return nil, err
	}
	return reservation, nil
}

func (b *incrementalCacheBuild) lockRendererPublication(
	prepared *preparedIncrementalStateCommit,
) error {
	b.builder.mu.Lock()
	if b.builder.closed || b.builder.desiredGeneration != b.generation ||
		context.Cause(b.ctx) != nil {
		b.builder.mu.Unlock()
		return errIncrementalCacheSuperseded
	}
	b.session.state.mu.Lock()
	state := b.session.state
	if state.retiring || state.retired || !state.cachePending ||
		state.cachePendingGeneration != b.generation || state.cacheReadySignal != b.ready ||
		state.snapshot != b.session.base ||
		b.ready.validate(state.cacheReadyAuthority, b.generation) != nil {
		b.session.state.mu.Unlock()
		b.builder.mu.Unlock()
		return incremental.ErrCommitConflict
	}
	completed, _ := b.ready.completion()
	if completed {
		b.session.state.mu.Unlock()
		b.builder.mu.Unlock()
		return incremental.ErrCommitConflict
	}
	if err := prepared.validateDetachedPublication(); err != nil {
		b.session.state.mu.Unlock()
		b.builder.mu.Unlock()
		return err
	}
	if b.reservation == nil {
		b.session.state.mu.Unlock()
		b.builder.mu.Unlock()
		return errors.New("incremental cache output reservation is unavailable")
	}
	if err := b.reservation.beginCommittedCachePublication(
		b.session.state,
		b.generation,
	); err != nil {
		b.session.state.mu.Unlock()
		b.builder.mu.Unlock()
		return err
	}
	return nil
}

func (b *incrementalCacheBuild) prepareRendererPublication(
	retired []incremental.InputKey,
) (incremental.CommitPublication, error) {
	prepared, err := b.session.prepareDetachedStateCommit(retired, b.active)
	if err != nil {
		return incremental.CommitPublication{}, err
	}
	locked := false
	reservationLocked := false
	hooks := b.builder.snapshotHooks()
	b.finalizeRenderer = func() {
		prepared.Release()
		if locked {
			b.session.state.mu.Unlock()
			b.builder.mu.Unlock()
			if reservationLocked {
				b.reservation.endCommittedCachePublication()
				reservationLocked = false
			}
			locked = false
		}
	}
	return incremental.CommitPublication{
		Publish: func() {
			if hooks.beforeRendererPublish != nil {
				hooks.beforeRendererPublish(b.ctx, b.generation)
			}
			if err := b.lockRendererPublication(prepared); err != nil {
				panic(err)
			}
			reservationLocked = true
			locked = true
		},
		Complete: func() {
			prepared.Publish()
			if err := prepared.commitPublishedPublication(); err != nil {
				panic(err)
			}
		},
		Abort: func() {
			prepared.Abort()
			if locked {
				b.session.state.mu.Unlock()
				b.builder.mu.Unlock()
				if reservationLocked {
					b.reservation.endCommittedCachePublication()
					reservationLocked = false
				}
				locked = false
			}
		},
	}, nil
}

func (b *incrementalCacheBuild) finish(succeeded bool, buildErr error) {
	if b == nil {
		return
	}
	b.finishOnce.Do(func() {
		b.cancel(nil)
		if !succeeded {
			b.draftMu.Lock()
			draft := b.draft
			b.draftAccepting = false
			b.draftMu.Unlock()
			draft.revoke()
			if buildErr == nil {
				buildErr = context.Cause(b.ctx)
			}
			if buildErr == nil {
				buildErr = incremental.ErrCommitConflict
			}
		}
		if !succeeded && b.session.graphSession != nil {
			b.session.graphSession.Abort()
		}
		b.session.releaseRenderFrames()
		callbackErr := b.session.finishDeferredCachePublication(succeeded)
		if succeeded {
			buildErr = b.completeReadyCache()
			b.builder.lastBuildMs.Store(time.Since(b.startedAt).Milliseconds())
		} else {
			buildErr = errors.Join(buildErr, callbackErr)
			b.ready.complete(buildErr)
		}
		if callbackErr != nil && b.logger != nil {
			b.logger.Debug("Discarding deferred exact cycle publication", "reason", callbackErr)
		}
		b.notifyCompleted(buildErr)
	})
}

func (b *incrementalCacheBuild) releasePublication() {
	if b == nil {
		return
	}
	b.publicationOnce.Do(func() { close(b.publicationReady) })
}

func (b *incrementalCacheBuild) finishSuperseded() {
	if b == nil || b.superseded == nil {
		return
	}
	b.superseded.finish(false, errIncrementalCacheSuperseded)
	b.superseded = nil
}

func (b *incrementalCacheBuild) notifyStarted() {
	if b == nil || b.observer == nil || b.identity.ValidateAuthentication() != nil {
		return
	}
	b.observerStarted.Store(true)
	defer func() { _ = recover() }()
	b.observer.IncrementalCacheBuildStarted(b.ctx, b.identity)
}

func (b *incrementalCacheBuild) notifyCompleted(buildErr error) {
	if b == nil || !b.observerStarted.Load() || b.observer == nil ||
		b.identity.ValidateAuthentication() != nil {
		return
	}
	defer func() { _ = recover() }()
	b.observer.IncrementalCacheBuildCompleted(b.identity, buildErr)
}

func (b *incrementalCacheBuild) completeReadyCache() error {
	state := b.session.state
	state.mu.Lock()
	if state.cachePending && state.cachePendingGeneration == b.generation &&
		state.cacheReadySignal == b.ready &&
		b.ready.validate(state.cacheReadyAuthority, b.generation) == nil {
		state.cachePending = false
		state.cachePendingGeneration = 0
		state.cacheReadySignal = nil
		state.mu.Unlock()
		b.ready.complete(nil)
		return nil
	}
	state.mu.Unlock()
	b.ready.complete(incremental.ErrCommitConflict)
	return incremental.ErrCommitConflict
}

func (b *incrementalCacheBuilder) snapshotHooks() incrementalCacheBuilderHooks {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.hooks
}

func (b *incrementalCacheBuilder) shutdown() {
	if b == nil {
		return
	}
	b.mu.Lock()
	if !b.closed {
		b.closed = true
		if b.current != nil {
			b.current.cancel(context.Canceled)
		}
		if b.pending != nil {
			b.pending.cancel(context.Canceled)
		}
	}
	b.mu.Unlock()
	b.wg.Wait()
}

func (r *incrementalRenderSession) bindCacheOutputGeneration(generation uint64) {
	if r == nil {
		return
	}
	r.mu.Lock()
	if r.cacheOutputGeneration == 0 {
		r.cacheOutputGeneration = generation
	}
	r.mu.Unlock()
}

type deferredIncrementalCachePublication struct {
	publish func()
	abort   func()
}

func (r *incrementalRenderSession) deferCachePublication(publish, abort func()) bool {
	if r == nil {
		return false
	}
	r.mu.Lock()
	if !r.cachePublicationDeferred {
		r.mu.Unlock()
		return false
	}
	if r.cachePublicationFinished {
		succeeded := r.exactCycleCacheCommitted
		r.mu.Unlock()
		if succeeded {
			if err := callDeferredCachePublication(publish); err != nil {
				_ = callDeferredCachePublication(abort)
			}
		} else {
			_ = callDeferredCachePublication(abort)
		}
		return true
	}
	if publish != nil || abort != nil {
		r.cachePublicationCallbacks = append(r.cachePublicationCallbacks, deferredIncrementalCachePublication{
			publish: publish,
			abort:   abort,
		})
	}
	r.mu.Unlock()
	return true
}

func (r *incrementalRenderSession) finishDeferredCachePublication(succeeded bool) error {
	if r == nil {
		return nil
	}
	r.mu.Lock()
	if r.cachePublicationFinished {
		r.mu.Unlock()
		return nil
	}
	r.cachePublicationFinished = true
	callbacks := r.cachePublicationCallbacks
	r.cachePublicationCallbacks = nil
	r.mu.Unlock()
	if !succeeded {
		return abortDeferredCachePublications(callbacks)
	}
	for _, callback := range callbacks {
		if err := callDeferredCachePublication(callback.publish); err != nil {
			return errors.Join(err, abortDeferredCachePublications(callbacks))
		}
	}
	return nil
}

func abortDeferredCachePublications(callbacks []deferredIncrementalCachePublication) error {
	var abortErr error
	for i := len(callbacks) - 1; i >= 0; i-- {
		abortErr = errors.Join(abortErr, callDeferredCachePublication(callbacks[i].abort))
	}
	return abortErr
}

func callDeferredCachePublication(callback func()) (err error) {
	if callback == nil {
		return nil
	}
	defer func() {
		if recovered := recover(); recovered != nil {
			err = fmt.Errorf("deferred incremental cache publication callback panicked: %v", recovered)
		}
	}()
	callback()
	return nil
}

func withoutCancelWithCause(ctx context.Context) (context.Context, context.CancelCauseFunc) {
	return context.WithCancelCause(context.WithoutCancel(ctx))
}
