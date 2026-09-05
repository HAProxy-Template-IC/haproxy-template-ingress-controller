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
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"slices"
	"strings"
	"sync"

	iradix "github.com/hashicorp/go-immutable-radix/v2"

	"gitlab.com/haproxy-haptic/haptic/pkg/controller/rendercontext"
	"gitlab.com/haproxy-haptic/haptic/pkg/incremental"
	"gitlab.com/haproxy-haptic/haptic/pkg/rendercontent"
)

type incrementalPreparedPlan struct {
	instances           *iradix.Tree[string]
	calls               *iradix.Tree[string]
	backendCandidates   *iradix.Tree[*iradix.Tree[string]]
	profileCandidates   *iradix.Tree[*iradix.Tree[string]]
	profileVariants     *iradix.Tree[*iradix.Tree[*iradix.Tree[struct{}]]]
	standaloneProfiles  *iradix.Tree[*iradix.Tree[struct{}]]
	conditions          *iradix.Tree[*iradix.Tree[struct{}]]
	requirements        *iradix.Tree[*iradix.Tree[string]]
	missingProfiles     *iradix.Tree[string]
	conflictingProfiles *iradix.Tree[string]
	outputs             *iradix.Tree[string]
	groups              *iradix.Tree[*incrementalGroupIndex]
	selected            *rendercontext.PreparedPlanSnapshot
	resultRoot          *iradix.Node[incremental.ExactValueRoot]
	outputMemo          *incrementalPreparedPlanOutputMemo
	auth                incrementalPreparedPlanAuthentication
}

type incrementalPreparedPlanAuthentication struct {
	instances           *iradix.Tree[string]
	calls               *iradix.Tree[string]
	backendCandidates   *iradix.Tree[*iradix.Tree[string]]
	profileCandidates   *iradix.Tree[*iradix.Tree[string]]
	profileVariants     *iradix.Tree[*iradix.Tree[*iradix.Tree[struct{}]]]
	standaloneProfiles  *iradix.Tree[*iradix.Tree[struct{}]]
	conditions          *iradix.Tree[*iradix.Tree[struct{}]]
	requirements        *iradix.Tree[*iradix.Tree[string]]
	missingProfiles     *iradix.Tree[string]
	conflictingProfiles *iradix.Tree[string]
	outputs             *iradix.Tree[string]
	groups              *iradix.Tree[*incrementalGroupIndex]
	selected            *rendercontext.PreparedPlanSnapshot
	resultRoot          *iradix.Node[incremental.ExactValueRoot]
	outputMemo          *incrementalPreparedPlanOutputMemo
}

type incrementalPreparedBackendCandidate struct {
	Group     string                            `json:"group"`
	Component string                            `json:"component"`
	Source    string                            `json:"source"`
	Namespace string                            `json:"namespace"`
	Name      string                            `json:"name"`
	Call      uint32                            `json:"call"`
	Backend   rendercontext.PreparedPlanBackend `json:"backend"`
	WhenAny   *incrementalBackendPlanCondition  `json:"whenAny,omitempty"`
}

type incrementalPreparedProfileCandidate struct {
	Profile    rendercontext.PreparedPlanProfile `json:"profile"`
	Standalone bool                              `json:"standalone,omitempty"`
}

type incrementalPreparedPlanOutputPart struct {
	Text    string `json:"text,omitempty"`
	Backend string `json:"backend,omitempty"`
}

type incrementalPreparedPlanUpdate struct {
	plan         *incrementalPreparedPlan
	baseMemo     *incrementalPreparedPlanOutputMemo
	outputDirty  map[string]struct{}
	profileDirty map[string]struct{}
}

type incrementalPreparedPlanOutputMemo struct {
	root     *iradix.Node[string]
	selected *rendercontext.PreparedPlanSnapshot
	parent   *incrementalPreparedPlanOutputMemo
	changes  *iradix.Tree[struct{}]
	depth    uint8
	entries  *sync.Map
	auth     incrementalPreparedPlanOutputMemoAuthentication
}

type incrementalPreparedPlanOutputMemoAuthentication struct {
	owner    *incrementalPreparedPlanOutputMemo
	root     *iradix.Node[string]
	selected *rendercontext.PreparedPlanSnapshot
	parent   *incrementalPreparedPlanOutputMemo
	changes  *iradix.Tree[struct{}]
	depth    uint8
	entries  *sync.Map
}

type incrementalPreparedPlanOutputMemoKey struct {
	authority *rendercontext.PlanTokenAuthority
	group     string
	component string
}

type incrementalPreparedPlanOutputMemoEntry struct {
	owner  *incrementalPreparedPlanOutputMemo
	key    incrementalPreparedPlanOutputMemoKey
	output rendercontent.Output
	auth   incrementalPreparedPlanOutputMemoEntryAuthentication
}

type incrementalPreparedPlanOutputMemoEntryAuthentication struct {
	owner  *incrementalPreparedPlanOutputMemoEntry
	memo   *incrementalPreparedPlanOutputMemo
	key    incrementalPreparedPlanOutputMemoKey
	output rendercontent.Output
}

type incrementalPreparedPlanReplacement struct {
	update    *incrementalPreparedPlanUpdate
	oldResult *incrementalComponentResult
	newResult *incrementalComponentResult
}

type incrementalPreparedPlanRegistry interface {
	AttachPreparedPlan(*rendercontext.PreparedPlanSnapshot) error
	PreparedBackendToken(string) (string, error)
	PreparedPlanTokenAuthority() (*rendercontext.PlanTokenAuthority, error)
}

const incrementalPreparedPlanOutputMemoMaxDepth = 32

func newIncrementalPreparedPlan(
	groups []string,
	indexes map[string]*incrementalGroupIndex,
	resultRoot *iradix.Node[incremental.ExactValueRoot],
) (*incrementalPreparedPlan, error) {
	groupTree := iradix.New[*incrementalGroupIndex]()
	for _, group := range groups {
		index := indexes[group]
		if index == nil {
			return nil, fmt.Errorf("incremental group %q has no assembly index", group)
		}
		if err := index.validateAuthentication(); err != nil {
			return nil, err
		}
		groupTree, _, _ = groupTree.Insert([]byte(group), index)
	}
	plan := &incrementalPreparedPlan{
		instances:           iradix.New[string](),
		calls:               iradix.New[string](),
		backendCandidates:   iradix.New[*iradix.Tree[string]](),
		profileCandidates:   iradix.New[*iradix.Tree[string]](),
		profileVariants:     iradix.New[*iradix.Tree[*iradix.Tree[struct{}]]](),
		standaloneProfiles:  iradix.New[*iradix.Tree[struct{}]](),
		conditions:          iradix.New[*iradix.Tree[struct{}]](),
		requirements:        iradix.New[*iradix.Tree[string]](),
		missingProfiles:     iradix.New[string](),
		conflictingProfiles: iradix.New[string](),
		outputs:             iradix.New[string](),
		groups:              groupTree,
		selected:            rendercontext.NewPreparedPlanSnapshot(),
		resultRoot:          resultRoot,
	}
	plan.outputMemo = newIncrementalPreparedPlanOutputMemo(plan.outputs.Root(), plan.selected, nil, nil)
	plan.authenticate()
	return plan, nil
}

