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
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"golang.org/x/sync/errgroup"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	apiwatch "k8s.io/apimachinery/pkg/watch"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/util/flowcontrol"
	"sigs.k8s.io/e2e-framework/pkg/envconf"
	"sigs.k8s.io/e2e-framework/pkg/features"

	"gitlab.com/haproxy-haptic/haptic/tests/e2e/httpclient"
	"gitlab.com/haproxy-haptic/haptic/tests/testutil"
)

// Churn-tier environment variables (issue #64). The tier is opt-in: the
// nightly-gateway-churn CI job sets churnEnableEnv; ordinary e2e runs (MR
// pipelines, local `make test-e2e`) skip the test entirely.
const (
	// churnEnableEnv gates the whole tier. Must be "1" to run. Also read by
	// TestMain's helmInstallChart, which then installs the chart with
	// extraContext.dumpPodPortAllocations=true so the allocator dump is
	// available in the rendered config.
	churnEnableEnv = "HAPTIC_E2E_CHURN"
	// churnMinutesEnv tunes the churn window length in minutes (default 5).
	churnMinutesEnv = "CHURN_MINUTES"
	// churnWorkersEnv tunes the number of parallel churn goroutines (default 6).
	churnWorkersEnv = "CHURN_WORKERS"

	churnDefaultMinutes = 5
	churnDefaultWorkers = 6

	// churnSurvivorCount is the number of Gateways created before the churn
	// window and never touched by it. Survivors anchor three assertion
	// families: their allocator keys must appear in EVERY sampled render,
	// their marker Services' update rate bounds the oscillation check, and
	// they prove end-to-end routing still works after the storm.
	churnSurvivorCount = 3

	// churnConvergeTimeout bounds each per-cycle wait (Gateway Programmed
	// after create, per-Gateway Service pruned after delete). Deliberately
	// larger than the suite's usual 15s reaction budget: with N workers
	// churning concurrently, each op shares render/deploy cycles (reconcile
	// debounce + 5s minDeploymentInterval + per-pod reload) with up to N-1
	// siblings plus the admission webhook's dry-run renders, so p99 under
	// deliberate saturation legitimately exceeds a single-change budget.
	// A cycle that needs >60s even under 6-way churn is a real convergence
	// regression and fails the test.
	churnConvergeTimeout = 60 * time.Second
)

// gatewayGVR / httpRouteGVR are the dynamic-client coordinates the churn
// workers use. The typed fixture helpers (NewGateway/NewHTTPRoute) shell out
// to kubectl per call, which is fine for a handful of fixtures but too heavy
// for hundreds of churn cycles.
var (
	gatewayGVR   = schema.GroupVersionResource{Group: "gateway.networking.k8s.io", Version: "v1", Resource: "gateways"}
	httpRouteGVR = schema.GroupVersionResource{Group: "gateway.networking.k8s.io", Version: "v1", Resource: "httproutes"}
)

// gatewayLabelSelector matches the per-Gateway marker Services the chart
// emits into the controller namespace (one LoadBalancer Service per Gateway
// with HTTP/HTTPS listeners). The two labels are the chart's stable public
// contract — ForwardGateway and the upstream GatewayInfrastructure
// conformance test discover the Services the same way.
const (
	gatewayNameLabel      = "gateway.networking.k8s.io/gateway-name"
	gatewayNamespaceLabel = "gateway.networking.k8s.io/gateway-namespace"
)

// gwPodPortLineRe extracts one allocator-dump line from the rendered
// haproxy.cfg. The chart's frontend-extra-099-gateway-pod-port-debug snippet
// emits `# gw-pod-port: <gwNs>/<gwName>:<listenerName>:<listenerPort> = <podPort>`
// per allocation when extraContext.dumpPodPortAllocations is "true"
// (see charts/haptic/charts/gateway/15-pod-port-allocator.yaml).
var gwPodPortLineRe = regexp.MustCompile(`(?m)^\s*# gw-pod-port: (\S+) = (\d+)\s*$`)

// getRenderedConfig fetches the controller's last rendered haproxy.cfg from
// the `rendered` introspection var (/debug/vars/rendered). This is the raw
// template output — it still carries the `# gw-pod-port:` allocator-dump
// comments, unlike the config after HAProxy's own parser normalises it.
func (dc *debugClient) getRenderedConfig(ctx context.Context) (string, error) {
	body, err := dc.loopback.Get(ctx, "/debug/vars/rendered")
	if err != nil {
		return "", err
	}
	var res struct {
		Config string `json:"config"`
	}
	if err := json.Unmarshal(body, &res); err != nil {
		return "", fmt.Errorf("decode rendered config: %w", err)
	}
	return res.Config, nil
}

// parseGatewayPodPortDump extracts the allocator dump from a rendered
// config: allocation key (`<gwNs>/<gwName>:<listenerName>:<listenerPort>`)
// → allocated pod port.
func parseGatewayPodPortDump(config string) map[string]int {
	out := map[string]int{}
	for _, m := range gwPodPortLineRe.FindAllStringSubmatch(config, -1) {
		port, err := strconv.Atoi(m[2])
		if err != nil {
			continue // unreachable given the regex, but never allocate garbage
		}
		out[m[1]] = port
	}
	return out
}

