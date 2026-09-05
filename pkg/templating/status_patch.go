// Copyright 2025 Philipp Hossner
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

package templating

import (
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"fmt"
	"sync"

	projection "gitlab.com/haproxy-haptic/haptic/pkg/templating/internal/statuspatchprojection"
)

const (
	statusPhaseRendered     = "rendered"
	statusPhaseDeployed     = "deployed"
	statusPhaseRenderFailed = "renderFailed"
	statusPhaseDeployFailed = "deployFailed"
)

// StatusPatch represents a status update to apply to a Kubernetes resource.
// Templates register patches via the statusPatch() function during rendering.
// Each patch targets a specific resource and contains outcome-keyed variants
// for different pipeline lifecycle phases.
type StatusPatch struct {
	// Namespace of the target Kubernetes resource.
	Namespace string

	// Name of the target Kubernetes resource.
	Name string

	// APIVersion of the target resource (e.g., "networking.k8s.io/v1").
	APIVersion string

	// Kind of the target resource (e.g., "Service", "ConfigMap", or any watched CRD's Kind).
	Kind string

	// UID identifies the exact source resource incarnation. Empty for offline and legacy inputs.
	UID string

	// ResourceVersion identifies the exact source resource revision. Empty for offline and legacy inputs.
	ResourceVersion string

	// Variants maps pipeline phase names to desired status payloads.
	// Keys are phase names: "rendered", "deployed", "renderFailed", "deployFailed".
	// Values are the desired .status content for that phase.
	Variants map[string]map[string]any

	// SourceTemplate is the template path that called statusPatch() to register
	// this patch (best-effort, from native.Env.CallPath). Empty when unknown.
	// SourceLine is the 1-based line within that template of the statusPatch()
	// call (from native.Env.CallLine), 0 when unknown. Provenance metadata only —
	// the controller never reads them; they let the playground jump from a
	// rendered status block back to the exact statusPatch() call. Resource-agnostic:
	// a template name and line, not a resource path.
	SourceTemplate string
	SourceLine     int
}

// statusPatchKey uniquely identifies a target resource for patch merging.
func statusPatchKey(namespace, name, apiVersion, kind string) string {
	return namespace + "/" + name + "/" + apiVersion + "/" + kind
}

type statusPatchIdentity struct {
	namespace  string
	name       string
	apiVersion string
	kind       string
}

func newStatusPatchIdentity(namespace, name, apiVersion, kind string) statusPatchIdentity {
	return statusPatchIdentity{namespace: namespace, name: name, apiVersion: apiVersion, kind: kind}
}

// StatusPatchCollector collects status patches registered by templates during rendering.
// It is thread-safe for concurrent writes from parallel template goroutines.
// Created per render cycle (same lifecycle as FileRegistry).
type StatusPatchCollector struct {
	mu             sync.Mutex
	patches        map[statusPatchIdentity]*collectedStatusPatch
	order          []statusPatchIdentity
	projections    []*StatusPatchProjectionReplay
	projectionPlan *StatusPatchProjectionPlanReplay
	planBinding    *statusPatchProjectionPlanBinding
	frozen         bool
	snapshot       *StatusPatchSnapshot
}

type collectedStatusPatch struct {
	Namespace       string
	Name            string
	APIVersion      string
	Kind            string
	UID             string
	ResourceVersion string
	Variants        map[string]collectedStatusPatchVariant
	SourceTemplate  string
	SourceLine      int
	owner           *StatusPatchCollector
	sourceDigest    [sha256.Size]byte
	lineageDigest   [sha256.Size]byte
}

type collectedStatusPatchVariant struct {
	detached     map[string]any
	hasDetached  bool
	projected    projection.PhaseView
	hasProjected bool
	projection   *StatusPatchProjection
	owner        *StatusPatchCollector
	sourcePatch  projection.PatchView
}

// NewStatusPatchCollector creates a new thread-safe collector.
func NewStatusPatchCollector() *StatusPatchCollector {
	return &StatusPatchCollector{
		patches: make(map[statusPatchIdentity]*collectedStatusPatch),
	}
}

// Register registers a status patch for a Kubernetes resource.
// If a patch for the same resource already exists, the variant maps are merged
// (later calls override earlier ones for the same variant key).
//
// The variants parameter maps phase names to status payloads:
//   - "rendered": applied after successful render
//   - "deployed": applied after successful deployment
//   - "renderFailed": applied when later render phases fail
//   - "deployFailed": applied when deployment fails
func (c *StatusPatchCollector) Register(namespace, name, apiVersion, kind string, variants map[string]map[string]any) error {
	return c.RegisterWithLineage(namespace, name, apiVersion, kind, "", "", variants)
}

