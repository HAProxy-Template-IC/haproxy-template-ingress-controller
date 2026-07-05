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
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"golang.org/x/sync/errgroup"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/kubernetes"
	"sigs.k8s.io/e2e-framework/klient"
	"sigs.k8s.io/e2e-framework/pkg/envconf"
	"sigs.k8s.io/e2e-framework/pkg/features"

	hapticv1alpha1 "gitlab.com/haproxy-haptic/haptic/pkg/apis/haproxytemplate/v1alpha1"
	"gitlab.com/haproxy-haptic/haptic/pkg/compression"
	coreconfig "gitlab.com/haproxy-haptic/haptic/pkg/core/config"
	hapticclient "gitlab.com/haproxy-haptic/haptic/pkg/generated/clientset/versioned"
	"gitlab.com/haproxy-haptic/haptic/tests/e2e/httpclient"
	"gitlab.com/haproxy-haptic/haptic/tests/testutil"
)

// Scale/performance tier environment variables. The tier is opt-in: the
// nightly-scale CI job sets scaleEnableEnv; ordinary e2e runs (MR pipelines,
// local `make test-e2e`) skip the test entirely.
//
// Sizing arithmetic (why the defaults yield a 10k+ line haproxy.cfg): every
// Ingress backend renders as its own `backend <ns>_<name>_svc_<svc>_<port>`
// stanza (the chart's first_seen dedup keys on the INGRESS name, not the
// Service — see charts/haptic/libraries/ingress.yaml util-generate-backends-
// ingress), containing a comment line, the backend + guid lines, a
// default-server line, and 10 server-slot lines (base.yaml's util-backend-
// servers default `slots = 10`): >= 14 lines per Ingress. 20 namespaces x 40
// Ingresses = 800 backends x 14+ = 11,200+ lines from Ingress backends alone,
// plus the chart baseline (frontend/global/defaults/spoa sections, ~470
// lines) and 20 Gateway binds + `gtw_*` route backends on top: ~12k lines at
// defaults — past the project bar of judging config-apply against 10k+ line
// configs. Cross-checked against a reduced-scale smoke run (4x10 Ingresses
// + 4 Gateways + 5 latency probes = 49 Ingress backends): 1,160 rendered
// lines / 125 KB, i.e. ~108 B/line, extrapolating to ~1.3 MB at defaults —
// over the 1 MiB compression threshold, so the defaults also exercise the
// spec.content compression path (decompressed by this tier's waits).
const (
	// scaleEnableEnv gates the whole tier. Must be "1" to run. Also read by
	// TestMain's helmInstallChart, which then raises the controller memory
	// limit (so the RSS budget is measurable instead of the pod OOM-dying at
	// the dev-oriented 512Mi) and pins INFO logging (production-representative
	// measurement; DEBUG log volume at 800+ resources distorts timings).
	scaleEnableEnv = "HAPTIC_E2E_SCALE"
	// scaleNamespacesEnv tunes how many Ingress-bearing namespaces to seed.
	scaleNamespacesEnv = "SCALE_NAMESPACES"
	// scaleIngressPerNSEnv tunes how many Ingresses each namespace gets.
	scaleIngressPerNSEnv = "SCALE_INGRESSES_PER_NS"
	// scaleGatewaysEnv tunes how many Gateways (each with one HTTPRoute) to seed.
	scaleGatewaysEnv = "SCALE_GATEWAYS"
	// scaleSeedWorkersEnv bounds the seeding concurrency (parallel creates).
	scaleSeedWorkersEnv = "SCALE_SEED_WORKERS"

	scaleDefaultNamespaces   = 20
	scaleDefaultIngressPerNS = 40
	scaleDefaultGateways     = 20
	scaleDefaultSeedWorkers  = 8

	// scaleChangeSamples is how many single-change convergence measurements
	// the tier takes at full scale. 5 keeps the phase short while giving a
	// meaningful median; p95 over 5 samples is the max by nearest-rank.
	scaleChangeSamples = 5
)

// Budget environment variables (all env-overridable so a one-off run on
// weaker hardware can relax them without a code change) and their defaults.
//
// Rationale for each default:
//
//   - Change p95 <= 15s: the repo-wide reaction doctrine. The ordinary e2e
//     suite enforces 12s (controllerDeployedTimeout) at small scale; the
//     scale tier grants +3s because a single change at 800 backends pays a
//     full-size render + admission dry-run + a ~1 MiB CRD publish before the
//     deploy. Anything beyond 15s means routine changes on a large cluster
//     feel sluggish — exactly the regression this tier exists to catch.
//   - Seed <= 600s: 820 resources, each admission-webhook-validated (a
//     dry-run render each) and folded into batched reconciles. Measured well
//     under half of this on CI-class hardware; 600s is ~2x headroom. Blowing
//     it means super-linear render/deploy cost in the resource count.
//   - RSS <= 1 GiB: the controller holds the resource stores, the rendered
//     ~1-2 MiB config, parser caches, and per-iteration Prometheus state.
//     At 10k+ lines that measures in the low hundreds of MiB; 1 GiB catches
//     leaks and accidental O(n^2) blowups while staying deployable on
//     ordinary nodes. (TestMain raises the container limit to 2Gi for this
//     tier so the budget is measured, not masked by an OOM kill.)
//   - Compression: not tunable — an invariant. spec.content must be
//     compressed if and only if the rendered config exceeds the configured
//     compressionThreshold (chart/CRD default 1 MiB).
const (
	scaleBudgetChangeP95Env = "SCALE_BUDGET_CHANGE_P95_SECONDS"
	scaleBudgetSeedEnv      = "SCALE_BUDGET_SEED_SECONDS"
	scaleBudgetRSSEnv       = "SCALE_BUDGET_RSS_BYTES"

	scaleDefaultBudgetChangeP95Seconds = 15
	scaleDefaultBudgetSeedSeconds      = 600
	scaleDefaultBudgetRSSBytes         = int64(1) << 30 // 1 GiB
)