func (p *incrementalPreparedPlan) applyGroupReplacement(
	component *incrementalComponent,
	group string,
	oldIndex, newIndex *incrementalGroupIndex,
	id incrementalGroupInstanceID,
	oldResultRoot, newResultRoot *iradix.Node[incremental.ExactValueRoot],
) (*incrementalPreparedPlan, error) {
	replacement, tracked, err := p.beginGroupReplacement(group, oldIndex, newIndex, id, oldResultRoot)
	if err != nil {
		return nil, err
	}
	if !tracked {
		return p.rebindResultRoot(oldResultRoot, newResultRoot)
	}
	if err := replacement.replaceInstance(component, group, id, newIndex); err != nil {
		return nil, err
	}
	if err := replacement.update.refreshPublicationDependents(
		group, oldIndex, newIndex, replacement.oldResult, replacement.newResult,
	); err != nil {
		return nil, err
	}
	groups, _, _ := replacement.update.plan.groups.Insert([]byte(group), newIndex)
	replacement.update.plan.groups = groups
	if err := replacement.update.finish(); err != nil {
		return nil, err
	}
	replacement.update.plan.resultRoot = newResultRoot
	replacement.update.plan.authenticate()
	return replacement.update.plan, nil
}

type incrementalPreparedPlanGroupAddition struct {
	component *incrementalComponent
	id        incrementalGroupInstanceID
	result    *incrementalComponentResult
}

func (p *incrementalPreparedPlan) applyGroupAdditions(
	group string,
	oldIndex, newIndex *incrementalGroupIndex,
	additions []incrementalPreparedPlanGroupAddition,
	oldResultRoot, newResultRoot *iradix.Node[incremental.ExactValueRoot],
) (*incrementalPreparedPlan, error) {
	if len(additions) == 0 {
		return p.rebindResultRoot(oldResultRoot, newResultRoot)
	}
	if err := p.validateAuthentication(oldResultRoot); err != nil {
		return nil, err
	}
	if err := oldIndex.validateAuthentication(); err != nil {
		return nil, err
	}
	if err := newIndex.validateAuthentication(); err != nil {
		return nil, err
	}
	linked, tracked := p.groups.Root().Get([]byte(group))
	if !tracked {
		return p.rebindResultRoot(oldResultRoot, newResultRoot)
	}
	if linked != oldIndex {
		return nil, fmt.Errorf("incremental prepared plan group %q does not match its assembly index", group)
	}
	update := &incrementalPreparedPlanUpdate{
		plan: p.clone(), baseMemo: p.outputMemo,
		outputDirty: make(map[string]struct{}), profileDirty: make(map[string]struct{}),
	}
	seen := make(map[string]struct{}, len(additions))
	for index := range additions {
		if err := update.applyGroupAddition(
			group, oldIndex, newIndex, &additions[index], seen,
		); err != nil {
			return nil, err
		}
	}
	groups, _, _ := update.plan.groups.Insert([]byte(group), newIndex)
	update.plan.groups = groups
	if err := update.finish(); err != nil {
		return nil, err
	}
	update.plan.resultRoot = newResultRoot
	update.plan.authenticate()
	return update.plan, nil
}

func (u *incrementalPreparedPlanUpdate) applyGroupAddition(
	group string,
	oldIndex, newIndex *incrementalGroupIndex,
	addition *incrementalPreparedPlanGroupAddition,
	seen map[string]struct{},
) error {
	if addition.component == nil || addition.result == nil {
		return errors.New("incremental prepared plan addition is incomplete")
	}
	key := incrementalGroupInstanceKey(addition.id)
	if _, duplicate := seen[string(key)]; duplicate {
		return errors.New("incremental prepared plan repeats an addition")
	}
	seen[string(key)] = struct{}{}
	if _, exists := oldIndex.instances.Root().Get(key); exists {
		return errors.New("incremental prepared plan batch can only add new instances")
	}
	if addition.component.backendPlan {
		if err := u.addInstance(group, addition.id, addition.result, newIndex); err != nil {
			return err
		}
	}
	return u.refreshPublicationDependents(group, oldIndex, newIndex, nil, addition.result)
}

func (p *incrementalPreparedPlan) beginGroupReplacement(
	group string,
	oldIndex, newIndex *incrementalGroupIndex,
	id incrementalGroupInstanceID,
	resultRoot *iradix.Node[incremental.ExactValueRoot],
) (*incrementalPreparedPlanReplacement, bool, error) {
	if err := p.validateAuthentication(resultRoot); err != nil {
		return nil, false, err
	}
	if err := oldIndex.validateAuthentication(); err != nil {
		return nil, false, err
	}
	if err := newIndex.validateAuthentication(); err != nil {
		return nil, false, err
	}
	linked, tracked := p.groups.Root().Get([]byte(group))
	if !tracked {
		return nil, false, nil
	}
	if linked != oldIndex {
		return nil, false, fmt.Errorf("incremental prepared plan group %q does not match its assembly index", group)
	}
	oldResult, oldExists, err := indexedPlanResult(oldIndex, id)
	if err != nil {
		return nil, false, err
	}
	newResult, newExists, err := indexedPlanResult(newIndex, id)
	if err != nil {
		return nil, false, err
	}
	replacement := &incrementalPreparedPlanReplacement{
		update: &incrementalPreparedPlanUpdate{
			plan: p.clone(), baseMemo: p.outputMemo,
			outputDirty: make(map[string]struct{}), profileDirty: make(map[string]struct{}),
		},
	}
	if oldExists {
		replacement.oldResult = &oldResult
	}
	if newExists {
		replacement.newResult = &newResult
	}
	return replacement, true, nil
}

func (r *incrementalPreparedPlanReplacement) replaceInstance(
	component *incrementalComponent,
	group string,
	id incrementalGroupInstanceID,
	newIndex *incrementalGroupIndex,
) error {
	if component == nil || !component.backendPlan {
		return nil
	}
	if r.oldResult != nil {
		if err := r.update.removeInstance(group, id, r.oldResult); err != nil {
			return err
		}
	}
	if r.newResult == nil {
		return nil
	}
	return r.update.addInstance(group, id, r.newResult, newIndex)
}

