package incremental

import (
	"errors"

	"gitlab.com/haproxy-haptic/haptic/pkg/incremental/internal/immutablevector"
)

type preparedGraphCommitDraftRoot struct {
	seal           *preparedGraphCommitDraftRoot
	owner          *preparedGraphCommit
	generation     *graphGeneration
	generationRoot *graphGenerationAuthentication
	observations   immutablevector.Root[InputRevision]
	retiredInputs  immutablevector.Root[InputKey]
}

func newPreparedGraphCommitDraftRoot(
	owner *preparedGraphCommit,
) (*preparedGraphCommitDraftRoot, error) {
	if owner == nil || owner.graph == nil || owner.generation == nil ||
		!owner.generation.valid(owner.graph) {
		return nil, errors.New("incremental prepared graph draft has invalid provenance")
	}
	if err := owner.observations.ValidateOwnership(owner.graph.observationAuthority); err != nil {
		return nil, errors.New("incremental prepared graph draft observations have invalid provenance")
	}
	if err := owner.retiredInputs.ValidateOwnership(owner.graph.retiredInputAuthority); err != nil {
		return nil, errors.New("incremental prepared graph draft retirement has invalid provenance")
	}
	root := &preparedGraphCommitDraftRoot{
		owner:          owner,
		generation:     owner.generation,
		generationRoot: owner.generation.authentication,
		observations:   owner.observations,
		retiredInputs:  owner.retiredInputs,
	}
	root.seal = root
	owner.draftRoot = root
	if err := root.validate(owner); err != nil {
		owner.draftRoot = nil
		return nil, err
	}
	return root, nil
}

func (r *preparedGraphCommitDraftRoot) validate(owner *preparedGraphCommit) error {
	if r == nil || r.seal != r || r.owner != owner || owner == nil || owner.draftRoot != r ||
		owner.graph == nil || r.generation == nil || owner.generation != r.generation ||
		r.generationRoot == nil || owner.generation.authentication != r.generationRoot ||
		!owner.generation.valid(owner.graph) {
		return errors.New("incremental prepared graph draft has invalid provenance")
	}
	same, err := r.observations.SameRoot(owner.graph.observationAuthority, owner.observations)
	if err != nil || !same {
		return errors.New("incremental prepared graph draft observations changed")
	}
	same, err = r.retiredInputs.SameRoot(owner.graph.retiredInputAuthority, owner.retiredInputs)
	if err != nil || !same {
		return errors.New("incremental prepared graph draft retirement changed")
	}
	return nil
}
