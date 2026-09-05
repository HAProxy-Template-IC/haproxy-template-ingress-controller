package incremental

import (
	"errors"
	"fmt"
	"slices"
)

// ExactQueryObservation is an opaque authenticated query-node observation.
// Copies retain the same authority and never contain the query value bytes.
type ExactQueryObservation struct {
	authority *exactQueryObservationAuthority
	key       QueryKey
	root      ExactValueRoot
	changedAt uint64
	deps      []dependency
	inputs    []InputRevision
}

type exactQueryObservationAuthority struct {
	seal             *exactQueryObservationAuthority
	state            *coldExactBatchState
	session          *Session
	graph            *Graph
	baseGeneration   uint64
	targetGeneration uint64
}

func initializeExactQueryObservationAuthority(
	authority *exactQueryObservationAuthority,
	state *coldExactBatchState,
	session *Session,
) {
	*authority = exactQueryObservationAuthority{
		state:            state,
		session:          session,
		graph:            session.graph,
		baseGeneration:   session.baseGeneration,
		targetGeneration: session.targetGeneration,
	}
	authority.seal = authority
}

// ValidateAuthentication verifies that the observation belongs to its live
// cold-batch transaction and carries a well-formed authenticated value root.
func (o *ExactQueryObservation) ValidateAuthentication() error {
	state, err := o.lockState()
	if err != nil {
		return err
	}
	defer state.lifetime.RUnlock()
	return o.validateLocked(state)
}

// ValidateFor verifies authentication and exact query identity.
func (o *ExactQueryObservation) ValidateFor(key QueryKey) error {
	state, err := o.lockState()
	if err != nil {
		return err
	}
	defer state.lifetime.RUnlock()
	if err := o.validateLocked(state); err != nil {
		return err
	}
	if !validQueryKey(key) || o.key != key {
		return errors.New("incremental exact query observation belongs to another query")
	}
	return nil
}

func newExactQueryObservation(
	state *coldExactBatchState,
	key QueryKey,
	entry *nodeEntry,
) (ExactQueryObservation, error) {
	if state == nil || entry == nil {
		return ExactQueryObservation{}, errors.New("incremental exact query observation has invalid provenance")
	}
	observation := ExactQueryObservation{
		authority: &state.observationAuthority,
		key:       key,
		root:      entry.value,
		changedAt: entry.changedAt,
		deps:      entry.deps,
		inputs:    entry.inputs,
	}
	if err := observation.validateLocked(state); err != nil {
		return ExactQueryObservation{}, err
	}
	return observation, nil
}

func (o *ExactQueryObservation) lockState() (*coldExactBatchState, error) {
	authority := o.authority
	if authority == nil || authority.seal != authority || authority.state == nil {
		return nil, errors.New("incremental exact query observation has invalid provenance")
	}
	state := authority.state
	state.lifetime.RLock()
	if err := o.validateAuthorityLocked(state); err != nil {
		state.lifetime.RUnlock()
		return nil, err
	}
	return state, nil
}

func (o *ExactQueryObservation) validateLocked(state *coldExactBatchState) error {
	if err := o.validateAuthorityLocked(state); err != nil {
		return err
	}
	if !validQueryKey(o.key) || o.changedAt == 0 || o.changedAt > o.authority.targetGeneration {
		return errors.New("incremental exact query observation has an invalid node identity")
	}
	if err := o.root.validateOwned(o.authority.graph.valueAuthority, o.key); err != nil {
		return fmt.Errorf("incremental exact query observation root: %w", err)
	}
	if err := validateExactQueryObservationDependencies(o.deps, o.inputs, o.authority.targetGeneration); err != nil {
		return err
	}
	return nil
}

func (o *ExactQueryObservation) validateAuthorityLocked(state *coldExactBatchState) error {
	authority := o.authority
	if authority == nil || authority.seal != authority || authority.state != state ||
		state == nil || state.seal != state || authority != &state.observationAuthority {
		return errors.New("incremental exact query observation has invalid provenance")
	}
	if state.revoked || state.session == nil || state.session.activeColdExactBatch != state {
		return errors.New("incremental exact query observation is no longer active")
	}
	if failure := state.observationFailure.Load(); failure != nil {
		return fmt.Errorf("incremental exact query observation transaction has failed: %w", failure.err)
	}
	if state.failure != nil {
		return fmt.Errorf("incremental exact query observation transaction has failed: %w", state.failure)
	}
	session := state.session
	if authority.session != session || authority.graph == nil || authority.graph != session.graph ||
		authority.baseGeneration != session.baseGeneration ||
		authority.targetGeneration != session.targetGeneration {
		return errors.New("incremental exact query observation has invalid transaction provenance")
	}
	return nil
}

func validateExactQueryObservationDependencies(
	dependencies []dependency,
	inputs []InputRevision,
	targetGeneration uint64,
) error {
	for index := range dependencies {
		current := dependencies[index]
		if current.changedAt == 0 || current.changedAt > targetGeneration {
			return errors.New("incremental exact query observation has an invalid dependency revision")
		}
		if err := validateExactQueryObservationDependency(current); err != nil {
			return err
		}
		if index > 0 && compareDependencyKeys(dependencies[index-1].key, current.key) >= 0 {
			return errors.New("incremental exact query observation dependencies are not canonical")
		}
	}
	return validateExactQueryObservationInputs(inputs)
}

func validateExactQueryObservationDependency(current dependency) error {
	switch current.key.kind {
	case inputDependency:
		if !validInputKey(current.key.input) || current.key.query != (QueryKey{}) ||
			!validRevision(current.revision) {
			return errors.New("incremental exact query observation has an invalid input dependency")
		}
	case queryDependency:
		if !validQueryKey(current.key.query) || current.key.input != (InputKey{}) ||
			current.revision != (Revision{}) || current.found {
			return errors.New("incremental exact query observation has an invalid query dependency")
		}
	default:
		return errors.New("incremental exact query observation has an invalid dependency")
	}
	return nil
}

func validateExactQueryObservationInputs(inputs []InputRevision) error {
	for index := range inputs {
		current := inputs[index]
		if !validInputKey(current.Key) || !validRevision(current.Revision) {
			return errors.New("incremental exact query observation has an invalid transitive input")
		}
		if index > 0 && inputs[index-1].Key.value >= current.Key.value {
			return errors.New("incremental exact query observation transitive inputs are not canonical")
		}
	}
	return nil
}

func (o *ExactQueryObservation) matches(entry *nodeEntry) bool {
	return entry != nil && !entry.dirty && entry.value.value == o.root.value &&
		entry.changedAt == o.changedAt && slices.Equal(entry.deps, o.deps) &&
		slices.Equal(entry.inputs, o.inputs)
}
