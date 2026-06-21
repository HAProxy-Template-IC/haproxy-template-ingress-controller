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

// describeACLOp wraps describeACL into the IndexChildCRUDWithDescriber describer shape.
func describeACLOp(parentType string) func(OperationType, *models.ACL, string, int) func() string {
	return func(op OperationType, acl *models.ACL, parentName string, _ int) func() string {
		return describeACL(op, acl.ACLName, parentType, parentName)
	}
}

// CRUD builders for ACLs in frontends and backends.
var (
	ACLFrontendOps = NewIndexChildCRUDWithDescriber[*models.ACL]("acl", describeACLOp("frontend"))
	ACLBackendOps  = NewIndexChildCRUDWithDescriber[*models.ACL]("acl", describeACLOp("backend"))
)
