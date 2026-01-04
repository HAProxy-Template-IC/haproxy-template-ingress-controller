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

package conversion

import (
	"encoding/json"
	"fmt"
	"strings"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"

	"gitlab.com/haproxy-haptic/haptic/pkg/apis/haproxytemplate/v1alpha1"
	"gitlab.com/haproxy-haptic/haptic/pkg/core/config"
)

// ConvertSpec converts a HAProxyTemplateConfig CRD Spec to internal config.Config format.
//
// This is a comprehensive converter that handles ALL fields from the CRD spec including:
//   - Production fields: PodSelector, Controller, Logging, Dataplane
//   - Template fields: HAProxyConfig, TemplateSnippets, Maps, Files, SSLCertificates
//   - Resource fields: WatchedResources, WatchedResourcesIgnoreFields
//   - Configuration fields: TemplatingSettings
//   - Test fields: ValidationTests (includes fixtures and assertions)
//
// The CRD spec field CredentialsSecretRef is intentionally excluded as it's handled
// separately by the credentials loader component.
//
// IMPORTANT: When adding or modifying fields in the CRD types (pkg/apis/haproxytemplate/v1alpha1/types.go),
// you MUST update this function to copy those fields. The CRD types have documentation comments
// pointing to this file as a reminder.
//
// Common mistake: Adding a field to the CRD but forgetting to copy it here, resulting in the field
// being silently ignored by the controller.
func ConvertSpec(spec *v1alpha1.HAProxyTemplateConfigSpec) (*config.Config, error) {
	// Convert pod selector
	podSelector := config.PodSelector{
		MatchLabels: spec.PodSelector.MatchLabels,
	}

	// Convert controller config
	// Handle pointer to bool for Enabled field
	leaderElectionEnabled := true // default
	if spec.Controller.LeaderElection.Enabled != nil {
		leaderElectionEnabled = *spec.Controller.LeaderElection.Enabled
	}

	// Apply default compression threshold when not set (Go zero value)
	// This ensures runtime behavior matches the CRD kubebuilder default annotation
	compressionThreshold := spec.Controller.ConfigPublishing.CompressionThreshold
	if compressionThreshold == 0 {
		compressionThreshold = config.DefaultCompressionThreshold
	}

	controllerConfig := config.ControllerConfig{
		LeaderElection: config.LeaderElectionConfig{
			Enabled:       leaderElectionEnabled,
			LeaseName:     spec.Controller.LeaderElection.LeaseName,
			LeaseDuration: spec.Controller.LeaderElection.LeaseDuration,
			RenewDeadline: spec.Controller.LeaderElection.RenewDeadline,
			RetryPeriod:   spec.Controller.LeaderElection.RetryPeriod,
		},
		ConfigPublishing: config.ConfigPublishingConfig{
			CompressionThreshold: compressionThreshold,
		},
	}

	// Convert logging config
	loggingConfig := config.LoggingConfig{
		Level: spec.Logging.Level,
	}

	// Convert dataplane config
	// Note: Scheme, InsecureSkipVerify, and Version are not in CRD spec.
	// These are internal Dataplane API client configuration fields set by defaults.
	dataplaneConfig := config.DataplaneConfig{
		Port:                    spec.Dataplane.Port,
		MinDeploymentInterval:   spec.Dataplane.MinDeploymentInterval,
		DriftPreventionInterval: spec.Dataplane.DriftPreventionInterval,
		DeploymentTimeout:       spec.Dataplane.DeploymentTimeout,
		MapsDir:                 spec.Dataplane.MapsDir,
		SSLCertsDir:             spec.Dataplane.SSLCertsDir,
		GeneralStorageDir:       spec.Dataplane.GeneralStorageDir,
		ConfigFile:              spec.Dataplane.ConfigFile,
	}

	// Convert watched resources
	watchedResources := make(map[string]config.WatchedResource)
	for name := range spec.WatchedResources {
		crdRes := spec.WatchedResources[name]
		// Parse label selector string into map
		// CRD uses string format "key1=value1,key2=value2"
		// Config uses map[string]string
		labelSelectorMap := parseLabelSelector(crdRes.LabelSelector)

		watchedResources[name] = config.WatchedResource{
			APIVersion:              crdRes.APIVersion,
			Resources:               crdRes.Resources,
			EnableValidationWebhook: crdRes.EnableValidationWebhook,
			IndexBy:                 crdRes.IndexBy,
			LabelSelector:           labelSelectorMap,
			FieldSelector:           crdRes.FieldSelector,
			Store:                   crdRes.Store,
		}
	}

	// Convert template snippets
	templateSnippets := make(map[string]config.TemplateSnippet)
	for name, crdSnippet := range spec.TemplateSnippets {
		templateSnippets[name] = config.TemplateSnippet{
			Name:     name, // Name comes from map key
			Template: crdSnippet.Template,
		}
	}

	// Convert maps
	maps := make(map[string]config.MapFile)
	for name, crdMap := range spec.Maps {
		maps[name] = config.MapFile{
			Template:       crdMap.Template,
			PostProcessing: convertPostProcessors(crdMap.PostProcessing),
		}
	}

	// Convert files
	files := make(map[string]config.GeneralFile)
	for name, crdFile := range spec.Files {
		files[name] = config.GeneralFile{
			Template:       crdFile.Template,
			PostProcessing: convertPostProcessors(crdFile.PostProcessing),
		}
	}

	// Convert SSL certificates
	sslCertificates := make(map[string]config.SSLCertificate)
	for name, crdCert := range spec.SSLCertificates {
		sslCertificates[name] = config.SSLCertificate{
			Template:       crdCert.Template,
			PostProcessing: convertPostProcessors(crdCert.PostProcessing),
		}
	}

	// Convert HAProxy config
	haproxyConfig := config.HAProxyConfig{
		Template:       spec.HAProxyConfig.Template,
		PostProcessing: convertPostProcessors(spec.HAProxyConfig.PostProcessing),
	}

	// Convert templating settings
	templatingSettings := config.TemplatingSettings{
		Engine: spec.TemplatingSettings.Engine, // Empty string defaults to "scriggo" at runtime
	}
	if len(spec.TemplatingSettings.ExtraContext.Raw) > 0 {
		// Unmarshal runtime.RawExtension JSON to map[string]interface{}
		var extraContext map[string]interface{}
		if err := json.Unmarshal(spec.TemplatingSettings.ExtraContext.Raw, &extraContext); err != nil {
			return nil, fmt.Errorf("failed to unmarshal templating_settings.extra_context: %w", err)
		}
		templatingSettings.ExtraContext = extraContext
	}

	// Convert validation tests
	// Note: Using convertValidationTests helper to avoid linter warning about
	// copying large struct (128 bytes) per iteration in range loop
	validationTests, err := convertValidationTests(spec.ValidationTests)
	if err != nil {
		return nil, err
	}

	// Construct final config
	cfg := &config.Config{
		PodSelector:                  podSelector,
		Controller:                   controllerConfig,
		Logging:                      loggingConfig,
		Dataplane:                    dataplaneConfig,
		TemplatingSettings:           templatingSettings,
		WatchedResourcesIgnoreFields: spec.WatchedResourcesIgnoreFields,
		WatchedResources:             watchedResources,
		TemplateSnippets:             templateSnippets,
		Maps:                         maps,
		Files:                        files,
		SSLCertificates:              sslCertificates,
		HAProxyConfig:                haproxyConfig,
		ValidationTests:              validationTests,
	}

	return cfg, nil
}

