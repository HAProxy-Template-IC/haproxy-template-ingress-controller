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
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

// proberSnapshotter captures HAProxy + controller state IMMEDIATELY when a
// probe failure is observed, before retries/cleanup tear the namespace down.
// The existing DumpLogsOnFailure helper runs in t.Cleanup, which fires AFTER
// the test ends — by which time the master socket is gone and namespace state
// has been wiped. The data the existing dump captures (configs, json view of
// backends as of dump time) reflects the END state, not the failure state,
// which is why earlier post-mortem couldn't tell whether SRV_1's address was
// stale at the failure moment.
//
// What this captures, per failure:
//   - `@1 show servers state` from EACH HAProxy pod (live worker, runtime view)
//   - `@1 show servers conn` from EACH pod (per-server connection counts)
//   - Controller pod log tail (last 100 lines) for correlation
//   - Test namespace EndpointSlice yaml at that moment
//   - Wall-clock timestamp of the failure
//
// Files land under debug-logs/<test>/failure-snapshots/<ts>/.
type proberSnapshotter struct {
	t           *testing.T
	namespace   string
	rootDir     string
	mu          sync.Mutex
	snapshotted int
}

// newFailureSnapshotter builds a snapshotter that writes failure-time captures
// to debug-logs/<test>/failure-snapshots/. The directory is created lazily on
// first snapshot so passing runs don't litter the filesystem.
func newProberSnapshotter(t *testing.T, namespace string) *proberSnapshotter {
	t.Helper()
	root, err := repoRoot()
	if err != nil {
		t.Logf("proberSnapshotter: locate repo root: %v (snapshots disabled)", err)
		return nil
	}
	dir := filepath.Join(root, "debug-logs", sanitizeForFilesystem(t.Name()), "failure-snapshots")
	return &proberSnapshotter{t: t, namespace: namespace, rootDir: dir}
}

