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

// describeFilter generates a human-readable description for a filter operation.
func describeFilter(opType OperationType, filter *models.Filter, parentType, parentName string, index int) string {
	identifier := filter.Type
	if identifier == "" {
		identifier = fmt.Sprintf("at index %d", index)
	} else {
		identifier = fmt.Sprintf("(%s)", identifier)
	}
	switch opType {
	case OperationCreate:
		return fmt.Sprintf("Create filter %s in %s '%s'", identifier, parentType, parentName)
	case OperationUpdate:
		return fmt.Sprintf("Update filter %s in %s '%s'", identifier, parentType, parentName)
	case OperationDelete:
		return fmt.Sprintf("Delete filter %s from %s '%s'", identifier, parentType, parentName)
	default:
		return fmt.Sprintf("Unknown operation on filter %s in %s '%s'", identifier, parentType, parentName)
	}
}

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
		func() string { return describeFilter(OperationCreate, filter, "frontend", frontendName, index) },
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
		func() string { return describeFilter(OperationUpdate, filter, "frontend", frontendName, index) },
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
		func() string { return describeFilter(OperationDelete, filter, "frontend", frontendName, index) },
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
		func() string { return describeFilter(OperationCreate, filter, "backend", backendName, index) },
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
		func() string { return describeFilter(OperationUpdate, filter, "backend", backendName, index) },
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
		func() string { return describeFilter(OperationDelete, filter, "backend", backendName, index) },
	)
}

// describeLogTarget generates a human-readable description for a log target operation.
// Uses the Address field if available (e.g., "ring@myring"), falls back to index.
func describeLogTarget(opType OperationType, logTarget *models.LogTarget, parentType, parentName string, index int) string {
	// Use Address if available (e.g., "ring@myring", "127.0.0.1:514")
	identifier := logTarget.Address
	if identifier == "" {
		identifier = fmt.Sprintf("at index %d", index)
	} else {
		identifier = fmt.Sprintf("(%s)", identifier)
	}

	// Use appropriate verb based on operation type
	switch opType {
	case OperationCreate:
		return fmt.Sprintf("Create log target %s in %s '%s'", identifier, parentType, parentName)
	case OperationUpdate:
		return fmt.Sprintf("Update log target %s in %s '%s'", identifier, parentType, parentName)
	case OperationDelete:
		return fmt.Sprintf("Delete log target %s from %s '%s'", identifier, parentType, parentName)
	default:
		return fmt.Sprintf("Unknown operation on log target %s in %s '%s'", identifier, parentType, parentName)
	}
}

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
		func() string { return describeLogTarget(OperationCreate, logTarget, "frontend", frontendName, index) },
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
		func() string { return describeLogTarget(OperationUpdate, logTarget, "frontend", frontendName, index) },
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
		func() string { return describeLogTarget(OperationDelete, logTarget, "frontend", frontendName, index) },
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
		func() string { return describeLogTarget(OperationCreate, logTarget, "backend", backendName, index) },
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
		func() string { return describeLogTarget(OperationUpdate, logTarget, "backend", backendName, index) },
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
		func() string { return describeLogTarget(OperationDelete, logTarget, "backend", backendName, index) },
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
