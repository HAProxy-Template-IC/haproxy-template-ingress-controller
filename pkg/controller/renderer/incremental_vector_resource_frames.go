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
	"reflect"

	"gitlab.com/haproxy-haptic/haptic/pkg/controller/rendercontext"
	"gitlab.com/haproxy-haptic/haptic/pkg/incremental"
	"gitlab.com/haproxy-haptic/haptic/pkg/stores"
	"gitlab.com/haproxy-haptic/haptic/pkg/templating"
)

type incrementalDirectResourceProjection struct {
	seal            *incrementalDirectResourceProjection
	proof           *incrementalDirectResourceProjectionProof
	authority       *incrementalResourceMaterializationAuthority
	materialization *incrementalResourceMaterialization
	resourceType    string
	owner           incrementalDerivedOwnerResolution
	ownerObserved   bool
}

type incrementalDirectResourceProjectionProof struct {
	seal            *incrementalDirectResourceProjectionProof
	projection      *incrementalDirectResourceProjection
	authority       *incrementalResourceMaterializationAuthority
	materialization *incrementalResourceMaterialization
	resourceType    string
	owner           incrementalDerivedOwnerResolution
	ownerObserved   bool
}

func (v *incrementalVectorResourceView) MaterializeDirectBoundResource(
	ctx context.Context,
	invocation rendercontext.DirectBoundStoreInvocation,
	request *rendercontext.DirectBoundResourceMaterializationRequest,
	_ stores.Store,
	keys []string,
) (reflect.Value, error) {
	declaration, err := request.Describe()
	if err != nil {
		return reflect.Value{}, err
	}
	authenticated, err := v.beginDirectBoundStoreRead(ctx, invocation)
	if err != nil {
		return reflect.Value{}, err
	}
	defer authenticated.finishRead()
	spec, err := authenticated.execution.session.resourceMaterializations.directBoundResourceSpec(
		declaration,
		keys,
	)
	if err != nil {
		return reflect.Value{}, authenticated.execution.recordViolation(err)
	}
	items, materialization, err := authenticated.execution.session.decodeMaterializedResourceInput(
		authenticated.item.prepared.reader,
		spec,
	)
	if err != nil {
		return reflect.Value{}, err
	}
	projection, cacheable, err := authenticated.directResourceProjection(
		declaration.ResourceType,
		materialization,
	)
	if err != nil {
		return reflect.Value{}, err
	}
	if cacheable {
		return request.Materialize(authenticated.item.ctx, projection, keys)
	}
	if materialization != nil {
		items, err = materialization.rawItems()
		if err != nil {
			return reflect.Value{}, err
		}
	}
	items, err = authenticated.projectResourceItems(declaration.ResourceType, items)
	if err != nil {
		return reflect.Value{}, err
	}
	return request.MaterializeUncached(authenticated.item.ctx, items, keys)
}

func directBoundResourceInputSpec(
	declaration rendercontext.DirectBoundResourceMaterialization,
	keys []string,
) (resourceInputSpec, error) {
	if declaration.ResourceType == "" {
		return resourceInputSpec{}, errors.New("direct resource declaration has no resource type")
	}
	switch declaration.Operation {
	case rendercontext.DirectBoundResourceList:
		if len(keys) != 0 {
			return resourceInputSpec{}, errors.New("direct resource List declaration has lookup keys")
		}
		return sealResourceInputSpec(&resourceInputSpec{
			resourceType: declaration.ResourceType,
			scope:        resourceInputList,
		}), nil
	case rendercontext.DirectBoundResourceFetch, rendercontext.DirectBoundResourceGetSingle:
		return sealResourceInputSpec(&resourceInputSpec{
			resourceType: declaration.ResourceType,
			scope:        resourceInputGet,
			keys:         keys,
		}), nil
	default:
		return resourceInputSpec{}, errors.New("direct resource declaration has an invalid operation")
	}
}

func (i authenticatedIncrementalVectorDirectInvocation) directResourceProjection(
	resourceType string,
	materialization *incrementalResourceMaterialization,
) (*incrementalDirectResourceProjection, bool, error) {
	if materialization == nil || i.item.derived != nil {
		return nil, false, nil
	}
	if i.item.derivedResolver == nil {
		return nil, false, errors.New(
			"incremental component vector has no derived-resource resolver",
		)
	}
	if materialization.itemCount == 0 {
		projection, err := materialization.directProjection(
			i.execution.session,
			resourceType,
			&incrementalDerivedOwnerResolution{},
			false,
		)
		return projection, true, err
	}
	owner, err := i.item.derivedResolver.resolveOwnerForProjection(resourceType)
	if err != nil {
		return nil, false, err
	}
	if owner.found {
		return nil, false, nil
	}
	projection, err := materialization.directProjection(
		i.execution.session,
		resourceType,
		&owner,
		true,
	)
	return projection, true, err
}