// snapshot dumps state synchronously. Called from runProbeLoop on every
// failing probe so each failure has its own folder. Synchronous deliberately:
// the failure is already recorded, we want the data ASAP before reload
// churn moves the runtime state further.
func (s *proberSnapshotter) snapshot(failure probeFailure) {
	if s == nil {
		return
	}

	// Per-failure subdirectory, named with millisecond-resolution timestamp
	// so multiple failures within the same second don't overwrite.
	tsLabel := failure.ts.UTC().Format("150405.000")
	dir := filepath.Join(s.rootDir, tsLabel)
	if err := os.MkdirAll(dir, 0755); err != nil {
		s.t.Logf("proberSnapshotter: mkdir %s: %v", dir, err)
		return
	}

	s.mu.Lock()
	s.snapshotted++
	s.mu.Unlock()

	// Manifest of what the failure was (status, duration, error). Lets a
	// human read the directory listing and find the right failure without
	// re-parsing the test log.
	s.writeFile(dir, "MANIFEST.txt", fmt.Sprintf(
		"timestamp_utc: %s\nstatus: %d\nduration_ms: %d\nerror: %v\nnamespace: %s\n",
		failure.ts.UTC().Format(time.RFC3339Nano),
		failure.status,
		failure.dur.Milliseconds(),
		failure.err,
		s.namespace,
	))

	// Cluster-wide CPU/memory utilization at failure time (requires
	// metrics-server, installed by TestMain). This is the load-bearing data
	// for answering which component was actually CPU-pegged when the 503
	// fired — the controller (render→deploy), the dataplane (SRV apply),
	// HAProxy, or the kube control plane — instead of inferring it from
	// resource requests. Sorted by CPU so the hottest pod is first.
	s.dumpCommand(dir, "top-pods-all-namespaces.txt",
		"kubectl", "--kubeconfig", kubeconfigPath, "top", "pods", "-A", "--sort-by=cpu")
	s.dumpCommand(dir, "top-nodes.txt",
		"kubectl", "--kubeconfig", kubeconfigPath, "top", "nodes")

	// Per-HAProxy-pod runtime state captures. The master socket route
	// `@1 show servers state` returns the LIVE worker's view of every
	// server slot — runtime_addr, runtime_port, operational state, admin
	// state, srv_uweight. This is the load-bearing data point for
	// answering "what address did SRV_1 actually have when this request
	// landed". `@1 show servers conn` adds the per-server connection
	// counters so we can also see whether the live worker had any
	// in-flight connections to the affected server at failure time.
	pods := listHAProxyPods(s.t)
	for _, pod := range pods {
		s.dumpCommand(dir, "haproxy-servers-state-"+pod+".txt",
			"kubectl", "--kubeconfig", kubeconfigPath, "-n", ControllerNamespace,
			"exec", pod, "-c", "haproxy", "--",
			"sh", "-c", `printf '@1 show servers state\n' | socat - UNIX-CONNECT:/etc/haproxy/haproxy-master.sock`)
		s.dumpCommand(dir, "haproxy-servers-conn-"+pod+".txt",
			"kubectl", "--kubeconfig", kubeconfigPath, "-n", ControllerNamespace,
			"exec", pod, "-c", "haproxy", "--",
			"sh", "-c", `printf '@1 show servers conn\n' | socat - UNIX-CONNECT:/etc/haproxy/haproxy-master.sock`)
		// Also which worker PID is currently active — under reload churn
		// this tells us how recently the live worker was spawned and
		// whether the runtime state was migrated from the prior worker.
		s.dumpCommand(dir, "haproxy-show-proc-"+pod+".txt",
			"kubectl", "--kubeconfig", kubeconfigPath, "-n", ControllerNamespace,
			"exec", pod, "-c", "haproxy", "--",
			"sh", "-c", `printf 'show proc\n' | socat - UNIX-CONNECT:/etc/haproxy/haproxy-master.sock`)
		// HAProxy's captured request parse errors — THE load-bearing
		// capture for a `<BADREQ>` 400 (`PR--` in the access log).
		// `show errors` retains the last erroring request per proxy with a
		// full hex+ascii dump of the offending bytes AND the exact byte
		// offset + character that violated the protocol. Captured HERE
		// (synchronously, on the failing probe, on the same live worker
		// that rejected the request) rather than only in the t.Cleanup
		// snapshot: under reload churn a later reload resets the worker's
		// error buffer, so the cleanup snapshot — ~14 s late — reliably
		// returns "0 events" (observed, session c952f94a). This per-failure
		// capture fires within milliseconds of the 400.
		s.dumpCommand(dir, "haproxy-show-errors-"+pod+".txt",
			"kubectl", "--kubeconfig", kubeconfigPath, "-n", ControllerNamespace,
			"exec", pod, "-c", "haproxy", "--",
			"sh", "-c", `printf '@1 show errors\n' | socat - UNIX-CONNECT:/etc/haproxy/haproxy-master.sock`)
	}

	// EndpointSlice state for the test namespace at failure time. Tells
	// us what K8s thought the backend pod IPs were when the failure fired.
	s.dumpCommand(dir, "endpointslices.yaml",
		"kubectl", "--kubeconfig", kubeconfigPath, "-n", s.namespace,
		"get", "endpointslices", "-o", "yaml")

	// Controller log tail — last 500 lines, which at TRACE level covers
	// roughly the prior 5–10 s of reconciles. Enough to see what HAPTIC's
	// most recent deploy to each HAProxy pod was, and whether the
	// rolling-restart EP event had been processed.
	s.dumpCommand(dir, "controller-logs-tail.txt",
		"kubectl", "--kubeconfig", kubeconfigPath, "-n", ControllerNamespace,
		"logs", "-l", LabelSelectorController, "--all-containers", "--tail=500")

	// HAProxy stdout (which is where the access log lands in this chart):
	// captures Tw/Tc/Tt timings of the actual failing probe and any
	// adjacent requests. The continuousTailer also captures this, but a
	// per-snapshot tail simplifies bisecting "what was logged just before
	// this failure" without parsing the test-long tail file.
	for _, pod := range pods {
		s.dumpCommand(dir, "haproxy-access-tail-"+pod+".log",
			"kubectl", "--kubeconfig", kubeconfigPath, "-n", ControllerNamespace,
			"logs", pod, "-c", "haproxy", "--tail=400")
	}

	// ── Backend reachability evidence ──────────────────────────────────
	// The HAProxy log shape "sC--/Tc=-1" says "TCP connect to backend
	// failed" — but doesn't say WHY. Three competing explanations:
	//   (a) HAProxy SRV_1 had a stale/wrong address (haptic bug)
	//   (b) Backend was correct but unreachable from THIS HAProxy pod
	//       (pod-network/conntrack path issue specific to one source)
	//   (c) Backend itself wasn't accepting (process not listening)
	// To distinguish, we need three things at failure-time:
	//   1. What address haptic believes each backend pod has (EPS, above)
	//   2. An out-of-band reachability probe from EACH HAProxy pod to
	//      EACH backend pod IP. Failing pod fails AND sister pod succeeds
	//      ⇒ (b). Both fail ⇒ (c). Both succeed ⇒ (a).
	//   3. Backend pod status + recent stdout (echo-server logs every
	//      received request) to see whether any SYN actually arrived.

	s.dumpBackendState(dir)
	s.dumpReachabilityProbes(dir, pods)
	s.dumpTCPLevelProbes(dir, pods)
	s.dumpHAProxyNetworkState(dir, pods)
	s.dumpConntrackState(dir)
	s.dumpRichPodEvents(dir)
	s.dumpKubeletAndKCMTail(dir)
}