// allocationScope reduces an allocation key to its (Gateway, listenerPort)
// tuple — the granularity at which pod ports must be unique. Listeners
// sharing a Gateway+port share one bind by design (host-disambiguated), so
// the listener-name segment is dropped. Key segments are `:`-separated and
// neither namespace/name (DNS labels) nor listener names (RFC 1123 labels)
// may contain `:`, so index-based splitting is safe.
func allocationScope(key string) string {
	parts := strings.Split(key, ":")
	if len(parts) != 3 {
		return key // malformed — keep verbatim so the mismatch surfaces
	}
	return parts[0] + ":" + parts[2]
}

// crossWiredPorts returns one message per pod port claimed by more than one
// (Gateway, listenerPort) scope in a single render's allocation dump.
//
// Within one render of the pure hash-and-probe allocator this is impossible
// by construction (the probe loop never reuses a slot), so a hit here means
// the allocator was regressed — e.g. someone reintroduced the forbidden
// read-committed-Services-back design whose collision lock-in produced two
// Gateways answering on one pod port (see the DO-NOT-"improve" header in
// charts/haptic/charts/gateway/15-pod-port-allocator.yaml).
func crossWiredPorts(alloc map[string]int) []string {
	scopesByPort := map[int]map[string]bool{}
	for key, port := range alloc {
		if scopesByPort[port] == nil {
			scopesByPort[port] = map[string]bool{}
		}
		scopesByPort[port][allocationScope(key)] = true
	}
	var out []string
	for port, scopes := range scopesByPort {
		if len(scopes) <= 1 {
			continue
		}
		names := make([]string, 0, len(scopes))
		for s := range scopes {
			names = append(names, s)
		}
		sort.Strings(names)
		out = append(out, fmt.Sprintf("pod port %d claimed by %s", port, strings.Join(names, " AND ")))
	}
	sort.Strings(out)
	return out
}

// churnMonitor aggregates observations from the background samplers while
// the churn workers run. All mutation goes through the mutex; assertions
// read the totals after the workers and samplers have stopped.
type churnMonitor struct {
	mu sync.Mutex

	// violations are hard invariant breaches (cross-wired dump, survivor
	// key missing from a render, sustained Service-targetPort duplicate).
	// Capped so a pathological failure can't produce gigabytes of output.
	violations []string

	// survivorUpdates counts resourceVersion changes per survivor marker
	// Service, fed by the Service watch. Bounded by the oscillation check.
	survivorUpdates map[string]int
	lastServiceRV   map[string]string

	// dumpSamples / dumpErrors track allocator-dump sampler health so a
	// sampler that never managed to observe anything fails the test instead
	// of green-lighting assertions that never ran.
	dumpSamples int
	dumpErrors  int

	// duplicateStreaks tracks, per "port|gwA|gwB" duplicate pair, how many
	// CONSECUTIVE Service-list samples showed the same two Gateways' marker
	// Services DNATing to the same pod port. One-sample duplicates are legal
	// (a probe-chain shift updates both Services in the same render, but the
	// two apiserver writes aren't atomic); a long streak is the historical
	// permanent-collision lock-in.
	duplicateStreaks map[string]int
}

func (m *churnMonitor) addViolation(format string, args ...any) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if len(m.violations) < 20 {
		m.violations = append(m.violations, fmt.Sprintf(format, args...))
	}
}

// recordServiceRV folds one observed (service, resourceVersion) pair into
// the update counters, deduplicating by RV so watch re-establishment can't
// double-count.
func (m *churnMonitor) recordServiceRV(name, rv string, countChange bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.lastServiceRV[name] == rv {
		return
	}
	m.lastServiceRV[name] = rv
	if countChange {
		m.survivorUpdates[name]++
	}
}

// duplicateStreakThreshold is the number of consecutive duplicate-targetPort
// samples (sampled every churnSampleInterval) after which a Service-layer
// port collision counts as sustained cross-wiring rather than an in-flight
// shift. 8 samples ≈ 16s — far above a legit shift's exposure (both Services
// move in the same render, so the stale window is one apply, seconds) and
// far below "permanent".
const duplicateStreakThreshold = 8

// churnSampleInterval paces the allocator-dump / Service-list sampler. This
// is a sampler cadence, not a synchronization sleep: no assertion waits ON a
// tick, the ticker only bounds how often cluster state is photographed.
const churnSampleInterval = 2 * time.Second

