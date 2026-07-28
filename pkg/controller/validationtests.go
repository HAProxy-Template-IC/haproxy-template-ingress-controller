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

package controller

import (
	"context"
	"fmt"
	"log/slog"
	"sort"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"

	"gitlab.com/haproxy-haptic/haptic/pkg/apis/haproxytemplate/v1alpha1"
	"gitlab.com/haproxy-haptic/haptic/pkg/controller/conversion"
	coreconfig "gitlab.com/haproxy-haptic/haptic/pkg/core/config"
)

// validationTestLister lists HAProxyValidationTests objects. It is an interface
// so the union can be exercised without a cluster.
type validationTestLister interface {
	ListResources(ctx context.Context, gvr schema.GroupVersionResource, labelSelector string) (*unstructured.UnstructuredList, error)
}

// unionDiscoveredValidationTests folds every HAProxyValidationTests object the
// config selects into cfg.ValidationTests.
//
// It must run after the config is parsed and BEFORE it is structurally
// validated: the completeness checks, the "a test must assert something" check
// and the requires/requiresFields stripping all read cfg.ValidationTests, and a
// test that arrives after them is a test none of those ever saw.
//
// Every failure here is returned, never swallowed into an empty result. An
// empty suite is an unconditional pass three layers down, so a 403 or a
// malformed object that degraded to "found nothing" would present as a
// configuration that validated cleanly while running no tests at all.
func unionDiscoveredValidationTests(
	ctx context.Context,
	lister validationTestLister,
	cfg *coreconfig.Config,
	crd *v1alpha1.HAProxyTemplateConfig,
	logger *slog.Logger,
) error {
	var selector *metav1.LabelSelector
	inline := map[string]v1alpha1.ValidationTest{}
	if crd != nil {
		selector = crd.Spec.ValidationTestsSelector
		inline = crd.Spec.ValidationTests
	}

	// The union runs on the API types because every other consumer of it — the
	// offline validate command and the admission webhook — holds the spec, not
	// the converted config. Converting once at the end keeps one implementation.
	sources := []conversion.ValidationTestSource{{
		Origin: "HAProxyTemplateConfig spec.validationTests",
		Tests:  inline,
	}}

	// A nil selector selects nothing — distinct from an empty selector, which
	// selects every tests object in the namespace.
	if selector != nil {
		labelSelector, err := metav1.LabelSelectorAsSelector(selector)
		if err != nil {
			return fmt.Errorf("validationTestsSelector is not a usable label selector: %w", err)
		}

		list, err := lister.ListResources(ctx, validationTestsGVR, labelSelector.String())
		if err != nil {
			return fmt.Errorf("listing HAProxyValidationTests (selector %q): %w", labelSelector, err)
		}

		for _, item := range sortedByName(list.Items) {
			tests, err := validationTestsFromObject(&item)
			if err != nil {
				return fmt.Errorf("reading HAProxyValidationTests %s: %w", item.GetName(), err)
			}
			sources = append(sources, conversion.ValidationTestSource{
				Origin: "HAProxyValidationTests/" + item.GetName(),
				Tests:  tests,
			})
		}
	}

	union, err := conversion.UnionValidationTests(sources)
	if err != nil {
		return err
	}
	converted, err := conversion.ConvertSpec(&v1alpha1.HAProxyTemplateConfigSpec{ValidationTests: union})
	if err != nil {
		return fmt.Errorf("converting unioned validation tests: %w", err)
	}
	cfg.ValidationTests = converted.ValidationTests

	if logger != nil && len(sources) > 1 {
		logger.Info("Discovered validation tests",
			"objects", len(sources)-1, "tests_total", len(union))
	}
	return nil
}

// enforceRequireValidationTests turns "no tests" from silence into a refusal.
//
// Load time is the only place this can be checked: during a fresh install the
// configuration is admitted before any tests object exists, so the same check at
// admission would reject the install that is creating them.
func enforceRequireValidationTests(cfg *coreconfig.Config, required bool) error {
	if !required || len(cfg.ValidationTests) > 0 {
		return nil
	}
	return fmt.Errorf(
		"requireValidationTests is set but no validation tests were found: " +
			"an empty suite passes unconditionally, so this would otherwise load as a config " +
			"that validated cleanly without running anything — check spec.validationTestsSelector " +
			"matches the HAProxyValidationTests objects and that the controller may list them")
}

// validationTestsFromObject reads the tests out of a HAProxyValidationTests
// object and converts them to the internal shape the runner consumes, reusing
// the config converter so both sources are decoded identically.
func validationTestsFromObject(obj *unstructured.Unstructured) (map[string]v1alpha1.ValidationTest, error) {
	typed := &v1alpha1.HAProxyValidationTests{}
	if err := runtime.DefaultUnstructuredConverter.FromUnstructured(obj.Object, typed); err != nil {
		return nil, err
	}
	return typed.Spec.ValidationTests, nil
}

// sortedByName fixes the order tests objects are folded in, so the accumulated
// `_global` fixtures — and therefore the rendered config every test asserts on —
// do not depend on the order the API server happened to return.
func sortedByName(items []unstructured.Unstructured) []unstructured.Unstructured {
	out := make([]unstructured.Unstructured, len(items))
	copy(out, items)
	sort.Slice(out, func(i, j int) bool { return out[i].GetName() < out[j].GetName() })
	return out
}

// newValidationTestResolver binds discovery to a client for the live config
// path. The live gate must resolve the same suite the startup gate does, or a
// configuration change is judged against the inline tests alone and the
// discovered ones reappear only at the next restart.
func newValidationTestResolver(
	ctx context.Context,
	lister validationTestLister,
	logger *slog.Logger,
) func(*coreconfig.Config, *v1alpha1.HAProxyTemplateConfig) error {
	return func(cfg *coreconfig.Config, crd *v1alpha1.HAProxyTemplateConfig) error {
		if err := unionDiscoveredValidationTests(ctx, lister, cfg, crd, logger); err != nil {
			return err
		}
		return enforceRequireValidationTests(cfg, crd.Spec.RequireValidationTests)
	}
}
