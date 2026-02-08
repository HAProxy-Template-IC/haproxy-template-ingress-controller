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

// NewACLFrontendCreate creates an operation to create an ACL in a frontend.
func NewACLFrontendCreate(frontendName string, acl *models.ACL, index int) Operation {
	return NewIndexChildOp(
		OperationCreate,
		"acl",
		PriorityACL,
		frontendName,
		index,
		acl,
		Identity[*models.ACL],
		executors.ACLFrontendCreate(),
		DescribeACL(OperationCreate, acl.ACLName, "frontend", frontendName),
	)
}

// NewACLFrontendUpdate creates an operation to update an ACL in a frontend.
func NewACLFrontendUpdate(frontendName string, acl *models.ACL, index int) Operation {
	return NewIndexChildOp(
		OperationUpdate,
		"acl",
		PriorityACL,
		frontendName,
		index,
		acl,
		Identity[*models.ACL],
		executors.ACLFrontendUpdate(),
		DescribeACL(OperationUpdate, acl.ACLName, "frontend", frontendName),
	)
}

// NewACLFrontendDelete creates an operation to delete an ACL from a frontend.
func NewACLFrontendDelete(frontendName string, acl *models.ACL, index int) Operation {
	return NewIndexChildOp(
		OperationDelete,
		"acl",
		PriorityACL,
		frontendName,
		index,
		acl,
		Nil[*models.ACL],
		executors.ACLFrontendDelete(),
		DescribeACL(OperationDelete, acl.ACLName, "frontend", frontendName),
	)
}

// NewACLBackendCreate creates an operation to create an ACL in a backend.
func NewACLBackendCreate(backendName string, acl *models.ACL, index int) Operation {
	return NewIndexChildOp(
		OperationCreate,
		"acl",
		PriorityACL,
		backendName,
		index,
		acl,
		Identity[*models.ACL],
		executors.ACLBackendCreate(),
		DescribeACL(OperationCreate, acl.ACLName, "backend", backendName),
	)
}

// NewACLBackendUpdate creates an operation to update an ACL in a backend.
func NewACLBackendUpdate(backendName string, acl *models.ACL, index int) Operation {
	return NewIndexChildOp(
		OperationUpdate,
		"acl",
		PriorityACL,
		backendName,
		index,
		acl,
		Identity[*models.ACL],
		executors.ACLBackendUpdate(),
		DescribeACL(OperationUpdate, acl.ACLName, "backend", backendName),
	)
}

// NewACLBackendDelete creates an operation to delete an ACL from a backend.
func NewACLBackendDelete(backendName string, acl *models.ACL, index int) Operation {
	return NewIndexChildOp(
		OperationDelete,
		"acl",
		PriorityACL,
		backendName,
		index,
		acl,
		Nil[*models.ACL],
		executors.ACLBackendDelete(),
		DescribeACL(OperationDelete, acl.ACLName, "backend", backendName),
	)
}
