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
	"fmt"

	"github.com/haproxytech/client-native/v6/models"

	"gitlab.com/haproxy-haptic/haptic/pkg/dataplane/comparator/sections/executors"
)

// filterIdentifier extracts the type identifier from a Filter model.
func filterIdentifier(filter *models.Filter) string { return filter.Type }

// NewFilterFrontendCreate creates an operation to create a filter in a frontend.
func NewFilterFrontendCreate(frontendName string, filter *models.Filter, index int) Operation {
	return NewIndexChildOp(
		OperationCreate,
		"filter",
		PriorityFilter,
		frontendName,
		index,
		filter,
		Identity[*models.Filter],
		executors.FilterFrontendCreate(),
		DescribeTypedChild(OperationCreate, "filter", filterIdentifier(filter), fmt.Sprintf("at index %d", index), "frontend", frontendName),
	)
}

// NewFilterFrontendUpdate creates an operation to update a filter in a frontend.
func NewFilterFrontendUpdate(frontendName string, filter *models.Filter, index int) Operation {
	return NewIndexChildOp(
		OperationUpdate,
		"filter",
		PriorityFilter,
		frontendName,
		index,
		filter,
		Identity[*models.Filter],
		executors.FilterFrontendUpdate(),
		DescribeTypedChild(OperationUpdate, "filter", filterIdentifier(filter), fmt.Sprintf("at index %d", index), "frontend", frontendName),
	)
}

// NewFilterFrontendDelete creates an operation to delete a filter from a frontend.
func NewFilterFrontendDelete(frontendName string, filter *models.Filter, index int) Operation {
	return NewIndexChildOp(
		OperationDelete,
		"filter",
		PriorityFilter,
		frontendName,
		index,
		filter,
		Nil[*models.Filter],
		executors.FilterFrontendDelete(),
		DescribeTypedChild(OperationDelete, "filter", filterIdentifier(filter), fmt.Sprintf("at index %d", index), "frontend", frontendName),
	)
}

// NewFilterBackendCreate creates an operation to create a filter in a backend.
func NewFilterBackendCreate(backendName string, filter *models.Filter, index int) Operation {
	return NewIndexChildOp(
		OperationCreate,
		"filter",
		PriorityFilter,
		backendName,
		index,
		filter,
		Identity[*models.Filter],
		executors.FilterBackendCreate(),
		DescribeTypedChild(OperationCreate, "filter", filterIdentifier(filter), fmt.Sprintf("at index %d", index), "backend", backendName),
	)
}

// NewFilterBackendUpdate creates an operation to update a filter in a backend.
func NewFilterBackendUpdate(backendName string, filter *models.Filter, index int) Operation {
	return NewIndexChildOp(
		OperationUpdate,
		"filter",
		PriorityFilter,
		backendName,
		index,
		filter,
		Identity[*models.Filter],
		executors.FilterBackendUpdate(),
		DescribeTypedChild(OperationUpdate, "filter", filterIdentifier(filter), fmt.Sprintf("at index %d", index), "backend", backendName),
	)
}

// NewFilterBackendDelete creates an operation to delete a filter from a backend.
func NewFilterBackendDelete(backendName string, filter *models.Filter, index int) Operation {
	return NewIndexChildOp(
		OperationDelete,
		"filter",
		PriorityFilter,
		backendName,
		index,
		filter,
		Nil[*models.Filter],
		executors.FilterBackendDelete(),
		DescribeTypedChild(OperationDelete, "filter", filterIdentifier(filter), fmt.Sprintf("at index %d", index), "backend", backendName),
	)
}

// logTargetIdentifier extracts the address identifier from a LogTarget model.
func logTargetIdentifier(logTarget *models.LogTarget) string { return logTarget.Address }

// NewLogTargetFrontendCreate creates an operation to create a log target in a frontend.
func NewLogTargetFrontendCreate(frontendName string, logTarget *models.LogTarget, index int) Operation {
	return NewIndexChildOp(
		OperationCreate,
		"log_target",
		PriorityLogTarget,
		frontendName,
		index,
		logTarget,
		Identity[*models.LogTarget],
		executors.LogTargetFrontendCreate(),
		DescribeTypedChild(OperationCreate, "log target", logTargetIdentifier(logTarget), fmt.Sprintf("at index %d", index), "frontend", frontendName),
	)
}

