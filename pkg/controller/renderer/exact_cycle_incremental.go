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

	iradix "github.com/hashicorp/go-immutable-radix/v2"

	"gitlab.com/haproxy-haptic/haptic/pkg/rendercontent"
	"gitlab.com/haproxy-haptic/haptic/pkg/templating"
)

type exactCycleIncrementalKind uint8

const (
	exactCycleIncrementalComponent exactCycleIncrementalKind = iota + 1
	exactCycleIncrementalValues
	exactCycleIncrementalRanked
)

type exactCycleIncrementalObservation struct {
	authority  *exactCycleIncrementalAuthority
	kind       exactCycleIncrementalKind
	key        string
	scope      string
	ordinal    uint64
	occurrence uint64
	group      string
	component  string
	cell       string
	delimiter  string
	root       *exactCycleIncrementalRoot
	auth       exactCycleIncrementalObservationAuthentication
}

type exactCycleIncrementalAuthority struct {
	seal *exactCycleIncrementalAuthority
}

type exactCycleIncrementalObservationAuthentication struct {
	authority  *exactCycleIncrementalAuthority
	kind       exactCycleIncrementalKind
	key        string
	scope      string
	ordinal    uint64
	occurrence uint64
	group      string
	component  string
	cell       string
	delimiter  string
	root       *exactCycleIncrementalRoot
}

type exactCycleIncrementalRootAuthentication struct {
	authority    *exactCycleIncrementalAuthority
	presentation any
	effects      *incrementalGroupIndex
}

type exactCycleIncrementalRoot struct {
	authority    *exactCycleIncrementalAuthority
	presentation any
	effects      *incrementalGroupIndex
	auth         exactCycleIncrementalRootAuthentication
	seal         *exactCycleIncrementalRoot
}

type exactCycleIncrementalObservationsAuthentication struct {
	authority *exactCycleIncrementalAuthority
	root      *iradix.Node[exactCycleIncrementalObservation]
	count     int
}

type exactCycleIncrementalObservations struct {
	authority *exactCycleIncrementalAuthority
	entries   *iradix.Tree[exactCycleIncrementalObservation]
	root      *iradix.Node[exactCycleIncrementalObservation]
	count     int
	auth      exactCycleIncrementalObservationsAuthentication
	seal      *exactCycleIncrementalObservations
}

func newExactCycleIncrementalAuthority() *exactCycleIncrementalAuthority {
	authority := &exactCycleIncrementalAuthority{}
	authority.seal = authority
	return authority
}

func newEmptyExactCycleIncrementalObservations() *exactCycleIncrementalObservations {
	authority := newExactCycleIncrementalAuthority()
	entries := iradix.New[exactCycleIncrementalObservation]()
	result := &exactCycleIncrementalObservations{
		authority: authority, entries: entries, root: entries.Root(), count: entries.Len(),
	}
	result.auth = exactCycleIncrementalObservationsAuthentication{
		authority: result.authority, root: result.root, count: result.count,
	}
	result.seal = result
	return result
}

func newExactCycleIncrementalRoot(
	authority *exactCycleIncrementalAuthority,
	presentation any,
	effects *incrementalGroupIndex,
) *exactCycleIncrementalRoot {
	root := &exactCycleIncrementalRoot{
		authority: authority, presentation: presentation, effects: effects,
	}
	root.auth = exactCycleIncrementalRootAuthentication{
		authority: authority, presentation: presentation, effects: effects,
	}
	root.seal = root
	return root
}

