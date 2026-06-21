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
)

// describeQUICInitialRule builds the description for the quic-initial-rule
// section. QUIC initial rules carry no model identifier, so the empty
// identifier makes DescribeTypedChild fall back to the "at index N" label.
func describeQUICInitialRule(parentType string) func(OperationType, *models.QUICInitialRule, string, int) func() string {
	return func(op OperationType, _ *models.QUICInitialRule, parentName string, index int) func() string {
		return DescribeTypedChild(op, "quic-initial-rule", "", fmt.Sprintf("at index %d", index), parentType, parentName)
	}
}

// CRUD builders for QUIC initial rules in frontends and defaults sections.
// QUIC initial rules are only available in HAProxy DataPlane API v3.1+.
var (
	quicInitialRuleFrontendOps = NewIndexChildCRUDWithDescriber[*models.QUICInitialRule]("quic_initial_rule", describeQUICInitialRule("frontend"))
	quicInitialRuleDefaultsOps = NewIndexChildCRUDWithDescriber[*models.QUICInitialRule]("quic_initial_rule", describeQUICInitialRule("defaults"))
)

// NewQUICInitialRuleFrontendCreate creates an operation to create a QUIC initial rule in a frontend.
func NewQUICInitialRuleFrontendCreate(frontendName string, rule *models.QUICInitialRule, index int) Operation {
	return quicInitialRuleFrontendOps.Create(frontendName, rule, index)
}

// NewQUICInitialRuleFrontendUpdate creates an operation to update a QUIC initial rule in a frontend.
func NewQUICInitialRuleFrontendUpdate(frontendName string, rule *models.QUICInitialRule, index int) Operation {
	return quicInitialRuleFrontendOps.Update(frontendName, rule, index)
}

// NewQUICInitialRuleFrontendDelete creates an operation to delete a QUIC initial rule from a frontend.
func NewQUICInitialRuleFrontendDelete(frontendName string, rule *models.QUICInitialRule, index int) Operation {
	return quicInitialRuleFrontendOps.Delete(frontendName, rule, index)
}

// NewQUICInitialRuleDefaultsCreate creates an operation to create a QUIC initial rule in defaults.
func NewQUICInitialRuleDefaultsCreate(defaultsName string, rule *models.QUICInitialRule, index int) Operation {
	return quicInitialRuleDefaultsOps.Create(defaultsName, rule, index)
}

// NewQUICInitialRuleDefaultsUpdate creates an operation to update a QUIC initial rule in defaults.
func NewQUICInitialRuleDefaultsUpdate(defaultsName string, rule *models.QUICInitialRule, index int) Operation {
	return quicInitialRuleDefaultsOps.Update(defaultsName, rule, index)
}

// NewQUICInitialRuleDefaultsDelete creates an operation to delete a QUIC initial rule from defaults.
func NewQUICInitialRuleDefaultsDelete(defaultsName string, rule *models.QUICInitialRule, index int) Operation {
	return quicInitialRuleDefaultsOps.Delete(defaultsName, rule, index)
}
