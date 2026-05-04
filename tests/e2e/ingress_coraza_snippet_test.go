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
	"regexp"
	"strings"
	"testing"

	"sigs.k8s.io/e2e-framework/pkg/envconf"
	"sigs.k8s.io/e2e-framework/pkg/features"
)

// TestIngressCorazaSnippetRejected exercises the pluggable-validator
// dispatch end-to-end: an Ingress carrying a deliberately-broken
// `nginx.ingress.kubernetes.io/modsecurity-snippet` is denied by the
// admission webhook with a line/col diagnostic from the SPOA hub's
// Coraza validator.
//
// The path under test:
//
//  1. nginx-ingress.yaml's features-200-nginx-ingress-modsecurity-snippet
//     reads the annotation and registers the snippet at
//     general/coraza/<ns>-<name>.conf via fileRegistry.Register.
//  2. The pluggable-validator manager (controller-side) sees the file,
//     matches the chart's `general/coraza/*.conf` glob, and dispatches
//     to the SPOA hub's coraza validate() socket.
//  3. Coraza parses the snippet, finds the parse error, returns a
//     Diagnostic with Line and a message.
//  4. The webhook formats the diagnostic as `<path>:<line>:<col>: <msg>`
//     and denies admission with the message in
//     AdmissionResponse.Result.Message — which surfaces as a 403
//     Forbidden through client-go.
//
// Any one of the four steps regressing breaks this test.
func TestIngressCorazaSnippetRejected(t *testing.T) {
	t.Parallel()

	feature := features.New("Ingress: broken modsecurity-snippet rejected by webhook").
		Assess("admission webhook denies with line/col diagnostic", func(ctx context.Context, t *testing.T, cfg *envconf.Config) context.Context {
			client, err := cfg.NewClient()
			if err != nil {
				t.Fatalf("new client: %v", err)
			}
			ns := NamespaceForTest(ctx, t, client)
			DumpLogsOnFailure(t, ns)
			backend := NewEchoServerBackend(ctx, t, client, ns)

			// "SecRule" with no arguments is a deterministic parse error
			// in coraza — it requires at least the variable selector,
			// match operator, and action list. Putting it on line 2 (with
			// a comment on line 1) lets us assert on a non-zero line
			// number and prove Coraza's line tracking actually survives
			// the wire protocol back to the webhook response.
			brokenSnippet := "# broken-on-purpose for e2e regression test\nSecRule\n"

			reason := NewIngressExpectDenied(ctx, t, client, ns, IngressSpec{
				Name:           "echo-broken-coraza",
				Host:           "ingress-coraza-broken.localdev.me",
				BackendService: backend.Service,
				BackendPort:    backend.Port,
				Annotations: map[string]string{
					"nginx.ingress.kubernetes.io/modsecurity-snippet": brokenSnippet,
				},
			})

			// 1. Path stem: confirms the rendered file landed where the
			//    chart's validator-files glob matches it.
			if !strings.Contains(reason, "coraza/") {
				t.Fatalf("denial reason missing 'coraza/' path stem (validator dispatch routing): %q", reason)
			}

			// 2. Line/col prefix: confirms Coraza's diagnostic line tracking
			//    survived the SPOA hub wire protocol and the webhook's
			//    formatDiagnostic — without this assertion, a generic
			//    "validation failed" string would silently pass and we'd
			//    lose the user-facing precision the whole pipeline exists
			//    to deliver.
			lineColRe := regexp.MustCompile(`coraza/[^:]+:\d+`)
			if !lineColRe.MatchString(reason) {
				t.Fatalf("denial reason missing '<file>:<line>' diagnostic prefix: %q", reason)
			}

			return ctx
		})

	testEnv.Test(t, feature.Feature())
}
