// Copyright 2025 Philipp Hossner
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0

//go:build e2e

package e2e

import (
	"bytes"
	"context"
	"fmt"
	"os/exec"
	"strings"
	"testing"
)

// firstHAProxyPodName returns the name of an arbitrary HAProxy pod via the
// chart's standard component label. The chart deploys multiple replicas so
// we just pick one — for the spoa-hub config delivery tests, every replica
// receives the same dataplane API push, so any pod is representative.
func firstHAProxyPodName(ctx context.Context, t *testing.T) string {
	t.Helper()
	cmd := exec.CommandContext(ctx, "kubectl",
		"--kubeconfig", kubeconfigPath,
		"-n", ControllerNamespace,
		"get", "pods",
		"-l", LabelSelectorHAProxy,
		"-o", "jsonpath={.items[0].metadata.name}",
	)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		t.Fatalf("kubectl get haproxy pods: %v\nstderr: %s", err, stderr.String())
	}
	name := strings.TrimSpace(stdout.String())
	if name == "" {
		t.Fatalf("no HAProxy pod matched selector %q", LabelSelectorHAProxy)
	}
	return name
}

// readFileFromHAProxyPod runs `kubectl exec` against the haproxy container
// of a HAProxy pod and returns the file content. Used to verify the
// dataplane API push of /etc/haproxy/general/spoa-hub-config.toml.
//
// The haproxy container's image has busybox `cat`, so we don't need any
// debug-image trickery.
func readFileFromHAProxyPod(ctx context.Context, podName, path string) (string, error) {
	cmd := exec.CommandContext(ctx, "kubectl",
		"--kubeconfig", kubeconfigPath,
		"-n", ControllerNamespace,
		"exec", podName, "-c", "haproxy", "--",
		"cat", path,
	)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		// Distinguish "file not yet created" (cat exits 1) from real
		// errors so the caller's WaitForCondition can keep polling
		// instead of giving up.
		if strings.Contains(stderr.String(), "No such file or directory") {
			return "", nil
		}
		return "", fmt.Errorf("kubectl exec cat %s: %w (stderr: %s)", path, err, stderr.String())
	}
	return stdout.String(), nil
}

// readSPOAHubLogs returns the recent log output from the spoa-hub
// container of a HAProxy pod. Used to verify the hub processed a config
// reload event after the controller pushed an updated file.
func readSPOAHubLogs(ctx context.Context, podName string) (string, error) {
	cmd := exec.CommandContext(ctx, "kubectl",
		"--kubeconfig", kubeconfigPath,
		"-n", ControllerNamespace,
		"logs", podName, "-c", "spoa-hub", "--tail=500",
	)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("kubectl logs spoa-hub: %w (stderr: %s)", err, stderr.String())
	}
	return stdout.String(), nil
}

// spoaHubLogsShowSuccessfulReload returns true if the recent hub logs
// contain a structured tracing event indicating a successful config
// reload. The hub binary (haproxy-spoa-hub MR1, see crates/hub/src/main.rs:376)
// logs "configuration reloaded successfully" once the new config has
// been parsed, plugins re-initialised, and the registry atomically
// swapped via arc_swap. That's the contract we depend on; matching
// either that phrase or the secondary "plugins reloaded" line
// (crates/hub/src/main.rs:400) gives us a stable test signal that
// survives minor log-format polish in the hub crate.
func spoaHubLogsShowSuccessfulReload(logs string) bool {
	return strings.Contains(logs, "configuration reloaded successfully") ||
		strings.Contains(logs, "plugins reloaded")
}
