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
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"sync"

	"gitlab.com/haproxy-haptic/haptic/pkg/controller/rendercontext"
	"gitlab.com/haproxy-haptic/haptic/pkg/controller/renderer/internal/resultauthority"
	"gitlab.com/haproxy-haptic/haptic/pkg/dataplane/renderplan"
	"gitlab.com/haproxy-haptic/haptic/pkg/incremental"
	"gitlab.com/haproxy-haptic/haptic/pkg/templating"
)

type incrementalContribution struct {
	Cell  string `json:"cell"`
	Key   string `json:"key"`
	Value string `json:"value"`
}

type incrementalPublishedValue struct {
	Cell  string          `json:"cell"`
	Key   string          `json:"key"`
	Rank  string          `json:"rank,omitempty"`
	Value json.RawMessage `json:"value"`

	snapshot *incrementalPublicationSnapshot
}

type incrementalStatusPatchCall struct {
	Namespace       string          `json:"namespace,omitempty"`
	Name            string          `json:"name"`
	APIVersion      string          `json:"apiVersion"`
	Kind            string          `json:"kind"`
	UID             string          `json:"uid,omitempty"`
	ResourceVersion string          `json:"resourceVersion,omitempty"`
	Variants        json.RawMessage `json:"variants"`
	SourceTemplate  string          `json:"sourceTemplate,omitempty"`
	SourceLine      int             `json:"sourceLine,omitempty"`
}

type incrementalComponentResult struct {
	Text              string                          `json:"text,omitempty"`
	Unique            []incrementalContribution       `json:"unique,omitempty"`
	Published         []incrementalPublishedValue     `json:"published,omitempty"`
	PublishedDigest   string                          `json:"publishedDigest,omitempty"`
	Derivations       []rendercontext.DerivedResource `json:"derivations,omitempty"`
	Events            []templating.RenderedEvent      `json:"events,omitempty"`
	StatusPatches     []incrementalStatusPatchCall    `json:"statusPatches,omitempty"`
	StatusPatchDigest string                          `json:"statusPatchDigest,omitempty"`
	BackendPlan       []incrementalBackendPlanCall    `json:"backendPlan,omitempty"`
	BackendPlanOutput []incrementalBackendPlanPart    `json:"backendPlanOutput,omitempty"`
	BackendPlanDigest string                          `json:"backendPlanDigest,omitempty"`

	publicationGeneration *incrementalPublicationSnapshotGeneration
	publicationGroup      string
	publicationOwner      incrementalGroupInstanceID
}

type authenticatedFreshComponentResult struct {
	key       incremental.QueryKey
	encoded   string
	root      incremental.ExactValueRoot
	authority *resultauthority.Handle[incrementalComponentResult, authenticatedFreshComponentEffects]
	arena     *incrementalColdResultArena
	arenaRef  *resultauthority.Ref[*incrementalColdResultArenaValue, authenticatedFreshComponentEffects]
	arenaSlot int
	arenaGen  uint64
	seal      *authenticatedFreshComponentResult
}

type authenticatedFreshComponentEffects struct {
	component          string
	source             string
	namespace          string
	name               string
	resourceProjection bool
	publishValue       bool
	deriveResource     bool
	recordEvent        bool
	statusPatch        bool
	backendPlan        bool
}

func newAuthenticatedFreshComponentResult(
	key incremental.QueryKey,
	result *incrementalComponentResult,
) (string, *authenticatedFreshComponentResult, error) {
	return newAuthenticatedFreshComponentResultWithEffects(key, result, nil)
}

func newAuthenticatedFreshComponentResultWithEffects(
	key incremental.QueryKey,
	result *incrementalComponentResult,
	effects *authenticatedFreshComponentEffects,
) (string, *authenticatedFreshComponentResult, error) {
	return newAuthenticatedFreshComponentResultAuthority(key, result, effects, false)
}

func newAuthenticatedFreshComponentResultFromRecorder(
	key incremental.QueryKey,
	result *incrementalComponentResult,
	effects *authenticatedFreshComponentEffects,
) (string, *authenticatedFreshComponentResult, error) {
	return newAuthenticatedFreshComponentResultAuthority(key, result, effects, true)
}

