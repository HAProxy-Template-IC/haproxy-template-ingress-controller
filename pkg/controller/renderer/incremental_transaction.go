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
	"slices"
	"strings"
	"sync"

	iradix "github.com/hashicorp/go-immutable-radix/v2"

	controllerhttpstore "gitlab.com/haproxy-haptic/haptic/pkg/controller/httpstore"
	"gitlab.com/haproxy-haptic/haptic/pkg/controller/rendercontext"
	"gitlab.com/haproxy-haptic/haptic/pkg/httpstore"
	"gitlab.com/haproxy-haptic/haptic/pkg/incremental"
	"gitlab.com/haproxy-haptic/haptic/pkg/stores"
	"gitlab.com/haproxy-haptic/haptic/pkg/templating"
)

var errCombinedInputTransactionAborted = errors.New("render input transaction was aborted")
var errRequiredRenderPublication = errors.New("required render publication failed")

type requiredRenderPublicationPanic struct {
	err error
}

func (p requiredRenderPublicationPanic) Error() string {
	return p.err.Error()
}

func (p requiredRenderPublicationPanic) Unwrap() error {
	return p.err
}

type combinedRenderInputTransaction struct {
	once         sync.Once
	publications stagedRenderPublications
	referencesMu sync.RWMutex
	http         RenderInputTransaction
	incremental  *incrementalRenderSession
	logger       *slog.Logger
	commitErr    error
}

type incrementalCommitPublications struct {
	prepare           func()
	validate          func()
	commit            func()
	release           func()
	outputReservation func() (*renderOutputReservation, error)
}

func newCombinedRenderInputTransaction(
	http RenderInputTransaction,
	runtime *incrementalRenderSession,
	logger *slog.Logger,
) RenderInputTransaction {
	if http == nil && runtime == nil {
		return nil
	}
	return &combinedRenderInputTransaction{http: http, incremental: runtime, logger: logger}
}

func (t *combinedRenderInputTransaction) HasCandidates() bool {
	http, _, _ := t.references()
	return http != nil && http.HasCandidates()
}

// CarriesHTTPState reports whether the commit would move the HTTP store, by
// accepting content or by changing which renders hold a source's active
// leases.
//
// HasCandidates answers only the first. The lease half is reference
// accounting: the commit tells the store how many renders now hold each
// source, and skipping it leaves the store counting references this render
// already stopped holding. The next render's removals then exceed what the
// store believes exists, and it rejects them as inconsistent. So a caller
// deciding whether a failed commit can be shrugged off has to ask about both.
func (t *combinedRenderInputTransaction) CarriesHTTPState() bool {
	http, _, _ := t.references()
	return http != nil
}

func (t *combinedRenderInputTransaction) ProvisionalURLs() []string {
	http, _, _ := t.references()
	if http == nil {
		return nil
	}
	transaction, ok := http.(interface{ ProvisionalURLs() []string })
	if !ok {
		return nil
	}
	return transaction.ProvisionalURLs()
}

func (t *combinedRenderInputTransaction) RetrySeed() *controllerhttpstore.InputRetrySeed {
	http, _, _ := t.references()
	transaction, ok := http.(interface {
		RetrySeed() *controllerhttpstore.InputRetrySeed
	})
	if !ok {
		return nil
	}
	return transaction.RetrySeed()
}

func (t *combinedRenderInputTransaction) StagePublication(callback func()) {
	t.stagePublicationFinalizer(callback, nil)
}

func (t *combinedRenderInputTransaction) stagePublicationFinalizer(publish, abort func()) {
	t.publications.stage(publish, abort)
}

func (t *combinedRenderInputTransaction) stageOptionalPublication(publish func()) {
	t.publications.stageOptional(publish)
}

func (t *combinedRenderInputTransaction) bindRenderOutputReservation(
	reservation *renderOutputReservation,
) error {
	return t.publications.bindRenderOutputReservation(reservation)
}

func (t *combinedRenderInputTransaction) Commit(ctx context.Context) error {
	t.once.Do(func() {
		http, incrementalSession, logger := t.references()
		defer t.releaseReferences()
		defer func() {
			if recovered := recover(); recovered != nil {
				t.commitErr = errors.Join(
					fmt.Errorf("render input transaction panicked: %v", recovered),
					t.abortCandidates(http, incrementalSession),
				)
			}
		}()
		if cause := context.Cause(ctx); cause != nil {
			t.commitErr = errors.Join(cause, t.abortCandidates(http, incrementalSession))
			return
		}
		handled, err := t.publications.resolveStaleCandidateBeforeCommit()
		if handled {
			t.commitErr = errors.Join(err, t.abortCandidates(http, incrementalSession))
			return
		}
		if incrementalSession != nil {
			publications := t.terminalCommitPublications()
			if err := incrementalSession.commit(ctx, logger, http, publications); err != nil {
				t.commitErr = errors.Join(err, t.abortCandidates(http, incrementalSession))
				return
			}
		} else if http != nil {
			t.commitHTTPOnly(ctx, http, incrementalSession)
		} else {
			t.commitErr = t.commitTerminalResultOnly()
		}
	})
	return t.commitErr
}

func (t *combinedRenderInputTransaction) commitHTTPOnly(
	ctx context.Context,
	http RenderInputTransaction,
	incrementalSession *incrementalRenderSession,
) {
	prepared, err := prepareHTTPInputCommit(ctx, http, nil, nil, nil, false)
	if err != nil {
		t.commitErr = errors.Join(err, t.abortCandidates(http, incrementalSession))
		return
	}
	defer func() {
		if t.commitErr != nil {
			prepared.Abort()
			t.commitErr = errors.Join(t.commitErr, t.publications.abortResult())
		}
	}()
	if cause := context.Cause(ctx); cause != nil {
		t.commitErr = cause
		return
	}
	if sealErr := prepared.SealPublication(); sealErr != nil {
		t.commitErr = sealErr
		prepared.Abort()
		return
	}
	if publicationErr := t.publications.prepareTerminalResult(); publicationErr != nil {
		t.commitErr = publicationErr
		prepared.Abort()
		return
	}
	prepared.PublishSealed()
	if publicationErr := prepared.ValidatePublishedPublication(); publicationErr != nil {
		t.commitErr = publicationErr
		prepared.Abort()
		return
	}
	if publicationErr := t.publications.validateTerminalResult(); publicationErr != nil {
		t.commitErr = publicationErr
		prepared.Abort()
		return
	}
	if publicationErr := t.publications.commitTerminalResult(); publicationErr != nil {
		t.commitErr = publicationErr
		prepared.Abort()
		return
	}
	if publicationErr := prepared.CommitPublishedPublication(); publicationErr != nil {
		t.commitErr = publicationErr
		prepared.Abort()
		return
	}
	prepared.ReleaseCommittedPublication()
	t.commitErr = t.publications.releaseTerminalResult()
}

func (t *combinedRenderInputTransaction) terminalCommitPublications() incrementalCommitPublications {
	return incrementalCommitPublications{
		outputReservation: t.publications.committedOutputReservation,
		prepare: func() {
			if publicationErr := t.publications.prepareTerminalResult(); publicationErr != nil {
				panic(requiredRenderPublicationPanic{err: publicationErr})
			}
		},
		validate: func() {
			if publicationErr := t.publications.validateTerminalResult(); publicationErr != nil {
				panic(requiredRenderPublicationPanic{err: publicationErr})
			}
		},
		commit: func() {
			if publicationErr := t.publications.commitTerminalResult(); publicationErr != nil {
				panic(requiredRenderPublicationPanic{err: publicationErr})
			}
		},
		release: func() {
			if publicationErr := t.publications.releaseTerminalResult(); publicationErr != nil {
				panic(requiredRenderPublicationPanic{err: publicationErr})
			}
		},
	}
}

func (t *combinedRenderInputTransaction) commitTerminalResultOnly() error {
	commitErr := t.publications.prepareTerminalResult()
	if commitErr == nil {
		commitErr = t.publications.validateTerminalResult()
	}
	if commitErr == nil {
		commitErr = t.publications.commitTerminalResult()
	}
	if commitErr == nil {
		commitErr = t.publications.releaseTerminalResult()
	}
	if commitErr != nil {
		commitErr = errors.Join(commitErr, t.publications.abortResult())
	}
	return commitErr
}

func (t *combinedRenderInputTransaction) Abort() {
	t.once.Do(func() {
		http, incrementalSession, _ := t.references()
		defer t.releaseReferences()
		t.commitErr = errCombinedInputTransactionAborted
		t.commitErr = errors.Join(t.commitErr, t.abortCandidates(http, incrementalSession))
	})
}

func (t *combinedRenderInputTransaction) abortCandidates(
	http RenderInputTransaction,
	incrementalSession *incrementalRenderSession,
) error {
	return errors.Join(
		abortRenderInputTransaction(http),
		abortIncrementalRenderSession(incrementalSession),
		t.publications.abortResult(),
	)
}

func abortIncrementalRenderSession(session *incrementalRenderSession) (err error) {
	if session == nil {
		return nil
	}
	defer func() {
		if recovered := recover(); recovered != nil {
			if recoveredErr, ok := recovered.(error); ok {
				err = fmt.Errorf("aborting incremental render session panicked: %w", recoveredErr)
				return
			}
			err = fmt.Errorf("aborting incremental render session panicked: %v", recovered)
		}
	}()
	session.abort()
	return nil
}