// dumpBackendState records what Kubernetes thinks of each backend pod
// (status conditions, phase, IP) and tails their stdout. Echo-server logs
// every received request; absence of the failing request in its stdout is
// strong evidence the SYN never arrived (vs arrived-and-was-rejected).
//
// IMPORTANT: we dump ALL pods in the namespace, not just label-matched
// ones. A pod that's mid-deletion can have its labels stripped by some
// controllers; also catches anything unexpected (sidecar, debug pod).
// Per-pod log capture uses BOTH `kubectl logs` (current container) and
// `kubectl logs --previous` (last terminated container) so we still see
// stdout from a pod whose container has already exited on SIGTERM.
func (s *proberSnapshotter) dumpBackendState(dir string) {
	// All pods, full YAML including deletionTimestamp / container status / IP.
	s.dumpCommand(dir, "all-pods.yaml",
		"kubectl", "--kubeconfig", kubeconfigPath, "-n", s.namespace,
		"get", "pods", "-o", "yaml")

	// Per-pod current + previous stdout. Per-pod files because a single
	// `kubectl logs -l app=…` stream doesn't differentiate which line
	// came from which pod, which is exactly what we need to know in a
	// rolling-restart window where two same-app pods coexist.
	out, err := s.kubectlOut(s.namespace, "get", "pods", "-l", "app=echo-server",
		"-o", "jsonpath={.items[*].metadata.name}")
	if err != nil {
		s.t.Logf("proberSnapshotter: list backend pods for logs: %v", err)
		return
	}
	for _, pod := range strings.Fields(string(out)) {
		s.dumpCommand(dir, "backend-"+pod+"-current.log",
			"kubectl", "--kubeconfig", kubeconfigPath, "-n", s.namespace,
			"logs", pod, "--tail=400")
		// --previous returns the last terminated container's stdout. If
		// the container hasn't restarted this will error with "previous
		// terminated container not found"; we still want the file so the
		// directory listing shows the attempt was made.
		s.dumpCommand(dir, "backend-"+pod+"-previous.log",
			"kubectl", "--kubeconfig", kubeconfigPath, "-n", s.namespace,
			"logs", pod, "--previous", "--tail=400")
	}
}

// dumpTCPLevelProbes does a pure SYN/connect test from each HAProxy pod
// to each backend pod IP at snapshot time — the layer below the existing
// curl HTTP probe. Distinguishes "TCP connect refused/RST" (process dead)
// from "TCP connect timeout" (path dropped) from "TCP connect OK but HTTP
// hangs" (different failure shape). socat is in the haproxy-debian image
// (already used by the chart's reload script); bash's /dev/tcp works on
// debian's bash. Output captures stderr so socat/bash error messages are
// preserved verbatim.
func (s *proberSnapshotter) dumpTCPLevelProbes(dir string, haproxyPods []string) {
	out, err := s.kubectlOut(s.namespace, "get", "pods",
		"-o", "jsonpath={range .items[*]}{.status.podIP} {.metadata.name}{\"\\n\"}{end}")
	if err != nil {
		return
	}

	type backend struct{ ip, name string }
	var backends []backend
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		fields := strings.Fields(line)
		if len(fields) < 2 || fields[0] == "" {
			continue
		}
		backends = append(backends, backend{ip: fields[0], name: fields[1]})
	}

	if len(backends) == 0 {
		s.writeFile(dir, "tcp-probes.txt", "no backend pods with podIP at snapshot time\n")
		return
	}

	for _, hapod := range haproxyPods {
		for _, b := range backends {
			fname := fmt.Sprintf("tcp-from-%s-to-%s-%s.txt", hapod, b.name, b.ip)
			// Three independent SYN attempts with 1 s timeout each:
			// 1. bash's /dev/tcp redirect — pure connect, no payload.
			// 2. socat ... CONNECT-TIMEOUT — exits 0 on connect, !=0 otherwise.
			// 3. The stats socket from socat reports remote-end behaviour
			//    (e.g. RST vs no SYN-ACK) in its stderr message.
			script := fmt.Sprintf(`set +e
echo '--- bash /dev/tcp/%[1]s/80 ---'
( timeout 2 bash -c 'echo > /dev/tcp/%[1]s/80' ) 2>&1
echo "bash_exit=$?"
echo
echo '--- socat - TCP:%[1]s:80,connect-timeout=2 ---'
( echo "" | timeout 3 socat - TCP:%[1]s:80,connect-timeout=2 ) 2>&1
echo "socat_exit=$?"
echo
echo '--- ss state, source IP=%[2]s ---'
ss -tn dst %[1]s 2>&1 || echo 'ss not available'
`, b.ip, hapod)
			s.dumpCommand(dir, fname,
				"kubectl", "--kubeconfig", kubeconfigPath, "-n", ControllerNamespace,
				"exec", hapod, "-c", "haproxy", "--",
				"sh", "-c", script)
		}
	}
}

