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

package sections

import (
	"github.com/haproxytech/client-native/v6/models"

	"gitlab.com/haproxy-haptic/haptic/pkg/dataplane/comparator/sections/executors"
)

// filterIdentifier extracts the type identifier from a Filter model.
func filterIdentifier(filter *models.Filter) string { return filter.Type }

// logTargetIdentifier extracts the address identifier from a LogTarget model.
func logTargetIdentifier(logTarget *models.LogTarget) string { return logTarget.Address }

// CRUD builders for filters, log targets and the log-forward top-level section.
var (
	filterFrontendOps = NewIndexChildCRUD[*models.Filter](
		"filter", "filter", "frontend", PriorityFilter, filterIdentifier,
		executors.FilterFrontendCreate(), executors.FilterFrontendUpdate(), executors.FilterFrontendDelete(),
	)
	filterBackendOps = NewIndexChildCRUD[*models.Filter](
		"filter", "filter", "backend", PriorityFilter, filterIdentifier,
		executors.FilterBackendCreate(), executors.FilterBackendUpdate(), executors.FilterBackendDelete(),
	)
	logTargetFrontendOps = NewIndexChildCRUD[*models.LogTarget](
		"log_target", "log target", "frontend", PriorityLogTarget, logTargetIdentifier,
		executors.LogTargetFrontendCreate(), executors.LogTargetFrontendUpdate(), executors.LogTargetFrontendDelete(),
	)
	logTargetBackendOps = NewIndexChildCRUD[*models.LogTarget](
		"log_target", "log target", "backend", PriorityLogTarget, logTargetIdentifier,
		executors.LogTargetBackendCreate(), executors.LogTargetBackendUpdate(), executors.LogTargetBackendDelete(),
	)
	logForwardOps = NewTopLevelCRUD[*models.LogForward](
		"log_forward", "log-forward", PriorityLogForward, LogForwardName,
		executors.LogForwardCreate(), executors.LogForwardUpdate(), executors.LogForwardDelete(),
	)
)

// NewFilterFrontendCreate creates an operation to create a filter in a frontend.
func NewFilterFrontendCreate(frontendName string, filter *models.Filter, index int) Operation {
	return filterFrontendOps.Create(frontendName, filter, index)
}

// NewFilterFrontendUpdate creates an operation to update a filter in a frontend.
func NewFilterFrontendUpdate(frontendName string, filter *models.Filter, index int) Operation {
	return filterFrontendOps.Update(frontendName, filter, index)
}

// NewFilterFrontendDelete creates an operation to delete a filter from a frontend.
func NewFilterFrontendDelete(frontendName string, filter *models.Filter, index int) Operation {
	return filterFrontendOps.Delete(frontendName, filter, index)
}

// NewFilterBackendCreate creates an operation to create a filter in a backend.
func NewFilterBackendCreate(backendName string, filter *models.Filter, index int) Operation {
	return filterBackendOps.Create(backendName, filter, index)
}

// NewFilterBackendUpdate creates an operation to update a filter in a backend.
func NewFilterBackendUpdate(backendName string, filter *models.Filter, index int) Operation {
	return filterBackendOps.Update(backendName, filter, index)
}

// NewFilterBackendDelete creates an operation to delete a filter from a backend.
func NewFilterBackendDelete(backendName string, filter *models.Filter, index int) Operation {
	return filterBackendOps.Delete(backendName, filter, index)
}

// NewLogTargetFrontendCreate creates an operation to create a log target in a frontend.
func NewLogTargetFrontendCreate(frontendName string, logTarget *models.LogTarget, index int) Operation {
	return logTargetFrontendOps.Create(frontendName, logTarget, index)
}

// NewLogTargetFrontendUpdate creates an operation to update a log target in a frontend.
func NewLogTargetFrontendUpdate(frontendName string, logTarget *models.LogTarget, index int) Operation {
	return logTargetFrontendOps.Update(frontendName, logTarget, index)
}

// NewLogTargetFrontendDelete creates an operation to delete a log target from a frontend.
func NewLogTargetFrontendDelete(frontendName string, logTarget *models.LogTarget, index int) Operation {
	return logTargetFrontendOps.Delete(frontendName, logTarget, index)
}

// NewLogTargetBackendCreate creates an operation to create a log target in a backend.
func NewLogTargetBackendCreate(backendName string, logTarget *models.LogTarget, index int) Operation {
	return logTargetBackendOps.Create(backendName, logTarget, index)
}

// NewLogTargetBackendUpdate creates an operation to update a log target in a backend.
func NewLogTargetBackendUpdate(backendName string, logTarget *models.LogTarget, index int) Operation {
	return logTargetBackendOps.Update(backendName, logTarget, index)
}

// NewLogTargetBackendDelete creates an operation to delete a log target from a backend.
func NewLogTargetBackendDelete(backendName string, logTarget *models.LogTarget, index int) Operation {
	return logTargetBackendOps.Delete(backendName, logTarget, index)
}

// NewLogForwardCreate creates an operation to create a log-forward section.
func NewLogForwardCreate(logForward *models.LogForward) Operation {
	return logForwardOps.Create(logForward)
}

// NewLogForwardUpdate creates an operation to update a log-forward section.
func NewLogForwardUpdate(logForward *models.LogForward) Operation {
	return logForwardOps.Update(logForward)
}

// NewLogForwardDelete creates an operation to delete a log-forward section.
func NewLogForwardDelete(logForward *models.LogForward) Operation {
	return logForwardOps.Delete(logForward)
}
