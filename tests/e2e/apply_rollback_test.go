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
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"sigs.k8s.io/e2e-framework/klient"
	"sigs.k8s.io/e2e-framework/pkg/envconf"
	"sigs.k8s.io/e2e-framework/pkg/features"

	"gitlab.com/haproxy-haptic/haptic/pkg/dataplane/agent/api"
	hapticclient "gitlab.com/haproxy-haptic/haptic/pkg/generated/clientset/versioned"
	"gitlab.com/haproxy-haptic/haptic/tests/e2e/httpclient"
	"gitlab.com/haproxy-haptic/haptic/tests/testutil"
)

// TestApplyRollbackOnCorruptCertificate is the fleet-wide-rejection drill.
//
// A TLS Secret is not admission-validated — no webhook sees it — so unusable
// certificate bytes reach the render, and the first thing that judges them is
// HAProxy itself. Every layer that is supposed to contain that is asserted
// here, on the only evidence an operator has:
//
//   - the old certificate is still served (the rollback restored the file set
//     HAProxy had loaded, so the fleet never served the corrupt one),
//   - no request 5xxs and no HAProxy pod leaves Ready during the rejection
//     (agent readiness never reflects apply outcomes — a fleet-correlated
//     rejection must not drain the Service nor fence off the repair),
//   - the rejection is visible: the HAProxyCfg carries `ConfigValidated=False`
//     with HAProxy's own message (and `haptic_apply_rejected_total` moves when
//     the render reached the pods before the gate judged it),
//   - fixing the Secret clears both with no operator action.
func TestApplyRollbackOnCorruptCertificate(t *testing.T) {
	const (
		host       = "apply-rollback.localdev.me"
		secretName = "apply-rollback-cert"
	)

	var (
		client         klient.Client
		clientset      kubernetes.Interface
		namespace      string
		goodCertDER    []byte
		rejected       float64
		acceptedPlanID string
	)

	feature := features.New("Apply rollback: a corrupt certificate never reaches the fleet").
		Setup(func(ctx context.Context, t *testing.T, cfg *envconf.Config) context.Context {
			var err error
			if client, err = cfg.NewClient(); err != nil {
				t.Fatalf("new client: %v", err)
			}
			if clientset, err = newClientsetForE2E(client.RESTConfig()); err != nil {
				t.Fatalf("build clientset: %v", err)
			}
			namespace = NamespaceForTest(ctx, t, client)
			DumpLogsOnFailure(t, namespace)

			backend := NewEchoServerBackend(ctx, t, client, namespace)
			NewTLSSecret(ctx, t, client, namespace, secretName, []string{host})
			NewIngress(ctx, t, client, namespace, IngressSpec{
				Name:           "echo",
				Host:           host,
				BackendService: backend.Service,
				BackendPort:    backend.Port,
				TLSSecretName:  secretName,
			})
			return ctx
		}).
		Assess("the route serves its own certificate", func(ctx context.Context, t *testing.T, cfg *envconf.Config) context.Context {
			httpclient.New(t).HTTPS(host, "/").ExpectOK(t)
			goodCertDER = servedCertificate(ctx, t, host)
			rejected = applyRejectedTotal(ctx, t, clientset)
			acceptedPlanID = settledFleetPlan(ctx, t, clientset)
			return ctx
		}).
		Assess("a corrupt certificate never reaches the fleet", func(ctx context.Context, t *testing.T, cfg *envconf.Config) context.Context {
			readyBefore := readyHAProxyPods(ctx, t, clientset)

			// Probe continuously across the whole rejection window: the
			// guarantee is that traffic never notices, which a single
			// before/after pair cannot show.
			probe := startAvailabilityProbe(t, host, clientset)
			corruptTLSSecret(ctx, t, client, namespace, secretName)

			// The render gate's verdict is the synchronisation point, because
			// it is the one thing that always happens. Whether the render
			// reached the pods first is a race the gate is allowed to win:
			// dispatched-then-refused costs a NACK and a rollback, refused
			// before dispatch costs nothing at all. Both keep the fleet on the
			// certificate HAProxy accepted, which is what is asserted below.
			condition := waitForConfigValidatedCondition(ctx, t, client, metav1.ConditionFalse)
			observed := probe.stop()

			if condition.Message == "" {
				t.Fatal("ConfigValidated=False carries no message: HAProxy's own words are the " +
					"operator's only pointer at what to fix")
			}
			t.Logf("ConfigValidated=False reason=%s message=%s", condition.Reason, condition.Message)

			if got := servedCertificate(ctx, t, host); !bytes.Equal(got, goodCertDER) {
				t.Fatal("the fleet is serving a different certificate: it either loaded the corrupt " +
					"one or was not restored to the file set HAProxy had accepted")
			}
			if observed.failures > 0 {
				t.Fatalf("%d of %d requests failed during the rejection (first: %s); a fleet-wide "+
					"rejection must not reach traffic", observed.failures, observed.attempts, observed.first)
			}
			if observed.minReadyPods < readyBefore {
				t.Fatalf("ready HAProxy pods dropped from %d to %d during the rejection: agent "+
					"readiness must never reflect apply outcomes, or a rejection drains the Service "+
					"and fences off the repair", readyBefore, observed.minReadyPods)
			}

			// Whichever side of the race ran, no pod may be left holding the
			// refused render: the revert is scoped to the pods that took it,
			// and a pod that never got it was never touched.
			waitForFleetBackOnPlan(ctx, t, clientset, acceptedPlanID)

			// Which side of the race ran is evidence, not a verdict — but a
			// dispatched render MUST have been NACKed, never quietly accepted.
			if nacks := applyRejectedTotal(ctx, t, clientset) - rejected; nacks > 0 {
				t.Logf("the render reached the pods first: %v applies refused and rolled back", nacks)
			} else {
				t.Log("the render gate refused the render before it was dispatched; the pods never saw it")
			}
			t.Logf("rejection window: %d requests, 0 failures, ready pods never below %d",
				observed.attempts, observed.minReadyPods)
			return ctx
		}).
		Assess("fixing the Secret clears the condition with no operator action", func(ctx context.Context, t *testing.T, cfg *envconf.Config) context.Context {
			repairTLSSecret(ctx, t, client, namespace, secretName, host)

			waitForConfigValidatedCondition(ctx, t, client, metav1.ConditionTrue)
			httpclient.New(t).HTTPS(host, "/").ExpectOK(t)
			return ctx
		}).
		Feature()

	testEnv.Test(t, feature)
}

