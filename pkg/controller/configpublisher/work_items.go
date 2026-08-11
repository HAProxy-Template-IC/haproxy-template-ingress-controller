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

package configpublisher

import (
	"fmt"
	"slices"

	"gitlab.com/haproxy-haptic/haptic/pkg/apis/haproxytemplate/v1alpha1"
	"gitlab.com/haproxy-haptic/haptic/pkg/controller/events"
	"gitlab.com/haproxy-haptic/haptic/pkg/dataplane"
)

func cloneRenderedConfigEntry(entry *renderedConfigEntry) *renderedConfigEntry {
	clone := *entry
	clone.auxFiles = cloneAuxiliaryFiles(entry.auxFiles)
	return &clone
}

func cloneAuxiliaryFiles(files *dataplane.AuxiliaryFiles) *dataplane.AuxiliaryFiles {
	if files == nil {
		return nil
	}
	clone := *files
	clone.MapFiles = slices.Clone(files.MapFiles)
	clone.SSLCertificates = slices.Clone(files.SSLCertificates)
	clone.SSLCaFiles = slices.Clone(files.SSLCaFiles)
	clone.GeneralFiles = slices.Clone(files.GeneralFiles)
	for i := range clone.GeneralFiles {
		if files.GeneralFiles[i].ReloadOnPush != nil {
			reloadOnPush := *files.GeneralFiles[i].ReloadOnPush
			clone.GeneralFiles[i].ReloadOnPush = &reloadOnPush
		}
	}
	clone.CRTListFiles = slices.Clone(files.CRTListFiles)
	return &clone
}

func (c *Component) makePublishWorkItem(
	correlationID string,
	templateConfig *v1alpha1.HAProxyTemplateConfig,
	entry *renderedConfigEntry,
	deployDriven bool,
) *publishWorkItem {
	templateSnapshot := templateConfig.DeepCopy()
	entrySnapshot := cloneRenderedConfigEntry(entry)
	generation, term, superseded := c.assignPublishAuthority(deployDriven)
	return &publishWorkItem{
		correlationID:  correlationID,
		templateConfig: templateSnapshot,
		entry:          entrySnapshot,
		request:        c.buildPublishRequest(templateSnapshot, entrySnapshot),
		deployDriven:   deployDriven,
		generation:     generation,
		term:           term,
		superseded:     superseded,
	}
}

func (c *Component) makeValidationFailedWorkItem(
	correlationID string,
	event *events.ValidationFailedEvent,
	templateConfig *v1alpha1.HAProxyTemplateConfig,
	entry *renderedConfigEntry,
) *validationFailedWorkItem {
	validationError := ""
	if len(event.Errors) > 0 {
		validationError = event.Errors[0]
		if len(event.Errors) > 1 {
			validationError = fmt.Sprintf("%s (+%d more errors)", validationError, len(event.Errors)-1)
		}
	}

	templateSnapshot := templateConfig.DeepCopy()
	entrySnapshot := cloneRenderedConfigEntry(entry)
	request := c.buildPublishRequest(templateSnapshot, entrySnapshot)
	request.NameSuffix = "-invalid"
	request.ValidationError = validationError
	generation, term, superseded := c.assignInvalidGeneration()
	return &validationFailedWorkItem{
		correlationID:   correlationID,
		templateConfig:  templateSnapshot,
		entry:           entrySnapshot,
		request:         request,
		validationError: validationError,
		generation:      generation,
		term:            term,
		superseded:      superseded,
	}
}
