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

// Package e2e is the full-stack end-to-end test suite for
// HAPTIC. It is self-contained: TestMain creates its own kind
// cluster, helm-installs the chart, and deploys backend fixtures. Nothing
// outside the test binary needs to run first.
package e2e

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"golang.org/x/sync/errgroup"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	"sigs.k8s.io/e2e-framework/pkg/env"
	"sigs.k8s.io/e2e-framework/pkg/envconf"
	"sigs.k8s.io/gateway-api/pkg/consts"
	kindcluster "sigs.k8s.io/kind/pkg/cluster"
	kindcmd "sigs.k8s.io/kind/pkg/cmd"

	haproxyv1alpha1 "gitlab.com/haproxy-haptic/haptic/pkg/apis/haproxytemplate/v1alpha1"
	devassets "gitlab.com/haproxy-haptic/haptic/scripts/dev-env-assets"
	"gitlab.com/haproxy-haptic/haptic/tests/kindutil"
)

// testEnv is the e2e-framework environment shared by all tests in the suite.
// Initialised in TestMain. Tests run via testEnv.Test(t, feature).
var testEnv env.Environment

// kubeconfigPath is the kubeconfig file the suite uses; it is isolated from
// the developer's default kubeconfig to prevent accidental cluster access.
const kubeconfigPath = "/tmp/haproxy-e2e-kubeconfig"

func init() {
	// Register the HAProxyTemplateConfig CRD types with the global scheme so
	// the e2e-framework client can read/write them via dynamic typed access.
	if err := haproxyv1alpha1.AddToScheme(clientgoscheme.Scheme); err != nil {
		panic(fmt.Sprintf("e2e: register haproxy scheme: %v", err))
	}
}

// TestMain wires the suite's lifecycle: create or reuse the kind cluster,
// helm-install the chart, deploy backend fixtures, then run the tests.
//
// Cluster lifecycle is governed by:
//   - KEEP_CLUSTER (default true): keep cluster after the suite for fast
//     iteration. Set to false for ephemeral runs.
//   - SKIP_CLUSTER_CREATE: assume cluster already exists; skip the kind
//     provider call entirely. Set by the CI runner when helm/kind-action
//     pre-creates the cluster.
//
// Image expectations: haptic:test must exist in the local docker daemon
// before running. The Makefile target `test-e2e` depends on
// `docker-build-test` to build it.
func TestMain(m *testing.M) {
	testEnv = env.NewParallel()

	// SAFETY: Isolate kubeconfig.
	if err := os.Setenv("KUBECONFIG", kubeconfigPath); err != nil {
		fmt.Fprintf(os.Stderr, "e2e: set KUBECONFIG: %v\n", err)
		os.Exit(1)
	}

	// Wire the at-failure HAProxy state snapshotter into the
	// httpclient package's poll-timeout hook. Triggered when a
	// test's HTTP polling exhausts its 180s retry budget; dumps
	// the chart-rendered HAProxyCfg + the running pod's
	// /etc/haproxy tree BEFORE the test's t.Cleanup chain deletes
	// per-test fixtures. Without this, the standard
	// DumpLogsOnFailure runs after fixture deletion and captures
	// only the empty-defaults post-cleanup state — leaving real
	// failures undiagnosable from CI artifacts. See snapshot.go
	// for the per-test throttle.
	InstallFailureSnapshotter()

	provider := kindcluster.NewProvider(
		kindcluster.ProviderWithLogger(kindcmd.NewLogger()),
	)

	// Heartbeat: emit current setup phase + elapsed every 5s so the GitLab
	// job trace shows progress instead of looking frozen for the ~4-minute
	// window between `go: downloading` and the first test PASS line.
	// `go test -v` + `t.Parallel()` buffers per-test output until parents
	// complete, but TestMain-side writes from a goroutine to os.Stderr
	// flush in real time.
	heartbeatCtx, heartbeatStop := context.WithCancel(context.Background())
	startSetupHeartbeat(heartbeatCtx)

	// caBundleB64 is captured from the webhook-cert setup step and consumed
	// by the helm-install step. Closure rather than context-passing because
	// envconf.Config doesn't carry arbitrary values.
	var caBundleB64 string

	testEnv.Setup(
		phase("cluster-create", func(ctx context.Context, cfg *envconf.Config) (context.Context, error) {
			return setupCluster(ctx, cfg, provider)
		}),
		phase("load-controller-image", func(ctx context.Context, cfg *envconf.Config) (context.Context, error) {
			return loadControllerImage(ctx)
		}),
		phase("ensure-namespaces", func(ctx context.Context, cfg *envconf.Config) (context.Context, error) {
			return ctx, ensureNamespaces(ctx)
		}),
		// install-cluster-services fans out two independent chains in
		// parallel after the cluster + namespaces are up:
		//
		//   chain A: install-metallb              (~85s on a cold runner)
		//   chain B: install-crds+certs → helm-install → backend-fixtures
		//                                         (~120s end-to-end)
		//
		// Chains A and B are fully independent — MetalLB doesn't touch
		// the chart and the chart doesn't talk to MetalLB until the
		// loadbalancer Service needs an IP, which doesn't happen until
		// helm finishes. Running A in parallel with B cuts the e2e
		// cluster bootstrap by ~85s wall-clock on a fresh runner, which
		// is the dominant saving the conformance jobs see (they pay the
		// full bootstrap via `TEST_RUN_PATTERN=^$ make test-e2e` before
		// running their actual suite).
		//
		// Backend fixtures (echo-server, blocklist-server, auth-server,
		// …) are skipped when HAPTIC_E2E_PROFILE=conformance because the
		// upstream conformance suites bring up their own per-scenario
		// backend pods. Loading the e2e fixtures alongside pollutes the
		// namespace inventory AND (in the blocklist-server case)
		// triggers a per-render http.Fetch the chart keeps retrying on
		// every reconcile until the pod becomes ready — historically
		// the timing source behind shard-4 TLSRouteHostnameIntersection
		// failures and the 7s-per-render burn that broke
		// HTTPRouteReferenceGrant within its 10s convergence budget.
		// The errgroup propagates a single error from either chain and
		// cancels its sibling, matching the previous sequential
		// behaviour where the first failure aborted the whole setup.
		phase("install-cluster-services (parallel chains)", func(ctx context.Context, cfg *envconf.Config) (context.Context, error) {
			g, gctx := errgroup.WithContext(ctx)
			g.Go(func() error {
				_, err := installMetalLB(gctx)
				return err
			})
			g.Go(func() error {
				b, err := preInstallParallel(gctx)
				if err != nil {
					return err
				}
				caBundleB64 = b
				if _, err := helmInstallChart(gctx, caBundleB64); err != nil {
					return err
				}
				if os.Getenv("HAPTIC_E2E_PROFILE") == "conformance" {
					fmt.Fprintln(os.Stderr, "e2e: conformance profile — skipping backend fixtures")
					return nil
				}
				_, err = applyBackendFixtures(gctx)
				return err
			})
			return ctx, g.Wait()
		}),
		phase("wait-environment-ready", func(ctx context.Context, cfg *envconf.Config) (context.Context, error) {
			client, err := cfg.NewClient()
			if err != nil {
				return ctx, fmt.Errorf("new client: %w", err)
			}
			return ctx, WaitForE2EEnvironmentReady(ctx, client)
		}),
		phase("tests-running", func(ctx context.Context, cfg *envconf.Config) (context.Context, error) {
			return ctx, nil
		}),
	)

	testEnv.Finish(
		func(ctx context.Context, cfg *envconf.Config) (context.Context, error) {
			return teardownCluster(ctx, provider)
		},
	)

	code := testEnv.Run(m)
	heartbeatStop()
	os.Exit(code)
}

