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

package rendercontext

import (
	"errors"

	"gitlab.com/haproxy-haptic/haptic/pkg/dataplane/renderplan"
	"gitlab.com/haproxy-haptic/haptic/pkg/rendercontent"
	"gitlab.com/haproxy-haptic/haptic/pkg/templating"
)

type planDocumentAssembly struct {
	owner     *PlanRegistry
	document  rendercontent.Document
	sections  *planDocumentSections
	revision  uint64
	authority *PlanTokenAuthority
	prepared  *PreparedPlanSnapshot
	paths     templating.PathResolver
	hasPaths  bool
	seal      *planDocumentAssembly
	auth      planDocumentAssemblyAuthentication
}

type planDocumentSections struct {
	values []renderplan.Section
	seal   *planDocumentSections
}

type planDocumentAssemblyAuthentication struct {
	owner     *planDocumentAssembly
	registry  *PlanRegistry
	document  rendercontent.Document
	sections  *planDocumentSections
	revision  uint64
	authority *PlanTokenAuthority
	prepared  *PreparedPlanSnapshot
	paths     templating.PathResolver
	hasPaths  bool
}

func (r *PlanRegistry) acceptAssembledDocument(
	document rendercontent.Document,
	sections []renderplan.Section,
) error {
	if err := document.ValidateAuthentication(); err != nil {
		return err
	}
	if err := validateSectionDocumentShape(document, sections); err != nil {
		return err
	}
	r.acceptAssembledSections(sections)
	sectionRoot := &planDocumentSections{values: r.assembled}
	sectionRoot.seal = sectionRoot
	proof := &planDocumentAssembly{
		owner: r, document: document, sections: sectionRoot,
		revision: r.declarationRevision, authority: r.authority, prepared: r.prepared,
	}
	if r.paths != nil {
		proof.paths = *r.paths
		proof.hasPaths = true
	}
	proof.seal = proof
	proof.auth = planDocumentAssemblyAuthentication{
		owner: proof, registry: r, document: document, sections: sectionRoot,
		revision: proof.revision, authority: proof.authority, prepared: proof.prepared,
		paths: proof.paths, hasPaths: proof.hasPaths,
	}
	r.documentAssembly = proof
	return nil
}

func (p *planDocumentAssembly) sealIntact() bool {
	return p != nil && p.seal == p && p.auth.owner == p &&
		p.auth.registry == p.owner && p.auth.document == p.document && p.sections != nil &&
		p.sections.seal == p.sections && p.auth.sections == p.sections &&
		p.auth.revision == p.revision && p.auth.authority == p.authority &&
		p.auth.prepared == p.prepared && p.auth.paths == p.paths &&
		p.auth.hasPaths == p.hasPaths
}

func (p *planDocumentAssembly) matchesRegistry(r *PlanRegistry) bool {
	return p.owner == r && p.revision == r.declarationRevision && p.authority == r.authority &&
		p.prepared == r.prepared && slicesExactRoot(p.sections.values, r.assembled) &&
		p.matchesPaths(r.paths)
}

func (r *PlanRegistry) validateDocumentAssembly(document rendercontent.Document) error {
	proof := r.documentAssembly
	if !proof.sealIntact() || !proof.matchesRegistry(r) {
		return errors.New("planRegistry: assembled document proof is stale")
	}
	if err := proof.document.ValidateAuthentication(); err != nil {
		return errors.New("planRegistry: assembled document proof is invalid")
	}
	same, err := proof.document.SameRoot(document)
	if err != nil {
		return err
	}
	if !same {
		return errors.New("planRegistry: document does not match the authenticated assembly")
	}
	return nil
}

func (p *planDocumentAssembly) matchesPaths(paths *templating.PathResolver) bool {
	if paths == nil {
		return !p.hasPaths
	}
	return p.hasPaths && p.paths == *paths
}

func slicesExactRoot[T any](left, right []T) bool {
	if len(left) != len(right) {
		return false
	}
	if len(left) == 0 {
		return (left == nil) == (right == nil)
	}
	return &left[0] == &right[0]
}
