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
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"sync"

	iradix "github.com/hashicorp/go-immutable-radix/v2"

	"gitlab.com/haproxy-haptic/haptic/pkg/incremental"
)

type incrementalPreparedPlanColdComponent struct {
	group       string
	backendPlan bool
}

type incrementalPreparedPlanColdBuilder struct {
	mu sync.Mutex

	groups     []string
	components map[string]incrementalPreparedPlanColdComponent
	batches    []*incrementalPreparedPlanColdBatch
	instances  map[string]struct{}
	locations  map[string]struct{}
	outputs    map[string]struct{}
	finalized  bool
}

type incrementalPreparedPlanColdBatch struct {
	owner      *incrementalPreparedPlanColdBuilder
	projection *incrementalPreparedPlanBootstrapBuilder
	seal       *incrementalPreparedPlanColdBatch
	committed  bool
}

type incrementalPreparedPlanBackendDigestEnvelope struct {
	BackendPlanDigest string `json:"backendPlanDigest,omitempty"`
}

func newIncrementalPreparedPlanColdBuilder(
	groups []string,
	components map[string]incrementalComponent,
) (*incrementalPreparedPlanColdBuilder, error) {
	tracked := make(map[string]struct{}, len(groups))
	ownedGroups := slices.Clone(groups)
	slices.Sort(ownedGroups)
	for index, group := range ownedGroups {
		if group == "" {
			return nil, errors.New("incremental prepared plan has an empty group")
		}
		if index > 0 && group == ownedGroups[index-1] {
			return nil, fmt.Errorf("incremental prepared plan repeats group %q", group)
		}
		tracked[group] = struct{}{}
	}
	ownedComponents := make(map[string]incrementalPreparedPlanColdComponent, len(components))
	for name := range components {
		component := components[name]
		if name == "" || component.name != name || component.group == "" {
			return nil, fmt.Errorf("incremental prepared plan component %q has invalid provenance", name)
		}
		if component.backendPlan {
			if _, exists := tracked[component.group]; !exists {
				return nil, fmt.Errorf(
					"incremental backendPlan component %q has untracked group %q", name, component.group,
				)
			}
		}
		ownedComponents[name] = incrementalPreparedPlanColdComponent{
			group: component.group, backendPlan: component.backendPlan,
		}
	}
	return &incrementalPreparedPlanColdBuilder{
		groups:     ownedGroups,
		components: ownedComponents,
		instances:  make(map[string]struct{}),
		locations:  make(map[string]struct{}),
		outputs:    make(map[string]struct{}),
	}, nil
}

func (b *incrementalPreparedPlanColdBuilder) covers(group string) bool {
	return b != nil && slices.Contains(b.groups, group)
}

func (b *incrementalPreparedPlanColdBuilder) prepareValidatedGroupAdditions(
	group string,
	index *incrementalGroupIndex,
	additions []incrementalPreparedPlanGroupAddition,
) (*incrementalPreparedPlanColdBatch, error) {
	if b == nil {
		return nil, errors.New("incremental prepared plan cold builder is unavailable")
	}
	b.mu.Lock()
	finalized := b.finalized
	b.mu.Unlock()
	if finalized {
		return nil, errors.New("incremental prepared plan cold builder is finalized")
	}
	if !b.covers(group) {
		return nil, fmt.Errorf("incremental prepared plan cold builder does not cover group %q", group)
	}
	if index == nil {
		return nil, fmt.Errorf("incremental group %q has no assembly index", group)
	}
	if err := index.validateAuthentication(); err != nil {
		return nil, fmt.Errorf("authenticating incremental group %q: %w", group, err)
	}
	projection := newIncrementalPreparedPlanBootstrapBuilder(len(additions))
	for additionIndex := range additions {
		if err := b.addValidatedGroupAddition(
			group, index, &additions[additionIndex], projection,
		); err != nil {
			return nil, err
		}
	}
	batch := &incrementalPreparedPlanColdBatch{owner: b, projection: projection}
	batch.seal = batch
	return batch, nil
}