// setupPhase is the current TestMain phase, read by the heartbeat goroutine
// and updated by the phase() wrapper before each setup step runs.
var setupPhase atomic.Pointer[string]

// setupStart marks when TestMain began. The heartbeat reports
// time-since-this so each line shows total elapsed setup time.
var setupStart = time.Now()

// phase wraps an env.Func to record the current setup-phase label before
// running it, so the heartbeat reflects which step is currently executing.
func phase(name string, fn env.Func) env.Func {
	return func(ctx context.Context, cfg *envconf.Config) (context.Context, error) {
		setupPhase.Store(&name)
		return fn(ctx, cfg)
	}
}

// startSetupHeartbeat prints the current TestMain phase + elapsed-since-start
// to stderr every 5 seconds until ctx is cancelled. Restores legibility to
// the GitLab job trace, which otherwise sits silent for ~4 minutes during
// compile + cluster-bring-up + helm install + fixture deploy.
func startSetupHeartbeat(ctx context.Context) {
	initial := "init"
	setupPhase.Store(&initial)
	go func() {
		t := time.NewTicker(5 * time.Second)
		defer t.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-t.C:
				p := setupPhase.Load()
				if p == nil {
					continue
				}
				fmt.Fprintf(os.Stderr, "[e2e] phase=%s elapsed=%ds\n", *p, int(time.Since(setupStart).Seconds()))
			}
		}
	}()
}

// setupCluster creates the kind cluster if it doesn't already exist,
// patches the kubeconfig for DinD if applicable, and writes it to the
// suite's isolated kubeconfig path.
func setupCluster(ctx context.Context, cfg *envconf.Config, provider *kindcluster.Provider) (context.Context, error) {
	if os.Getenv("SKIP_CLUSTER_CREATE") == "true" {
		// CI mode: cluster pre-created. Just record the kubeconfig.
		kc := os.Getenv("KUBECONFIG")
		if kc == "" {
			return ctx, fmt.Errorf("SKIP_CLUSTER_CREATE=true but KUBECONFIG is empty")
		}
		cfg.WithKubeconfigFile(kc)
		return ctx, nil
	}

	clusters, err := provider.List()
	if err != nil {
		return ctx, fmt.Errorf("list kind clusters: %w", err)
	}
	clusterExists := false
	for _, c := range clusters {
		if c == ClusterName {
			clusterExists = true
			break
		}
	}

	if !clusterExists {
		opts := []kindcluster.CreateOption{
			kindcluster.CreateWithWaitForReady(DefaultClusterCreateTimeout),
			kindcluster.CreateWithRawConfig([]byte(e2eKindConfig)),
		}
		if err := provider.Create(ClusterName, opts...); err != nil {
			return ctx, fmt.Errorf("create kind cluster %q: %w", ClusterName, err)
		}
		// Best-effort metrics-server so the rolling-restart failure snapshot's
		// `kubectl top` capture has real utilization data. Non-fatal.
		installMetricsServerBestEffort(ctx)
	}

	kubeconfig, err := provider.KubeConfig(ClusterName, false)
	if err != nil {
		return ctx, fmt.Errorf("get kubeconfig for %q: %w", ClusterName, err)
	}
	if kindutil.IsDockerInDocker() {
		kubeconfig = kindutil.PatchKubeconfigForDind(kubeconfig)
	}
	if err := os.WriteFile(kubeconfigPath, []byte(kubeconfig), 0600); err != nil {
		return ctx, fmt.Errorf("write kubeconfig: %w", err)
	}
	cfg.WithKubeconfigFile(kubeconfigPath)
	return ctx, nil
}