// convertValidationTests converts CRD validation tests to internal config format.
// This function exists to properly handle the map iteration without triggering
// the rangeValCopy linter warning.
func convertValidationTests(crdTests map[string]v1alpha1.ValidationTest) (map[string]config.ValidationTest, error) {
	validationTests := make(map[string]config.ValidationTest, len(crdTests))

	// Get sorted keys to ensure deterministic ordering
	testNames := make([]string, 0, len(crdTests))
	for testName := range crdTests {
		testNames = append(testNames, testName)
	}

	for _, testName := range testNames {
		crdTest := crdTests[testName]
		testConfig := config.ValidationTest{
			Description:   crdTest.Description,
			Fixtures:      convertFixtures(crdTest.Fixtures),
			HTTPFixtures:  convertHTTPFixtures(crdTest.HTTPResources),
			CurrentConfig: crdTest.CurrentConfig,
			Assertions:    convertAssertions(crdTest.Assertions),
		}
		// Parse test-specific extraContext if present
		if len(crdTest.ExtraContext.Raw) > 0 {
			var testExtraContext map[string]interface{}
			if err := json.Unmarshal(crdTest.ExtraContext.Raw, &testExtraContext); err != nil {
				return nil, fmt.Errorf("failed to unmarshal validation_tests[%s].extra_context: %w", testName, err)
			}
			testConfig.ExtraContext = testExtraContext
		}
		validationTests[testName] = testConfig
	}

	return validationTests, nil
}