// TestGatewayChurn is the churn/soak tier from issue #64: N goroutines
// creating and deleting Gateways + HTTPRoutes in per-goroutine namespaces
// for M minutes, against untouched "survivor" Gateways, asserting:
//
//   - zero pod-port cross-wiring, via the debug allocator dump: the chart's
//     pod-port allocator emits `# gw-pod-port:` comments into the rendered
//     config (extraContext.dumpPodPortAllocations, set by TestMain's helm
//     install when this tier is enabled), read through the `rendered`
//     introspection var. Sampled continuously during the churn window and
//     matched exactly against the survivor set at the end.
//   - zero sustained Service-update oscillation: a watch counts
//     resourceVersion changes on the survivor Gateways' marker Services; the
//     read-back oscillation regression this guards against (issue #58,
//     ~50 Service flips/sec sustained) exceeds the bound by orders of
//     magnitude, while legitimate probe-chain shifts stay far under it.
//   - final convergence: every Gateway a worker created became Programmed
//     (asserted per cycle), every deleted Gateway's marker Service is
//     pruned, the allocator dump contains exactly the survivors, routing
//     works for the survivors, and the survivor objects then go quiescent
//     (the #63 transitionTime status churn oscillated at idle — the
//     quiescence wait would have timed out on it).
//
// Scheduling independence: every assertion is a convergence contract (ends
// on the expected state, then quiescent) or a rate bound with an
// orders-of-magnitude margin — never an exact interleaving. All waits are
// condition-based via testutil.WaitConfig; there are no synchronization
// sleeps.
//
// Deliberately NOT t.Parallel(): the tier owns the cluster for the whole
// churn window and its oscillation bounds assume no sibling test is mutating
// Gateway resources. The nightly job runs it as the only test in the binary
// (TEST_RUN_PATTERN=TestGatewayChurn).
func TestGatewayChurn(t *testing.T) {
	if v, _ := lookupEnv(churnEnableEnv); v != "1" {
		t.Skipf("%s != 1 — churn/soak tier only runs in the nightly-gateway-churn job (issue #64)", churnEnableEnv)
	}
	churnWindow := time.Duration(envInt(t, churnMinutesEnv, churnDefaultMinutes)) * time.Minute
	workers := envInt(t, churnWorkersEnv, churnDefaultWorkers)

	var (
		dc         *debugClient
		cs         kubernetes.Interface
		dyn        dynamic.Interface
		survivorNS string
		workerNSs  []string

		survivorGateways []string          // Gateway names in survivorNS
		survivorHosts    map[string]string // gateway name -> route hostname
		survivorSvcs     map[string]string // gateway name -> marker Service name

		monitor = &churnMonitor{
			survivorUpdates:  map[string]int{},
			lastServiceRV:    map[string]string{},
			duplicateStreaks: map[string]int{},
		}
		totalOps int
	)

	// survivorAllocationKey is the allocator-dump key each survivor must hold
	// in every render: single HTTP listener named "http" on port 80.
	survivorAllocationKey := func(gwName string) string {
		return survivorNS + "/" + gwName + ":http:80"
	}

	feature := features.New(fmt.Sprintf("Gateway churn/soak: %d workers x %s, zero cross-wiring/oscillation", workers, churnWindow)).
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
			dc = &debugClient{
				clientset:   cs,
				namespace:   ControllerNamespace,
				serviceName: DebugServiceNameValue,
				port:        strconv.Itoa(DebugPort),
				loopback: testutil.NewLoopbackPodClient(
					client.RESTConfig(), cs, ControllerNamespace, LabelSelectorController, DebugPort,
				),
			}

			// Survivor fixtures: one namespace, its own echo backend, and
			// churnSurvivorCount Gateways+HTTPRoutes that the churn never
			// touches.
			survivorNS = NamespaceForTest(ctx, t, client)
			DumpLogsOnFailure(t, survivorNS)
			backend := NewEchoServerBackend(ctx, t, client, survivorNS)
			survivorHosts = map[string]string{}
			for i := 0; i < churnSurvivorCount; i++ {
				name := fmt.Sprintf("survivor-%d", i)
				host := fmt.Sprintf("churn-survivor-%d.localdev.me", i)
				NewGateway(ctx, t, survivorNS, name)
				NewHTTPRoute(ctx, t, survivorNS, HTTPRouteSpec{
					Name:        name,
					GatewayName: name,
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
				survivorGateways = append(survivorGateways, name)
				survivorHosts[name] = host
			}

			// Baseline: routing works for every survivor BEFORE the churn, so
			// a post-churn routing failure is attributable to the churn.
			// ForwardGateway also gates on Programmed=True per survivor.
			for _, gw := range survivorGateways {
				fwd := ForwardGateway(ctx, t, survivorNS, gw, 80)
				resp := httpclient.ForForwarded(t, fwd.HTTPPort, 0).GET(survivorHosts[gw], "/").ExpectOK(t)
				if resp.Echo == nil {
					t.Fatalf("survivor %s baseline: expected echo-server JSON, got %d bytes", gw, len(resp.Body))
				}
			}

			// Resolve the survivors' marker Services (the oscillation watch
			// counts updates by Service name).
			survivorSvcs = map[string]string{}
			for _, gw := range survivorGateways {
				svcs, err := cs.CoreV1().Services(ControllerNamespace).List(ctx, metav1.ListOptions{
					LabelSelector: gatewayNameLabel + "=" + gw + "," + gatewayNamespaceLabel + "=" + survivorNS,
				})
				if err != nil || len(svcs.Items) != 1 {
					t.Fatalf("survivor %s: expected exactly 1 marker Service (err=%v, got=%d)", gw, err, len(svcs.Items))
				}
				survivorSvcs[gw] = svcs.Items[0].Name
			}

			// The allocator dump must be live before any dump-based assertion
			// means anything. If this times out, the cluster was helm-installed
			// WITHOUT the churn flag (e.g. KEEP_CLUSTER reuse of a cluster whose
			// TestMain ran without HAPTIC_E2E_CHURN=1) — TestMain's
			// helmInstallChart only sets extraContext.dumpPodPortAllocations
			// when the tier is enabled.
			waitCfg := testutil.FastWaitConfig()
			if err := testutil.WaitForConditionWithDescription(ctx, waitCfg,
				"allocator dump (# gw-pod-port lines) present in /debug/vars/rendered for all survivors",
				func(ctx context.Context) (bool, error) {
					rendered, err := dc.getRenderedConfig(ctx)
					if err != nil {
						return false, err
					}
					alloc := parseGatewayPodPortDump(rendered)
					for _, gw := range survivorGateways {
						if _, ok := alloc[survivorAllocationKey(gw)]; !ok {
							return false, fmt.Errorf("survivor key %q not in dump (%d keys total) — was the chart installed with %s=1?",
								survivorAllocationKey(gw), len(alloc), churnEnableEnv)
						}
					}
					return true, nil
				}); err != nil {
				t.Fatalf("allocator dump not available: %v", err)
			}

			// Per-worker namespaces + backends. Workers only ever touch their
			// own namespace, so their create/delete cycles can't collide.
			for i := 0; i < workers; i++ {
				ns := NamespaceForTest(ctx, t, client)
				NewEchoServerBackend(ctx, t, client, ns)
				workerNSs = append(workerNSs, ns)
			}
			return ctx
		}).
		Assess("sustained parallel churn: no cross-wiring, no oscillation, every Gateway converges", func(ctx context.Context, t *testing.T, cfg *envconf.Config) context.Context {
			monCtx, stopMonitors := context.WithCancel(ctx)
			var monWG sync.WaitGroup

			// Sampler 1: allocator dump + Service-layer duplicate detection.
			monWG.Add(1)
			go func() {
				defer monWG.Done()
				runChurnSampler(monCtx, dc, cs, monitor, survivorGateways, survivorAllocationKey)
			}()

			// Sampler 2: survivor marker-Service update watch (oscillation).
			monWG.Add(1)
			go func() {
				defer monWG.Done()
				watchSurvivorServices(monCtx, cs, monitor, survivorSvcs)
			}()

			// Churn workers.
			deadline := time.Now().Add(churnWindow)
			g, gctx := errgroup.WithContext(ctx)
			opCounts := make([]int, workers)
			for i := 0; i < workers; i++ {
				i := i
				g.Go(func() error {
					ops, err := churnWorker(gctx, dyn, cs, workerNSs[i], i, deadline)
					opCounts[i] = ops
					return err
				})
			}
			err := g.Wait()
			stopMonitors()
			monWG.Wait()
			if err != nil {
				t.Fatalf("churn worker failed: %v", err)
			}
			for i, ops := range opCounts {
				totalOps += ops
				t.Logf("worker %d (%s): %d create/converge/delete/prune cycles", i, workerNSs[i], ops)
			}
			if totalOps == 0 {
				t.Fatal("churn window produced zero completed cycles — the tier exercised nothing")
			}

			monitor.mu.Lock()
			defer monitor.mu.Unlock()
			t.Logf("allocator-dump samples: %d ok, %d fetch errors", monitor.dumpSamples, monitor.dumpErrors)
			if len(monitor.violations) > 0 {
				t.Fatalf("churn invariant violations (%d, showing up to 20):\n  %s",
					len(monitor.violations), strings.Join(monitor.violations, "\n  "))
			}
			// The samplers must have actually observed the system, otherwise
			// "zero violations" is vacuous. ~10 samples minimum even for the
			// 1-minute smoke configuration.
			if monitor.dumpSamples < 10 {
				t.Fatalf("allocator-dump sampler observed only %d samples (%d errors) — assertions never ran",
					monitor.dumpSamples, monitor.dumpErrors)
			}

			// Oscillation bound. Legitimate updates to a survivor's marker
			// Service during churn are probe-chain shifts: per churn event
			// (~2 per cycle) each survivor key shifts with probability
			// ≈ live keys / 1000-slot range ≈ 1-2%, so the expected total
			// across all survivors is well under totalOps/10. The bound
			// below keeps >5x headroom over that while sitting orders of
			// magnitude under the failure mode it exists to catch — the
			// issue-#58 read-back oscillation sustained ~50 Service flips/s
			// (thousands per minute).
			allowed := 30 + totalOps/2
			total := 0
			for gw, svc := range survivorSvcs {
				n := monitor.survivorUpdates[svc]
				total += n
				t.Logf("survivor %s marker Service %s: %d updates during churn", gw, svc, n)
			}
			if total > allowed {
				t.Fatalf("sustained Service-update oscillation: %d survivor marker-Service updates during churn (bound %d for %d ops)",
					total, allowed, totalOps)
			}
			return ctx
		}).
		Assess("final convergence: allocator dump and marker Services match exactly the survivor set", func(ctx context.Context, t *testing.T, cfg *envconf.Config) context.Context {
			// The allocator dump must converge to EXACTLY the survivor keys:
			// all churned Gateways' allocations gone, all survivors present.
			expected := map[string]bool{}
			for _, gw := range survivorGateways {
				expected[survivorAllocationKey(gw)] = true
			}
			finalAlloc := map[string]int{}
			waitCfg := testutil.DefaultWaitConfig()
			waitCfg.Timeout = 3 * time.Minute
			if err := testutil.WaitForConditionWithDescription(ctx, waitCfg,
				"allocator dump converged to exactly the survivor allocations",
				func(ctx context.Context) (bool, error) {
					rendered, err := dc.getRenderedConfig(ctx)
					if err != nil {
						return false, err
					}
					alloc := parseGatewayPodPortDump(rendered)
					if len(alloc) != len(expected) {
						return false, fmt.Errorf("dump has %d keys, want %d: %v", len(alloc), len(expected), allocKeys(alloc))
					}
					for key := range expected {
						if _, ok := alloc[key]; !ok {
							return false, fmt.Errorf("survivor key %q missing from dump: %v", key, allocKeys(alloc))
						}
					}
					finalAlloc = alloc
					return true, nil
				}); err != nil {
				t.Fatalf("final allocator state: %v", err)
			}
			if wired := crossWiredPorts(finalAlloc); len(wired) > 0 {
				t.Fatalf("final allocator dump is cross-wired: %v", wired)
			}

			// Every deleted Gateway's marker Service must be pruned: no
			// Service labelled with any churn-worker namespace may remain.
			churnNS := map[string]bool{}
			for _, ns := range workerNSs {
				churnNS[ns] = true
			}
			if err := testutil.WaitForConditionWithDescription(ctx, waitCfg,
				"all churned Gateways' marker Services pruned",
				func(ctx context.Context) (bool, error) {
					svcs, err := cs.CoreV1().Services(ControllerNamespace).List(ctx, metav1.ListOptions{
						LabelSelector: gatewayNameLabel,
					})
					if err != nil {
						return false, err
					}
					var leftovers []string
					for i := range svcs.Items {
						if churnNS[svcs.Items[i].Labels[gatewayNamespaceLabel]] {
							leftovers = append(leftovers, svcs.Items[i].Name)
						}
					}
					if len(leftovers) > 0 {
						return false, fmt.Errorf("%d churn marker Services still present: %v", len(leftovers), leftovers)
					}
					return true, nil
				}); err != nil {
				t.Fatalf("churn Service pruning: %v", err)
			}

			// The cluster-layer wiring must agree with the dump: each
			// survivor's marker Service DNATs port 80 to the allocated pod
			// port. Combined with the dump's per-render uniqueness this rules
			// out cross-wiring end to end (allocator AND committed Services).
			for _, gw := range survivorGateways {
				svc, err := cs.CoreV1().Services(ControllerNamespace).Get(ctx, survivorSvcs[gw], metav1.GetOptions{})
				if err != nil {
					t.Fatalf("survivor %s marker Service: %v", gw, err)
				}
				want := finalAlloc[survivorAllocationKey(gw)]
				got := targetPortForServicePort(svc, 80)
				if got != want {
					t.Fatalf("survivor %s: marker Service targetPort %d != allocator dump port %d (cross-wiring at the Service layer)",
						gw, got, want)
				}
			}
			return ctx
		}).
		Assess("routing works for survivors after the churn", func(ctx context.Context, t *testing.T, cfg *envconf.Config) context.Context {
			for _, gw := range survivorGateways {
				fwd := ForwardGateway(ctx, t, survivorNS, gw, 80)
				resp := httpclient.ForForwarded(t, fwd.HTTPPort, 0).GET(survivorHosts[gw], "/").ExpectOK(t)
				if resp.Echo == nil {
					t.Fatalf("survivor %s post-churn: expected echo-server JSON, got %d bytes", gw, len(resp.Body))
				}
			}
			return ctx
		}).
		Assess("survivors go quiescent at idle (no residual update churn)", func(ctx context.Context, t *testing.T, cfg *envconf.Config) context.Context {
			// The #63 transitionTime churn oscillated AT IDLE: with no input
			// changing, the controller kept re-patching status, re-triggering
			// renders. Contract: once converged, the survivor Gateways and
			// their marker Services stop changing entirely. We wait until
			// their resourceVersions have been stable for a full 15s
			// observation window; sustained idle churn makes this wait time
			// out (the failure), while a healthy controller passes on the
			// first stable window.
			const stableFor = 15 * time.Second
			var (
				lastSnapshot string
				stableSince  time.Time
			)
			cfgWait := testutil.WaitConfig{
				InitialInterval: time.Second,
				MaxInterval:     time.Second,
				Timeout:         2 * time.Minute,
				Multiplier:      1.0,
			}
			if err := testutil.WaitForConditionWithDescription(ctx, cfgWait,
				fmt.Sprintf("survivor Gateways+Services resourceVersions stable for %s", stableFor),
				func(ctx context.Context) (bool, error) {
					snap, err := survivorRVSnapshot(ctx, cs, dyn, survivorNS, survivorGateways, survivorSvcs)
					if err != nil {
						return false, err
					}
					now := time.Now()
					if snap != lastSnapshot {
						lastSnapshot = snap
						stableSince = now
						return false, fmt.Errorf("resourceVersions still changing: %s", snap)
					}
					return now.Sub(stableSince) >= stableFor, nil
				}); err != nil {
				t.Fatalf("survivors never went quiescent: %v", err)
			}
			return ctx
		}).
		Feature()

	testEnv.Test(t, feature)
}