func (p *incrementalPreparedPlan) rebindResultRoot(
	oldRoot, newRoot *iradix.Node[incremental.ExactValueRoot],
) (*incrementalPreparedPlan, error) {
	if err := p.validateAuthentication(oldRoot); err != nil {
		return nil, err
	}
	if oldRoot == newRoot {
		return p, nil
	}
	updated := p.clone()
	updated.resultRoot = newRoot
	updated.authenticate()
	return updated, nil
}

func (p *incrementalPreparedPlan) clone() *incrementalPreparedPlan {
	cloned := *p
	return &cloned
}

func indexedPlanResult(
	index *incrementalGroupIndex,
	id incrementalGroupInstanceID,
) (incrementalComponentResult, bool, error) {
	indexed, exists := index.instances.Root().Get(incrementalGroupInstanceKey(id))
	if !exists {
		return incrementalComponentResult{}, false, nil
	}
	result, err := decodeIndexedGroupInstanceResult(&indexed)
	if err != nil {
		return incrementalComponentResult{}, false, err
	}
	return result, true, nil
}

func (u *incrementalPreparedPlanUpdate) removeInstance(
	group string,
	id incrementalGroupInstanceID,
	result *incrementalComponentResult,
) error {
	instanceKey := incrementalGroupInstanceKey(id)
	encoded, exists := u.plan.instances.Root().Get(instanceKey)
	if !exists {
		return errors.New("incremental prepared plan is missing an instance")
	}
	stored, err := decodeIncrementalComponentResultString(encoded)
	if err != nil {
		return fmt.Errorf("decoding incremental prepared plan instance: %w", err)
	}
	if !equalIncrementalPlanPayload(&stored, result) {
		return errors.New("incremental prepared plan instance does not match its assembly index")
	}
	for callIndex := range result.BackendPlan {
		call := &result.BackendPlan[callIndex]
		location := incrementalGroupLocationKey(id, uint64(callIndex))
		if call.Profile != nil {
			if err := u.removeProfileCandidate(location, call); err != nil {
				return err
			}
			continue
		}
		candidate, err := u.removeBackendCall(group, id, location, call)
		if err != nil {
			return err
		}
		if err := u.setBackendReachable(&candidate, location, false); err != nil {
			return err
		}
	}
	u.plan.instances, _, _ = u.plan.instances.Delete(instanceKey)
	outputKey := incrementalPreparedPlanOutputKey(group, id)
	u.plan.outputs, _, _ = u.plan.outputs.Delete(outputKey)
	u.outputDirty[string(outputKey)] = struct{}{}
	return nil
}

func (u *incrementalPreparedPlanUpdate) addInstance(
	group string,
	id incrementalGroupInstanceID,
	result *incrementalComponentResult,
	index *incrementalGroupIndex,
) error {
	if err := validateIncrementalBackendPlanInstance(result); err != nil {
		return err
	}
	encoded, err := json.Marshal(result)
	if err != nil {
		return fmt.Errorf("encoding incremental prepared plan instance: %w", err)
	}
	instanceKey := incrementalGroupInstanceKey(id)
	if _, exists := u.plan.instances.Root().Get(instanceKey); exists {
		return errors.New("incremental prepared plan repeats an instance")
	}
	u.plan.instances, _, _ = u.plan.instances.Insert(instanceKey, string(encoded))
	for callIndex := range result.BackendPlan {
		call := &result.BackendPlan[callIndex]
		location := incrementalGroupLocationKey(id, uint64(callIndex))
		if call.Profile != nil {
			if err := u.addProfileCandidate(location, call); err != nil {
				return err
			}
			continue
		}
		candidate, err := u.addBackendCall(group, id, uint32(callIndex), location, call)
		if err != nil {
			return err
		}
		reachable, err := incrementalPreparedBackendReachable(&candidate, index)
		if err != nil {
			return err
		}
		if err := u.setBackendReachable(&candidate, location, reachable); err != nil {
			return err
		}
	}
	u.outputDirty[string(incrementalPreparedPlanOutputKey(group, id))] = struct{}{}
	return nil
}

func equalIncrementalPlanPayload(left, right *incrementalComponentResult) bool {
	leftEncoded, leftErr := json.Marshal(struct {
		Calls  []incrementalBackendPlanCall `json:"calls"`
		Output []incrementalBackendPlanPart `json:"output"`
	}{left.BackendPlan, left.BackendPlanOutput})
	rightEncoded, rightErr := json.Marshal(struct {
		Calls  []incrementalBackendPlanCall `json:"calls"`
		Output []incrementalBackendPlanPart `json:"output"`
	}{right.BackendPlan, right.BackendPlanOutput})
	return leftErr == nil && rightErr == nil && bytes.Equal(leftEncoded, rightEncoded)
}

func (u *incrementalPreparedPlanUpdate) addBackendCall(
	group string,
	id incrementalGroupInstanceID,
	callIndex uint32,
	location []byte,
	call *incrementalBackendPlanCall,
) (incrementalPreparedBackendCandidate, error) {
	candidate := incrementalPreparedBackendCandidate{
		Group: group, Component: id.component, Source: id.source, Namespace: id.namespace, Name: id.name,
		Call: callIndex, Backend: call.Backend.Clone(), WhenAny: cloneIncrementalBackendPlanCondition(call.WhenAny),
	}
	encoded, err := json.Marshal(candidate)
	if err != nil {
		return incrementalPreparedBackendCandidate{}, fmt.Errorf("encoding incremental prepared backend call: %w", err)
	}
	if _, exists := u.plan.calls.Root().Get(location); exists {
		return incrementalPreparedBackendCandidate{}, errors.New("incremental prepared plan repeats a backend call")
	}
	u.plan.calls, _, _ = u.plan.calls.Insert(location, string(encoded))
	for _, key := range conditionKeys(call.WhenAny) {
		conditionKey := incrementalPreparedPlanConditionKey(group, call.WhenAny.Cell, key)
		u.plan.conditions = insertNestedSet(u.plan.conditions, conditionKey, location)
	}
	return candidate, nil
}

