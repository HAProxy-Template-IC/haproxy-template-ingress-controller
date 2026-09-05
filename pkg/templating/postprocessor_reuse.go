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

package templating

import (
	"errors"
	"fmt"
	"reflect"
	"slices"
)

// PostProcessReuseProof is an opaque identity for one compiler-certified chain.
type PostProcessReuseProof struct {
	owner        *ScriggoEngine
	templateName string
	processors   []PostProcessor
	seal         *PostProcessReuseProof
}

func newPostProcessReuseProof(
	owner *ScriggoEngine,
	templateName string,
	processors []PostProcessor,
) *PostProcessReuseProof {
	proof := &PostProcessReuseProof{
		owner:        owner,
		templateName: templateName,
		processors:   slices.Clone(processors),
	}
	proof.seal = proof
	return proof
}

// ValidateAuthentication rejects copied, substituted, and stale proofs.
func (p *PostProcessReuseProof) ValidateAuthentication() error {
	if p == nil || p.seal != p || p.owner == nil || p.templateName == "" {
		return errors.New("post-process reuse proof is invalid")
	}
	if p.owner.postProcessReuseProofs[p.templateName] != p {
		return errors.New("post-process reuse proof is not owned by its engine")
	}
	if !samePostProcessorChain(p.processors, p.owner.postProcessors[p.templateName]) {
		return errors.New("post-process reuse proof does not match the configured chain")
	}
	if len(p.processors) > 0 && !postProcessorChainCacheable(p.processors) {
		return errors.New("post-process reuse proof contains an uncacheable processor")
	}
	return nil
}

// CertifiesIdentity reports whether this proof belongs to the exact engine and
// template and authenticates an empty post-processor chain.
func (p *PostProcessReuseProof) CertifiesIdentity(engine Engine, templateName string) (bool, error) {
	if err := p.ValidateAuthentication(); err != nil {
		return false, err
	}
	if templateName != p.templateName {
		return false, errors.New("post-process reuse proof belongs to another template")
	}
	engineValue := reflect.ValueOf(engine)
	ownerValue := reflect.ValueOf(p.owner)
	if !engineValue.IsValid() || engineValue.Type() != ownerValue.Type() ||
		engineValue.Kind() != reflect.Pointer || engineValue.Pointer() != ownerValue.Pointer() {
		return false, errors.New("post-process reuse proof belongs to another engine")
	}
	return len(p.processors) == 0, nil
}

func samePostProcessorChain(first, second []PostProcessor) bool {
	if len(first) != len(second) {
		return false
	}
	for index := range first {
		if !samePostProcessor(first[index], second[index]) {
			return false
		}
	}
	return true
}

func samePostProcessor(first, second PostProcessor) bool {
	if first == nil || second == nil {
		return first == nil && second == nil
	}
	firstValue := reflect.ValueOf(first)
	secondValue := reflect.ValueOf(second)
	if firstValue.Type() != secondValue.Type() {
		return false
	}
	if firstValue.Comparable() {
		return firstValue.Interface() == secondValue.Interface()
	}
	return false
}

// PostProcessReuseProof returns nil when the chain cannot be proven deterministic.
func (e *ScriggoEngine) PostProcessReuseProof(templateName string) (*PostProcessReuseProof, error) {
	if e == nil {
		return nil, errors.New("post-process reuse proof engine is nil")
	}
	proof := e.postProcessReuseProofs[templateName]
	if proof != nil {
		if proof.templateName != templateName {
			return nil, fmt.Errorf("template %q: post-process reuse proof belongs to another template", templateName)
		}
		if err := proof.ValidateAuthentication(); err != nil {
			return nil, fmt.Errorf("template %q: %w", templateName, err)
		}
	}
	return proof, nil
}
