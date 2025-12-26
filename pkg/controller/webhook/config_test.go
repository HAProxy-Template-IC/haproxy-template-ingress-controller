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
	"testing"

	"github.com/stretchr/testify/assert"
	admissionv1 "k8s.io/api/admissionregistration/v1"

	"gitlab.com/haproxy-haptic/haptic/pkg/core/config"
	webhooklib "gitlab.com/haproxy-haptic/haptic/pkg/webhook"
)

func TestExtractWebhookRules(t *testing.T) {
	tests := []struct {
		name          string
		config        *config.Config
		expectedRules int
		checkFirst    func(*testing.T, []webhooklib.WebhookRule)
	}{
		{
			name: "no webhook enabled",
			config: &config.Config{
				WatchedResources: map[string]config.WatchedResource{
					"services": {
						APIVersion:              "v1",
						Resources:               "services",
						EnableValidationWebhook: false,
					},
				},
			},
			expectedRules: 0,
		},
		{
			name: "single webhook enabled",
			config: &config.Config{
				WatchedResources: map[string]config.WatchedResource{
					"ingresses": {
						APIVersion:              "networking.k8s.io/v1",
						Resources:               "ingresses",
						EnableValidationWebhook: true,
					},
				},
			},
			expectedRules: 1,
			checkFirst: func(t *testing.T, rules []webhooklib.WebhookRule) {
				t.Helper()
				assert.Equal(t, []string{"networking.k8s.io"}, rules[0].APIGroups)
				assert.Equal(t, []string{"v1"}, rules[0].APIVersions)
				assert.Equal(t, []string{"ingresses"}, rules[0].Resources)
				// Kind is now resolved at runtime via RESTMapper, not stored in config
				assert.Equal(t, []admissionv1.OperationType{
					admissionv1.Create,
					admissionv1.Update,
				}, rules[0].Operations)
			},
		},
		{
			name: "core API group",
			config: &config.Config{
				WatchedResources: map[string]config.WatchedResource{
					"configmaps": {
						APIVersion:              "v1",
						Resources:               "configmaps",
						EnableValidationWebhook: true,
					},
				},
			},
			expectedRules: 1,
			checkFirst: func(t *testing.T, rules []webhooklib.WebhookRule) {
				t.Helper()
				assert.Equal(t, []string{""}, rules[0].APIGroups)
				assert.Equal(t, []string{"v1"}, rules[0].APIVersions)
				assert.Equal(t, []string{"configmaps"}, rules[0].Resources)
				// Kind is now resolved at runtime via RESTMapper, not stored in config
			},
		},
		{
			name: "multiple resources mixed",
			config: &config.Config{
				WatchedResources: map[string]config.WatchedResource{
					"ingresses": {
						APIVersion:              "networking.k8s.io/v1",
						Resources:               "ingresses",
						EnableValidationWebhook: true,
					},
					"services": {
						APIVersion:              "v1",
						Resources:               "services",
						EnableValidationWebhook: false,
					},
					"secrets": {
						APIVersion:              "v1",
						Resources:               "secrets",
						EnableValidationWebhook: true,
					},
				},
			},
			expectedRules: 2,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rules := ExtractWebhookRules(tt.config)

			assert.Len(t, rules, tt.expectedRules)

			if tt.checkFirst != nil && len(rules) > 0 {
				tt.checkFirst(t, rules)
			}
		})
	}
}

func TestParseAPIVersion(t *testing.T) {
	tests := []struct {
		name            string
		apiVersion      string
		expectedGroup   string
		expectedVersion string
	}{
		{
			name:            "core API group",
			apiVersion:      "v1",
			expectedGroup:   "",
			expectedVersion: "v1",
		},
		{
			name:            "networking API group",
			apiVersion:      "networking.k8s.io/v1",
			expectedGroup:   "networking.k8s.io",
			expectedVersion: "v1",
		},
		{
			name:            "apps API group",
			apiVersion:      "apps/v1",
			expectedGroup:   "apps",
			expectedVersion: "v1",
		},
		{
			name:            "discovery API group",
			apiVersion:      "discovery.k8s.io/v1",
			expectedGroup:   "discovery.k8s.io",
			expectedVersion: "v1",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			group, version := parseAPIVersion(tt.apiVersion)

			assert.Equal(t, tt.expectedGroup, group)
			assert.Equal(t, tt.expectedVersion, version)
		})
	}
}

func TestHasWebhookEnabled(t *testing.T) {
	tests := []struct {
		name     string
		config   *config.Config
		expected bool
	}{
		{
			name: "no webhooks enabled",
			config: &config.Config{
				WatchedResources: map[string]config.WatchedResource{
					"services": {
						EnableValidationWebhook: false,
					},
				},
			},
			expected: false,
		},
		{
			name: "one webhook enabled",
			config: &config.Config{
				WatchedResources: map[string]config.WatchedResource{
					"ingresses": {
						EnableValidationWebhook: true,
					},
					"services": {
						EnableValidationWebhook: false,
					},
				},
			},
			expected: true,
		},
		{
			name: "empty config",
			config: &config.Config{
				WatchedResources: map[string]config.WatchedResource{},
			},
			expected: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := HasWebhookEnabled(tt.config)
			assert.Equal(t, tt.expected, result)
		})
	}
}