func (u *incrementalPreparedPlanUpdate) removeBackendCall(
	group string,
	id incrementalGroupInstanceID,
	location []byte,
	call *incrementalBackendPlanCall,
) (incrementalPreparedBackendCandidate, error) {
	encoded, exists := u.plan.calls.Root().Get(location)
	if !exists {
		return incrementalPreparedBackendCandidate{}, errors.New("incremental prepared plan is missing a backend call")
	}
	candidate, err := decodeIncrementalPreparedBackendCandidate(encoded)
	if err != nil {
		return incrementalPreparedBackendCandidate{}, err
	}
	backend := call.Backend.Clone()
	whenAny := cloneIncrementalBackendPlanCondition(call.WhenAny)
	if candidate.Group != group || candidate.Component != id.component || candidate.Source != id.source ||
		candidate.Namespace != id.namespace || candidate.Name != id.name ||
		!reflect.DeepEqual(candidate.Backend, backend) || !reflect.DeepEqual(candidate.WhenAny, whenAny) {
		return incrementalPreparedBackendCandidate{}, errors.New("incremental prepared backend call does not match its instance")
	}
	for _, key := range conditionKeys(candidate.WhenAny) {
		conditionKey := incrementalPreparedPlanConditionKey(group, candidate.WhenAny.Cell, key)
		var removed bool
		u.plan.conditions, removed = deleteNestedSet(u.plan.conditions, conditionKey, location)
		if !removed {
			return incrementalPreparedBackendCandidate{}, errors.New("incremental prepared plan is missing a backend condition")
		}
	}
	u.plan.calls, _, _ = u.plan.calls.Delete(location)
	return candidate, nil
}

func conditionKeys(condition *incrementalBackendPlanCondition) []string {
	if condition == nil {
		return nil
	}
	return condition.Keys
}

func (u *incrementalPreparedPlanUpdate) addProfileCandidate(
	location []byte,
	call *incrementalBackendPlanCall,
) error {
	candidate := incrementalPreparedProfileCandidate{
		Profile:    call.Profile.Clone(),
		Standalone: len(call.Owners) == 0,
	}
	encoded, err := json.Marshal(candidate)
	if err != nil {
		return fmt.Errorf("encoding incremental prepared profile call: %w", err)
	}
	name := []byte(candidate.Profile.Name)
	u.plan.profileCandidates = insertNestedValue(u.plan.profileCandidates, name, location, string(encoded))
	u.plan.profileVariants = insertProfileVariant(u.plan.profileVariants, name, []byte(candidate.Profile.Text), location)
	if candidate.Standalone {
		u.plan.standaloneProfiles = insertNestedSet(u.plan.standaloneProfiles, name, location)
	}
	u.profileDirty[candidate.Profile.Name] = struct{}{}
	return nil
}

func (u *incrementalPreparedPlanUpdate) removeProfileCandidate(
	location []byte,
	call *incrementalBackendPlanCall,
) error {
	name := []byte(call.Profile.Name)
	encoded, exists := nestedValue(u.plan.profileCandidates, name, location)
	if !exists {
		return errors.New("incremental prepared plan is missing a profile call")
	}
	var candidate incrementalPreparedProfileCandidate
	if err := json.Unmarshal([]byte(encoded), &candidate); err != nil {
		return fmt.Errorf("decoding incremental prepared profile call: %w", err)
	}
	profile := call.Profile.Clone()
	if !reflect.DeepEqual(candidate.Profile, profile) || candidate.Standalone != (len(call.Owners) == 0) {
		return errors.New("incremental prepared profile call does not match its instance")
	}
	var removed bool
	u.plan.profileCandidates, removed = deleteNestedValue(u.plan.profileCandidates, name, location)
	if !removed {
		return errors.New("incremental prepared plan is missing a profile candidate")
	}
	u.plan.profileVariants, removed = deleteProfileVariant(
		u.plan.profileVariants, name, []byte(candidate.Profile.Text), location,
	)
	if !removed {
		return errors.New("incremental prepared plan is missing a profile variant")
	}
	if candidate.Standalone {
		u.plan.standaloneProfiles, removed = deleteNestedSet(u.plan.standaloneProfiles, name, location)
		if !removed {
			return errors.New("incremental prepared plan is missing a standalone profile")
		}
	}
	u.profileDirty[candidate.Profile.Name] = struct{}{}
	return nil
}

func (u *incrementalPreparedPlanUpdate) setBackendReachable(
	candidate *incrementalPreparedBackendCandidate,
	location []byte,
	reachable bool,
) error {
	identity := []byte(candidate.Backend.Backend.Name)
	current := nestedTree(u.plan.backendCandidates, identity)
	oldLocation, oldEncoded, oldExists := minimumNestedValue(current)
	encoded, err := json.Marshal(candidate)
	if err != nil {
		return fmt.Errorf("encoding incremental prepared backend candidate: %w", err)
	}
	_, exists := current.Root().Get(location)
	if reachable == exists {
		return nil
	}
	if reachable {
		current, _, _ = current.Insert(location, string(encoded))
	} else {
		current, _, _ = current.Delete(location)
	}
	u.plan.backendCandidates = storeNestedTree(u.plan.backendCandidates, identity, current)
	newLocation, newEncoded, newExists := minimumNestedValue(current)
	if oldExists == newExists && bytes.Equal(oldLocation, newLocation) && oldEncoded == newEncoded {
		return nil
	}
	return u.replaceBackendWinner(identity, oldEncoded, oldExists, newEncoded, newExists)
}

func (u *incrementalPreparedPlanUpdate) replaceBackendWinner(
	identity []byte,
	oldEncoded string,
	oldExists bool,
	newEncoded string,
	newExists bool,
) error {
	if oldExists {
		oldCandidate, err := decodeIncrementalPreparedBackendCandidate(oldEncoded)
		if err != nil {
			return err
		}
		if err := u.removeRequirement(string(identity), &oldCandidate); err != nil {
			return err
		}
		u.outputDirty[string(incrementalPreparedPlanOutputKey(oldCandidate.Group, oldCandidate.instanceID()))] = struct{}{}
	}
	selected, err := u.plan.selected.WithoutBackend(string(identity))
	if err != nil {
		return err
	}
	u.plan.selected = selected
	if !newExists {
		return nil
	}
	newCandidate, err := decodeIncrementalPreparedBackendCandidate(newEncoded)
	if err != nil {
		return err
	}
	selected, err = u.plan.selected.WithBackend(&newCandidate.Backend)
	if err != nil {
		return err
	}
	u.plan.selected = selected
	if err := u.addRequirement(string(identity), &newCandidate); err != nil {
		return err
	}
	u.outputDirty[string(incrementalPreparedPlanOutputKey(newCandidate.Group, newCandidate.instanceID()))] = struct{}{}
	return nil
}

func (u *incrementalPreparedPlanUpdate) addRequirement(
	identity string,
	candidate *incrementalPreparedBackendCandidate,
) error {
	profile := candidate.Backend.Backend.Profile
	if profile == "" {
		return nil
	}
	encoded, err := json.Marshal(candidate)
	if err != nil {
		return fmt.Errorf("encoding incremental prepared profile requirement: %w", err)
	}
	u.plan.requirements = insertNestedValue(u.plan.requirements, []byte(profile), []byte(identity), string(encoded))
	u.profileDirty[profile] = struct{}{}
	return nil
}