func (t *combinedRenderInputTransaction) references() (
	RenderInputTransaction,
	*incrementalRenderSession,
	*slog.Logger,
) {
	t.referencesMu.RLock()
	defer t.referencesMu.RUnlock()
	return t.http, t.incremental, t.logger
}

func (t *combinedRenderInputTransaction) releaseReferences() {
	t.referencesMu.Lock()
	t.http = nil
	t.incremental = nil
	t.logger = nil
	t.referencesMu.Unlock()
}

func renderInputCommitError(ctx context.Context, err error) error {
	cause := context.Cause(ctx)
	if cause == nil {
		return err
	}
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return cause
	}
	return err
}

type incrementalHTTPPublication struct {
	session     *incrementalRenderSession
	transaction RenderInputTransaction
	prepared    *controllerhttpstore.PreparedInputCommit
	active      httpstore.ActiveLeaseToken
	published   bool
	committed   bool
}

func (p *incrementalHTTPPublication) prepare(ctx context.Context, retainGraphProofs bool) error {
	if p.prepared != nil {
		return errors.New("incremental HTTP publication was prepared twice")
	}
	var proofs []httpstore.ObservationToken
	var active *httpstore.ActiveLeaseCommit
	hasActive := false
	if retainGraphProofs || p.transaction == nil {
		proofs = p.session.httpObservationProofs()
	}
	if retainGraphProofs {
		var err error
		active, hasActive, err = p.session.activeLeaseCommit()
		if err != nil {
			return err
		}
	}
	prepared, err := prepareHTTPInputCommit(
		ctx, p.transaction, proofs, active, p.session.httpComponent, !retainGraphProofs,
	)
	if err != nil {
		return err
	}
	if hasActive {
		token, _, planned := prepared.PlannedActiveLeases()
		if !planned || !token.Valid() {
			prepared.Abort()
			return errors.New("incremental HTTP lease publication has no authenticated token")
		}
		p.active = token
	}
	if err := prepared.SealPublication(); err != nil {
		prepared.Abort()
		return err
	}
	p.prepared = prepared
	return nil
}

func (p *incrementalHTTPPublication) prepareWithActiveLease(
	ctx context.Context,
	active *httpstore.ActiveLeaseCommit,
) error {
	if p.prepared != nil {
		return errors.New("incremental HTTP publication was prepared twice")
	}
	prepared, err := prepareHTTPInputCommit(
		ctx,
		p.transaction,
		p.session.httpObservationProofs(),
		active,
		p.session.httpComponent,
		false,
	)
	if err != nil {
		return err
	}
	if active != nil {
		token, _, planned := prepared.PlannedActiveLeases()
		if !planned || !token.Valid() {
			prepared.Abort()
			return errors.New("incremental HTTP lease publication has no authenticated token")
		}
		p.active = token
	}
	if err := prepared.SealPublication(); err != nil {
		prepared.Abort()
		return err
	}
	p.prepared = prepared
	return nil
}

func (p *incrementalHTTPPublication) prepareExactCycleLease(
	ctx context.Context,
	active *httpstore.ActiveLeaseCommit,
) error {
	if p.prepared != nil || active == nil {
		return errors.New("exact cycle HTTP publication has an invalid lease preparation")
	}
	prepared, err := prepareHTTPInputCommit(
		ctx,
		p.transaction,
		p.session.httpObservationProofs(),
		active,
		p.session.httpComponent,
		false,
	)
	if err != nil {
		return err
	}
	token, _, planned := prepared.PlannedActiveLeases()
	if !planned || !token.Valid() {
		prepared.Abort()
		return errors.New("exact cycle HTTP lease publication has no authenticated token")
	}
	p.active = token
	if err := prepared.SealPublication(); err != nil {
		prepared.Abort()
		return err
	}
	p.prepared = prepared
	return nil
}

func (p *incrementalHTTPPublication) publish() {
	if p.prepared == nil {
		return
	}
	p.prepared.PublishSealed()
	p.published = true
}

func (p *incrementalHTTPPublication) validatePublishedPublication() error {
	if p == nil || p.prepared == nil || !p.published {
		return errors.New("published incremental HTTP publication is invalid")
	}
	return p.prepared.ValidatePublishedPublication()
}

func (p *incrementalHTTPPublication) commitPublishedPublication() error {
	if p == nil || p.prepared == nil || !p.published {
		return errors.New("published incremental HTTP publication is invalid")
	}
	if p.committed {
		return nil
	}
	if err := p.prepared.CommitPublishedPublication(); err != nil {
		return err
	}
	p.committed = true
	return nil
}

func (p *incrementalHTTPPublication) finish() {
	if p.prepared == nil {
		return
	}
	prepared := p.prepared
	if p.published && p.committed {
		prepared.ReleaseCommittedPublication()
	} else {
		prepared.Abort()
	}
	p.prepared = nil
}

func (p *incrementalHTTPPublication) abort() {
	if p == nil || p.prepared == nil {
		return
	}
	p.prepared.Abort()
	p.prepared = nil
	p.published = false
	p.committed = false
}

func (r *incrementalRenderSession) commit(
	ctx context.Context,
	logger *slog.Logger,
	httpTransaction RenderInputTransaction,
	publications incrementalCommitPublications,
) (commitErr error) {
	r.commitAcceptsCandidates = httpTransaction != nil && httpTransaction.HasCandidates()
	cacheTransferred := false
	defer func() {
		if !cacheTransferred {
			r.releaseRenderFrames()
		}
	}()
	defer func() {
		if !cacheTransferred {
			commitErr = r.finishCommitHTTPInputs(renderInputCommitError(ctx, commitErr))
		}
	}()
	releaseFences, err := r.acquireStoreCommitFences(ctx)
	if err != nil {
		r.abortGraphSession()
		return fmt.Errorf("acquiring incremental render commit fences: %w", err)
	}
	defer releaseFences()
	httpPublication := &incrementalHTTPPublication{
		session:     r,
		transaction: httpTransaction,
	}
	defer func() {
		if commitErr != nil {
			httpPublication.abort()
			return
		}
		httpPublication.finish()
	}()
	r.mu.Lock()
	cachePublishable := r.cachePublishable
	cold := r.cold
	cacheGeneration := r.cacheOutputGeneration
	r.mu.Unlock()
	if !cachePublishable || !r.cachePublicationEnabled {
		return r.commitHTTPWithoutCache(ctx, httpPublication, publications)
	}
	if cold && cacheGeneration != 0 {
		var startErr error
		cacheTransferred, startErr = r.startColdGraphCache(
			ctx, cacheGeneration, logger, httpPublication, publications,
		)
		return startErr
	}
	return r.commitWithGraphCache(ctx, logger, httpPublication, publications)
}

func (r *incrementalRenderSession) startColdGraphCache(
	ctx context.Context,
	cacheGeneration uint64,
	logger *slog.Logger,
	httpPublication *incrementalHTTPPublication,
	publications incrementalCommitPublications,
) (bool, error) {
	build, transferred, err := r.commitColdGraphCacheAsync(
		ctx, cacheGeneration, logger, httpPublication, publications,
	)
	if errors.Is(err, incremental.ErrCommitConflict) {
		return transferred, err
	}
	if err != nil {
		return transferred, fmt.Errorf("starting asynchronous incremental render cache: %w", err)
	}
	// The commit that produced the graph is the only window in which nothing can
	// overtake it, so it is where the graph gets to land.
	awaitColdCacheBuild(ctx, build, maxColdCacheBuildWait)
	return transferred, nil
}

func (r *incrementalRenderSession) commitWithGraphCache(
	ctx context.Context,
	logger *slog.Logger,
	httpPublication *incrementalHTTPPublication,
	publications incrementalCommitPublications,
) error {
	prepared, err := r.commitGraphCache(ctx, httpPublication, publications)
	if errors.Is(err, incremental.ErrCommitConflict) {
		if logger != nil {
			logger.Debug("Discarding incremental render cache transaction", "reason", err)
		}
		return r.commitHTTPWithoutCache(ctx, httpPublication, publications)
	}
	if errors.Is(err, incremental.ErrRevisionConflict) {
		return err
	}
	if err != nil {
		return fmt.Errorf("committing incremental render cache: %w", err)
	}
	prepared.releaseStateLock()
	httpPublication.finish()
	prepared.state.Release()
	return callRequiredIncrementalPublication(publications.release)
}

func (r *incrementalRenderSession) commitColdGraphCacheAsync(
	ctx context.Context,
	generation uint64,
	logger *slog.Logger,
	httpPublication *incrementalHTTPPublication,
	publications incrementalCommitPublications,
) (*incrementalCacheBuild, bool, error) {
	if publications.outputReservation == nil {
		return nil, false, errors.New("incremental cache output reservation is unavailable")
	}
	verified, err := r.verifyResources(ctx, nil)
	if err != nil {
		return nil, false, fmt.Errorf("verifying incremental render inputs: %w", err)
	}
	if !verified {
		return nil, false, incremental.ErrRevisionConflict
	}
	active, _, err := r.activeLeaseCommit()
	if err != nil {
		return nil, false, err
	}
	build := newIncrementalCacheBuild(
		ctx,
		&r.state.cache,
		r,
		generation,
		httpstore.ActiveLeaseToken{},
		publications.outputReservation,
		logger,
	)
	hooks := r.state.cache.snapshotHooks()
	publish, abort, release, err := r.prepareColdCachePublication(
		ctx, active, httpPublication, build, hooks, publications,
	)
	if err != nil {
		build.cancel(err)
		return nil, false, err
	}
	published, err := r.state.cache.publishCold(
		ctx,
		r.state,
		r.base,
		build,
		publish,
		abort,
	)
	if err != nil {
		build.cancel(err)
		return nil, false, err
	}
	if !published {
		build.cancel(incremental.ErrCommitConflict)
		return nil, false, nil
	}
	defer build.releasePublication()
	httpPublication.finish()
	release()
	if err := callRequiredIncrementalPublication(publications.release); err != nil {
		build.finishSuperseded()
		build.cancel(err)
		return nil, false, err
	}
	build.finishSuperseded()
	return build, published, nil
}