// installMetricsServerBestEffort applies metrics-server to the freshly-created
// kind cluster so `kubectl top pods/nodes` works for the rolling-restart
// failure snapshot's utilization capture. Deliberately best-effort and
// non-fatal: it applies the manifest and returns without waiting for readiness.
// metrics-server needs ~15-30s to start scraping, which the subsequent
// image-load + helm-install + fixture-deploy comfortably covers; and if a
// restricted-egress CI can't pull the image, `kubectl top` simply returns no
// data (empty snapshot capture) — never a reason to fail cluster setup.
func installMetricsServerBestEffort(ctx context.Context) {
	node := ClusterName + "-control-plane"
	cmd := exec.CommandContext(ctx, "docker", "exec", "-i", node,
		"kubectl", "--kubeconfig=/etc/kubernetes/admin.conf", "apply", "-f", "-")
	cmd.Stdin = bytes.NewReader(devassets.MetricsServerYAML)
	if out, err := cmd.CombinedOutput(); err != nil {
		fmt.Fprintf(os.Stderr, "e2e: metrics-server apply (best-effort) failed: %v: %s\n", err, out)
		return
	}
	fmt.Fprintln(os.Stderr, "e2e: metrics-server applied (best-effort; kubectl top available once it scrapes)")
}

// loadControllerImage loads haptic:test into the kind cluster so the helm
// install can find it (the chart sets imagePullPolicy: Never via dev-values).
// Skipped when SKIP_CLUSTER_CREATE=true (CI does its own load).
//
// We avoid `kind load docker-image` because it stages the image as a tar
// in $TMPDIR and then docker saves into that path — which fails when
// dockerd runs under systemd with PrivateTmp=yes (the daemon and the
// caller see different /tmp namespaces). Instead, pipe `docker save`
// straight into `ctr image import` inside the kind control-plane
// container. Same effect, no host temp files.
func loadControllerImage(ctx context.Context) (context.Context, error) {
	if os.Getenv("SKIP_CLUSTER_CREATE") == "true" {
		return ctx, nil
	}
	if err := loadImageIntoKind(ctx, ControllerImageName); err != nil {
		return ctx, err
	}
	// Every non-conformance shard deploys the echo-server fixture. Preload its
	// pinned image so individual test namespaces never depend on a kind node's
	// Docker Hub path while their short endpoint-readiness deadline is running.
	if os.Getenv("HAPTIC_E2E_PROFILE") != "conformance" {
		if err := pullImageIntoKind(ctx, echoServerImage); err != nil {
			return ctx, err
		}
	}
	// The cache shard deploys the Varnish tier. Pull the (stock upstream) image
	// on the host and load it into kind so the StatefulSet doesn't depend on the
	// kind node reaching Docker Hub (and no rate-limit flakiness in CI).
	if os.Getenv("HAPTIC_E2E_PROFILE") == "cache" {
		if err := pullImageIntoKind(ctx, VarnishImage); err != nil {
			return ctx, err
		}
		if err := pullImageIntoKind(ctx, VarnishPolicyProbeImage); err != nil {
			return ctx, err
		}
	}
	// The shared rate-limit shard deploys Valkey. Load the stock image into kind
	// for the same reason as the cache shard's Varnish image: deterministic CI
	// and no dependency on the kind node reaching Docker Hub.
	if os.Getenv("HAPTIC_E2E_PROFILE") == "rate-limit" {
		if err := pullImageIntoKind(ctx, ValkeyImage); err != nil {
			return ctx, err
		}
		if os.Getenv("SPOA_TAG") == "" {
			fmt.Fprintf(os.Stderr, "e2e: rate-limit profile — loading local %s into kind\n", LocalSPOAHubImage)
			if err := loadImageIntoKind(ctx, LocalSPOAHubImage); err != nil {
				return ctx, err
			}
		}
	}
	if os.Getenv("HAPTIC_E2E_PROFILE") == "api-gateway" && os.Getenv("SPOA_TAG") == "" {
		fmt.Fprintf(os.Stderr, "e2e: api-gateway profile — loading local %s into kind\n", LocalSPOAHubImage)
		if err := loadImageIntoKind(ctx, LocalSPOAHubImage); err != nil {
			return ctx, err
		}
	}
	return ctx, nil
}

// pullImageRetries is how many times a registry pull is attempted before the
// suite gives up. Every e2e shard pulls these images during TestMain, so a
// single upstream blip used to red a whole pipeline for no local reason: a
// Docker Hub `502 Bad Gateway` on ealen/echo-server took out test-e2e-rate-limit
// on main before any test ran. The retry covers only the network fetch — the
// kind import that follows is local and fails deterministically.
const pullImageRetries = 3

func pullImageIntoKind(ctx context.Context, image string) error {
	var err error
	for attempt := 1; attempt <= pullImageRetries; attempt++ {
		pull := exec.CommandContext(ctx, "docker", "pull", image)
		pull.Stdout, pull.Stderr = os.Stderr, os.Stderr
		if err = pull.Run(); err == nil {
			return loadImageIntoKind(ctx, image)
		}
		if attempt == pullImageRetries {
			break
		}
		backoff := time.Duration(attempt*attempt) * 2 * time.Second
		fmt.Fprintf(os.Stderr, "e2e: docker pull %s failed (attempt %d/%d): %v; retrying in %s\n",
			image, attempt, pullImageRetries, err, backoff)
		select {
		case <-ctx.Done():
			return fmt.Errorf("docker pull %s: %w", image, ctx.Err())
		case <-time.After(backoff):
		}
	}
	return fmt.Errorf("docker pull %s failed after %d attempts: %w", image, pullImageRetries, err)
}