func (u *incrementalPreparedPlanUpdate) removeRequirement(
	identity string,
	candidate *incrementalPreparedBackendCandidate,
) error {
	profile := candidate.Backend.Backend.Profile
	if profile == "" {
		return nil
	}
	var removed bool
	u.plan.requirements, removed = deleteNestedValue(u.plan.requirements, []byte(profile), []byte(identity))
	if !removed {
		return errors.New("incremental prepared plan is missing a profile requirement")
	}
	u.profileDirty[profile] = struct{}{}
	return nil
}

func (u *incrementalPreparedPlanUpdate) refreshPublicationDependents(
	group string,
	oldIndex, newIndex *incrementalGroupIndex,
	oldResult, newResult *incrementalComponentResult,
) error {
	identities := make(map[string][]byte)
	if oldResult != nil {
		collectPublicationIdentities(identities, oldResult)
	}
	if newResult != nil {
		collectPublicationIdentities(identities, newResult)
	}
	for _, identity := range identities {
		if err := u.refreshPublicationIdentity(group, identity, oldIndex, newIndex); err != nil {
			return err
		}
	}
	return nil
}

func (u *incrementalPreparedPlanUpdate) refreshPublicationIdentity(
	group string,
	identity []byte,
	oldIndex, newIndex *incrementalGroupIndex,
) error {
	oldOwner, oldOwned, err := incrementalPublicationOwner(oldIndex, identity)
	if err != nil {
		return err
	}
	newOwner, newOwned, err := incrementalPublicationOwner(newIndex, identity)
	if err != nil {
		return err
	}
	if oldOwned == newOwned && (!oldOwned || oldOwner == newOwner) {
		return nil
	}
	conditionKey := incrementalOrderedTuple(group, string(identity))
	dependents, exists := u.plan.conditions.Root().Get(conditionKey)
	if !exists {
		return nil
	}
	return u.refreshConditionDependents(dependents, newIndex)
}

func (u *incrementalPreparedPlanUpdate) refreshConditionDependents(
	dependents *iradix.Tree[struct{}],
	index *incrementalGroupIndex,
) error {
	var updateErr error
	dependents.Root().Walk(func(location []byte, _ struct{}) bool {
		encoded, found := u.plan.calls.Root().Get(location)
		if !found {
			updateErr = errors.New("incremental prepared condition has no backend call")
			return true
		}
		candidate, err := decodeIncrementalPreparedBackendCandidate(encoded)
		if err != nil {
			updateErr = err
			return true
		}
		reachable, err := incrementalPreparedBackendReachable(&candidate, index)
		if err != nil {
			updateErr = err
			return true
		}
		updateErr = u.setBackendReachable(&candidate, location, reachable)
		return updateErr != nil
	})
	return updateErr
}

func collectPublicationIdentities(target map[string][]byte, result *incrementalComponentResult) {
	for index := range result.Published {
		value := &result.Published[index]
		identity := incrementalPublicationIdentityKey(value.Cell, value.Key)
		target[string(identity)] = identity
	}
}

func incrementalPublicationOwner(
	index *incrementalGroupIndex,
	identity []byte,
) (incrementalGroupInstanceID, bool, error) {
	if err := index.validateAuthentication(); err != nil {
		return incrementalGroupInstanceID{}, false, err
	}
	owners, exists := index.publications.Root().Get(identity)
	if !exists {
		return incrementalGroupInstanceID{}, false, nil
	}
	_, owner, exists := owners.Root().Minimum()
	if !exists {
		return incrementalGroupInstanceID{}, false, errors.New("incremental publication index has no owner")
	}
	return owner.instance, true, nil
}

func incrementalPreparedBackendReachable(
	candidate *incrementalPreparedBackendCandidate,
	index *incrementalGroupIndex,
) (bool, error) {
	if candidate.WhenAny == nil {
		return true, nil
	}
	id := candidate.instanceID()
	for _, key := range candidate.WhenAny.Keys {
		owner, exists, err := incrementalPublicationOwner(
			index, incrementalPublicationIdentityKey(candidate.WhenAny.Cell, key),
		)
		if err != nil {
			return false, err
		}
		if exists && owner == id {
			return true, nil
		}
	}
	return false, nil
}

func (c *incrementalPreparedBackendCandidate) instanceID() incrementalGroupInstanceID {
	return incrementalGroupInstanceID{
		component: c.Component,
		source:    c.Source,
		namespace: c.Namespace,
		name:      c.Name,
	}
}

func decodeIncrementalPreparedBackendCandidate(encoded string) (incrementalPreparedBackendCandidate, error) {
	var candidate incrementalPreparedBackendCandidate
	if err := json.Unmarshal([]byte(encoded), &candidate); err != nil {
		return incrementalPreparedBackendCandidate{}, fmt.Errorf("decoding incremental prepared backend candidate: %w", err)
	}
	if err := candidate.Backend.Validate(); err != nil {
		return incrementalPreparedBackendCandidate{}, err
	}
	return candidate, nil
}

func (u *incrementalPreparedPlanUpdate) finish() error {
	profiles := make([]string, 0, len(u.profileDirty))
	for profile := range u.profileDirty {
		profiles = append(profiles, profile)
	}
	slices.Sort(profiles)
	for _, profile := range profiles {
		if err := u.refreshProfile(profile); err != nil {
			return err
		}
	}
	outputs := make([]string, 0, len(u.outputDirty))
	for output := range u.outputDirty {
		outputs = append(outputs, output)
	}
	slices.Sort(outputs)
	for _, output := range outputs {
		if err := u.refreshOutput([]byte(output)); err != nil {
			return err
		}
	}
	if err := u.plan.selected.ValidateAuthentication(); err != nil {
		return err
	}
	u.plan.refreshOutputMemo(u.baseMemo, outputs)
	return nil
}

func (u *incrementalPreparedPlanUpdate) refreshProfile(name string) error {
	key := []byte(name)
	candidates := nestedTree(u.plan.profileCandidates, key)
	variants := nestedVariantTree(u.plan.profileVariants, key)
	standalone := nestedSet(u.plan.standaloneProfiles, key).Len() > 0
	required := nestedTree(u.plan.requirements, key).Len() > 0
	needed := standalone || required
	if variants.Len() > 1 {
		u.plan.conflictingProfiles, _, _ = u.plan.conflictingProfiles.Insert(key, name)
	} else {
		u.plan.conflictingProfiles, _, _ = u.plan.conflictingProfiles.Delete(key)
	}
	_, encoded, exists := candidates.Root().Minimum()
	if needed && !exists {
		u.plan.missingProfiles, _, _ = u.plan.missingProfiles.Insert(key, name)
	} else {
		u.plan.missingProfiles, _, _ = u.plan.missingProfiles.Delete(key)
	}
	selected, err := u.plan.selected.WithoutProfile(name)
	if err != nil {
		return err
	}
	u.plan.selected = selected
	if !needed || !exists {
		return nil
	}
	var candidate incrementalPreparedProfileCandidate
	if err := json.Unmarshal([]byte(encoded), &candidate); err != nil {
		return fmt.Errorf("decoding incremental prepared profile candidate: %w", err)
	}
	selected, err = u.plan.selected.WithProfile(candidate.Profile)
	if err != nil {
		return err
	}
	u.plan.selected = selected
	return nil
}