func (r *incrementalRenderSession) prepareColdCachePublication(
	ctx context.Context,
	active *httpstore.ActiveLeaseCommit,
	httpPublication *incrementalHTTPPublication,
	build *incrementalCacheBuild,
	hooks incrementalCacheBuilderHooks,
	publications incrementalCommitPublications,
) (publish, abort, release func(), err error) {
	var preparedOwnership *preparedIncrementalHTTPLeaseOwnershipCommit
	defer func() {
		if recovered := recover(); recovered != nil {
			if preparedOwnership != nil {
				preparedOwnership.Abort()
			}
			publish = nil
			abort = nil
			release = nil
			err = fmt.Errorf("preparing cold incremental cache publication panicked: %v", recovered)
		}
	}()
	if err := httpPublication.prepareWithActiveLease(ctx, active); err != nil {
		return nil, nil, nil, err
	}
	if hooks.afterHTTPPrepare != nil {
		hooks.afterHTTPPrepare(ctx, build.generation)
	}
	preparedOwnership, err = r.prepareHTTPLeaseOwnershipCommit()
	if err != nil {
		return nil, nil, nil, err
	}
	if hooks.afterDependencyPrepare != nil {
		hooks.afterDependencyPrepare(ctx, build.generation)
	}
	next := r.beginColdCacheSnapshot(httpPublication, build)
	notifyBeforeColdPublication(ctx, hooks, build.generation)
	var previousSnapshot *incrementalStateSnapshot
	var previousReady *incrementalCacheReadySignal
	var previousPending bool
	var previousPendingGeneration uint64
	var previousDeferred bool
	published := false
	return func() {
			r.mu.Lock()
			previousDeferred = r.cachePublicationDeferred
			r.cachePublicationDeferred = true
			r.mu.Unlock()
			mustPublishRequiredIncrementalPublication(publications.prepare)
			mustSucceedIncrementalPublication(httpPublication.prepared.ValidatePublication())
			mustSucceedIncrementalPublication(preparedOwnership.validatePublication())
			mustSucceedIncrementalPublication(r.state.validateIncrementalCacheReadinessLocked())
			previousSnapshot = r.state.snapshot
			previousPending = r.state.cachePending
			previousPendingGeneration = r.state.cachePendingGeneration
			previousReady = r.state.cacheReadySignal
			published = true
			httpPublication.publish()
			preparedOwnership.Publish()
			r.state.snapshot = next
			r.state.cachePending = true
			r.state.cachePendingGeneration = build.generation
			r.state.cacheReadySignal = build.ready
			r.base = next
			mustSucceedIncrementalPublication(httpPublication.validatePublishedPublication())
			mustSucceedIncrementalPublication(preparedOwnership.validatePublishedPublication())
			mustSucceedIncrementalPublication(validateIncrementalStateSnapshotAuthentication(r.state.snapshot))
			mustPublishRequiredIncrementalPublication(publications.validate)
			mustSucceedIncrementalPublication(httpPublication.commitPublishedPublication())
			mustSucceedIncrementalPublication(preparedOwnership.commitPublishedPublication())
			mustPublishRequiredIncrementalPublication(publications.commit)
		}, func() {
			if published {
				if r.state.snapshot == next && r.state.cacheReadySignal == build.ready &&
					r.state.cachePendingGeneration == build.generation {
					r.state.snapshot = previousSnapshot
					r.state.cachePending = previousPending
					r.state.cachePendingGeneration = previousPendingGeneration
					r.state.cacheReadySignal = previousReady
					r.base = previousSnapshot
				} else {
					r.state.cachePublicationErr = errors.New(
						"cold incremental cache rollback lost its publication state",
					)
				}
				r.mu.Lock()
				r.cachePublicationDeferred = previousDeferred
				r.mu.Unlock()
			}
			preparedOwnership.Abort()
		}, func() {
			if previousReady != nil && previousReady != build.ready {
				previousReady.complete(errIncrementalCacheSuperseded)
			}
			preparedOwnership.Release()
		}, nil
}

func (r *incrementalRenderSession) beginColdCacheSnapshot(
	httpPublication *incrementalHTTPPublication,
	build *incrementalCacheBuild,
) *incrementalStateSnapshot {
	token := httpPublication.active
	if !token.Valid() {
		token = r.base.httpCursor.token
	}
	next := *r.base
	next.httpCursor = incrementalHTTPCursor{token: token}
	authenticateIncrementalStatusPatchPlan(&next)
	authenticateIncrementalStateSnapshot(&next)
	build.active = token
	build.ready = newIncrementalCacheReadySignal(r.state.cacheReadyAuthority, build.generation)
	return &next
}

func notifyBeforeColdPublication(
	ctx context.Context,
	hooks incrementalCacheBuilderHooks,
	generation uint64,
) {
	if hooks.beforeColdPublication == nil {
		return
	}
	for _, stage := range []incrementalColdPublicationStage{
		incrementalColdPublicationHTTP,
		incrementalColdPublicationOwnership,
		incrementalColdPublicationState,
		incrementalColdPublicationOutput,
	} {
		hooks.beforeColdPublication(ctx, generation, stage)
	}
}

func (r *incrementalRenderSession) finishCommitHTTPInputs(commitErr error) error {
	if err := r.finishHTTPInputs(false, nil); err != nil {
		return errors.Join(commitErr, fmt.Errorf("releasing incremental HTTP inputs: %w", err))
	}
	return commitErr
}

func (r *incrementalRenderSession) abortGraphSession() {
	if r.graphSession != nil {
		r.graphSession.Abort()
	}
}

func (r *incrementalRenderSession) commitHTTPWithoutCache(
	ctx context.Context,
	httpPublication *incrementalHTTPPublication,
	publications incrementalCommitPublications,
) error {
	active, exactOutput, err := r.exactCycleOutputActiveLeaseCommit()
	if err != nil {
		return err
	}
	if exactOutput {
		return r.commitExactCycleOutputWithoutCache(ctx, active, httpPublication, publications)
	}
	return r.commitWithoutCache(ctx, func(verifyCtx context.Context) error {
		return httpPublication.prepare(verifyCtx, false)
	}, func() {
		httpPublication.publish()
	}, httpPublication.validatePublishedPublication, httpPublication.commitPublishedPublication,
		httpPublication.abort, func() {
			httpPublication.finish()
		}, publications)
}

func (r *incrementalRenderSession) commitExactCycleOutputWithoutCache(
	ctx context.Context,
	active *httpstore.ActiveLeaseCommit,
	httpPublication *incrementalHTTPPublication,
	publications incrementalCommitPublications,
) error {
	stateLocked := false
	defer func() {
		if stateLocked {
			r.state.mu.Unlock()
		}
	}()
	var nextSnapshot *incrementalStateSnapshot
	baseSnapshot := r.base
	return r.commitWithoutCache(ctx, func(verifyCtx context.Context) error {
		if err := httpPublication.prepareExactCycleLease(verifyCtx, active); err != nil {
			return err
		}
		r.state.mu.Lock()
		stateLocked = true
		if r.state.retiring || r.state.retired {
			return errors.New("incremental render cache was retired")
		}
		if r.state.snapshot != r.base {
			return incremental.ErrCommitConflict
		}
		var err error
		nextSnapshot, err = r.prepareExactCycleHTTPLeaseSnapshotLocked(httpPublication.active)
		return err
	}, func() {
		httpPublication.publish()
		r.state.snapshot = nextSnapshot
	}, func() error {
		if err := httpPublication.validatePublishedPublication(); err != nil {
			return err
		}
		if r.state.snapshot != nextSnapshot {
			return errors.New("published exact-cycle renderer state changed")
		}
		if err := validateIncrementalStateSnapshotAuthentication(nextSnapshot); err != nil {
			return err
		}
		return validateIncrementalStateSnapshotAuthentication(baseSnapshot)
	}, func() error {
		return httpPublication.commitPublishedPublication()
	}, func() {
		r.state.snapshot = baseSnapshot
		httpPublication.abort()
	}, func() {
		httpPublication.finish()
	}, publications)
}

func (r *incrementalRenderSession) commitGraphCache(
	ctx context.Context,
	httpPublication *incrementalHTTPPublication,
	publications incrementalCommitPublications,
) (*preparedIncrementalGraphPublication, error) {
	var prepared *preparedIncrementalGraphPublication
	err := r.graphSession.CommitWithPreparedPublisher(
		ctx,
		func(verifyCtx context.Context, inputs []incremental.InputRevision) (bool, error) {
			return r.verifyGraphPublication(verifyCtx, inputs, httpPublication)
		},
		func(retired []incremental.InputKey) (incremental.CommitPublication, error) {
			var err error
			prepared, err = r.prepareGraphPublication(retired, httpPublication, publications)
			if err != nil {
				return incremental.CommitPublication{}, err
			}
			return prepared.core, nil
		},
	)
	if err != nil {
		return nil, err
	}
	return prepared, nil
}