// loadImageIntoKind pipes `docker save <image>` into `ctr image import` inside
// the kind control-plane container. We avoid `kind load docker-image` because it
// stages a tar in $TMPDIR, which fails when dockerd runs under systemd
// PrivateTmp=yes (daemon and caller see different /tmp namespaces).
func loadImageIntoKind(ctx context.Context, image string) error {
	saveCmd := exec.CommandContext(ctx, "docker", "save", image)
	importCmd := exec.CommandContext(ctx, "docker", "exec", "-i",
		ClusterName+"-control-plane",
		"ctr", "--namespace=k8s.io", "images", "import", "-")

	pipe, err := saveCmd.StdoutPipe()
	if err != nil {
		return fmt.Errorf("pipe docker save: %w", err)
	}
	importCmd.Stdin = pipe
	importCmd.Stdout = os.Stderr
	importCmd.Stderr = os.Stderr
	saveCmd.Stderr = os.Stderr

	if err := importCmd.Start(); err != nil {
		return fmt.Errorf("start ctr import: %w", err)
	}
	if err := saveCmd.Run(); err != nil {
		_ = importCmd.Wait()
		return fmt.Errorf("docker save %s: %w", image, err)
	}
	if err := importCmd.Wait(); err != nil {
		return fmt.Errorf("ctr image import %s: %w", image, err)
	}
	return nil
}

// installCRDs applies the chart's CRD manifests. We do this separately
// from helm install because the chart references the CRDs and they must
// exist before the chart installs.
func installCRDs(ctx context.Context) (context.Context, error) {
	crdDir, err := chartCRDDir()
	if err != nil {
		return ctx, err
	}
	cmd := exec.CommandContext(ctx, "kubectl", "apply", "--kubeconfig", kubeconfigPath, "-f", crdDir)
	if out, err := cmd.CombinedOutput(); err != nil {
		return ctx, fmt.Errorf("kubectl apply CRDs: %w (output: %s)", err, out)
	}
	return ctx, nil
}

// defaultGatewayAPIVersion is the Gateway API release whose standard-channel
// CRDs the suite installs by default. Read from the module rather than pinned
// by hand: it is the exact value the CRDs carry in their bundle-version
// annotation, and the conformance suite refuses to start when that disagrees
// with its own module version. A hand-written copy goes stale the moment
// sigs.k8s.io/gateway-api is bumped in go.mod (job 15627486657).
const defaultGatewayAPIVersion = consts.BundleVersion

// installGatewayAPICRDs installs the upstream Gateway API standard-channel
// CRDs (Gateway, HTTPRoute, GRPCRoute, etc.) so the chart's gateway library
// can register watchers and HTTPRoute-based tests can run.
//
// HAPTIC_E2E_GWAPI_VERSION overrides the installed release — the nightly
// gwapi-matrix CI job sets an old release tag (e.g. v1.1.0) to verify
// runtime version detection against a live old cluster, and the nightly
// canary job sets a git ref ("main") to test against unreleased upstream
// CRDs. TestGatewayAPIReleaseMatrix only runs under an override.
//
// Idempotent: if CRDs already exist (KEEP_CLUSTER=true reuse), kubectl apply
// is a no-op and the wait condition just returns immediately.
func installGatewayAPICRDs(ctx context.Context) (context.Context, error) {
	version := os.Getenv("HAPTIC_E2E_GWAPI_VERSION")
	if version == "" {
		version = defaultGatewayAPIVersion
	}
	return ctx, applyGatewayAPICRDs(ctx, version)
}

// applyGatewayAPICRDs applies the standard-channel CRD bundle of the given
// Gateway API version and waits for the core CRDs to be Established.
// A version starting with "v" resolves to that release's standard-install
// manifest; anything else is treated as a git ref of the upstream repo and
// applied via kustomize (config/crd, the standard-channel base), which is
// how unreleased refs like "main" ship their CRDs.
func applyGatewayAPICRDs(ctx context.Context, version string) error {
	// The CRD bundle (release manifest for a "v" version, kustomize base for a
	// git ref) is fetched from GitHub, which intermittently 504s / times out at
	// setup. A single failure there sinks the whole e2e or conformance job
	// before any test runs, so retry a few times with linear backoff. exec.Cmd
	// isn't reusable, so rebuild it each attempt.
	newApply := func() *exec.Cmd {
		if strings.HasPrefix(version, "v") {
			url := fmt.Sprintf("https://github.com/kubernetes-sigs/gateway-api/releases/download/%s/standard-install.yaml", version)
			return exec.CommandContext(ctx, "kubectl", "apply", "--kubeconfig", kubeconfigPath, "-f", url)
		}
		ref := fmt.Sprintf("github.com/kubernetes-sigs/gateway-api/config/crd?ref=%s", version)
		return exec.CommandContext(ctx, "kubectl", "apply", "--kubeconfig", kubeconfigPath, "-k", ref)
	}
	var out []byte
	var err error
	for attempt := 1; attempt <= 4; attempt++ {
		if out, err = newApply().CombinedOutput(); err == nil {
			break
		}
		if ctx.Err() != nil {
			break // suite is shutting down — don't keep retrying
		}
		if attempt < 4 {
			time.Sleep(time.Duration(attempt) * 2 * time.Second) // 2s, 4s, 6s
		}
	}
	if err != nil {
		return fmt.Errorf("install Gateway API CRDs (%s) after retries: %w (output: %s)", version, err, out)
	}

	// Single multi-arg `kubectl wait` — kubectl waits on all four CRDs in
	// parallel, so we don't pay the sequential per-CRD shell-out overhead.
	// The Established check guards against the chart's helm install racing
	// the watcher's CRD lookup. These four exist in the standard channel of
	// every release the suite targets (GRPCRoute is standard since v1.1).
	wait := exec.CommandContext(ctx, "kubectl", "wait", "--kubeconfig", kubeconfigPath,
		"--for=condition=Established", "--timeout=60s",
		"crd/gatewayclasses.gateway.networking.k8s.io",
		"crd/gateways.gateway.networking.k8s.io",
		"crd/httproutes.gateway.networking.k8s.io",
		"crd/grpcroutes.gateway.networking.k8s.io",
	)
	if out, err := wait.CombinedOutput(); err != nil {
		return fmt.Errorf("wait for Gateway API CRDs established (%s): %w (output: %s)", version, err, out)
	}
	return nil
}