func exactCycleIncrementalObservationKey(
	kind exactCycleIncrementalKind,
	scope string,
	ordinal uint64,
	occurrence uint64,
	group, component, cell, delimiter string,
) string {
	// Built in one buffer rather than through eight strings and a variadic
	// slice: this key is made per observation, and the parts are known here.
	buffer := make([]byte, 0, observationKeyBufferHint)
	buffer = appendIncrementalOrderedTupleUint(buffer, occurrence, 20)
	buffer = appendIncrementalOrderedTuplePart(buffer, scope)
	buffer = appendIncrementalOrderedTupleUint(buffer, ordinal, 20)
	buffer = appendIncrementalOrderedTupleUint(buffer, uint64(kind), 3)
	buffer = appendIncrementalOrderedTuplePart(buffer, group)
	buffer = appendIncrementalOrderedTuplePart(buffer, component)
	buffer = appendIncrementalOrderedTuplePart(buffer, cell)
	buffer = appendIncrementalOrderedTuplePart(buffer, delimiter)
	return string(buffer)
}

// observationKeyBufferHint covers the two 20-digit counters, the 3-digit kind
// and their separators, leaving room for the four names before the buffer has
// to grow.
const observationKeyBufferHint = 128

func (r *incrementalRenderSession) recordExactCycleIncrementalObservation(
	ctx context.Context,
	kind exactCycleIncrementalKind,
	group, component, cell, delimiter string,
	root any,
) error {
	if r.exactCycleRootReplay || !r.cachePublicationEnabled {
		return nil
	}
	scope, ok := templating.IncrementalScope(ctx)
	if !ok {
		return errors.New("exact cycle incremental observation has no root scope")
	}
	ordinal := uint64(len(r.exactCycleRootCalls[scope])) + 1
	r.exactCycleRootOccurrence++
	occurrence := r.exactCycleRootOccurrence
	key := exactCycleIncrementalObservationKey(
		kind, scope, ordinal, occurrence, group, component, cell, delimiter,
	)
	index := r.groupIndexes[group]
	if index == nil {
		return fmt.Errorf("exact cycle incremental group %q has no effect root", group)
	}
	rootWithEffects := newExactCycleIncrementalRoot(r.exactCycleRootAuthority, root, index)
	observation := exactCycleIncrementalObservation{
		authority: r.exactCycleRootAuthority,
		kind:      kind, key: key, scope: scope, ordinal: ordinal, occurrence: occurrence, group: group,
		component: component, cell: cell, delimiter: delimiter, root: rootWithEffects,
	}
	observation.auth = exactCycleIncrementalObservationAuthentication{
		authority: observation.authority,
		kind:      observation.kind, key: observation.key, scope: observation.scope,
		ordinal: observation.ordinal, occurrence: observation.occurrence,
		group: observation.group, component: observation.component,
		cell: observation.cell, delimiter: observation.delimiter, root: observation.root,
	}
	if err := observation.validate(); err != nil {
		return err
	}
	r.exactCycleRootCalls[scope] = append(r.exactCycleRootCalls[scope], observation)
	return nil
}

func (r *incrementalRenderSession) captureExactCycleIncrementalObservations() (
	*exactCycleIncrementalObservations,
	error,
) {
	if r == nil || !r.cachePublicationEnabled {
		return nil, errExactCycleUnavailable
	}
	r.renderMu.Lock()
	defer r.renderMu.Unlock()
	txn := iradix.New[exactCycleIncrementalObservation]().Txn()
	count := 0
	for _, observations := range r.exactCycleRootCalls {
		for index := range observations {
			observation := observations[index]
			if err := observation.validate(); err != nil {
				return nil, err
			}
			if _, replaced := txn.Insert([]byte(observation.key), observation); replaced {
				return nil, errors.New("exact cycle incremental observation is duplicated")
			}
			count++
		}
	}
	entries := txn.Commit()
	result := &exactCycleIncrementalObservations{
		authority: r.exactCycleRootAuthority, entries: entries, root: entries.Root(), count: count,
	}
	result.auth = exactCycleIncrementalObservationsAuthentication{
		authority: result.authority, root: result.root, count: result.count,
	}
	result.seal = result
	return result, nil
}