func (i authenticatedIncrementalVectorDirectInvocation) projectResourceItems(
	resourceType string,
	items []any,
) ([]any, error) {
	if i.item.derived != nil {
		return i.item.derived.Project(resourceType, items)
	}
	if i.item.derivedResolver == nil {
		return nil, errors.New("incremental component vector has no derived-resource resolver")
	}
	return i.item.derivedResolver.project(resourceType, items)
}

func (m *incrementalResourceMaterialization) directProjection(
	session *incrementalRenderSession,
	resourceType string,
	owner *incrementalDerivedOwnerResolution,
	ownerObserved bool,
) (*incrementalDirectResourceProjection, error) {
	if err := validateIncrementalDirectResourceProjectionCandidate(
		m, session, resourceType, owner, ownerObserved,
	); err != nil {
		return nil, err
	}
	if existing := m.projection.Load(); existing != nil {
		if err := existing.authenticateExpected(
			session, m, resourceType, owner, ownerObserved,
		); err != nil {
			return nil, err
		}
		return existing, nil
	}
	candidate := &incrementalDirectResourceProjection{
		authority: m.authority, materialization: m, resourceType: resourceType,
		owner: *owner, ownerObserved: ownerObserved,
	}
	candidate.seal = candidate
	candidate.proof = &incrementalDirectResourceProjectionProof{
		projection: candidate,
		authority:  m.authority, materialization: m, resourceType: resourceType,
		owner: *owner, ownerObserved: ownerObserved,
	}
	candidate.proof.seal = candidate.proof
	if !m.projection.CompareAndSwap(nil, candidate) {
		existing := m.projection.Load()
		if err := existing.authenticateExpected(
			session, m, resourceType, owner, ownerObserved,
		); err != nil {
			return nil, err
		}
		return existing, nil
	}
	err := candidate.authenticateExpected(
		session, m, resourceType, owner, ownerObserved,
	)
	return candidate, err
}

func validateIncrementalDirectResourceProjectionCandidate(
	materialization *incrementalResourceMaterialization,
	session *incrementalRenderSession,
	resourceType string,
	owner *incrementalDerivedOwnerResolution,
	ownerObserved bool,
) error {
	if session == nil || session.resourceMaterializations == nil || resourceType == "" ||
		materialization == nil {
		return errors.New("incremental direct resource projection has invalid provenance")
	}
	if err := materialization.authenticateIdentity(session.resourceMaterializations); err != nil {
		return err
	}
	if materialization.resourceType != resourceType ||
		(materialization.scope != resourceInputList && materialization.scope != resourceInputGet) {
		return errors.New("incremental direct resource projection has invalid resource provenance")
	}
	if ownerObserved {
		if materialization.itemCount == 0 || owner.source != resourceType || owner.found {
			return errors.New("incremental direct resource projection has invalid owner absence")
		}
		return owner.authenticate(session, resourceType)
	}
	if materialization.itemCount != 0 || *owner != (incrementalDerivedOwnerResolution{}) {
		return errors.New("incremental direct resource projection has an invalid empty proof")
	}
	return nil
}

func (p *incrementalDirectResourceProjection) authenticateExpected(
	session *incrementalRenderSession,
	materialization *incrementalResourceMaterialization,
	resourceType string,
	owner *incrementalDerivedOwnerResolution,
	ownerObserved bool,
) error {
	if p == nil || p.seal != p || p.proof == nil || p.proof.seal != p.proof ||
		p.proof.projection != p || p.authority == nil || p.proof.authority != p.authority ||
		p.materialization != materialization || p.proof.materialization != materialization ||
		p.resourceType != resourceType || p.proof.resourceType != resourceType ||
		p.owner != *owner || p.proof.owner != *owner ||
		p.ownerObserved != ownerObserved || p.proof.ownerObserved != ownerObserved {
		return errors.New("incremental direct resource projection has invalid provenance")
	}
	return validateIncrementalDirectResourceProjectionCandidate(
		materialization, session, resourceType, owner, ownerObserved,
	)
}