// envInt reads an integer environment variable with a default; a set-but-
// invalid value fails the test rather than silently running with defaults.
func envInt(t *testing.T, key string, def int) int {
	t.Helper()
	v, ok := lookupEnv(key)
	if !ok || v == "" {
		return def
	}
	n, err := strconv.Atoi(v)
	if err != nil || n < 1 {
		t.Fatalf("%s=%q: want a positive integer", key, v)
	}
	return n
}

// newDynamicForE2E mirrors newClientsetForE2E for the dynamic client: rate
// limiting disabled so the churn workers aren't throttled client-side.
func newDynamicForE2E(cfg *rest.Config) (dynamic.Interface, error) {
	c := rest.CopyConfig(cfg)
	c.RateLimiter = flowcontrol.NewFakeAlwaysRateLimiter()
	return dynamic.NewForConfig(c)
}

// churnWorker runs one create → converge → delete → prune loop in its own
// namespace until deadline. Every completed cycle proves both directions of
// convergence under churn: the created Gateway became Programmed (bind live,
// reload verified) and, after deletion, its marker Service was pruned. Names
// are fresh each cycle so every cycle exercises a fresh allocator key.
func churnWorker(ctx context.Context, dyn dynamic.Interface, cs kubernetes.Interface, ns string, worker int, deadline time.Time) (int, error) {
	ops := 0
	for seq := 0; time.Now().Before(deadline); seq++ {
		if ctx.Err() != nil {
			return ops, ctx.Err()
		}
		gwName := fmt.Sprintf("churn-w%d-g%d", worker, seq)
		host := fmt.Sprintf("churn-w%d-%d.localdev.me", worker, seq)

		if err := createChurnGateway(ctx, dyn, ns, gwName); err != nil {
			return ops, fmt.Errorf("worker %d cycle %d: create Gateway: %w", worker, seq, err)
		}
		if err := createChurnHTTPRoute(ctx, dyn, ns, gwName, host); err != nil {
			return ops, fmt.Errorf("worker %d cycle %d: create HTTPRoute: %w", worker, seq, err)
		}

		waitCfg := testutil.FastWaitConfig()
		waitCfg.Timeout = churnConvergeTimeout
		if err := testutil.WaitForConditionWithDescription(ctx, waitCfg,
			fmt.Sprintf("churn Gateway %s/%s Programmed", ns, gwName),
			func(ctx context.Context) (bool, error) {
				gw, err := dyn.Resource(gatewayGVR).Namespace(ns).Get(ctx, gwName, metav1.GetOptions{})
				if err != nil {
					return false, err
				}
				return gatewayProgrammed(gw), nil
			}); err != nil {
			return ops, fmt.Errorf("worker %d cycle %d: %w", worker, seq, err)
		}

		if err := dyn.Resource(httpRouteGVR).Namespace(ns).Delete(ctx, gwName, metav1.DeleteOptions{}); err != nil && !apierrors.IsNotFound(err) {
			return ops, fmt.Errorf("worker %d cycle %d: delete HTTPRoute: %w", worker, seq, err)
		}
		if err := dyn.Resource(gatewayGVR).Namespace(ns).Delete(ctx, gwName, metav1.DeleteOptions{}); err != nil && !apierrors.IsNotFound(err) {
			return ops, fmt.Errorf("worker %d cycle %d: delete Gateway: %w", worker, seq, err)
		}

		if err := testutil.WaitForConditionWithDescription(ctx, waitCfg,
			fmt.Sprintf("churn Gateway %s/%s marker Service pruned", ns, gwName),
			func(ctx context.Context) (bool, error) {
				svcs, err := cs.CoreV1().Services(ControllerNamespace).List(ctx, metav1.ListOptions{
					LabelSelector: gatewayNameLabel + "=" + gwName + "," + gatewayNamespaceLabel + "=" + ns,
				})
				if err != nil {
					return false, err
				}
				if n := len(svcs.Items); n > 0 {
					return false, fmt.Errorf("%d marker Services still present", n)
				}
				return true, nil
			}); err != nil {
			return ops, fmt.Errorf("worker %d cycle %d: %w", worker, seq, err)
		}
		ops++
	}
	return ops, nil
}

