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
	"fmt"
	"strings"
	"testing"
	"time"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/e2e-framework/pkg/envconf"
	"sigs.k8s.io/e2e-framework/pkg/features"

	v1alpha1 "gitlab.com/haproxy-haptic/haptic/pkg/apis/haproxytemplate/v1alpha1"
	hapticclient "gitlab.com/haproxy-haptic/haptic/pkg/generated/clientset/versioned"
	"gitlab.com/haproxy-haptic/haptic/tests/testutil"
)

// TestHAProxyTemplateConfigAdmission_AcceptsValid is the happy-path for the
// CRD-level admission webhook. Applies a HAProxyTemplateConfig CRD with a
// known-good template into a test namespace and expects admission to allow
// it. The chart-installed controller doesn't reconcile this CRD (it watches
// a fixed name in the haptic namespace), so the test is parallel-safe with
// the rest of the e2e suite.
func TestHAProxyTemplateConfigAdmission_AcceptsValid(t *testing.T) {
	feature := features.New("HAProxyTemplateConfig admission accepts a valid CRD").
		Assess("apply succeeds; webhook does not reject", func(ctx context.Context, t *testing.T, cfg *envconf.Config) context.Context {
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

			crd := minimalValidHAProxyTemplateConfig(ns, "valid-canary")
			created, err := hc.HaproxyTemplateICV1alpha1().HAProxyTemplateConfigs(ns).Create(ctx, crd, metav1.CreateOptions{})
			if err != nil {
				t.Fatalf("admission rejected valid CRD: %v", err)
			}
			t.Cleanup(func() {
				_ = hc.HaproxyTemplateICV1alpha1().HAProxyTemplateConfigs(ns).Delete(context.Background(), created.Name, metav1.DeleteOptions{})
			})
			return ctx
		})

	testEnv.Test(t, feature.Feature())
}

// TestHAProxyTemplateConfigAdmission_RejectsInvalidHAProxySyntax verifies
// the webhook denies a CRD whose template renders to invalid HAProxy
// syntax. The error must reach the operator via the AdmissionResponse —
// that's the whole point of moving the gate upstream.
func TestHAProxyTemplateConfigAdmission_RejectsInvalidHAProxySyntax(t *testing.T) {
	feature := features.New("HAProxyTemplateConfig admission denies invalid HAProxy syntax").
		Assess("apply fails with a denial reason referencing the bad directive", func(ctx context.Context, t *testing.T, cfg *envconf.Config) context.Context {
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

			// Deliberately bad: `defraultserver` is not a valid HAProxy
			// directive. The webhook's strict ValidationService should
			// reject this at the syntax / schema / `haproxy -c` phase.
			crd := minimalValidHAProxyTemplateConfig(ns, "bad-syntax-canary")
			crd.Spec.HAProxyConfig.Template = `
global
  daemon
defaults
  defraultserver check
  mode http
`

			denial := createConfigExpectDenied(ctx, t, hc, crd)

			// The denial reason should include enough text to point an
			// operator at the typo. We accept either the directive name
			// itself or a parser-shaped error string ("unknown keyword",
			// "parsing", "unrecognized") — match conservatively so the
			// test doesn't pin a specific HAProxy version's exact phrasing.
			msg := denial.Error()
			matched := strings.Contains(msg, "defraultserver") ||
				strings.Contains(msg, "unknown keyword") ||
				strings.Contains(msg, "parsing") ||
				strings.Contains(msg, "unrecognized")
			if !matched {
				t.Fatalf("admission denial reason did not mention the bad directive or a parser error.\nfull error: %s", msg)
			}
			return ctx
		})

	testEnv.Test(t, feature.Feature())
}

// TestHAProxyTemplateConfigAdmission_RejectsFailingValidationTests verifies the
// webhook runs the config's embedded validationTests and DENIES a config whose
// tests fail — refusing it at apply so it never lands in etcd to crash-loop a
// future fresh controller pod. The template itself is valid HAProxy (so this
// exercises the validationTests gate specifically, not syntax/schema).
func TestHAProxyTemplateConfigAdmission_RejectsFailingValidationTests(t *testing.T) {
	feature := features.New("HAProxyTemplateConfig admission denies failing validationTests").
		Assess("apply is denied with a reason naming the failing test", func(ctx context.Context, t *testing.T, cfg *envconf.Config) context.Context {
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

			crd := minimalValidHAProxyTemplateConfig(ns, "failing-vt-canary")
			crd.Spec.ValidationTests = map[string]v1alpha1.ValidationTest{
				"test-gate-canary-must-fail": {
					Description: "deliberately failing test to exercise the admission validationTests gate",
					Fixtures:    map[string][]runtime.RawExtension{},
					Assertions: []v1alpha1.ValidationAssertion{{
						Type:        "contains",
						Target:      "haproxy.cfg",
						Pattern:     "HAPTIC_GATE_CANARY_NEVER_RENDERED",
						Description: "guaranteed to fail — pattern never appears in the render",
					}},
				},
			}

			denial := createConfigExpectDenied(ctx, t, hc, crd)
			msg := denial.Error()
			if !strings.Contains(msg, "validationTests") && !strings.Contains(msg, "test-gate-canary-must-fail") {
				t.Fatalf("admission denial reason did not reference the failing validationTest.\nfull error: %s", msg)
			}
			return ctx
		})

	testEnv.Test(t, feature.Feature())
}