func (o *exactCycleIncrementalObservations) matches(
	ctx context.Context,
	session *incrementalRenderSession,
) (bool, error) {
	if err := o.validate(); err != nil {
		return false, err
	}
	if session == nil || !session.cachePublicationEnabled {
		return false, nil
	}
	session.renderMu.Lock()
	if session.exactCycleRootReplay {
		session.renderMu.Unlock()
		return false, errors.New("exact cycle incremental replay is already active")
	}
	session.exactCycleRootReplay = true
	session.renderMu.Unlock()
	defer func() {
		session.renderMu.Lock()
		session.exactCycleRootReplay = false
		session.renderMu.Unlock()
	}()

	matched := true
	var matchErr error
	lastOccurrence := uint64(0)
	o.entries.Root().Walk(func(key []byte, observation exactCycleIncrementalObservation) bool {
		if err := ctx.Err(); err != nil {
			matchErr = err
			return true
		}
		if string(key) != observation.key {
			matchErr = errors.New("exact cycle incremental observation has an invalid key")
			return true
		}
		if lastOccurrence == ^uint64(0) || observation.occurrence != lastOccurrence+1 {
			matchErr = errors.New("exact cycle incremental observation has an invalid occurrence")
			return true
		}
		lastOccurrence = observation.occurrence
		scoped := templating.WithIncrementalScope(ctx, observation.scope)
		var current any
		switch observation.kind {
		case exactCycleIncrementalComponent:
			current, matchErr = session.RenderIncrementalTextFragment(scoped, observation.component)
		case exactCycleIncrementalValues:
			current, matchErr = session.IncrementalValuesCertified(scoped, observation.group, observation.cell)
		case exactCycleIncrementalRanked:
			if observation.delimiter == "" {
				current, matchErr = session.IncrementalRankedTextFragment(
					scoped, observation.group, observation.cell,
				)
			} else {
				current, matchErr = session.IncrementalRankedTextFragmentJoin(
					scoped, observation.group, observation.cell, observation.delimiter,
				)
			}
		default:
			matchErr = errors.New("exact cycle incremental observation has an invalid kind")
		}
		if matchErr != nil {
			return true
		}
		session.renderMu.Lock()
		currentIndex := session.groupIndexes[observation.group]
		session.renderMu.Unlock()
		currentWithEffects := newExactCycleIncrementalRoot(o.authority, current, currentIndex)
		matched, matchErr = sameExactCycleIncrementalRoot(observation.root, currentWithEffects)
		return !matched || matchErr != nil
	})
	return matched && matchErr == nil, matchErr
}

func (r *incrementalRenderSession) resetExactCycleReplayTracking() error {
	r.renderMu.Lock()
	r.calls = map[string][]incrementalCall{}
	r.scopedCalls = map[string]map[string][]incrementalCall{}
	r.callStatuses = map[string]map[string]incrementalScopeCallStatus{}
	r.valueAccesses = map[string]int{}
	r.renderMu.Unlock()

	r.mu.Lock()
	defer r.mu.Unlock()
	if len(r.exactCycleHTTPPublishedLease) != 0 {
		return errors.New("exact cycle replay mismatch has a published HTTP lease")
	}
	r.exactCycleHTTPLease = nil
	return nil
}

func (o *exactCycleIncrementalObservations) validate() error {
	if o == nil || o.seal != o || o.authority == nil || o.authority.seal != o.authority ||
		o.entries == nil || o.root != o.entries.Root() || o.count != o.entries.Len() ||
		o.authority != o.auth.authority || o.root != o.auth.root || o.count != o.auth.count {
		return errors.New("exact cycle incremental observations have invalid provenance")
	}
	return nil
}

func (o *exactCycleIncrementalObservation) validate() error {
	if o == nil || !o.identityComplete() || !o.matchesAuthentication() {
		return errors.New("exact cycle incremental observation has invalid provenance")
	}
	if err := o.validateKindFields(); err != nil {
		return err
	}
	return o.root.validate(o.authority)
}