// servedCertificate returns the DER of the leaf certificate HAProxy presents
// for host.
func servedCertificate(ctx context.Context, t *testing.T, host string) []byte {
	t.Helper()
	cert, err := httpclient.New(t).PeerCertificate(ctx, host)
	if err != nil {
		t.Fatalf("read the served certificate for %s: %v", host, err)
	}
	return cert.Raw
}

// corruptTLSSecret replaces a TLS Secret's certificate with bytes that are
// PEM-shaped but not a certificate, so the render succeeds and HAProxy is what
// refuses the result.
func corruptTLSSecret(ctx context.Context, t *testing.T, client klient.Client, namespace, name string) {
	t.Helper()
	secret := &corev1.Secret{}
	if err := client.Resources(namespace).Get(ctx, name, namespace, secret); err != nil {
		t.Fatalf("get TLS Secret %s/%s: %v", namespace, name, err)
	}
	secret.Data["tls.crt"] = []byte(
		"-----BEGIN CERTIFICATE-----\nbm90IGEgY2VydGlmaWNhdGU=\n-----END CERTIFICATE-----\n")
	if err := client.Resources(namespace).Update(ctx, secret); err != nil {
		t.Fatalf("corrupt TLS Secret %s/%s: %v", namespace, name, err)
	}
}