func newAuthenticatedFreshComponentResultAuthority(
	key incremental.QueryKey,
	result *incrementalComponentResult,
	effects *authenticatedFreshComponentEffects,
	owned bool,
) (string, *authenticatedFreshComponentResult, error) {
	canonical, err := json.Marshal(result)
	if err != nil {
		return "", nil, fmt.Errorf("encoding fresh incremental component result: %w", err)
	}
	encoded := string(canonical)
	fresh := &authenticatedFreshComponentResult{key: key, encoded: encoded}
	if owned {
		fresh.authority = resultauthority.NewOwned(key, encoded, *result, effects)
	} else {
		fresh.authority = resultauthority.New(
			key, encoded, *result, effects, cloneIncrementalComponentResult,
		)
	}
	fresh.seal = fresh
	return encoded, fresh, nil
}

func validatePendingAuthenticatedFreshComponentResult(
	fresh *authenticatedFreshComponentResult,
	key incremental.QueryKey,
) error {
	if err := validateAuthenticatedFreshComponentResultWrapper(fresh, key); err != nil {
		return err
	}
	if fresh.arena != nil {
		return fresh.arena.pending(fresh, key)
	}
	return resultauthority.Pending(fresh.authority, key, fresh.encoded, fresh.root)
}

func bindAuthenticatedFreshComponentResult(
	fresh *authenticatedFreshComponentResult,
	key incremental.QueryKey,
	root incremental.ExactValueRoot,
) error {
	if err := validateAuthenticatedFreshComponentResultWrapper(fresh, key); err != nil {
		return err
	}
	if fresh.arena != nil {
		if err := fresh.arena.bind(fresh, key, root); err != nil {
			return err
		}
		fresh.root = root
		return nil
	}
	if err := resultauthority.Bind(fresh.authority, key, fresh.encoded, fresh.root, root); err != nil {
		return err
	}
	fresh.root = root
	return nil
}

func validateAuthenticatedFreshComponentResult(
	fresh *authenticatedFreshComponentResult,
	key incremental.QueryKey,
	root incremental.ExactValueRoot,
) error {
	if err := validateAuthenticatedFreshComponentResultWrapper(fresh, key); err != nil {
		return err
	}
	if fresh.arena != nil {
		return fresh.arena.validate(fresh, key, root)
	}
	return resultauthority.Validate(fresh.authority, key, fresh.encoded, fresh.root, root)
}

func materializeAuthenticatedFreshComponentResult(
	fresh *authenticatedFreshComponentResult,
	key incremental.QueryKey,
	root incremental.ExactValueRoot,
) (incrementalComponentResult, error) {
	if err := validateAuthenticatedFreshComponentResultWrapper(fresh, key); err != nil {
		return incrementalComponentResult{}, err
	}
	if fresh.arena != nil {
		return fresh.arena.materialize(fresh, key, root)
	}
	return resultauthority.Materialize(
		fresh.authority, key, fresh.encoded, fresh.root, root, cloneIncrementalComponentResult,
	)
}

func takeAuthenticatedFreshComponentResult(
	fresh *authenticatedFreshComponentResult,
	key incremental.QueryKey,
	root incremental.ExactValueRoot,
) (incrementalComponentResult, error) {
	if err := validateAuthenticatedFreshComponentResultWrapper(fresh, key); err != nil {
		return incrementalComponentResult{}, err
	}
	if fresh.arena != nil {
		return fresh.arena.take(fresh, key, root)
	}
	return resultauthority.Take(fresh.authority, key, fresh.encoded, fresh.root, root)
}

func validateAuthenticatedFreshComponentEffects(
	fresh *authenticatedFreshComponentResult,
	key incremental.QueryKey,
	root incremental.ExactValueRoot,
	component *incrementalComponent,
	source, namespace, name string,
) (bool, error) {
	effects, err := newAuthenticatedFreshComponentEffects(component, source, namespace, name)
	if err != nil {
		return false, err
	}
	if err := validateAuthenticatedFreshComponentResultWrapper(fresh, key); err != nil {
		return false, err
	}
	if fresh.arena != nil {
		err = fresh.arena.metadataMatches(fresh, key, root, effects)
		if errors.Is(err, resultauthority.ErrMetadataUnavailable) {
			return false, nil
		}
		return err == nil, err
	}
	err = resultauthority.MetadataMatches(
		fresh.authority, key, fresh.encoded, fresh.root, root, effects,
	)
	if errors.Is(err, resultauthority.ErrMetadataUnavailable) {
		return false, nil
	}
	return err == nil, err
}