// dumpHAProxyNetworkState records the HAProxy pod's view of the local
// kernel network stack: TCP connection table (`ss -tn`), ARP/neighbour
// cache, routing table. Useful for detecting cases where pod-network
// state diverged from the cluster (stale ARP, missing route, etc.).
func (s *proberSnapshotter) dumpHAProxyNetworkState(dir string, haproxyPods []string) {
	for _, pod := range haproxyPods {
		script := `echo '--- ss -tn (TCP sockets) ---'
ss -tn 2>&1 || netstat -tn 2>&1 || echo 'no ss/netstat'
echo
echo '--- ss -tnp (with processes) ---'
ss -tnp 2>&1 || true
echo
echo '--- ip neigh (ARP/ND cache) ---'
ip neigh 2>&1 || arp -an 2>&1 || echo 'no ip/arp'
echo
echo '--- ip route ---'
ip route 2>&1 || route -n 2>&1 || echo 'no ip/route'
echo
echo '--- ip addr ---'
ip addr 2>&1 || ifconfig 2>&1 || echo 'no ip/ifconfig'
`
		s.dumpCommand(dir, "haproxy-netstate-"+pod+".txt",
			"kubectl", "--kubeconfig", kubeconfigPath, "-n", ControllerNamespace,
			"exec", pod, "-c", "haproxy", "--",
			"sh", "-c", script)
	}
}

// dumpRichPodEvents captures Kubernetes events with FULL RFC3339
// timestamps (the default `kubectl get events` output uses "Nm ago"
// relative format which loses ms precision and floats as the snapshot
// runs). We sort by lastTimestamp so the ordering matches kubelet's
// event emission order.
func (s *proberSnapshotter) dumpRichPodEvents(dir string) {
	// Full RFC3339 timestamps via custom-columns.
	s.dumpCommand(dir, "events-rfc3339.txt",
		"kubectl", "--kubeconfig", kubeconfigPath, "-n", s.namespace,
		"get", "events", "--sort-by=.lastTimestamp",
		"-o", "custom-columns=FIRST:.firstTimestamp,LAST:.lastTimestamp,TYPE:.type,REASON:.reason,OBJECT:.involvedObject.kind/.involvedObject.name,MESSAGE:.message")
	// Also full YAML for any field we forget to extract.
	s.dumpCommand(dir, "events-full.yaml",
		"kubectl", "--kubeconfig", kubeconfigPath, "-n", s.namespace,
		"get", "events", "--sort-by=.lastTimestamp", "-o", "yaml")
}

// dumpKubeletAndKCMTail pulls the recent kubelet and kube-controller-manager
// logs from the kind control-plane container via docker exec. These cover
// the kube-api-side timing that's invisible from inside the cluster:
//   - kubelet's PATCH to Pod.Status (ready/terminating transitions)
//   - kube-controller-manager's EndpointSlice controller decisions
//     (deciding to flip an endpoint to terminating, deciding to drop it)
//
// Best-effort: docker exec may not work in every CI runner topology.
// Failure is logged and ignored.
func (s *proberSnapshotter) dumpKubeletAndKCMTail(dir string) {
	// Kubelet — runs as a systemd unit inside the kind node.
	s.dumpCommand(dir, "kubelet-tail.log",
		"docker", "exec", ClusterName+"-control-plane",
		"sh", "-c", `journalctl -u kubelet --no-pager --since "20 seconds ago" 2>&1 | tail -500 || true`)
	// kube-controller-manager and kube-apiserver run as static pods, so
	// their logs are accessible via /var/log/containers/.
	s.dumpCommand(dir, "kcm-tail.log",
		"docker", "exec", ClusterName+"-control-plane",
		"sh", "-c", `tail -500 /var/log/containers/kube-controller-manager-*.log 2>&1 || echo 'kcm log not found'`)
	s.dumpCommand(dir, "kube-apiserver-tail.log",
		"docker", "exec", ClusterName+"-control-plane",
		"sh", "-c", `tail -500 /var/log/containers/kube-apiserver-*.log 2>&1 || echo 'apiserver log not found'`)
	// Conntrack table state on the node, in addition to the counters
	// already captured by dumpConntrackState. Full table is big but the
	// filter narrows to the test namespace pod IPs.
	s.dumpCommand(dir, "conntrack-table-sample.txt",
		"docker", "exec", ClusterName+"-control-plane",
		"sh", "-c", `conntrack -L 2>&1 | head -200 || echo 'conntrack not available'`)
}

// kubectlOut runs kubectl get with a tight timeout and returns stdout.
// Centralised so the various snapshot helpers don't each duplicate the
// timeout+kubeconfig boilerplate.
func (s *proberSnapshotter) kubectlOut(namespace string, args ...string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	full := append([]string{"--kubeconfig", kubeconfigPath, "-n", namespace}, args...)
	return exec.CommandContext(ctx, "kubectl", full...).Output()
}

