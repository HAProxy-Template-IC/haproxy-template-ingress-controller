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
	"testing"
	"time"
)

// DumpLogsOnFailure registers a t.Cleanup that, if the test fails, writes
// diagnostic artifacts under debug-logs/<sanitised-test-name>/ at the repo
// root. CI uploads this directory as an artifact.
//
// The dump captures everything a future debugger would want without
// requiring a re-run:
//   - Controller pod logs (all containers)
//   - HAProxy pod logs (haproxy, dataplane, spoa-hub containers)
//   - Backend fixture pod logs (echo-server, auth-server, blocklist-server)
//   - Pod events from the test namespace and the controller namespace
//   - HAProxyCfg JSON (with status), so the rendered config and per-pod
//     deployment state survive
//   - The full manifest list of the test namespace
//
// Errors during the dump are logged but never fail the test — this is a
// best-effort diagnostic, not a correctness check.
func DumpLogsOnFailure(t *testing.T, namespace string) {
	t.Helper()
	t.Cleanup(func() {
		if !t.Failed() {
			return
		}
		dumpDir, err := failureDumpDir(t)
		if err != nil {
			t.Logf("DumpLogsOnFailure: setup dump dir: %v", err)
			return
		}
		t.Logf("Test failed — dumping diagnostics to %s", dumpDir)

		// Best-effort dumps. Each in its own helper so one failure
		// doesn't prevent the others.
		dumpCommand(t, dumpDir, "controller-logs.txt",
			"kubectl", "--kubeconfig", kubeconfigPath, "-n", ControllerNamespace,
			"logs", "-l", LabelSelectorController, "--all-containers", "--tail=50000")

		// --prefix tags each line with [pod/<name> container/<name>] so the
		// haproxy / dataplane / spoa-hub containers can be told apart when
		// reading back the dump. Without it, all three containers'
		// stdout collapses into one un-attributable stream — investigating
		// publish-step latency or dataplane errors requires re-running the
		// test just to know which container said what.
		//
		// --tail=50000 (vs the historical 500): the dataplane API at
		// log_level=trace plus per-transaction access lines emits ~200
		// lines/s under parallel-test load. With three containers shared
		// in one selector, 500 lines amounts to <1 s of real-time
		// coverage — a CI failure happening more than a second before
		// dump time has its dataplane log clipped out of the artifact
		// entirely. 50000 covers ~4 minutes of trace-level activity per
		// container, which fits within the artifact upload limit and
		// reliably preserves the window around a probe-loop failure
		// (typical e2e probe loop ≤ 30 s).
		dumpCommand(t, dumpDir, "haproxy-logs.txt",
			"kubectl", "--kubeconfig", kubeconfigPath, "-n", ControllerNamespace,
			"logs", "-l", LabelSelectorHAProxy, "--all-containers", "--prefix", "--tail=50000")

		dumpCommand(t, dumpDir, "backend-fixtures-logs.txt",
			"kubectl", "--kubeconfig", kubeconfigPath, "-n", SharedFixturesNamespace,
			"logs", "--all-containers", "--prefix", "--tail=200", "-l", "")

		dumpCommand(t, dumpDir, "controller-namespace-events.txt",
			"kubectl", "--kubeconfig", kubeconfigPath, "-n", ControllerNamespace,
			"get", "events", "--sort-by=.lastTimestamp")

		dumpCommand(t, dumpDir, "test-namespace-events.txt",
			"kubectl", "--kubeconfig", kubeconfigPath, "-n", namespace,
			"get", "events", "--sort-by=.lastTimestamp")

		dumpCommand(t, dumpDir, "test-namespace-resources.yaml",
			"kubectl", "--kubeconfig", kubeconfigPath, "-n", namespace,
			"get", "all,ingresses,httproutes,secrets,configmaps", "-o", "yaml")

		dumpCommand(t, dumpDir, "haproxycfg.yaml",
			"kubectl", "--kubeconfig", kubeconfigPath, "-n", ControllerNamespace,
			"get", "haproxycfg", "-o", "yaml")

		dumpCommand(t, dumpDir, "controller-pods.yaml",
			"kubectl", "--kubeconfig", kubeconfigPath, "-n", ControllerNamespace,
			"get", "pods", "-o", "yaml")

		// EndpointSlices for the test namespace AND across the cluster. Two
		// files because the test-namespace ones are the load-bearing data for
		// rolling-restart analysis (which IPs / conditions did our backend
		// expose at failure time), while the cluster-wide dump is what
		// disambiguates the aggregated "endpoints modified=1" counters in
		// the controller log when sibling parallel tests churn at the same
		// time.
		dumpCommand(t, dumpDir, "test-namespace-endpointslices.yaml",
			"kubectl", "--kubeconfig", kubeconfigPath, "-n", namespace,
			"get", "endpointslices", "-o", "yaml")
		dumpCommand(t, dumpDir, "all-endpointslices.yaml",
			"kubectl", "--kubeconfig", kubeconfigPath, "-A",
			"get", "endpointslices", "-o", "yaml")

		// HAProxy's view of every server slot — admin_state, operational_state,
		// runtime_addr, runtime_port. Ground truth for "which SRV slot points
		// where right now" that no controller log can match. Captured per
		// HAProxy pod via the dataplane /runtime/backends endpoint family
		// (the only network-accessible surface; HAProxy's stats socket is
		// not reachable from outside the pod). One file per HAProxy pod
		// keeps the dump readable when there are multiple replicas.
		dumpHAProxyRuntimeServers(t, dumpDir)
	})
}