func (o *exactCycleIncrementalObservation) identityComplete() bool {
	return o.authority != nil && o.authority.seal == o.authority && o.key != "" &&
		o.scope != "" && o.ordinal != 0 && o.occurrence != 0 && o.root != nil
}

func (o *exactCycleIncrementalObservation) matchesAuthentication() bool {
	return o.authority == o.auth.authority && o.kind == o.auth.kind && o.key == o.auth.key &&
		o.scope == o.auth.scope && o.ordinal == o.auth.ordinal &&
		o.occurrence == o.auth.occurrence && o.group == o.auth.group &&
		o.component == o.auth.component && o.cell == o.auth.cell &&
		o.delimiter == o.auth.delimiter && o.root == o.auth.root &&
		o.key == exactCycleIncrementalObservationKey(
			o.kind, o.scope, o.ordinal, o.occurrence, o.group, o.component, o.cell, o.delimiter,
		)
}

func (o *exactCycleIncrementalObservation) validateKindFields() error {
	switch o.kind {
	case exactCycleIncrementalComponent:
		if o.group == "" || o.component == "" || o.cell != "" || o.delimiter != "" {
			return errors.New("exact cycle incremental component observation is invalid")
		}
	case exactCycleIncrementalValues:
		if o.group == "" || o.cell == "" || o.component != "" || o.delimiter != "" {
			return errors.New("exact cycle incremental values observation is invalid")
		}
	case exactCycleIncrementalRanked:
		if o.group == "" || o.cell == "" || o.component != "" {
			return errors.New("exact cycle incremental ranked observation is invalid")
		}
	default:
		return errors.New("exact cycle incremental observation has an invalid kind")
	}
	return nil
}

func sameExactCycleIncrementalRoot(left, right any) (bool, error) {
	expected, ok := left.(*exactCycleIncrementalRoot)
	if !ok {
		return false, fmt.Errorf("exact cycle incremental root has unsupported type %T", left)
	}
	current, ok := right.(*exactCycleIncrementalRoot)
	if !ok {
		return false, nil
	}
	if err := expected.validate(expected.authority); err != nil {
		return false, err
	}
	if err := current.validate(expected.authority); err != nil {
		return false, err
	}
	if expected.effects != current.effects {
		return false, nil
	}
	return sameExactCycleIncrementalPresentationRoot(expected.presentation, current.presentation)
}

func (r *exactCycleIncrementalRoot) validate(authority *exactCycleIncrementalAuthority) error {
	if r == nil || r.seal != r || authority == nil || authority.seal != authority ||
		r.authority != authority || r.effects == nil || r.authority != r.auth.authority ||
		r.effects != r.auth.effects {
		return errors.New("exact cycle incremental effect root has invalid provenance")
	}
	if err := r.effects.validateAuthentication(); err != nil {
		return err
	}
	same, err := sameExactCycleIncrementalPresentationRoot(r.presentation, r.auth.presentation)
	if err != nil {
		return err
	}
	if !same {
		return errors.New("exact cycle incremental presentation root failed authentication")
	}
	return nil
}

func sameExactCycleIncrementalPresentationRoot(left, right any) (bool, error) {
	switch expected := left.(type) {
	case rendercontent.Output:
		current, ok := right.(rendercontent.Output)
		if !ok {
			return false, nil
		}
		return expected.SameRoot(current)
	case rendercontent.TextFragment:
		current, ok := right.(rendercontent.TextFragment)
		if !ok {
			return false, nil
		}
		return expected.SameRoot(current)
	case *templating.IncrementalCertifiedValues:
		current, ok := right.(*templating.IncrementalCertifiedValues)
		if !ok {
			return false, nil
		}
		return expected.SameRoot(current)
	default:
		return false, fmt.Errorf("exact cycle incremental presentation root has unsupported type %T", left)
	}
}