// ensureNamespaces idempotently creates the controller, shared-fixture, and
// security namespaces upfront so the parallel install/fixture phases don't
// race on namespace creation. It also installs the centrally owned WAF policy
// catalog that e2e-values.yaml references before the controller starts.
func ensureNamespaces(ctx context.Context) error {
	manifest := fmt.Sprintf(`apiVersion: v1
kind: Namespace
metadata:
  name: %s
---
apiVersion: v1
kind: Namespace
metadata:
  name: %s
---
apiVersion: v1
kind: Namespace
metadata:
  name: security
---
apiVersion: v1
kind: ConfigMap
metadata:
  namespace: security
  name: haptic-waf-policies
data:
  policies.yaml: |
    streaming-search:
      description: Metadata inspection with a narrow search exception and no request-body buffering
      requestBody:
        mode: none
      enforcement: deny
      ruleExclusions:
        - tags: [attack-sqli, attack-xss]
          excludeTarget: "ARGS:q"
    form-body-inspection:
      description: Form request policy with bounded complete body inspection
      requestBody:
        mode: any
      enforcement: deny
`, ControllerNamespace, SharedFixturesNamespace)
	return kubectlApplyStdin(ctx, []byte(manifest))
}

// preInstallParallel runs the helm-install prerequisites concurrently:
// chart CRDs, Gateway API CRDs, the webhook CA + server-cert Secret, and
// the default-ssl-cert Secret. They all depend only on the cluster + the
// pre-created namespace, not on each other, so fanning them out cuts the
// sequential cost of this phase.
//
// Returns the base64 CA bundle from setupWebhookCerts so the helm step
// can pass it as --set controller.webhook.caBundle=...
func preInstallParallel(ctx context.Context) (string, error) {
	g, gctx := errgroup.WithContext(ctx)
	var caBundleB64 string

	g.Go(func() error {
		_, err := installCRDs(gctx)
		return err
	})
	g.Go(func() error {
		_, err := installGatewayAPICRDs(gctx)
		return err
	})
	g.Go(func() error {
		b, err := setupWebhookCerts(gctx)
		if err != nil {
			return err
		}
		caBundleB64 = b
		return nil
	})
	g.Go(func() error {
		return setupDefaultSSLCert(gctx)
	})

	if err := g.Wait(); err != nil {
		return "", err
	}
	return caBundleB64, nil
}