func (r *incrementalRenderSession) verifyGraphPublication(
	ctx context.Context,
	inputs []incremental.InputRevision,
	httpPublication *incrementalHTTPPublication,
) (bool, error) {
	verified, err := r.verifyResources(ctx, inputs)
	if err != nil || !verified {
		return verified, err
	}
	if err := httpPublication.prepare(ctx, true); err != nil {
		return false, err
	}
	return true, nil
}

func (r *incrementalRenderSession) prepareGraphPublication(
	retired []incremental.InputKey,
	httpPublication *incrementalHTTPPublication,
	publications incrementalCommitPublications,
) (prepared *preparedIncrementalGraphPublication, err error) {
	var preparedState *preparedIncrementalStateCommit
	stateLocked := false
	defer func() {
		if recovered := recover(); recovered != nil {
			err = fmt.Errorf("preparing incremental renderer publication panicked: %v", recovered)
		}
		if err == nil {
			return
		}
		if preparedState != nil {
			preparedState.Abort()
		}
		if stateLocked {
			r.state.mu.Unlock()
		}
		prepared = nil
	}()
	r.state.mu.Lock()
	stateLocked = true
	if r.state.retiring || r.state.retired || r.state.snapshot != r.base {
		return nil, incremental.ErrCommitConflict
	}
	preparedState, err = r.prepareStateCommit(retired, httpPublication.active)
	if err != nil {
		return nil, err
	}
	if httpPublication.prepared == nil {
		return nil, errors.New("incremental HTTP publication is missing")
	}
	if err := httpPublication.prepared.ValidatePublication(); err != nil {
		return nil, err
	}
	if err := preparedState.validatePublication(); err != nil {
		return nil, err
	}
	prepared = &preparedIncrementalGraphPublication{
		state:       preparedState,
		http:        httpPublication,
		stateLocked: true,
	}
	prepared.core = incremental.CommitPublication{
		Publish: func() {
			mustPublishRequiredIncrementalPublication(publications.prepare)
			mustSucceedIncrementalPublication(prepared.validateTerminal())
		},
		Complete: func() {
			prepared.publishTerminal()
			mustSucceedIncrementalPublication(prepared.validatePublishedTerminal())
			mustPublishRequiredIncrementalPublication(publications.validate)
			mustSucceedIncrementalPublication(prepared.state.commitPublishedPublication())
			mustSucceedIncrementalPublication(prepared.http.commitPublishedPublication())
			mustPublishRequiredIncrementalPublication(publications.commit)
		},
		Abort: prepared.abort,
	}
	stateLocked = false
	return prepared, nil
}

type preparedIncrementalGraphPublication struct {
	core        incremental.CommitPublication
	state       *preparedIncrementalStateCommit
	http        *incrementalHTTPPublication
	stateLocked bool
}

func (p *preparedIncrementalGraphPublication) publishTerminal() {
	p.state.Publish()
	p.http.publish()
}

func (p *preparedIncrementalGraphPublication) validateTerminal() error {
	if p == nil || p.state == nil || p.http == nil || p.http.prepared == nil {
		return errors.New("prepared incremental graph publication is invalid")
	}
	if err := p.state.validatePublication(); err != nil {
		return err
	}
	return p.http.prepared.ValidatePublication()
}

func (p *preparedIncrementalGraphPublication) validatePublishedTerminal() error {
	if p == nil || p.state == nil || p.http == nil || p.http.prepared == nil {
		return errors.New("published incremental graph publication is invalid")
	}
	if err := p.state.validatePublishedPublication(); err != nil {
		return err
	}
	return p.http.prepared.ValidatePublishedPublication()
}

func (p *preparedIncrementalGraphPublication) abort() {
	if p == nil {
		return
	}
	if p.state != nil {
		p.state.Abort()
	}
	if p.http != nil {
		p.http.abort()
	}
	p.releaseStateLock()
}

func (p *preparedIncrementalGraphPublication) releaseStateLock() {
	if p == nil || !p.stateLocked {
		return
	}
	p.stateLocked = false
	p.state.runtime.state.mu.Unlock()
}

type preparedIncrementalStateCommit struct {
	runtime            *incrementalRenderSession
	base               *incrementalStateSnapshot
	snapshot           *incrementalStateSnapshot
	http               *preparedHTTPInputCommit
	detached           bool
	published          bool
	committed          bool
	released           bool
	baseCacheCommitted bool
}

type preparedIncrementalHTTPLeaseOwnershipCommit struct {
	runtime   *incrementalRenderSession
	http      *preparedHTTPInputCommit
	published bool
	committed bool
	released  bool
}

func (c *preparedIncrementalHTTPLeaseOwnershipCommit) validatePublication() error {
	if c == nil || c.released || c.published || c.runtime == nil || c.http == nil {
		return errors.New("prepared incremental HTTP lease ownership publication is invalid")
	}
	return c.http.validatePublication()
}

func (c *preparedIncrementalHTTPLeaseOwnershipCommit) validatePublishedPublication() error {
	if c == nil || c.released || !c.published || c.runtime == nil || c.http == nil {
		return errors.New("published incremental HTTP lease ownership publication is invalid")
	}
	return c.http.validatePublishedPublication()
}

func (c *preparedIncrementalHTTPLeaseOwnershipCommit) commitPublishedPublication() error {
	if c == nil {
		return errors.New("published incremental HTTP lease ownership publication is invalid")
	}
	if c.committed {
		return nil
	}
	if err := c.validatePublishedPublication(); err != nil {
		return err
	}
	if err := c.http.commitPublishedPublication(); err != nil {
		return err
	}
	c.committed = true
	return nil
}

func (c *preparedIncrementalStateCommit) validatePublication() error {
	if c == nil || c.released || c.detached || c.runtime == nil || c.runtime.state == nil ||
		c.snapshot == nil || c.http == nil {
		return errors.New("prepared incremental state publication is invalid")
	}
	if err := c.validateSnapshotAuthentication(); err != nil {
		return err
	}
	return c.http.validatePublication()
}

func (c *preparedIncrementalStateCommit) validateDetachedPublication() error {
	if c == nil || c.released || !c.detached || c.runtime == nil || c.runtime.state == nil || c.snapshot == nil ||
		c.http != nil {
		return errors.New("prepared detached incremental state publication is invalid")
	}
	return c.validateSnapshotAuthentication()
}

func (c *preparedIncrementalStateCommit) validateSnapshotAuthentication() error {
	return validateIncrementalStateSnapshotAuthentication(c.snapshot)
}

func (c *preparedIncrementalStateCommit) validatePublishedPublication() error {
	if c == nil || c.released || !c.published || c.runtime == nil || c.runtime.state == nil ||
		c.snapshot == nil || c.runtime.state.snapshot != c.snapshot {
		return errors.New("published incremental state publication is invalid")
	}
	if err := validateIncrementalStateSnapshotAuthentication(c.snapshot); err != nil {
		return err
	}
	if err := validateIncrementalStateSnapshotAuthentication(c.base); err != nil {
		return fmt.Errorf("published incremental state rollback: %w", err)
	}
	if c.detached {
		return nil
	}
	if c.http == nil {
		return errors.New("published incremental state publication has no HTTP ownership")
	}
	return c.http.validatePublishedPublication()
}

func (c *preparedIncrementalStateCommit) commitPublishedPublication() error {
	if c == nil {
		return errors.New("published incremental state publication is invalid")
	}
	if c.committed {
		return nil
	}
	if err := c.validatePublishedPublication(); err != nil {
		return err
	}
	if !c.detached {
		if err := c.http.commitPublishedPublication(); err != nil {
			return err
		}
	}
	c.committed = true
	return nil
}

func (r *incrementalRenderSession) prepareHTTPLeaseOwnershipCommit() (
	*preparedIncrementalHTTPLeaseOwnershipCommit,
	error,
) {
	r.releaseMu.Lock()
	if r.released {
		r.releaseMu.Unlock()
		return nil, errors.New("incremental render inputs were already released")
	}
	r.httpMu.Lock()
	retained := make(map[uint64]struct{}, len(r.httpRetained))
	for id := range r.httpRetained {
		retained[id] = struct{}{}
	}
	deltas := make(map[uint64]httpRefDelta, len(r.httpRefDeltas))
	for id, delta := range r.httpRefDeltas {
		deltas[id] = delta
	}
	var rebuild *iradix.Tree[*iradix.Tree[incrementalHTTPEffect]]
	if r.cold {
		rebuild = r.httpEffects.Clone().Commit()
	}
	preparedHTTP, err := r.state.prepareHTTPInputCommit(retained, deltas, rebuild, true)
	if err != nil {
		r.httpMu.Unlock()
		r.releaseMu.Unlock()
		return nil, err
	}
	return &preparedIncrementalHTTPLeaseOwnershipCommit{runtime: r, http: preparedHTTP}, nil
}

func (c *preparedIncrementalHTTPLeaseOwnershipCommit) Publish() {
	if c == nil || c.released {
		return
	}
	c.http.Publish()
	c.published = true
}

func (c *preparedIncrementalHTTPLeaseOwnershipCommit) Release() {
	if c == nil || c.released {
		return
	}
	if !c.published || !c.committed {
		panic("incremental HTTP lease ownership was released before commit")
	}
	c.runtime.httpRetained = nil
	c.runtime.httpKnown = nil
	c.runtime.httpRefDeltas = nil
	c.runtime.released = true
	c.http.Release()
	c.released = true
	c.runtime.httpMu.Unlock()
	c.runtime.releaseMu.Unlock()
}