func validateAuthenticatedFreshComponentResultWrapper(
	fresh *authenticatedFreshComponentResult,
	key incremental.QueryKey,
) error {
	if fresh == nil || fresh.seal != fresh || fresh.key != key {
		return errors.New("fresh incremental component result has invalid provenance")
	}
	if fresh.arena != nil {
		return fresh.arena.validateWrapper(fresh, key)
	}
	if fresh.authority == nil || fresh.arenaRef != nil || fresh.arenaSlot != 0 || fresh.arenaGen != 0 {
		return errors.New("fresh incremental component result has invalid provenance")
	}
	return nil
}

func newAuthenticatedFreshComponentEffects(
	component *incrementalComponent,
	source, namespace, name string,
) (authenticatedFreshComponentEffects, error) {
	if component == nil {
		return authenticatedFreshComponentEffects{}, errors.New("fresh incremental component effects have no component")
	}
	return authenticatedFreshComponentEffects{
		component:          component.name,
		source:             source,
		namespace:          namespace,
		name:               name,
		resourceProjection: component.resourceProjection,
		publishValue:       component.publishValue,
		deriveResource:     component.deriveResource,
		recordEvent:        component.recordEvent,
		statusPatch:        component.statusPatch,
		backendPlan:        component.backendPlan,
	}, nil
}

func stringBytesEqual(left string, right []byte) bool {
	if len(left) != len(right) {
		return false
	}
	for index := range right {
		if left[index] != right[index] {
			return false
		}
	}
	return true
}

func cloneIncrementalComponentResult(source *incrementalComponentResult) incrementalComponentResult {
	if source == nil {
		return incrementalComponentResult{}
	}
	var derivations []rendercontext.DerivedResource
	if len(source.Derivations) > 0 {
		derivations = make([]rendercontext.DerivedResource, len(source.Derivations))
		for index := range source.Derivations {
			derivations[index] = cloneDerivedResource(&source.Derivations[index])
		}
	}
	return incrementalComponentResult{
		Text:                  source.Text,
		Unique:                slices.Clone(source.Unique),
		Published:             cloneIncrementalPublishedValues(source.Published),
		PublishedDigest:       source.PublishedDigest,
		Derivations:           derivations,
		Events:                slices.Clone(source.Events),
		StatusPatches:         cloneIncrementalStatusPatchCalls(source.StatusPatches),
		StatusPatchDigest:     source.StatusPatchDigest,
		BackendPlan:           cloneIncrementalBackendPlanCalls(source.BackendPlan),
		BackendPlanOutput:     cloneIncrementalBackendPlanParts(source.BackendPlanOutput),
		BackendPlanDigest:     source.BackendPlanDigest,
		publicationGeneration: source.publicationGeneration,
		publicationGroup:      source.publicationGroup,
		publicationOwner:      source.publicationOwner,
	}
}

type incrementalRecorder struct {
	mu                    sync.Mutex
	lease                 *incrementalBatchReaderLease
	publicationGeneration *incrementalPublicationSnapshotGeneration
	publicationGroup      string
	publicationOwner      incrementalGroupInstanceID
	unique                []incrementalContribution
	published             []incrementalPublishedValue
	events                *templating.EventCollector
	patches               []incrementalStatusPatchCall
	deriver               *incrementalResourceDeriver
	plan                  *incrementalBackendPlanRecorder
	err                   error
}

type incrementalPreflightRecorder struct {
	recorder *incrementalRecorder
}

func (r *incrementalRecorder) Unique(cell, key, value string) {
	release, err := beginIncrementalCapability(r.lease, "shared.Unique")
	if err != nil {
		r.recordCapabilityViolation(err)
		return
	}
	defer release()
	r.recordUnique(cell, key, value)
}