// dumpReachabilityProbes runs an out-of-band curl from each HAProxy pod
// to each backend pod IP. Asymmetric results (some HAProxy pods reach,
// others can't) are hard evidence of a pod-network issue rather than a
// haptic config issue. The curl uses --connect-timeout 2 to match the
// scale of HAProxy's 3×timeout-connect retry budget without blocking the
// snapshot for long. The output format is a single line per probe so the
// directory listing is grep-friendly.
func (s *proberSnapshotter) dumpReachabilityProbes(dir string, haproxyPods []string) {
	// Fetch backend pod IPs via kubectl. We deliberately do this at
	// snapshot time (not test setup time) because rolling-restart
	// scenarios mean the IPs change mid-test.
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, "kubectl",
		"--kubeconfig", kubeconfigPath, "-n", s.namespace,
		"get", "pods", "-l", "app=echo-server",
		"-o", "jsonpath={range .items[*]}{.status.podIP} {.metadata.name}{\"\\n\"}{end}").Output()
	if err != nil {
		s.t.Logf("proberSnapshotter: list backend pods: %v", err)
		return
	}

	type backend struct{ ip, name string }
	var backends []backend
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		fields := strings.Fields(line)
		if len(fields) < 2 || fields[0] == "" {
			continue
		}
		backends = append(backends, backend{ip: fields[0], name: fields[1]})
	}
	if len(backends) == 0 {
		s.writeFile(dir, "reachability-probes.txt", "no backend pods with podIP at snapshot time\n")
		return
	}

	// One file per (haproxy-pod, backend-pod) pair. Filename layout
	// makes it trivial to grep "did pod X reach IP Y at this failure"
	// without parsing structure.
	for _, hapod := range haproxyPods {
		for _, b := range backends {
			fname := fmt.Sprintf("reach-from-%s-to-%s-%s.txt", hapod, b.name, b.ip)
			s.dumpCommand(dir, fname,
				"kubectl", "--kubeconfig", kubeconfigPath, "-n", ControllerNamespace,
				"exec", hapod, "-c", "haproxy", "--",
				"curl", "-sS", "--connect-timeout", "2", "--max-time", "3",
				"-o", "/dev/null",
				"-w", "http=%{http_code} connect=%{time_connect}s total=%{time_total}s errno=%{errormsg}\n",
				fmt.Sprintf("http://%s:80/", b.ip))
		}
	}
}

// dumpConntrackState captures the kind node's conntrack counters via
// docker exec on the kind control-plane container. If the conntrack
// table is at/near nf_conntrack_max the kernel drops new SYNs silently
// — a known kind-on-CI failure shape that masquerades as backend
// unreachability. Two counters matter:
//   - /proc/sys/net/netfilter/nf_conntrack_count  (current)
//   - /proc/sys/net/netfilter/nf_conntrack_max    (limit)
//
// /proc/net/stat/nf_conntrack adds drop/insert_failed counters
// per-CPU, which surface even transient pressure.
//
// Best-effort: `docker exec` may not work in every CI runner topology.
// Failure is logged and ignored.
func (s *proberSnapshotter) dumpConntrackState(dir string) {
	script := strings.Join([]string{
		`echo "=== nf_conntrack_count ==="`,
		`cat /proc/sys/net/netfilter/nf_conntrack_count 2>&1 || true`,
		`echo "=== nf_conntrack_max ==="`,
		`cat /proc/sys/net/netfilter/nf_conntrack_max 2>&1 || true`,
		`echo "=== /proc/net/stat/nf_conntrack (per-CPU counters) ==="`,
		`cat /proc/net/stat/nf_conntrack 2>&1 || true`,
		`echo "=== conntrack -S (stats summary) ==="`,
		`conntrack -S 2>&1 || true`,
	}, "; ")
	s.dumpCommand(dir, "conntrack-stats.txt",
		"docker", "exec", ClusterName+"-control-plane", "sh", "-c", script)
}

// dumpCommand runs cmd with a tight timeout (10s) and writes its combined
// output. We use a short timeout because the test is in flight — we don't
// want a snapshot to block the next probe interval.
func (s *proberSnapshotter) dumpCommand(dir, filename string, cmd string, args ...string) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	c := exec.CommandContext(ctx, cmd, args...)
	var out, stderr bytes.Buffer
	c.Stdout = &out
	c.Stderr = &stderr
	runErr := c.Run()
	body := out.Bytes()
	if runErr != nil {
		body = append(body, []byte(fmt.Sprintf(
			"\n--- command failed: %v\nstderr:\n%s\n", runErr, stderr.String()))...)
	}
	s.writeFile(dir, filename, string(body))
}

// writeFile is best-effort — log on error, don't fail the test. The failure
// snapshot is diagnostic, not a correctness gate.
func (s *proberSnapshotter) writeFile(dir, name, body string) {
	if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0644); err != nil {
		s.t.Logf("proberSnapshotter: write %s: %v", name, err)
	}
}

// listHAProxyPods returns the names of HAProxy pods in ControllerNamespace.
// Used to fan failure-time runtime captures out across replicas.
func listHAProxyPods(t *testing.T) []string {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, "kubectl",
		"--kubeconfig", kubeconfigPath, "-n", ControllerNamespace,
		"get", "pods", "-l", LabelSelectorHAProxy,
		"-o", "jsonpath={.items[*].metadata.name}").Output()
	if err != nil {
		t.Logf("listHAProxyPods: %v", err)
		return nil
	}
	return strings.Fields(string(out))
}

