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
		Port:                      spec.Dataplane.Port,
		MinDeploymentInterval:     spec.Dataplane.MinDeploymentInterval,
		DriftPreventionInterval:   spec.Dataplane.DriftPreventionInterval,
		DeploymentTimeout:         spec.Dataplane.DeploymentTimeout,
		MapsDir:                   spec.Dataplane.MapsDir,
		SSLCertsDir:               spec.Dataplane.SSLCertsDir,
		GeneralStorageDir:         spec.Dataplane.GeneralStorageDir,
		ConfigFile:                spec.Dataplane.ConfigFile,
		ConfigPublishInterval:     spec.Dataplane.ConfigPublishInterval,
		ReloadVerificationTimeout: spec.Dataplane.ReloadVerificationTimeout,
		SyncTimeout:               spec.Dataplane.SyncTimeout,
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
			APIVersions:             crdRes.APIVersions,
			Optional:                crdRes.Optional,
			Resources:               crdRes.Resources,
			EnableValidationWebhook: crdRes.EnableValidationWebhook,
			IndexBy:                 crdRes.IndexBy,
			LabelSelector:           labelSelectorMap,
			FieldSelector:           crdRes.FieldSelector,
			Store:                   crdRes.Store,
			DebounceInterval:        crdRes.DebounceInterval,
		}
	}

	// Convert template snippets
	templateSnippets := make(map[string]config.TemplateSnippet)
	for name, crdSnippet := range spec.TemplateSnippets {
		templateSnippets[name] = config.TemplateSnippet{
			Name:     name, // Name comes from map key
			Template: crdSnippet.Template,
			Requires: crdSnippet.Requires,
		}
	}

	// Convert maps
	mapFiles := make(map[string]config.MapFile)
	for name, crdMap := range spec.Maps {
		mapFiles[name] = config.MapFile{
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
			ReloadOnPush:   crdFile.ReloadOnPush,
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

	// Convert k8s resources
	k8sResources := make(map[string]config.K8sResource)
	for name, crdRes := range spec.K8sResources {
		k8sResources[name] = config.K8sResource{
			Template:       crdRes.Template,
			PostProcessing: convertPostProcessors(crdRes.PostProcessing),
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
		// Unmarshal runtime.RawExtension JSON to map[string]any
		var extraContext map[string]any
		if err := json.Unmarshal(spec.TemplatingSettings.ExtraContext.Raw, &extraContext); err != nil {
			return nil, fmt.Errorf("unmarshalling templating_settings.extra_context: %w", err)
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
		Validators:                   convertValidators(spec.Validators),
		TemplateSnippets:             templateSnippets,
		Maps:                         mapFiles,
		Files:                        files,
		SSLCertificates:              sslCertificates,
		K8sResources:                 k8sResources,
		HAProxyConfig:                haproxyConfig,
		ValidationTests:              validationTests,
		MigrationCoverage:            convertMigrationCoverage(spec.MigrationCoverage),
	}

	return cfg, nil
}

// convertMigrationCoverage copies the CRD migration-coverage declarations
// into the internal core-config representation. The shapes are identical;
// the copy is defensive (slices and maps are re-allocated) so later CRD
// mutations can't alias into the parsed config.
func convertMigrationCoverage(crdCoverage []v1alpha1.MigrationCoverageSource) []config.MigrationCoverageSource {
	if len(crdCoverage) == 0 {
		return nil
	}
	out := make([]config.MigrationCoverageSource, 0, len(crdCoverage))
	for i := range crdCoverage {
		src := &crdCoverage[i]
		entry := config.MigrationCoverageSource{
			Source: src.Source,
			Detect: config.MigrationDetect{
				IngressClasses:     append([]string(nil), src.Detect.IngressClasses...),
				AnnotationPrefixes: append([]string(nil), src.Detect.AnnotationPrefixes...),
			},
		}
		if len(src.Annotations) > 0 {
			entry.Annotations = make(map[string]config.AnnotationCoverage, len(src.Annotations))
			for key, ann := range src.Annotations {
				entry.Annotations[key] = config.AnnotationCoverage{
					Status: ann.Status,
					Note:   ann.Note,
					Doc:    ann.Doc,
				}
			}
		}
		out = append(out, entry)
	}
	return out
}

// convertValidators copies the CRD validators array into the
// internal core-config representation. The shapes are nearly identical;
// the translation dereferences the optional pointer fields (TimeoutMs,
// MaxConnections) into non-pointer int32 fields (zero defaults to the
// pluggablevalidator package defaults downstream) and copies the Files
// glob slice defensively.
func convertValidators(crdValidators []v1alpha1.ValidatorConfig) []config.ValidatorConfig {
	if len(crdValidators) == 0 {
		return nil
	}
	out := make([]config.ValidatorConfig, 0, len(crdValidators))
	for _, v := range crdValidators {
		entry := config.ValidatorConfig{
			Name:       v.Name,
			SocketPath: v.SocketPath,
			Files:      append([]string(nil), v.Files...),
			DataFiles:  append([]string(nil), v.DataFiles...),
		}
		if v.TimeoutMs != nil {
			entry.TimeoutMs = *v.TimeoutMs
		}
		if v.MaxConnections != nil {
			entry.MaxConnections = *v.MaxConnections
		}
		out = append(out, entry)
	}
	return out
}

// convertValidationTests converts CRD validation tests to internal config format.
// This function exists to properly handle the map iteration without triggering
// the rangeValCopy linter warning.
func convertValidationTests(crdTests map[string]v1alpha1.ValidationTest) (map[string]config.ValidationTest, error) {
	validationTests := make(map[string]config.ValidationTest, len(crdTests))

	for testName := range crdTests {
		crdTest := crdTests[testName]
		testConfig := config.ValidationTest{
			Description:       crdTest.Description,
			Fixtures:          convertFixtures(crdTest.Fixtures),
			HTTPFixtures:      convertHTTPFixtures(crdTest.HTTPResources),
			CurrentConfig:     crdTest.CurrentConfig,
			CurrentFiles:      crdTest.CurrentFiles,
			MinHAProxyVersion: crdTest.MinHAProxyVersion,
			Requires:          crdTest.Requires,
			RequiresFields:    crdTest.RequiresFields,
			Assertions:        convertAssertions(crdTest.Assertions),
		}
		// Parse test-specific extraContext if present
		if len(crdTest.ExtraContext.Raw) > 0 {
			var testExtraContext map[string]any
			if err := json.Unmarshal(crdTest.ExtraContext.Raw, &testExtraContext); err != nil {
				return nil, fmt.Errorf("unmarshalling validation_tests[%s].extra_context: %w", testName, err)
			}
			testConfig.ExtraContext = testExtraContext
		}
		validationTests[testName] = testConfig
	}

	return validationTests, nil
}

// convertFixtures converts CRD fixtures to internal config format.
// This converts from map[string][]runtime.RawExtension to map[string][]any.
func convertFixtures(crdFixtures map[string][]runtime.RawExtension) map[string][]any {
	fixtures := make(map[string][]any)
	for resourceType, resources := range crdFixtures {
		interfaceSlice := make([]any, len(resources))
		for i, rawExt := range resources {
			// Parse RawExtension.Raw ([]byte) into unstructured object
			obj := &unstructured.Unstructured{}
			if err := json.Unmarshal(rawExt.Raw, &obj.Object); err != nil {
				// If parsing fails, use empty object to avoid breaking fixture processing
				// The error will be caught during test execution
				obj.Object = make(map[string]any)
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
	for pair := range strings.SplitSeq(selector, ",") {
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