func (c *preparedIncrementalHTTPLeaseOwnershipCommit) Abort() {
	if c == nil || c.released {
		return
	}
	c.http.Abort()
	c.released = true
	c.runtime.httpMu.Unlock()
	c.runtime.releaseMu.Unlock()
}

func (r *incrementalRenderSession) prepareStateCommit(
	retired []incremental.InputKey,
	active httpstore.ActiveLeaseToken,
) (*preparedIncrementalStateCommit, error) {
	r.releaseMu.Lock()
	if r.released {
		r.releaseMu.Unlock()
		return nil, errors.New("incremental render inputs were already released")
	}
	snapshot, committedEffects, err := r.prepareStateSnapshot(retired, active)
	if err != nil {
		r.releaseMu.Unlock()
		return nil, err
	}

	r.httpMu.Lock()
	retained := make(map[uint64]struct{}, len(r.httpRetained))
	for id := range r.httpRetained {
		retained[id] = struct{}{}
	}
	deltas := make(map[uint64]httpRefDelta, len(r.httpRefDeltas))
	for id, delta := range r.httpRefDeltas {
		deltas[id] = delta
	}
	var rebuild *iradix.Tree[*iradix.Tree[incrementalHTTPEffect]]
	if r.cold {
		rebuild = committedEffects
	}
	preparedHTTP, err := r.state.prepareHTTPInputCommit(retained, deltas, rebuild, true)
	if err != nil {
		r.httpMu.Unlock()
		r.releaseMu.Unlock()
		return nil, err
	}
	r.mu.Lock()
	baseCacheCommitted := r.exactCycleCacheCommitted
	r.mu.Unlock()
	return &preparedIncrementalStateCommit{
		runtime: r, base: r.base, snapshot: snapshot, http: preparedHTTP,
		baseCacheCommitted: baseCacheCommitted,
	}, nil
}

func (r *incrementalRenderSession) prepareDetachedStateCommit(
	retired []incremental.InputKey,
	active httpstore.ActiveLeaseToken,
) (*preparedIncrementalStateCommit, error) {
	snapshot, _, err := r.prepareStateSnapshot(retired, active)
	if err != nil {
		return nil, err
	}
	r.mu.Lock()
	baseCacheCommitted := r.exactCycleCacheCommitted
	r.mu.Unlock()
	prepared := &preparedIncrementalStateCommit{
		runtime: r, base: r.base, snapshot: snapshot, detached: true,
		baseCacheCommitted: baseCacheCommitted,
	}
	if err := prepared.validateDetachedPublication(); err != nil {
		return nil, err
	}
	return prepared, nil
}

func (r *incrementalRenderSession) prepareStateSnapshot(
	retired []incremental.InputKey,
	active httpstore.ActiveLeaseToken,
) (*incrementalStateSnapshot, *iradix.Tree[*iradix.Tree[incrementalHTTPEffect]], error) {
	if err := r.finalizeStatusPatchPlanBootstrap(); err != nil {
		return nil, nil, err
	}
	if err := r.finalizePreparedPlanBootstrap(); err != nil {
		return nil, nil, err
	}
	for _, key := range retired {
		if _, resourceInput := parseResourceInputKey(key); resourceInput {
			if err := r.catalogDelete(key); err != nil {
				return nil, nil, err
			}
		}
	}
	if err := r.pruneUnreferencedResourceCursors(); err != nil {
		return nil, nil, err
	}
	for group, changed := range r.groupChanged {
		if changed && !r.requested[group] {
			r.groupReady[group] = false
		}
	}
	committedEffects := r.httpEffects.Commit()
	resultRoot := r.results.Root()
	committedResults := r.results.Commit()
	preparedPlan := r.preparedPlan
	if preparedPlan != nil {
		updatedPlan, planErr := preparedPlan.rebindResultRoot(resultRoot, committedResults.Root())
		if planErr != nil {
			return nil, nil, planErr
		}
		preparedPlan = updatedPlan
	}
	bindingCache := r.bindingCache
	if bindingCache == nil && r.base != nil {
		bindingCache = r.base.bindingCache
	}
	committedCatalog, err := r.catalogCommit()
	if err != nil {
		return nil, nil, err
	}
	snapshot := &incrementalStateSnapshot{
		cursors:      mapsCloneCursors(r.cursors),
		httpCursor:   incrementalHTTPCursor{token: active},
		bindings:     r.bindings.Commit(),
		members:      r.members.Commit(),
		activeGroups: sealIncrementalActiveGroupIndex(r.activeGroups.Commit()),
		retired:      r.retired.Commit(),
		results:      committedResults,
		derived:      r.derived.Commit(),
		httpEffects:  committedEffects,
		catalog:      committedCatalog,
		groupIndexes: cloneGroupIndexes(r.groupIndexes),
		groupReady:   cloneBools(r.groupReady),
		preparedPlan: preparedPlan,
		statusPlan:   r.statusPlan,
		bindingCache: bindingCache,
	}
	authenticateIncrementalStatusPatchPlan(snapshot)
	authenticateIncrementalStateSnapshot(snapshot)
	return snapshot, committedEffects, nil
}

func (c *preparedIncrementalStateCommit) Publish() {
	if c == nil || c.released {
		return
	}
	if c.detached {
		if err := c.validateDetachedPublication(); err != nil {
			panic(err)
		}
		c.runtime.state.snapshot = c.snapshot
		c.runtime.mu.Lock()
		c.runtime.exactCycleCacheCommitted = true
		c.runtime.mu.Unlock()
		c.published = true
		return
	}
	if err := c.validatePublication(); err != nil {
		panic(err)
	}
	r := c.runtime
	c.http.Publish()
	r.state.snapshot = c.snapshot
	r.mu.Lock()
	r.exactCycleCacheCommitted = true
	r.mu.Unlock()
	c.published = true
}

func (c *preparedIncrementalStateCommit) Release() {
	if c == nil || c.released {
		return
	}
	if !c.published || !c.committed {
		panic("incremental state publication was released before commit")
	}
	if c.detached {
		c.released = true
		return
	}
	c.runtime.httpRetained = nil
	c.runtime.httpKnown = nil
	c.runtime.httpRefDeltas = nil
	c.runtime.released = true
	c.http.Release()
	c.released = true
	c.runtime.httpMu.Unlock()
	c.runtime.releaseMu.Unlock()
}

func (c *preparedIncrementalStateCommit) Abort() {
	if c == nil || c.released {
		return
	}
	if c.detached {
		if c.published {
			c.runtime.state.snapshot = c.base
			c.runtime.mu.Lock()
			c.runtime.exactCycleCacheCommitted = c.baseCacheCommitted
			c.runtime.mu.Unlock()
		}
		c.released = true
		return
	}
	if c.published {
		c.runtime.state.snapshot = c.base
		c.runtime.mu.Lock()
		c.runtime.exactCycleCacheCommitted = c.baseCacheCommitted
		c.runtime.mu.Unlock()
	}
	c.http.Abort()
	c.released = true
	c.runtime.httpMu.Unlock()
	c.runtime.releaseMu.Unlock()
}

func (r *incrementalRenderSession) commitWithoutCache(
	ctx context.Context,
	prepare func(context.Context) error,
	publish func(),
	validate,
	commit func() error,
	abort,
	release func(),
	publications incrementalCommitPublications,
) (commitErr error) {
	published := false
	defer func() {
		if recovered := recover(); recovered != nil {
			commitErr = fmt.Errorf("publishing render inputs panicked: %v", recovered)
		}
		if commitErr != nil && published {
			commitErr = errors.Join(commitErr, callRequiredIncrementalPublication(abort))
		}
	}()
	verified, err := r.verifyResources(ctx, nil)
	if r.graphSession != nil {
		r.graphSession.Abort()
	}
	if err != nil {
		return fmt.Errorf("verifying incremental render inputs: %w", err)
	}
	if !verified {
		return incremental.ErrRevisionConflict
	}
	if err := prepare(ctx); err != nil {
		return fmt.Errorf("preparing render inputs: %w", err)
	}
	if cause := context.Cause(ctx); cause != nil {
		return cause
	}
	if err := r.finishHTTPInputs(false, nil); err != nil {
		return fmt.Errorf("releasing incremental HTTP inputs: %w", err)
	}
	if cause := context.Cause(ctx); cause != nil {
		return cause
	}
	if err := callRequiredIncrementalPublication(publications.prepare); err != nil {
		return err
	}
	published = true
	publish()
	if err := validate(); err != nil {
		return err
	}
	if err := callRequiredIncrementalPublication(publications.validate); err != nil {
		return err
	}
	if err := commit(); err != nil {
		return err
	}
	if err := callRequiredIncrementalPublication(publications.commit); err != nil {
		return err
	}
	if err := callRequiredIncrementalPublication(release); err != nil {
		return err
	}
	published = false
	if err := callRequiredIncrementalPublication(publications.release); err != nil {
		return err
	}
	return nil
}

func mustSucceedIncrementalPublication(err error) {
	if err != nil {
		panic(err)
	}
}

func mustPublishRequiredIncrementalPublication(publication func()) {
	if err := callRequiredIncrementalPublication(publication); err != nil {
		panic(requiredRenderPublicationPanic{err: err})
	}
}

func callRequiredIncrementalPublication(publication func()) (err error) {
	if publication == nil {
		return nil
	}
	defer func() {
		if recovered := recover(); recovered != nil {
			if failure, ok := recovered.(requiredRenderPublicationPanic); ok {
				err = fmt.Errorf("%w: %w", errRequiredRenderPublication, failure.err)
				return
			}
			err = fmt.Errorf("%w: publication panicked: %v", errRequiredRenderPublication, recovered)
		}
	}()
	publication()
	return nil
}