// repairTLSSecret puts a fresh, valid certificate back.
func repairTLSSecret(ctx context.Context, t *testing.T, client klient.Client, namespace, name, host string) {
	t.Helper()
	certPEM, keyPEM, err := generateSelfSignedCert([]string{host})
	if err != nil {
		t.Fatalf("generate replacement cert for %s: %v", host, err)
	}
	secret := &corev1.Secret{}
	if err := client.Resources(namespace).Get(ctx, name, namespace, secret); err != nil {
		t.Fatalf("get TLS Secret %s/%s: %v", namespace, name, err)
	}
	secret.Data["tls.crt"] = certPEM
	secret.Data["tls.key"] = keyPEM
	if err := client.Resources(namespace).Update(ctx, secret); err != nil {
		t.Fatalf("repair TLS Secret %s/%s: %v", namespace, name, err)
	}
}

// availabilityObservation is what a probe run saw across its window.
type availabilityObservation struct {
	attempts     int
	failures     int
	first        string
	minReadyPods int
}

// availabilityProbe requests the route continuously until stopped, sampling
// HAProxy pod readiness on every pass.
type availabilityProbe struct {
	cancel context.CancelFunc
	done   chan struct{}
	mu     sync.Mutex
	result availabilityObservation
}

// startAvailabilityProbe keeps a request in flight for the whole window a test
// wants to prove nothing broke in. A 5xx or a transport error is a failure; a
// slow answer is not. Pod readiness is sampled alongside, because a Service
// that drains mid-window and recovers is invisible to a before/after pair.
func startAvailabilityProbe(t *testing.T, host string, cs kubernetes.Interface) *availabilityProbe {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	probe := &availabilityProbe{
		cancel: cancel,
		done:   make(chan struct{}),
		result: availabilityObservation{minReadyPods: -1},
	}
	client := httpclient.New(t)

	go func() {
		defer close(probe.done)
		for ctx.Err() == nil {
			resp, err := client.HTTPS(host, "/").Do(ctx)
			if ctx.Err() != nil {
				return
			}
			ready := countReadyHAProxyPods(ctx, cs)

			probe.mu.Lock()
			probe.result.attempts++
			if ready >= 0 && (probe.result.minReadyPods < 0 || ready < probe.result.minReadyPods) {
				probe.result.minReadyPods = ready
			}
			switch {
			case err != nil:
				probe.result.failures++
				if probe.result.first == "" {
					probe.result.first = err.Error()
				}
			case resp.Status >= 500:
				probe.result.failures++
				if probe.result.first == "" {
					probe.result.first = "status " + strconv.Itoa(resp.Status)
				}
			}
			probe.mu.Unlock()
			time.Sleep(50 * time.Millisecond)
		}
	}()
	return probe
}

func (p *availabilityProbe) stop() availabilityObservation {
	p.cancel()
	<-p.done
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.result
}

// applyRejectedTotal sums haptic_apply_rejected_total across every controller
// pod. Only the leader increments it, but which pod leads can change.
func applyRejectedTotal(ctx context.Context, t *testing.T, cs kubernetes.Interface) float64 {
	t.Helper()
	var total float64
	scraped := 0
	for pod := range controllerPodNames(ctx, t, cs) {
		value, err := labelledMetricSum(ctx, cs, pod, "haptic_apply_rejected_total")
		if err != nil {
			t.Logf("apply-rejected scrape: %v (tolerated)", err)
			continue
		}
		scraped++
		total += value
	}
	if scraped == 0 {
		t.Fatal("apply-rejected scrape: no controller pod's /metrics was reachable")
	}
	return total
}

