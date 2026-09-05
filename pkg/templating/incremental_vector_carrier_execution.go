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
	"slices"
)

func (e *ScriggoEngine) IncrementalComponentVectorCarrierEligibility() (
	IncrementalComponentVectorCarrierEligibility,
	bool,
) {
	carrier := e.incrementalVectorCarrier
	if !validIncrementalVectorCarrier(e, carrier) {
		return IncrementalComponentVectorCarrierEligibility{}, false
	}
	bindingNames := make([]string, len(carrier.bindings))
	for index := range carrier.bindings {
		bindingNames[index] = carrier.bindings[index].name
	}
	return IncrementalComponentVectorCarrierEligibility{
		TemplateNames: slices.Clone(carrier.entryPoints),
		BindingNames:  bindingNames,
	}, true
}

// IncrementalComponentVectorCarrierDiagnostic returns the fail-closed build rejection.
func (e *ScriggoEngine) IncrementalComponentVectorCarrierDiagnostic() error {
	return e.incrementalVectorCarrierError
}

func validIncrementalVectorCarrier(engine *ScriggoEngine, carrier *incrementalVectorCarrier) bool {
	if engine == nil || carrier == nil || engine.incrementalVectorCarrier != carrier ||
		carrier.seal != carrier || carrier.template == nil || len(carrier.entryPoints) == 0 ||
		len(carrier.entryPoints) != len(carrier.originals) || len(carrier.laneByName) != len(carrier.entryPoints) ||
		len(carrier.bindings) != len(incrementalVectorBaseBindingNames) {
		return false
	}
	for index, name := range carrier.entryPoints {
		if name == "" || carrier.laneByName[name] != index || engine.compiledTemplates[name] != carrier.originals[index] {
			return false
		}
		entryPoint := engine.incrementalVectorEntryPoints[name]
		if entryPoint == nil || entryPoint.seal != entryPoint || entryPoint.original != carrier.originals[index] {
			return false
		}
	}
	for index := range carrier.bindings {
		if carrier.bindings[index].name != incrementalVectorBaseBindingNames[index] ||
			carrier.bindings[index].variableType == nil {
			return false
		}
	}
	return true
}

func remapIncrementalVectorCarrierError(templateName string, err error) error {
	switch typed := err.(type) {
	case *RenderError:
		if incrementalGeneratedCarrierPath(typed.TemplateName) {
			return NewRenderError(templateName, typed.Cause)
		}
	case *RenderTimeoutError:
		if incrementalGeneratedCarrierPath(typed.TemplateName) {
			return &RenderTimeoutError{TemplateName: templateName, Cause: typed.Cause}
		}
	case *TemplateNotFoundError:
		if incrementalGeneratedCarrierPath(typed.TemplateName) {
			return NewTemplateNotFoundError(templateName, typed.AvailableTemplates)
		}
	}
	return err
}

func incrementalGeneratedCarrierPath(path string) bool {
	return path == incrementalVectorCarrierTemplatePath || path == incrementalSourceTransactionTemplatePath
}
