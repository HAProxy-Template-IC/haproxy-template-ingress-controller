// Copyright 2026 Philipp Hossner
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
	"strconv"
	"strings"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"sigs.k8s.io/e2e-framework/klient"
	"sigs.k8s.io/e2e-framework/pkg/envconf"
	"sigs.k8s.io/e2e-framework/pkg/features"

	hapticclient "gitlab.com/haproxy-haptic/haptic/pkg/generated/clientset/versioned"
	"gitlab.com/haproxy-haptic/haptic/tests/e2e/httpclient"
	"gitlab.com/haproxy-haptic/haptic/tests/testutil"
)

// TestLeaderChangeReloadsNothing forces a leadership handover and asserts the
// fleet does not notice.
//
// A new leader has no memory of what it deployed; it reads each pod's applied
// plan back from the agent and diffs against that. If that read-back ever
// regressed — a leader trusting its own empty state, or one that cannot decode
// a plan another leader stored — the first reconciliation of every failover
// would reload the whole fleet. That is invisible in functional tests and
// expensive in production, so the worker's start time is the assertion.
//
// The handover is forced by handing the Lease to the standby, not by killing
// the leader pod. The bundled `pod-names.map` maps every pod IP in the cluster
// to its name, controller pods included, so replacing a controller pod
// legitimately changes the render and would charge the failover for a config
// change it did not cause. Rewriting the Lease's holder changes the leader and
// nothing else. (Deleting the Lease does not work: client-go re-creates it from
// the same loop, so the leader never stops leading and the test would pass
// without exercising anything.)
//
// The render gate is part of the same claim: it starts OPTIMISTIC on a fresh
// term (the agents' own last-known-good sets protect the fleet), so a failover
// must not stall dispatch waiting for a verdict either.
func TestLeaderChangeReloadsNothing(t *testing.T) {
	const host = "leader-change.localdev.me"

	var (
		client    klient.Client
		clientset kubernetes.Interface
		oldLeader string
		baseline  map[string]podPlanState
	)

	feature := features.New("Leader change: a new leader reloads nothing").
		Setup(func(ctx context.Context, t *testing.T, cfg *envconf.Config) context.Context {
			var err error
			if client, err = cfg.NewClient(); err != nil {
				t.Fatalf("new client: %v", err)
			}
			if clientset, err = newClientsetForE2E(client.RESTConfig()); err != nil {
				t.Fatalf("build clientset: %v", err)
			}
			namespace := NamespaceForTest(ctx, t, client)
			DumpLogsOnFailure(t, namespace)

			backend := NewEchoServerBackend(ctx, t, client, namespace)
			NewIngress(ctx, t, client, namespace, IngressSpec{
				Name:           "echo",
				Host:           host,
				BackendService: backend.Service,
				BackendPort:    backend.Port,
			})
			return ctx
		}).
		Assess("the route is live and the fleet is quiet", func(ctx context.Context, t *testing.T, cfg *envconf.Config) context.Context {
			httpclient.New(t).GET(host, "/").ExpectOK(t)

			// The route answering proves ONE pod reloaded, not that the fleet
			// finished: reloads are paced per pod, so a second pod's reload can
			// still be scheduled. Measuring from here would charge that reload
			// to the failover.
			// A namespace still terminating is another test's fixtures still
			// being withdrawn from the render — a config change that would land
			// inside this measurement and be charged to the failover.
			waitForNoTerminatingNamespaces(ctx, t, clientset)
			oldLeader = currentLeader(ctx, t, clientset)
			waitForQuietFleet(ctx, t, clientset)
			baseline = fleetPlanState(ctx, t, client, clientset)
			t.Logf("leader %s, fleet baseline %v", oldLeader, baseline)
			return ctx
		}).
		Assess("the handover does not touch the fleet", func(ctx context.Context, t *testing.T, cfg *envconf.Config) context.Context {
			handLeaseTo(ctx, t, clientset, standbyController(ctx, t, clientset, oldLeader))
			waitForLeadershipHandover(ctx, t, clientset, oldLeader)

			// The new leader's first reconciliation is what would reload:
			// give it room to run, and to have finished, before reading the
			// counters. The route must keep answering throughout.
			deadline := time.Now().Add(30 * time.Second)
			for time.Now().Before(deadline) {
				httpclient.New(t).GET(host, "/").ExpectOK(t)
				time.Sleep(2 * time.Second)
			}

			// The claim is about reloads, which re-execute the worker and are
			// what a leader change must not cost. A changed plan is not itself
			// a failure: a new leader re-derives the server-slot names, so
			// pod-names.map is rewritten and applied file-only. That is worth
			// reporting, not failing on — the worker is what must not restart.
			after := fleetPlanState(ctx, t, client, clientset)
			for pod, before := range baseline {
				now, ok := after[pod]
				if !ok {
					t.Fatalf("HAProxy pod %s disappeared across the failover", pod)
				}
				if now.appliedPlanID != before.appliedPlanID {
					t.Logf("pod %s was given another plan across the failover (%s → %s)",
						pod, before.appliedPlanID, now.appliedPlanID)
				}
				if now.startTime != before.startTime {
					t.Fatalf("HAProxy pod %s re-executed its worker across the failover "+
						"(start time %v → %v): a new leader must diff against what the pod "+
						"reports, not reload it", pod, before.startTime, now.startTime)
				}
			}
			t.Logf("all %d pods kept their worker across the failover", len(baseline))
			return ctx
		}).
		Assess("the new leader deploys a fresh route", func(ctx context.Context, t *testing.T, cfg *envconf.Config) context.Context {
			// Zero reloads must not mean a wedged controller: the new leader
			// has to be driving the fleet, which only a new route proves.
			namespace := NamespaceForTest(ctx, t, client)
			DumpLogsOnFailure(t, namespace)
			backend := NewEchoServerBackend(ctx, t, client, namespace)
			const freshHost = "leader-change-after.localdev.me"
			NewIngress(ctx, t, client, namespace, IngressSpec{
				Name:           "echo-after",
				Host:           freshHost,
				BackendService: backend.Service,
				BackendPort:    backend.Port,
			})
			httpclient.New(t).GET(freshHost, "/").ExpectOK(t)
			return ctx
		}).
		Feature()

	testEnv.Test(t, feature)
}

