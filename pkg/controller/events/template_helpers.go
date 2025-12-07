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

package events

import "haproxy-template-ic/pkg/dataplane"

// GetValidationAuxiliaryFiles returns ValidationAuxiliaryFiles with proper type.
// Returns nil, false if the type assertion fails.
//
// This method provides type-safe access to the ValidationAuxiliaryFiles field,
// avoiding the need for manual type assertions in consumer code.
func (e *TemplateRenderedEvent) GetValidationAuxiliaryFiles() (*dataplane.AuxiliaryFiles, bool) {
	aux, ok := e.ValidationAuxiliaryFiles.(*dataplane.AuxiliaryFiles)
	return aux, ok
}

// GetValidationPaths returns ValidationPaths with proper type.
// Returns nil, false if the type assertion fails.
//
// This method provides type-safe access to the ValidationPaths field,
// avoiding the need for manual type assertions in consumer code.
func (e *TemplateRenderedEvent) GetValidationPaths() (*dataplane.ValidationPaths, bool) {
	paths, ok := e.ValidationPaths.(*dataplane.ValidationPaths)
	return paths, ok
}

// GetAuxiliaryFiles returns AuxiliaryFiles with proper type.
// Returns nil, false if the type assertion fails.
//
// This method provides type-safe access to the AuxiliaryFiles field,
// avoiding the need for manual type assertions in consumer code.
func (e *TemplateRenderedEvent) GetAuxiliaryFiles() (*dataplane.AuxiliaryFiles, bool) {
	aux, ok := e.AuxiliaryFiles.(*dataplane.AuxiliaryFiles)
	return aux, ok
}