func (u *incrementalPreparedPlanUpdate) refreshOutput(key []byte) error {
	group, id, ok := parseIncrementalPreparedPlanOutputKey(key)
	if !ok {
		return errors.New("incremental prepared plan has an invalid output key")
	}
	encoded, exists := u.plan.instances.Root().Get(incrementalGroupInstanceKey(id))
	if !exists {
		u.plan.outputs, _, _ = u.plan.outputs.Delete(key)
		return nil
	}
	result, err := decodeIncrementalComponentResultString(encoded)
	if err != nil {
		return fmt.Errorf("decoding incremental prepared plan output instance: %w", err)
	}
	parts := make([]incrementalPreparedPlanOutputPart, 0, len(result.BackendPlanOutput))
	for _, part := range result.BackendPlanOutput {
		if part.BackendCall == nil {
			parts = append(parts, incrementalPreparedPlanOutputPart{Text: part.Text})
			continue
		}
		callIndex := int(*part.BackendCall)
		call := &result.BackendPlan[callIndex]
		location := incrementalGroupLocationKey(id, uint64(*part.BackendCall))
		candidates := nestedTree(u.plan.backendCandidates, []byte(call.Identity))
		winner, _, won := candidates.Root().Minimum()
		if won && bytes.Equal(winner, location) {
			parts = append(parts, incrementalPreparedPlanOutputPart{Backend: call.Identity})
		}
	}
	encodedOutput, err := json.Marshal(parts)
	if err != nil {
		return fmt.Errorf("encoding incremental prepared plan output: %w", err)
	}
	canonicalKey := incrementalPreparedPlanOutputKey(group, id)
	if !bytes.Equal(canonicalKey, key) {
		return errors.New("incremental prepared plan output key is not canonical")
	}
	u.plan.outputs, _, _ = u.plan.outputs.Insert(key, string(encodedOutput))
	return nil
}

func (p *incrementalPreparedPlan) prepareRegistry(
	groups []string,
	indexes map[string]*incrementalGroupIndex,
	resultRoot *iradix.Node[incremental.ExactValueRoot],
	registry incrementalPreparedPlanRegistry,
) error {
	if err := p.validateAuthentication(resultRoot); err != nil {
		return err
	}
	for _, group := range groups {
		linked, exists := p.groups.Root().Get([]byte(group))
		if !exists || linked != indexes[group] {
			return fmt.Errorf("incremental prepared plan group %q does not match its assembly index", group)
		}
		if err := linked.validateAuthentication(); err != nil {
			return err
		}
	}
	if _, name, exists := p.conflictingProfiles.Root().Minimum(); exists {
		return fmt.Errorf("backendPlan profile %q declared twice with different text", name)
	}
	if _, name, exists := p.missingProfiles.Root().Minimum(); exists {
		return fmt.Errorf("backendPlan winning backend references undeclared profile %q", name)
	}
	return registry.AttachPreparedPlan(p.selected)
}

func (p *incrementalPreparedPlan) output(
	group, component string,
	resultRoot *iradix.Node[incremental.ExactValueRoot],
	registry incrementalPreparedPlanRegistry,
) (string, error) {
	fragment, err := p.outputFragment(group, component, resultRoot, registry)
	if err != nil {
		return "", err
	}
	return fragment.String()
}

func (p *incrementalPreparedPlan) outputFragment(
	group, component string,
	resultRoot *iradix.Node[incremental.ExactValueRoot],
	registry incrementalPreparedPlanRegistry,
) (rendercontent.Output, error) {
	if err := p.validateAuthentication(resultRoot); err != nil {
		return rendercontent.Output{}, err
	}
	authority, err := registry.PreparedPlanTokenAuthority()
	if err != nil {
		return rendercontent.Output{}, err
	}
	key := incrementalPreparedPlanOutputMemoKey{authority: authority, group: group, component: component}
	return p.outputMemo.output(key, registry)
}

func (p *incrementalPreparedPlan) validateAuthentication(
	resultRoot *iradix.Node[incremental.ExactValueRoot],
) error {
	if !p.available() {
		return errors.New("incremental prepared plan is unavailable")
	}
	if !p.authenticationMatches(resultRoot) {
		return errors.New("incremental prepared plan authentication seal does not match its roots")
	}
	return p.selected.ValidateAuthentication()
}

func (p *incrementalPreparedPlan) available() bool {
	return p != nil && p.instances != nil && p.calls != nil && p.backendCandidates != nil &&
		p.profileCandidates != nil && p.profileVariants != nil && p.standaloneProfiles != nil &&
		p.remainingRootsAvailable()
}

func (p *incrementalPreparedPlan) remainingRootsAvailable() bool {
	return p.conditions != nil && p.requirements != nil && p.missingProfiles != nil &&
		p.conflictingProfiles != nil && p.outputs != nil && p.groups != nil && p.selected != nil
}

func (p *incrementalPreparedPlan) authenticationMatches(
	resultRoot *iradix.Node[incremental.ExactValueRoot],
) bool {
	return p.auth.instances == p.instances && p.auth.calls == p.calls &&
		p.auth.backendCandidates == p.backendCandidates && p.auth.profileCandidates == p.profileCandidates &&
		p.auth.profileVariants == p.profileVariants && p.auth.standaloneProfiles == p.standaloneProfiles &&
		p.authenticationRemainderMatches(resultRoot)
}

func (p *incrementalPreparedPlan) authenticationRemainderMatches(
	resultRoot *iradix.Node[incremental.ExactValueRoot],
) bool {
	return p.auth.conditions == p.conditions && p.auth.requirements == p.requirements &&
		p.auth.missingProfiles == p.missingProfiles && p.auth.conflictingProfiles == p.conflictingProfiles &&
		p.auth.outputs == p.outputs && p.auth.groups == p.groups && p.auth.selected == p.selected &&
		p.auth.resultRoot == p.resultRoot && p.resultRoot == resultRoot && p.auth.outputMemo == p.outputMemo &&
		p.outputMemo.valid(p.outputs.Root(), p.selected)
}

