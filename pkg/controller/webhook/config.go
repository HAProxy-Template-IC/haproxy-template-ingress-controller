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
	"strings"

	admissionv1 "k8s.io/api/admissionregistration/v1"

	"haptic/pkg/core/config"
	"haptic/pkg/webhook"
)

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
func ExtractWebhookRules(cfg *config.Config) []webhook.WebhookRule {
	rules := make([]webhook.WebhookRule, 0, len(cfg.WatchedResources))

	for _, resource := range cfg.WatchedResources {
		if !resource.EnableValidationWebhook {
			continue
		}

		// Parse API version into group and version
		apiGroup, apiVersion := parseAPIVersion(resource.APIVersion)

		// Create webhook rule
		// Use resource.Resources which is the plural form (e.g., "ingresses", "services")
		// Kind is not needed here - the webhook server gets it from AdmissionRequest at runtime
		rule := webhook.WebhookRule{
			APIGroups:   []string{apiGroup},
			APIVersions: []string{apiVersion},
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

// parseAPIVersion splits an API version string into group and version.
//
// Examples:
//   - "v1" → ("", "v1")                          # Core API group
//   - "networking.k8s.io/v1" → ("networking.k8s.io", "v1")
//   - "apps/v1" → ("apps", "v1")
func parseAPIVersion(apiVersion string) (group, version string) {
	parts := strings.Split(apiVersion, "/")

	if len(parts) == 1 {
		// Core API group (e.g., "v1")
		return "", parts[0]
	}

	// Named API group (e.g., "networking.k8s.io/v1")
	return parts[0], parts[1]
}

// HasWebhookEnabled checks if any watched resources have webhook validation enabled.
func HasWebhookEnabled(cfg *config.Config) bool {
	for _, resource := range cfg.WatchedResources {
		if resource.EnableValidationWebhook {
			return true
		}
	}
	return false
}