// scaleMetricsFile is where the tier writes its flat-key metrics JSON,
// relative to the repo root. The nightly-scale CI job uploads it as an
// always-artifact and trend-compares it against the previous main run.
const scaleMetricsFile = "scale-metrics.json"

// TestScale is the scale/performance validation tier: seed a 10k+ line
// haproxy.cfg worth of routing resources, verify the system converges and
// routes, then measure and budget-assert the numbers that matter at scale:
//
//	(a) seed SCALE_NAMESPACES x SCALE_INGRESSES_PER_NS Ingresses (each its
//	    own chart backend) + SCALE_GATEWAYS Gateways with one HTTPRoute each,
//	    with bounded concurrency; wait for FULL convergence (every backend
//	    marker present in the deployed HAProxyCfg content on every HAProxy
//	    pod — one batch condition, not per-Ingress polling); spot-check real
//	    routing on a sample via NodePort (Ingress) and ForwardGateway.
//	(b) measure: seed→converged wall time; single-change convergence latency
//	    at full scale (create 1 Ingress, time create→deployed-marker and
//	    create→routed, x5, median/p95 — THE key number); rendered config
//	    line count, HAProxyCfg spec size, compression state; controller
//	    container memory (kubelet stats summary via the apiserver node
//	    proxy — headless, no metrics-server dependency); HAProxy reload
//	    count over the run and render/deploy durations (controller /metrics).
//	(c) assert explicit env-overridable budgets (see the budget block above).
//	(d) write everything to scale-metrics.json (flat keys) in the repo root.
//
// All waits are condition-based via testutil.WaitConfig; assertions are
// convergence contracts or budget bounds, never interleavings — scheduling-
// independent per tests/CLAUDE.md.
//
// Deliberately NOT t.Parallel(): the tier owns the cluster's entire load
// budget; a sibling test mutating resources would distort every measurement.
// The nightly job runs it as the only test in the binary
// (TEST_RUN_PATTERN=TestScale).
func TestScale(t *testing.T) {
	if v, _ := lookupEnv(scaleEnableEnv); v != "1" {
		t.Skipf("%s != 1 — scale/performance tier only runs in the nightly-scale job", scaleEnableEnv)
	}

	namespaceCount := envInt(t, scaleNamespacesEnv, scaleDefaultNamespaces)
	ingressPerNS := envInt(t, scaleIngressPerNSEnv, scaleDefaultIngressPerNS)
	gatewayCount := envInt(t, scaleGatewaysEnv, scaleDefaultGateways)
	seedWorkers := envInt(t, scaleSeedWorkersEnv, scaleDefaultSeedWorkers)
	budgetChangeP95 := time.Duration(envInt(t, scaleBudgetChangeP95Env, scaleDefaultBudgetChangeP95Seconds)) * time.Second
	budgetSeed := time.Duration(envInt(t, scaleBudgetSeedEnv, scaleDefaultBudgetSeedSeconds)) * time.Second
	budgetRSS := envInt64(t, scaleBudgetRSSEnv, scaleDefaultBudgetRSSBytes)
	totalIngresses := namespaceCount * ingressPerNS

	// Evidence preservation: metrics are accumulated into the sink AS they
	// are produced, and this cleanup flushes whatever has been measured if a
	// t.Fatalf aborts the run before the final Assess writes the file (the
	// flush is idempotent, so the happy path's explicit write wins and this
	// becomes a no-op). The CI job uploads scale-metrics.json with
	// `when: always`, so even a failed run ships its partial numbers.
	sink := newScaleMetricsSink()
	sink.set("timestamp", time.Now().UTC().Format(time.RFC3339))
	sink.set("haproxy_version", ChartHAProxyVersion)
	sink.set("scale_namespaces", namespaceCount)
	sink.set("scale_ingresses_per_ns", ingressPerNS)
	sink.set("scale_ingresses_total", totalIngresses)
	sink.set("scale_gateways", gatewayCount)
	sink.set("budget_seed_seconds", budgetSeed.Seconds())
	sink.set("budget_change_p95_seconds", budgetChangeP95.Seconds())
	sink.set("budget_rss_bytes", budgetRSS)
	t.Cleanup(func() {
		path, wrote, err := sink.flush()
		switch {
		case err != nil:
			t.Logf("evidence-preservation flush of %s failed: %v", scaleMetricsFile, err)
		case wrote:
			t.Logf("partial scale metrics flushed to %s (run ended before the final Assess wrote them)", path)
		}
	})

	var (
		cs  kubernetes.Interface
		dyn dynamic.Interface
		hc  hapticclient.Interface

		ingressNamespaces []string
		gatewayNS         string
		probeNS           string

		haproxyReplicas int

		// markers is the full set of chart-emitted backend-name prefixes the
		// convergence wait requires in the deployed config (one per Ingress,
		// one per Gateway's HTTPRoute).
		markers []string
		// sampleIngressHosts / sampleGateways are the routing spot-check picks.
		sampleIngressHosts []string
		sampleGateways     []string

		reloadsBefore map[string]float64

		seedDuration    time.Duration
		markerDurations []time.Duration
		routedDurations []time.Duration
	)

	feature := features.New(fmt.Sprintf("Scale tier: %d ns x %d Ingresses + %d Gateways, budget-asserted",
		namespaceCount, ingressPerNS, gatewayCount)).
		Setup(func(ctx context.Context, t *testing.T, cfg *envconf.Config) context.Context {
			client, err := cfg.NewClient()
			if err != nil {
				t.Fatalf("new client: %v", err)
			}
			cs, err = newClientsetForE2E(client.RESTConfig())
			if err != nil {
				t.Fatalf("build clientset: %v", err)
			}
			dyn, err = newDynamicForE2E(client.RESTConfig())
			if err != nil {
				t.Fatalf("build dynamic client: %v", err)
			}
			hc, err = hapticclient.NewForConfig(client.RESTConfig())
			if err != nil {
				t.Fatalf("build haptic clientset: %v", err)
			}

			haproxyReplicas, err = discoverHAProxyReplicaCount(ctx, client)
			if err != nil {
				t.Fatalf("discover HAProxy replica count: %v", err)
			}
			sink.set("haproxy_replicas", haproxyReplicas)

			// Probe namespace: hosts the single-change latency Ingresses. The
			// only namespace worth per-test log capture — the 20+ seed
			// namespaces would just multiply identical dumps (the CI
			// after_script captures suite-level controller/HAProxy logs).
			probeNS = NamespaceForTest(ctx, t, client)
			DumpLogsOnFailure(t, probeNS)
			NewEchoServerBackend(ctx, t, client, probeNS)

			// Seed namespaces: create them all plus their echo backends
			// WITHOUT waiting per-namespace, then wait for every backend's
			// endpoint in one condition. This is fixture bring-up (pod
			// scheduling + image start), deliberately outside the measured
			// seed window — the system under test is haptic, not kubelet.
			for i := 0; i < namespaceCount; i++ {
				ns := NamespaceForTest(ctx, t, client)
				if err := applyEchoServerBackend(ctx, client, ns); err != nil {
					t.Fatalf("seed namespace %s: %v", ns, err)
				}
				ingressNamespaces = append(ingressNamespaces, ns)
			}
			gatewayNS = NamespaceForTest(ctx, t, client)
			if err := applyEchoServerBackend(ctx, client, gatewayNS); err != nil {
				t.Fatalf("gateway namespace %s: %v", gatewayNS, err)
			}
			waitAllEchoBackendsReady(ctx, t, client, append(append([]string{}, ingressNamespaces...), gatewayNS))

			// Reload-counter baseline BEFORE any load, so the reported delta
			// covers the whole run (seed + latency probes).
			reloadsBefore = snapshotReloadCounters(ctx, t, cs)
			return ctx
		}).
		Assess("seed to full convergence within budget-bounded wait", func(ctx context.Context, t *testing.T, cfg *envconf.Config) context.Context {
			seedStart := time.Now()

			g, gctx := errgroup.WithContext(ctx)
			g.SetLimit(seedWorkers)
			for nsIdx, ns := range ingressNamespaces {
				for i := 0; i < ingressPerNS; i++ {
					name := fmt.Sprintf("ing-%03d", i)
					host := fmt.Sprintf("s%02d-i%03d.scale.localdev.me", nsIdx, i)
					markers = append(markers, ingressBackendMarker(ns, name))
					g.Go(func() error {
						return createScaleIngress(gctx, cs, ns, IngressSpec{
							Name:           name,
							Host:           host,
							BackendService: EchoServerBackend.Service,
							BackendPort:    EchoServerBackend.Port,
						})
					})
					switch {
					case nsIdx == 0 && i == 0,
						nsIdx == len(ingressNamespaces)/2 && i == ingressPerNS/2,
						nsIdx == len(ingressNamespaces)-1 && i == ingressPerNS-1:
						sampleIngressHosts = append(sampleIngressHosts, host)
					}
				}
			}
			for gi := 0; gi < gatewayCount; gi++ {
				gwName := fmt.Sprintf("gw-%02d", gi)
				host := fmt.Sprintf("%s.scale.localdev.me", gwName)
				markers = append(markers, gatewayBackendMarker(gatewayNS, gwName))
				if gi == 0 || gi == gatewayCount-1 {
					sampleGateways = append(sampleGateways, gwName)
				}
				g.Go(func() error {
					if err := createChurnGateway(gctx, dyn, gatewayNS, gwName); err != nil {
						return fmt.Errorf("create Gateway %s/%s: %w", gatewayNS, gwName, err)
					}
					if err := createChurnHTTPRoute(gctx, dyn, gatewayNS, gwName, host); err != nil {
						return fmt.Errorf("create HTTPRoute %s/%s: %w", gatewayNS, gwName, err)
					}
					return nil
				})
			}
			if err := g.Wait(); err != nil {
				t.Fatalf("seeding failed: %v", err)
			}
			t.Logf("seeded %d Ingresses across %d namespaces + %d Gateways in %s (creates only)",
				totalIngresses, namespaceCount, gatewayCount, time.Since(seedStart).Round(time.Second))

			// Batch convergence: ONE wait over the whole marker set. The wait
			// gets 1.5x the seed budget so a slow-but-converging run still
			// produces measurements and a scale-metrics.json (the budget
			// assertion fails afterwards with the real number); only a run
			// that can't even converge at 1.5x dies here.
			waitCfg := testutil.WaitConfig{
				InitialInterval: time.Second,
				MaxInterval:     3 * time.Second,
				Timeout:         budgetSeed + budgetSeed/2,
				Multiplier:      1.2,
			}
			if err := waitForMarkersDeployed(ctx, hc, haproxyReplicas, markers, waitCfg,
				fmt.Sprintf("all %d backend markers deployed to %d HAProxy pods", len(markers), haproxyReplicas)); err != nil {
				t.Fatalf("seed convergence: %v", err)
			}
			seedDuration = time.Since(seedStart)
			sink.set("seed_to_converged_seconds", round2(seedDuration.Seconds()))
			t.Logf("seed -> full convergence: %s", seedDuration.Round(time.Millisecond))
			return ctx
		}).
		Assess("routing spot-check on a sample", func(ctx context.Context, t *testing.T, cfg *envconf.Config) context.Context {
			for _, host := range sampleIngressHosts {
				resp := httpclient.New(t).GET(host, "/").ExpectOK(t)
				if resp.Echo == nil {
					t.Fatalf("spot-check %s: expected echo-server JSON, got %d bytes", host, len(resp.Body))
				}
			}
			for _, gw := range sampleGateways {
				fwd := ForwardGateway(ctx, t, gatewayNS, gw, 80)
				host := fmt.Sprintf("%s.scale.localdev.me", gw)
				resp := httpclient.ForForwarded(t, fwd.HTTPPort, 0).GET(host, "/").ExpectOK(t)
				if resp.Echo == nil {
					t.Fatalf("spot-check Gateway %s: expected echo-server JSON, got %d bytes", gw, len(resp.Body))
				}
			}
			return ctx
		}).
		Assess("single-change convergence latency at full scale", func(ctx context.Context, t *testing.T, cfg *envconf.Config) context.Context {
			// Per-sample wait: fine-grained polling for timing resolution
			// (<=500ms granularity vs a 15s budget), capped well past the
			// budget so a slow sample is MEASURED and fails the budget assert
			// with its real value instead of dying inside the wait.
			perSampleTimeout := 2 * time.Minute
			if 4*budgetChangeP95 > perSampleTimeout {
				perSampleTimeout = 4 * budgetChangeP95
			}
			for k := 1; k <= scaleChangeSamples; k++ {
				name := fmt.Sprintf("probe-%d", k)
				host := fmt.Sprintf("scale-probe-%d.localdev.me", k)
				start := time.Now()
				if err := createScaleIngress(ctx, cs, probeNS, IngressSpec{
					Name:           name,
					Host:           host,
					BackendService: EchoServerBackend.Service,
					BackendPort:    EchoServerBackend.Port,
				}); err != nil {
					t.Fatalf("latency sample %d: %v", k, err)
				}
				waitCfg := testutil.WaitConfig{
					InitialInterval: 100 * time.Millisecond,
					MaxInterval:     500 * time.Millisecond,
					Timeout:         perSampleTimeout,
					Multiplier:      1.3,
				}
				if err := waitForMarkersDeployed(ctx, hc, haproxyReplicas,
					[]string{ingressBackendMarker(probeNS, name)}, waitCfg,
					fmt.Sprintf("probe Ingress %s deployed to all HAProxy pods", name)); err != nil {
					t.Fatalf("latency sample %d: %v", k, err)
				}
				markerDur := time.Since(start)
				// Marker-deployed already implies every pod reloaded the
				// probe's backend; the HTTP poll closes the last gap to
				// "actually routed" (NodePort round-robin across pods).
				httpclient.New(t).GET(host, "/").ExpectOK(t)
				routedDur := time.Since(start)
				markerDurations = append(markerDurations, markerDur)
				routedDurations = append(routedDurations, routedDur)
				// Record the sample and the running aggregates immediately so
				// an abort mid-loop still ships every measured sample.
				sink.set(fmt.Sprintf("change_convergence_seconds_sample_%d", k), round2(routedDur.Seconds()))
				sink.set("change_convergence_seconds_median", round2(durationPercentile(routedDurations, 50).Seconds()))
				sink.set("change_convergence_seconds_p95", round2(durationPercentile(routedDurations, 95).Seconds()))
				sink.set("change_marker_seconds_median", round2(durationPercentile(markerDurations, 50).Seconds()))
				sink.set("change_marker_seconds_p95", round2(durationPercentile(markerDurations, 95).Seconds()))
				t.Logf("latency sample %d: create->deployed %s, create->routed %s",
					k, markerDur.Round(time.Millisecond), routedDur.Round(time.Millisecond))
			}
			return ctx
		}).
		Assess("collect metrics, write scale-metrics.json, assert budgets", func(ctx context.Context, t *testing.T, cfg *envconf.Config) context.Context {
			changeMedian := durationPercentile(routedDurations, 50)
			changeP95 := durationPercentile(routedDurations, 95)

			// Final rendered-config shape, straight from the HAProxyCfg CR.
			// Each measurement lands in the sink the moment it exists, so a
			// Fatal on a later step still preserves the earlier ones.
			cfgName := HAProxyConfigName + "-haproxycfg"
			obj, err := hc.HaproxyTemplateICV1alpha1().HAProxyCfgs(ControllerNamespace).Get(ctx, cfgName, metav1.GetOptions{})
			if err != nil {
				t.Fatalf("get HAProxyCfg: %v", err)
			}
			content, err := haproxyCfgContent(obj)
			if err != nil {
				t.Fatalf("decode HAProxyCfg content: %v", err)
			}
			configLines := strings.Count(content, "\n") + 1
			sink.set("config_lines", configLines)
			sink.set("config_bytes", len(content))
			sink.set("haproxycfg_spec_content_bytes", len(obj.Spec.Content))
			sink.set("compression_engaged", obj.Spec.Compressed)

			threshold := compressionThreshold(ctx, t, hc)
			sink.set("compression_threshold_bytes", threshold)

			// Controller container memory: kubelet stats summary. workingSet
			// is the number kubectl top and the OOM killer act on — that is
			// the budgeted value; plain RSS is recorded alongside.
			workingSet, procRSS := maxControllerMemory(ctx, t, cs)
			sink.set("controller_rss_bytes", workingSet)
			sink.set("controller_memory_rss_proc_bytes", procRSS)

			// Reload + duration counters from the controller's /metrics.
			reloadsAfter := snapshotReloadCounters(ctx, t, cs)
			reloadDelta := reloadCounterDelta(reloadsBefore, reloadsAfter)
			sink.set("haproxy_reloads_total_delta", reloadDelta)
			for key, metric := range map[string]string{
				"reconciliation_duration_seconds_avg":  "haptic_reconciliation_duration_seconds",
				"deployment_duration_seconds_avg":      "haptic_deployment_duration_seconds",
				"webhook_request_duration_seconds_avg": "haptic_webhook_request_duration_seconds",
			} {
				if avg, ok := controllerHistogramAvg(ctx, cs, metric); ok {
					sink.set(key, round2(avg))
				}
			}

			path, _, err := sink.flush()
			if err != nil {
				t.Fatalf("write scale metrics: %v", err)
			}
			t.Logf("scale metrics written to %s", path)
			t.Logf("scale metrics: %d config lines, %d B uncompressed (compressed=%v, threshold=%d B), "+
				"seed=%s, change median=%s p95=%s, controller workingSet=%d MiB, reloads=%.0f",
				configLines, len(content), obj.Spec.Compressed, threshold,
				seedDuration.Round(time.Second), changeMedian.Round(time.Millisecond),
				changeP95.Round(time.Millisecond), workingSet/(1<<20), reloadDelta)

			// ── Budget assertions (metrics JSON is already on disk, so a
			// failing budget still ships full artifacts). ──
			if seedDuration > budgetSeed {
				t.Errorf("BUDGET: seed->converged %s exceeds %s (%s to relax)",
					seedDuration.Round(time.Second), budgetSeed, scaleBudgetSeedEnv)
			}
			if changeP95 > budgetChangeP95 {
				t.Errorf("BUDGET: single-change convergence p95 %s exceeds %s at full scale (%s to relax)",
					changeP95.Round(time.Millisecond), budgetChangeP95, scaleBudgetChangeP95Env)
			}
			if workingSet > uint64(budgetRSS) {
				t.Errorf("BUDGET: controller workingSet %d bytes exceeds %d (%s to relax)",
					workingSet, budgetRSS, scaleBudgetRSSEnv)
			}
			// Compression invariant: engaged iff the rendered content is over
			// the threshold. (The publisher also skips compression when it
			// wouldn't shrink the payload; for haproxy.cfg text zstd always
			// shrinks by >80%, so the iff holds.)
			if wantCompressed := int64(len(content)) > threshold; obj.Spec.Compressed != wantCompressed {
				t.Errorf("compression invariant violated: content %d B vs threshold %d B, want compressed=%v got %v",
					len(content), threshold, wantCompressed, obj.Spec.Compressed)
			}
			return ctx
		}).
		Feature()

	testEnv.Test(t, feature)
}

