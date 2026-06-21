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

// backendSwitchingRuleIdentifier extracts the name identifier from a BackendSwitchingRule model.
func backendSwitchingRuleIdentifier(rule *models.BackendSwitchingRule) string {
	if rule != nil {
		return rule.Name
	}
	return ""
}

// serverSwitchingRuleIdentifier extracts the target server identifier from a ServerSwitchingRule model.
func serverSwitchingRuleIdentifier(rule *models.ServerSwitchingRule) string { return rule.TargetServer }

// CRUD builders for backend and server switching rules.
var (
	BackendSwitchingRuleFrontendOps = NewIndexChildCRUD[*models.BackendSwitchingRule]("backend_switching_rule", "backend switching rule", "frontend", backendSwitchingRuleIdentifier)
	ServerSwitchingRuleBackendOps   = NewIndexChildCRUD[*models.ServerSwitchingRule]("server_switching_rule", "server switching rule", "backend", serverSwitchingRuleIdentifier)
)