// dumpHAProxyRuntimeServers fetches `GET /runtime/backends` (all backends and
// their runtime server state) from each HAProxy pod's dataplane API and
// writes one file per pod. Without this, post-mortem inspection has to
// reverse-engineer slot mappings from the rendered config + dataplane access
// log — which is exactly the kind of forensics that takes hours.
func dumpHAProxyRuntimeServers(t *testing.T, dumpDir string) {
	t.Helper()

	ctx, cancel := context.WithTimeout(context.Background(), 30*1_000_000_000) // 30s
	defer cancel()

	// Get the HAProxy pod names.
	podsCmd := exec.CommandContext(ctx, "kubectl",
		"--kubeconfig", kubeconfigPath, "-n", ControllerNamespace,
		"get", "pods", "-l", LabelSelectorHAProxy,
		"-o", "jsonpath={.items[*].metadata.name}")
	podsOut, err := podsCmd.Output()
	if err != nil {
		t.Logf("dumpHAProxyRuntimeServers: list pods: %v", err)
		return
	}

	pods := bytes.Fields(podsOut)
	if len(pods) == 0 {
		return
	}

	for _, podBytes := range pods {
		pod := string(podBytes)
		// curl from inside the dataplane container against localhost.
		// Auth: the dataplane API rejects unauthenticated requests on
		// every route, localhost included. The container has
		// $DATAPLANE_USERNAME / $DATAPLANE_PASSWORD in its environment
		// (set from the credentials Secret in the deployment spec); we
		// pass them through curl's -u with shell expansion. Without
		// the auth this returned 401 and every artifact was empty
		// (verified on MR !1019 e2e [3.1]).
		//
		// `GET /configuration/backends` is the only network-exposed
		// collection endpoint that lists every backend.
		curlAuth := `curl -sS --max-time 5 -u "$DATAPLANE_USERNAME:$DATAPLANE_PASSWORD"`
		dumpCommand(t, dumpDir, "haproxy-configured-backends-"+pod+".json",
			"kubectl", "--kubeconfig", kubeconfigPath, "-n", ControllerNamespace,
			"exec", pod, "-c", "dataplane", "--",
			"sh", "-c", curlAuth+" http://localhost:5555/v3/services/haproxy/configuration/backends")

		// Capture the full HAProxy config from disk — single shot, no
		// per-backend iteration needed.
		dumpCommand(t, dumpDir, "haproxy-config-raw-"+pod+".cfg",
			"kubectl", "--kubeconfig", kubeconfigPath, "-n", ControllerNamespace,
			"exec", pod, "-c", "dataplane", "--",
			"sh", "-c", curlAuth+" http://localhost:5555/v3/services/haproxy/configuration/raw")

		// HAProxy's runtime (in-memory) server table via `show servers
		// state` on the master socket, routed to worker 1 with the
		// `@1` prefix. This dumps every backend's slot in a single
		// shot: srv_addr / srv_port / srv_op_state / srv_admin_state
		// per row. Ground truth for "what address was SRV_1 carrying
		// at the moment of the failure" — the dataplane API's
		// /runtime/backends/{name}/servers is the JSON equivalent but
		// needs one curl per backend, which times out the t.Cleanup
		// budget on a large cluster.
		//
		// The chart's HAProxy global section doesn't declare a stats
		// socket (only the master-worker socket); without the `@1`
		// prefix the master interprets the command directly and
		// returns "Unknown command: 'show'".
		dumpCommand(t, dumpDir, "haproxy-show-servers-state-"+pod+".txt",
			"kubectl", "--kubeconfig", kubeconfigPath, "-n", ControllerNamespace,
			"exec", pod, "-c", "haproxy", "--",
			"sh", "-c", `printf '@1 show servers state\n' | socat - UNIX-CONNECT:/etc/haproxy/haproxy-master.sock`)

		// HAProxy's captured request/response parse errors via `show errors`
		// on the master socket (`@1` → worker 1, same routing as above).
		// HAProxy retains the last erroring request and the last erroring
		// response PER PROXY, including the full offending bytes and the byte
		// offset where parsing failed. This is the ground truth for a
		// `<BADREQ>` 400 (which the access log reports only as
		// `"term":"PR--"` with an empty request):
		// it shows exactly which request HAProxy could not parse and where.
		// Load-bearing for diagnosing 400s that surface under reload churn —
		// HAProxy must serve correctly across reloads, not merely avoid 503s
		// (a malformed-request 400 mid-reload is just as much a real bug).
		dumpCommand(t, dumpDir, "haproxy-show-errors-"+pod+".txt",
			"kubectl", "--kubeconfig", kubeconfigPath, "-n", ControllerNamespace,
			"exec", pod, "-c", "haproxy", "--",
			"sh", "-c", `printf '@1 show errors\n' | socat - UNIX-CONNECT:/etc/haproxy/haproxy-master.sock`)
	}
}

