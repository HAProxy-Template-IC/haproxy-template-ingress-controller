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
	"errors"
	"fmt"
	"slices"
	"time"

	controllerhttpstore "gitlab.com/haproxy-haptic/haptic/pkg/controller/httpstore"
	"gitlab.com/haproxy-haptic/haptic/pkg/controller/names"
	"gitlab.com/haproxy-haptic/haptic/pkg/controller/rendercontext"
	"gitlab.com/haproxy-haptic/haptic/pkg/controller/rendercycle"
	"gitlab.com/haproxy-haptic/haptic/pkg/core/config"
	"gitlab.com/haproxy-haptic/haptic/pkg/templating"
)

// errExactCycleUnavailable reports that exact-cycle state cannot be captured
// for this render; callers publish no candidate and fall back to a full render.
var errExactCycleUnavailable = errors.New("exact cycle state is unavailable")

var errExactCycleRetry = errors.New("exact cycle replay requires a fresh normal render attempt")
var errExactCycleOutputOnlyRetry = errors.New("exact output cycle replay requires a fresh cold render attempt")
var errExactCycleInvalidCandidateRetry = errors.New("invalid exact cycle replay candidate requires a fresh cold render attempt")

type exactCycleCandidateMode uint8

const (
	exactCycleCandidateGraph exactCycleCandidateMode = iota + 1
	exactCycleCandidateOutputOnly
)

type exactCycleReplayPreparer interface {
	PrepareExactCycleReplay([]string) (*templating.ExactCycleReplayProgram, error)
}

func exactCycleRootEntryPoints(cfg *config.Config) []string {
	invocations := exactCycleRootInvocations(cfg)
	result := make([]string, len(invocations))
	for index := range invocations {
		result[index] = invocations[index].Name
	}
	return result
}

func exactCycleRootInvocations(cfg *config.Config) []templating.ExactCycleRootInvocation {
	if cfg == nil {
		return nil
	}
	result := []templating.ExactCycleRootInvocation{{Kind: "main", Name: names.MainTemplateName}}
	appendGroup := func(kind string, roots map[string]struct{}) {
		names := make([]string, 0, len(roots))
		for name := range roots {
			names = append(names, name)
		}
		slices.Sort(names)
		for _, name := range names {
			result = append(result, templating.ExactCycleRootInvocation{Kind: kind, Name: name})
		}
	}
	maps := make(map[string]struct{}, len(cfg.Maps))
	for name := range cfg.Maps {
		maps[name] = struct{}{}
	}
	files := make(map[string]struct{}, len(cfg.Files))
	for name := range cfg.Files {
		files[name] = struct{}{}
	}
	certificates := make(map[string]struct{}, len(cfg.SSLCertificates))
	for name := range cfg.SSLCertificates {
		certificates[name] = struct{}{}
	}
	resources := make(map[string]struct{}, len(cfg.K8sResources))
	for name := range cfg.K8sResources {
		resources[name] = struct{}{}
	}
	appendGroup("map", maps)
	appendGroup("file", files)
	appendGroup("SSL certificate", certificates)
	appendGroup("k8s resource", resources)
	return result
}

type exactCycleCandidateAuthentication struct {
	program       *templating.ExactCycleReplayProgram
	inputs        *templating.ExactCycleReplayInputs
	previous      *exactCyclePreviousOutputs
	resources     *exactCycleResourceObservations
	storeRoots    *exactCycleStoreRoots
	http          *exactCycleHTTPObservations
	incremental   *exactCycleIncrementalObservations
	roots         *exactCycleRootOutputs
	cycle         *rendercycle.Snapshot
	cache         *rendercontext.PreparedRenderCachePublication
	planIdentity  *rendercontext.RenderPlanIdentity
	bindingPlan   string
	requiresRoots bool
	mode          exactCycleCandidateMode
}

type exactCycleCandidate struct {
	program       *templating.ExactCycleReplayProgram
	inputs        *templating.ExactCycleReplayInputs
	previous      *exactCyclePreviousOutputs
	resources     *exactCycleResourceObservations
	storeRoots    *exactCycleStoreRoots
	http          *exactCycleHTTPObservations
	incremental   *exactCycleIncrementalObservations
	roots         *exactCycleRootOutputs
	cycle         *rendercycle.Snapshot
	cache         *rendercontext.PreparedRenderCachePublication
	planIdentity  *rendercontext.RenderPlanIdentity
	bindingPlan   string
	requiresRoots bool
	mode          exactCycleCandidateMode
	auth          exactCycleCandidateAuthentication
	seal          *exactCycleCandidate
}

