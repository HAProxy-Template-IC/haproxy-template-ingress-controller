package incremental

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"

	"gitlab.com/haproxy-haptic/haptic/pkg/incremental/internal/immutablevector"
)

const (
	preparedGraphCommitReady uint8 = iota + 1
	preparedGraphCommitPublishing
	preparedGraphCommitPublished
	preparedGraphCommitRevoked
)

const (
	preparedGraphCommitPending uint32 = iota
	preparedGraphCommitAborted
	preparedGraphCommitSucceeded
)

// PreparedGraphCommit is an opaque authenticated replacement-generation draft.
type PreparedGraphCommit struct {
	prepared *preparedGraphCommit
}

type preparedGraphCommit struct {
	seal             *preparedGraphCommit
	graph            *Graph
	session          *Session
	base             *graphGeneration
	generation       *graphGeneration
	baseGeneration   uint64
	targetGeneration uint64
	observations     immutablevector.Root[InputRevision]
	retiredInputs    immutablevector.Root[InputKey]
	draftRoot        *preparedGraphCommitDraftRoot

	gate    sync.Mutex
	state   uint8
	outcome atomic.Uint32
	active  atomic.Bool
}

// PrepareGraphCommit builds and authenticates an immutable replacement draft.
func (s *Session) PrepareGraphCommit(ctx context.Context) (PreparedGraphCommit, error) {
	if err := s.beginPublication(ctx); err != nil {
		return PreparedGraphCommit{}, err
	}
	return s.prepareGraphCommitReady(ctx)
}

func (s *Session) prepareGraphCommitReady(ctx context.Context) (
	prepared PreparedGraphCommit,
	err error,
) {
	if !s.replacement {
		err := s.fail(errors.New(
			"incremental prepared graph commit requires a replacement transaction",
		))
		s.discard()
		return PreparedGraphCommit{}, err
	}
	base, counters, err := s.snapshotPreparedGraphBase()
	if err != nil {
		s.discard()
		return PreparedGraphCommit{}, err
	}
	s.committing = true
	defer func() {
		if recovered := recover(); recovered != nil {
			s.discard()
			prepared = PreparedGraphCommit{}
			err = fmt.Errorf("preparing incremental graph commit panicked: %v", recovered)
		}
	}()
	plan, observations, err := s.prepareReplacementCommitFromCounters(ctx, counters)
	if err != nil {
		s.discard()
		return PreparedGraphCommit{}, fmt.Errorf("preparing incremental graph publication: %w", err)
	}
	if err := ctx.Err(); err != nil {
		s.discard()
		return PreparedGraphCommit{}, err
	}
	if !s.graph.matchesGenerationIdentity(base, s.baseGeneration) {
		s.discard()
		return PreparedGraphCommit{}, ErrCommitConflict
	}
	if err := authenticatePreparedGraphCommitPlan(s.graph, plan, observations); err != nil {
		s.discard()
		return PreparedGraphCommit{}, fmt.Errorf("authenticating incremental graph publication: %w", err)
	}
	observationRoot, err := s.graph.observationAuthority.Own(observations)
	if err != nil {
		s.discard()
		return PreparedGraphCommit{}, fmt.Errorf("authenticating incremental graph observations: %w", err)
	}
	retiredInputRoot, err := s.graph.retiredInputAuthority.Own(plan.retiredInputs)
	if err != nil {
		s.discard()
		return PreparedGraphCommit{}, fmt.Errorf("authenticating incremental graph retirement: %w", err)
	}
	draft := &preparedGraphCommit{
		graph:            s.graph,
		session:          s,
		base:             base,
		generation:       plan.replacementGeneration,
		baseGeneration:   s.baseGeneration,
		targetGeneration: s.targetGeneration,
		observations:     observationRoot,
		retiredInputs:    retiredInputRoot,
		state:            preparedGraphCommitReady,
	}
	draft.seal = draft
	draftRoot, err := newPreparedGraphCommitDraftRoot(draft)
	if err != nil {
		s.discard()
		return PreparedGraphCommit{}, fmt.Errorf("authenticating incremental graph draft: %w", err)
	}
	draft.draftRoot = draftRoot
	s.preparedGraphCommit.Store(draft)
	prepared = PreparedGraphCommit{prepared: draft}
	if err := prepared.validateForState(preparedGraphCommitReady); err != nil {
		s.discard()
		return PreparedGraphCommit{}, err
	}
	return prepared, nil
}