func (p *incrementalPreparedPlan) authenticate() {
	p.auth = incrementalPreparedPlanAuthentication{
		instances: p.instances, calls: p.calls, backendCandidates: p.backendCandidates,
		profileCandidates: p.profileCandidates, profileVariants: p.profileVariants,
		standaloneProfiles: p.standaloneProfiles, conditions: p.conditions,
		requirements: p.requirements, missingProfiles: p.missingProfiles,
		conflictingProfiles: p.conflictingProfiles, outputs: p.outputs, groups: p.groups,
		selected: p.selected, resultRoot: p.resultRoot, outputMemo: p.outputMemo,
	}
}

func (p *incrementalPreparedPlan) refreshOutputMemo(
	base *incrementalPreparedPlanOutputMemo,
	changes []string,
) {
	if base != nil && base.root == p.outputs.Root() && base.selected == p.selected {
		p.outputMemo = base
		return
	}
	parent := base
	if parent == nil || parent.selected != p.selected || parent.depth >= incrementalPreparedPlanOutputMemoMaxDepth {
		parent = nil
	}
	p.outputMemo = newIncrementalPreparedPlanOutputMemo(p.outputs.Root(), p.selected, parent, changes)
}

func newIncrementalPreparedPlanOutputMemo(
	root *iradix.Node[string],
	selected *rendercontext.PreparedPlanSnapshot,
	parent *incrementalPreparedPlanOutputMemo,
	changes []string,
) *incrementalPreparedPlanOutputMemo {
	changeSet := iradix.New[struct{}]()
	for _, changed := range changes {
		changeSet, _, _ = changeSet.Insert([]byte(changed), struct{}{})
	}
	memo := &incrementalPreparedPlanOutputMemo{
		root: root, selected: selected, parent: parent, changes: changeSet, entries: &sync.Map{},
	}
	if parent != nil {
		memo.depth = parent.depth + 1
	}
	memo.auth = incrementalPreparedPlanOutputMemoAuthentication{
		owner: memo, root: memo.root, selected: memo.selected, parent: memo.parent,
		changes: memo.changes, depth: memo.depth, entries: memo.entries,
	}
	return memo
}

func (m *incrementalPreparedPlanOutputMemo) valid(
	root *iradix.Node[string],
	selected *rendercontext.PreparedPlanSnapshot,
) bool {
	return m != nil && m.authenticationMatches() && m.root == root && m.selected == selected &&
		(m.parent == nil || m.parent.authenticationMatches())
}

func (m *incrementalPreparedPlanOutputMemo) authenticationMatches() bool {
	return m != nil && m.auth.owner == m && m.auth.root == m.root && m.auth.selected == m.selected &&
		m.auth.parent == m.parent && m.auth.changes == m.changes && m.auth.depth == m.depth &&
		m.auth.entries == m.entries && m.changes != nil && m.entries != nil
}

func (m *incrementalPreparedPlanOutputMemo) output(
	key incrementalPreparedPlanOutputMemoKey,
	registry incrementalPreparedPlanRegistry,
) (rendercontent.Output, error) {
	if !m.valid(m.root, m.selected) || key.authority == nil || key.group == "" || key.component == "" {
		return rendercontent.Output{}, errors.New("incremental prepared plan output memo has invalid provenance")
	}
	if cached, exists := m.entries.Load(key); exists {
		entry, ok := cached.(*incrementalPreparedPlanOutputMemoEntry)
		if !ok || !entry.valid(m, key) {
			return rendercontent.Output{}, errors.New("incremental prepared plan output memo entry has invalid provenance")
		}
		return entry.output, nil
	}

	output, err := m.build(key, registry)
	if err != nil {
		return rendercontent.Output{}, err
	}
	entry := &incrementalPreparedPlanOutputMemoEntry{owner: m, key: key, output: output}
	entry.auth = incrementalPreparedPlanOutputMemoEntryAuthentication{
		owner: entry, memo: m, key: key, output: output,
	}
	cached, loaded := m.entries.LoadOrStore(key, entry)
	if !loaded {
		return output, nil
	}
	existing, ok := cached.(*incrementalPreparedPlanOutputMemoEntry)
	if !ok || !existing.valid(m, key) {
		return rendercontent.Output{}, errors.New("incremental prepared plan output memo entry has invalid provenance")
	}
	return existing.output, nil
}

func (e *incrementalPreparedPlanOutputMemoEntry) valid(
	owner *incrementalPreparedPlanOutputMemo,
	key incrementalPreparedPlanOutputMemoKey,
) bool {
	return e != nil && e.auth.owner == e && e.auth.memo == e.owner && e.auth.key == e.key &&
		e.auth.output == e.output && e.owner == owner && e.key == key &&
		e.output.ValidateAuthentication() == nil
}

func (m *incrementalPreparedPlanOutputMemo) build(
	key incrementalPreparedPlanOutputMemoKey,
	registry incrementalPreparedPlanRegistry,
) (rendercontent.Output, error) {
	prefix := incrementalOrderedTuple(key.group, key.component)
	if m.parent == nil {
		changes := make([]rendercontent.Change, 0)
		var walkErr error
		m.root.WalkPrefix(prefix, func(outputKey []byte, encoded string) bool {
			text, err := materializeIncrementalPreparedPlanOutput(encoded, registry)
			if err != nil {
				walkErr = err
				return true
			}
			changes = append(changes, rendercontent.Change{Key: string(outputKey), Text: text})
			return false
		})
		if walkErr != nil {
			return rendercontent.Output{}, walkErr
		}
		return rendercontent.FromSorted(changes)
	}

	base, err := m.parent.output(key, registry)
	if err != nil {
		return rendercontent.Output{}, err
	}
	changes := make([]rendercontent.Change, 0, m.changes.Len())
	var walkErr error
	m.changes.Root().WalkPrefix(prefix, func(changedKey []byte, _ struct{}) bool {
		encoded, exists := m.root.Get(changedKey)
		text := ""
		if exists {
			text, err = materializeIncrementalPreparedPlanOutput(encoded, registry)
			if err != nil {
				walkErr = err
				return true
			}
		}
		changes = append(changes, rendercontent.Change{Key: string(changedKey), Text: text})
		return false
	})
	if walkErr != nil {
		return rendercontent.Output{}, walkErr
	}
	return base.Apply(changes)
}

func materializeIncrementalPreparedPlanOutput(
	encoded string,
	registry incrementalPreparedPlanRegistry,
) (string, error) {
	var parts []incrementalPreparedPlanOutputPart
	if err := json.Unmarshal([]byte(encoded), &parts); err != nil {
		return "", fmt.Errorf("decoding incremental prepared plan output: %w", err)
	}
	var builder strings.Builder
	for _, part := range parts {
		if (part.Text == "") == (part.Backend == "") {
			return "", errors.New("incremental prepared plan output part is invalid")
		}
		if part.Text != "" {
			builder.WriteString(part.Text)
			continue
		}
		token, err := registry.PreparedBackendToken(part.Backend)
		if err != nil {
			return "", err
		}
		builder.WriteString(token)
	}
	return builder.String(), nil
}