func (r *incrementalRecorder) recordUnique(cell, key, value string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.err != nil {
		return
	}
	if cell == "" || key == "" {
		r.err = errors.New("shared.Unique requires a non-empty cell and key")
		return
	}
	r.unique = append(r.unique, incrementalContribution{Cell: cell, Key: key, Value: value})
}

func (r *incrementalRecorder) Publish(cell, key string, value any) {
	r.publish(cell, key, "", value, "shared.Publish")
}

func (r *incrementalRecorder) PublishRanked(cell, key, rank string, value any) {
	r.publish(cell, key, rank, value, "shared.PublishRanked")
}

func (r *incrementalRecorder) publish(cell, key, rank string, value any, operation string) {
	detached, err := templating.NewIncrementalDetachedValue(value)
	if err != nil {
		r.recordCapabilityViolation(fmt.Errorf("%s value is not JSON serializable: %w", operation, err))
		return
	}
	r.publishDetached(cell, key, rank, detached, operation)
}

func (r *incrementalRecorder) PublishDetached(
	cell, key string,
	value *templating.IncrementalDetachedValue,
) {
	r.publishDetached(cell, key, "", value, "shared.Publish")
}

func (r *incrementalRecorder) PublishRankedDetached(
	cell, key, rank string,
	value *templating.IncrementalDetachedValue,
) {
	r.publishDetached(cell, key, rank, value, "shared.PublishRanked")
}

func (r *incrementalRecorder) publishDetached(
	cell, key, rank string,
	value *templating.IncrementalDetachedValue,
	operation string,
) {
	release, err := beginIncrementalCapability(r.lease, operation)
	if err != nil {
		r.recordCapabilityViolation(err)
		return
	}
	defer release()
	r.publishDetachedAfterPreflight(cell, key, rank, value, operation)
}

func (r *incrementalRecorder) publishAfterPreflight(cell, key, rank string, value any, operation string) {
	detached, err := templating.NewIncrementalDetachedValue(value)
	if err != nil {
		r.recordCapabilityViolation(fmt.Errorf("%s value is not JSON serializable: %w", operation, err))
		return
	}
	r.publishDetachedAfterPreflight(cell, key, rank, detached, operation)
}

func (r *incrementalRecorder) publishDetachedAfterPreflight(
	cell, key, rank string,
	value *templating.IncrementalDetachedValue,
	operation string,
) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.err != nil {
		return
	}
	if cell == "" || key == "" {
		r.err = fmt.Errorf("%s requires a non-empty cell and key", operation)
		return
	}
	if operation == "shared.PublishRanked" && rank == "" {
		r.err = errors.New("shared.PublishRanked requires a non-empty rank")
		return
	}
	encoded, snapshot, err := r.publicationGeneration.capture(
		r.publicationGroup,
		r.publicationOwner,
		len(r.published),
		cell,
		key,
		rank,
		value,
	)
	if err != nil {
		r.err = fmt.Errorf("%s value is not JSON serializable: %w", operation, err)
		return
	}
	r.published = append(r.published, incrementalPublishedValue{
		Cell: cell, Key: key, Rank: rank, Value: encoded, snapshot: snapshot,
	})
}

func (r *incrementalRecorder) RecordStatusPatch(
	namespace, name, apiVersion, kind, uid, resourceVersion string,
	variants map[string]map[string]any,
	sourceTemplate string,
	sourceLine int,
) error {
	if r == nil {
		return errors.New("incremental status patch recorder is nil")
	}
	release, err := beginIncrementalCapability(r.lease, "statusPatch")
	if err != nil {
		r.recordCapabilityViolation(err)
		return err
	}
	defer release()
	return r.recordStatusPatch(
		namespace, name, apiVersion, kind, uid, resourceVersion, variants, sourceTemplate, sourceLine,
	)
}

