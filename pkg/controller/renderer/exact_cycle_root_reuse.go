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

	"gitlab.com/haproxy-haptic/haptic/pkg/dataplane"
	"gitlab.com/haproxy-haptic/haptic/pkg/templating"
)

// exactCycleRootOutputs is what a cycle's auxiliary roots produced, keyed by
// kind and name, so a later render can hand an unchanged root's text back
// without executing it.
type exactCycleRootOutputs struct {
	outputs map[string]string
	seal    *exactCycleRootOutputs
}

func exactCycleRootOutputKey(kind, name string) string {
	return kind + "\x00" + name
}

func captureExactCycleRootOutputs(files *dataplane.AuxiliaryFiles) *exactCycleRootOutputs {
	outputs := &exactCycleRootOutputs{outputs: map[string]string{}}
	if files != nil {
		for _, file := range files.MapFiles {
			outputs.outputs[exactCycleRootOutputKey("map", file.Path)] = file.Content
		}
		for _, file := range files.GeneralFiles {
			outputs.outputs[exactCycleRootOutputKey("file", file.Filename)] = file.Content
		}
		for _, file := range files.SSLCertificates {
			outputs.outputs[exactCycleRootOutputKey("SSL certificate", file.Path)] = file.Content
		}
	}
	outputs.seal = outputs
	return outputs
}

func (o *exactCycleRootOutputs) valid() bool {
	return o != nil && o.seal == o && o.outputs != nil
}

func (o *exactCycleRootOutputs) lookup(kind, name string) (string, bool) {
	if !o.valid() {
		return "", false
	}
	content, found := o.outputs[exactCycleRootOutputKey(kind, name)]
	return content, found
}

// exactCycleRootReuser hands an auxiliary root its previous output when the
// previous cycle's external inputs matched and the root observed the same
// incremental values in the same order. The root's execution is what it
// skips; the observations are made again, so the group calls and the
// recorded observations are what a render would have left behind.
type exactCycleRootReuser struct {
	program   *templating.ExactCycleReplayProgram
	engine    templating.Engine
	candidate *exactCycleCandidate
	session   *incrementalRenderSession
	scopes    map[string][]exactCycleIncrementalObservation
	count     int
}

// reused counts the roots that handed back their previous output.
func (r *exactCycleRootReuser) reused() int {
	if r == nil {
		return 0
	}
	return r.count
}

type exactCycleRootReuseEngine interface {
	ReuseExactCycleRoot(ctx context.Context, templateName string) error
}

func newExactCycleRootReuser(
	program *templating.ExactCycleReplayProgram,
	engine templating.Engine,
	candidate *exactCycleCandidate,
	session *incrementalRenderSession,
) *exactCycleRootReuser {
	if program == nil || candidate == nil || session == nil || !candidate.roots.valid() ||
		candidate.mode != exactCycleCandidateGraph {
		return nil
	}
	if _, ok := engine.(exactCycleRootReuseEngine); !ok {
		return nil
	}
	if err := candidate.incremental.validate(); err != nil {
		return nil
	}
	scopes := map[string][]exactCycleIncrementalObservation{}
	candidate.incremental.entries.Root().Walk(func(_ []byte, observation exactCycleIncrementalObservation) bool {
		scopes[observation.scope] = append(scopes[observation.scope], observation)
		return false
	})
	return &exactCycleRootReuser{
		program: program, engine: engine, candidate: candidate, session: session, scopes: scopes,
	}
}

// reuse returns the root's previous output when the root can be skipped.
func (r *exactCycleRootReuser) reuse(
	ctx context.Context,
	kind, name string,
) (content string, reused bool, err error) {
	if r == nil {
		return "", false, nil
	}
	content, known := r.candidate.roots.lookup(kind, name)
	if !known {
		return "", false, nil
	}
	outputOnly, err := r.program.OutputOnlyRoot(name)
	if err != nil {
		return "", false, err
	}
	if !outputOnly {
		return "", false, nil
	}
	matched, err := r.session.matchExactCycleRootObservations(ctx, name, r.scopes[name])
	if err != nil || !matched {
		return "", false, err
	}
	if err := r.engine.(exactCycleRootReuseEngine).ReuseExactCycleRoot(ctx, name); err != nil {
		return "", false, err
	}
	r.count++
	return content, true, nil
}