// helmInstallChart installs the chart from charts/haptic with dev-values.yaml
// as the base, layering a controller.image.tag=test override to point at the local
// haptic:test image. Idempotent: if the release already exists (e.g.,
// from a previous run with KEEP_CLUSTER=true), this becomes a `helm upgrade`.
//
// Implementation note: this uses the `helm` CLI rather than the helm Go
// SDK. The SDK would be the cleaner choice but its transitive imports drag
// in `k8s.io/api/scheduling/v1alpha1`, a package removed in k8s 1.32+ and
// thus incompatible with this repo's k8s.io/* v0.36 dependency. Switch to
// the SDK once an upstream helm release ships against k8s 1.36 client-go.
func helmInstallChart(ctx context.Context, caBundleB64 string) (context.Context, error) {
	chartDir, err := chartPath()
	if err != nil {
		return ctx, err
	}

	// HAPTIC_E2E_PROFILE selects which embedded values file to
	// install with. Default uses E2EValuesYAML (the e2e suite has
	// parallel-ingress-create timing requirements that diverge
	// from the dev loop — see its doc comment). The conformance
	// profile strips out the HTTP-store demo and other dev-loop
	// fixtures that don't belong in conformance runs.
	profile := os.Getenv("HAPTIC_E2E_PROFILE")
	var valuesBytes []byte
	// cacheProfile enables the Varnish shared-cache tier for the cache shard.
	cacheProfile := profile == "cache"
	// rateLimitProfile enables shared rate limiting and its Valkey store.
	rateLimitProfile := profile == "rate-limit"
	// apiGatewayProfile enables the api-gateway SPOA plugin for JSON request validation.
	apiGatewayProfile := profile == "api-gateway"
	switch profile {
	case "conformance":
		valuesBytes = devassets.ConformanceValuesYAML
		fmt.Fprintln(os.Stderr, "e2e: using conformance values profile")
	default:
		valuesBytes = devassets.E2EValuesYAML
	}
	// Write the chosen values bytes to a temp file so helm can consume them.
	valuesFile, err := os.CreateTemp("", "haptic-e2e-values-*.yaml")
	if err != nil {
		return ctx, fmt.Errorf("create temp values file: %w", err)
	}
	defer os.Remove(valuesFile.Name())
	if _, err := valuesFile.Write(valuesBytes); err != nil {
		return ctx, fmt.Errorf("write temp values: %w", err)
	}
	if err := valuesFile.Close(); err != nil {
		return ctx, fmt.Errorf("close temp values: %w", err)
	}

	// We deliberately omit --wait. The chart's HAProxy readiness probe
	// only passes once the controller has pushed an initial config — which
	// is a chicken-and-egg situation under helm --wait, since helm waits
	// for *all* pods Ready before returning. Instead, we let helm install
	// return as soon as the manifests are applied, and WaitForE2EEnvironmentReady
	// polls the controller's debug endpoint for deployment.status=succeeded
	// (which implies HAProxy received and reloaded the config).
	args := []string{
		"upgrade", "--install", HelmReleaseName, chartDir,
		"--kubeconfig", kubeconfigPath,
		"--namespace", ControllerNamespace,
		"--create-namespace",
		"--values", valuesFile.Name(),
		"--set", "controller.image.tag=test",
		// haproxyVersion gates two things in the chart: the controller image
		// tag suffix (`<image.tag>-haproxy<haproxyVersion>`, e.g.
		// "test-haproxy3.0") and the haproxy:VERSION sidecar image. The
		// Makefile re-tags haptic:test → haptic:test-haproxy${HAPROXY_VERSION}
		// per matrix entry; without forcing the chart's haproxyVersion to
		// match, the chart falls back to its values.yaml default (currently
		// 3.2) and only the 3.2 matrix entry happens to find its image.
		// Every other matrix entry hits ImagePullBackOff.
		"--set", "haproxyVersion=" + ChartHAProxyVersion,
		"--set", "controller.webhook.caBundle=" + caBundleB64,
		// LoadBalancer for the haproxy frontend service so MetalLB
		// (installed in the install-metallb phase above) assigns a
		// real reachable IP. The Gateway API conformance suite uses
		// Gateway.status.addresses to construct test traffic URLs;
		// without a LoadBalancer IP the suite times out waiting for
		// the address to populate. Existing e2e tests still work via
		// the same Service: kind+MetalLB allocates a NodePort on
		// LoadBalancer-typed Services too, and the dind-aware
		// httpclient picks the right destination automatically.
		"--set", "haproxy.service.type=LoadBalancer",
		"--timeout", DefaultHelmInstallTimeout.String(),
	}
	// All three vendor annotation libraries are enabled by the core values
	// (e2e-values.yaml), so every vendor test runs in the default profile and
	// there are no per-vendor shards. That became possible with the per-object
	// config split (ADR-0014): the combination used to exceed etcd's ~1.5 MiB
	// per-object limit, and `make cr-size-check` now renders exactly this
	// profile as the standing size regression test.
	// Cache shard: deploy the Varnish tier (one replica keeps the shard quick).
	// The tier's origin is the HAProxy Service; loopback + caching are exercised
	// by TestHapticVarnishCache (gated on this profile via RequireCacheProfile).
	if cacheProfile {
		args = append(args,
			"--set", "cache.varnish.enabled=true",
			"--set", "cache.varnish.replicas=1",
			"--set", "cache.varnish.podDisruptionBudget.enabled=false",
			"--set", fmt.Sprintf("haproxy.service.http.port=%d", ChartHAProxyServiceHTTPPort))
		fmt.Fprintf(os.Stderr, "e2e: cache shard — enabling Varnish with HAProxy Service port %d and kindnet policy enforcement\n", ChartHAProxyServiceHTTPPort)
	}
	// Shared rate-limit shard: deploy Valkey and auto-wire the bundled
	// rate-limit plugin. TestHapticSharedRateLimit is gated on this profile.
	if rateLimitProfile {
		args = append(args,
			"--set", "rateLimit.shared.enabled=true",
			"--set", "rateLimit.shared.managedStore.enabled=true")
		if os.Getenv("SPOA_TAG") == "" {
			args = append(args,
				"--set", "spoaHub.image.repository=spoa-hub",
				"--set", "spoaHub.image.tag=dev",
				"--set", "spoaHub.image.pullPolicy=Never")
			fmt.Fprintln(os.Stderr, "e2e: rate-limit shard — using local spoa-hub:dev image")
		}
		fmt.Fprintln(os.Stderr, "e2e: rate-limit shard — enabling shared rate limiting with Valkey")
	}
	if apiGatewayProfile {
		args = append(args,
			"--set", "controller.config.templatingSettings.extraContext.apiGateway.requestSchemaValidation.enabled=true")
		if os.Getenv("SPOA_TAG") == "" {
			args = append(args,
				"--set", "spoaHub.image.repository=spoa-hub",
				"--set", "spoaHub.image.tag=dev",
				"--set", "spoaHub.image.pullPolicy=Never")
			fmt.Fprintln(os.Stderr, "e2e: api-gateway shard — using local spoa-hub:dev image")
		}
		fmt.Fprintln(os.Stderr, "e2e: api-gateway shard — enabling request validation")
	}
	// dev-values.yaml hardcodes spoaHub.image.tag=main-latest. CI sets
	// SPOA_TAG to ci-${CI_PIPELINE_ID} so the test loads the spoa-hub
	// image freshly built by build-spoa-image-snapshot in the same
	// pipeline. Without this override the test pulls the stale
	// :main-latest image, which carries main's hub+plugin versions —
	// not whatever versions-spoa.env points at on the MR branch. When
	// an MR bumps versions-spoa.env (e.g. for a new --validate-socket
	// flag the chart references), main-latest is the wrong image to
	// load: the validator sidecar CrashLoopBackOffs on an unknown
	// flag and every webhook call returns 'connection refused'. This
	// is the same shape as the !890 chart-MR breakage; the e2e test
	// was supposed to catch it on !893 but didn't because the helm
	// install never honored the freshly-built image.
	if spoaTag := os.Getenv("SPOA_TAG"); spoaTag != "" {
		args = append(args, "--set", "spoaHub.image.tag="+spoaTag)
	}
	// Churn tier (issue #64): expose the Gateway pod-port allocator's
	// assignments as `# gw-pod-port:` comment lines in the rendered config
	// so TestGatewayChurn can assert zero cross-wiring through the
	// `rendered` introspection var. The flag only gates debug-comment
	// emission (see charts/haptic/charts/gateway/15-pod-port-allocator.yaml);
	// ordinary e2e runs never set it, matching production. --set-string keeps
	// the value a YAML string — the chart snippet compares against "true".
	// The value path is controller.config.templatingSettings: the chart's
	// haproxytemplateconfig.yaml deep-copies `.Values.controller.config`
	// into the CR spec (a controller.templatingSettings sibling would be
	// silently ignored).
	if os.Getenv(churnEnableEnv) == "1" {
		args = append(args, "--set-string",
			"controller.config.templatingSettings.extraContext.dumpPodPortAllocations=true")
	}
	// Scale tier: TestScale measures controller RSS against a 1 GiB budget
	// and times convergence at 800+ Ingresses. Two deviations from the
	// dev-oriented e2e profile make those measurements honest:
	//   - memory limit 2Gi: the profile's 512Mi limit would OOM-kill the
	//     controller before the RSS budget could ever be observed — the
	//     budget must be enforced by the test, not masked by the kubelet.
	//   - INFO logging: the profile's DEBUG level emits per-resource lines;
	//     at 800+ resources the log volume itself would distort the timing
	//     measurements, and production runs INFO.
	//
	// The sidecar is deliberately NOT given its own budget here: it runs on the
	// chart default, so this tier is the one place that exercises what actually
	// ships. Forcing it to 2Gi (as this did) meant no run anywhere validated the
	// shipped cap, and issue #111 sat ambiguous for a day because a green
	// nightly proved nothing about it.
	if os.Getenv(scaleEnableEnv) == "1" {
		args = append(args,
			"--set", "controller.resources.limits.memory=2Gi",
			"--set", "controller.logLevel=INFO",
			"--set", "controller.config.logging.level=INFO",
		)
	}
	cmd := exec.CommandContext(ctx, "helm", args...)
	cmd.Stdout = os.Stderr
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		return ctx, fmt.Errorf("helm upgrade --install: %w", err)
	}
	return ctx, nil
}