// ingressBackendMarker is the chart-emitted backend-name prefix for an
// Ingress (see BackendNameIngress in charts/haptic/libraries/ingress.yaml:
// `<ns>_<ingressName>_svc_<service>_<portId>`). Matching on the prefix keeps
// the marker independent of port-name resolution details.
func ingressBackendMarker(namespace, ingressName string) string {
	return namespace + "_" + ingressName + "_svc_"
}

// gatewayBackendMarker is the chart-emitted backend-name prefix for an
// HTTPRoute (see BackendNameGateway in charts/haptic/charts/gateway/
// 21-route-helpers.yaml: `gtw_<ns>_<routeName>_<svc>_<port>`).
func gatewayBackendMarker(namespace, routeName string) string {
	return "gtw_" + namespace + "_" + routeName + "_"
}

// createScaleIngress creates one Ingress via the typed client, retrying
// transient failures. Under seed load the admission webhook (which dry-run
// renders the full config per request) can exceed its timeout; a bounded
// retry with backoff absorbs that without hiding persistent failures.
func createScaleIngress(ctx context.Context, cs kubernetes.Interface, namespace string, spec IngressSpec) error {
	ing := buildIngress(namespace, spec)
	var lastErr error
	for attempt := 1; attempt <= 3; attempt++ {
		_, err := cs.NetworkingV1().Ingresses(namespace).Create(ctx, ing, metav1.CreateOptions{})
		if err == nil {
			return nil
		}
		lastErr = err
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(time.Duration(attempt) * time.Second):
		}
	}
	return fmt.Errorf("create Ingress %s/%s (3 attempts): %w", namespace, spec.Name, lastErr)
}