// matchExactCycleRootObservations makes a root's recorded observations again,
// in order, through the calls a render would make, and reports whether every
// one presented the same value. The calls record themselves as a render
// would; a mismatch restores the session's call bookkeeping so the render
// that follows starts where the root would have.
func (r *incrementalRenderSession) matchExactCycleRootObservations(
	ctx context.Context,
	scope string,
	observations []exactCycleIncrementalObservation,
) (bool, error) {
	if r == nil || !r.cachePublicationEnabled {
		return false, nil
	}
	r.renderMu.Lock()
	if r.exactCycleRootReplay {
		r.renderMu.Unlock()
		return false, errors.New("exact cycle root reuse during a replay")
	}
	saved := r.saveRootCallBookkeeping(scope, observations)
	r.renderMu.Unlock()

	scoped := templating.WithIncrementalScope(ctx, scope)
	for index := range observations {
		observation := &observations[index]
		if observation.scope != scope {
			return false, errors.New("exact cycle root observation belongs to another root")
		}
		current, err := r.observeExactCycleIncremental(scoped, observation)
		if err != nil {
			return false, err
		}
		same, err := sameExactCycleIncrementalPresentationRoot(observation.root.presentation, current)
		if err != nil {
			return false, err
		}
		if !same {
			r.renderMu.Lock()
			r.restoreRootCallBookkeeping(scope, saved)
			r.renderMu.Unlock()
			return false, nil
		}
	}
	return true, nil
}

func (r *incrementalRenderSession) observeExactCycleIncremental(
	ctx context.Context,
	observation *exactCycleIncrementalObservation,
) (any, error) {
	switch observation.kind {
	case exactCycleIncrementalComponent:
		return r.RenderIncrementalTextFragment(ctx, observation.component)
	case exactCycleIncrementalValues:
		return r.IncrementalValuesCertified(ctx, observation.group, observation.cell)
	case exactCycleIncrementalValueCount:
		return r.IncrementalValueCount(ctx, observation.group, observation.cell)
	case exactCycleIncrementalRanked:
		if observation.delimiter == "" {
			return r.IncrementalRankedTextFragment(ctx, observation.group, observation.cell)
		}
		return r.IncrementalRankedTextFragmentJoin(
			ctx, observation.group, observation.cell, observation.delimiter,
		)
	default:
		return nil, fmt.Errorf("exact cycle incremental observation has an invalid kind %d", observation.kind)
	}
}

// rootCallBookkeeping is the per-call state a root's incremental calls
// leave in the session for the groups it observes, captured so a partial
// match can be undone.
type rootCallBookkeeping struct {
	groups     map[string]rootGroupBookkeeping
	rootCalls  int
	occurrence uint64
}

type rootGroupBookkeeping struct {
	calls         int
	scopedCalls   int
	status        incrementalScopeCallStatus
	statusKnown   bool
	valueAccesses int
}

func (r *incrementalRenderSession) saveRootCallBookkeeping(
	scope string,
	observations []exactCycleIncrementalObservation,
) rootCallBookkeeping {
	saved := rootCallBookkeeping{
		groups:     make(map[string]rootGroupBookkeeping, len(observations)),
		rootCalls:  len(r.exactCycleRootCalls[scope]),
		occurrence: r.exactCycleRootOccurrence,
	}
	for index := range observations {
		group := observations[index].group
		if _, seen := saved.groups[group]; seen {
			continue
		}
		status, known := r.callStatuses[group][scope]
		saved.groups[group] = rootGroupBookkeeping{
			calls:         len(r.calls[group]),
			scopedCalls:   len(r.scopedCalls[group][scope]),
			status:        status,
			statusKnown:   known,
			valueAccesses: r.valueAccesses[group],
		}
	}
	return saved
}

func (r *incrementalRenderSession) restoreRootCallBookkeeping(scope string, saved rootCallBookkeeping) {
	for group, state := range saved.groups {
		if calls := r.calls[group]; len(calls) > state.calls {
			r.calls[group] = calls[:state.calls]
		}
		if byScope := r.scopedCalls[group]; byScope != nil {
			if calls := byScope[scope]; len(calls) > state.scopedCalls {
				byScope[scope] = calls[:state.scopedCalls]
			}
		}
		if statuses := r.callStatuses[group]; statuses != nil {
			if state.statusKnown {
				statuses[scope] = state.status
			} else {
				delete(statuses, scope)
			}
		}
		if state.valueAccesses == 0 {
			delete(r.valueAccesses, group)
		} else {
			r.valueAccesses[group] = state.valueAccesses
		}
	}
	r.exactCycleRootCalls[scope] = r.exactCycleRootCalls[scope][:saved.rootCalls]
	r.exactCycleRootOccurrence = saved.occurrence
}
