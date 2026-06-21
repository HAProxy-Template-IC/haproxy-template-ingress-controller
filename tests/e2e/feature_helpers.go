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
	"testing"

	"sigs.k8s.io/e2e-framework/klient"
	"sigs.k8s.io/e2e-framework/pkg/envconf"
	"sigs.k8s.io/e2e-framework/pkg/features"
)

// SimpleIngressTest captures the most common test shape: one Ingress
// pointing at a per-test echo-server, plus one or more behavioural
// assertions. Tests with this shape make up most of the suite —
// extracting the setup eliminates the boilerplate that was repeated
// across 30+ test files.
//
// For tests that need additional fixtures (TLS Secrets, custom
// backends, multiple Ingresses) the per-file setup pattern remains
// the right tool — those tests inline their setup so the
// orchestration is visible at the call site.
type SimpleIngressTest struct {
	// Description is the e2e-framework feature name, shown in
	// `go test -v` output. Keep it human-readable and short.
	Description string

	// Host is the request Host: header. Also used as the Ingress's
	// rule.host. Must be a valid DNS label after kebab-casing for
	// e2e-framework to slot it into the sub-test name.
	Host string

	// Path is the Ingress rule's path. Defaults to "/" when empty.
	Path string

	// PathType is the Ingress pathType ("Prefix" (default), "Exact",
	// or "ImplementationSpecific"). Use "ImplementationSpecific" for
	// the haproxy-ingress regex-path flavour (combined with the
	// haproxy-ingress.github.io/path-type annotation).
	PathType string

	// Annotations are applied verbatim to the Ingress. Pass nil for
	// the chart's default behaviour with no annotations.
	Annotations map[string]string

	// TLSSecretName, if set, populates Ingress.spec.tls[] with the
	// named Secret. The test fixture creates the secret in the
	// per-test namespace before the Ingress. The auto-generated TLS
	// Secret covers the test's Host.
	TLSSecretName string

	// PreSetup runs before the Ingress is created. Use it to deploy
	// fixtures the Ingress depends on — auth Secrets, ConfigMaps,
	// custom backends, alternate Services. Receives the per-test
	// namespace so fixtures land in the right scope and tear down
	// with the namespace.
	//
	// Skip for tests that just need an Ingress + echo-server
	// backend; the helper handles those by default.
	PreSetup func(ctx context.Context, t *testing.T, client klient.Client, namespace string)

	// Assess registers behavioural assertions against the deployed
	// Ingress. Each entry produces a `t.Run`-style sub-test under
	// the feature so failures are reported with their assertion
	// name. At least one assertion must be provided.
	Assess []SimpleIngressAssertion
}

// SimpleIngressAssertion is one behavioural check on the deployed
// Ingress. Multiple assertions per test are evaluated sequentially
// in declaration order — they share the same setup.
type SimpleIngressAssertion struct {
	// Name describes the assertion in the test output.
	Name string
	// Check runs the assertion. Receives the Host: the test's Ingress
	// is configured for so the assertion can probe via httpclient.
	Check func(t *testing.T, host string)
}

// RunSimpleIngressTest executes a single SimpleIngressTest against
// testEnv. Pass `t` from the outer Test* function. Internally creates
// a per-test namespace, deploys an echo-server, applies the Ingress,
// and runs each assertion as a feature `Assess` block.
func RunSimpleIngressTest(t *testing.T, sit SimpleIngressTest) {
	t.Helper()
	if len(sit.Assess) == 0 {
		t.Fatalf("RunSimpleIngressTest %q: at least one assertion required", sit.Description)
	}

	feature := features.New(sit.Description).
		Setup(func(ctx context.Context, t *testing.T, cfg *envconf.Config) context.Context {
			client, err := cfg.NewClient()
			if err != nil {
				t.Fatalf("new client: %v", err)
			}
			ns := NamespaceForTest(ctx, t, client)
			DumpLogsOnFailure(t, ns)
			backend := NewEchoServerBackend(ctx, t, client, ns)

			if sit.PreSetup != nil {
				sit.PreSetup(ctx, t, client, ns)
			}

			spec := IngressSpec{
				Name:           "echo",
				Host:           sit.Host,
				Path:           sit.Path,
				PathType:       sit.PathType,
				BackendService: backend.Service,
				BackendPort:    backend.Port,
				Annotations:    sit.Annotations,
				TLSSecretName:  sit.TLSSecretName,
			}
			if sit.TLSSecretName != "" {
				NewTLSSecret(ctx, t, client, ns, sit.TLSSecretName, []string{sit.Host})
			}
			NewIngress(ctx, t, client, ns, spec)
			return ctx
		})

	for _, a := range sit.Assess {
		a := a
		feature = feature.Assess(a.Name, func(ctx context.Context, t *testing.T, cfg *envconf.Config) context.Context {
			a.Check(t, sit.Host)
			return ctx
		})
	}

	testEnv.Test(t, feature.Feature())
}