func newExactCycleCandidate(
	program *templating.ExactCycleReplayProgram,
	inputs *templating.ExactCycleReplayInputs,
	previous *exactCyclePreviousOutputs,
	resources *exactCycleResourceObservations,
	storeRoots *exactCycleStoreRoots,
	httpObservations *exactCycleHTTPObservations,
	incrementalObservations *exactCycleIncrementalObservations,
	roots *exactCycleRootOutputs,
	cycle *rendercycle.Snapshot,
	cache *rendercontext.PreparedRenderCachePublication,
	planIdentity *rendercontext.RenderPlanIdentity,
	bindingPlan string,
	requiresRoots bool,
	mode exactCycleCandidateMode,
) *exactCycleCandidate {
	candidate := &exactCycleCandidate{
		program: program, inputs: inputs, previous: previous, resources: resources,
		storeRoots: storeRoots, http: httpObservations, incremental: incrementalObservations, roots: roots,
		cycle: cycle, cache: cache, planIdentity: planIdentity, requiresRoots: requiresRoots, mode: mode,
		bindingPlan: bindingPlan,
	}
	candidate.auth = exactCycleCandidateAuthentication{
		program: candidate.program, inputs: candidate.inputs, previous: candidate.previous,
		resources: candidate.resources, storeRoots: candidate.storeRoots, http: candidate.http,
		incremental: candidate.incremental, roots: candidate.roots, cycle: candidate.cycle, cache: candidate.cache,
		planIdentity: candidate.planIdentity, requiresRoots: candidate.requiresRoots, mode: candidate.mode,
		bindingPlan: candidate.bindingPlan,
	}
	candidate.seal = candidate
	return candidate
}

func (c *exactCycleCandidate) sealed() bool {
	return c != nil && c.seal == c && c.program != nil && c.inputs != nil && c.previous != nil &&
		c.resources != nil && c.storeRoots != nil && c.http != nil && c.incremental != nil &&
		c.cycle != nil && c.cache != nil
}

func (c *exactCycleCandidate) matchesAuthentication() bool {
	return c.program == c.auth.program && c.inputs == c.auth.inputs &&
		c.previous == c.auth.previous && c.resources == c.auth.resources &&
		c.storeRoots == c.auth.storeRoots && c.http == c.auth.http &&
		c.incremental == c.auth.incremental && c.roots == c.auth.roots && c.cycle == c.auth.cycle &&
		c.cache == c.auth.cache && c.planIdentity == c.auth.planIdentity &&
		c.requiresRoots == c.auth.requiresRoots && c.bindingPlan == c.auth.bindingPlan &&
		c.mode == c.auth.mode
}

func (c *exactCycleCandidate) validMode() bool {
	if c.mode != exactCycleCandidateGraph && c.mode != exactCycleCandidateOutputOnly {
		return false
	}
	return c.mode != exactCycleCandidateOutputOnly || c.requiresRoots
}

func (c *exactCycleCandidate) validate() error {
	if !c.sealed() || !c.matchesAuthentication() || !c.validMode() {
		return errors.New("exact cycle candidate has invalid provenance")
	}
	if _, err := c.inputs.Generation(); err != nil {
		return err
	}
	if err := c.previous.validate(); err != nil {
		return err
	}
	if err := c.resources.validate(); err != nil {
		return err
	}
	if err := c.storeRoots.validate(); err != nil {
		return err
	}
	if err := c.http.validate(); err != nil {
		return err
	}
	if err := c.incremental.validate(); err != nil {
		return err
	}
	if err := c.cycle.ValidateAuthentication(); err != nil {
		return err
	}
	if err := c.cache.ValidateAuthentication(); err != nil {
		return err
	}
	if c.planIdentity != nil {
		return c.planIdentity.ValidateAuthentication()
	}
	return nil
}