func (s *Session) snapshotPreparedGraphBase() (
	*graphGeneration,
	map[QueryKey]NodeCounters,
	error,
) {
	s.graph.mu.RLock()
	defer s.graph.mu.RUnlock()
	base := s.graph.current
	if !base.valid(s.graph) || base.number != s.baseGeneration {
		return nil, nil, ErrCommitConflict
	}
	counters, err := cloneCommittedCounters(base)
	if err != nil {
		return nil, nil, err
	}
	return base, counters, nil
}

func (s *Session) prepareReplacementCommitFromCounters(
	ctx context.Context,
	counters map[QueryKey]NodeCounters,
) (*graphCommitPlan, []InputRevision, error) {
	prepared := make(chan graphCommitPlanResult, 1)
	go func() {
		plan, err := s.callPrepareReplacementGraphCommitFromCounters(counters)
		prepared <- graphCommitPlanResult{plan: plan, err: err}
	}()
	validationErr := s.validateNodeChanges()
	observations, observationErr := s.commitObservations()
	contextErr := ctx.Err()
	result := <-prepared
	if validationErr != nil {
		return nil, nil, validationErr
	}
	if observationErr != nil {
		return nil, nil, observationErr
	}
	if contextErr != nil {
		return nil, nil, contextErr
	}
	return result.plan, observations, result.err
}

func (s *Session) callPrepareReplacementGraphCommitFromCounters(
	counters map[QueryKey]NodeCounters,
) (plan *graphCommitPlan, err error) {
	defer func() {
		if recovered := recover(); recovered != nil {
			plan = nil
			err = fmt.Errorf("incremental graph preparation panicked: %v", recovered)
		}
	}()
	return s.prepareReplacementGraphCommitFromCounters(counters)
}

func (s *Session) prepareReplacementGraphCommitFromCounters(
	counters map[QueryKey]NodeCounters,
) (*graphCommitPlan, error) {
	inputs, nodes := s.prepareReplacementEntries()
	reverse, err := s.replacementReverseRoots()
	if err != nil {
		return nil, err
	}
	if err := authenticateReplacementReverseRoots(s.graph.reverseAuthority, reverse); err != nil {
		return nil, err
	}
	if err := s.validateReplacementRemovedQueries(reverse); err != nil {
		return nil, err
	}
	retired, err := s.prepareReplacementRetirement(inputs, reverse)
	if err != nil {
		return nil, err
	}
	counters, err = s.applyReplacementCounterDeltas(counters)
	if err != nil {
		return nil, err
	}
	generation, err := newGraphGeneration(
		s.graph,
		s.targetGeneration,
		inputs,
		nodes,
		reverse,
		map[QueryKey]struct{}{},
		counters,
	)
	if err != nil {
		return nil, err
	}
	return &graphCommitPlan{
		generation:            s.targetGeneration,
		replacement:           true,
		replacementGeneration: generation,
		retiredInputs:         retired,
	}, nil
}

func authenticatePreparedGraphCommitPlan(
	graph *Graph,
	plan *graphCommitPlan,
	observations []InputRevision,
) error {
	if graph == nil || plan == nil || !plan.replacement || plan.generation == 0 ||
		!plan.replacementGeneration.valid(graph) ||
		plan.replacementGeneration.number != plan.generation {
		return errors.New("incremental prepared generation has invalid provenance")
	}
	generation := plan.replacementGeneration
	if err := validatePreparedGraphObservations(observations); err != nil {
		return err
	}
	for index, key := range plan.retiredInputs {
		if !validInputKey(key) || (index > 0 && plan.retiredInputs[index-1].value >= key.value) {
			return errors.New("incremental prepared generation retired inputs are not canonical")
		}
		if _, exists := generation.inputs.Root().Get([]byte(key.value)); exists {
			return errors.New("incremental prepared generation retained a retired input")
		}
	}
	return nil
}