func prepareHTTPInputCommit(
	ctx context.Context,
	transaction RenderInputTransaction,
	observations []httpstore.ObservationToken,
	active *httpstore.ActiveLeaseCommit,
	component *controllerhttpstore.Component,
	preserveRefreshers bool,
) (*controllerhttpstore.PreparedInputCommit, error) {
	if transaction == nil {
		if len(observations) == 0 && active == nil {
			return &controllerhttpstore.PreparedInputCommit{}, nil
		}
		if component == nil {
			return nil, errors.New("incremental HTTP proofs have no source store")
		}
		return component.PrepareObservationCommitWithActiveLeases(ctx, observations, active)
	}
	httpTransaction, ok := transaction.(*controllerhttpstore.InputTransaction)
	if !ok {
		return nil, fmt.Errorf("render input transaction %T has no atomic preparation protocol", transaction)
	}
	if preserveRefreshers {
		if active != nil {
			return nil, errors.New("incremental cache publication cannot preserve HTTP refreshers")
		}
		return httpTransaction.PrepareCommitPreservingRefreshers(ctx, observations)
	}
	return httpTransaction.PrepareCommitWithObservationsAndActiveLeases(ctx, observations, active)
}

func (r *incrementalRenderSession) httpObservationProofs() []httpstore.ObservationToken {
	r.mu.Lock()
	fullCold := r.fullCold
	r.mu.Unlock()
	r.httpMu.Lock()
	proofs := make([]httpstore.ObservationToken, 0, len(r.httpProofs))
	if !fullCold {
		keys := make([]incremental.InputKey, 0, len(r.httpProofs))
		for key := range r.httpProofs {
			keys = append(keys, key)
		}
		slices.SortFunc(keys, func(left, right incremental.InputKey) int {
			return strings.Compare(left.Opaque(), right.Opaque())
		})
		for _, key := range keys {
			proofs = append(proofs, r.httpProofs[key])
		}
	}
	r.httpMu.Unlock()
	if r.httpWrapper == nil {
		return proofs
	}
	snapshots, _ := r.httpWrapper.ContentSnapshots()
	for index := range snapshots {
		proof := snapshots[index].ObservationToken()
		if proof.Valid() {
			proofs = append(proofs, proof)
		}
	}
	return proofs
}

func (r *incrementalRenderSession) abort() {
	defer r.releaseRenderFrames()
	if err := r.finishDeferredCachePublication(false); err != nil && r.loggerContext.logger != nil {
		r.loggerContext.logger.Error("Incremental deferred publication cleanup failed", "error", err)
	}
	if r.graphSession != nil {
		r.graphSession.Abort()
	}
	if err := r.finishHTTPInputs(false, nil); err != nil && r.loggerContext.logger != nil {
		r.loggerContext.logger.Error("Incremental render cleanup failed", "error", err)
	}
}

func (r *incrementalRenderSession) releaseRenderFrames() {
	r.releaseResourceFrames()
	r.releasePublicationFrames()
}

func (r *incrementalRenderSession) finishHTTPInputs(
	commit bool,
	committed *iradix.Tree[*iradix.Tree[incrementalHTTPEffect]],
) error {
	r.releaseMu.Lock()
	defer r.releaseMu.Unlock()
	if r.released {
		return nil
	}
	r.httpMu.Lock()
	defer r.httpMu.Unlock()
	retained := make(map[uint64]struct{}, len(r.httpRetained))
	for id := range r.httpRetained {
		retained[id] = struct{}{}
	}
	deltas := make(map[uint64]httpRefDelta, len(r.httpRefDeltas))
	for id, delta := range r.httpRefDeltas {
		deltas[id] = delta
	}
	var rebuild *iradix.Tree[*iradix.Tree[incrementalHTTPEffect]]
	if commit && r.cold {
		rebuild = committed
	}
	if err := r.state.finishHTTPInputs(retained, deltas, rebuild, commit); err != nil {
		return err
	}
	r.httpRetained = nil
	r.httpKnown = nil
	r.httpRefDeltas = nil
	r.released = true
	return nil
}

type coldIncrementalRenderer struct {
	state            *incrementalRenderState
	baseContext      map[string]any
	resourceErrors   *rendercontext.ResourceErrorCollector
	bindingPlan      *incrementalBindingPlan
	bindingPlanExact bool
	renderMode       rendercontext.RenderMode
	resourceView     rendercontext.StoreSnapshotView
	// resourcesValue is the per-render facade shared by every instance that
	// does not derive a resource. See sharedResourcesValue.
	resourcesValue any
	stores         map[string]stores.Store
	loggerContext  incrementalLoggerContext
	http           templating.HTTPFetcher
	staged         map[coldIncrementalInstanceKey]incrementalComponentResult

	mu                    sync.Mutex
	transitionMu          sync.Mutex
	instances             map[string][]incrementalInstanceResult
	outputs               map[string]map[string]string
	groupIndexes          map[string]*incrementalGroupIndex
	calls                 map[string][]incrementalCall
	scopedCalls           map[string]map[string][]incrementalCall
	callStatuses          map[string]map[string]incrementalScopeCallStatus
	valueAccesses         map[string]int
	requested             map[string]bool
	backendPlanReady      bool
	statusReplayed        bool
	transitionTime        string
	publicationGeneration *incrementalPublicationSnapshotGeneration
	publicationAuthority  *incrementalPublicationSnapshotAuthority
}

type coldIncrementalInstanceKey struct {
	component string
	source    string
	namespace string
	name      string
}

func (r *coldIncrementalRenderer) incrementalTransitionTime(ctx context.Context) (string, error) {
	r.transitionMu.Lock()
	defer r.transitionMu.Unlock()
	if r.transitionTime != "" {
		return r.transitionTime, nil
	}
	value, err := r.state.sampleTransitionTime(ctx)
	if err != nil {
		return "", err
	}
	r.transitionTime = value
	return value, nil
}

func newColdIncrementalRenderer(
	ctx context.Context,
	state *incrementalRenderState,
	provider stores.StoreProvider,
	mode rendercontext.RenderMode,
	baseContext map[string]any,
	resourceErrors *rendercontext.ResourceErrorCollector,
	loggerContext incrementalLoggerContext,
) (*coldIncrementalRenderer, error) {
	bindings, _, bindingPlanExact, err := state.prepareBindingPlan(ctx, baseContext)
	if err != nil {
		return nil, err
	}
	resourceView, storesByName, err := newColdIncrementalResourceView(
		ctx,
		state.config,
		bindings.required(state.required),
		provider,
	)
	if err != nil {
		return nil, err
	}
	if err := addColdIncrementalControllerResources(ctx, baseContext, resourceView, storesByName); err != nil {
		return nil, err
	}
	return newColdIncrementalRendererWithInputs(
		ctx,
		state,
		mode,
		baseContext,
		resourceErrors,
		loggerContext,
		bindings,
		bindingPlanExact,
		resourceView,
		storesByName,
	)
}

func newPinnedColdIncrementalRenderer(
	ctx context.Context,
	runtime *incrementalRenderSession,
	baseContext map[string]any,
	resourceErrors *rendercontext.ResourceErrorCollector,
	loggerContext incrementalLoggerContext,
) (*coldIncrementalRenderer, error) {
	if runtime == nil || runtime.state == nil || runtime.bindingPlan == nil {
		return nil, errors.New("pinned cold incremental render requires an exact session")
	}
	renderer, err := newColdIncrementalRendererWithInputs(
		ctx,
		runtime.state,
		runtime.renderMode,
		baseContext,
		resourceErrors,
		loggerContext,
		runtime.bindingPlan,
		runtime.bindingPlanExact,
		&incrementalPinnedResourceView{session: runtime},
		runtime.stores,
	)
	if err != nil {
		return nil, err
	}
	renderer.transitionTime = runtime.transitionTime
	return renderer, nil
}

func newColdIncrementalRendererWithInputs(
	ctx context.Context,
	state *incrementalRenderState,
	mode rendercontext.RenderMode,
	baseContext map[string]any,
	resourceErrors *rendercontext.ResourceErrorCollector,
	loggerContext incrementalLoggerContext,
	bindings *incrementalBindingPlan,
	bindingPlanExact bool,
	resourceView rendercontext.StoreSnapshotView,
	storesByName map[string]stores.Store,
) (*coldIncrementalRenderer, error) {
	if state.configErr != nil {
		return nil, state.configErr
	}
	derived, _ := baseContext[templating.ResourceDeriverContextName].(*rendercontext.DerivedResourceView)
	if derived == nil {
		return nil, errors.New("cold incremental render requires a derived resource view")
	}
	coldContext := make(map[string]any, len(baseContext)+1)
	for name, value := range baseContext {
		coldContext[name] = value
	}
	var httpFetcher templating.HTTPFetcher
	if baseHTTP, ok := baseContext[incrementalHTTPContextName].(templating.HTTPFetcher); ok && baseHTTP != nil {
		httpFetcher = &strictIncrementalHTTPFetcher{base: baseHTTP}
		coldContext[incrementalHTTPContextName] = httpFetcher
	}
	renderer := &coldIncrementalRenderer{
		state:            state,
		baseContext:      coldContext,
		resourceErrors:   resourceErrors,
		bindingPlan:      bindings,
		bindingPlanExact: bindingPlanExact,
		renderMode:       mode,
		resourceView:     resourceView,
		stores:           storesByName,
		loggerContext:    loggerContext,
		http:             httpFetcher,
		staged:           map[coldIncrementalInstanceKey]incrementalComponentResult{},
		instances:        map[string][]incrementalInstanceResult{},
		outputs:          map[string]map[string]string{},
		groupIndexes:     map[string]*incrementalGroupIndex{},
		calls:            map[string][]incrementalCall{},
		scopedCalls:      map[string]map[string][]incrementalCall{},
		callStatuses:     map[string]map[string]incrementalScopeCallStatus{},
		valueAccesses:    map[string]int{},
		requested:        map[string]bool{},
	}
	renderer.publicationGeneration, renderer.publicationAuthority = newIncrementalPublicationSnapshotGeneration()
	if err := renderer.prepareDerivedStage(ctx); err != nil {
		return nil, fmt.Errorf("preparing cold incremental derived resources: %w", err)
	}
	derived.Freeze()
	return renderer, nil
}