// createChurnGateway creates a minimal one-HTTP-listener Gateway via the
// dynamic client (same shape as the NewGateway kubectl fixture).
func createChurnGateway(ctx context.Context, dyn dynamic.Interface, ns, name string) error {
	gw := &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": "gateway.networking.k8s.io/v1",
		"kind":       "Gateway",
		"metadata":   map[string]any{"name": name, "namespace": ns},
		"spec": map[string]any{
			"gatewayClassName": gatewayClassName,
			"listeners": []any{map[string]any{
				"name":     "http",
				"protocol": "HTTP",
				"port":     int64(80),
				"allowedRoutes": map[string]any{
					"namespaces": map[string]any{"from": "Same"},
				},
			}},
		},
	}}
	_, err := dyn.Resource(gatewayGVR).Namespace(ns).Create(ctx, gw, metav1.CreateOptions{})
	return err
}

// createChurnHTTPRoute attaches a single catch-all-path route for host to
// the worker's Gateway, backed by the namespace-local echo-server Service.
func createChurnHTTPRoute(ctx context.Context, dyn dynamic.Interface, ns, gwName, host string) error {
	route := &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": "gateway.networking.k8s.io/v1",
		"kind":       "HTTPRoute",
		"metadata":   map[string]any{"name": gwName, "namespace": ns},
		"spec": map[string]any{
			"parentRefs": []any{map[string]any{"name": gwName}},
			"hostnames":  []any{host},
			"rules": []any{map[string]any{
				"matches": []any{map[string]any{
					"path": map[string]any{"type": "PathPrefix", "value": "/"},
				}},
				"backendRefs": []any{map[string]any{
					"name": EchoServerBackend.Service,
					"port": int64(EchoServerBackend.Port),
				}},
			}},
		},
	}}
	_, err := dyn.Resource(httpRouteGVR).Namespace(ns).Create(ctx, route, metav1.CreateOptions{})
	return err
}