// waitAllEchoBackendsReady blocks until every listed namespace's echo-server
// Service has at least one ready endpoint — one condition over the whole
// fleet instead of a sequential per-namespace wait.
func waitAllEchoBackendsReady(ctx context.Context, t *testing.T, client klient.Client, namespaces []string) {
	t.Helper()
	cfg := testutil.FastWaitConfig()
	cfg.Timeout = 3 * time.Minute // dozens of pods on a cold CI node
	pending := map[string]bool{}
	for _, ns := range namespaces {
		pending[ns] = true
	}
	if err := testutil.WaitForConditionWithDescription(ctx, cfg,
		fmt.Sprintf("echo-server endpoints ready in all %d namespaces", len(namespaces)),
		func(ctx context.Context) (bool, error) {
			for ns := range pending {
				if err := serviceHasReadyEndpoint(ctx, client, ns, EchoServerBackend.Service); err != nil {
					return false, fmt.Errorf("%d namespaces pending (e.g. %s: %v)", len(pending), ns, err)
				}
				delete(pending, ns)
			}
			return true, nil
		}); err != nil {
		t.Fatalf("seed backends not ready: %v", err)
	}
}

// waitForMarkersDeployed blocks until a single HAProxyCfg render whose
// (decompressed) content contains EVERY marker has been deployed to at least
// expectedReplicas HAProxy pods, with every reported pod at such a render.
//
// Convergence logic mirrors waitForControllerDeployed's membership test:
// checksums observed to be marker-complete are accumulated across polls (a
// content checksum uniquely identifies a render, so once complete, always
// complete), and pods must sit on one of them. Unlike the ordinary-suite
// helper this one (a) takes the whole marker set in one batch, (b)
// decompresses spec.content when the scale config crosses the compression
// threshold, and (c) takes a caller-owned WaitConfig — seed convergence and
// single-change latency need very different timeout/poll shapes.
func waitForMarkersDeployed(ctx context.Context, hc hapticclient.Interface, expectedReplicas int, markers []string, cfg testutil.WaitConfig, description string) error {
	cfgName := HAProxyConfigName + "-haproxycfg"
	completeChecksums := map[string]struct{}{}
	return testutil.WaitForConditionWithDescription(ctx, cfg, description,
		func(ctx context.Context) (bool, error) {
			obj, err := hc.HaproxyTemplateICV1alpha1().HAProxyCfgs(ControllerNamespace).Get(ctx, cfgName, metav1.GetOptions{})
			if err != nil {
				return false, err
			}
			if obj.Spec.Checksum == "" {
				return false, fmt.Errorf("HAProxyCfg.spec.checksum not populated yet")
			}
			content, err := haproxyCfgContent(obj)
			if err != nil {
				return false, err
			}
			missing := 0
			firstMissing := ""
			for _, m := range markers {
				if !strings.Contains(content, m) {
					if firstMissing == "" {
						firstMissing = m
					}
					missing++
				}
			}
			if missing > 0 {
				return false, fmt.Errorf("%d/%d markers not yet in rendered config (first missing: %s)",
					missing, len(markers), firstMissing)
			}
			completeChecksums[obj.Spec.Checksum] = struct{}{}
			deployed := obj.Status.DeployedToPods
			if len(deployed) < expectedReplicas {
				return false, fmt.Errorf("only %d/%d HAProxy pods reported deployed", len(deployed), expectedReplicas)
			}
			for _, p := range deployed {
				if _, ok := completeChecksums[p.Checksum]; !ok {
					return false, fmt.Errorf("pod %s at checksum %q, not yet a marker-complete render (spec %q)",
						p.PodName, p.Checksum, obj.Spec.Checksum)
				}
			}
			return true, nil
		})
}