// NewLogTargetFrontendUpdate creates an operation to update a log target in a frontend.
func NewLogTargetFrontendUpdate(frontendName string, logTarget *models.LogTarget, index int) Operation {
	return NewIndexChildOp(
		OperationUpdate,
		"log_target",
		PriorityLogTarget,
		frontendName,
		index,
		logTarget,
		Identity[*models.LogTarget],
		executors.LogTargetFrontendUpdate(),
		DescribeTypedChild(OperationUpdate, "log target", logTargetIdentifier(logTarget), fmt.Sprintf("at index %d", index), "frontend", frontendName),
	)
}

// NewLogTargetFrontendDelete creates an operation to delete a log target from a frontend.
func NewLogTargetFrontendDelete(frontendName string, logTarget *models.LogTarget, index int) Operation {
	return NewIndexChildOp(
		OperationDelete,
		"log_target",
		PriorityLogTarget,
		frontendName,
		index,
		logTarget,
		Nil[*models.LogTarget],
		executors.LogTargetFrontendDelete(),
		DescribeTypedChild(OperationDelete, "log target", logTargetIdentifier(logTarget), fmt.Sprintf("at index %d", index), "frontend", frontendName),
	)
}

// NewLogTargetBackendCreate creates an operation to create a log target in a backend.
func NewLogTargetBackendCreate(backendName string, logTarget *models.LogTarget, index int) Operation {
	return NewIndexChildOp(
		OperationCreate,
		"log_target",
		PriorityLogTarget,
		backendName,
		index,
		logTarget,
		Identity[*models.LogTarget],
		executors.LogTargetBackendCreate(),
		DescribeTypedChild(OperationCreate, "log target", logTargetIdentifier(logTarget), fmt.Sprintf("at index %d", index), "backend", backendName),
	)
}

// NewLogTargetBackendUpdate creates an operation to update a log target in a backend.
func NewLogTargetBackendUpdate(backendName string, logTarget *models.LogTarget, index int) Operation {
	return NewIndexChildOp(
		OperationUpdate,
		"log_target",
		PriorityLogTarget,
		backendName,
		index,
		logTarget,
		Identity[*models.LogTarget],
		executors.LogTargetBackendUpdate(),
		DescribeTypedChild(OperationUpdate, "log target", logTargetIdentifier(logTarget), fmt.Sprintf("at index %d", index), "backend", backendName),
	)
}

// NewLogTargetBackendDelete creates an operation to delete a log target from a backend.
func NewLogTargetBackendDelete(backendName string, logTarget *models.LogTarget, index int) Operation {
	return NewIndexChildOp(
		OperationDelete,
		"log_target",
		PriorityLogTarget,
		backendName,
		index,
		logTarget,
		Nil[*models.LogTarget],
		executors.LogTargetBackendDelete(),
		DescribeTypedChild(OperationDelete, "log target", logTargetIdentifier(logTarget), fmt.Sprintf("at index %d", index), "backend", backendName),
	)
}

// NewLogForwardCreate creates an operation to create a log-forward section.
func NewLogForwardCreate(logForward *models.LogForward) Operation {
	return NewTopLevelOp(
		OperationCreate,
		"log_forward",
		PriorityLogForward,
		logForward,
		Identity[*models.LogForward],
		LogForwardName,
		executors.LogForwardCreate(),
		DescribeTopLevel(OperationCreate, "log-forward", logForward.Name),
	)
}

// NewLogForwardUpdate creates an operation to update a log-forward section.
func NewLogForwardUpdate(logForward *models.LogForward) Operation {
	return NewTopLevelOp(
		OperationUpdate,
		"log_forward",
		PriorityLogForward,
		logForward,
		Identity[*models.LogForward],
		LogForwardName,
		executors.LogForwardUpdate(),
		DescribeTopLevel(OperationUpdate, "log-forward", logForward.Name),
	)
}

// NewLogForwardDelete creates an operation to delete a log-forward section.
func NewLogForwardDelete(logForward *models.LogForward) Operation {
	return NewTopLevelOp(
		OperationDelete,
		"log_forward",
		PriorityLogForward,
		logForward,
		Nil[*models.LogForward],
		LogForwardName,
		executors.LogForwardDelete(),
		DescribeTopLevel(OperationDelete, "log-forward", logForward.Name),
	)
}