// gatewayProgrammed reports whether the Gateway carries condition
// Programmed=True.
func gatewayProgrammed(gw *unstructured.Unstructured) bool {
	conds, _, _ := unstructured.NestedSlice(gw.Object, "status", "conditions")
	for _, c := range conds {
		m, ok := c.(map[string]any)
		if !ok {
			continue
		}
		if m["type"] == "Programmed" && m["status"] == "True" {
			return true
		}
	}
	return false
}

// runChurnSampler photographs cluster state every churnSampleInterval until
// ctx is cancelled, feeding the monitor with two independent invariants:
//
//  1. Allocator dump (via /debug/vars/rendered): every sampled render must
//     contain all survivor allocation keys and must not map two
//     (Gateway, listenerPort) scopes to one pod port.
//  2. Marker Services: two different Gateways' Services DNATing to the same
//     pod port is tolerated transiently (in-flight probe-chain shift) but
//     becomes a violation when the SAME collision persists for
//     duplicateStreakThreshold consecutive samples — the historical
//     permanent-collision lock-in signature.
//
// Transient fetch/list errors are counted, not fatal: the churn itself puts
// the apiserver proxy under load. The caller asserts a minimum number of
// successful samples so a dead sampler can't green-light the run.
func runChurnSampler(ctx context.Context, dc *debugClient, cs kubernetes.Interface, m *churnMonitor, survivorGateways []string, survivorKey func(string) string) {
	ticker := time.NewTicker(churnSampleInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}

		// Invariant 1: the allocator dump of the latest render.
		rendered, err := dc.getRenderedConfig(ctx)
		if err != nil {
			m.mu.Lock()
			m.dumpErrors++
			m.mu.Unlock()
		} else {
			alloc := parseGatewayPodPortDump(rendered)
			m.mu.Lock()
			m.dumpSamples++
			m.mu.Unlock()
			for _, gw := range survivorGateways {
				if _, ok := alloc[survivorKey(gw)]; !ok {
					m.addViolation("render sample %d: survivor allocation %q missing from dump (%d keys)",
						m.dumpSamples, survivorKey(gw), len(alloc))
				}
			}
			for _, v := range crossWiredPorts(alloc) {
				m.addViolation("render sample %d: allocator dump cross-wired: %s", m.dumpSamples, v)
			}
		}

		// Invariant 2: committed marker Services, deduped port claims.
		svcs, err := cs.CoreV1().Services(ControllerNamespace).List(ctx, metav1.ListOptions{
			LabelSelector: gatewayNameLabel,
		})
		if err != nil {
			continue // transient; the dump invariant above is the primary signal
		}
		claims := map[int][]string{}
		for i := range svcs.Items {
			svc := &svcs.Items[i]
			port := targetPortForServicePort(svc, 80)
			if port == 0 {
				continue
			}
			gwID := svc.Labels[gatewayNamespaceLabel] + "/" + svc.Labels[gatewayNameLabel]
			claims[port] = append(claims[port], gwID)
		}
		seenPairs := map[string]bool{}
		for port, gws := range claims {
			if len(gws) <= 1 {
				continue
			}
			sort.Strings(gws)
			pair := strconv.Itoa(port) + "|" + strings.Join(gws, "|")
			seenPairs[pair] = true
			m.mu.Lock()
			m.duplicateStreaks[pair]++
			streak := m.duplicateStreaks[pair]
			m.mu.Unlock()
			if streak == duplicateStreakThreshold {
				m.addViolation("sustained Service-layer port collision: pod port %d claimed by %s for %d consecutive samples (~%s)",
					port, strings.Join(gws, " AND "), streak, time.Duration(streak)*churnSampleInterval)
			}
		}
		// Reset streaks whose collision cleared — only CONSECUTIVE samples count.
		m.mu.Lock()
		for pair := range m.duplicateStreaks {
			if !seenPairs[pair] {
				delete(m.duplicateStreaks, pair)
			}
		}
		m.mu.Unlock()
	}
}