// labelledMetricSum sums every series of one metric family. The suite's other
// scraper drops labelled lines, and every counter this test reads is labelled
// by pod.
func labelledMetricSum(ctx context.Context, cs kubernetes.Interface, pod, metric string) (float64, error) {
	body, err := cs.CoreV1().Pods(ControllerNamespace).ProxyGet(
		"http", pod, strconv.Itoa(ControllerMetricsPort), "/metrics", nil,
	).DoRaw(ctx)
	if err != nil {
		return 0, fmt.Errorf("scrape %s/metrics: %w", pod, err)
	}
	var sum float64
	for _, line := range strings.Split(string(body), "\n") {
		if !strings.HasPrefix(line, metric) {
			continue
		}
		rest := line[len(metric):]
		if rest != "" && !strings.HasPrefix(rest, "{") && !strings.HasPrefix(rest, " ") {
			continue // a longer metric name that merely shares this prefix
		}
		fields := strings.Fields(line)
		if len(fields) != 2 {
			continue
		}
		if value, err := strconv.ParseFloat(fields[1], 64); err == nil {
			sum += value
		}
	}
	return sum, nil
}

// waitForConfigValidatedCondition blocks until the render gate's verdict on
// the HAProxyCfg reaches want, and returns it.
func waitForConfigValidatedCondition(
	ctx context.Context, t *testing.T, client klient.Client, want metav1.ConditionStatus,
) metav1.Condition {
	t.Helper()
	hc, err := hapticclient.NewForConfig(client.RESTConfig())
	if err != nil {
		t.Fatalf("build haptic clientset: %v", err)
	}
	cfgName := HAProxyConfigName + "-haproxycfg"

	var found metav1.Condition
	err = testutil.WaitForConditionWithDescription(ctx, testutil.WaitConfig{
		InitialInterval: 250 * time.Millisecond,
		MaxInterval:     2 * time.Second,
		Timeout:         90 * time.Second,
		Multiplier:      1.5,
	}, fmt.Sprintf("HAProxyCfg ConfigValidated=%s", want), func(ctx context.Context) (bool, error) {
		obj, err := hc.HaproxyTemplateICV1alpha1().HAProxyCfgs(ControllerNamespace).
			Get(ctx, cfgName, metav1.GetOptions{})
		if err != nil {
			return false, fmt.Errorf("get HAProxyCfg %s: %w", cfgName, err)
		}
		for _, condition := range obj.Status.Conditions {
			if condition.Type != "ConfigValidated" {
				continue
			}
			if condition.Status == want {
				found = condition
				return true, nil
			}
			return false, fmt.Errorf("ConfigValidated is %s (%s)", condition.Status, condition.Reason)
		}
		return false, fmt.Errorf("no ConfigValidated condition yet")
	})
	if err != nil {
		t.Fatalf("ConfigValidated never reached %s: %v", want, err)
	}
	return found
}

// settledFleetPlan is the plan every HAProxy pod applied and runs. It is the
// baseline the rejection must leave the fleet on.
func settledFleetPlan(ctx context.Context, t *testing.T, cs kubernetes.Interface) string {
	t.Helper()
	states := agentStates(ctx, t, cs)
	if len(states) == 0 {
		t.Fatal("no HAProxy pod answered /v1/state before the test even starts")
	}
	settled := ""
	for pod, state := range states {
		if state.AppliedPlanID == "" || state.AppliedPlanID != state.RunningPlanID {
			t.Fatalf("pod %s has not settled: applied=%q running=%q",
				pod, state.AppliedPlanID, state.RunningPlanID)
		}
		if settled != "" && settled != state.AppliedPlanID {
			t.Fatalf("the fleet is split across plans %q and %q", settled, state.AppliedPlanID)
		}
		settled = state.AppliedPlanID
	}
	return settled
}

