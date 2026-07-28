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
	"errors"
	"strings"
	"testing"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"

	"gitlab.com/haproxy-haptic/haptic/pkg/apis/haproxytemplate/v1alpha1"
	coreconfig "gitlab.com/haproxy-haptic/haptic/pkg/core/config"
)

type fakeLister struct {
	items []unstructured.Unstructured
	err   error
	// selector records what the caller asked for, so a test can prove the
	// config's selector actually reaches the API call.
	selector string
}

func (f *fakeLister) ListResources(_ context.Context, _ schema.GroupVersionResource, labelSelector string) (*unstructured.UnstructuredList, error) {
	f.selector = labelSelector
	if f.err != nil {
		return nil, f.err
	}
	return &unstructured.UnstructuredList{Items: f.items}, nil
}

func testsObject(name string, testNames ...string) unstructured.Unstructured {
	tests := map[string]any{}
	for _, tn := range testNames {
		tests[tn] = map[string]any{
			"assertions": []any{map[string]any{"type": "haproxy_valid"}},
		}
	}
	return unstructured.Unstructured{Object: map[string]any{
		"apiVersion": "haproxy-haptic.org/v1alpha1",
		"kind":       "HAProxyValidationTests",
		"metadata":   map[string]any{"name": name, "namespace": "haptic"},
		"spec":       map[string]any{"validationTests": tests},
	}}
}

// crdWith builds the config object the union reads its inline tests and
// selector from.
func crdWith(selector *metav1.LabelSelector, inline map[string]v1alpha1.ValidationTest) *v1alpha1.HAProxyTemplateConfig {
	return &v1alpha1.HAProxyTemplateConfig{Spec: v1alpha1.HAProxyTemplateConfigSpec{
		ValidationTests:         inline,
		ValidationTestsSelector: selector,
	}}
}

func everything() *metav1.LabelSelector { return &metav1.LabelSelector{} }

func inlineTest(name string) map[string]v1alpha1.ValidationTest {
	return map[string]v1alpha1.ValidationTest{name: {
		Assertions: []v1alpha1.ValidationAssertion{{Type: "haproxy_valid"}},
	}}
}

func TestUnionDiscoveredValidationTests_NilSelector(t *testing.T) {
	t.Run("nil selector discovers nothing and leaves inline tests alone", func(t *testing.T) {
		cfg := &coreconfig.Config{}
		lister := &fakeLister{items: []unstructured.Unstructured{testsObject("extra", "discovered")}}

		if err := unionDiscoveredValidationTests(context.Background(), lister, cfg, crdWith(nil, inlineTest("inline")), nil); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if _, found := cfg.ValidationTests["discovered"]; found {
			t.Fatal("a nil selector must select nothing")
		}
		if len(cfg.ValidationTests) != 1 {
			t.Fatalf("inline tests changed: %v", cfg.ValidationTests)
		}
	})
}

func TestUnionDiscoveredValidationTests_Discovery(t *testing.T) {
	t.Run("discovered tests join the inline ones", func(t *testing.T) {
		cfg := &coreconfig.Config{}
		lister := &fakeLister{items: []unstructured.Unstructured{
			testsObject("a", "from-a"),
			testsObject("b", "from-b"),
		}}

		if err := unionDiscoveredValidationTests(context.Background(), lister, cfg, crdWith(everything(), inlineTest("inline")), nil); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		for _, want := range []string{"inline", "from-a", "from-b"} {
			if _, found := cfg.ValidationTests[want]; !found {
				t.Fatalf("%q missing from union: %v", want, cfg.ValidationTests)
			}
		}
	})

	// The whole point of the design: an empty suite is an unconditional pass, so
	// "could not look" must never arrive as "found nothing".
	t.Run("a list failure is an error, never an empty suite", func(t *testing.T) {
		cfg := &coreconfig.Config{}
		lister := &fakeLister{err: errors.New("forbidden: cannot list haproxyvalidationtests")}

		err := unionDiscoveredValidationTests(context.Background(), lister, cfg, crdWith(everything(), inlineTest("inline")), nil)
		if err == nil {
			t.Fatal("a failed List must fail the load; an empty suite passes unconditionally three layers down")
		}
		if !strings.Contains(err.Error(), "forbidden") {
			t.Fatalf("error should surface the cause, got: %v", err)
		}
	})

	t.Run("a duplicate test name across objects is an error", func(t *testing.T) {
		cfg := &coreconfig.Config{}
		lister := &fakeLister{items: []unstructured.Unstructured{testsObject("extra", "clash")}}

		err := unionDiscoveredValidationTests(context.Background(), lister, cfg, crdWith(everything(), inlineTest("clash")), nil)
		if err == nil {
			t.Fatal("expected a collision error")
		}
		if !strings.Contains(err.Error(), "HAProxyValidationTests/extra") {
			t.Fatalf("error must name the colliding object, got: %v", err)
		}
	})
}

func TestUnionDiscoveredValidationTests_Selector(t *testing.T) {
	t.Run("the config's selector reaches the API call", func(t *testing.T) {
		cfg := &coreconfig.Config{}
		lister := &fakeLister{}
		selector := &metav1.LabelSelector{MatchLabels: map[string]string{"app.kubernetes.io/instance": "haptic"}}

		if err := unionDiscoveredValidationTests(context.Background(), lister, cfg, crdWith(selector, nil), nil); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if !strings.Contains(lister.selector, "app.kubernetes.io/instance=haptic") {
			t.Fatalf("selector not passed through, got %q", lister.selector)
		}
	})
}

func TestEnforceRequireValidationTests(t *testing.T) {
	tests := []struct {
		name     string
		required bool
		tests    map[string]coreconfig.ValidationTest
		wantErr  bool
	}{
		{name: "not required and none present is fine", required: false, wantErr: false},
		{name: "required and present is fine", required: true,
			tests: map[string]coreconfig.ValidationTest{"t": {}}, wantErr: false},
		{name: "required and none present is refused", required: true, wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := enforceRequireValidationTests(&coreconfig.Config{ValidationTests: tt.tests}, tt.required)
			if tt.wantErr && err == nil {
				t.Fatal("expected a refusal: a config that runs zero tests must not load silently")
			}
			if !tt.wantErr && err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
		})
	}
}