// convertFixtures converts CRD fixtures to internal config format.
// This converts from map[string][]runtime.RawExtension to map[string][]interface{}.
func convertFixtures(crdFixtures map[string][]runtime.RawExtension) map[string][]interface{} {
	fixtures := make(map[string][]interface{})
	for resourceType, resources := range crdFixtures {
		interfaceSlice := make([]interface{}, len(resources))
		for i, rawExt := range resources {
			// Parse RawExtension.Raw ([]byte) into unstructured object
			obj := &unstructured.Unstructured{}
			if err := json.Unmarshal(rawExt.Raw, &obj.Object); err != nil {
				// If parsing fails, use empty object to avoid breaking fixture processing
				// The error will be caught during test execution
				obj.Object = make(map[string]interface{})
			}
			interfaceSlice[i] = obj.Object
		}
		fixtures[resourceType] = interfaceSlice
	}
	return fixtures
}

// convertPostProcessors converts CRD PostProcessorConfig to internal config format.
func convertPostProcessors(crdPostProcessors []v1alpha1.PostProcessorConfig) []config.PostProcessorConfig {
	if len(crdPostProcessors) == 0 {
		return nil
	}

	postProcessors := make([]config.PostProcessorConfig, len(crdPostProcessors))
	for i, pp := range crdPostProcessors {
		postProcessors[i] = config.PostProcessorConfig{
			Type:   pp.Type,
			Params: pp.Params,
		}
	}
	return postProcessors
}

// convertAssertions converts CRD assertion types to internal config format.
func convertAssertions(crdAssertions []v1alpha1.ValidationAssertion) []config.ValidationAssertion {
	assertions := make([]config.ValidationAssertion, len(crdAssertions))
	for i, a := range crdAssertions {
		assertions[i] = config.ValidationAssertion{
			Type:        a.Type,
			Description: a.Description,
			Target:      a.Target,
			Pattern:     a.Pattern,
			Expected:    a.Expected,
			JSONPath:    a.JSONPath,
			Patterns:    a.Patterns,
		}
	}
	return assertions
}

// convertHTTPFixtures converts CRD HTTP resource fixtures to internal config format.
func convertHTTPFixtures(crdHTTPFixtures []v1alpha1.HTTPResourceFixture) []config.HTTPResourceFixture {
	if len(crdHTTPFixtures) == 0 {
		return nil
	}

	httpFixtures := make([]config.HTTPResourceFixture, len(crdHTTPFixtures))
	for i, f := range crdHTTPFixtures {
		httpFixtures[i] = config.HTTPResourceFixture{
			URL:     f.URL,
			Content: f.Content,
		}
	}
	return httpFixtures
}

// parseLabelSelector parses a label selector string into a map.
//
// Kubernetes label selectors in string format use "key1=value1,key2=value2".
// This function converts that to the map format used by config.WatchedResource.
// Example: "app=nginx,env=prod" -> map[string]string{"app": "nginx", "env": "prod"}.
func parseLabelSelector(selector string) map[string]string {
	if selector == "" {
		return nil
	}

	result := make(map[string]string)

	// Split by comma to get individual label assignments
	for _, pair := range strings.Split(selector, ",") {
		pair = strings.TrimSpace(pair)
		if pair == "" {
			continue
		}

		// Split by equals to get key=value
		parts := strings.SplitN(pair, "=", 2)
		if len(parts) == 2 {
			key := strings.TrimSpace(parts[0])
			value := strings.TrimSpace(parts[1])
			if key != "" {
				result[key] = value
			}
		}
	}

	if len(result) == 0 {
		return nil
	}

	return result
}
