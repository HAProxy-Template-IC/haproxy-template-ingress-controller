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
			"logs", "-l", LabelSelectorController, "--all-containers", "--tail=500")

		// --prefix tags each line with [pod/<name> container/<name>] so the
		// haproxy / dataplane / spoa-hub containers can be told apart when
		// reading back the dump. Without it, all three containers'
		// stdout collapses into one un-attributable stream — investigating
		// publish-step latency or dataplane errors requires re-running the
		// test just to know which container said what.
		dumpCommand(t, dumpDir, "haproxy-logs.txt",
			"kubectl", "--kubeconfig", kubeconfigPath, "-n", ControllerNamespace,
			"logs", "-l", LabelSelectorHAProxy, "--all-containers", "--prefix", "--tail=500")

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
	})
}

// dumpCommand runs cmd and writes its combined output to filename inside
// dumpDir. Failures are logged via t.Logf but do not fail the test.
func dumpCommand(t *testing.T, dumpDir, filename string, cmd string, args ...string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 30*1_000_000_000) // 30s
	defer cancel()

	c := exec.CommandContext(ctx, cmd, args...)
	var stdout, stderr bytes.Buffer
	c.Stdout = &stdout
	c.Stderr = &stderr
	runErr := c.Run()

	out := stdout.Bytes()
	if runErr != nil {
		// Include stderr and the failure note so the artifact is
		// self-contained even when the command itself failed.
		out = append(out, []byte(fmt.Sprintf(
			"\n--- command failed: %v\nstderr:\n%s\n",
			runErr, stderr.String()))...)
	}
	if writeErr := os.WriteFile(filepath.Join(dumpDir, filename), out, 0644); writeErr != nil {
		t.Logf("DumpLogsOnFailure: write %s: %v", filename, writeErr)
	}
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