func validatePreparedGraphObservations(observations []InputRevision) error {
	for index, observation := range observations {
		if !validInputKey(observation.Key) || !validRevision(observation.Revision) ||
			(index > 0 && observations[index-1].Key.value >= observation.Key.value) {
			return errors.New("incremental prepared generation observations are not canonical")
		}
	}
	return nil
}

// ValidateAuthentication verifies that the draft is live and still targets its exact base.
func (p PreparedGraphCommit) ValidateAuthentication() error {
	return p.validateForState(preparedGraphCommitReady)
}

// ValidateFor verifies authentication and exact session identity.
func (p PreparedGraphCommit) ValidateFor(session *Session) error {
	if err := p.validateForState(preparedGraphCommitReady); err != nil {
		return err
	}
	if p.prepared.session != session {
		return errors.New("incremental prepared graph commit belongs to another session")
	}
	return nil
}

func (p PreparedGraphCommit) validateForState(state uint8) error {
	prepared := p.prepared
	if prepared == nil || prepared.seal != prepared {
		return errors.New("incremental prepared graph commit has invalid provenance")
	}
	prepared.gate.Lock()
	defer prepared.gate.Unlock()
	if err := prepared.validateLocked(state); err != nil {
		return err
	}
	if !prepared.graph.matchesGenerationIdentity(prepared.base, prepared.baseGeneration) {
		return ErrCommitConflict
	}
	return nil
}

func (p *preparedGraphCommit) validateLocked(state uint8) error {
	if p == nil {
		return errors.New("incremental prepared graph commit has invalid provenance")
	}
	switch p.outcome.Load() {
	case preparedGraphCommitAborted, preparedGraphCommitSucceeded:
		return ErrSessionClosed
	case preparedGraphCommitPending:
	default:
		return errors.New("incremental prepared graph commit has invalid outcome")
	}
	if err := p.session.publicationActive(); err != nil {
		return err
	}
	if p.seal != p || p.state != state {
		if p.state == preparedGraphCommitPublished || p.state == preparedGraphCommitRevoked {
			return ErrSessionClosed
		}
		return errors.New("incremental prepared graph commit has invalid provenance")
	}
	if !p.transactionProvenanceValid() {
		return errors.New("incremental prepared graph commit has invalid transaction provenance")
	}
	if err := p.draftRoot.validate(p); err != nil {
		return err
	}
	return nil
}

func (p *preparedGraphCommit) transactionProvenanceValid() bool {
	return p.graph != nil && p.session != nil && p.base != nil && p.generation != nil &&
		p.baseGeneration != ^uint64(0) && p.targetGeneration == p.baseGeneration+1 &&
		p.base.valid(p.graph) && p.base.number == p.baseGeneration &&
		p.generation.valid(p.graph) && p.generation.number == p.targetGeneration &&
		p.session.graph == p.graph && p.session.baseGeneration == p.baseGeneration &&
		p.session.targetGeneration == p.targetGeneration && p.session.preparedGraphCommit.Load() == p
}

// RetiredInputs returns the immutable retirement set prepared with the draft.
func (p PreparedGraphCommit) RetiredInputs() ([]InputKey, error) {
	if err := p.ValidateAuthentication(); err != nil {
		return nil, err
	}
	return p.prepared.retiredInputs.Values(p.prepared.graph.retiredInputAuthority)
}

// Publish verifies exact inputs and atomically installs the prepared generation.
func (p PreparedGraphCommit) Publish(ctx context.Context, verifier RevisionVerifier) error {
	return p.PublishWithPublisher(ctx, verifier, nil)
}