// createConfigExpectDenied creates the (invalid) HAProxyTemplateConfig and
// returns the admission denial error, retrying through webhook fail-open
// windows. It fails the test only if denial never happens within the budget.
//
// The HAProxyTemplateConfig webhook rule carries failurePolicy: Ignore by
// design (openspec/specs/validating-webhook/spec.md, "Fixed Fail-Open Policy
// for Config Admission"): a down controller must never block operators from
// applying config fixes. The flip side is that ANY apiserver→webhook delivery
// failure silently ADMITS the create — issue #62 hit exactly that in a
// nightly run (the invalid CR was admitted; zero HAProxyTemplateConfig
// AdmissionReviews reached the controller for the whole suite). A single
// Create is therefore not a reliable probe of the deny path. This helper
// treats an admitted create as a fail-open window: it deletes the CR, waits
// until it is fully gone (the retry must not hit AlreadyExists, and the
// invalid config must not linger in etcd), then retries with backoff inside
// a bounded budget.
//
// The watched-resource webhook rules (Ingress etc.) use failurePolicy: Fail
// (charts/haptic/templates/validatingwebhookconfiguration.yaml), so
// NewIngressExpectDenied does NOT need this treatment — a delivery failure
// there surfaces as a Create error, never as a silent admit.
func createConfigExpectDenied(ctx context.Context, t *testing.T, hc hapticclient.Interface, crd *v1alpha1.HAProxyTemplateConfig) error {
	t.Helper()

	configs := hc.HaproxyTemplateICV1alpha1().HAProxyTemplateConfigs(crd.Namespace)

	// Final safety net: if the budget expires right after a fail-open admit
	// (or the in-loop delete itself failed), the invalid CR must not outlive
	// the test.
	t.Cleanup(func() {
		if err := configs.Delete(context.Background(), crd.Name, metav1.DeleteOptions{}); err != nil && !apierrors.IsNotFound(err) {
			t.Logf("cleanup HAProxyTemplateConfig %s/%s: %v (best-effort)", crd.Namespace, crd.Name, err)
		}
	})

	var denial error
	waitCfg := testutil.WaitConfig{
		InitialInterval: 500 * time.Millisecond,
		MaxInterval:     5 * time.Second,
		Timeout:         60 * time.Second,
		Multiplier:      2.0,
	}
	err := testutil.WaitForConditionWithDescription(ctx, waitCfg,
		fmt.Sprintf("admission webhook denies HAProxyTemplateConfig %s/%s", crd.Namespace, crd.Name),
		func(ctx context.Context) (bool, error) {
			_, createErr := configs.Create(ctx, crd, metav1.CreateOptions{})
			if createErr != nil {
				// Only a genuine admission rejection counts as the denial
				// this helper probes for. Webhook denials surface as
				// Forbidden/Invalid/BadRequest status errors; anything else
				// (transient apiserver blip, AlreadyExists from a cleanup
				// still settling) is retryable within the budget — treating
				// it as the denial would fail the substring assertions with
				// a misleading message.
				if apierrors.IsForbidden(createErr) || apierrors.IsInvalid(createErr) || apierrors.IsBadRequest(createErr) {
					denial = createErr
					return true, nil
				}
				return false, createErr
			}

			// Fail-open window: the apiserver admitted the CR without a
			// webhook verdict. Remove it and confirm it is gone before the
			// next attempt.
			if delErr := configs.Delete(ctx, crd.Name, metav1.DeleteOptions{}); delErr != nil && !apierrors.IsNotFound(delErr) {
				return false, fmt.Errorf("delete fail-open-admitted HAProxyTemplateConfig %s/%s: %w", crd.Namespace, crd.Name, delErr)
			}
			if goneErr := testutil.WaitForConditionWithDescription(ctx, testutil.FastWaitConfig(),
				fmt.Sprintf("fail-open-admitted HAProxyTemplateConfig %s/%s deleted", crd.Namespace, crd.Name),
				func(ctx context.Context) (bool, error) {
					_, getErr := configs.Get(ctx, crd.Name, metav1.GetOptions{})
					if apierrors.IsNotFound(getErr) {
						return true, nil
					}
					return false, getErr
				}); goneErr != nil {
				return false, goneErr
			}
			return false, fmt.Errorf("create was admitted (webhook fail-open window, failurePolicy=Ignore)")
		})
	if err != nil {
		t.Fatalf("admission never denied HAProxyTemplateConfig %s/%s within the fail-open retry budget: %v", crd.Namespace, crd.Name, err)
	}
	return denial
}

// minimalValidHAProxyTemplateConfig returns the smallest HAProxyTemplateConfig
// that passes K8s schema validation AND admission validation against a fresh
// test namespace's stores. The template renders to a config that has no
// Ingresses or other routing resources — `haproxy -c` accepts it as a
// minimal but legal config.
//
// All `+kubebuilder:validation:Required` fields on the CRD spec
// (CredentialsSecretRef, PodSelector with non-empty matchLabels,
// WatchedResources with at least one entry, HAProxyConfig.Template) are
// populated with placeholder values — the canary CRD never gets
// reconciled (it's in a test namespace with a non-matching name) so
// these values just need to satisfy the schema and the render+validate
// pipeline.
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
				// rejects shared names with "backend 'X' has the same name as
				// frontend 'X' declared at haproxy.cfg:N. This is no longer
				// supported as of 3.3. Please rename one or the other."
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