func captureExactCycleCandidate(
	program *templating.ExactCycleReplayProgram,
	inputs *templating.ExactCycleReplayInputs,
	bctx *builtRenderingContext,
	session *incrementalRenderSession,
	httpComponent *controllerhttpstore.Component,
	cycle *rendercycle.Snapshot,
	cache *rendercontext.PreparedRenderCachePublication,
	planIdentity *rendercontext.RenderPlanIdentity,
	roots *exactCycleRootOutputs,
) (*exactCycleCandidate, error) {
	if program == nil || inputs == nil || bctx == nil || session == nil ||
		!session.cachePublicationEnabled || cycle == nil || cache == nil {
		return nil, errExactCycleUnavailable
	}
	previous, err := captureExactCyclePreviousOutputs(program, bctx)
	if err != nil {
		return nil, err
	}
	mode := exactCycleCandidateGraph
	if session.exactCycleFullCold() {
		mode = exactCycleCandidateOutputOnly
	}
	resources, storeRoots, incrementalObservations, err := captureExactCycleObservations(session, mode)
	if err != nil || resources == nil {
		return nil, err
	}
	httpObservations, err := captureExactCycleHTTPForCandidate(bctx, session, httpComponent)
	if err != nil {
		return nil, err
	}
	requiresRoots, err := program.RequiresUnchangedInputRoots()
	if err != nil {
		return nil, err
	}
	if mode == exactCycleCandidateOutputOnly {
		requiresRoots = true
	}
	candidate := newExactCycleCandidate(
		program, inputs, previous, resources, storeRoots, httpObservations,
		incrementalObservations, roots, cycle, cache, planIdentity,
		exactCycleBindingPlanState(session.bindingPlan), requiresRoots, mode,
	)
	if err := candidate.validate(); err != nil {
		return nil, err
	}
	return candidate, nil
}

func captureExactCyclePreviousOutputs(
	program *templating.ExactCycleReplayProgram,
	bctx *builtRenderingContext,
) (*exactCyclePreviousOutputs, error) {
	useConfig, err := program.UsesPreviousOutput("currentConfig")
	if err != nil {
		return nil, err
	}
	useFiles, err := program.UsesPreviousOutput("currentFiles")
	if err != nil {
		return nil, err
	}
	currentConfig, currentFiles := bctx.PreviousOutputSources()
	if useConfig && currentConfig == nil || useFiles && currentFiles == nil {
		return nil, errExactCycleUnavailable
	}
	previous := newExactCyclePreviousOutputs(currentConfig, currentFiles, useConfig, useFiles)
	if err := previous.validate(); err != nil {
		return nil, err
	}
	return previous, nil
}

// captureExactCycleObservations returns all-nil observations when the cycle is
// unavailable, so callers test the first result rather than each one.
func captureExactCycleObservations(
	session *incrementalRenderSession,
	mode exactCycleCandidateMode,
) (
	resources *exactCycleResourceObservations,
	storeRoots *exactCycleStoreRoots,
	incrementalObservations *exactCycleIncrementalObservations,
	err error,
) {
	if mode == exactCycleCandidateOutputOnly {
		resources = newEmptyExactCycleResourceObservations()
	} else {
		resources, err = session.captureExactCycleResourceObservations()
		if err != nil || resources == nil {
			return nil, nil, nil, err
		}
	}
	storeRoots, err = session.captureExactCycleStoreRoots()
	if err != nil || storeRoots == nil {
		return nil, nil, nil, err
	}
	if mode == exactCycleCandidateOutputOnly {
		incrementalObservations = newEmptyExactCycleIncrementalObservations()
	} else {
		incrementalObservations, err = session.captureExactCycleIncrementalObservations()
		if err != nil || incrementalObservations == nil {
			return nil, nil, nil, err
		}
	}
	return resources, storeRoots, incrementalObservations, nil
}