// ─────────────────────────────────────────────────────────────────────
// Continuous tailers
//
// The proberSnapshotter above is point-in-time: it fires after a probe
// failure is observed and captures state at THAT moment. By construction,
// that moment is hundreds of ms (and often seconds) after the actual
// failure: the failing probe must time out, the test goroutine records
// it, and the snapshot's first kubectl exec then has to round-trip. By
// the time the data lands, HAProxy may have reloaded, the dying backend
// pod may have been deleted, EPS may have flipped to the steady state.
//
// continuousTailer addresses this by starting kubectl logs / get -w
// processes at test setup time and letting them run for the whole test.
// Their output streams to files in debug-logs/<test>/continuous/, which
// the CI artifact upload picks up alongside the failure snapshots. The
// continuous data is what tells you "what was in HAProxy's access log
// 5 ms before the failure" — a question the snapshot, no matter how
// rich, can't answer because it runs too late.
//
// The tailers track pod lifecycle on the fly: when a new backend pod
// appears (rolling restart), a tailer for it starts; when a pod goes
// away, its tailer exits naturally as kubectl logs returns.
type continuousTailer struct {
	t         *testing.T
	namespace string
	rootDir   string
	cancel    context.CancelFunc
	wg        sync.WaitGroup
}

// newContinuousTailer starts the background tailers. Call from test
// Setup AFTER the namespace + backend exist. The returned tailer
// registers itself for cleanup on test end.
func newContinuousTailer(t *testing.T, namespace string) *continuousTailer {
	t.Helper()
	root, err := repoRoot()
	if err != nil {
		t.Logf("continuousTailer: locate repo root: %v (continuous capture disabled)", err)
		return nil
	}
	dir := filepath.Join(root, "debug-logs", sanitizeForFilesystem(t.Name()), "continuous")
	if err := os.MkdirAll(dir, 0755); err != nil {
		t.Logf("continuousTailer: mkdir %s: %v", dir, err)
		return nil
	}

	ctx, cancel := context.WithCancel(context.Background())
	ct := &continuousTailer{t: t, namespace: namespace, rootDir: dir, cancel: cancel}

	// HAProxy access logs (one stream per HAProxy pod). The HAProxy
	// container's stdout is its access log + lifecycle messages, so
	// `kubectl logs -f` gives us the wire-level request record.
	for _, pod := range listHAProxyPods(t) {
		ct.tailPodLog(ctx, ControllerNamespace, pod, "haproxy", "haproxy-"+pod+".log")
	}

	// Backend pod stdout — managed dynamically since pods come and go
	// during a rolling restart.
	ct.startBackendPodReconciler(ctx)

	// Watch EPS yaml: every kube-api commit on EndpointSlices for the
	// test namespace prints the new state. Sub-second arrival times of
	// EPS gens are captured by the watch's own line buffering — each
	// event appears in this file at the moment kube-api emitted it to
	// the watch connection (modulo network).
	ct.kubectlStreamToFile(ctx, "eps-watch.yaml", "-n", ct.namespace,
		"get", "endpointslices", "-w", "-o", "yaml")

	// Watch events — full text, no relative timestamps.
	ct.kubectlStreamToFile(ctx, "events-watch.yaml", "-n", ct.namespace,
		"get", "events", "-w", "-o", "yaml")

	// kubelet log from the kind control-plane — best-effort, requires
	// docker exec access from the runner.
	ct.tailKubeletViaDocker(ctx)

	// Packet capture inside each HAProxy pod's network namespace, via
	// `docker exec <kind-node> nsenter -t <pod-pid> -n tcpdump`. This
	// is the layer below every other observation we have: when an
	// HAProxy worker reports sC-- (connect failed), the pcap shows
	// whether the SYN ever left the pod and whether a SYN-ACK came
	// back. The CONNECT_TIME=193ms / Tc=-1 pattern observed in CI
	// can be explained by ARP timeout, conntrack drop, or veth queue
	// — only the wire can disambiguate. Best-effort; the entire path
	// (docker exec → crictl → nsenter → tcpdump) can fail on any one
	// of those tools missing from the kind image.
	ct.startHAProxyPacketCaptures(ctx)

	t.Cleanup(func() { ct.stop() })
	return ct
}

// stop cancels all tailers and waits up to 3 s for them to flush.
func (ct *continuousTailer) stop() {
	if ct == nil {
		return
	}
	ct.cancel()
	done := make(chan struct{})
	go func() {
		ct.wg.Wait()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		ct.t.Logf("continuousTailer: timeout waiting for tailers to exit")
	}
}