func (r *coldIncrementalRenderer) RenderIncremental(ctx context.Context, name string) (string, error) {
	fragment, err := r.RenderIncrementalTextFragment(ctx, name)
	if err != nil {
		return "", err
	}
	return materializeIncrementalTextFragment(fragment)
}

func (r *coldIncrementalRenderer) RenderIncrementalTextFragment(
	ctx context.Context,
	name string,
) (templating.TextFragment, error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	component, ok := r.state.components[name]
	if !ok {
		return nil, fmt.Errorf("incremental component %q is not configured", name)
	}
	scope, _ := templating.IncrementalScope(ctx)
	if err := validateIncrementalBackendPlanScope(&component, scope); err != nil {
		return nil, err
	}
	r.calls, r.scopedCalls, r.callStatuses = recordIncrementalCall(
		r.calls,
		r.scopedCalls,
		r.callStatuses,
		component.group,
		r.state.groups[component.group],
		incrementalCall{scope: scope, component: name},
	)
	if component.backendPlan && !r.backendPlanReady {
		if err := r.prepareBackendPlans(ctx); err != nil {
			return nil, err
		}
	}
	index, err := r.renderGroup(ctx, component.group)
	if err != nil {
		return nil, err
	}
	if !r.requested[component.group] {
		if err := r.replayGroupEvents(component.group); err != nil {
			return nil, err
		}
		r.requested[component.group] = true
	}
	if component.backendPlan {
		return incrementalStringFragment(r.outputs[component.group][name]), nil
	}
	return index.outputContent(name)
}

func (r *coldIncrementalRenderer) replayGroupEvents(group string) error {
	for index := range r.instances[group] {
		if err := r.replayEvents(&r.instances[group][index].result); err != nil {
			return err
		}
	}
	return nil
}

func (r *coldIncrementalRenderer) renderGroup(ctx context.Context, group string) (*incrementalGroupIndex, error) {
	if index := r.groupIndexes[group]; index != nil {
		return index, nil
	}
	scope, _ := templating.IncrementalScope(ctx)
	if err := r.requireGroupDependencies(group, scope); err != nil {
		return nil, err
	}
	instances, err := r.resolveGroupInstances(ctx, group)
	if err != nil {
		return nil, err
	}
	index := newIncrementalGroupIndex()
	for instanceIndex := range instances {
		if err := validateIncrementalPublicationResultGroup(&instances[instanceIndex].result, group); err != nil {
			return nil, err
		}
		index, err = index.replace(&instances[instanceIndex], nil)
		if err != nil {
			return nil, err
		}
	}
	r.groupIndexes[group] = index
	if r.outputs[group] == nil {
		r.outputs[group] = make(map[string]string)
	}
	return index, nil
}

func (r *coldIncrementalRenderer) resolveGroupInstances(
	ctx context.Context,
	group string,
) ([]incrementalInstanceResult, error) {
	if instances, resolved := r.instances[group]; resolved {
		return instances, nil
	}
	instances := []incrementalInstanceResult{}
	for index := range r.state.groups[group] {
		definition := &r.state.groups[group][index]
		resolved, err := r.resolveComponentInstances(ctx, definition)
		if err != nil {
			return nil, err
		}
		instances = append(instances, resolved...)
	}
	r.instances[group] = instances
	return instances, nil
}

// sharedResourcesValue returns the resources facade for one instance.
//
// The facade is expensive to build — one adapter per watched resource, each
// carrying reflect.MakeFunc trampolines — and depends only on the stores and
// the resource view, both fixed for a render. What varies per instance is the
// immutable-input context, which coldIncrementalResourceView now serves
// dynamically, so the facade is built once and re-pointed rather than rebuilt.
//
// A component that derives a resource renders against a different view and
// cannot share it.
func (r *coldIncrementalRenderer) sharedResourcesValue(
	componentCtx context.Context,
	componentDerived *rendercontext.DerivedResourceView,
	derivedIsInstanceSpecific bool,
) any {
	view, reusable := r.resourceView.(*coldIncrementalResourceView)
	if !reusable || derivedIsInstanceSpecific {
		return r.state.incrementalResourcesValue(
			componentCtx, r.stores, r.resourceErrors, r.resourceView,
			componentDerived, r.loggerContext,
		)
	}
	view.instanceContext = componentCtx
	if r.resourcesValue == nil {
		r.resourcesValue = r.state.incrementalResourcesValue(
			componentCtx, r.stores, r.resourceErrors, r.resourceView,
			componentDerived, r.loggerContext,
		)
	}
	return r.resourcesValue
}

func (r *coldIncrementalRenderer) resolveComponentInstances(
	ctx context.Context,
	component *incrementalComponent,
) ([]incrementalInstanceResult, error) {
	if component.resourceProjection {
		return r.resolveResourceProjectionInstances(ctx, component)
	}
	instances := []incrementalInstanceResult{}
	for _, binding := range r.bindingPlan.byComponent[component.name] {
		items, err := r.resourceView.List(binding.source, nil)
		if err != nil {
			return nil, fmt.Errorf("listing cold incremental source %q: %w", binding.source, err)
		}
		for _, rawItem := range items {
			if err := ctx.Err(); err != nil {
				return nil, err
			}
			instance, err := r.resolveInstance(ctx, component, binding, rawItem)
			if err != nil {
				return nil, err
			}
			instances = append(instances, instance)
		}
	}
	return instances, nil
}

func (r *coldIncrementalRenderer) resolveResourceProjectionInstances(
	ctx context.Context,
	component *incrementalComponent,
) ([]incrementalInstanceResult, error) {
	bindings := r.bindingPlan.byComponent[component.name]
	instances := make([]incrementalInstanceResult, 0, len(bindings))
	for _, binding := range bindings {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		projection, err := incrementalResourceProjectionForBinding(binding)
		if err != nil {
			return nil, fmt.Errorf(
				"incremental component %q binding %q: %w",
				component.name,
				binding.source,
				err,
			)
		}
		namespace, name, ok := incrementalResourceProjectionIdentity(projection)
		if !ok {
			return nil, fmt.Errorf(
				"incremental component %q binding %q has invalid projection identity",
				component.name,
				binding.source,
			)
		}
		items, err := r.resourceView.Get(binding.source, r.stores[binding.source], projection.Keys...)
		if err != nil {
			return nil, fmt.Errorf(
				"reading cold incremental component %q resource projection: %w",
				component.name,
				err,
			)
		}
		recorder, err := newIncrementalResourceProjectionRecorder(
			component,
			binding.source,
			namespace,
			name,
			projection,
			items,
			r.publicationGeneration,
		)
		if err != nil {
			return nil, err
		}
		result, _, err := recorder.validatedResult(
			component,
			binding.source,
			namespace,
			name,
			"",
		)
		if err != nil {
			return nil, fmt.Errorf("incremental component %q result: %w", component.name, err)
		}
		instances = append(instances, incrementalInstanceResult{
			component: component.name,
			source:    binding.source,
			namespace: namespace,
			name:      name,
			result:    result,
		})
	}
	return instances, nil
}

func (r *coldIncrementalRenderer) prepareDerivedStage(ctx context.Context) error {
	sources := make([]string, 0, len(r.bindingPlan.owners))
	for source := range r.bindingPlan.owners {
		sources = append(sources, source)
	}
	slices.Sort(sources)
	for _, source := range sources {
		owner := r.bindingPlan.owners[source]
		binding := incrementalBinding{
			component: owner.name,
			source:    source,
			props:     slices.Clone(r.bindingPlan.props[string(bindingKey(owner.name, source))]),
		}
		items, err := r.resourceView.List(source, nil)
		if err != nil {
			return fmt.Errorf("listing cold incremental source %q: %w", source, err)
		}
		for _, item := range items {
			instance, err := r.executeInstance(ctx, &owner, binding, item)
			if err != nil {
				return err
			}
			key := coldInstanceKey(&instance)
			if _, duplicate := r.staged[key]; duplicate {
				return fmt.Errorf("cold incremental derived owner %q repeats source %q %s/%s",
					owner.name, source, instance.namespace, instance.name)
			}
			r.staged[key] = instance.result
			if err := r.replayDerivations(&instance.result); err != nil {
				return err
			}
		}
	}
	return nil
}