func captureExactCycleHTTPForCandidate(
	bctx *builtRenderingContext,
	session *incrementalRenderSession,
	httpComponent *controllerhttpstore.Component,
) (*exactCycleHTTPObservations, error) {
	httpWrapper, err := incrementalHTTPWrapper(bctx.Context)
	if err != nil {
		return nil, err
	}
	httpObservations, replaySnapshots, replayCacheable, err := captureExactCycleHTTPObservationsForSession(
		httpWrapper,
		httpComponent,
		session,
	)
	if err != nil {
		return nil, err
	}
	if httpObservations == nil {
		var transaction *controllerhttpstore.InputTransaction
		if httpWrapper != nil {
			transaction = httpWrapper.InputTransaction()
		}
		if replayCacheable && transaction != nil && transaction.HasCandidates() {
			if err := session.setExactCycleHTTPPublishedLease(replaySnapshots); err != nil {
				return nil, err
			}
		}
		return nil, errExactCycleUnavailable
	}
	if httpState := httpObservations.leaseState(); httpState != nil {
		if err := session.setExactCycleHTTPLease(httpState); err != nil {
			return nil, err
		}
	}
	return httpObservations, nil
}

func (c *exactCycleCandidate) matchesExternalInputs(
	ctx context.Context,
	bctx *builtRenderingContext,
	session *incrementalRenderSession,
	httpComponent *controllerhttpstore.Component,
) (matched, unchangedRoots bool, err error) {
	if err := c.validate(); err != nil {
		return false, false, err
	}
	if bctx == nil || session == nil || !session.cachePublicationEnabled {
		return false, false, nil
	}
	matched, err = c.matchesReplayInputs(bctx)
	if err != nil || !matched {
		return matched, false, err
	}
	storeRootsSame, err := c.storeRoots.matches(session)
	if err != nil {
		return false, false, err
	}
	if c.requiresRoots && !storeRootsSame {
		return false, false, nil
	}
	bindingPlanSame := c.bindingPlan == exactCycleBindingPlanState(session.bindingPlan)
	if c.requiresRoots && !bindingPlanSame {
		return false, false, nil
	}
	if !storeRootsSame {
		matched, err = c.resources.matches(ctx, session)
		if err != nil || !matched {
			return matched, false, err
		}
	}
	matched, httpRootSame, err := c.matchesHTTPInputs(ctx, bctx, session, httpComponent)
	if err != nil || !matched {
		return matched, false, err
	}
	return true, storeRootsSame && httpRootSame && bindingPlanSame, nil
}

func (c *exactCycleCandidate) matchesReplayInputs(bctx *builtRenderingContext) (bool, error) {
	matched, err := c.program.Matches(c.inputs, bctx.Context)
	if err != nil || !matched {
		return matched, err
	}
	currentConfig, currentFiles := bctx.PreviousOutputSources()
	current := newExactCyclePreviousOutputs(
		currentConfig, currentFiles, c.previous.useConfig, c.previous.useFiles,
	)
	return c.previous.matches(current)
}

func (c *exactCycleCandidate) matchesHTTPInputs(
	ctx context.Context,
	bctx *builtRenderingContext,
	session *incrementalRenderSession,
	httpComponent *controllerhttpstore.Component,
) (matched, rootSame bool, err error) {
	httpWrapper, err := incrementalHTTPWrapper(bctx.Context)
	if err != nil {
		return false, false, err
	}
	httpRootSame, err := c.http.sameReplayRoot(httpWrapper, httpComponent)
	if err != nil {
		return false, false, err
	}
	if session.httpLease != nil && session.httpLease.HasChanges() {
		httpRootSame = false
	}
	matched, err = c.http.matches(ctx, httpWrapper, httpComponent, c.requiresRoots)
	if err != nil || !matched {
		return matched, false, err
	}
	return true, httpRootSame, nil
}

func (c *exactCycleCandidate) rebasedSuccessor(
	bctx *builtRenderingContext,
	session *incrementalRenderSession,
	cache *rendercontext.PreparedRenderCachePublication,
) (*exactCycleCandidate, error) {
	if err := c.validate(); err != nil {
		return nil, err
	}
	if bctx == nil || session == nil || cache == nil {
		return nil, errors.New("exact cycle candidate cannot rebase without an active render")
	}
	resources, err := c.resources.rebase(session)
	if resources == nil || err != nil {
		if err != nil && !errors.Is(err, errExactCycleUnavailable) {
			return nil, err
		}
		return nil, errors.New("exact cycle resource observations cannot rebase")
	}
	storeRoots, err := session.captureExactCycleStoreRoots()
	if storeRoots == nil || err != nil {
		if err != nil && !errors.Is(err, errExactCycleUnavailable) {
			return nil, err
		}
		return nil, errors.New("exact cycle store roots cannot rebase")
	}
	httpWrapper, err := incrementalHTTPWrapper(bctx.Context)
	if err != nil {
		return nil, err
	}
	httpObservations, err := c.http.rebaseCommitted(httpWrapper)
	if err != nil {
		return nil, err
	}
	if httpObservations == nil {
		return nil, errors.New("exact cycle HTTP observations cannot rebase")
	}
	successor := newExactCycleCandidate(
		c.program,
		c.inputs,
		c.previous,
		resources,
		storeRoots,
		httpObservations,
		c.incremental,
		c.roots,
		c.cycle,
		cache,
		c.planIdentity,
		exactCycleBindingPlanState(session.bindingPlan),
		c.requiresRoots,
		c.mode,
	)
	if err := successor.validate(); err != nil {
		return nil, err
	}
	return successor, nil
}