// watchSurvivorServices counts resourceVersion changes on the survivor
// Gateways' marker Services via a watch (complete — a poll could undersample
// a fast oscillation), deduplicating by RV across watch re-establishment.
// The initial list seeds the per-Service baseline so pre-existing state is
// not counted as an update.
func watchSurvivorServices(ctx context.Context, cs kubernetes.Interface, m *churnMonitor, survivorSvcs map[string]string) {
	watched := map[string]bool{}
	for _, svc := range survivorSvcs {
		watched[svc] = true
	}

	seed := func() string {
		list, err := cs.CoreV1().Services(ControllerNamespace).List(ctx, metav1.ListOptions{
			LabelSelector: gatewayNameLabel,
		})
		if err != nil {
			return ""
		}
		for i := range list.Items {
			if watched[list.Items[i].Name] {
				m.recordServiceRV(list.Items[i].Name, list.Items[i].ResourceVersion, false)
			}
		}
		return list.ResourceVersion
	}

	rv := seed()
	for ctx.Err() == nil {
		w, err := cs.CoreV1().Services(ControllerNamespace).Watch(ctx, metav1.ListOptions{
			LabelSelector:       gatewayNameLabel,
			ResourceVersion:     rv,
			AllowWatchBookmarks: true,
		})
		if err != nil {
			// 410 Gone or transient failure: re-seed from a fresh list. Changes
			// inside the gap are missed, which can only UNDERCOUNT — safe for a
			// bound that catches thousands-per-minute oscillation.
			rv = seed()
			select {
			case <-ctx.Done():
				return
			case <-time.After(200 * time.Millisecond):
			}
			continue
		}
		for ev := range w.ResultChan() {
			switch ev.Type {
			case apiwatch.Added, apiwatch.Modified:
				svc, ok := ev.Object.(*corev1.Service)
				if !ok {
					continue
				}
				rv = svc.ResourceVersion
				if watched[svc.Name] {
					m.recordServiceRV(svc.Name, svc.ResourceVersion, true)
				}
			case apiwatch.Bookmark:
				if svc, ok := ev.Object.(*corev1.Service); ok {
					rv = svc.ResourceVersion
				}
			case apiwatch.Error:
				rv = "" // force a re-list + re-seed on the next loop
			case apiwatch.Deleted:
				// Churn Services disappearing is normal; survivors are never
				// deleted (a deleted survivor would fail the convergence
				// assertions later).
			}
		}
		w.Stop()
	}
}