// PublishWithPublisher installs caller state immediately before graph visibility.
func (p PreparedGraphCommit) PublishWithPublisher(
	ctx context.Context,
	verifier RevisionVerifier,
	publish func(),
) error {
	return p.PublishWithPreparedPublisher(ctx, verifier, func([]InputKey) (CommitPublication, error) {
		return CommitPublication{Publish: publish}, nil
	})
}

// PublishWithPreparedPublisher publishes a fully prepared draft with an exact base CAS.
func (p PreparedGraphCommit) PublishWithPreparedPublisher(
	ctx context.Context,
	verifier RevisionVerifier,
	prepare CommitPublicationPreparer,
) (err error) {
	prepared := p.prepared
	if prepared == nil || prepared.seal != prepared {
		return errors.New("incremental prepared graph commit has invalid provenance")
	}
	prepared.gate.Lock()
	defer prepared.gate.Unlock()
	if err := prepared.validateLocked(preparedGraphCommitReady); err != nil {
		return err
	}
	observations, err := prepared.observations.Values(prepared.graph.observationAuthority)
	if err != nil {
		return fmt.Errorf("loading incremental graph observations: %w", err)
	}
	retiredInputs, err := prepared.retiredInputs.Values(prepared.graph.retiredInputAuthority)
	if err != nil {
		return fmt.Errorf("loading incremental graph retirement: %w", err)
	}
	prepared.state = preparedGraphCommitPublishing
	prepared.active.Store(true)
	defer prepared.active.Store(false)
	publication := CommitPublication{}
	publicationPrepared := false
	succeeded := false
	defer func() {
		if recovered := recover(); recovered != nil {
			err = fmt.Errorf("publishing incremental graph commit panicked: %v", recovered)
		}
		if succeeded {
			return
		}
		if abortErr := prepared.rollbackPublication(publicationPrepared, publication); abortErr != nil {
			err = errors.Join(err, abortErr)
		}
	}()
	if verifier == nil {
		return prepared.session.fail(ErrVerifierRequired)
	}
	prepared.graph.commitMu.Lock()
	defer prepared.graph.commitMu.Unlock()
	if !prepared.graph.matchesGenerationIdentity(prepared.base, prepared.baseGeneration) {
		return ErrCommitConflict
	}
	if err := prepared.verifyPreparedInputs(ctx, verifier, observations); err != nil {
		return err
	}
	publication, publicationPrepared, err = prepared.preparePublication(prepare, retiredInputs)
	if err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := prepared.runPublishPhase(publication); err != nil {
		return err
	}

	if err := prepared.installPreparedGeneration(publication); err != nil {
		return err
	}
	prepared.session.retiredInputs = retiredInputs
	prepared.state = preparedGraphCommitPublished
	prepared.session.discard()
	succeeded = true
	return nil
}

// rollbackPublication revokes the prepared commit and runs the caller's abort
// phase; the returned error is joined onto whatever failed the publication.
func (p *preparedGraphCommit) rollbackPublication(
	publicationPrepared bool,
	publication CommitPublication,
) error {
	p.outcome.CompareAndSwap(preparedGraphCommitPending, preparedGraphCommitAborted)
	p.state = preparedGraphCommitRevoked
	p.session.discard()
	if !publicationPrepared || publication.Abort == nil {
		return nil
	}
	return callCommitPublicationPhase("abort", publication.Abort)
}

func (p *preparedGraphCommit) runPublishPhase(publication CommitPublication) error {
	if publication.Publish == nil {
		return nil
	}
	if err := callCommitPublicationPhase("publish", publication.Publish); err != nil {
		return err
	}
	return p.validateLocked(preparedGraphCommitPublishing)
}