// waitForFleetBackOnPlan blocks until no pod holds anything but the accepted
// plan.
//
// The agents are the authority here, not the published status: a scoped revert
// puts a pod back without a deployment, so the HAProxyCfg only catches up on
// the next one. Both orderings of the gate/apply race end here — a pod that
// took the refused render is reverted, a pod that never got it never moved.
func waitForFleetBackOnPlan(ctx context.Context, t *testing.T, cs kubernetes.Interface, accepted string) {
	t.Helper()
	var carrying string
	err := testutil.WaitForConditionWithDescription(ctx, testutil.WaitConfig{
		InitialInterval: 500 * time.Millisecond,
		MaxInterval:     2 * time.Second,
		Timeout:         60 * time.Second,
		Multiplier:      1.5,
	}, "no HAProxy pod carries the refused plan", func(ctx context.Context) (bool, error) {
		carrying = ""
		for pod, state := range agentStates(ctx, t, cs) {
			if state.AppliedPlanID != accepted ||
				(state.WorkerOpsPlanID != "" && state.WorkerOpsPlanID != accepted) {
				carrying = fmt.Sprintf("%s applied=%q worker_ops=%q", pod, state.AppliedPlanID, state.WorkerOpsPlanID)
				return false, fmt.Errorf("%s still carries a plan the fleet was not left on", carrying)
			}
		}
		return true, nil
	})
	if err != nil {
		t.Fatalf("the fleet was not put back on the plan HAProxy accepted (%s): %v", accepted, err)
	}
}

// agentStates reads every HAProxy pod's agent state. The agent rejects
// unauthenticated requests on every route, localhost included, so the request
// is made from inside the container with the credentials it already holds.
func agentStates(ctx context.Context, t *testing.T, cs kubernetes.Interface) map[string]*api.State {
	t.Helper()
	pods, err := cs.CoreV1().Pods(ControllerNamespace).List(ctx, metav1.ListOptions{
		LabelSelector: LabelSelectorHAProxy,
	})
	if err != nil {
		t.Fatalf("list HAProxy pods: %v", err)
	}

	const curlState = `curl -sS --max-time 5 -u "$DATAPLANE_USERNAME:$DATAPLANE_PASSWORD" ` +
		`http://localhost:5555/v1/state`
	states := make(map[string]*api.State, len(pods.Items))
	for i := range pods.Items {
		name := pods.Items[i].Name
		out, err := exec.CommandContext(ctx, "kubectl", "--kubeconfig", kubeconfigPath,
			"-n", ControllerNamespace, "exec", name, "-c", "agent", "--",
			"sh", "-c", curlState).Output()
		if err != nil {
			t.Fatalf("read agent state from %s: %v", name, err)
		}
		state := &api.State{}
		if err := json.Unmarshal(out, state); err != nil {
			t.Fatalf("parse agent state from %s: %v", name, err)
		}
		states[name] = state
	}
	return states
}

// readyHAProxyPods is how many HAProxy pods report Ready. Pod readiness is
// what gates Service endpoints, so it is the property the rejection must not
// move.
func readyHAProxyPods(ctx context.Context, t *testing.T, cs kubernetes.Interface) int {
	t.Helper()
	ready := countReadyHAProxyPods(ctx, cs)
	if ready < 0 {
		t.Fatal("could not list HAProxy pods")
	}
	if ready == 0 {
		t.Fatal("no HAProxy pod is ready before the test even starts")
	}
	return ready
}

// countReadyHAProxyPods returns -1 when the list call fails, so a transient
// API error inside the probe loop is not read as a drained Service.
func countReadyHAProxyPods(ctx context.Context, cs kubernetes.Interface) int {
	pods, err := cs.CoreV1().Pods(ControllerNamespace).List(ctx, metav1.ListOptions{
		LabelSelector: LabelSelectorHAProxy,
	})
	if err != nil {
		return -1
	}
	ready := 0
	for i := range pods.Items {
		for _, condition := range pods.Items[i].Status.Conditions {
			if condition.Type == corev1.PodReady && condition.Status == corev1.ConditionTrue {
				ready++
			}
		}
	}
	return ready
}