// RegisterWithLineage registers a patch for one exact source resource revision.
func (c *StatusPatchCollector) RegisterWithLineage(
	namespace, name, apiVersion, kind, uid, resourceVersion string,
	variants map[string]map[string]any,
) error {
	// Namespace is intentionally optional: cluster-scoped resources
	// (GatewayClass, ClusterRole, etc.) have no namespace. The applier
	// passes Namespace("") to the dynamic client, which the client-go
	// dynamic interface treats as cluster-scoped automatically.
	if name == "" || apiVersion == "" || kind == "" {
		return errors.New("statusPatch: name, apiVersion, and kind are required")
	}

	if len(variants) == 0 {
		return errors.New("statusPatch: at least one variant is required")
	}

	// Validate phase keys
	for phase := range variants {
		switch phase {
		case statusPhaseRendered, statusPhaseDeployed, statusPhaseRenderFailed, statusPhaseDeployFailed:
			// valid
		default:
			return fmt.Errorf("statusPatch: invalid phase %q, must be one of: rendered, deployed, renderFailed, deployFailed", phase)
		}
	}
	detachedVariants, err := cloneStatusPatchVariants(variants)
	if err != nil {
		return err
	}

	key := newStatusPatchIdentity(namespace, name, apiVersion, kind)

	c.mu.Lock()
	defer c.mu.Unlock()
	if c.frozen {
		return errors.New("statusPatch: collector is sealed")
	}
	if c.projectionPlan != nil {
		return errors.New("statusPatch: collector already has a projection plan")
	}

	existing, exists := c.patches[key]
	if !exists {
		existing = &collectedStatusPatch{
			Namespace: namespace, Name: name, APIVersion: apiVersion, Kind: kind,
			UID: uid, ResourceVersion: resourceVersion,
			Variants: make(map[string]collectedStatusPatchVariant, len(detachedVariants)), owner: c,
		}
		existing.sourceDigest = statusPatchSourceDigest("", 0)
		existing.lineageDigest = statusPatchLineageDigest(uid, resourceVersion)
		c.patches[key] = existing
		c.order = append(c.order, key)
	} else {
		if existing.owner != c || existing.lineageDigest != statusPatchLineageDigest(existing.UID, existing.ResourceVersion) {
			return errors.New("statusPatch: existing patch has invalid provenance")
		}
		if existing.UID != uid || existing.ResourceVersion != resourceVersion {
			return fmt.Errorf("statusPatch: %s/%s has conflicting source lineage", namespace, name)
		}
	}
	for phase, value := range detachedVariants {
		existing.Variants[phase] = collectedStatusPatchVariant{
			detached: value, hasDetached: true, owner: c,
		}
	}

	return nil
}

