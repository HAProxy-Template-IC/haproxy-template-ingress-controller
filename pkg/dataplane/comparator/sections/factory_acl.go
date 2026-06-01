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
)

// describeACLOp wraps DescribeACL into the IndexChildCRUDWithDescriber describer shape.
func describeACLOp(parentType string) func(OperationType, *models.ACL, string, int) func() string {
	return func(op OperationType, acl *models.ACL, parentName string, _ int) func() string {
		return DescribeACL(op, acl.ACLName, parentType, parentName)
	}
}

// CRUD builders for ACLs in frontends and backends.
var (
	aclFrontendOps = NewIndexChildCRUDWithDescriber[*models.ACL]("acl", describeACLOp("frontend"))
	aclBackendOps  = NewIndexChildCRUDWithDescriber[*models.ACL]("acl", describeACLOp("backend"))
)

// NewACLFrontendCreate creates an operation to create an ACL in a frontend.
func NewACLFrontendCreate(frontendName string, acl *models.ACL, index int) Operation {
	return aclFrontendOps.Create(frontendName, acl, index)
}

// NewACLFrontendUpdate creates an operation to update an ACL in a frontend.
func NewACLFrontendUpdate(frontendName string, acl *models.ACL, index int) Operation {
	return aclFrontendOps.Update(frontendName, acl, index)
}

// NewACLFrontendDelete creates an operation to delete an ACL from a frontend.
func NewACLFrontendDelete(frontendName string, acl *models.ACL, index int) Operation {
	return aclFrontendOps.Delete(frontendName, acl, index)
}

// NewACLBackendCreate creates an operation to create an ACL in a backend.
func NewACLBackendCreate(backendName string, acl *models.ACL, index int) Operation {
	return aclBackendOps.Create(backendName, acl, index)
}

// NewACLBackendUpdate creates an operation to update an ACL in a backend.
func NewACLBackendUpdate(backendName string, acl *models.ACL, index int) Operation {
	return aclBackendOps.Update(backendName, acl, index)
}

// NewACLBackendDelete creates an operation to delete an ACL from a backend.
func NewACLBackendDelete(backendName string, acl *models.ACL, index int) Operation {
	return aclBackendOps.Delete(backendName, acl, index)
}