func (r *incrementalRecorder) recordStatusPatch(
	namespace, name, apiVersion, kind, uid, resourceVersion string,
	variants map[string]map[string]any,
	sourceTemplate string,
	sourceLine int,
) error {
	collector := templating.NewStatusPatchCollector()
	if err := collector.RegisterWithLineage(
		namespace, name, apiVersion, kind, uid, resourceVersion, variants,
	); err != nil {
		return err
	}
	encoded, err := encodeIncrementalStatusPatchVariants(variants)
	if err != nil {
		return fmt.Errorf("encoding statusPatch variants: %w", err)
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.err != nil {
		return r.err
	}
	r.patches = append(r.patches, incrementalStatusPatchCall{
		Namespace: namespace, Name: name, APIVersion: apiVersion, Kind: kind,
		UID: uid, ResourceVersion: resourceVersion,
		Variants: slices.Clone(encoded), SourceTemplate: sourceTemplate, SourceLine: sourceLine,
	})
	return nil
}

func (r *incrementalPreflightRecorder) Unique(cell, key, value string) {
	r.recorder.recordUnique(cell, key, value)
}

func (r *incrementalPreflightRecorder) Publish(cell, key string, value any) {
	r.recorder.publishAfterPreflight(cell, key, "", value, "shared.Publish")
}

func (r *incrementalPreflightRecorder) PublishDetached(
	cell, key string,
	value *templating.IncrementalDetachedValue,
) {
	r.recorder.publishDetachedAfterPreflight(cell, key, "", value, "shared.Publish")
}

func (r *incrementalPreflightRecorder) PublishRanked(cell, key, rank string, value any) {
	r.recorder.publishAfterPreflight(cell, key, rank, value, "shared.PublishRanked")
}

func (r *incrementalPreflightRecorder) PublishRankedDetached(
	cell, key, rank string,
	value *templating.IncrementalDetachedValue,
) {
	r.recorder.publishDetachedAfterPreflight(cell, key, rank, value, "shared.PublishRanked")
}

func (r *incrementalPreflightRecorder) RecordStatusPatch(
	namespace, name, apiVersion, kind, uid, resourceVersion string,
	variants map[string]map[string]any,
	sourceTemplate string,
	sourceLine int,
) error {
	return r.recorder.recordStatusPatch(
		namespace, name, apiVersion, kind, uid, resourceVersion, variants, sourceTemplate, sourceLine,
	)
}

func (r *incrementalRecorder) freezeBackendPlanLocked(text string) (
	calls []incrementalBackendPlanCall,
	output []incrementalBackendPlanPart,
	digest string,
	remainingText string,
	err error,
) {
	if r.plan == nil {
		return nil, nil, "", text, nil
	}
	calls, output, err = r.plan.freeze(text)
	if err != nil {
		return nil, nil, "", "", err
	}
	if len(calls) == 0 && len(output) == 0 {
		return calls, output, "", text, nil
	}
	digest, err = digestIncrementalBackendPlan(calls, output)
	if err != nil {
		return nil, nil, "", "", err
	}
	return calls, output, digest, "", nil
}

func (r *incrementalRecorder) result(text string) (incrementalComponentResult, error) {
	if err := r.lease.err(); err != nil {
		return incrementalComponentResult{}, err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.err != nil {
		return incrementalComponentResult{}, r.err
	}
	if text != "" && len(r.unique) != 0 {
		return incrementalComponentResult{}, errors.New("incremental component cannot mix text with shared.Unique contributions")
	}
	var events []templating.RenderedEvent
	if r.events != nil {
		events = r.events.Events()
	}
	var derivations []rendercontext.DerivedResource
	if r.deriver != nil {
		derivations = r.deriver.freeze()
	}
	backendPlan, backendPlanOutput, backendPlanDigest, text, err := r.freezeBackendPlanLocked(text)
	if err != nil {
		return incrementalComponentResult{}, err
	}
	if len(r.published) != 0 &&
		(text != "" || len(r.unique) != 0 || incrementalBackendPlanHasOrdinaryText(backendPlanOutput)) {
		return incrementalComponentResult{}, errors.New("incremental component cannot mix shared.Publish with text or shared.Unique contributions")
	}
	published := cloneIncrementalPublishedValues(r.published)
	publishedDigest, err := digestIncrementalPublishedValues(published)
	if err != nil {
		return incrementalComponentResult{}, err
	}
	patches := cloneIncrementalStatusPatchCalls(r.patches)
	statusPatchDigest, err := digestIncrementalStatusPatchCalls(patches)
	if err != nil {
		return incrementalComponentResult{}, err
	}
	result := incrementalComponentResult{
		Text:              text,
		Unique:            slices.Clone(r.unique),
		Published:         published,
		PublishedDigest:   publishedDigest,
		Derivations:       derivations,
		Events:            events,
		StatusPatches:     patches,
		StatusPatchDigest: statusPatchDigest,
		BackendPlan:       backendPlan,
		BackendPlanOutput: backendPlanOutput,
		BackendPlanDigest: backendPlanDigest,
	}
	if len(published) != 0 && r.publicationGeneration != nil {
		result.publicationGeneration = r.publicationGeneration
		result.publicationGroup = r.publicationGroup
		result.publicationOwner = r.publicationOwner
	}
	return result, nil
}

func (r *incrementalRecorder) authenticatedResult(
	key incremental.QueryKey,
	component *incrementalComponent,
	source, namespace, name, text string,
) (string, *authenticatedFreshComponentResult, error) {
	result, effects, err := r.validatedResult(component, source, namespace, name, text)
	if err != nil {
		return "", nil, err
	}
	return newAuthenticatedFreshComponentResultFromRecorder(key, &result, &effects)
}

func (r *incrementalRecorder) validatedResult(
	component *incrementalComponent,
	source, namespace, name, text string,
) (incrementalComponentResult, authenticatedFreshComponentEffects, error) {
	result, err := r.result(text)
	if err != nil {
		return incrementalComponentResult{}, authenticatedFreshComponentEffects{}, err
	}
	effects, err := newAuthenticatedFreshComponentEffects(component, source, namespace, name)
	if err != nil {
		return incrementalComponentResult{}, authenticatedFreshComponentEffects{}, err
	}
	if err := validateRecorderGeneratedIncrementalEffects(component, source, namespace, name, &result); err != nil {
		return incrementalComponentResult{}, authenticatedFreshComponentEffects{}, err
	}
	return result, effects, nil
}

func validateRecorderGeneratedIncrementalEffects(
	component *incrementalComponent,
	source, namespace, name string,
	result *incrementalComponentResult,
) error {
	if component == nil || result == nil {
		return errors.New("incremental recorder result has invalid provenance")
	}
	if err := validateDeclaredPublishValueEffect(component, result); err != nil {
		return err
	}
	if err := validateDeclaredBackendPlanEffect(component, result); err != nil {
		return err
	}
	for index := range result.BackendPlan {
		if err := validateIncrementalBackendPlanCondition(
			result, index, result.BackendPlan[index].WhenAny,
		); err != nil {
			return err
		}
	}
	if len(result.Derivations) > 1 || len(result.Derivations) > 0 && !component.deriveResource {
		return fmt.Errorf("incremental component %q returned invalid derivations", component.name)
	}
	if len(result.Derivations) == 1 {
		identity := rendercontext.DerivedResourceIdentity{Resource: source, Namespace: namespace, Name: name}
		if result.Derivations[0].Identity != identity {
			return fmt.Errorf("incremental component %q returned a derivation for another source", component.name)
		}
	}
	if len(result.Events) > 0 && !component.recordEvent {
		return fmt.Errorf("incremental component %q returned an undeclared recordEvent effect", component.name)
	}
	if (len(result.StatusPatches) > 0 || result.StatusPatchDigest != "") && !component.statusPatch {
		return fmt.Errorf("incremental component %q returned an undeclared statusPatch effect", component.name)
	}
	return nil
}

func (r *incrementalRecorder) recordCapabilityViolation(err error) {
	if r == nil || err == nil {
		return
	}
	r.mu.Lock()
	if r.err == nil {
		r.err = err
	}
	r.mu.Unlock()
}

func incrementalBackendPlanHasOrdinaryText(parts []incrementalBackendPlanPart) bool {
	for index := range parts {
		if parts[index].BackendCall == nil && strings.TrimSpace(parts[index].Text) != "" {
			return true
		}
	}
	return false
}

type incrementalInstanceResult struct {
	component string
	source    string
	namespace string
	name      string
	result    incrementalComponentResult
}

func assembleIncrementalGroup(instances []incrementalInstanceResult) (map[string]string, error) {
	ordered := make([]*incrementalInstanceResult, len(instances))
	for index := range instances {
		ordered[index] = &instances[index]
	}
	slices.SortFunc(ordered, compareIncrementalInstances)

	type contributionKey struct {
		cell string
		key  string
	}
	winners := make(map[contributionKey]struct{})
	outputs := make(map[string]*strings.Builder)
	for _, instance := range ordered {
		if err := validateIncrementalInstanceResult(&instance.result); err != nil {
			return nil, fmt.Errorf("incremental component %q source %q %s/%s: %w",
				instance.component, instance.source, instance.namespace, instance.name, err)
		}
		builder := outputs[instance.component]
		if builder == nil {
			builder = &strings.Builder{}
			outputs[instance.component] = builder
		}
		builder.WriteString(instance.result.Text)
		for _, contribution := range instance.result.Unique {
			winnerKey := contributionKey{cell: contribution.Cell, key: contribution.Key}
			if _, exists := winners[winnerKey]; exists {
				continue
			}
			winners[winnerKey] = struct{}{}
			builder.WriteString(contribution.Value)
		}
	}

	result := make(map[string]string, len(outputs))
	for component, builder := range outputs {
		result[component] = builder.String()
	}
	return result, nil
}

func validateIncrementalInstanceResult(result *incrementalComponentResult) error {
	if result == nil {
		return errors.New("incremental component result is nil")
	}
	if result.Text != "" && len(result.Unique) != 0 {
		return errors.New("incremental component cannot mix text with shared.Unique contributions")
	}
	for _, contribution := range result.Unique {
		if contribution.Cell == "" || contribution.Key == "" {
			return errors.New("shared.Unique requires a non-empty cell and key")
		}
	}
	if len(result.Published) != 0 && (result.Text != "" || len(result.Unique) != 0) {
		return errors.New("incremental component cannot mix shared.Publish with text or shared.Unique contributions")
	}
	if len(result.Published) == 0 && (result.publicationGeneration != nil ||
		result.publicationGroup != "" || result.publicationOwner != (incrementalGroupInstanceID{})) {
		return errors.New("incremental publication snapshot has no published values")
	}
	if err := validateIncrementalPublishedValues(result); err != nil {
		return err
	}
	if err := validateIncrementalStatusPatchDigest(result); err != nil {
		return err
	}
	return validateIncrementalBackendPlanResult(result)
}

func validateIncrementalPublishedValues(result *incrementalComponentResult) error {
	for index := range result.Published {
		publication := &result.Published[index]
		if publication.Cell == "" || publication.Key == "" {
			return errors.New("shared.Publish requires a non-empty cell and key")
		}
		certified, err := authenticateIncrementalPublicationResultSnapshot(result, publication, index)
		if err != nil {
			return fmt.Errorf("shared.Publish value %d has an invalid snapshot: %w", index, err)
		}
		if !certified {
			decoded, err := decodeResourceValue(publication.Value)
			if err != nil {
				return fmt.Errorf("shared.Publish value %d is invalid: %w", index, err)
			}
			canonical, err := encodeResourceValue(decoded)
			if err != nil || !bytes.Equal(canonical, publication.Value) {
				return fmt.Errorf("shared.Publish value %d is not canonical JSON", index)
			}
		}
	}
	digest, err := digestIncrementalPublishedValues(result.Published)
	if err != nil {
		return err
	}
	if result.PublishedDigest != digest {
		return errors.New("publishValue effect has an invalid digest")
	}
	return nil
}

func validateIncrementalStatusPatchDigest(result *incrementalComponentResult) error {
	if err := validateIncrementalStatusPatchCalls(result.StatusPatches); err != nil {
		return err
	}
	statusPatchDigest, err := digestIncrementalStatusPatchCalls(result.StatusPatches)
	if err != nil {
		return err
	}
	if result.StatusPatchDigest != statusPatchDigest {
		return errors.New("statusPatch effect has an invalid digest")
	}
	return nil
}

func validateIncrementalStatusPatchCalls(calls []incrementalStatusPatchCall) error {
	for index := range calls {
		call := &calls[index]
		patch, err := decodeIncrementalStatusPatchCall(call)
		if err != nil {
			return fmt.Errorf("statusPatch call %d is invalid: %w", index, err)
		}
		canonical, err := encodeIncrementalStatusPatchVariants(patch.Variants)
		if err != nil || !bytes.Equal(canonical, call.Variants) {
			return fmt.Errorf("statusPatch call %d variants are not canonical JSON", index)
		}
	}
	return nil
}

func encodeIncrementalStatusPatchVariants(variants map[string]map[string]any) ([]byte, error) {
	serializable := make(map[string]any, len(variants))
	for phase, value := range variants {
		serializable[phase] = value
	}
	return encodeResourceValue(serializable)
}

func decodeIncrementalStatusPatchCall(call *incrementalStatusPatchCall) (templating.StatusPatch, error) {
	if call == nil {
		return templating.StatusPatch{}, errors.New("call is nil")
	}
	decoded, err := decodeResourceValue(call.Variants)
	if err != nil {
		return templating.StatusPatch{}, err
	}
	values, ok := decoded.(map[string]any)
	if !ok {
		return templating.StatusPatch{}, fmt.Errorf("variants must be an object, got %T", decoded)
	}
	variants := make(map[string]map[string]any, len(values))
	for phase, value := range values {
		status, valid := value.(map[string]any)
		if !valid {
			return templating.StatusPatch{}, fmt.Errorf("variant %q must be an object, got %T", phase, value)
		}
		variants[phase] = status
	}
	collector := templating.NewStatusPatchCollector()
	if err := collector.RegisterWithLineage(
		call.Namespace, call.Name, call.APIVersion, call.Kind, call.UID, call.ResourceVersion, variants,
	); err != nil {
		return templating.StatusPatch{}, err
	}
	collector.SetSource(call.Namespace, call.Name, call.APIVersion, call.Kind, call.SourceTemplate, call.SourceLine)
	patches, err := collector.Patches()
	if err != nil {
		return templating.StatusPatch{}, err
	}
	if len(patches) != 1 {
		return templating.StatusPatch{}, errors.New("call did not produce exactly one patch")
	}
	return patches[0], nil
}

func digestIncrementalStatusPatchCalls(calls []incrementalStatusPatchCall) (string, error) {
	if len(calls) == 0 {
		return "", nil
	}
	encoded, err := json.Marshal(calls)
	if err != nil {
		return "", fmt.Errorf("encoding statusPatch effect: %w", err)
	}
	return renderplan.Digest(encoded), nil
}

func cloneIncrementalStatusPatchCalls(source []incrementalStatusPatchCall) []incrementalStatusPatchCall {
	if len(source) == 0 {
		return nil
	}
	cloned := slices.Clone(source)
	for index := range cloned {
		cloned[index].Variants = slices.Clone(source[index].Variants)
	}
	return cloned
}

func digestIncrementalPublishedValues(values []incrementalPublishedValue) (string, error) {
	if len(values) == 0 {
		return "", nil
	}
	encoded, err := json.Marshal(values)
	if err != nil {
		return "", fmt.Errorf("encoding publishValue effect: %w", err)
	}
	return renderplan.Digest(encoded), nil
}

func cloneIncrementalPublishedValues(source []incrementalPublishedValue) []incrementalPublishedValue {
	if len(source) == 0 {
		return nil
	}
	cloned := slices.Clone(source)
	for index := range cloned {
		cloned[index].Value = slices.Clone(source[index].Value)
	}
	return cloned
}

func compareIncrementalInstances(left, right *incrementalInstanceResult) int {
	if left.component < right.component {
		return -1
	}
	if left.component > right.component {
		return 1
	}
	if left.source != right.source {
		return strings.Compare(left.source, right.source)
	}
	if left.namespace != right.namespace {
		return strings.Compare(left.namespace, right.namespace)
	}
	return strings.Compare(left.name, right.name)
}

func incrementalInstanceError(instance *incrementalInstanceResult, err error) error {
	return fmt.Errorf("incremental component %q source %q %s/%s: %w",
		instance.component, instance.source, instance.namespace, instance.name, err)
}