func (p *preparedGraphCommit) verifyPreparedInputs(
	ctx context.Context,
	verifier RevisionVerifier,
	observations []InputRevision,
) error {
	p.session.verifying = true
	verifyErr := verifyCommitInputs(
		ctx,
		verifier,
		observations,
	)
	p.session.verifying = false
	if verifyErr != nil {
		return verifyErr
	}
	return p.validateLocked(preparedGraphCommitPublishing)
}

func (p *preparedGraphCommit) preparePublication(
	prepare CommitPublicationPreparer,
	retiredInputs []InputKey,
) (CommitPublication, bool, error) {
	if prepare == nil {
		return CommitPublication{}, false, nil
	}
	publication, err := callCommitPublicationPreparer(prepare, retiredInputs)
	if err != nil {
		return publication, true, fmt.Errorf("preparing incremental publication: %w", err)
	}
	if err := p.validateLocked(preparedGraphCommitPublishing); err != nil {
		return publication, true, err
	}
	return publication, true, nil
}

func (p *preparedGraphCommit) installPreparedGeneration(publication CommitPublication) error {
	p.graph.mu.Lock()
	if !p.graph.matchesGenerationIdentityLocked(p.base, p.baseGeneration) {
		p.graph.mu.Unlock()
		return ErrCommitConflict
	}
	baseAuthentication := p.graph.currentAuthentication
	p.graph.installGenerationLocked(p.generation)
	restore := func() {
		p.graph.current = p.base
		p.graph.currentAuthentication = baseAuthentication
		p.graph.mu.Unlock()
	}
	if err := p.validateLocked(preparedGraphCommitPublishing); err != nil {
		restore()
		return err
	}
	if publication.Complete != nil {
		if err := callCommitPublicationPhase("complete", publication.Complete); err != nil {
			restore()
			return err
		}
	}
	if err := p.validateLocked(preparedGraphCommitPublishing); err != nil {
		restore()
		return err
	}
	if !p.outcome.CompareAndSwap(preparedGraphCommitPending, preparedGraphCommitSucceeded) {
		restore()
		return ErrSessionClosed
	}
	p.session.publicationState.Store(sessionPublicationClosed)
	p.graph.mu.Unlock()
	return nil
}

func callCommitPublicationPreparer(
	prepare CommitPublicationPreparer,
	retired []InputKey,
) (publication CommitPublication, err error) {
	defer func() {
		if recovered := recover(); recovered != nil {
			publication = CommitPublication{}
			err = fmt.Errorf("incremental publication preparation panicked: %v", recovered)
		}
	}()
	return prepare(retired)
}

func callCommitPublicationPhase(phase string, call func()) (err error) {
	defer func() {
		if recovered := recover(); recovered != nil {
			err = fmt.Errorf("incremental publication %s panicked: %v", phase, recovered)
		}
	}()
	call()
	return nil
}

// Abort revokes the draft and discards its speculative session.
func (p PreparedGraphCommit) Abort() error {
	prepared := p.prepared
	if prepared == nil || prepared.seal != prepared {
		return errors.New("incremental prepared graph commit has invalid provenance")
	}
	return prepared.abort()
}

func (p *preparedGraphCommit) abort() error {
	if p == nil || p.seal != p {
		return errors.New("incremental prepared graph commit has invalid provenance")
	}
	if !p.outcome.CompareAndSwap(preparedGraphCommitPending, preparedGraphCommitAborted) {
		return ErrSessionClosed
	}
	p.session.requestPublicationAbort()
	if p.active.Load() {
		return nil
	}
	p.gate.Lock()
	defer p.gate.Unlock()
	p.state = preparedGraphCommitRevoked
	p.session.discard()
	return nil
}

func (g *Graph) matchesGenerationIdentity(base *graphGeneration, number uint64) bool {
	g.mu.RLock()
	defer g.mu.RUnlock()
	return g.matchesGenerationIdentityLocked(base, number)
}

func (g *Graph) matchesGenerationIdentityLocked(base *graphGeneration, number uint64) bool {
	return g.current == base && g.currentValidLocked() && g.current.number == number
}