// applyBackendFixtures installs the stateless backend fixtures into the
// SharedFixturesNamespace. These are deployed once per cluster and shared
// across all tests; they don't carry per-test state. The namespace itself
// is created by ensureNamespaces upstream so all five applies can fan out
// concurrently without racing on namespace creation.
func applyBackendFixtures(ctx context.Context) (context.Context, error) {
	fixtures := []struct {
		name string
		yaml []byte
	}{
		{"echo-server", devassets.EchoServerYAML},
		{"blocklist-server", devassets.BlocklistServerYAML},
		{"auth-server", devassets.AuthServerYAML},
		{"haproxy-demo-backend", devassets.HAProxyDemoBackendYAML},
		{"haproxy-test-backend", devassets.HAProxyTestBackendYAML},
	}

	g, gctx := errgroup.WithContext(ctx)
	for _, f := range fixtures {
		f := f
		g.Go(func() error {
			if err := kubectlApplyStdin(gctx, f.yaml); err != nil {
				return fmt.Errorf("apply %s: %w", f.name, err)
			}
			return nil
		})
	}
	return ctx, g.Wait()
}

// teardownCluster destroys the kind cluster unless KEEP_CLUSTER is true
// (default). In CI we also keep so after_script can collect logs.
func teardownCluster(ctx context.Context, provider *kindcluster.Provider) (context.Context, error) {
	if os.Getenv("SKIP_CLUSTER_CREATE") == "true" {
		// CI mode: leave cluster lifecycle to the runner.
		return ctx, nil
	}
	if os.Getenv("KEEP_CLUSTER") != "false" {
		return ctx, nil
	}
	if err := provider.Delete(ClusterName, ""); err != nil {
		fmt.Fprintf(os.Stderr, "e2e: delete kind cluster: %v\n", err)
	}
	return ctx, nil
}

// kubectlApplyStdin pipes a YAML manifest through `kubectl apply -f -`.
// Used for fixture YAMLs that don't need typed Go fixtures.
func kubectlApplyStdin(ctx context.Context, yaml []byte) error {
	cmd := exec.CommandContext(ctx, "kubectl", "apply", "--kubeconfig", kubeconfigPath, "-f", "-")
	cmd.Stdin = bytes.NewReader(yaml)
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("%w (output: %s)", err, out)
	}
	return nil
}