func incrementalPreparedPlanConditionKey(group, cell, key string) []byte {
	return incrementalOrderedTuple(group, string(incrementalPublicationIdentityKey(cell, key)))
}

func incrementalPreparedPlanOutputKey(group string, id incrementalGroupInstanceID) []byte {
	return incrementalOrderedTuple(group, id.component, id.source, id.namespace, id.name)
}

func parseIncrementalPreparedPlanOutputKey(key []byte) (string, incrementalGroupInstanceID, bool) {
	parts, ok := decodeIncrementalOrderedTuple(key)
	if !ok || len(parts) != 5 {
		return "", incrementalGroupInstanceID{}, false
	}
	return parts[0], incrementalGroupInstanceID{
		component: parts[1], source: parts[2], namespace: parts[3], name: parts[4],
	}, true
}

func decodeIncrementalOrderedTuple(encoded []byte) ([]string, bool) {
	parts := make([]string, 0)
	current := make([]byte, 0, 16)
	for index := 0; index < len(encoded); {
		if encoded[index] != 0 {
			current = append(current, encoded[index])
			index++
			continue
		}
		if index+1 >= len(encoded) {
			return nil, false
		}
		switch encoded[index+1] {
		case 0:
			parts = append(parts, string(current))
			current = make([]byte, 0, 16)
		case 0xff:
			current = append(current, 0)
		default:
			return nil, false
		}
		index += 2
	}
	return parts, len(current) == 0
}

func insertNestedValue[T any](
	outer *iradix.Tree[*iradix.Tree[T]],
	outerKey, innerKey []byte,
	value T,
) *iradix.Tree[*iradix.Tree[T]] {
	inner := nestedGenericTree(outer, outerKey)
	inner, _, _ = inner.Insert(innerKey, value)
	outer, _, _ = outer.Insert(outerKey, inner)
	return outer
}

func deleteNestedValue[T any](
	outer *iradix.Tree[*iradix.Tree[T]],
	outerKey, innerKey []byte,
) (*iradix.Tree[*iradix.Tree[T]], bool) {
	inner, exists := outer.Root().Get(outerKey)
	if !exists {
		return outer, false
	}
	updated, _, removed := inner.Delete(innerKey)
	if !removed {
		return outer, false
	}
	return storeNestedGenericTree(outer, outerKey, updated), true
}

func nestedValue[T any](
	outer *iradix.Tree[*iradix.Tree[T]],
	outerKey, innerKey []byte,
) (T, bool) {
	return nestedGenericTree(outer, outerKey).Root().Get(innerKey)
}

func nestedGenericTree[T any](
	outer *iradix.Tree[*iradix.Tree[T]],
	key []byte,
) *iradix.Tree[T] {
	inner, exists := outer.Root().Get(key)
	if !exists {
		return iradix.New[T]()
	}
	return inner
}

func storeNestedGenericTree[T any](
	outer *iradix.Tree[*iradix.Tree[T]],
	key []byte,
	inner *iradix.Tree[T],
) *iradix.Tree[*iradix.Tree[T]] {
	if inner.Len() == 0 {
		outer, _, _ = outer.Delete(key)
		return outer
	}
	outer, _, _ = outer.Insert(key, inner)
	return outer
}

func nestedTree(outer *iradix.Tree[*iradix.Tree[string]], key []byte) *iradix.Tree[string] {
	return nestedGenericTree(outer, key)
}

func storeNestedTree(
	outer *iradix.Tree[*iradix.Tree[string]],
	key []byte,
	inner *iradix.Tree[string],
) *iradix.Tree[*iradix.Tree[string]] {
	return storeNestedGenericTree(outer, key, inner)
}

func nestedSet(outer *iradix.Tree[*iradix.Tree[struct{}]], key []byte) *iradix.Tree[struct{}] {
	return nestedGenericTree(outer, key)
}

func insertNestedSet(
	outer *iradix.Tree[*iradix.Tree[struct{}]],
	outerKey, innerKey []byte,
) *iradix.Tree[*iradix.Tree[struct{}]] {
	return insertNestedValue(outer, outerKey, innerKey, struct{}{})
}

func deleteNestedSet(
	outer *iradix.Tree[*iradix.Tree[struct{}]],
	outerKey, innerKey []byte,
) (*iradix.Tree[*iradix.Tree[struct{}]], bool) {
	return deleteNestedValue(outer, outerKey, innerKey)
}

func minimumNestedValue(tree *iradix.Tree[string]) (key []byte, value string, exists bool) {
	key, value, exists = tree.Root().Minimum()
	return slices.Clone(key), value, exists
}

func insertProfileVariant(
	outer *iradix.Tree[*iradix.Tree[*iradix.Tree[struct{}]]],
	profile, text, location []byte,
) *iradix.Tree[*iradix.Tree[*iradix.Tree[struct{}]]] {
	variants := nestedVariantTree(outer, profile)
	locations := nestedSet(variants, text)
	locations, _, _ = locations.Insert(location, struct{}{})
	variants, _, _ = variants.Insert(text, locations)
	outer, _, _ = outer.Insert(profile, variants)
	return outer
}

func deleteProfileVariant(
	outer *iradix.Tree[*iradix.Tree[*iradix.Tree[struct{}]]],
	profile, text, location []byte,
) (*iradix.Tree[*iradix.Tree[*iradix.Tree[struct{}]]], bool) {
	variants, exists := outer.Root().Get(profile)
	if !exists {
		return outer, false
	}
	locations, exists := variants.Root().Get(text)
	if !exists {
		return outer, false
	}
	locations, _, removed := locations.Delete(location)
	if !removed {
		return outer, false
	}
	if locations.Len() == 0 {
		variants, _, _ = variants.Delete(text)
	} else {
		variants, _, _ = variants.Insert(text, locations)
	}
	if variants.Len() == 0 {
		outer, _, _ = outer.Delete(profile)
	} else {
		outer, _, _ = outer.Insert(profile, variants)
	}
	return outer, true
}

func nestedVariantTree(
	outer *iradix.Tree[*iradix.Tree[*iradix.Tree[struct{}]]],
	key []byte,
) *iradix.Tree[*iradix.Tree[struct{}]] {
	variants, exists := outer.Root().Get(key)
	if !exists {
		return iradix.New[*iradix.Tree[struct{}]]()
	}
	return variants
}
