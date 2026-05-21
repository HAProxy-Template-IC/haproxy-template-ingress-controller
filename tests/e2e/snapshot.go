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
	"path/filepath"
	"sync"
	"testing"

	"gitlab.com/haproxy-haptic/haptic/tests/e2e/httpclient"
)

// failureSnapshotter dumps live HAProxy state when an httpclient
// poll exhausts its retry budget. Runs BEFORE the test's t.Cleanup
// chain (which deletes per-test Ingresses and similar), so the
// captured state reflects the chart's rendered config and the
// dataplane's on-disk haproxy.cfg at the failing moment — not the
// post-cleanup empty defaults the standard DumpLogsOnFailure
// captures.
//
// Triggered from httpclient.poll() via the package-default
// callback registered by InstallFailureSnapshotter (called from
// TestMain). Per-test throttle: at most one snapshot per t.Name(),
// taken on the first poll timeout. Subsequent timeouts in the same
// test are skipped — they'd snapshot near-identical state and
// double the artifact size.
func failureSnapshotter() httpclient.PollTimeoutSnapshot {
	var (
		mu      sync.Mutex
		snapped = map[string]bool{}
	)
	return func(t *testing.T, description string, _ *httpclient.Response, _ error) {
		mu.Lock()
		if snapped[t.Name()] {
			mu.Unlock()
			return
		}
		snapped[t.Name()] = true
		mu.Unlock()

		dir, err := failureDumpDir(t)
		if err != nil {
			t.Logf("failureSnapshotter: setup dump dir: %v", err)
			return
		}
		// Separate subdir so the at-failure snapshot doesn't get
		// confused with DumpLogsOnFailure's post-cleanup dump.
		// Future debugger reads `at-timeout/` and knows it was
		// captured while the test's fixtures were still alive.
		out := filepath.Join(dir, "at-timeout")
		if err := os.MkdirAll(out, 0o750); err != nil {
			t.Logf("failureSnapshotter: mkdir %s: %v", out, err)
			return
		}

		dumpCommand(t, out, "reason.txt", "echo", description)

		// Chart-rendered HAProxyCfg at the failing moment — what
		// the controller PRODUCED, before the dataplane sync
		// translated it into incremental ops.
		dumpCommand(t, out, "haproxycfg.yaml",
			"kubectl", "--kubeconfig", kubeconfigPath, "-n", ControllerNamespace,
			"get", "haproxycfg", "-o", "yaml")

		// The haproxy pod's /etc/haproxy directory tree — what
		// the dataplane API actually wrote to disk. Comparing
		// against the rendered HAProxyCfg above pins whether a
		// failing routing rule was the chart's fault (missing in
		// the rendered spec) or the dataplane sync's fault
		// (rendered correctly but the on-disk tree diverged).
		// Caught the same-name-ACL bug in MR !970 — rendered
		// config had two ACL lines, on-disk had one.
		dumpCommand(t, out, "etc-haproxy.tar.gz.b64",
			"sh", "-c",
			"kubectl --kubeconfig "+kubeconfigPath+
				" -n "+ControllerNamespace+
				" exec deploy/"+HAProxyDeploymentName+" -c haproxy -- "+
				"tar -czf - -C /etc/haproxy . | base64")

		t.Logf("failureSnapshotter: dumped at-timeout state to %s (description: %s)", out, description)
	}
}

// InstallFailureSnapshotter wires the failure snapshotter into
// the httpclient package's default callback. Called from TestMain
// during suite startup so every Client returned by
// httpclient.New picks it up automatically — no per-test wiring.
//
// Idempotent: subsequent calls overwrite the previous registration.
// Tests that explicitly need a different callback can override per
// Client (future Client-method extension; not currently exposed).
func InstallFailureSnapshotter() {
	httpclient.SetDefaultPollTimeoutSnapshot(failureSnapshotter())
}