func (b *incrementalPreparedPlanColdBuilder) addValidatedGroupAddition(
	group string,
	index *incrementalGroupIndex,
	addition *incrementalPreparedPlanGroupAddition,
	projection *incrementalPreparedPlanBootstrapBuilder,
) error {
	if addition.component == nil || addition.result == nil {
		return errors.New("incremental prepared plan cold addition is incomplete")
	}
	component, exists := b.components[addition.id.component]
	if !exists || addition.component.name != addition.id.component ||
		addition.component.group != group || component.group != group ||
		addition.component.backendPlan != component.backendPlan {
		return errors.New("incremental prepared plan cold addition has invalid component provenance")
	}
	indexed, exists := index.instances.Root().Get(incrementalGroupInstanceKey(addition.id))
	if !exists || indexed.id != addition.id {
		return fmt.Errorf(
			"incremental group %q assembly index is missing a prepared plan addition", group,
		)
	}
	if !component.backendPlan {
		return nil
	}
	failure := &incrementalInstanceResult{
		component: addition.id.component,
		source:    addition.id.source,
		namespace: addition.id.namespace,
		name:      addition.id.name,
	}
	encodedDigest, err := incrementalPreparedPlanEncodedBackendDigest(indexed.encodedResult)
	if err != nil {
		return incrementalInstanceError(failure, err)
	}
	if encodedDigest != addition.result.BackendPlanDigest {
		return errors.New("incremental prepared plan cold addition does not match its assembly index")
	}
	instance := incrementalPreparedPlanBootstrapInstance{
		group:   group,
		id:      addition.id,
		key:     incrementalPreparedPlanBootstrapSeenKey(group, addition.id),
		encoded: indexed.encodedResult,
	}
	if err := projection.addValidatedInstance(&instance, addition.result); err != nil {
		return incrementalInstanceError(failure, err)
	}
	return nil
}

func incrementalPreparedPlanEncodedBackendDigest(encoded string) (string, error) {
	var envelope incrementalPreparedPlanBackendDigestEnvelope
	if err := json.Unmarshal([]byte(encoded), &envelope); err != nil {
		return "", fmt.Errorf("decoding incremental prepared plan digest: %w", err)
	}
	return envelope.BackendPlanDigest, nil
}