// targetPortForServicePort returns the int targetPort the Service maps the
// given port to, or 0 when the port isn't declared.
func targetPortForServicePort(svc *corev1.Service, port int32) int {
	for i := range svc.Spec.Ports {
		if svc.Spec.Ports[i].Port == port {
			return svc.Spec.Ports[i].TargetPort.IntValue()
		}
	}
	return 0
}

// allocKeys returns the sorted key set of an allocation dump for error text.
func allocKeys(alloc map[string]int) []string {
	keys := make([]string, 0, len(alloc))
	for k := range alloc {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// survivorRVSnapshot serialises the survivors' Gateway + marker-Service
// resourceVersions into one comparable string for the quiescence wait.
func survivorRVSnapshot(ctx context.Context, cs kubernetes.Interface, dyn dynamic.Interface, ns string, gateways []string, svcs map[string]string) (string, error) {
	var parts []string
	for _, gw := range gateways {
		obj, err := dyn.Resource(gatewayGVR).Namespace(ns).Get(ctx, gw, metav1.GetOptions{})
		if err != nil {
			return "", fmt.Errorf("get Gateway %s/%s: %w", ns, gw, err)
		}
		parts = append(parts, "gw/"+gw+"="+obj.GetResourceVersion())
		svc, err := cs.CoreV1().Services(ControllerNamespace).Get(ctx, svcs[gw], metav1.GetOptions{})
		if err != nil {
			return "", fmt.Errorf("get marker Service %s: %w", svcs[gw], err)
		}
		parts = append(parts, "svc/"+svc.Name+"="+svc.ResourceVersion)
	}
	sort.Strings(parts)
	return strings.Join(parts, ","), nil
}