func (r *coldIncrementalRenderer) resolveInstance(
	ctx context.Context,
	component *incrementalComponent,
	binding incrementalBinding,
	rawItem any,
) (incrementalInstanceResult, error) {
	item, err := cloneNormalizedColdIncrementalResource(rawItem)
	if err != nil {
		return incrementalInstanceResult{}, fmt.Errorf("reading cold incremental source %q: %w", binding.source, err)
	}
	namespace, name, identified := resourceIdentity(item)
	if !identified {
		return incrementalInstanceResult{}, fmt.Errorf("incremental source %q has an object without metadata.name", binding.source)
	}
	key := coldIncrementalInstanceKey{
		component: component.name,
		source:    binding.source,
		namespace: namespace,
		name:      name,
	}
	if result, staged := r.staged[key]; staged {
		return incrementalInstanceResult{
			component: component.name,
			source:    binding.source,
			namespace: namespace,
			name:      name,
			result:    result,
		}, nil
	}
	return r.executeDecodedInstance(ctx, component, binding, item, namespace, name)
}

func (r *coldIncrementalRenderer) executeInstance(
	ctx context.Context,
	component *incrementalComponent,
	binding incrementalBinding,
	rawItem any,
) (incrementalInstanceResult, error) {
	item, err := cloneNormalizedColdIncrementalResource(rawItem)
	if err != nil {
		return incrementalInstanceResult{}, fmt.Errorf("reading cold incremental source %q: %w", binding.source, err)
	}
	namespace, name, identified := resourceIdentity(item)
	if !identified {
		return incrementalInstanceResult{}, fmt.Errorf("incremental source %q has an object without metadata.name", binding.source)
	}
	return r.executeDecodedInstance(ctx, component, binding, item, namespace, name)
}

func (r *coldIncrementalRenderer) projectedInstanceItem(
	component *incrementalComponent,
	binding incrementalBinding,
	item map[string]any,
	componentDerived *rendercontext.DerivedResourceView,
) (map[string]any, error) {
	if component.deriveResource {
		return item, nil
	}
	if _, derived := r.bindingPlan.owners[binding.source]; !derived {
		return item, nil
	}
	projected, _, err := projectComponentItem(componentDerived, binding.source, item)
	if err != nil {
		return nil, fmt.Errorf(
			"projecting cold incremental component %q item: %w", component.name, err,
		)
	}
	return projected, nil
}

func (r *coldIncrementalRenderer) recorderComponentContext(
	ctx context.Context,
	componentCtx context.Context,
	component *incrementalComponent,
	recorder *incrementalRecorder,
) (context.Context, error) {
	if component.deriveResource {
		componentCtx = templating.WithIncrementalResourceDeriver(componentCtx, recorder.deriver)
	}
	if component.recordEvent {
		componentCtx = templating.WithIncrementalEventRecorder(componentCtx, recorder)
	}
	if component.statusPatch {
		transitionTime, transitionErr := r.incrementalTransitionTime(ctx)
		if transitionErr != nil {
			return nil, fmt.Errorf("sampling incremental transition time: %w", transitionErr)
		}
		componentCtx = templating.WithIncrementalStatusPatchRecorder(componentCtx, recorder)
		componentCtx = templating.WithIncrementalTransitionTime(componentCtx, transitionTime)
	}
	return componentCtx, nil
}

func (r *coldIncrementalRenderer) executeDecodedInstance(
	ctx context.Context,
	component *incrementalComponent,
	binding incrementalBinding,
	item map[string]any,
	namespace, name string,
) (incrementalInstanceResult, error) {
	componentDerived, _ := r.baseContext[templating.ResourceDeriverContextName].(*rendercontext.DerivedResourceView)
	item, err := r.projectedInstanceItem(component, binding, item, componentDerived)
	if err != nil {
		return incrementalInstanceResult{}, err
	}
	active, err := incrementalComponentActive(component, item)
	if err != nil {
		return incrementalInstanceResult{}, fmt.Errorf("evaluating cold incremental component %q activation: %w", component.name, err)
	}
	if !active {
		return incrementalInstanceResult{
			component: component.name,
			source:    binding.source,
			namespace: namespace,
			name:      name,
		}, nil
	}
	propsValue, err := decodeResourceValue(binding.props)
	if err != nil {
		return incrementalInstanceResult{}, err
	}
	props, ok := propsValue.(map[string]any)
	if !ok {
		return incrementalInstanceResult{}, fmt.Errorf("incremental component %q props are not an object", component.name)
	}
	renderSubject, err := coldIncrementalRenderSubject(
		r.baseContext,
		r.renderMode,
		binding.source,
		namespace,
		name,
	)
	if err != nil {
		return incrementalInstanceResult{}, err
	}
	recorder := &incrementalRecorder{
		publicationGeneration: r.publicationGeneration,
		publicationGroup:      component.group,
		publicationOwner: incrementalGroupInstanceID{
			component: component.name,
			source:    binding.source,
			namespace: namespace,
			name:      name,
		},
	}
	if component.backendPlan {
		recorder.plan = newIncrementalBackendPlanRecorder()
	}
	componentCtx := templating.WithIncrementalImmutableInputs(
		templating.WithImmutableResourceInputs(ctx),
		item,
		props,
		renderSubject,
	)
	if component.deriveResource {
		encoded, encodeErr := encodeResourceValue(item)
		if encodeErr != nil {
			return incrementalInstanceResult{}, encodeErr
		}
		recorder.deriver, err = newIncrementalResourceDeriver(binding.source, namespace, name, encoded)
		if err != nil {
			return incrementalInstanceResult{}, err
		}
		componentDerived = recorder.deriver.view
	}
	// Build the resources facade once per render and point it at this
	// instance, instead of rebuilding every adapter and reflect.MakeFunc
	// trampoline per instance — that was 30% of a cold render's allocations
	// for ~18 watched resource types. A component deriving a resource sees a
	// different view, so it still builds its own.
	componentResources := r.sharedResourcesValue(componentCtx, componentDerived, component.deriveResource)
	componentContext := map[string]any{
		incrementalSourceContextName:        binding.source,
		incrementalItemContextName:          item,
		incrementalPropsContextName:         props,
		incrementalRenderSubjectContextName: renderSubject,
		incrementalResourcesContextName:     componentResources,
		incrementalControllerContextName:    incrementalControllerValue(componentCtx, r.baseContext, r.resourceView, false),
		incrementalSharedContextName: templating.NewSharedContributionContext(recorder, &coldIncrementalPublicationSelector{
			ctx: componentCtx, renderer: r, component: component,
		}),
	}
	if r.http != nil {
		componentContext[incrementalHTTPContextName] = r.http
	}
	if recorder.plan != nil {
		componentContext[incrementalPlanRegistryContextName] = recorder.plan
	}
	componentCtx = templating.WithIncrementalImmutableCapabilityInputs(componentCtx, componentResources)
	componentCtx, err = r.recorderComponentContext(ctx, componentCtx, component, recorder)
	if err != nil {
		return incrementalInstanceResult{}, err
	}
	text, err := r.state.engine.RenderIncrementalComponent(componentCtx, component.entryPoint, componentContext)
	if err != nil {
		return incrementalInstanceResult{}, remapIncrementalTemplateError(component.name, component.entryPoint, err)
	}
	result, err := recorder.result(text)
	if err != nil {
		return incrementalInstanceResult{}, fmt.Errorf("incremental component %q: %w", component.name, err)
	}
	if err := validateIncrementalEffects(component, binding.source, namespace, name, &result); err != nil {
		return incrementalInstanceResult{}, err
	}
	return incrementalInstanceResult{
		component: component.name,
		source:    binding.source,
		namespace: namespace,
		name:      name,
		result:    result,
	}, nil
}

func coldInstanceKey(instance *incrementalInstanceResult) coldIncrementalInstanceKey {
	return coldIncrementalInstanceKey{
		component: instance.component,
		source:    instance.source,
		namespace: instance.namespace,
		name:      instance.name,
	}
}

func (r *coldIncrementalRenderer) replayDerivations(result *incrementalComponentResult) error {
	derived, _ := r.baseContext[templating.ResourceDeriverContextName].(*rendercontext.DerivedResourceView)
	if derived == nil && len(result.Derivations) > 0 {
		return errors.New("incremental derived resource view is unavailable")
	}
	for index := range result.Derivations {
		if err := derived.Replay(&result.Derivations[index]); err != nil {
			return err
		}
	}
	return nil
}

func (r *coldIncrementalRenderer) replayEvents(result *incrementalComponentResult) error {
	collector, _ := r.baseContext["recordEventCollector"].(*templating.EventCollector)
	if collector == nil && len(result.Events) > 0 {
		return errors.New("incremental recordEvent collector is unavailable")
	}
	for index := range result.Events {
		event := &result.Events[index]
		if err := collector.Register(event.Namespace, event.Name, event.APIVersion, event.Kind,
			event.Type, event.Reason, event.Message); err != nil {
			return err
		}
	}
	return nil
}

func (r *coldIncrementalRenderer) HasIncrementalCalls() bool {
	for group := range r.calls {
		if len(r.calls[group]) != 0 {
			return true
		}
	}
	return false
}

func (r *coldIncrementalRenderer) ValidateIncrementalCalls() error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if !r.bindingPlanExact {
		bindings, err := r.state.planBindings(context.Background(), r.baseContext)
		if err != nil {
			return err
		}
		if !sameIncrementalBindingPlans(r.bindingPlan, bindings) {
			return incremental.ErrRevisionConflict
		}
	}
	if err := validateIncrementalCallsWithValues(r.state.groups, r.calls, r.valueAccesses); err != nil {
		return err
	}
	if r.statusReplayed {
		return nil
	}
	if err := replayIncrementalStatusPatches(r.baseContext, r.groupIndexes); err != nil {
		return err
	}
	r.statusReplayed = true
	return nil
}
