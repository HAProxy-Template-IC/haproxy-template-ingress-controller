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

package webhook

import (
	admissionv1 "k8s.io/api/admissionregistration/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"

	"gitlab.com/haproxy-haptic/haptic/pkg/core/config"
)

// WebhookRule specifies which resources a webhook should intercept.
type WebhookRule struct {
	// APIGroups that this rule matches.
	// Example: ["networking.k8s.io"]
	APIGroups []string

	// APIVersions that this rule matches.
	// Example: ["v1"]
	APIVersions []string

	// Resources that this rule matches (plural, lowercase).
	// Example: ["ingresses"]
	Resources []string

	// Operations that this rule matches.
	// Default: ["CREATE", "UPDATE"]
	Operations []admissionv1.OperationType

	// Scope restricts the rule to cluster or namespace-scoped resources.
	// Default: "*" (all scopes)
	Scope *admissionv1.ScopeType
}

// ExtractWebhookRules extracts webhook rules from controller configuration.
//
// It iterates through watched resources and creates webhook rules for resources
// with enable_validation_webhook: true.
//
// Parameters:
//   - cfg: Controller configuration containing watched resources
//
// Returns:
//   - Slice of webhook rules for resources that have validation enabled
//   - Empty slice if no resources have webhook validation enabled
func ExtractWebhookRules(cfg *config.Config) []WebhookRule {
	rules := make([]WebhookRule, 0, len(cfg.WatchedResources))

	for _, resource := range cfg.WatchedResources {
		if !resource.EnableValidationWebhook {
			continue
		}

		// Parse API version into group and version. ParseGroupVersion handles
		// both the core "v1" form (empty group) and "group/version"; a
		// malformed value yields an empty GroupVersion, which still produces a
		// (harmlessly empty) rule rather than panicking.
		gv, _ := schema.ParseGroupVersion(resource.APIVersion)

		// Create webhook rule
		// Use resource.Resources which is the plural form (e.g., "ingresses", "services")
		// Kind is not needed here - the webhook server gets it from AdmissionRequest at runtime
		rule := WebhookRule{
			APIGroups:   []string{gv.Group},
			APIVersions: []string{gv.Version},
			Resources:   []string{resource.Resources},

			// Default to CREATE and UPDATE operations
			Operations: []admissionv1.OperationType{
				admissionv1.Create,
				admissionv1.Update,
			},
		}

		rules = append(rules, rule)
	}

	return rules
}