func (b *incrementalPreparedPlanColdBuilder) commit(
	batch *incrementalPreparedPlanColdBatch,
) error {
	if batch == nil {
		return nil
	}
	if b == nil {
		return errors.New("incremental prepared plan cold builder is unavailable")
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.finalized {
		return errors.New("incremental prepared plan cold builder is finalized")
	}
	if batch.owner != b || batch.seal != batch || batch.projection == nil || batch.committed {
		return errors.New("incremental prepared plan cold batch has invalid provenance")
	}
	instanceKeys := incrementalPreparedPlanSortedKeys(batch.projection.instances)
	locationKeys := incrementalPreparedPlanColdLocationKeys(batch.projection)
	outputKeys := incrementalPreparedPlanSortedKeys(batch.projection.pendingOutputKeys)
	if duplicateIncrementalPreparedPlanColdKey(b.instances, instanceKeys) ||
		duplicateIncrementalPreparedPlanColdKey(b.locations, locationKeys) ||
		duplicateIncrementalPreparedPlanColdKey(b.outputs, outputKeys) {
		return errors.New("incremental prepared plan cold batch repeats a committed identity")
	}
	insertIncrementalPreparedPlanColdKeys(b.instances, instanceKeys)
	insertIncrementalPreparedPlanColdKeys(b.locations, locationKeys)
	insertIncrementalPreparedPlanColdKeys(b.outputs, outputKeys)
	b.batches = append(b.batches, batch)
	batch.committed = true
	return nil
}

func incrementalPreparedPlanColdLocationKeys(
	builder *incrementalPreparedPlanBootstrapBuilder,
) []string {
	keys := make([]string, 0, len(builder.calls))
	for key := range builder.calls {
		keys = append(keys, key)
	}
	for _, candidates := range builder.profileValues {
		for key := range candidates {
			keys = append(keys, key)
		}
	}
	slices.Sort(keys)
	return keys
}

func duplicateIncrementalPreparedPlanColdKey(existing map[string]struct{}, keys []string) bool {
	for _, key := range keys {
		if _, duplicate := existing[key]; duplicate {
			return true
		}
	}
	return false
}

func insertIncrementalPreparedPlanColdKeys(target map[string]struct{}, keys []string) {
	for _, key := range keys {
		target[key] = struct{}{}
	}
}

func (b *incrementalPreparedPlanColdBuilder) finalize(
	groups []string,
	indexes map[string]*incrementalGroupIndex,
	components map[string]incrementalComponent,
	resultRoot *iradix.Node[incremental.ExactValueRoot],
	validator incrementalPreparedPlanExactRootValidator,
) (*incrementalPreparedPlan, bool, error) {
	if b == nil {
		return nil, false, errors.New("incremental prepared plan cold builder is unavailable")
	}
	b.mu.Lock()
	if b.finalized {
		b.mu.Unlock()
		return nil, false, errors.New("incremental prepared plan cold builder is finalized")
	}
	b.finalized = true
	batches := slices.Clone(b.batches)
	b.batches = nil
	b.instances = nil
	b.locations = nil
	b.outputs = nil
	b.mu.Unlock()
	exact := func() (*incrementalPreparedPlan, bool, error) {
		plan, err := newIncrementalPreparedPlanFromIndexes(
			groups, indexes, components, resultRoot, validator,
		)
		return plan, false, err
	}
	if !b.matchesConfiguration(groups, components) {
		return exact()
	}
	groupIndexes, trackedGroups, err := validateIncrementalPreparedPlanBootstrapGroups(
		groups, indexes, components,
	)
	if err != nil {
		return nil, false, err
	}
	instances, err := collectIncrementalPreparedPlanBootstrapInstances(
		trackedGroups, indexes, components, resultRoot, validator,
	)
	if err != nil {
		return nil, false, err
	}
	var projection *incrementalPreparedPlanBootstrapBuilder
	if len(batches) == 1 && batches[0] != nil {
		projection = batches[0].projection
	} else {
		projection = newIncrementalPreparedPlanBootstrapBuilder(len(instances))
	}
	for batchIndex, batch := range batches {
		if batch == nil || batch.owner != b || batch.seal != batch || !batch.committed ||
			batch.projection == nil {
			return exact()
		}
		if len(batches) == 1 && batchIndex == 0 {
			continue
		}
		if err := mergeIncrementalPreparedPlanBootstrapBuilder(projection, batch.projection); err != nil {
			return exact()
		}
	}
	if !incrementalPreparedPlanColdCoverageMatches(projection, instances) {
		return exact()
	}
	plan, err := newIncrementalPreparedPlanFromBootstrapBuilder(
		projection, groupIndexes, indexes, resultRoot,
	)
	if err == nil {
		return plan, true, nil
	}
	return exact()
}

func (b *incrementalPreparedPlanColdBuilder) matchesConfiguration(
	groups []string,
	components map[string]incrementalComponent,
) bool {
	ownedGroups := slices.Clone(groups)
	slices.Sort(ownedGroups)
	if !slices.Equal(b.groups, ownedGroups) || len(b.components) != len(components) {
		return false
	}
	for name := range components {
		component := components[name]
		owned, exists := b.components[name]
		if !exists || component.name != name || component.group != owned.group ||
			component.backendPlan != owned.backendPlan {
			return false
		}
	}
	return true
}

func incrementalPreparedPlanColdCoverageMatches(
	projection *incrementalPreparedPlanBootstrapBuilder,
	instances []incrementalPreparedPlanBootstrapInstance,
) bool {
	if projection == nil || len(projection.instances) != len(instances) ||
		len(projection.pendingOutputKeys) != len(instances) {
		return false
	}
	for index := range instances {
		instance := &instances[index]
		encoded, exists := projection.instances[string(incrementalGroupInstanceKey(instance.id))]
		if !exists || encoded != instance.encoded {
			return false
		}
		if _, exists := projection.pendingOutputKeys[instance.key]; !exists {
			return false
		}
	}
	return true
}

func mergeIncrementalPreparedPlanBootstrapBuilder(
	target *incrementalPreparedPlanBootstrapBuilder,
	source *incrementalPreparedPlanBootstrapBuilder,
) error {
	if target == nil || source == nil || len(source.backendCandidates) != 0 ||
		len(source.backendWinners) != 0 || len(source.backendWinnerKeys) != 0 ||
		len(source.requirements) != 0 || len(source.missingProfiles) != 0 ||
		len(source.conflictingProfiles) != 0 || len(source.outputs) != 0 {
		return errors.New("incremental prepared plan cold projection is invalid")
	}
	if incrementalPreparedPlanMapsOverlap(target.instances, source.instances) ||
		incrementalPreparedPlanMapsOverlap(target.calls, source.calls) ||
		incrementalPreparedPlanMapsOverlap(target.backendCalls, source.backendCalls) ||
		incrementalPreparedPlanNestedMapsOverlap(target.profileCandidates, source.profileCandidates) ||
		incrementalPreparedPlanNestedMapsOverlap(target.profileValues, source.profileValues) ||
		incrementalPreparedPlanVariantMapsOverlap(target.profileVariants, source.profileVariants) ||
		incrementalPreparedPlanNestedMapsOverlap(target.standaloneProfiles, source.standaloneProfiles) ||
		incrementalPreparedPlanNestedMapsOverlap(target.conditions, source.conditions) ||
		incrementalPreparedPlanMapsOverlap(target.pendingOutputKeys, source.pendingOutputKeys) {
		return errors.New("incremental prepared plan cold projection repeats an identity")
	}
	mergeIncrementalPreparedPlanMap(target.instances, source.instances)
	mergeIncrementalPreparedPlanMap(target.calls, source.calls)
	mergeIncrementalPreparedPlanMap(target.backendCalls, source.backendCalls)
	mergeIncrementalPreparedPlanNestedMap(target.profileCandidates, source.profileCandidates)
	mergeIncrementalPreparedPlanNestedMap(target.profileValues, source.profileValues)
	mergeIncrementalPreparedPlanVariantMap(target.profileVariants, source.profileVariants)
	mergeIncrementalPreparedPlanNestedMap(target.standaloneProfiles, source.standaloneProfiles)
	mergeIncrementalPreparedPlanNestedMap(target.conditions, source.conditions)
	mergeIncrementalPreparedPlanMap(target.pendingOutputKeys, source.pendingOutputKeys)
	for outputIndex := range source.pendingOutputs {
		output := source.pendingOutputs[outputIndex]
		output.parts = slices.Clone(output.parts)
		target.pendingOutputs = append(target.pendingOutputs, output)
	}
	return nil
}

func incrementalPreparedPlanMapsOverlap[T any](left, right map[string]T) bool {
	for key := range right {
		if _, exists := left[key]; exists {
			return true
		}
	}
	return false
}

func incrementalPreparedPlanNestedMapsOverlap[T any](
	left, right map[string]map[string]T,
) bool {
	for outer, rightValues := range right {
		if incrementalPreparedPlanMapsOverlap(left[outer], rightValues) {
			return true
		}
	}
	return false
}

func incrementalPreparedPlanVariantMapsOverlap(
	left, right map[string]map[string]map[string]struct{},
) bool {
	for profile, rightVariants := range right {
		for variant, rightLocations := range rightVariants {
			if incrementalPreparedPlanMapsOverlap(left[profile][variant], rightLocations) {
				return true
			}
		}
	}
	return false
}

func mergeIncrementalPreparedPlanMap[T any](target, source map[string]T) {
	for key, value := range source {
		target[key] = value
	}
}

func mergeIncrementalPreparedPlanNestedMap[T any](
	target, source map[string]map[string]T,
) {
	for outer, sourceValues := range source {
		targetValues := target[outer]
		if targetValues == nil {
			targetValues = make(map[string]T, len(sourceValues))
			target[outer] = targetValues
		}
		mergeIncrementalPreparedPlanMap(targetValues, sourceValues)
	}
}

func mergeIncrementalPreparedPlanVariantMap(
	target, source map[string]map[string]map[string]struct{},
) {
	for profile, sourceVariants := range source {
		targetVariants := target[profile]
		if targetVariants == nil {
			targetVariants = make(map[string]map[string]struct{}, len(sourceVariants))
			target[profile] = targetVariants
		}
		mergeIncrementalPreparedPlanNestedMap(targetVariants, sourceVariants)
	}
}