func (r *incrementalRenderSession) completeUnchangedExactCycleReplay() error {
	if r == nil || !r.cachePublicationEnabled {
		return errors.New("exact cycle replay cannot publish its incremental state")
	}
	r.renderMu.Lock()
	defer r.renderMu.Unlock()
	if r.statusPlanBootstrapPending || r.preparedPlanBootstrapPending {
		return errors.New("exact cycle replay has an uncommitted incremental bootstrap")
	}
	r.statusPatchesReplayed = true
	return nil
}

func (s *RenderService) tryExactCycleReuse(
	ctx context.Context,
	bctx *builtRenderingContext,
	session *incrementalRenderSession,
	cacheSession *rendercontext.RenderCacheSession,
	inputTransaction RenderInputTransaction,
	attemptInputs *renderAttemptInputs,
	startTime time.Time,
) (*RenderResult, bool, error) {
	candidate := attemptInputs.exactCycle
	if candidate == nil || s.exactCycleProgram == nil || candidate.program != s.exactCycleProgram {
		return nil, false, nil
	}
	if err := candidate.validate(); err != nil {
		s.discardExactCycleCandidate(candidate)
		return nil, false, errExactCycleInvalidCandidateRetry
	}
	retryErr := errExactCycleRetry
	if candidate.mode == exactCycleCandidateOutputOnly {
		retryErr = errExactCycleOutputOnlyRetry
	}
	if candidate.cache != attemptInputs.renderCache {
		return nil, false, retryErr
	}
	matched, unchangedRoots, err := candidate.matchesExternalInputs(
		ctx, bctx, session, s.httpStoreComponent,
	)
	if err != nil {
		return nil, false, err
	}
	if !matched {
		return nil, false, retryErr
	}
	if candidate.mode == exactCycleCandidateOutputOnly && !unchangedRoots {
		return nil, false, errors.New("exact output cycle matched without unchanged input roots")
	}
	if candidate.mode == exactCycleCandidateOutputOnly {
		session.useExactCycleOutputOnlyReplay()
	}
	if err := candidate.applyHTTPLease(session); err != nil {
		return nil, false, err
	}
	replayable, err := confirmExactCycleReplayScope(ctx, session, candidate, unchangedRoots)
	if err != nil {
		return nil, false, err
	}
	if !replayable {
		session.rootReuser = newExactCycleRootReuser(s.exactCycleProgram, s.engine, candidate, session)
		return nil, false, nil
	}
	result, err := s.finishExactCycleReuse(
		ctx, bctx, session, cacheSession, inputTransaction, attemptInputs, candidate, startTime,
	)
	if err != nil {
		return nil, false, err
	}
	if !unchangedRoots && s.logger != nil {
		// Reusing the previous output while a watched store moved underneath is
		// the one replay worth a line: the store advanced its revision, and this
		// render still answered with the output the last one produced. That is
		// correct only if nothing the render reads actually changed, and it is
		// indistinguishable from a missed invalidation without saying so. Silent
		// in the common case, where the roots did not move at all.
		s.logger.Debug("Render reused the previous output while a store root moved",
			"output_only", candidate.mode == exactCycleCandidateOutputOnly)
	}
	return result, true, nil
}

func (c *exactCycleCandidate) applyHTTPLease(session *incrementalRenderSession) error {
	httpState := c.http.leaseState()
	if httpState == nil {
		return nil
	}
	return session.setExactCycleHTTPLease(httpState)
}