// haproxyCfgContent returns the HAProxyCfg's rendered content, decompressing
// the zstd+base64 encoding when spec.compressed is set (which the scale
// config legitimately triggers once it crosses the compression threshold).
func haproxyCfgContent(obj *hapticv1alpha1.HAProxyCfg) (string, error) {
	if !obj.Spec.Compressed {
		return obj.Spec.Content, nil
	}
	content, err := compression.Decompress(obj.Spec.Content)
	if err != nil {
		return "", fmt.Errorf("decompress HAProxyCfg content: %w", err)
	}
	return content, nil
}

// compressionThreshold returns the effective HAProxyCfg compression
// threshold: the HAProxyTemplateConfig's configured value, or the compiled
// default when unset — the same resolution the config publisher applies.
func compressionThreshold(ctx context.Context, t *testing.T, hc hapticclient.Interface) int64 {
	t.Helper()
	tc, err := hc.HaproxyTemplateICV1alpha1().HAProxyTemplateConfigs(ControllerNamespace).Get(ctx, HAProxyConfigName, metav1.GetOptions{})
	if err != nil {
		t.Fatalf("get HAProxyTemplateConfig: %v", err)
	}
	if v := tc.Spec.Controller.ConfigPublishing.CompressionThreshold; v > 0 {
		return v
	}
	return coreconfig.DefaultCompressionThreshold
}