// SetSource records the template and line that registered the patch for the
// given target, if a patch exists and none is set yet. Best-effort provenance
// used by the playground; a no-op if the target was never registered. Kept
// separate from Register so the render-path signature and all its callers stay
// unchanged.
func (c *StatusPatchCollector) SetSource(namespace, name, apiVersion, kind, sourceTemplate string, sourceLine int) {
	if sourceTemplate == "" {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.frozen {
		return
	}
	if c.projectionPlan != nil {
		return
	}
	if p, ok := c.patches[newStatusPatchIdentity(namespace, name, apiVersion, kind)]; ok && p.SourceTemplate == "" {
		p.SourceTemplate = sourceTemplate
		p.SourceLine = sourceLine
		p.sourceDigest = statusPatchSourceDigest(sourceTemplate, sourceLine)
	}
}

// Patches returns all collected status patches as a detached snapshot.
func (c *StatusPatchCollector) Patches() ([]StatusPatch, error) {
	c.mu.Lock()
	if c.snapshot != nil {
		snapshot := c.snapshot
		c.mu.Unlock()
		return snapshot.Patches()
	}
	patches, err := c.materializeLocked("")
	c.mu.Unlock()
	return patches, err
}

func (c *StatusPatchCollector) materializeLocked(phase string) ([]StatusPatch, error) {
	if err := c.validateMaterializeProvenanceLocked(); err != nil {
		return nil, err
	}
	result := make([]StatusPatch, 0, len(c.patches))
	for index, key := range c.order {
		patch := c.patches[key]
		if patch == nil || patch.Namespace != key.namespace || patch.Name != key.name ||
			patch.APIVersion != key.apiVersion || patch.Kind != key.kind || patch.owner != c ||
			patch.sourceDigest != statusPatchSourceDigest(patch.SourceTemplate, patch.SourceLine) ||
			patch.lineageDigest != statusPatchLineageDigest(patch.UID, patch.ResourceVersion) {
			return nil, fmt.Errorf("statusPatch: patch %d has invalid provenance", index)
		}
		variants, err := c.materializePatchVariantsLocked(key, patch, phase)
		if err != nil {
			return nil, err
		}
		if phase != "" && len(variants) == 0 {
			continue
		}
		result = append(result, StatusPatch{
			Namespace: patch.Namespace, Name: patch.Name, APIVersion: patch.APIVersion, Kind: patch.Kind,
			UID: patch.UID, ResourceVersion: patch.ResourceVersion,
			Variants: variants, SourceTemplate: patch.SourceTemplate, SourceLine: patch.SourceLine,
		})
	}
	return c.applyProjectionPlanLocked(phase, result)
}

func (c *StatusPatchCollector) validateMaterializeProvenanceLocked() error {
	if len(c.order) != len(c.patches) {
		for _, patch := range c.patches {
			return fmt.Errorf("statusPatch: snapshotting %s/%s: collector ordering has invalid provenance",
				patch.Namespace, patch.Name)
		}
		return errors.New("statusPatch: collector ordering has invalid provenance")
	}
	for index, replay := range c.projections {
		if replay == nil || replay.seal != replay || replay.projection == nil ||
			replay.root == nil || replay.root != replay.projection.root {
			return fmt.Errorf("statusPatch: cached projection %d has invalid provenance", index)
		}
		if err := replay.projection.auditIntegrity(); err != nil {
			return fmt.Errorf("statusPatch: cached projection %d: %w", index, err)
		}
	}
	if c.projectionPlan != nil && !c.validProjectionPlanBinding() {
		return errors.New("statusPatch: cached projection plan has invalid provenance")
	}
	return nil
}

func (c *StatusPatchCollector) materializePatchVariantsLocked(
	key statusPatchIdentity,
	patch *collectedStatusPatch,
	phase string,
) (map[string]map[string]any, error) {
	variantCapacity := len(patch.Variants)
	if phase != "" {
		variantCapacity = 1
	}
	variants := make(map[string]map[string]any, variantCapacity)
	for variantPhase, variant := range patch.Variants {
		if phase != "" && variantPhase != phase {
			continue
		}
		value, err := c.materializeVariantLocked(key, patch, variantPhase, &variant)
		if err != nil {
			return nil, fmt.Errorf("statusPatch: snapshotting %s/%s phase %q: %w",
				patch.Namespace, patch.Name, variantPhase, err)
		}
		variants[variantPhase] = value
	}
	return variants, nil
}

func (c *StatusPatchCollector) materializeVariantLocked(
	key statusPatchIdentity,
	patch *collectedStatusPatch,
	variantPhase string,
	variant *collectedStatusPatchVariant,
) (map[string]any, error) {
	if variant.owner != c {
		return nil, errors.New("variant has invalid provenance")
	}
	switch {
	case variant.hasProjected:
		if variant.projection == nil {
			return nil, errors.New("projected variant has no authority")
		}
		metadata, metadataErr := variant.sourcePatch.Metadata()
		phaseName, phaseErr := variant.projected.Name()
		if metadataErr != nil || phaseErr != nil ||
			!variant.projected.BelongsTo(variant.sourcePatch) ||
			phaseName != variantPhase ||
			metadata.Namespace != key.namespace ||
			metadata.Name != key.name ||
			metadata.APIVersion != key.apiVersion ||
			metadata.Kind != key.kind ||
			metadata.UID != patch.UID ||
			metadata.ResourceVersion != patch.ResourceVersion {
			return nil, errors.New("projected variant has invalid provenance")
		}
		return variant.projected.Materialize()
	case variant.hasDetached:
		return cloneStatusPatchVariant(variant.detached)
	default:
		return nil, errors.New("variant is unavailable")
	}
}

func (c *StatusPatchCollector) applyProjectionPlanLocked(
	phase string,
	result []StatusPatch,
) ([]StatusPatch, error) {
	if c.projectionPlan == nil {
		return result, nil
	}
	resultByKey := make(map[statusPatchIdentity]int, len(result))
	for index := range result {
		patch := &result[index]
		resultByKey[newStatusPatchIdentity(patch.Namespace, patch.Name, patch.APIVersion, patch.Kind)] = index
	}
	if err := c.projectionPlan.visitPatches(func(
		_ *StatusPatchProjection,
		projected projection.PatchView,
	) error {
		var mergeErr error
		result, mergeErr = mergeProjectedStatusPatch(result, resultByKey, projected, phase)
		return mergeErr
	}); err != nil {
		return nil, err
	}
	return result, nil
}

func mergeProjectedStatusPatch(
	result []StatusPatch,
	resultByKey map[statusPatchIdentity]int,
	projected projection.PatchView,
	phase string,
) ([]StatusPatch, error) {
	metadata, err := projected.Metadata()
	if err != nil {
		return result, err
	}
	key := newStatusPatchIdentity(metadata.Namespace, metadata.Name, metadata.APIVersion, metadata.Kind)
	index, exists := resultByKey[key]
	if exists {
		patch := &result[index]
		if patch.UID != metadata.UID || patch.ResourceVersion != metadata.ResourceVersion {
			return result, fmt.Errorf("statusPatch: %s/%s has conflicting source lineage", metadata.Namespace, metadata.Name)
		}
	}
	materialized, err := materializeProjectedStatusPhases(projected, &metadata, phase)
	if err != nil {
		return result, err
	}
	if len(materialized) == 0 {
		return result, nil
	}
	if !exists {
		index = len(result)
		resultByKey[key] = index
		result = append(result, StatusPatch{
			Namespace: metadata.Namespace, Name: metadata.Name,
			APIVersion: metadata.APIVersion, Kind: metadata.Kind,
			UID: metadata.UID, ResourceVersion: metadata.ResourceVersion,
			Variants: make(map[string]map[string]any, len(materialized)),
		})
	}
	patch := &result[index]
	if patch.SourceTemplate == "" && metadata.SourceTemplate != "" {
		patch.SourceTemplate = metadata.SourceTemplate
		patch.SourceLine = metadata.SourceLine
	}
	for phaseName, value := range materialized {
		patch.Variants[phaseName] = value
	}
	return result, nil
}

func materializeProjectedStatusPhases(
	projected projection.PatchView,
	metadata *projection.Metadata,
	phase string,
) (map[string]map[string]any, error) {
	materialized := make(map[string]map[string]any)
	if err := projected.VisitPhases(func(projectedPhase projection.PhaseView) error {
		phaseName, phaseErr := projectedPhase.Name()
		if phaseErr != nil {
			return phaseErr
		}
		if phase != "" && phaseName != phase {
			return nil
		}
		value, materializeErr := projectedPhase.Materialize()
		if materializeErr != nil {
			return fmt.Errorf("statusPatch: snapshotting %s/%s phase %q: %w",
				metadata.Namespace, metadata.Name, phaseName, materializeErr)
		}
		materialized[phaseName] = value
		return nil
	}); err != nil {
		return nil, err
	}
	return materialized, nil
}

func statusPatchSourceDigest(sourceTemplate string, sourceLine int) [sha256.Size]byte {
	hasher := sha256.New()
	_, _ = hasher.Write([]byte(sourceTemplate))
	var line [binary.MaxVarintLen64]byte
	length := binary.PutVarint(line[:], int64(sourceLine))
	_, _ = hasher.Write(line[:length])
	var digest [sha256.Size]byte
	hasher.Sum(digest[:0])
	return digest
}

func statusPatchLineageDigest(uid, resourceVersion string) [sha256.Size]byte {
	hasher := sha256.New()
	_, _ = hasher.Write([]byte(uid))
	_, _ = hasher.Write([]byte{0})
	_, _ = hasher.Write([]byte(resourceVersion))
	var digest [sha256.Size]byte
	hasher.Sum(digest[:0])
	return digest
}

func cloneStatusPatchVariant(variant map[string]any) (map[string]any, error) {
	detached, err := cloneIncrementalSerialization(variant)
	if err != nil {
		return nil, fmt.Errorf("variant cannot be detached: %w", err)
	}
	detachedVariant, ok := detached.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("variant has type %T", detached)
	}
	return detachedVariant, nil
}

func cloneStatusPatchVariants(variants map[string]map[string]any) (map[string]map[string]any, error) {
	detached, err := cloneIncrementalSerialization(variants)
	if err != nil {
		return nil, fmt.Errorf("variants cannot be detached: %w", err)
	}
	detachedVariants, ok := detached.(map[string]map[string]any)
	if !ok {
		return nil, fmt.Errorf("variants have type %T", detached)
	}
	return detachedVariants, nil
}