// currentLeader returns the controller pod holding the lease.
func currentLeader(ctx context.Context, t *testing.T, cs kubernetes.Interface) string {
	t.Helper()
	var holder string
	err := testutil.WaitForConditionWithDescription(ctx, testutil.WaitConfig{
		InitialInterval: 250 * time.Millisecond,
		MaxInterval:     2 * time.Second,
		Timeout:         60 * time.Second,
		Multiplier:      1.5,
	}, "a controller pod holds the leader lease", func(ctx context.Context) (bool, error) {
		name, err := leaseHolder(ctx, cs)
		if err != nil {
			return false, err
		}
		holder = name
		return true, nil
	})
	if err != nil {
		t.Fatalf("no leader: %v", err)
	}
	return holder
}

// standbyController returns the controller pod that is not the leader.
func standbyController(ctx context.Context, t *testing.T, cs kubernetes.Interface, leader string) string {
	t.Helper()
	for pod := range controllerPodNames(ctx, t, cs) {
		if pod != leader {
			return pod
		}
	}
	t.Fatalf("no standby controller alongside leader %s; the chart must run more than one replica "+
		"for a handover to be testable at all", leader)
	return ""
}

// handLeaseTo rewrites the Lease's holder, which is how a handover happens
// without touching a pod: the leader sees an identity that is not its own and
// stops, the standby sees its own and starts renewing.
func handLeaseTo(ctx context.Context, t *testing.T, cs kubernetes.Interface, standby string) {
	t.Helper()
	leases := cs.CoordinationV1().Leases(ControllerNamespace)
	lease, err := leases.Get(ctx, controllerLeaseName, metav1.GetOptions{})
	if err != nil {
		t.Fatalf("get the controller lease: %v", err)
	}
	now := metav1.NewMicroTime(time.Now())
	transitions := int32(1)
	if lease.Spec.LeaseTransitions != nil {
		transitions = *lease.Spec.LeaseTransitions + 1
	}
	lease.Spec.HolderIdentity = &standby
	lease.Spec.AcquireTime = &now
	lease.Spec.RenewTime = &now
	lease.Spec.LeaseTransitions = &transitions
	if _, err := leases.Update(ctx, lease, metav1.UpdateOptions{}); err != nil {
		t.Fatalf("hand the lease to %s: %v", standby, err)
	}
	t.Logf("handed the lease to %s", standby)
}