// maxControllerMemory returns the maximum controller-container workingSet
// and RSS across all controller replicas, read from the kubelet stats
// summary API through the apiserver's node proxy. Headless: no metrics-
// server, no exec into the (shell-less) controller image.
func maxControllerMemory(ctx context.Context, t *testing.T, cs kubernetes.Interface) (workingSet, rss uint64) {
	t.Helper()
	pods := controllerPodNames(ctx, t, cs)
	nodes, err := cs.CoreV1().Nodes().List(ctx, metav1.ListOptions{})
	if err != nil {
		t.Fatalf("list nodes: %v", err)
	}
	type containerStats struct {
		Name   string `json:"name"`
		Memory struct {
			WorkingSetBytes *uint64 `json:"workingSetBytes"`
			RSSBytes        *uint64 `json:"rssBytes"`
		} `json:"memory"`
	}
	type podStats struct {
		PodRef struct {
			Name      string `json:"name"`
			Namespace string `json:"namespace"`
		} `json:"podRef"`
		Containers []containerStats `json:"containers"`
	}
	for _, node := range nodes.Items {
		raw, err := cs.CoreV1().RESTClient().Get().
			Resource("nodes").Name(node.Name).
			SubResource("proxy").Suffix("stats/summary").
			DoRaw(ctx)
		if err != nil {
			t.Fatalf("kubelet stats summary for node %s: %v", node.Name, err)
		}
		var summary struct {
			Pods []podStats `json:"pods"`
		}
		if err := json.Unmarshal(raw, &summary); err != nil {
			t.Fatalf("decode stats summary for node %s: %v", node.Name, err)
		}
		for _, p := range summary.Pods {
			if p.PodRef.Namespace != ControllerNamespace || !pods[p.PodRef.Name] {
				continue
			}
			for _, c := range p.Containers {
				if c.Name != "controller" {
					continue
				}
				if c.Memory.WorkingSetBytes != nil && *c.Memory.WorkingSetBytes > workingSet {
					workingSet = *c.Memory.WorkingSetBytes
				}
				if c.Memory.RSSBytes != nil && *c.Memory.RSSBytes > rss {
					rss = *c.Memory.RSSBytes
				}
			}
		}
	}
	if workingSet == 0 {
		t.Fatalf("kubelet stats summary carried no controller-container memory data for pods %v", podNamesSorted(pods))
	}
	return workingSet, rss
}

