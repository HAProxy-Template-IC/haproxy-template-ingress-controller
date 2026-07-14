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
	"os"
	"strings"
	"testing"

	"sigs.k8s.io/e2e-framework/klient"
	"sigs.k8s.io/e2e-framework/pkg/envconf"
	"sigs.k8s.io/e2e-framework/pkg/features"
)

// vendorPrefixes maps a vendor annotation prefix to the chart template-library
// flag that must be enabled for those annotations to take effect.
var vendorPrefixes = map[string]string{
	"haproxy.org/":                 "haproxytech",
	"haproxy-ingress.github.io/":   "haproxyIngress",
	"nginx.ingress.kubernetes.io/": "nginxIngress",
}

// activeVendorLibrary returns the single vendor library enabled in the current
// e2e shard (from HAPTIC_E2E_PROFILE), or "" for the core / conformance
// profiles. See the sharding note in main_test.go.
func activeVendorLibrary() string {
	switch os.Getenv("HAPTIC_E2E_PROFILE") {
	case "haproxytech":
		return "haproxytech"
	case "haproxy-ingress":
		return "haproxyIngress"
	case "nginx":
		return "nginxIngress"
	default:
		return ""
	}
}

// RequireVendorLibrary skips the test unless the named vendor library
// (haproxytech | haproxyIngress | nginxIngress) is the one enabled in the
// current e2e shard. Use it in vendor tests whose setup doesn't run through
// RunSimpleIngressTest.
func RequireVendorLibrary(t *testing.T, lib string) {
	t.Helper()
	if activeVendorLibrary() != lib {
		t.Skipf("vendor library %q is not enabled in this e2e shard (HAPTIC_E2E_PROFILE=%q); it runs in the %q shard", lib, os.Getenv("HAPTIC_E2E_PROFILE"), lib)
	}
}

// RequireCacheProfile skips the test unless the Varnish cache shard is active
// (HAPTIC_E2E_PROFILE=cache) — the only profile that deploys the cache tier.
func RequireCacheProfile(t *testing.T) {
	t.Helper()
	if os.Getenv("HAPTIC_E2E_PROFILE") != "cache" {
		t.Skipf("Varnish cache tier not deployed in this shard (HAPTIC_E2E_PROFILE=%q); it runs in the cache shard", os.Getenv("HAPTIC_E2E_PROFILE"))
	}
}

// skipIfVendorDisabled skips the test if any of its annotations use a vendor
// prefix whose library isn't the one enabled in the current shard. This
// auto-gates every RunSimpleIngressTest-based vendor test with no per-test
// change: a haproxy.org/* test runs only in the haproxytech shard, and so on.
func skipIfVendorDisabled(t *testing.T, annotations map[string]string) {
	t.Helper()
	active := activeVendorLibrary()
	for key := range annotations {
		for prefix, lib := range vendorPrefixes {
			if strings.HasPrefix(key, prefix) && lib != active {
				t.Skipf("annotation %q needs vendor library %q, not enabled in this e2e shard (HAPTIC_E2E_PROFILE=%q)", key, lib, os.Getenv("HAPTIC_E2E_PROFILE"))
			}
		}
	}
}

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
	// Auto-skip vendor-annotation tests in shards where their library is off.
	skipIfVendorDisabled(t, sit.Annotations)

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
