//go:build e2e

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

package e2e

import (
	"context"
	"strings"
	"testing"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/client-go/dynamic"
	"sigs.k8s.io/e2e-framework/pkg/envconf"
	"sigs.k8s.io/e2e-framework/pkg/features"

	v1alpha1 "gitlab.com/haproxy-haptic/haptic/pkg/apis/haproxytemplate/v1alpha1"
	hapticclient "gitlab.com/haproxy-haptic/haptic/pkg/generated/clientset/versioned"
)

// TestHAProxyTemplateConfigCompleteness pins the apply-time gate that replaced
// config admission (ADR-0016): the CRD's CEL completeness rule. The
// per-object webhook is gone — it structurally could not judge a multi-object
// change set (it saw the mid-batch state and denied coupled edits) — and
// semantic validation moved to the pre-rollout preflight hook, the strict
// first render of each iteration, and the fail-closed load gate, all of which
// see the complete set. What stays at APPLY time is structural completeness
// for the one shape that is correctly judged alone: a standalone
// (non-spec.partial) config.
//
// CEL is enforced by the apiserver itself, which — unlike the webhook it
// replaces (failurePolicy: Ignore, whose fail-open windows this file's
// predecessor spent 60-second retry budgets probing around) — cannot be
// unreachable and cannot silently admit.
func TestHAProxyTemplateConfigCompleteness(t *testing.T) {
	feature := features.New("CRD CEL rule guards standalone config completeness at apply time").
		Assess("a complete standalone config is accepted", func(ctx context.Context, t *testing.T, cfg *envconf.Config) context.Context {
			hc, ns := hapticClientAndNamespace(ctx, t, cfg)

			crd := minimalValidHAProxyTemplateConfig(ns, "complete-standalone")
			created, err := hc.HaproxyTemplateICV1alpha1().HAProxyTemplateConfigs(ns).Create(ctx, crd, metav1.CreateOptions{})
			if err != nil {
				t.Fatalf("apiserver rejected a complete standalone config: %v", err)
			}
			t.Cleanup(func() {
				_ = hc.HaproxyTemplateICV1alpha1().HAProxyTemplateConfigs(ns).Delete(context.Background(), created.Name, metav1.DeleteOptions{})
			})
			return ctx
		}).
		Assess("an incomplete standalone config is rejected by the apiserver", func(ctx context.Context, t *testing.T, cfg *envconf.Config) context.Context {
			_, ns := hapticClientAndNamespace(ctx, t, cfg)

			// A library-shaped fragment WITHOUT spec.partial: snippets only, no
			// podSelector, no haproxyConfig — a hand-written config missing
			// half its fields. Before ADR-0016 this loaded silently and failed
			// only at the controller; now the apiserver refuses the write.
			//
			// Unstructured, not the typed client: the typed spec's value-typed
			// fields serialize as empty objects, which trip per-field schema
			// minima BEFORE the CEL rule runs — the rejection would then prove
			// nothing about the rule this test pins. The chart's shards are
			// YAML with the fields genuinely absent; this is that shape.
			dyn := dynamicClient(t, cfg)
			_, err := dyn.Resource(configGVR).Namespace(ns).Create(ctx, fragmentConfig(ns, "incomplete-standalone", false), metav1.CreateOptions{})
			if err == nil {
				_ = dyn.Resource(configGVR).Namespace(ns).Delete(context.Background(), "incomplete-standalone", metav1.DeleteOptions{})
				t.Fatal("apiserver accepted an incomplete standalone config — the CEL completeness rule is not enforcing")
			}
			if !strings.Contains(err.Error(), "spec.partial") {
				t.Fatalf("rejection did not come from the CEL completeness rule: %v", err)
			}
			return ctx
		}).
		Assess("the same fragment marked partial is accepted", func(ctx context.Context, t *testing.T, cfg *envconf.Config) context.Context {
			_, ns := hapticClientAndNamespace(ctx, t, cfg)

			dyn := dynamicClient(t, cfg)
			created, err := dyn.Resource(configGVR).Namespace(ns).Create(ctx, fragmentConfig(ns, "partial-fragment", true), metav1.CreateOptions{})
			if err != nil {
				t.Fatalf("apiserver rejected a spec.partial fragment: %v — every chart shard has this shape", err)
			}
			t.Cleanup(func() {
				_ = dyn.Resource(configGVR).Namespace(ns).Delete(context.Background(), created.GetName(), metav1.DeleteOptions{})
			})
			return ctx
		}).Feature()

	testEnv.Test(t, feature)
}

// fragmentConfig is a library-shaped shard as unstructured YAML-equivalent:
// only templateSnippets, every other spec field genuinely absent.
func fragmentConfig(namespace, name string, partial bool) *unstructured.Unstructured {
	spec := map[string]any{
		"templateSnippets": map[string]any{
			"orphan": map[string]any{"template": "# nothing"},
		},
	}
	if partial {
		spec["partial"] = true
	}
	return &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": "haproxy-haptic.org/v1alpha1",
		"kind":       "HAProxyTemplateConfig",
		"metadata":   map[string]any{"name": name, "namespace": namespace},
		"spec":       spec,
	}}
}

func dynamicClient(t *testing.T, cfg *envconf.Config) dynamic.Interface {
	t.Helper()
	dyn, err := dynamic.NewForConfig(cfg.Client().RESTConfig())
	if err != nil {
		t.Fatalf("dynamic client: %v", err)
	}
	return dyn
}

func hapticClientAndNamespace(ctx context.Context, t *testing.T, cfg *envconf.Config) (hapticclient.Interface, string) {
	t.Helper()
	client, err := cfg.NewClient()
	if err != nil {
		t.Fatalf("new client: %v", err)
	}
	ns := NamespaceForTest(ctx, t, client)
	DumpLogsOnFailure(t, ns)

	hc, err := hapticclient.NewForConfig(client.RESTConfig())
	if err != nil {
		t.Fatalf("build haptic clientset: %v", err)
	}
	return hc, ns
}

// minimalValidHAProxyTemplateConfig returns the smallest HAProxyTemplateConfig
// that satisfies the CRD's CEL completeness rule for standalone configs:
// podSelector, at least one watchedResources entry, and haproxyConfig. The
// canary is never reconciled (test namespace, non-matching name), so the
// values only need to satisfy the schema.
func minimalValidHAProxyTemplateConfig(namespace, name string) *v1alpha1.HAProxyTemplateConfig {
	return &v1alpha1.HAProxyTemplateConfig{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: namespace,
		},
		Spec: v1alpha1.HAProxyTemplateConfigSpec{
			CredentialsSecretRef: v1alpha1.SecretReference{
				Name: "canary-credentials-not-used",
			},
			PodSelector: v1alpha1.PodSelector{
				MatchLabels: map[string]string{
					"app": "haproxy-canary-not-used",
				},
			},
			WatchedResources: map[string]v1alpha1.WatchedResource{
				"namespaces": {
					APIVersion: "v1",
					Resources:  "namespaces",
				},
			},
			HAProxyConfig: v1alpha1.HAProxyConfig{
				// Frontend and backend MUST have distinct names — HAProxy 3.3+
				// rejects shared names.
				Template: `
global
  default-path origin /etc/haproxy
  daemon
defaults
  mode http
  timeout connect 5s
  timeout client 30s
  timeout server 30s
frontend dummy_fe
  bind :8081
  default_backend dummy_be
backend dummy_be
  server placeholder 127.0.0.1:1 disabled
`,
			},
		},
	}
}