// waitForLeadershipHandover blocks until a controller other than previous holds
// the lease and keeps renewing it, so the measurement starts against a leader
// that is actually leading.
func waitForLeadershipHandover(ctx context.Context, t *testing.T, cs kubernetes.Interface, previous string) {
	t.Helper()
	err := testutil.WaitForConditionWithDescription(ctx, testutil.WaitConfig{
		InitialInterval: 500 * time.Millisecond,
		MaxInterval:     3 * time.Second,
		Timeout:         120 * time.Second,
		Multiplier:      1.5,
	}, "another controller holds and renews the lease", func(ctx context.Context) (bool, error) {
		lease, err := cs.CoordinationV1().Leases(ControllerNamespace).
			Get(ctx, controllerLeaseName, metav1.GetOptions{})
		if err != nil {
			return false, fmt.Errorf("get the controller lease: %w", err)
		}
		if lease.Spec.HolderIdentity == nil || *lease.Spec.HolderIdentity == previous {
			return false, fmt.Errorf("%s holds the lease again", previous)
		}
		// A holder that is not renewing is a name this test wrote, not a leader.
		if lease.Spec.RenewTime == nil || time.Since(lease.Spec.RenewTime.Time) > 15*time.Second {
			return false, fmt.Errorf("%s holds the lease but is not renewing it", *lease.Spec.HolderIdentity)
		}
		t.Logf("leadership moved from %s to %s", previous, *lease.Spec.HolderIdentity)
		return true, nil
	})
	if err != nil {
		t.Fatalf("leadership never moved: %v", err)
	}
}

// leaseHolder reads the holder identity off the controller's Lease. The chart
// names the lease after the release, and the identity is the pod name.
func leaseHolder(ctx context.Context, cs kubernetes.Interface) (string, error) {
	leases, err := cs.CoordinationV1().Leases(ControllerNamespace).List(ctx, metav1.ListOptions{})
	if err != nil {
		return "", fmt.Errorf("list leases: %w", err)
	}
	for i := range leases.Items {
		lease := &leases.Items[i]
		if lease.Spec.HolderIdentity == nil || *lease.Spec.HolderIdentity == "" {
			continue
		}
		// Kubernetes' own control-plane leases share the namespace only in
		// single-node clusters; match the release's lease by name prefix.
		if !isControllerLease(lease.Name) {
			continue
		}
		return *lease.Spec.HolderIdentity, nil
	}
	return "", fmt.Errorf("no controller lease has a holder yet")
}

// controllerLeaseName is the Lease the chart names after the release.
const controllerLeaseName = HelmReleaseName

func isControllerLease(name string) bool {
	return name == controllerLeaseName || name == controllerLeaseName+"-controller"
}

// waitForNoTerminatingNamespaces blocks until no namespace is being deleted, so
// a measurement does not start while another test's resources are still
// disappearing from the render.
func waitForNoTerminatingNamespaces(ctx context.Context, t *testing.T, cs kubernetes.Interface) {
	t.Helper()
	err := testutil.WaitForConditionWithDescription(ctx, testutil.WaitConfig{
		InitialInterval: 500 * time.Millisecond,
		MaxInterval:     3 * time.Second,
		Timeout:         2 * time.Minute,
		Multiplier:      1.5,
	}, "no namespace is terminating", func(ctx context.Context) (bool, error) {
		list, err := cs.CoreV1().Namespaces().List(ctx, metav1.ListOptions{})
		if err != nil {
			return false, fmt.Errorf("list namespaces: %w", err)
		}
		for i := range list.Items {
			if list.Items[i].Status.Phase == corev1.NamespaceTerminating {
				return false, fmt.Errorf("%s is still terminating", list.Items[i].Name)
			}
		}
		return true, nil
	})
	if err != nil {
		t.Fatalf("the cluster never went quiet: %v", err)
	}
}

// podPlanState pairs what a pod was given with whether its worker re-executed,
// which is what makes a reload attributable to a cause.
type podPlanState struct {
	appliedPlanID string
	startTime     float64
}