// tailPodLog streams `kubectl logs -f` for one container to a file.
//
// --tail=1000 backfills the most recent 1000 lines before streaming new
// ones. This catches pods that became live BEFORE our 500 ms reconciler
// got around to starting their tailer — without backfill, the first
// few hundred ms of stdout from a freshly-created pod is invisible,
// which silently misleads any analysis that compares stdout
// timestamps. (Burned on this in pipeline 2561396045: 9rtp7's first
// request was at 37.499 per PCAP but the backend log file's first
// entry was 38.502, giving the false appearance of an 1 s gap between
// old-pod-down and new-pod-up.) 1000 lines is roughly the most we'd
// expect any single backend pod to log in the brief window between
// creation and tailer-attach.
func (ct *continuousTailer) tailPodLog(ctx context.Context, ns, pod, container, filename string) {
	ct.wg.Add(1)
	go func() {
		defer ct.wg.Done()
		f, err := os.OpenFile(filepath.Join(ct.rootDir, filename),
			os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0644)
		if err != nil {
			ct.t.Logf("continuousTailer: open %s: %v", filename, err)
			return
		}
		defer f.Close()
		cmd := exec.CommandContext(ctx, "kubectl",
			"--kubeconfig", kubeconfigPath,
			"logs", "-f", "-n", ns, pod, "-c", container, "--tail=1000")
		cmd.Stdout = f
		cmd.Stderr = f
		_ = cmd.Run()
	}()
}

// kubectlStreamToFile runs a kubectl command and streams its stdout to
// a file in the tailer's root directory. Used for `kubectl get -w …`
// where the command itself is long-lived and emits events as they happen.
func (ct *continuousTailer) kubectlStreamToFile(ctx context.Context, filename string, args ...string) {
	ct.wg.Add(1)
	go func() {
		defer ct.wg.Done()
		f, err := os.Create(filepath.Join(ct.rootDir, filename))
		if err != nil {
			ct.t.Logf("continuousTailer: create %s: %v", filename, err)
			return
		}
		defer f.Close()
		full := append([]string{"--kubeconfig", kubeconfigPath}, args...)
		cmd := exec.CommandContext(ctx, "kubectl", full...)
		cmd.Stdout = f
		cmd.Stderr = f
		_ = cmd.Run()
	}()
}

// tailKubeletViaDocker streams the kind node's kubelet log via docker exec.
// Best-effort; many CI topologies don't expose Docker from inside the test
// container.
func (ct *continuousTailer) tailKubeletViaDocker(ctx context.Context) {
	ct.wg.Add(1)
	go func() {
		defer ct.wg.Done()
		f, err := os.Create(filepath.Join(ct.rootDir, "kubelet.log"))
		if err != nil {
			return
		}
		defer f.Close()
		cmd := exec.CommandContext(ctx, "docker", "exec",
			ClusterName+"-control-plane",
			"sh", "-c", `journalctl -u kubelet -f --no-pager 2>&1 || echo 'kubelet log unavailable'`)
		cmd.Stdout = f
		cmd.Stderr = f
		_ = cmd.Run()
	}()
}

// startHAProxyPacketCaptures starts tcpdump inside each HAProxy pod's
// network namespace. The output stream is the pod's wire-level activity
// to backend pods, which is what we need to distinguish "haptic SRV_1
// stale" from "kernel dropped the SYN" from "backend RST'd" — none of
// which is observable from haptic-side or HAProxy-side logs alone.
//
// Plumbing path:
//   1. docker exec into the kind control-plane container (we already do
//      this for kubelet log capture)
//   2. install tcpdump there if missing (kindest/node may or may not
//      have it; apt-get is best-effort)
//   3. resolve the HAProxy pod's haproxy-container PID via crictl
//      (containerd's CRI lookup tool, present on the kind node)
//   4. nsenter -t <pid> -n joins that container's network namespace
//   5. tcpdump -w - streams pcap to stdout, which the docker exec
//      passes back to us, which we redirect to a file.
//
// All five steps are best-effort: any failure (no tcpdump, no crictl,
// no PID, namespace mismatch) gets logged but doesn't fail the test.
// The pcap files are named tcpdump-<pod>.pcap so the failure-snapshot
// analyzer can correlate them with the access-log entries.
func (ct *continuousTailer) startHAProxyPacketCaptures(ctx context.Context) {
	// Idempotent install — runs once, no-op if tcpdump is already there.
	installCtx, installCancel := context.WithTimeout(ctx, 30*time.Second)
	installCmd := exec.CommandContext(installCtx, "docker", "exec",
		ClusterName+"-control-plane",
		"sh", "-c", `command -v tcpdump >/dev/null 2>&1 || (apt-get update -qq >/dev/null 2>&1 && apt-get install -y -qq tcpdump >/dev/null 2>&1)`)
	if err := installCmd.Run(); err != nil {
		ct.t.Logf("continuousTailer: install tcpdump in kind node: %v (packet capture disabled)", err)
		installCancel()
		return
	}
	installCancel()

	for _, pod := range listHAProxyPods(ct.t) {
		ct.startTcpdumpForPod(ctx, pod)
	}
}

