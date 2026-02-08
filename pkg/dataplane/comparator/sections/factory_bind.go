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

// describeBindWithSSL creates a description for bind operations that includes SSL info.
// This matches the legacy behavior where bind descriptions included SSL certificate paths.
func describeBindWithSSL(opType OperationType, bind *models.Bind, frontendName string) string {
	// Format bind description based on address and port
	bindDesc := ""
	if bind.Address != "" && bind.Port != nil {
		bindDesc = fmt.Sprintf("%s:%d", bind.Address, *bind.Port)
	} else if bind.Port != nil {
		bindDesc = fmt.Sprintf("*:%d", *bind.Port)
	} else if bind.Name != "" {
		bindDesc = bind.Name
	}

	// Add SSL info if present
	if bind.Ssl {
		sslInfo := " ssl"
		if bind.SslCertificate != "" {
			sslInfo += fmt.Sprintf(" crt %s", bind.SslCertificate)
		}
		bindDesc += sslInfo
	}

	// Use appropriate verb based on operation type
	switch opType {
	case OperationCreate:
		return fmt.Sprintf("Create bind '%s' in frontend '%s'", bindDesc, frontendName)
	case OperationUpdate:
		return fmt.Sprintf("Update bind '%s' in frontend '%s'", bindDesc, frontendName)
	case OperationDelete:
		return fmt.Sprintf("Delete bind '%s' from frontend '%s'", bindDesc, frontendName)
	default:
		return fmt.Sprintf("Unknown operation on bind '%s' in frontend '%s'", bindDesc, frontendName)
	}
}

// NewBindFrontendCreate creates an operation to create a bind in a frontend.
func NewBindFrontendCreate(frontendName, bindName string, bind *models.Bind) Operation {
	return NewNameChildOp(
		OperationCreate,
		"bind",
		PriorityBind,
		frontendName,
		bindName,
		bind,
		Identity[*models.Bind],
		executors.BindFrontendCreate(frontendName),
		func() string { return describeBindWithSSL(OperationCreate, bind, frontendName) },
	)
}

// NewBindFrontendUpdate creates an operation to update a bind in a frontend.
func NewBindFrontendUpdate(frontendName, bindName string, bind *models.Bind) Operation {
	return NewNameChildOp(
		OperationUpdate,
		"bind",
		PriorityBind,
		frontendName,
		bindName,
		bind,
		Identity[*models.Bind],
		executors.BindFrontendUpdate(frontendName),
		func() string { return describeBindWithSSL(OperationUpdate, bind, frontendName) },
	)
}

// NewBindFrontendDelete creates an operation to delete a bind from a frontend.
func NewBindFrontendDelete(frontendName, bindName string, bind *models.Bind) Operation {
	return NewNameChildOp(
		OperationDelete,
		"bind",
		PriorityBind,
		frontendName,
		bindName,
		bind,
		Nil[*models.Bind],
		executors.BindFrontendDelete(frontendName),
		func() string { return describeBindWithSSL(OperationDelete, bind, frontendName) },
	)
}