// controllerPodNames returns the current controller pod names as a set.
func controllerPodNames(ctx context.Context, t *testing.T, cs kubernetes.Interface) map[string]bool {
	t.Helper()
	list, err := cs.CoreV1().Pods(ControllerNamespace).List(ctx, metav1.ListOptions{
		LabelSelector: LabelSelectorController,
	})
	if err != nil {
		t.Fatalf("list controller pods: %v", err)
	}
	if len(list.Items) == 0 {
		t.Fatalf("no controller pods match %q", LabelSelectorController)
	}
	names := map[string]bool{}
	for i := range list.Items {
		names[list.Items[i].Name] = true
	}
	return names
}

func podNamesSorted(pods map[string]bool) []string {
	names := make([]string, 0, len(pods))
	for n := range pods {
		names = append(names, n)
	}
	sort.Strings(names)
	return names
}

// scrapeControllerMetrics fetches one controller pod's /metrics (port 9090
// via the apiserver pod proxy) and parses every UNlabelled sample into a
// name -> value map. Labelled families aren't needed by this tier.
func scrapeControllerMetrics(ctx context.Context, cs kubernetes.Interface, pod string) (map[string]float64, error) {
	body, err := cs.CoreV1().Pods(ControllerNamespace).ProxyGet(
		"http", pod, strconv.Itoa(ControllerMetricsPort), "/metrics", nil,
	).DoRaw(ctx)
	if err != nil {
		return nil, fmt.Errorf("scrape %s/metrics: %w", pod, err)
	}
	out := map[string]float64{}
	for _, line := range strings.Split(string(body), "\n") {
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) != 2 || strings.ContainsRune(fields[0], '{') {
			continue
		}
		if v, err := strconv.ParseFloat(fields[1], 64); err == nil {
			out[fields[0]] = v
		}
	}
	return out, nil
}