// startTcpdumpForPod resolves the HAProxy container's PID and spawns
// tcpdump inside its network namespace, streaming pcap to a file.
func (ct *continuousTailer) startTcpdumpForPod(ctx context.Context, pod string) {
	// Resolve PID first (synchronous, short timeout). If it fails, no
	// tailer goroutine to launch.
	pidCtx, pidCancel := context.WithTimeout(ctx, 5*time.Second)
	defer pidCancel()
	// crictl lookup script — finds the haproxy container's host-side PID.
	// We use --label to match the pod by its k8s name + namespace, which
	// is more robust than name-matching crictl's truncated output.
	script := fmt.Sprintf(`set -e
POD_ID=$(crictl pods --label "io.kubernetes.pod.name=%s" --label "io.kubernetes.pod.namespace=%s" -q | head -1)
[ -z "$POD_ID" ] && { echo "no sandbox for pod %s"; exit 1; }
CONT=$(crictl ps --pod $POD_ID --name haproxy -q | head -1)
[ -z "$CONT" ] && { echo "no haproxy container in pod %s"; exit 1; }
crictl inspect $CONT | grep -m1 '"pid"' | sed 's/.*: //; s/,//; s/ //g'`,
		pod, ControllerNamespace, pod, pod)
	pidOut, err := exec.CommandContext(pidCtx, "docker", "exec",
		ClusterName+"-control-plane", "sh", "-c", script).CombinedOutput()
	if err != nil {
		ct.t.Logf("startTcpdumpForPod %s: pid resolve failed: %s (%v)", pod, strings.TrimSpace(string(pidOut)), err)
		return
	}
	pid := strings.TrimSpace(string(pidOut))
	if pid == "" || pid == "null" || pid == "0" {
		ct.t.Logf("startTcpdumpForPod %s: empty/invalid pid %q", pod, pid)
		return
	}

	ct.wg.Add(1)
	go func() {
		defer ct.wg.Done()
		f, err := os.Create(filepath.Join(ct.rootDir, "tcpdump-"+pod+".pcap"))
		if err != nil {
			ct.t.Logf("startTcpdumpForPod %s: create pcap: %v", pod, err)
			return
		}
		defer f.Close()
		errF, _ := os.Create(filepath.Join(ct.rootDir, "tcpdump-"+pod+".stderr"))
		if errF != nil {
			defer errF.Close()
		}

		// Filter:
		//   - tcp port 80 / 8080 / 8443: backend traffic (echo-server,
		//     haproxy-demo-backend, ssl backends)
		//   - tcp port 5555: dataplane API on this same pod (so haptic's
		//     own pushes are visible if they're contributing to the
		//     contention)
		//   - icmp: ARP misses / unreachables
		// snap length 2048: enough to capture full HTTP request/response
		// headers, not just flags/timestamps. The original 96 bytes was
		// tuned for the 503/SYN-drop analysis (flags + 4-tuple only), but
		// it clips the HTTP payload at ~24 bytes — which makes a
		// `<BADREQ>` 400 undiagnosable (you can't see WHICH header/byte
		// HAProxy rejected). 2048 covers any realistic GET request's
		// headers (+ appended garbage, if the request is being corrupted)
		// while staying bounded; a `GET /` probe is ~200-400 bytes.
		// -U flushes after each packet so a SIGTERM at test cleanup
		// doesn't truncate the last second of capture.
		cmd := exec.CommandContext(ctx, "docker", "exec",
			ClusterName+"-control-plane",
			"nsenter", "-t", pid, "-n",
			"tcpdump", "-i", "any", "-U", "-w", "-", "-s", "2048",
			"(tcp and (port 80 or port 8080 or port 8443 or port 5555)) or icmp")
		cmd.Stdout = f
		if errF != nil {
			cmd.Stderr = errF
		}
		_ = cmd.Run()
	}()
}

// startBackendPodReconciler watches for backend pods appearing (e.g.
// the new pod created by a rolling restart) and starts a tailer for
// each. Naturally handles pods going away: kubectl logs -f exits on
// pod deletion, the goroutine completes, and we don't re-spawn.
func (ct *continuousTailer) startBackendPodReconciler(ctx context.Context) {
	ct.wg.Add(1)
	go func() {
		defer ct.wg.Done()
		active := make(map[string]bool)
		ticker := time.NewTicker(500 * time.Millisecond)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
			}
			listCtx, listCancel := context.WithTimeout(ctx, 3*time.Second)
			out, err := exec.CommandContext(listCtx, "kubectl",
				"--kubeconfig", kubeconfigPath, "-n", ct.namespace,
				"get", "pods", "-l", "app=echo-server",
				"-o", "jsonpath={.items[*].metadata.name}").Output()
			listCancel()
			if err != nil {
				continue
			}
			for _, p := range strings.Fields(string(out)) {
				if active[p] {
					continue
				}
				active[p] = true
				ct.tailPodLog(ctx, ct.namespace, p, "server", "backend-"+p+".log")
			}
		}
	}()
}
