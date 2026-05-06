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
// haproxy-template-ic. It is self-contained: TestMain creates its own kind
// cluster, helm-installs the chart, and deploys backend fixtures. Nothing
// outside the test binary needs to run first.
package e2e

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"golang.org/x/sync/errgroup"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	"sigs.k8s.io/e2e-framework/pkg/env"
	"sigs.k8s.io/e2e-framework/pkg/envconf"
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
		phase("install-metallb", func(ctx context.Context, cfg *envconf.Config) (context.Context, error) {
			return installMetalLB(ctx)
		}),
		phase("install-crds+certs (parallel)", func(ctx context.Context, cfg *envconf.Config) (context.Context, error) {
			b, err := preInstallParallel(ctx)
			if err != nil {
				return ctx, err
			}
			caBundleB64 = b
			return ctx, nil
		}),
		phase("helm-install", func(ctx context.Context, cfg *envconf.Config) (context.Context, error) {
			return helmInstallChart(ctx, caBundleB64)
		}),
		phase("apply-backend-fixtures (parallel)", func(ctx context.Context, cfg *envconf.Config) (context.Context, error) {
			return applyBackendFixtures(ctx)
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

	saveCmd := exec.CommandContext(ctx, "docker", "save", ControllerImageName)
	importCmd := exec.CommandContext(ctx, "docker", "exec", "-i",
		ClusterName+"-control-plane",
		"ctr", "--namespace=k8s.io", "images", "import", "-")

	pipe, err := saveCmd.StdoutPipe()
	if err != nil {
		return ctx, fmt.Errorf("pipe docker save: %w", err)
	}
	importCmd.Stdin = pipe
	importCmd.Stdout = os.Stderr
	importCmd.Stderr = os.Stderr
	saveCmd.Stderr = os.Stderr

	if err := importCmd.Start(); err != nil {
		return ctx, fmt.Errorf("start ctr import: %w", err)
	}
	if err := saveCmd.Run(); err != nil {
		_ = importCmd.Wait()
		return ctx, fmt.Errorf("docker save %s: %w", ControllerImageName, err)
	}
	if err := importCmd.Wait(); err != nil {
		return ctx, fmt.Errorf("ctr image import: %w", err)
	}
	return ctx, nil
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

// installGatewayAPICRDs installs the upstream Gateway API standard-channel
// CRDs (Gateway, HTTPRoute, GRPCRoute, etc.) so the chart's gateway library
// can register watchers and HTTPRoute-based tests can run.
//
// Idempotent: if CRDs already exist (KEEP_CLUSTER=true reuse), kubectl apply
// is a no-op and the wait condition just returns immediately. Pinned to v1.5.1
// to match scripts/start-dev-env.sh and the vendored
// sigs.k8s.io/gateway-api/conformance@v1.5.1 module — the conformance suite
// refuses to run when CRDs and suite disagree on bundle-version.
func installGatewayAPICRDs(ctx context.Context) (context.Context, error) {
	const url = "https://github.com/kubernetes-sigs/gateway-api/releases/download/v1.5.1/standard-install.yaml"

	apply := exec.CommandContext(ctx, "kubectl", "apply", "--kubeconfig", kubeconfigPath, "-f", url)
	if out, err := apply.CombinedOutput(); err != nil {
		return ctx, fmt.Errorf("install Gateway API CRDs: %w (output: %s)", err, out)
	}

	// Single multi-arg `kubectl wait` — kubectl waits on all four CRDs in
	// parallel, so we don't pay the sequential per-CRD shell-out overhead.
	// The Established check guards against the chart's helm install racing
	// the watcher's CRD lookup.
	wait := exec.CommandContext(ctx, "kubectl", "wait", "--kubeconfig", kubeconfigPath,
		"--for=condition=Established", "--timeout=60s",
		"crd/gatewayclasses.gateway.networking.k8s.io",
		"crd/gateways.gateway.networking.k8s.io",
		"crd/httproutes.gateway.networking.k8s.io",
		"crd/grpcroutes.gateway.networking.k8s.io",
	)
	if out, err := wait.CombinedOutput(); err != nil {
		return ctx, fmt.Errorf("wait for Gateway API CRDs established: %w (output: %s)", err, out)
	}
	return ctx, nil
}

// ensureNamespaces idempotently creates the controller and shared-fixture
// namespaces upfront so the parallel install/fixture phases don't race on
// namespace creation. echo-server.yaml ships its own Namespace block, but
// the other fixtures only reference namespace: echo and would otherwise
// race against echo-server's apply when fanned out.
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
// can pass it as --set webhook.caBundle=...
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
// as the base, layering an image.tag=test override to point at the local
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

	// Write the embedded dev-values.yaml to a temp file so helm can consume it.
	valuesFile, err := os.CreateTemp("", "haptic-e2e-values-*.yaml")
	if err != nil {
		return ctx, fmt.Errorf("create temp values file: %w", err)
	}
	defer os.Remove(valuesFile.Name())
	if _, err := valuesFile.Write(devassets.DevValuesYAML); err != nil {
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
		"--set", "image.tag=test",
		// haproxyVersion gates two things in the chart: the controller image
		// tag suffix (`<image.tag>-haproxy<haproxyVersion>`, e.g.
		// "test-haproxy3.0") and the haproxy:VERSION sidecar image. The
		// Makefile re-tags haptic:test → haptic:test-haproxy${HAPROXY_VERSION}
		// per matrix entry; without forcing the chart's haproxyVersion to
		// match, the chart falls back to its values.yaml default (currently
		// 3.2) and only the 3.2 matrix entry happens to find its image.
		// Every other matrix entry hits ImagePullBackOff.
		"--set", "haproxyVersion=" + ChartHAProxyVersion,
		"--set", "webhook.caBundle=" + caBundleB64,
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
kubeadmConfigPatchesJSON6902:
  - group: kubeadm.k8s.io
    version: v1beta3
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