// snapshotReloadCounters reads haptic_haproxy_reloads_total from every
// controller pod (only the leader increments it, but which pod leads can
// change between snapshots). Per-pod scrape failures are tolerated — a
// follower whose metrics server isn't up yet must not fail the run — but at
// least one pod must answer.
func snapshotReloadCounters(ctx context.Context, t *testing.T, cs kubernetes.Interface) map[string]float64 {
	t.Helper()
	snapshot := map[string]float64{}
	scraped := 0
	for pod := range controllerPodNames(ctx, t, cs) {
		metrics, err := scrapeControllerMetrics(ctx, cs, pod)
		if err != nil {
			t.Logf("reload-counter snapshot: %v (tolerated)", err)
			continue
		}
		scraped++
		snapshot[pod] = metrics["haptic_haproxy_reloads_total"]
	}
	if scraped == 0 {
		t.Fatalf("reload-counter snapshot: no controller pod's /metrics was reachable")
	}
	return snapshot
}

// reloadCounterDelta folds two per-pod counter snapshots into the total
// reload count over the interval: sum(after) - sum(before), clamped at >= 0.
//
// This is deliberately a SOFT metric: a controller pod rename between the
// snapshots (replica restart) drops the old pod's accumulated count from the
// `after` sum and undercounts the interval, and an iteration restart resets
// a pod's registry with the same effect. That is acceptable — the value
// feeds the nightly-scale job's WARN-only trend corridor, never a hard
// budget, and the clamp keeps a mid-run reset from reporting a negative
// delta.
func reloadCounterDelta(before, after map[string]float64) float64 {
	var sumBefore, sumAfter float64
	for _, v := range before {
		sumBefore += v
	}
	for _, v := range after {
		sumAfter += v
	}
	if delta := sumAfter - sumBefore; delta > 0 {
		return delta
	}
	return 0
}

// controllerHistogramAvg returns sum/count of the named histogram from the
// leader controller pod (the replica doing the rendering/deploying). Returns
// ok=false when no leader is identifiable or the histogram is empty.
func controllerHistogramAvg(ctx context.Context, cs kubernetes.Interface, name string) (float64, bool) {
	list, err := cs.CoreV1().Pods(ControllerNamespace).List(ctx, metav1.ListOptions{
		LabelSelector: LabelSelectorController,
	})
	if err != nil {
		return 0, false
	}
	for i := range list.Items {
		metrics, err := scrapeControllerMetrics(ctx, cs, list.Items[i].Name)
		if err != nil {
			continue
		}
		if metrics["haptic_leader_election_is_leader"] != 1 {
			continue
		}
		count := metrics[name+"_count"]
		if count == 0 {
			return 0, false
		}
		return metrics[name+"_sum"] / count, true
	}
	return 0, false
}

// scaleMetricsSink accumulates the tier's flat-key metrics AS they are
// produced (seed duration right after convergence, each latency sample when
// measured, config stats when fetched) so that a t.Fatalf anywhere mid-run
// still leaves a partial scale-metrics.json with everything measured up to
// that point. TestScale registers a t.Cleanup that flushes the current state;
// the final Assess flushes explicitly and the flush is idempotent, so the
// cleanup is a no-op on the happy path.
type scaleMetricsSink struct {
	mu      sync.Mutex
	values  map[string]any
	written bool
}

func newScaleMetricsSink() *scaleMetricsSink {
	return &scaleMetricsSink{values: map[string]any{}}
}

// set records one metric under the sink's lock.
func (s *scaleMetricsSink) set(key string, value any) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.values[key] = value
}

// flush writes the accumulated metrics as indented JSON to scale-metrics.json
// in the repo root (= CI project dir, where the job's artifacts:paths picks
// it up). Idempotent: only the first call writes; wrote reports whether THIS
// call performed the write.
func (s *scaleMetricsSink) flush() (path string, wrote bool, err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	root, err := repoRoot()
	if err != nil {
		return "", false, fmt.Errorf("locate repo root for %s: %w", scaleMetricsFile, err)
	}
	path = filepath.Join(root, scaleMetricsFile)
	if s.written {
		return path, false, nil
	}
	data, err := json.MarshalIndent(s.values, "", "  ")
	if err != nil {
		return path, false, fmt.Errorf("marshal scale metrics: %w", err)
	}
	if err := os.WriteFile(path, append(data, '\n'), 0o644); err != nil {
		return path, false, fmt.Errorf("write %s: %w", path, err)
	}
	s.written = true
	return path, true, nil
}

// durationPercentile returns the nearest-rank percentile of the samples.
func durationPercentile(samples []time.Duration, pct int) time.Duration {
	if len(samples) == 0 {
		return 0
	}
	sorted := append([]time.Duration{}, samples...)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i] < sorted[j] })
	rank := (pct*len(sorted) + 99) / 100 // ceil(pct/100 * n)
	if rank < 1 {
		rank = 1
	}
	if rank > len(sorted) {
		rank = len(sorted)
	}
	return sorted[rank-1]
}

// round2 rounds to two decimals so the metrics JSON stays legible.
func round2(v float64) float64 {
	return float64(int64(v*100+0.5)) / 100
}

// envInt64 mirrors envInt for int64-sized values (byte budgets).
func envInt64(t *testing.T, key string, def int64) int64 {
	t.Helper()
	v, ok := lookupEnv(key)
	if !ok || v == "" {
		return def
	}
	n, err := strconv.ParseInt(v, 10, 64)
	if err != nil || n < 1 {
		t.Fatalf("%s=%q: want a positive integer", key, v)
	}
	return n
}
