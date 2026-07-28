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

//go:build e2e

package e2e

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"testing"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/e2e-framework/pkg/envconf"
	"sigs.k8s.io/e2e-framework/pkg/features"

	hapticclient "gitlab.com/haproxy-haptic/haptic/pkg/generated/clientset/versioned"
)

// The chart renders one HAProxyTemplateConfig per enabled template library plus
// one for the operator's own config, and the controller merges the set in
// CRD_NAME order (ADR-0014). These tests pin the two halves of that contract
// against a live install: that the chart really did emit a set, and that the
// controller really is merging exactly that set — the invariant chart unit tests
// can only assert one side of.

// TestConfigSplit_ControllerMergesTheRenderedSet asserts that every config the
// chart rendered is named in the controller's CRD_NAME, in the same order, with
// the operator's own config last.
//
// A mismatch here is the failure mode the split introduces and the one unit
// tests cannot see: the chart emits the objects and the deployment builds the
// name list from two separate template helpers, so a library added to one and
// not the other would leave the controller silently ignoring a library's
// snippets — rendering a config that is valid but missing features.
func TestConfigSplit_ControllerMergesTheRenderedSet(t *testing.T) {
	feature := features.New("controller merges exactly the configs the chart rendered").
		Assess("CRD_NAME lists every rendered config, in order, operator last", func(ctx context.Context, t *testing.T, cfg *envconf.Config) context.Context {
			client, err := cfg.NewClient()
			if err != nil {
				t.Fatalf("new client: %v", err)
			}

			hc, err := hapticclient.NewForConfig(client.RESTConfig())
			if err != nil {
				t.Fatalf("build haptic clientset: %v", err)
			}

			list, err := hc.HaproxyTemplateICV1alpha1().
				HAProxyTemplateConfigs(ControllerNamespace).
				List(ctx, metav1.ListOptions{})
			if err != nil {
				t.Fatalf("listing HAProxyTemplateConfigs in %s: %v", ControllerNamespace, err)
			}

			rendered := make([]string, 0, len(list.Items))
			for i := range list.Items {
				rendered = append(rendered, list.Items[i].Name)
			}
			if len(rendered) < 2 {
				t.Fatalf("expected the chart to render a SET of configs, got %v — "+
					"the split is not in effect and the merge is untested", rendered)
			}

			merged, err := controllerCRDNames(ctx)
			if err != nil {
				t.Fatalf("reading CRD_NAME from the controller Deployment: %v", err)
			}

			// Same members, regardless of List's ordering.
			slices.Sort(rendered)
			sortedMerged := slices.Clone(merged)
			slices.Sort(sortedMerged)
			if !slices.Equal(rendered, sortedMerged) {
				t.Fatalf("the controller does not merge what the chart rendered.\n"+
					"  rendered: %v\n  CRD_NAME: %v\n"+
					"A library present in one and not the other is silently dropped from the effective config.",
					rendered, sortedMerged)
			}

			// Order carries precedence: the operator's own config is last so it
			// wins over every library.
			operator := merged[len(merged)-1]
			if strings.Contains(operator, "-") && operator != HAProxyConfigName {
				t.Fatalf("CRD_NAME ends with %q, expected the operator config %q last so it overrides every library: %v",
					operator, HAProxyConfigName, merged)
			}
			t.Logf("controller merges %d configs in order: %v", len(merged), merged)
			return ctx
		})

	testEnv.Test(t, feature.Feature())
}

// TestConfigSplit_LibraryConfigsAreIncompleteAlone documents the reason the CRD
// no longer marks four spec fields required, by proving the state it enables: a
// library config carries template content and nothing else.
//
// If a library object ever gained a podSelector or haproxyConfig, the merge
// order would start deciding fleet identity, so this is worth pinning.
func TestConfigSplit_LibraryConfigsAreIncompleteAlone(t *testing.T) {
	feature := features.New("a template-library config carries only template content").
		Assess("library configs declare no podSelector and no credentialsSecretRef", func(ctx context.Context, t *testing.T, cfg *envconf.Config) context.Context {
			client, err := cfg.NewClient()
			if err != nil {
				t.Fatalf("new client: %v", err)
			}

			hc, err := hapticclient.NewForConfig(client.RESTConfig())
			if err != nil {
				t.Fatalf("build haptic clientset: %v", err)
			}

			list, err := hc.HaproxyTemplateICV1alpha1().
				HAProxyTemplateConfigs(ControllerNamespace).
				List(ctx, metav1.ListOptions{})
			if err != nil {
				t.Fatalf("listing HAProxyTemplateConfigs in %s: %v", ControllerNamespace, err)
			}

			checked := 0
			for i := range list.Items {
				item := &list.Items[i]
				if item.Name == HAProxyConfigName {
					continue // the operator's own config; it carries the identity
				}
				checked++
				if len(item.Spec.PodSelector.MatchLabels) != 0 {
					t.Errorf("library config %q declares a podSelector (%v) — fleet identity belongs only to the operator config",
						item.Name, item.Spec.PodSelector.MatchLabels)
				}
				if item.Spec.CredentialsSecretRef.Name != "" {
					t.Errorf("library config %q declares credentialsSecretRef %q — credentials belong only to the operator config",
						item.Name, item.Spec.CredentialsSecretRef.Name)
				}
			}
			if checked == 0 {
				t.Fatal("no library configs found — the split is not in effect")
			}
			t.Logf("checked %d library configs", checked)
			return ctx
		})

	testEnv.Test(t, feature.Feature())
}

// controllerCRDNames reads the ordered merge list the chart handed the
// controller, from the CRD_NAME environment variable on its Deployment.
func controllerCRDNames(ctx context.Context) ([]string, error) {
	// No label selector: the controller Deployment's own metadata.labels carry
	// no component label (only its selector and pod template do), so the env
	// var is the identifying feature.
	raw, err := kubectlJSON(ctx, "get", "deployment", "-n", ControllerNamespace, "-o", "json")
	if err != nil {
		return nil, err
	}

	var list struct {
		Items []struct {
			Spec struct {
				Template struct {
					Spec struct {
						Containers []struct {
							Env []struct {
								Name  string `json:"name"`
								Value string `json:"value"`
							} `json:"env"`
						} `json:"containers"`
					} `json:"spec"`
				} `json:"template"`
			} `json:"spec"`
		} `json:"items"`
	}
	if err := json.Unmarshal(raw, &list); err != nil {
		return nil, fmt.Errorf("decoding deployment list: %w", err)
	}

	for _, item := range list.Items {
		for _, container := range item.Spec.Template.Spec.Containers {
			for _, env := range container.Env {
				if env.Name == "CRD_NAME" {
					return strings.Split(env.Value, ","), nil
				}
			}
		}
	}
	return nil, fmt.Errorf("no CRD_NAME env var on any controller Deployment in %s", ControllerNamespace)
}
