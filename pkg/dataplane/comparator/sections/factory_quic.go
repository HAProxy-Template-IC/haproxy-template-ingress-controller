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

// NewQUICInitialRuleFrontendCreate creates an operation to create a QUIC initial rule in a frontend.
// QUIC initial rules are only available in HAProxy DataPlane API v3.1+.
func NewQUICInitialRuleFrontendCreate(frontendName string, rule *models.QUICInitialRule, index int) Operation {
	return NewIndexChildOp(
		OperationCreate,
		"quic_initial_rule",
		PriorityQUICInitialRule,
		frontendName,
		index,
		rule,
		IdentityQUICInitialRule,
		executors.QUICInitialRuleFrontendCreate(),
		DescribeIndexChild(OperationCreate, "quic-initial-rule", index, "frontend", frontendName),
	)
}

// NewQUICInitialRuleFrontendUpdate creates an operation to update a QUIC initial rule in a frontend.
// QUIC initial rules are only available in HAProxy DataPlane API v3.1+.
func NewQUICInitialRuleFrontendUpdate(frontendName string, rule *models.QUICInitialRule, index int) Operation {
	return NewIndexChildOp(
		OperationUpdate,
		"quic_initial_rule",
		PriorityQUICInitialRule,
		frontendName,
		index,
		rule,
		IdentityQUICInitialRule,
		executors.QUICInitialRuleFrontendUpdate(),
		DescribeIndexChild(OperationUpdate, "quic-initial-rule", index, "frontend", frontendName),
	)
}

// NewQUICInitialRuleFrontendDelete creates an operation to delete a QUIC initial rule from a frontend.
// QUIC initial rules are only available in HAProxy DataPlane API v3.1+.
func NewQUICInitialRuleFrontendDelete(frontendName string, rule *models.QUICInitialRule, index int) Operation {
	return NewIndexChildOp(
		OperationDelete,
		"quic_initial_rule",
		PriorityQUICInitialRule,
		frontendName,
		index,
		rule,
		NilQUICInitialRule,
		executors.QUICInitialRuleFrontendDelete(),
		DescribeIndexChild(OperationDelete, "quic-initial-rule", index, "frontend", frontendName),
	)
}

// NewQUICInitialRuleDefaultsCreate creates an operation to create a QUIC initial rule in defaults.
// QUIC initial rules are only available in HAProxy DataPlane API v3.1+.
func NewQUICInitialRuleDefaultsCreate(defaultsName string, rule *models.QUICInitialRule, index int) Operation {
	return NewIndexChildOp(
		OperationCreate,
		"quic_initial_rule",
		PriorityQUICInitialRule,
		defaultsName,
		index,
		rule,
		IdentityQUICInitialRule,
		executors.QUICInitialRuleDefaultsCreate(),
		DescribeIndexChild(OperationCreate, "quic-initial-rule", index, "defaults", defaultsName),
	)
}

// NewQUICInitialRuleDefaultsUpdate creates an operation to update a QUIC initial rule in defaults.
// QUIC initial rules are only available in HAProxy DataPlane API v3.1+.
func NewQUICInitialRuleDefaultsUpdate(defaultsName string, rule *models.QUICInitialRule, index int) Operation {
	return NewIndexChildOp(
		OperationUpdate,
		"quic_initial_rule",
		PriorityQUICInitialRule,
		defaultsName,
		index,
		rule,
		IdentityQUICInitialRule,
		executors.QUICInitialRuleDefaultsUpdate(),
		DescribeIndexChild(OperationUpdate, "quic-initial-rule", index, "defaults", defaultsName),
	)
}

// NewQUICInitialRuleDefaultsDelete creates an operation to delete a QUIC initial rule from defaults.
// QUIC initial rules are only available in HAProxy DataPlane API v3.1+.
func NewQUICInitialRuleDefaultsDelete(defaultsName string, rule *models.QUICInitialRule, index int) Operation {
	return NewIndexChildOp(
		OperationDelete,
		"quic_initial_rule",
		PriorityQUICInitialRule,
		defaultsName,
		index,
		rule,
		NilQUICInitialRule,
		executors.QUICInitialRuleDefaultsDelete(),
		DescribeIndexChild(OperationDelete, "quic-initial-rule", index, "defaults", defaultsName),
	)
}