// kubectlGetSecretData returns the .data map of a Secret as a map of
// base64-encoded string values (the same encoding the apiserver wires).
// Returns an error if the Secret is missing or unreadable; the cert-
// reuse path treats any error as "regenerate from scratch", so distinct
// error types are intentionally not surfaced.
func kubectlGetSecretData(ctx context.Context, namespace, name string) (map[string]string, error) {
	cmd := exec.CommandContext(ctx, "kubectl", "get", "secret", name,
		"--kubeconfig", kubeconfigPath, "-n", namespace, "-o", "json")
	out, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("kubectl get secret %s/%s: %w", namespace, name, err)
	}
	var secret struct {
		Data map[string]string `json:"data"`
	}
	if err := json.Unmarshal(out, &secret); err != nil {
		return nil, fmt.Errorf("decode secret %s/%s: %w", namespace, name, err)
	}
	return secret.Data, nil
}

// chartPath returns the absolute path to charts/haptic, walking up from the
// current working directory until it finds the repo root.
func chartPath() (string, error) {
	root, err := repoRoot()
	if err != nil {
		return "", err
	}
	return filepath.Join(root, "charts", "haptic"), nil
}

// chartCRDDir returns the absolute path to the chart's CRDs directory.
func chartCRDDir() (string, error) {
	root, err := repoRoot()
	if err != nil {
		return "", err
	}
	return filepath.Join(root, "charts", "haptic", "crds"), nil
}

// e2eKindConfig is the kind cluster config the e2e suite uses. Distinct
// from scripts/dev-env-assets/kind-config.yaml because the host ports are
// shifted by 1000 (30080→31080, 30443→31443, 30404→31404) so kind-haptic-e2e
// can coexist with the developer's interactive kind-haptic-dev cluster
// (which already binds 30080/30443/30404). The container-side NodePorts
// remain 30080/30443/30404 — that's what the chart configures.
//
// extraPortMappings expose only the chart-static NodePorts (HTTP / HTTPS /
// stats). Conformance tests run as a sibling container on the kind docker
// network (see Dockerfile.conformance-test + `make test-conformance`) so
// they reach MetalLB-allocated LoadBalancer IPs directly without needing
// every random NodePort exported to the host. The e2e Go suite still
// dials via NodePort + DinD remap and only needs these three ports.
//
// DinD compatibility: networking.apiServerAddress, the "docker" certSAN, and
// listenAddress on extraPortMappings are all required when this suite runs
// inside GitLab's docker:dind service container. They are harmless on a
// local developer's docker daemon (the API server still binds locally and
// the extra cert SAN is unused), so the same config works in both
// environments without a runtime branch.
const e2eKindConfig = `kind: Cluster
apiVersion: kind.x-k8s.io/v1alpha4
networking:
  apiServerAddress: "0.0.0.0"
nodes:
  - role: control-plane
    kubeadmConfigPatches:
      - |
        kind: InitConfiguration
        nodeRegistration:
          kubeletExtraArgs:
            node-labels: "ingress-ready=true"
    extraPortMappings:
      - containerPort: 30080
        hostPort: 31080
        protocol: TCP
        listenAddress: "0.0.0.0"
      - containerPort: 30443
        hostPort: 31443
        protocol: TCP
        listenAddress: "0.0.0.0"
      - containerPort: 30404
        hostPort: 31404
        protocol: TCP
        listenAddress: "0.0.0.0"
kubeadmConfigPatches:
  - |
    kind: ClusterConfiguration
    apiServer:
      extraArgs:
        enable-admission-plugins: NodeRestriction,MutatingAdmissionWebhook,ValidatingAdmissionWebhook
  # Raise the kubelet's per-container log rotation cap (default 10Mi).
  # The controller logs at DEBUG during e2e/conformance runs and the
  # leader replica exceeds 10Mi well within one suite, after which
  # "kubectl logs" (used by the CI after_script diagnostics capture)
  # returns only the newest rotated file — job 15180387459's artifacts
  # carried just ~7s of leader logs, none covering the failure window
  # (issue #56). 200Mi buys roughly a minute at the observed ~3 MB/s.
  #
  # It is NOT sufficient on its own: kubectl logs serves only the CURRENT
  # rotated file, so a run longer than that minute still loses its earlier
  # history to this capture path. The CI after_script therefore also reads the
  # rotated files directly off the node (/var/log/pods) into
  # debug-logs/_suite/controller-full.log.gz — that, not this cap, is what
  # makes a whole run retrievable.
  - |
    kind: KubeletConfiguration
    containerLogMaxSize: 200Mi
kubeadmConfigPatchesJSON6902:
  # Both kubeadm config versions: kind applies the one matching the node's
  # k8s version (v1beta3 for <= 1.35, v1beta4 for >= 1.36) and skips the other.
  # The e2e suite uses kind's default 1.36 node (v1beta4); keep both so the
  # SAN lands regardless of node version. Do not collapse to a single version.
  - group: kubeadm.k8s.io
    version: v1beta3
    kind: ClusterConfiguration
    patch: |
      - op: add
        path: /apiServer/certSANs/-
        value: docker
  - group: kubeadm.k8s.io
    version: v1beta4
    kind: ClusterConfiguration
    patch: |
      - op: add
        path: /apiServer/certSANs/-
        value: docker
`

// repoRoot walks up from the current working directory until it finds a
// directory containing go.mod (the repo root).
func repoRoot() (string, error) {
	dir, err := os.Getwd()
	if err != nil {
		return "", err
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir, nil
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", fmt.Errorf("go.mod not found walking up from cwd")
		}
		dir = parent
	}
}