func confirmExactCycleReplayScope(
	ctx context.Context,
	session *incrementalRenderSession,
	candidate *exactCycleCandidate,
	unchangedRoots bool,
) (bool, error) {
	if unchangedRoots {
		return true, session.completeUnchangedExactCycleReplay()
	}
	matched, err := candidate.incremental.matches(ctx, session)
	if err != nil {
		return false, err
	}
	if !matched {
		return false, session.resetExactCycleReplayTracking()
	}
	return true, session.completeExactCycleReplayScope()
}

func (s *RenderService) finishExactCycleReuse(
	ctx context.Context,
	bctx *builtRenderingContext,
	session *incrementalRenderSession,
	cacheSession *rendercontext.RenderCacheSession,
	inputTransaction RenderInputTransaction,
	attemptInputs *renderAttemptInputs,
	candidate *exactCycleCandidate,
	startTime time.Time,
) (*RenderResult, error) {
	if resourceErr := bctx.Err(ctx); resourceErr != nil {
		return nil, resourceErr
	}
	if err := cacheSession.ReuseBase(); err != nil {
		return nil, err
	}
	cachePublication, err := cacheSession.Prepare(ctx)
	if err != nil {
		return nil, err
	}
	return s.reusedExactCycleResult(
		inputTransaction, attemptInputs.outputGeneration, candidate, bctx, cachePublication, session, startTime,
	)
}

func (s *RenderService) reusedExactCycleResult(
	inputTransaction RenderInputTransaction,
	generation uint64,
	candidate *exactCycleCandidate,
	bctx *builtRenderingContext,
	cache *rendercontext.PreparedRenderCachePublication,
	session *incrementalRenderSession,
	startTime time.Time,
) (*RenderResult, error) {
	output, err := candidate.cycle.OutputSnapshot()
	if err != nil {
		return nil, err
	}
	configText, err := output.Config()
	if err != nil {
		return nil, err
	}
	artifacts, err := output.ArtifactSnapshot()
	if err != nil {
		return nil, err
	}
	planID, err := output.PlanID()
	if err != nil {
		return nil, err
	}
	checksum, err := output.ContentChecksum()
	if err != nil {
		return nil, err
	}
	counts, err := output.Counts()
	if err != nil {
		return nil, err
	}
	status, err := candidate.cycle.StatusPatchSnapshot()
	if err != nil {
		return nil, err
	}
	events, err := candidate.cycle.RenderedEventSnapshot()
	if err != nil {
		return nil, err
	}
	resources, err := candidate.cycle.RenderedResourceSnapshot()
	if err != nil {
		return nil, err
	}
	inputTransaction, err = s.stageCyclePublication(
		inputTransaction, rendercontext.RenderModeReconcile, generation,
		candidate.planIdentity, candidate.cycle, cache,
	)
	if err != nil {
		return nil, err
	}
	inputTransaction = s.stageExactCycleRebasedCandidatePublication(
		inputTransaction, generation, candidate, bctx, cache, session,
	)
	return &RenderResult{
		CycleSnapshot: candidate.cycle, OutputSnapshot: output, HAProxyConfig: configText,
		AuxiliaryFileSnapshot: artifacts, ContentChecksum: checksum, PlanID: planID,
		StatusPatchSnapshot: status, EventSnapshot: events, RenderedResourceSnapshot: resources,
		DurationMs: time.Since(startTime).Milliseconds(), AuxFileCount: counts.Artifacts,
		CacheState: "replay", CacheBuildMs: s.incremental.cache.LastBuildMs(),
		InputTransaction: inputTransaction, renderCachePublication: cache,
		planIdentity: candidate.planIdentity,
	}, nil
}

// stageExactCycleRebasedCandidatePublication publishes the reused candidate at
// the snapshots this render pinned and matched. Pinning the live store here
// instead let a change that landed mid-commit hide behind an unchanged root.
func (s *RenderService) stageExactCycleRebasedCandidatePublication(
	transaction RenderInputTransaction,
	generation uint64,
	candidate *exactCycleCandidate,
	bctx *builtRenderingContext,
	cache *rendercontext.PreparedRenderCachePublication,
	session *incrementalRenderSession,
) RenderInputTransaction {
	return stageOptionalRenderPublication(transaction, func() {
		successor, err := candidate.rebasedSuccessor(bctx, session, cache)
		if err != nil && s.logger != nil {
			s.logger.Debug("Exact cycle successor could not be rebased", "reason", err)
		}
		if err != nil {
			s.publishExactCycleCandidate(generation, nil)
			return
		}
		s.publishOrDeferExactCycleCandidate(generation, successor, session)
	})
}