// dumpCommand runs cmd and writes its combined output to filename inside
// dumpDir. Failures are logged via t.Logf but do not fail the test.
func dumpCommand(t *testing.T, dumpDir, filename string, cmd string, args ...string) {
	t.Helper()
	out := runCommandCapture(30*time.Second, cmd, args...)
	if writeErr := os.WriteFile(filepath.Join(dumpDir, filename), out, 0644); writeErr != nil {
		t.Logf("DumpLogsOnFailure: write %s: %v", filename, writeErr)
	}
}

// runCommandCapture runs cmd with the given timeout and returns its combined
// output, appending a failure note (including stderr) when the command errors
// so the captured artifact is self-contained even when the command failed.
func runCommandCapture(timeout time.Duration, cmd string, args ...string) []byte {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	c := exec.CommandContext(ctx, cmd, args...)
	var stdout, stderr bytes.Buffer
	c.Stdout = &stdout
	c.Stderr = &stderr
	runErr := c.Run()

	out := stdout.Bytes()
	if runErr != nil {
		out = append(out, []byte(fmt.Sprintf(
			"\n--- command failed: %v\nstderr:\n%s\n", runErr, stderr.String()))...)
	}
	return out
}

// failureDumpDir returns (and creates if needed) a per-test directory
// under <repo>/debug-logs/. The directory name is the test name with
// non-DNS-safe characters replaced by underscores.
func failureDumpDir(t *testing.T) (string, error) {
	root, err := repoRoot()
	if err != nil {
		return "", err
	}
	safe := sanitizeForFilesystem(t.Name())
	dir := filepath.Join(root, "debug-logs", safe)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return "", fmt.Errorf("mkdir %s: %w", dir, err)
	}
	return dir, nil
}

// sanitizeForFilesystem replaces characters that are awkward in filesystem
// paths (slashes from t.Name() of subtests, etc.) with underscores.
func sanitizeForFilesystem(s string) string {
	out := make([]byte, len(s))
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case c >= 'A' && c <= 'Z', c >= 'a' && c <= 'z', c >= '0' && c <= '9',
			c == '-', c == '.', c == '_':
			out[i] = c
		default:
			out[i] = '_'
		}
	}
	return string(out)
}