// fleetPlanState reads each pod's applied plan off the HAProxyCfg status and
// its worker start time off its own stats port.
func fleetPlanState(
	ctx context.Context, t *testing.T, client klient.Client, cs kubernetes.Interface,
) map[string]podPlanState {
	t.Helper()
	hc, err := hapticclient.NewForConfig(client.RESTConfig())
	if err != nil {
		t.Fatalf("build haptic clientset: %v", err)
	}
	obj, err := hc.HaproxyTemplateICV1alpha1().HAProxyCfgs(ControllerNamespace).
		Get(ctx, HAProxyConfigName+"-haproxycfg", metav1.GetOptions{})
	if err != nil {
		t.Fatalf("get HAProxyCfg: %v", err)
	}
	plans := map[string]string{}
	for _, pod := range obj.Status.DeployedToPods {
		plans[pod.PodName] = pod.AppliedPlanID
	}

	state := map[string]podPlanState{}
	for pod, startTime := range haproxyWorkerStartTimes(ctx, t, cs) {
		planID, ok := plans[pod]
		if !ok {
			t.Fatalf("HAProxyCfg status reports no plan for pod %s", pod)
		}
		state[pod] = podPlanState{appliedPlanID: planID, startTime: startTime}
	}
	return state
}

// fleetQuietFor is how long every HAProxy worker must keep its start time
// before the fleet counts as settled. It exceeds the chart's default
// minDeploymentInterval (5s), which is the longest a pod may hold a reload
// behind its pacing window, so a scheduled reload has fired by the time this
// returns.
const fleetQuietFor = 12 * time.Second

// waitForQuietFleet blocks until no HAProxy worker has re-executed for
// fleetQuietFor, and returns the settled start times.
func waitForQuietFleet(ctx context.Context, t *testing.T, cs kubernetes.Interface) map[string]float64 {
	t.Helper()
	deadline := time.Now().Add(2 * time.Minute)
	settled := haproxyWorkerStartTimes(ctx, t, cs)
	quietSince := time.Now()

	for time.Now().Before(deadline) {
		time.Sleep(2 * time.Second)
		now := haproxyWorkerStartTimes(ctx, t, cs)
		if !sameStartTimes(settled, now) {
			settled = now
			quietSince = time.Now()
			continue
		}
		if time.Since(quietSince) >= fleetQuietFor {
			return settled
		}
	}
	t.Fatalf("the fleet never stopped reloading: %v", settled)
	return nil
}

func sameStartTimes(a, b map[string]float64) bool {
	if len(a) != len(b) {
		return false
	}
	for pod, value := range a {
		if other, ok := b[pod]; !ok || other != value {
			return false
		}
	}
	return true
}

// haproxyWorkerStartTimes reads haproxy_process_start_time_seconds from every
// HAProxy pod. A reload re-executes the worker, which moves it; nothing else
// does.
func haproxyWorkerStartTimes(ctx context.Context, t *testing.T, cs kubernetes.Interface) map[string]float64 {
	t.Helper()
	times, err := haproxyWorkerStartTimesE(ctx, cs)
	if err != nil {
		t.Fatal(err)
	}
	return times
}

// haproxyWorkerStartTimesE is the non-fatal variant, for pollers that must treat
// a transient scrape blip as "not yet observed" rather than a test abort.
func haproxyWorkerStartTimesE(ctx context.Context, cs kubernetes.Interface) (map[string]float64, error) {
	pods, err := cs.CoreV1().Pods(ControllerNamespace).List(ctx, metav1.ListOptions{
		LabelSelector: LabelSelectorHAProxy,
	})
	if err != nil {
		return nil, fmt.Errorf("list HAProxy pods: %w", err)
	}
	if len(pods.Items) == 0 {
		return nil, fmt.Errorf("no HAProxy pods match %q", LabelSelectorHAProxy)
	}

	times := map[string]float64{}
	for i := range pods.Items {
		name := pods.Items[i].Name
		body, err := apiProxyGet(ctx, name, HAProxyStatsPort, "metrics")
		if err != nil {
			return nil, fmt.Errorf("scrape %s stats port: %w", name, err)
		}
		value, ok := metricValue(body, "haproxy_process_start_time_seconds")
		if !ok {
			return nil, fmt.Errorf("pod %s exposes no haproxy_process_start_time_seconds", name)
		}
		times[name] = value
	}
	return times, nil
}

// metricValue reads one unlabelled sample out of a Prometheus exposition.
func metricValue(exposition, metric string) (float64, bool) {
	for _, line := range strings.Split(exposition, "\n") {
		if !strings.HasPrefix(line, metric+" ") {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) != 2 {
			continue
		}
		if value, err := strconv.ParseFloat(fields[1], 64); err == nil {
			return value, true
		}
	}
	return 0, false
}