func (s *RenderService) stageExactCycleCandidatePublication(
	transaction RenderInputTransaction,
	generation uint64,
	candidate *exactCycleCandidate,
	session *incrementalRenderSession,
) RenderInputTransaction {
	return stageOptionalRenderPublication(transaction, func() {
		s.publishOrDeferExactCycleCandidate(generation, candidate, session)
	})
}

func (s *RenderService) stageExactCycleCandidateCapture(
	transaction RenderInputTransaction,
	generation uint64,
	inputs *templating.ExactCycleReplayInputs,
	bctx *builtRenderingContext,
	session *incrementalRenderSession,
	cycle *rendercycle.Snapshot,
	cache *rendercontext.PreparedRenderCachePublication,
	planIdentity *rendercontext.RenderPlanIdentity,
	roots *exactCycleRootOutputs,
) RenderInputTransaction {
	return stageOptionalRenderPublication(transaction, func() {
		candidate, err := captureExactCycleCandidate(
			s.exactCycleProgram,
			inputs,
			bctx,
			session,
			s.httpStoreComponent,
			cycle,
			cache,
			planIdentity,
			roots,
		)
		if err != nil && !errors.Is(err, errExactCycleUnavailable) && s.logger != nil {
			s.logger.Debug("Exact cycle candidate could not be captured after input commit", "reason", err)
		}
		s.publishOrDeferExactCycleCandidate(generation, candidate, session)
	})
}

func (s *RenderService) publishOrDeferExactCycleCandidate(
	generation uint64,
	candidate *exactCycleCandidate,
	session *incrementalRenderSession,
) {
	if session.deferCachePublication(
		func() {
			if session.exactCycleCandidateCanPublish(candidate) {
				s.publishExactCycleCandidate(generation, candidate)
			}
		},
		func() { s.publishExactCycleCandidate(generation, nil) },
	) {
		s.publishExactCycleCandidate(generation, nil)
		return
	}
	if !session.exactCycleCandidateCanPublish(candidate) {
		candidate = nil
	}
	s.publishExactCycleCandidate(generation, candidate)
}

func (r *incrementalRenderSession) exactCycleFullCold() bool {
	if r == nil {
		return false
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.fullCold
}

func (r *incrementalRenderSession) useExactCycleOutputOnlyReplay() {
	r.mu.Lock()
	r.cachePublishable = false
	r.exactCycleOutputOnlyReplay = true
	r.mu.Unlock()
}

func (r *incrementalRenderSession) exactCycleCandidateCanPublish(candidate *exactCycleCandidate) bool {
	if r == nil || candidate == nil || candidate.validate() != nil {
		return false
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if candidate.mode == exactCycleCandidateOutputOnly {
		return r.fullCold || r.exactCycleOutputOnlyReplay
	}
	return r.exactCycleCacheCommitted
}

func (s *RenderService) publishExactCycleCandidate(
	generation uint64,
	candidate *exactCycleCandidate,
) {
	s.planMu.Lock()
	defer s.planMu.Unlock()
	if generation == 0 || generation != s.publishedOutputGeneration {
		return
	}
	if candidate != nil && candidate.validate() != nil {
		return
	}
	if candidate != nil && (candidate.cycle != s.lastCycleSnapshot || candidate.cache != s.lastRenderCache) {
		return
	}
	s.exactCycleCandidate = candidate
}

func (s *RenderService) discardExactCycleCandidate(candidate *exactCycleCandidate) {
	s.planMu.Lock()
	defer s.planMu.Unlock()
	if s.exactCycleCandidate == candidate {
		s.exactCycleCandidate = nil
	}
}

func exactCycleCaptureError(kind string, err error) error {
	if err == nil {
		return nil
	}
	return fmt.Errorf("capturing exact cycle %s: %w", kind, err)
}