func (p *incrementalDirectResourceProjection) authenticateDetached() error {
	if p == nil || p.seal != p || p.proof == nil || p.proof.seal != p.proof ||
		p.proof.projection != p || p.authority == nil || p.proof.authority != p.authority ||
		p.materialization == nil || p.proof.materialization != p.materialization ||
		p.authority != p.materialization.authority || p.resourceType == "" ||
		p.proof.resourceType != p.resourceType || p.proof.owner != p.owner ||
		p.proof.ownerObserved != p.ownerObserved {
		return errors.New("incremental direct resource projection has invalid provenance")
	}
	if err := p.materialization.authenticateDetached(); err != nil {
		return err
	}
	if p.materialization.resourceType != p.resourceType ||
		(p.materialization.scope != resourceInputList && p.materialization.scope != resourceInputGet) {
		return errors.New("incremental direct resource projection has invalid resource provenance")
	}
	return authenticateDetachedDerivedOwnerAbsence(
		p.materialization, p.resourceType, &p.owner, p.ownerObserved,
	)
}

func authenticateDetachedDerivedOwnerAbsence(
	materialization *incrementalResourceMaterialization,
	resourceType string,
	owner *incrementalDerivedOwnerResolution,
	ownerObserved bool,
) error {
	if !ownerObserved {
		if materialization.itemCount != 0 || *owner != (incrementalDerivedOwnerResolution{}) {
			return errors.New("incremental direct resource projection has an invalid empty proof")
		}
		return nil
	}
	if materialization.itemCount == 0 || owner.source != resourceType || owner.found || owner.owner != "" {
		return errors.New("incremental direct resource projection has invalid owner absence")
	}
	if !owner.supported {
		if owner.input != (incremental.ImmutableInput{}) {
			return errors.New("incremental direct resource projection has invalid owner absence")
		}
		return nil
	}
	expected := deriveOwnerInput(resourceType, nil, false)
	if owner.input.Key != expected.Key || owner.input.Revision != expected.Revision ||
		owner.input.Found != expected.Found || owner.input.Value != string(expected.Value) {
		return errors.New("incremental direct resource projection has invalid owner absence")
	}
	return nil
}

func (p *incrementalDirectResourceProjection) AuthenticateDirectBoundResourceProjection(
	resourceType string,
) error {
	if err := p.authenticateDetached(); err != nil {
		return err
	}
	if resourceType != p.resourceType {
		return errors.New("incremental direct resource projection has invalid immutable provenance")
	}
	return nil
}

func (p *incrementalDirectResourceProjection) ProjectDirectBoundResourceProjection(
	ctx context.Context,
	resourceType string,
	elementType reflect.Type,
) ([]reflect.Value, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if err := p.AuthenticateDirectBoundResourceProjection(resourceType); err != nil {
		return nil, err
	}
	items, err := p.materialization.projectItems(elementType)
	if err != nil {
		return nil, err
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return items, p.AuthenticateDirectBoundResourceProjection(resourceType)
}

func (p *incrementalDirectResourceProjection) ProjectDirectBoundResourceResult(
	ctx context.Context,
	resourceType string,
	operation rendercontext.DirectBoundResourceOperation,
	elementType reflect.Type,
	returnType reflect.Type,
) (reflect.Value, *templating.IncrementalImmutableCertificate, bool, error) {
	if operation != rendercontext.DirectBoundResourceGetSingle || p == nil ||
		p.materialization == nil || p.materialization.scope != resourceInputGet ||
		p.materialization.itemCount != 1 || elementType == nil ||
		returnType != reflect.PointerTo(elementType) {
		return reflect.Value{}, nil, false, nil
	}
	if err := ctx.Err(); err != nil {
		return reflect.Value{}, nil, false, err
	}
	if err := p.AuthenticateDirectBoundResourceProjection(resourceType); err != nil {
		return reflect.Value{}, nil, false, err
	}
	value, certificate, err := p.materialization.directSingleResult(elementType, returnType)
	if err != nil {
		return reflect.Value{}, nil, false, err
	}
	if err := ctx.Err(); err != nil {
		return reflect.Value{}, nil, false, err
	}
	if err := p.AuthenticateDirectBoundResourceProjection(resourceType); err != nil {
		return reflect.Value{}, nil, false, err
	}
	return value, certificate, true, nil
}

func (r *incrementalRenderSession) releaseResourceFrames() {
	if r == nil {
		return
	}
	if r.resourceMaterializations != nil {
		r.resourceMaterializations.revoke()
	}
	if r.resourceItemCache != nil {
		r.resourceItemCache.Revoke()
	}
}

var _ rendercontext.DirectBoundResourceProjection = (*incrementalDirectResourceProjection)(nil)
var _ rendercontext.DirectBoundResourceResultProjection = (*incrementalDirectResourceProjection)(nil)
var _ rendercontext.DirectBoundResourceMaterializationView = (*incrementalVectorResourceView)(nil)
