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
	"os"
	"time"
)

// Cluster, deployment, and image constants. The e2e suite is self-contained:
// it creates its own kind cluster and helm-installs the chart. None of these
// constants depend on scripts/start-dev-env.sh having been run.
const (
	// ClusterName is the kind cluster the e2e suite owns.
	// The kubectl context is "kind-" + ClusterName. Distinct from the
	// developer's interactive `kind-haptic-dev` cluster so the two don't
	// collide.
	ClusterName = "haptic-e2e"

	// HelmReleaseName is the helm release name used to install the chart.
	HelmReleaseName = "haptic"

	// ControllerNamespace is the namespace the helm release deploys into.
	// Holds the controller, the SPOA hub, and the HAProxy pods.
	ControllerNamespace = "haptic"

	// ControllerDeploymentName follows the helm chart's naming convention:
	// "<release>-controller".
	ControllerDeploymentName = "haptic-controller"

	// HAProxyDeploymentName is the HAProxy Deployment name (SPOA-enabled).
	HAProxyDeploymentName = "haptic-haproxy"

	// HAProxyConfigName is the HAProxyTemplateConfig CRD instance name.
	HAProxyConfigName = "haptic-config"

	// SharedFixturesNamespace holds the stateless backend fixtures
	// (echo-server, auth-server, blocklist-server) that all e2e tests
	// share. Deployed once during TestMain.
	SharedFixturesNamespace = "echo"

	// HTTPHostPort is the host-side TCP port the kind cluster exposes for
	// HAProxy HTTP traffic. Distinct from the dev cluster's 30080 so the
	// two clusters can coexist on a developer's machine. The chart still
	// configures NodePort 30080 *inside* the cluster; kind's
	// extraPortMappings (in the e2e kind config below) translate
	// containerPort 30080 → hostPort 31080.
	HTTPHostPort = 31080

	// HTTPSHostPort is the host-side TCP port for HAProxy HTTPS traffic.
	// Same coexistence reason as HTTPHostPort.
	HTTPSHostPort = 31443

	// StatsHostPort is the host-side TCP port for HAProxy stats.
	StatsHostPort = 31404

	// DebugPort is the controller debug HTTP server port (matches helm
	// chart's controller.debugPort default).
	DebugPort = 8080

	// DebugServiceName is the Service the chart provisions for the
	// controller's debug + metrics endpoints. Named after the helm release
	// (single multi-port Service rather than separate -debug/-metrics
	// services).
	DebugServiceNameValue = HelmReleaseName

	// defaultHAProxyVersion is the fallback used when HAPTIC_HAPROXY_VERSION
	// is unset (i.e., not invoked through `make test-e2e`). Aligned with
	// versions.env's DEFAULT_HAPROXY default. The Makefile passes the
	// authoritative value via the env var.
	defaultHAProxyVersion = "3.2"
)

// ChartHAProxyVersion is the haproxyVersion the e2e suite installs the
// chart with. Sourced from the HAPTIC_HAPROXY_VERSION env var (set by the
// `make test-e2e` target from versions.env's DEFAULT_HAPROXY) so the chart's
// haproxyVersion, the controller image's bundled HAProxy, and the chart's
// expected image tag suffix all stay in lockstep. Falls back to a constant
// when the env var is unset.
var ChartHAProxyVersion = func() string {
	if v := os.Getenv("HAPTIC_HAPROXY_VERSION"); v != "" {
		return v
	}
	return defaultHAProxyVersion
}()

// ControllerImageName is the docker image tag the chart installs.
// docker-build-test produces "haptic:test"; the e2e Makefile target
// re-tags it with the haproxy-version suffix the chart expects.
var ControllerImageName = "haptic:test-haproxy" + ChartHAProxyVersion

// Debug endpoint paths, mirrored from tests/acceptance/constants.go.
const (
	DebugPathConfig    = "/debug/vars/config"
	DebugPathRendered  = "/debug/vars/rendered"
	DebugPathPipeline  = "/debug/vars/pipeline"
	DebugPathValidated = "/debug/vars/validated"
	DebugPathErrors    = "/debug/vars/errors"
	DebugPathEvents    = "/debug/vars/events"
	DebugPathAuxFiles  = "/debug/vars/auxfiles"
)


// Pod label selectors for the chart's standard kubernetes.io recommended
// labels. The chart sets app.kubernetes.io/instance=<release> and
// app.kubernetes.io/component=<role>.
const (
	// LabelSelectorController matches the controller pods.
	LabelSelectorController = "app.kubernetes.io/instance=" + HelmReleaseName +
		",app.kubernetes.io/component=controller"

	// LabelSelectorHAProxy matches the HAProxy pods.
	LabelSelectorHAProxy = "app.kubernetes.io/instance=" + HelmReleaseName +
		",app.kubernetes.io/component=loadbalancer"
)

// Default timeouts used across the suite. Anything that needs a different
// budget passes its own to testutil.WaitConfig.
const (
	// DefaultClusterCreateTimeout caps the kind cluster bring-up wait.
	DefaultClusterCreateTimeout = 5 * time.Minute

	// DefaultHelmInstallTimeout caps `helm install --wait`.
	DefaultHelmInstallTimeout = 5 * time.Minute

	// DefaultEnvironmentReadyTimeout is the budget for the suite-level
	// readiness check (controller pipeline succeeded, all HAProxy pods
	// acknowledged the deployed config).
	DefaultEnvironmentReadyTimeout = 3 * time.Minute

	// DefaultPerTestSetupTimeout is the budget for per-test fixture
	// application + reaching steady-state for the new manifests.
	DefaultPerTestSetupTimeout = 90 * time.Second

	// DefaultRequestRetryBudget is the wall-clock budget for an HTTP probe
	// to succeed under the client's exponential-backoff retry policy.
	DefaultRequestRetryBudget = 30 * time.Second
)
