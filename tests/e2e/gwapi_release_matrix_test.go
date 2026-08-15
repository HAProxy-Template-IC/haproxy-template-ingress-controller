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
	"reflect"
	"testing"
	"time"

	"sigs.k8s.io/e2e-framework/pkg/envconf"
	"sigs.k8s.io/e2e-framework/pkg/features"

	"gitlab.com/haproxy-haptic/haptic/tests/e2e/e2ecluster"
	"gitlab.com/haproxy-haptic/haptic/tests/e2e/httpclient"
)

// TestGatewayAPIReleaseMatrix is the live counterpart of the offline
// degraded-profile harness in scripts/test-templates.sh: it verifies the
// runtime-version-detection contract against a REAL old Gateway API release
// installed in the cluster, then upgrades the CRDs in place to the suite
// default and asserts the controller converges without a pod restart.
//
// It only runs when TestMain installed a non-default release — the nightly
// gwapi-matrix CI job boots the cluster with HAPTIC_E2E_GWAPI_VERSION set to
// an old release tag (v1.1.0 / v1.4.0 / v1.5.1) and runs exactly this test.
// On a default (v1.6.0) cluster it skips: the in-place upgrade semantics are
// then already covered by TestGatewayAPICRDUpgradeInPlace.
//
// Deliberately NOT t.Parallel(): the final stage swaps cluster-scoped CRDs
// and reinitializes the controller, which would race any concurrent test.
// The nightly job runs it as the only test in the binary (TEST_RUN_PATTERN).
func TestGatewayAPIReleaseMatrix(t *testing.T) {
	release := os.Getenv("HAPTIC_E2E_GWAPI_VERSION")
	if release == "" || release == defaultGatewayAPIVersion {
		t.Skipf("HAPTIC_E2E_GWAPI_VERSION is %q — matrix test only runs against a non-default Gateway API release", release)
	}

	host := "gwapi-matrix.localdev.me"

	var (
		dc       *debugClient
		baseline *effectiveResolution
		fp       map[string]int32
		fwd      GatewayForward
	)

	// waitSettled polls until the controller reports an effective-config
	// resolution AND /healthz is settled (no reinit-grace annotation). The
	// accept callback gates on resolution content so callers can wait for a
	// specific post-upgrade state.
	waitSettled := func(ctx context.Context, t *testing.T, accept func(*effectiveResolution) bool) *effectiveResolution {
		t.Helper()
		deadline := time.Now().Add(3 * time.Minute)
		var last *effectiveResolution
		var lastErr error
		for time.Now().Before(deadline) {
			res, err := dc.getEffectiveResolution(ctx)
			if err == nil {
				last = res
				if accept(res) && dc.healthzSettled(ctx) {
					return res
				}
			} else {
				lastErr = err
			}
			time.Sleep(2 * time.Second)
		}
		t.Fatalf("timed out waiting for settled resolution (last: %+v, last error: %v)", last, lastErr)
		return nil
	}

	feature := features.New("Gateway API release matrix: degraded startup on "+release+" + in-place upgrade to "+defaultGatewayAPIVersion).
		Setup(func(ctx context.Context, t *testing.T, cfg *envconf.Config) context.Context {
			client, err := cfg.NewClient()
			if err != nil {
				t.Fatalf("new client: %v", err)
			}
			cs, err := newClientsetForE2E(client.RESTConfig())
			if err != nil {
				t.Fatalf("build clientset: %v", err)
			}
			dc = newDebugClient(client.RESTConfig(), cs)

			// TestMain already waited for the controller to become Ready on
			// the old release — reaching this point IS the degraded-startup
			// verification. Capture the resolution baseline and the pod
			// fingerprint for the upgrade assertions.
			baseline = waitSettled(ctx, t, func(*effectiveResolution) bool { return true })
			t.Logf("resolution on %s: resolved=%v unavailable=%v strippedSnippets=%d strippedTests=%d",
				release, baseline.ResolvedVersions, baseline.Unavailable,
				len(baseline.StrippedSnippets), len(baseline.StrippedTests))

			fp, err = controllerPodFingerprint(ctx, dc)
			if err != nil {
				t.Fatalf("pod fingerprint: %v", err)
			}
			if len(fp) == 0 {
				t.Fatal("no controller pods found for fingerprinting")
			}

			// Per-test routing fixtures — v1-core shapes only (Gateway +
			// HTTPRoute), valid on every release the matrix targets.
			ns := NamespaceForTest(ctx, t, client)
			DumpLogsOnFailure(t, ns)
			backend := NewEchoServerBackend(ctx, t, client, ns)
			NewGateway(ctx, t, ns, "matrix-gateway")
			fwd = ForwardGateway(ctx, t, ns, "matrix-gateway", 80)
			NewHTTPRoute(ctx, t, ns, HTTPRouteSpec{
				Name:        "matrix-route",
				GatewayName: "matrix-gateway",
				Hostnames:   []string{host},
				Rules: []HTTPRouteRule{{
					PathType: "PathPrefix",
					Path:     "/",
					BackendRefs: []HTTPRouteBackendRef{{
						Service: backend.Service,
						Port:    backend.Port,
					}},
				}},
			})
			return ctx
		}).
		Assess("core HTTPRoute routing works on "+release, func(ctx context.Context, t *testing.T, cfg *envconf.Config) context.Context {
			resp := httpclient.ForForwarded(t, fwd.HTTPPort, 0).GET(host, "/").ExpectOK(t)
			if resp.Echo == nil {
				t.Fatalf("expected echo-server JSON, got %d bytes", len(resp.Body))
			}
			return ctx
		}).
		Assess("in-place upgrade to "+defaultGatewayAPIVersion+" converges without pod restart", func(ctx context.Context, t *testing.T, cfg *envconf.Config) context.Context {
			if err := applyGatewayAPICRDs(ctx, defaultGatewayAPIVersion, e2ecluster.GatewayAPIChannelStandard); err != nil {
				t.Fatalf("upgrade Gateway API CRDs: %v", err)
			}

			// Convergence signal: settled /healthz (reinit finished) AND a
			// deterministic delta. Adjacent releases could legitimately
			// resolve identical version maps, so a pure map-difference wait
			// risks a false 3-minute timeout; tcproutes is standard-channel
			// only since v1.6, so its appearance is the concrete marker that
			// the upgraded CRDs were resolved. Accept either signal.
			res := waitSettled(ctx, t, func(r *effectiveResolution) bool {
				_, tcpWatched := r.ResolvedVersions["tcproutes"]
				return tcpWatched || !reflect.DeepEqual(r.ResolvedVersions, baseline.ResolvedVersions)
			})
			t.Logf("resolution after upgrade: resolved=%v unavailable=%v", res.ResolvedVersions, res.Unavailable)

			after, err := controllerPodFingerprint(ctx, dc)
			if err != nil {
				t.Fatalf("pod fingerprint after upgrade: %v", err)
			}
			if !reflect.DeepEqual(fp, after) {
				t.Fatalf("controller pods restarted during in-place CRD upgrade: before=%v after=%v", fp, after)
			}
			return ctx
		}).
		Assess("routing still works after the upgrade", func(ctx context.Context, t *testing.T, cfg *envconf.Config) context.Context {
			resp := httpclient.ForForwarded(t, fwd.HTTPPort, 0).GET(host, "/").ExpectOK(t)
			if resp.Echo == nil {
				t.Fatalf("expected echo-server JSON after upgrade, got %d bytes", len(resp.Body))
			}
			return ctx
		}).
		Feature()

	testEnv.Test(t, feature)
}
